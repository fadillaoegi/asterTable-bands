package app

import (
	"testing"
	"time"
)

func TestParseLocalDST(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if _, err := parseLocal("2026-03-29T02:30", berlin); err == nil {
		t.Fatal("spring gap accepted")
	}
	got, err := parseLocal("2026-10-25T02:30", berlin)
	if err != nil {
		t.Fatal(err)
	}
	_, off := got.Zone()
	if off != 2*3600 {
		t.Fatalf("fall overlap offset=%d, want +02:00", off)
	}
	end := got.Add(90 * time.Minute)
	if end.Format("15:04 -07:00") != "03:00 +01:00" {
		t.Fatalf("absolute duration end %s", end.Format("15:04 -07:00"))
	}

	ny, _ := time.LoadLocation("America/New_York")
	if _, err := parseLocal("2026-03-08T02:15", ny); err == nil {
		t.Fatal("NY spring gap accepted")
	}
	got, err = parseLocal("2026-11-01T01:30", ny)
	if err != nil {
		t.Fatal(err)
	}
	_, off = got.Zone()
	if off != -4*3600 {
		t.Fatalf("fall overlap offset=%d, want -04:00", off)
	}
}

func TestReservationResponseUsesRestaurantOffsetAfterDatabaseReload(t *testing.T) {
	start := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	r := Reservation{
		StartsAt: start, EndsAt: start.Add(90 * time.Minute),
		CreatedAt: start, Timezone: "Europe/Berlin",
	}
	got := r.response()
	if got["starts_at"] != "2026-10-25T02:30:00+02:00" {
		t.Fatalf("starts_at = %v", got["starts_at"])
	}
	if got["ends_at"] != "2026-10-25T03:00:00+01:00" {
		t.Fatalf("ends_at = %v", got["ends_at"])
	}
}

func TestParseLocalUsesHistoricalIANAOffset(t *testing.T) {
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseLocal("1938-01-03T12:00", amsterdam)
	if err != nil {
		t.Fatal(err)
	}
	// Host zoneinfo releases disagree on whether pre-1940 Amsterdam aliases
	// Brussels here. The resolver must follow the loaded database exactly.
	want := time.Date(1938, 1, 3, 12, 0, 0, 0, amsterdam)
	if !got.Equal(want) || got.Format("15:04") != "12:00" {
		t.Fatalf("historical time = %s, want %s", got, want)
	}
	end := got.Add(90 * time.Minute)
	if end.Sub(got) != 90*time.Minute || end.In(amsterdam).Format("15:04") != "13:30" {
		t.Fatalf("historical absolute end = %s", end.Format("2006-01-02T15:04:05-07:00"))
	}

	// Exercise the exact non-whitelisted +00:20 offset independently of the
	// host's Amsterdam alias choice.
	plusTwenty := time.FixedZone("historical-plus-twenty", 20*60)
	got, err = parseLocal("1938-01-03T12:00", plusTwenty)
	if err != nil || got.Format("2006-01-02T15:04:05-07:00") != "1938-01-03T12:00:00+00:20" {
		t.Fatalf("+00:20 time = %s, err=%v", got.Format("2006-01-02T15:04:05-07:00"), err)
	}
}
