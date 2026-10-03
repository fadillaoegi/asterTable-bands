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
