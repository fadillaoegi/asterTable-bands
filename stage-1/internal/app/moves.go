package app

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
)

type rawMove struct {
	Reference string
	Fields    map[string]json.RawMessage
}

func parseMoveEnvelope(m map[string]json.RawMessage) ([]rawMove, error) {
	raw, ok := m["moves"]
	if !ok {
		return nil, validation("moves is required")
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, validation("moves must be an array")
	}
	if len(items) < 1 || len(items) > 8 {
		return nil, validation("moves must contain 1 to 8 items")
	}
	out := make([]rawMove, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &fields) != nil || fields == nil {
			return nil, validation("each move must be an object")
		}
		ref, ok, e := strField(fields, "reference", true)
		if e != nil || !ok || validateID(ref, "reference") != nil {
			return nil, validation("each move requires a valid reference")
		}
		if seen[ref] {
			return nil, validation("move references must be distinct")
		}
		seen[ref] = true
		out = append(out, rawMove{ref, fields})
	}
	return out, nil
}

func (a *App) moveReservations(w http.ResponseWriter, r *http.Request) {
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
	moves, err := parseMoveEnvelope(m)
	if err != nil {
		writeError(w, err)
		return
	}
	refs := make([]string, len(moves))
	for i, x := range moves {
		refs[i] = x.Reference
	}
	sorted := append([]string(nil), refs...)
	sort.Strings(sorted)
	locked := map[string]Reservation{}
	for _, ref := range sorted {
		res, e := getReservationByReference(r.Context(), tx, ref, user, true)
		if e == pgx.ErrNoRows {
			writeError(w, notFound())
			return
		}
		if e != nil {
			writeError(w, e)
			return
		}
		locked[ref] = res
	}
	restaurantID := locked[moves[0].Reference].RestaurantID
	for _, x := range moves {
		if locked[x.Reference].RestaurantID != restaurantID {
			writeError(w, validation("all reservations must be at the same restaurant"))
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `SET CONSTRAINTS reservation_occupancies_no_overlap DEFERRED`); err != nil {
		writeError(w, err)
		return
	}
	results := make([]Reservation, 0, len(moves))
	for _, move := range moves {
		res := locked[move.Reference]
		rest, e := getRestaurant(r.Context(), tx, res.RestaurantID)
		if e != nil {
			writeError(w, e)
			return
		}
		if cutoffPassed(res, rest) {
			writeError(w, errAPI(409, "cutoff_passed", "amendment cutoff has passed"))
			return
		}
		if res.Status == "cancelled" {
			writeError(w, errAPI(409, "reservation_cancelled", "reservation is cancelled"))
			return
		}
		x, e := parseAmendment(move.Fields)
		if e != nil {
			writeError(w, e)
			return
		}
		in := amendedInput(res, x)
		_, start, end, e := validateReservationTarget(r.Context(), tx, in)
		if e != nil {
			writeError(w, e)
			return
		}
		_, e = tx.Exec(r.Context(), `UPDATE reservations SET table_id=$2,party_size=$3,starts_at_local=$4,starts_at=$5,ends_at=$6 WHERE id=$1`, res.ID, in.TableID, in.PartySize, in.StartsLocal, start, end)
		if e == nil {
			_, e = tx.Exec(r.Context(), `UPDATE reservation_occupancies SET table_id=$2,conflict_range=tstzrange($3,$4,'[)') WHERE reservation_id=$1`, res.ID, in.TableID, start, end)
		}
		if e != nil {
			writeError(w, databaseWriteError(e))
			return
		}
		res.TableID = in.TableID
		res.PartySize = in.PartySize
		res.StartsAtLocal = in.StartsLocal
		res.StartsAt = start
		res.EndsAt = end
		results = append(results, res)
	}
	responseItems := make([]map[string]any, len(results))
	for i, res := range results {
		responseItems[i] = res.response()
	}
	response, _ := json.Marshal(map[string]any{"reservations": responseItems})
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
