package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type userContextKey struct{}

func (a *App) Handler() http.Handler {
	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeError(w, notFound()) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, errAPI(405, "method_not_allowed", "method not allowed"))
	})
	r.Get("/health", a.health)
	r.Post("/_test/reset", a.reset)
	r.Get("/_test/export", a.exportState)
	r.Post("/_test/import", a.importState)
	r.Post("/auth/signup", a.signup)
	r.Post("/auth/login", a.login)
	r.Get("/restaurants", a.listRestaurants)
	r.Get("/restaurants/{id}", a.restaurantDetail)
	r.Get("/availability", a.availability)
	r.Group(func(p chi.Router) {
		p.Use(a.requireAuth)
		p.Post("/reservations", a.createReservation)
		p.Get("/reservations", a.listReservations)
		p.Get("/reservations/{reference}", a.getReservation)
		p.Post("/reservations/{reference}/cancel", a.cancelReservation)
		p.Patch("/reservations/{reference}", a.patchReservation)
		p.Post("/reservation-moves", a.moveReservations)
	})
	return r
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if !a.ready || a.db.Ping(r.Context()) != nil {
		writeError(w, errAPI(503, "unavailable", "service is not ready"))
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		parts := strings.Split(header, " ")
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
			writeError(w, unauth())
			return
		}
		var userID string
		err := a.db.QueryRow(r.Context(), `SELECT user_id FROM auth_sessions WHERE token_hash=$1`, tokenHash(parts[1])).Scan(&userID)
		if err != nil {
			writeError(w, unauth())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, userID)))
	})
}

func currentUser(r *http.Request) string { return r.Context().Value(userContextKey{}).(string) }

func (a *App) signup(w http.ResponseWriter, r *http.Request) {
	m, _, err := decodeObject(r)
	if err != nil {
		writeError(w, err)
		return
	}
	email, _, err := strField(m, "email", true)
	if err != nil {
		writeError(w, err)
		return
	}
	password, _, err := strField(m, "password", true)
	if err != nil {
		writeError(w, err)
		return
	}
	display, _, err := strField(m, "display_name", true)
	if err != nil {
		writeError(w, err)
		return
	}
	email = strings.ToLower(email)
	if !emailRE.MatchString(email) || len(email) > 320 || len(password) < 8 || display == "" {
		writeError(w, validation("invalid signup fields"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, err)
		return
	}
	uid, err := randomID("u_", 18)
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := randomID("tok_", 32)
	if err != nil {
		writeError(w, err)
		return
	}
	sid, err := randomID("ses_", 18)
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
	var exists bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists); err != nil {
		writeError(w, err)
		return
	}
	if exists {
		writeError(w, errAPI(409, "email_taken", "email is already registered"))
		return
	}
	now := time.Now().UTC()
	if _, err = tx.Exec(r.Context(), `INSERT INTO users(id,email,password_hash,display_name,created_at) VALUES($1,$2,$3,$4,$5)`, uid, email, hash, display, now); err != nil {
		if strings.Contains(err.Error(), "users_email_key") {
			writeError(w, errAPI(409, "email_taken", "email is already registered"))
			return
		}
		writeError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO auth_sessions(id,user_id,token_hash,created_at) VALUES($1,$2,$3,$4)`, sid, uid, tokenHash(token), now); err != nil {
		writeError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"user_id": uid, "display_name": display, "token": token})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	m, _, err := decodeObject(r)
	if err != nil {
		writeError(w, err)
		return
	}
	email, _, err := strField(m, "email", true)
	if err != nil {
		writeError(w, err)
		return
	}
	password, _, err := strField(m, "password", true)
	if err != nil {
		writeError(w, err)
		return
	}
	var uid, display string
	var hash []byte
	err = a.db.QueryRow(r.Context(), `SELECT id,display_name,password_hash FROM users WHERE email=$1`, strings.ToLower(email)).Scan(&uid, &display, &hash)
	if err != nil || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		writeError(w, unauth())
		return
	}
	token, err := randomID("tok_", 32)
	if err != nil {
		writeError(w, err)
		return
	}
	sid, err := randomID("ses_", 18)
	if err != nil {
		writeError(w, err)
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO auth_sessions(id,user_id,token_hash,created_at) VALUES($1,$2,$3,$4)`, sid, uid, tokenHash(token), time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"user_id": uid, "display_name": display, "token": token})
}

