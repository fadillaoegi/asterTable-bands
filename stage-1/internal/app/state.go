package app

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type resetFixture struct {
	Users []struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	} `json:"users"`
	Restaurants []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Timezone        string `json:"timezone"`
		SlotMinutes     int    `json:"slot_minutes"`
		DurationMinutes int    `json:"reservation_duration_minutes"`
		CutoffMinutes   int    `json:"cancellation_cutoff_minutes"`
		OpeningHours    []struct {
			Weekday string `json:"weekday"`
			Opens   string `json:"opens"`
			Closes  string `json:"closes"`
		} `json:"opening_hours"`
		Tables []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Capacity int    `json:"capacity"`
		} `json:"tables"`
	} `json:"restaurants"`
	Reservations []struct {
		ID            string `json:"id"`
		Reference     string `json:"reference"`
		UserID        string `json:"user_id"`
		RestaurantID  string `json:"restaurant_id"`
		TableID       string `json:"table_id"`
		StartsAtLocal string `json:"starts_at_local"`
		PartySize     int    `json:"party_size"`
	} `json:"reservations"`
}

var referenceRE = regexp.MustCompile(`^[A-Z0-9]{6,12}$`)

func (a *App) reset(w http.ResponseWriter, r *http.Request) {
	_, body, err := decodeObject(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var fixture resetFixture
	if json.Unmarshal(body, &fixture) != nil {
		writeError(w, malformed("fixture fields have invalid types"))
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `TRUNCATE idempotency_receipts,reservation_occupancies,reservations,restaurant_tables,restaurant_opening_hours,restaurants,auth_sessions,users CASCADE`)
	if err != nil {
		writeError(w, err)
		return
	}
	now := time.Now().UTC()
	for _, u := range fixture.Users {
		if validateID(u.ID, "user id") != nil || !emailRE.MatchString(strings.ToLower(u.Email)) || characterCount(u.Password) < 8 || u.DisplayName == "" {
			writeError(w, validation("invalid fixture user"))
			return
		}
		hash, e := bcrypt.GenerateFromPassword(passwordMaterial(u.Password), bcrypt.DefaultCost)
		if e != nil {
			writeError(w, e)
			return
		}
		_, e = tx.Exec(r.Context(), `INSERT INTO users(id,email,password_hash,display_name,created_at) VALUES($1,$2,$3,$4,$5)`, u.ID, strings.ToLower(u.Email), hash, u.DisplayName, now)
		if e != nil {
			writeError(w, validation("invalid or duplicate fixture user"))
			return
		}
	}
	for ri, rest := range fixture.Restaurants {
		if validateID(rest.ID, "restaurant id") != nil || rest.Name == "" || rest.SlotMinutes < 1 || rest.DurationMinutes < 1 || rest.CutoffMinutes < 0 {
			writeError(w, validation("invalid fixture restaurant"))
			return
		}
		if _, e := time.LoadLocation(rest.Timezone); e != nil {
			writeError(w, validation("invalid restaurant timezone"))
			return
		}
		_, e := tx.Exec(r.Context(), `INSERT INTO restaurants(id,name,timezone,slot_minutes,reservation_duration_minutes,cancellation_cutoff_minutes,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7)`, rest.ID, rest.Name, rest.Timezone, rest.SlotMinutes, rest.DurationMinutes, rest.CutoffMinutes, ri)
		if e != nil {
			writeError(w, validation("invalid or duplicate restaurant"))
			return
		}
		for hi, h := range rest.OpeningHours {
			weekday, ok := weekdayIndex[h.Weekday]
			open, e1 := wallMinutes(h.Opens)
			closeM, e2 := wallMinutes(h.Closes)
			if !ok || e1 != nil || e2 != nil || closeM <= open {
				writeError(w, validation("invalid opening hours"))
				return
			}
			_, e := tx.Exec(r.Context(), `INSERT INTO restaurant_opening_hours(restaurant_id,weekday,opens,closes,sort_order) VALUES($1,$2,$3::time,$4::time,$5)`, rest.ID, weekday, h.Opens, h.Closes, hi)
			if e != nil {
				writeError(w, validation("duplicate opening weekday"))
				return
			}
		}
		for ti, t := range rest.Tables {
			if validateID(t.ID, "table id") != nil || t.Label == "" || t.Capacity < 1 {
				writeError(w, validation("invalid fixture table"))
				return
			}
			_, e := tx.Exec(r.Context(), `INSERT INTO restaurant_tables(id,restaurant_id,label,capacity,sort_order) VALUES($1,$2,$3,$4,$5)`, t.ID, rest.ID, t.Label, t.Capacity, ti)
			if e != nil {
				writeError(w, validation("invalid or duplicate fixture table"))
				return
			}
		}
	}
	for _, seed := range fixture.Reservations {
		if validateID(seed.ID, "reservation id") != nil || validateID(seed.UserID, "user id") != nil || !referenceRE.MatchString(seed.Reference) {
			writeError(w, validation("invalid seeded reservation identity"))
			return
		}
		in := reservationInput{seed.RestaurantID, seed.TableID, seed.StartsAtLocal, seed.PartySize}
		if _, e := parseCreateInput(map[string]json.RawMessage{"restaurant_id": mustJSON(in.RestaurantID), "table_id": mustJSON(in.TableID), "starts_at_local": mustJSON(in.StartsLocal), "party_size": mustJSON(in.PartySize)}); e != nil {
			writeError(w, e)
			return
		}
		_, start, end, e := validateReservationTarget(r.Context(), tx, in)
		if e != nil {
			writeError(w, e)
			return
		}
		_, e = tx.Exec(r.Context(), `INSERT INTO reservations(id,reference,user_id,restaurant_id,table_id,party_size,status,starts_at_local,starts_at,ends_at,created_at) VALUES($1,$2,$3,$4,$5,$6,'confirmed',$7,$8,$9,$10)`, seed.ID, seed.Reference, seed.UserID, in.RestaurantID, in.TableID, in.PartySize, in.StartsLocal, start, end, now)
		if e == nil {
			_, e = tx.Exec(r.Context(), `INSERT INTO reservation_occupancies(reservation_id,restaurant_id,table_id,conflict_range) VALUES($1,$2,$3,tstzrange($4,$5,'[)'))`, seed.ID, in.RestaurantID, in.TableID, start, end)
		}
		if e != nil {
			writeError(w, databaseWriteError(e))
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, databaseWriteError(err))
		return
	}
	w.WriteHeader(204)
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

var stateTables = []string{"users", "auth_sessions", "restaurants", "restaurant_opening_hours", "restaurant_tables", "reservations", "reservation_occupancies", "idempotency_receipts"}

func (a *App) exportState(w http.ResponseWriter, r *http.Request) {
	tx, err := a.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	state := map[string]json.RawMessage{}
	for _, table := range stateTables {
		var raw []byte
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(t)),'[]'::jsonb) FROM ` + table + ` t`
		if err = tx.QueryRow(r.Context(), query).Scan(&raw); err != nil {
			writeError(w, err)
			return
		}
		state[table] = raw
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"track": "tablekeeper", "format_version": 1, "state": state})
}

func (a *App) importState(w http.ResponseWriter, r *http.Request) {
	m, _, err := decodeObject(r)
	if err != nil {
		writeError(w, err)
		return
	}
	track, _, e := strField(m, "track", true)
	if e != nil {
		writeError(w, validation("invalid track"))
		return
	}
	version, _, e := intField(m, "format_version", true, false)
	if e != nil {
		writeError(w, validation("invalid format_version"))
		return
	}
	if track != "tablekeeper" || version != 1 {
		writeError(w, validation("unsupported export format"))
		return
	}
	stateRaw, ok := m["state"]
	if !ok {
		writeError(w, validation("state is required"))
		return
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(stateRaw, &state) != nil || state == nil {
		writeError(w, validation("invalid state"))
		return
	}
	for _, name := range stateTables {
		raw, ok := state[name]
		if !ok {
			writeError(w, validation("state is incomplete"))
			return
		}
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			writeError(w, validation("state table must be an array"))
			return
		}
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		writeError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `TRUNCATE idempotency_receipts,reservation_occupancies,reservations,restaurant_tables,restaurant_opening_hours,restaurants,auth_sessions,users CASCADE`); err != nil {
		writeError(w, err)
		return
	}
	for _, table := range stateTables {
		query := `INSERT INTO ` + table + ` SELECT * FROM jsonb_populate_recordset(NULL::` + table + `,$1::jsonb)`
		if _, err = tx.Exec(r.Context(), query, state[table]); err != nil {
			writeError(w, validation("invalid state"))
			return
		}
	}
	var invalid bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS (
        SELECT 1 FROM reservations r
        LEFT JOIN restaurant_tables t ON t.id=r.table_id
        LEFT JOIN reservation_occupancies o ON o.reservation_id=r.id
        WHERE t.restaurant_id IS DISTINCT FROM r.restaurant_id
           OR o.restaurant_id IS DISTINCT FROM r.restaurant_id
           OR o.table_id IS DISTINCT FROM r.table_id
           OR (r.status='confirmed' AND o.conflict_range IS DISTINCT FROM tstzrange(r.starts_at,r.ends_at,'[)'))
           OR (r.status='cancelled' AND o.conflict_range IS NOT NULL)
      )`).Scan(&invalid)
	if err != nil || invalid {
		writeError(w, validation("invalid state"))
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, validation("invalid state"))
		return
	}
	w.WriteHeader(204)
}
