package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDecodeObjectCanonicalizesJSON(t *testing.T) {
	a := httptest.NewRequest("POST", "/", strings.NewReader(`{"b":2,"a":1}`))
	b := httptest.NewRequest("POST", "/", strings.NewReader(" { \"a\" : 1, \"b\": 2 } "))
	_, ca, err := decodeObject(a)
	if err != nil {
		t.Fatal(err)
	}
	_, cb, err := decodeObject(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("canonical values differ: %s / %s", ca, cb)
	}
}

func TestDecodeObjectRejectsMalformedAndWrongTopLevel(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `null`, `{"a":1} {"b":2}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if _, _, err := decodeObject(r); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

func TestEndpointSpecialIntegersAreValidationErrors(t *testing.T) {
	for _, raw := range []string{`"4"`, `true`, `4.0`, `1e2`, `-1`} {
		m := map[string]json.RawMessage{"party_size": json.RawMessage(raw)}
		n, _, err := intField(m, "party_size", true, true)
		if raw == `-1` {
			if err != nil || n != -1 {
				t.Fatalf("integer parsing %s: n=%d err=%v", raw, n, err)
			}
			continue
		}
		if err == nil || err.(*apiError).Code != "validation_failed" {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestParseCreateInputIgnoresUnknownAndChecksSpecialFields(t *testing.T) {
	m := map[string]json.RawMessage{
		"restaurant_id": json.RawMessage(`"r"`), "table_id": json.RawMessage(`"t"`),
		"starts_at_local": json.RawMessage(`"2030-01-02T18:00"`), "party_size": json.RawMessage(`2`),
		"future_field": json.RawMessage(`{"anything":true}`),
	}
	in, err := parseCreateInput(m)
	if err != nil || in.PartySize != 2 {
		t.Fatalf("input=%+v err=%v", in, err)
	}
	m["party_size"] = json.RawMessage(`false`)
	if _, err := parseCreateInput(m); err == nil || err.(*apiError).Code != "validation_failed" {
		t.Fatalf("wrong party type: %v", err)
	}
	m["party_size"] = json.RawMessage(`2`)
	m["starts_at_local"] = json.RawMessage(`"2030-01-02T18:00Z"`)
	if _, err := parseCreateInput(m); err == nil || err.(*apiError).Code != "validation_failed" {
		t.Fatalf("offset time: %v", err)
	}
}

func TestMoveEnvelopeBoundariesAndDuplicates(t *testing.T) {
	one := json.RawMessage(`[{"reference":"ABC123"}]`)
	if got, err := parseMoveEnvelope(map[string]json.RawMessage{"moves": one}); err != nil || len(got) != 1 {
		t.Fatalf("valid: %v %v", got, err)
	}
	duplicate := json.RawMessage(`[{"reference":"ABC123"},{"reference":"ABC123"}]`)
	if _, err := parseMoveEnvelope(map[string]json.RawMessage{"moves": duplicate}); err == nil {
		t.Fatal("duplicate accepted")
	}
	tooMany := `[`
	for i := 0; i < 9; i++ {
		if i > 0 {
			tooMany += ","
		}
		tooMany += `{"reference":"R` + string(rune('A'+i)) + `12345"}`
	}
	tooMany += `]`
	if _, err := parseMoveEnvelope(map[string]json.RawMessage{"moves": json.RawMessage(tooMany)}); err == nil {
		t.Fatal("nine moves accepted")
	}
}

func TestExplicitNumericOffset(t *testing.T) {
	got := formatTime(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	if got != "2030-01-02T03:04:05+00:00" {
		t.Fatalf("got %q", got)
	}
}

func TestIdempotencyKeyCountsCharacters(t *testing.T) {
	r := httptest.NewRequest("POST", "/reservations", nil)
	r.Header.Set("Idempotency-Key", strings.Repeat("é", 255))
	if _, err := idempotencyKey(r); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Idempotency-Key", strings.Repeat("é", 256))
	if _, err := idempotencyKey(r); err == nil {
		t.Fatal("256 characters accepted")
	}
}

func TestIdempotencyLockMaterialIsUnambiguousPostgresText(t *testing.T) {
	a := idempotencyLockMaterial("u_a", "x|y")
	b := idempotencyLockMaterial("u_a|x", "y")
	if strings.ContainsRune(a, '\x00') || a == b {
		t.Fatalf("unsafe lock material: %q / %q", a, b)
	}
}