func (a *App) listRestaurants(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT id,name,timezone FROM restaurants ORDER BY sort_order`)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, name, tz string
		if rows.Scan(&id, &name, &tz) != nil {
			writeError(w, rows.Err())
			return
		}
		out = append(out, map[string]string{"id": id, "name": name, "timezone": tz})
	}
	writeJSON(w, 200, map[string]any{"restaurants": out})
}

func (a *App) restaurantDetail(w http.ResponseWriter, r *http.Request) {
	rest, err := getRestaurant(r.Context(), a.db, chi.URLParam(r, "id"))
	if err == pgx.ErrNoRows {
		writeError(w, notFound())
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	hours := []OpeningHour{}
	rows, err := a.db.Query(r.Context(), `SELECT weekday,to_char(opens,'HH24:MI'),to_char(closes,'HH24:MI'),sort_order FROM restaurant_opening_hours WHERE restaurant_id=$1 ORDER BY sort_order`, rest.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	for rows.Next() {
		var h OpeningHour
		if err = rows.Scan(&h.Weekday, &h.Opens, &h.Closes, &h.SortOrder); err != nil {
			rows.Close()
			writeError(w, err)
			return
		}
		h.WeekdayName = weekdayNames[h.Weekday]
		hours = append(hours, h)
	}
	rows.Close()
	tables := []RestaurantTable{}
	rows, err = a.db.Query(r.Context(), `SELECT id,label,capacity,sort_order FROM restaurant_tables WHERE restaurant_id=$1 ORDER BY sort_order`, rest.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	for rows.Next() {
		var t RestaurantTable
		if err = rows.Scan(&t.ID, &t.Label, &t.Capacity, &t.SortOrder); err != nil {
			rows.Close()
			writeError(w, err)
			return
		}
		tables = append(tables, t)
	}
	rows.Close()
	writeJSON(w, 200, map[string]any{"id": rest.ID, "name": rest.Name, "timezone": rest.Timezone, "slot_minutes": rest.SlotMinutes, "reservation_duration_minutes": rest.DurationMinutes, "cancellation_cutoff_minutes": rest.CutoffMinutes, "opening_hours": hours, "tables": tables})
}

func (a *App) availability(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rid, date := q.Get("restaurant_id"), q.Get("date")
	party, err := parsePlainInt(q.Get("party_size"), "party_size")
	if err != nil {
		writeError(w, err)
		return
	}
	if rid == "" || date == "" {
		writeError(w, validation("restaurant_id, date and party_size are required"))
		return
	}
	if err = validateID(rid, "restaurant_id"); err != nil || party < 1 {
		if err == nil {
			err = validation("party_size must be at least 1")
		}
		writeError(w, err)
		return
	}
	rest, err := getRestaurant(r.Context(), a.db, rid)
	if err == pgx.ErrNoRows {
		writeError(w, notFound())
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	loc, err := time.LoadLocation(rest.Timezone)
	if err != nil {
		writeError(w, validation("invalid restaurant timezone"))
		return
	}
	if !dateRE.MatchString(date) {
		writeError(w, validation("date must be YYYY-MM-DD"))
		return
	}
	day, err := time.Parse("2006-01-02", date)
	if err != nil || day.Format("2006-01-02") != date {
		writeError(w, validation("invalid date"))
		return
	}
	h, err := getHours(r.Context(), a.db, rid, int(day.Weekday()))
	if err == pgx.ErrNoRows {
		writeJSON(w, 200, map[string]any{"restaurant_id": rid, "date": date, "timezone": rest.Timezone, "slots": []any{}})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	open, _ := wallMinutes(h.Opens)
	closeM, _ := wallMinutes(h.Closes)
	slots := []map[string]any{}
	for minute := open; minute+rest.DurationMinutes <= closeM; minute += rest.SlotMinutes {
		local := date + "T" + time.Date(2000, 1, 1, minute/60, minute%60, 0, 0, time.UTC).Format("15:04")
		start, e := parseLocal(local, loc)
		if e != nil {
			continue
		}
		end := start.Add(time.Duration(rest.DurationMinutes) * time.Minute)
		rows, e := a.db.Query(r.Context(), `SELECT t.id FROM restaurant_tables t WHERE t.restaurant_id=$1 AND t.capacity >= $2 AND NOT EXISTS (SELECT 1 FROM reservation_occupancies o WHERE o.restaurant_id=t.restaurant_id AND o.table_id=t.id AND o.conflict_range && tstzrange($3,$4,'[)')) ORDER BY t.sort_order`, rid, party, start, end)
		if e != nil {
			writeError(w, e)
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				writeError(w, e)
				return
			}
			ids = append(ids, id)
		}
		rows.Close()
		slots = append(slots, map[string]any{"starts_at_local": local, "starts_at": formatTime(start), "available_table_ids": ids})
	}
	writeJSON(w, 200, map[string]any{"restaurant_id": rid, "date": date, "timezone": rest.Timezone, "slots": slots})
}
