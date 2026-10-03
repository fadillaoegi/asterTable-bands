package app

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func errAPI(status int, code, message string) *apiError {
	return &apiError{Status: status, Code: code, Message: message}
}

var (
	malformed  = func(msg string) *apiError { return errAPI(400, "malformed_request", msg) }
	validation = func(msg string) *apiError { return errAPI(422, "validation_failed", msg) }
	unauth     = func() *apiError { return errAPI(401, "unauthenticated", "valid bearer authentication is required") }
	notFound   = func() *apiError { return errAPI(404, "not_found", "resource not found") }
	localRE    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$`)
	dateRE     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	emailRE    = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e error) {
	var ae *apiError
	if !errors.As(e, &ae) {
		ae = malformed("request could not be completed")
	}
	writeJSON(w, ae.Status, map[string]any{"error": map[string]string{"code": ae.Code, "message": ae.Message}})
}

func decodeObject(r *http.Request) (map[string]json.RawMessage, json.RawMessage, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return nil, nil, malformed("invalid JSON body")
	}
	var anyValue any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&anyValue); err != nil {
		return nil, nil, malformed("invalid JSON body")
	}
	if err := ensureEOF(dec); err != nil {
		return nil, nil, malformed("invalid JSON body")
	}
	if _, ok := anyValue.(map[string]any); !ok {
		return nil, nil, malformed("JSON body must be an object")
	}
	canonical, _ := json.Marshal(anyValue)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(canonical, &fields)
	return fields, canonical, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func strField(m map[string]json.RawMessage, key string, required bool) (string, bool, error) {
	raw, ok := m[key]
	if !ok {
		if required {
			return "", false, validation(key + " is required")
		}
		return "", false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", true, malformed(key + " must be a string")
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return "", true, malformed(key + " must be a string")
	}
	return s, true, nil
}

func intField(m map[string]json.RawMessage, key string, required, endpointSpecial bool) (int, bool, error) {
	raw, ok := m[key]
	if !ok {
		if required {
			return 0, false, validation(key + " is required")
		}
		return 0, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !((trimmed[0] >= '0' && trimmed[0] <= '9') || trimmed[0] == '-') {
		if endpointSpecial {
			return 0, true, validation(key + " must be an integer")
		}
		return 0, true, malformed(key + " must be an integer")
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		if endpointSpecial {
			return 0, true, validation(key + " must be an integer")
		}
		return 0, true, malformed(key + " must be an integer")
	}
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		if endpointSpecial {
			return 0, true, validation(key + " must be an integer")
		}
		return 0, true, malformed(key + " must be an integer")
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		if endpointSpecial {
			return 0, true, validation(key + " must be an integer")
		}
		return 0, true, malformed(key + " must be an integer")
	}
	return i, true, nil
}

func parsePlainInt(s, name string) (int, error) {
	if s == "" {
		return 0, validation(name + " is required")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, validation(name + " must be plain decimal digits")
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, validation(name + " is out of range")
	}
	return n, nil
}

func validateID(s, name string) error {
	if s == "" || characterCount(s) > 64 {
		return validation(name + " must contain 1 to 64 characters")
	}
	return nil
}

func randomID(prefix string, bytesN int) (string, error) {
	b := make([]byte, bytesN)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func randomReference() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 8)
	r := make([]byte, 8)
	if _, err := rand.Read(r); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(r[i])%len(alphabet)]
	}
	return string(b), nil
}

func tokenHash(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }

// bcrypt rejects inputs longer than 72 bytes. Pre-hashing gives every
// password a fixed-size bcrypt input while retaining bcrypt's salt and cost.
func passwordMaterial(password string) []byte {
	h := sha256.Sum256([]byte(password))
	return h[:]
}

func formatTime(t time.Time) string { return t.Format("2006-01-02T15:04:05-07:00") }

func characterCount(s string) int { return utf8.RuneCountInString(s) }

type Restaurant struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Timezone        string `json:"timezone"`
	SlotMinutes     int    `json:"slot_minutes"`
	DurationMinutes int    `json:"reservation_duration_minutes"`
	CutoffMinutes   int    `json:"cancellation_cutoff_minutes"`
	SortOrder       int    `json:"-"`
}

type OpeningHour struct {
	Weekday     int    `json:"-"`
	WeekdayName string `json:"weekday"`
	Opens       string `json:"opens"`
	Closes      string `json:"closes"`
	SortOrder   int    `json:"-"`
}

type RestaurantTable struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Capacity  int    `json:"capacity"`
	SortOrder int    `json:"-"`
}

type Reservation struct {
	ID            string
	Reference     string
	UserID        string
	RestaurantID  string
	TableID       string
	PartySize     int
	Status        string
	StartsAtLocal string
	StartsAt      time.Time
	EndsAt        time.Time
	CreatedAt     time.Time
	Timezone      string
}

func (r Reservation) response() map[string]any {
	start, end := r.StartsAt, r.EndsAt
	if loc, err := time.LoadLocation(r.Timezone); err == nil {
		start, end = start.In(loc), end.In(loc)
	}
	return map[string]any{
		"reservation_id": r.ID, "reference": r.Reference,
		"restaurant_id": r.RestaurantID, "table_id": r.TableID,
		"party_size": r.PartySize, "status": r.Status,
		"starts_at_local": r.StartsAtLocal, "starts_at": formatTime(start),
		"ends_at": formatTime(end), "created_at": formatTime(r.CreatedAt.UTC()),
	}
}

func parseLocal(local string, loc *time.Location) (time.Time, error) {
	if !localRE.MatchString(local) {
		return time.Time{}, validation("starts_at_local must be YYYY-MM-DDTHH:MM")
	}
	wall, err := time.Parse("2006-01-02T15:04", local)
	if err != nil || wall.Format("2006-01-02T15:04") != local {
		return time.Time{}, validation("invalid local date or time")
	}
	matches := localDay(loc, wall)[wall.Format("15:04")]
	if len(matches) == 0 {
		return time.Time{}, errAPI(422, "invalid_local_time", "local time does not exist")
	}
	// localDay scans UTC in ascending order, so index zero is the first
	// occurrence when a clock change repeats a local time.
	return matches[0].In(loc), nil
}

// A location's rules are the source of truth for valid wall times. Indexing a
// local calendar day by walking the bounded UTC interval avoids assumptions
// about the shape of historical offsets (some include unusual minutes or
// seconds), detects gaps, and retains both occurrences of folds.
var localDayCache = struct {
	sync.Mutex
	days map[string]map[string][]time.Time
}{days: make(map[string]map[string][]time.Time)}

func localDay(loc *time.Location, wall time.Time) map[string][]time.Time {
	key := loc.String() + "\x00" + wall.Format("2006-01-02")
	localDayCache.Lock()
	defer localDayCache.Unlock()
	if day, ok := localDayCache.days[key]; ok {
		return day
	}

	day := make(map[string][]time.Time, 24*60)
	base := time.Date(wall.Year(), wall.Month(), wall.Day(), 0, 0, 0, 0, time.UTC)
	// All IANA UTC offsets are within 24 hours. The margin also covers
	// historical date-line transitions while keeping the scan bounded.
	for instant, end := base.Add(-26*time.Hour), base.Add(50*time.Hour); instant.Before(end); instant = instant.Add(time.Second) {
		zoned := instant.In(loc)
		if zoned.Year() != wall.Year() || zoned.Month() != wall.Month() || zoned.Day() != wall.Day() || zoned.Second() != 0 {
			continue
		}
		minute := zoned.Format("15:04")
		day[minute] = append(day[minute], instant)
	}
	// Bound memory under arbitrary-date traffic. Eviction is intentionally
	// simple because entries are immutable and inexpensive to rebuild.
	if len(localDayCache.days) >= 128 {
		for old := range localDayCache.days {
			delete(localDayCache.days, old)
			break
		}
	}
	localDayCache.days[key] = day
	return day
}

func wallMinutes(hhmm string) (int, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil || t.Format("15:04") != hhmm {
		return 0, fmt.Errorf("invalid HH:MM")
	}
	return t.Hour()*60 + t.Minute(), nil
}

var weekdayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
var weekdayIndex = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
