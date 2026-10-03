package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func idempotencyKey(r *http.Request) (string, error) {
	k := r.Header.Get("Idempotency-Key")
	if k == "" {
		return "", errAPI(400, "missing_idempotency_key", "Idempotency-Key is required")
	}
	if characterCount(k) > 255 {
		return "", validation("Idempotency-Key must be 1 to 255 characters")
	}
	return k, nil
}

func lockIdempotency(ctx context.Context, tx pgx.Tx, user, key string) error {
	// PostgreSQL text cannot contain NUL bytes. JSON array encoding is an
	// unambiguous, stable pairing of the user and client key for this lock.
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, idempotencyLockMaterial(user, key))
	return err
}

func idempotencyLockMaterial(user, key string) string {
	b, _ := json.Marshal([]string{user, key})
	return string(b)
}

func replayReceipt(ctx context.Context, tx pgx.Tx, user, key, method, path string, body []byte) (json.RawMessage, bool, error) {
	var same bool
	var response []byte
	err := tx.QueryRow(ctx, `SELECT request_body=$5::jsonb,response_body FROM idempotency_receipts WHERE user_id=$1 AND key=$2 AND method=$3 AND path=$4`, user, key, method, path, body).Scan(&same, &response)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !same {
		return nil, true, errAPI(409, "idempotency_key_reuse", "key was used with a different request body")
	}
	return json.RawMessage(response), true, nil
}

