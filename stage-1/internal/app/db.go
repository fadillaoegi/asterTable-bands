package app

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type App struct {
	db    *pgxpool.Pool
	ready bool
}

func New(ctx context.Context, databaseURL string) (*App, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 60
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a := &App{db: db}
	if err := a.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	a.ready = true
	return a, nil
}

func (a *App) Close() { a.db.Close() }

func (a *App) migrate(ctx context.Context) error {
	if _, err := a.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var version int64
		if _, err := fmt.Sscanf(entry.Name(), "%d_", &version); err != nil || version < 1 {
			return fmt.Errorf("invalid migration name %q", entry.Name())
		}
		tx, err := a.db.Begin(ctx)
		if err != nil {
			return err
		}
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists)
		if err == nil && !exists {
			var sqlBytes []byte
			sqlBytes, err = migrationFS.ReadFile("migrations/" + entry.Name())
			if err == nil {
				_, err = tx.Exec(ctx, string(sqlBytes))
			}
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES($1,$2,$3)`, version, entry.Name(), time.Now().UTC())
			}
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func scanRestaurant(row pgx.Row) (Restaurant, error) {
	var r Restaurant
	err := row.Scan(&r.ID, &r.Name, &r.Timezone, &r.SlotMinutes, &r.DurationMinutes, &r.CutoffMinutes, &r.SortOrder)
	return r, err
}

func getRestaurant(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (Restaurant, error) {
	return scanRestaurant(q.QueryRow(ctx, `SELECT id,name,timezone,slot_minutes,reservation_duration_minutes,cancellation_cutoff_minutes,sort_order FROM restaurants WHERE id=$1`, id))
}

func getHours(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, restaurantID string, weekday int) (OpeningHour, error) {
	var h OpeningHour
	err := q.QueryRow(ctx, `SELECT weekday,to_char(opens,'HH24:MI'),to_char(closes,'HH24:MI'),sort_order FROM restaurant_opening_hours WHERE restaurant_id=$1 AND weekday=$2`, restaurantID, weekday).Scan(&h.Weekday, &h.Opens, &h.Closes, &h.SortOrder)
	h.WeekdayName = weekdayNames[h.Weekday]
	return h, err
}

func getTable(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, restaurantID, id string) (RestaurantTable, error) {
	var t RestaurantTable
	err := q.QueryRow(ctx, `SELECT id,label,capacity,sort_order FROM restaurant_tables WHERE restaurant_id=$1 AND id=$2`, restaurantID, id).Scan(&t.ID, &t.Label, &t.Capacity, &t.SortOrder)
	return t, err
}

func scanReservation(row pgx.Row) (Reservation, error) {
	var r Reservation
	err := row.Scan(&r.ID, &r.Reference, &r.UserID, &r.RestaurantID, &r.TableID, &r.PartySize, &r.Status, &r.StartsAtLocal, &r.StartsAt, &r.EndsAt, &r.CreatedAt, &r.Timezone)
	return r, err
}

const reservationCols = `id,reference,user_id,restaurant_id,table_id,party_size,status,starts_at_local,starts_at,ends_at,created_at,(SELECT timezone FROM restaurants WHERE restaurants.id=reservations.restaurant_id)`

func getReservationByReference(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, ref, userID string, lock bool) (Reservation, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return scanReservation(q.QueryRow(ctx, `SELECT `+reservationCols+` FROM reservations WHERE reference=$1 AND user_id=$2`+suffix, ref, userID))
}
