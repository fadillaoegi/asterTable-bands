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
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
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
	if s == "" || len(s) > 64 {
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
}

func (r Reservation) response() map[string]any {
	return map[string]any{
		"reservation_id": r.ID, "reference": r.Reference,
		"restaurant_id": r.RestaurantID, "table_id": r.TableID,
		"party_size": r.PartySize, "status": r.Status,
		"starts_at_local": r.StartsAtLocal, "starts_at": formatTime(r.StartsAt),
		"ends_at": formatTime(r.EndsAt), "created_at": formatTime(r.CreatedAt.UTC()),
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
	// Search all plausible offsets and select the earliest instant: the first occurrence on fall-back.
	var matches []time.Time
	seen := map[int]bool{}
	for h := -14; h <= 14; h++ {
		for _, minute := range []int{0, 30, 45} {
			off := h * 3600
			if h >= 0 {
				off += minute * 60
			} else {
				off -= minute * 60
			}
			if seen[off] {
				continue
			}
			seen[off] = true
			candidate := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, time.FixedZone("candidate", off)).UTC()
			if candidate.In(loc).Format("2006-01-02T15:04") == local {
				matches = append(matches, candidate)
			}
		}
	}
	if len(matches) == 0 {
		return time.Time{}, errAPI(422, "invalid_local_time", "local time does not exist")
	}
	best := matches[0]
	for _, t := range matches[1:] {
		if t.Before(best) {
			best = t
		}
	}
	return best.In(loc), nil
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