func saveReceipt(ctx context.Context, tx pgx.Tx, user, key, method, path string, body, response []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO idempotency_receipts(user_id,key,method,path,request_body,response_body,created_at) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7)`, user, key, method, path, body, response, time.Now().UTC())
	return err
}

type reservationInput struct {
	RestaurantID, TableID, StartsLocal string
	PartySize                          int
}

func parseCreateInput(m map[string]json.RawMessage) (reservationInput, error) {
	var in reservationInput
	var err error
	in.RestaurantID, _, err = strField(m, "restaurant_id", true)
	if err != nil {
		return in, err
	}
	in.TableID, _, err = strField(m, "table_id", true)
	if err != nil {
		return in, err
	}
	in.StartsLocal, _, err = strField(m, "starts_at_local", true)
	if err != nil {
		return in, err
	}
	in.PartySize, _, err = intField(m, "party_size", true, true)
	if err != nil {
		return in, err
	}
	if err = validateID(in.RestaurantID, "restaurant_id"); err != nil {
		return in, err
	}
	if err = validateID(in.TableID, "table_id"); err != nil {
		return in, err
	}
	if in.PartySize < 1 {
		return in, validation("party_size must be at least 1")
	}
	if !localRE.MatchString(in.StartsLocal) {
		return in, validation("starts_at_local must be bare YYYY-MM-DDTHH:MM")
	}
	return in, nil
}

func validateReservationTarget(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, in reservationInput) (Restaurant, time.Time, time.Time, error) {
	rest, err := getRestaurant(ctx, q, in.RestaurantID)
	if err == pgx.ErrNoRows {
		return rest, time.Time{}, time.Time{}, notFound()
	}
	if err != nil {
		return rest, time.Time{}, time.Time{}, err
	}
	table, err := getTable(ctx, q, in.RestaurantID, in.TableID)
	if err == pgx.ErrNoRows {
		return rest, time.Time{}, time.Time{}, notFound()
	}
	if err != nil {
		return rest, time.Time{}, time.Time{}, err
	}
	loc, err := time.LoadLocation(rest.Timezone)
	if err != nil {
		return rest, time.Time{}, time.Time{}, validation("invalid restaurant timezone")
	}
	start, err := parseLocal(in.StartsLocal, loc)
	if err != nil {
		return rest, time.Time{}, time.Time{}, err
	}
	wall, err := time.Parse("2006-01-02T15:04", in.StartsLocal)
	if err != nil {
		return rest, time.Time{}, time.Time{}, validation("invalid local time")
	}
	h, err := getHours(ctx, q, in.RestaurantID, int(wall.Weekday()))
	if err == pgx.ErrNoRows {
		return rest, time.Time{}, time.Time{}, errAPI(422, "outside_opening_hours", "restaurant is closed")
	}
	if err != nil {
		return rest, time.Time{}, time.Time{}, err
	}
	open, _ := wallMinutes(h.Opens)
	closeM, _ := wallMinutes(h.Closes)
	minute := wall.Hour()*60 + wall.Minute()
	if minute < open || minute+rest.DurationMinutes > closeM {
		return rest, time.Time{}, time.Time{}, errAPI(422, "outside_opening_hours", "reservation is outside opening hours")
	}
	if (minute-open)%rest.SlotMinutes != 0 {
		return rest, time.Time{}, time.Time{}, errAPI(422, "not_on_slot_grid", "start is not on the slot grid")
	}
	if in.PartySize > table.Capacity {
		return rest, time.Time{}, time.Time{}, errAPI(422, "party_exceeds_capacity", "party exceeds table capacity")
	}
	return rest, start, start.Add(time.Duration(rest.DurationMinutes) * time.Minute), nil
}

func databaseWriteError(err error) error {
	var pe *pgconn.PgError
	if err != nil && strings.Contains(err.Error(), "reservation_occupancies_no_overlap") {
		return errAPI(409, "table_unavailable", "table is unavailable")
	}
	if err != nil && errors.As(err, &pe) && pe.Code == "23P01" {
		return errAPI(409, "table_unavailable", "table is unavailable")
	}
	return err
}

func (a *App) createReservation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	m, body, err := decodeObject(r)
	if err != nil {
		writeError(w, err)
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = lockIdempotency(r.Context(), tx, user, key); err != nil {
		writeError(w, err)
		return
	}
	if response, found, e := replayReceipt(r.Context(), tx, user, key, r.Method, r.URL.Path, body); found {
		if e != nil {
			writeError(w, e)
		} else {
			writeJSON(w, 200, response)
		}
		return
	}
	in, err := parseCreateInput(m)
	if err != nil {
		writeError(w, err)
		return
	}
	rest, start, end, err := validateReservationTarget(r.Context(), tx, in)
	if err != nil {
		writeError(w, err)
		return
	}
	id, err := randomID("res_", 18)
	if err != nil {
		writeError(w, err)
		return
	}
	var ref string
	for i := 0; i < 8; i++ {
		ref, err = randomReference()
		if err != nil {
			writeError(w, err)
			return
		}
		var exists bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM reservations WHERE reference=$1)`, ref).Scan(&exists); err != nil {
			writeError(w, err)
			return
		}
		if !exists {
			break
		}
		ref = ""
	}
	if ref == "" {
		writeError(w, malformed("could not allocate reference"))
		return
	}
	now := time.Now().UTC()
	_, err = tx.Exec(r.Context(), `INSERT INTO reservations(id,reference,user_id,restaurant_id,table_id,party_size,status,starts_at_local,starts_at,ends_at,created_at) VALUES($1,$2,$3,$4,$5,$6,'confirmed',$7,$8,$9,$10)`, id, ref, user, in.RestaurantID, in.TableID, in.PartySize, in.StartsLocal, start, end, now)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO reservation_occupancies(reservation_id,restaurant_id,table_id,conflict_range) VALUES($1,$2,$3,tstzrange($4,$5,'[)'))`, id, in.RestaurantID, in.TableID, start, end)
	}
	if err != nil {
		writeError(w, databaseWriteError(err))
		return
	}
	res := Reservation{ID: id, Reference: ref, UserID: user, RestaurantID: in.RestaurantID, TableID: in.TableID, PartySize: in.PartySize, Status: "confirmed", StartsAtLocal: in.StartsLocal, StartsAt: start, EndsAt: end, CreatedAt: now, Timezone: rest.Timezone}
	response, _ := json.Marshal(res.response())
	if err = saveReceipt(r.Context(), tx, user, key, r.Method, r.URL.Path, body, response); err != nil {
		writeError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, databaseWriteError(err))
		return
	}
	writeJSON(w, 201, json.RawMessage(response))
}

func (a *App) listReservations(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT `+reservationCols+` FROM reservations WHERE user_id=$1 ORDER BY starts_at DESC`, currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		res, err := scanReservation(rows)
		if err != nil {
			writeError(w, err)
			return
		}
		out = append(out, res.response())
	}
	writeJSON(w, 200, map[string]any{"reservations": out})
}

func (a *App) getReservation(w http.ResponseWriter, r *http.Request) {
	res, err := getReservationByReference(r.Context(), a.db, chi.URLParam(r, "reference"), currentUser(r), false)
	if err == pgx.ErrNoRows {
		writeError(w, notFound())
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, res.response())
}

func cutoffPassed(res Reservation, rest Restaurant) bool {
	return !time.Now().UTC().Before(res.StartsAt.Add(-time.Duration(rest.CutoffMinutes) * time.Minute))
}

func (a *App) cancelReservation(w http.ResponseWriter, r *http.Request) {
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	res, err := getReservationByReference(r.Context(), tx, chi.URLParam(r, "reference"), currentUser(r), true)
	if err == pgx.ErrNoRows {
		writeError(w, notFound())
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if res.Status == "cancelled" {
		writeJSON(w, 200, res.response())
		return
	}
	rest, err := getRestaurant(r.Context(), tx, res.RestaurantID)
	if err != nil {
		writeError(w, err)
		return
	}
	if cutoffPassed(res, rest) {
		writeError(w, errAPI(409, "cutoff_passed", "cancellation cutoff has passed"))
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE reservations SET status='cancelled' WHERE id=$1`, res.ID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE reservation_occupancies SET conflict_range=NULL WHERE reservation_id=$1`, res.ID)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	res.Status = "cancelled"
	writeJSON(w, 200, res.response())
}

type amendment struct {
	TableID, StartsLocal          string
	PartySize                     int
	HasTable, HasStarts, HasParty bool
}

func parseAmendment(m map[string]json.RawMessage) (amendment, error) {
	var x amendment
	var err error
	x.TableID, x.HasTable, err = strField(m, "table_id", false)
	if err != nil {
		return x, err
	}
	x.StartsLocal, x.HasStarts, err = strField(m, "starts_at_local", false)
	if err != nil {
		return x, err
	}
	x.PartySize, x.HasParty, err = intField(m, "party_size", false, true)
	if err != nil {
		return x, err
	}
	if x.HasTable {
		if err = validateID(x.TableID, "table_id"); err != nil {
			return x, err
		}
	}
	if x.HasStarts && !localRE.MatchString(x.StartsLocal) {
		return x, validation("starts_at_local must be bare YYYY-MM-DDTHH:MM")
	}
	if x.HasParty && x.PartySize < 1 {
		return x, validation("party_size must be at least 1")
	}
	return x, nil
}
func amendedInput(res Reservation, x amendment) reservationInput {
	in := reservationInput{res.RestaurantID, res.TableID, res.StartsAtLocal, res.PartySize}
	if x.HasTable {
		in.TableID = x.TableID
	}
	if x.HasStarts {
		in.StartsLocal = x.StartsLocal
	}
	if x.HasParty {
		in.PartySize = x.PartySize
	}
	return in
}

func (a *App) patchReservation(w http.ResponseWriter, r *http.Request) {
	m, _, err := decodeObject(r)
	if err != nil {
		writeError(w, err)
		return
	}
	x, err := parseAmendment(m)
	if err != nil {
		writeError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	res, err := getReservationByReference(r.Context(), tx, chi.URLParam(r, "reference"), currentUser(r), true)
	if err == pgx.ErrNoRows {
		writeError(w, notFound())
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	rest, err := getRestaurant(r.Context(), tx, res.RestaurantID)
	if err != nil {
		writeError(w, err)
		return
	}
	if res.Status == "cancelled" {
		writeError(w, errAPI(409, "reservation_cancelled", "reservation is cancelled"))
		return
	}
	if cutoffPassed(res, rest) {
		writeError(w, errAPI(409, "cutoff_passed", "amendment cutoff has passed"))
		return
	}
	in := amendedInput(res, x)
	_, start, end, err := validateReservationTarget(r.Context(), tx, in)
	if err != nil {
		writeError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE reservations SET table_id=$2,party_size=$3,starts_at_local=$4,starts_at=$5,ends_at=$6 WHERE id=$1`, res.ID, in.TableID, in.PartySize, in.StartsLocal, start, end)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE reservation_occupancies SET table_id=$2,conflict_range=tstzrange($3,$4,'[)') WHERE reservation_id=$1`, res.ID, in.TableID, start, end)
	}
	if err != nil {
		writeError(w, databaseWriteError(err))
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, databaseWriteError(err))
		return
	}
	res.TableID = in.TableID
	res.PartySize = in.PartySize
	res.StartsAtLocal = in.StartsLocal
	res.StartsAt = start
	res.EndsAt = end
	writeJSON(w, 200, res.response())
}
