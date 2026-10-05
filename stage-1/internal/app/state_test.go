package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImportRejectsInvalidTimezoneBeforeDatabaseMutation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/_test/import", strings.NewReader(`{
		"track": "tablekeeper",
		"format_version": 1,
		"state": {
			"users": [],
			"auth_sessions": [],
			"restaurants": [
				{"timezone": "UTC"},
				{"timezone": "No/Such_Zone"}
			],
			"restaurant_opening_hours": [],
			"restaurant_tables": [],
			"reservations": [],
			"reservation_occupancies": [],
			"idempotency_receipts": []
		}
	}`))
	response := httptest.NewRecorder()

	// The nil database is intentional: semantic import validation must finish
	// before importState can begin a transaction or mutate destination state.
	(&App{}).importState(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "validation_failed" {
		t.Fatalf("error code = %q, want validation_failed", body.Error.Code)
	}
}

func TestValidateImportedRestaurantTimezonesUsesLoadedZoneRules(t *testing.T) {
	valid := json.RawMessage(`[{"timezone":"UTC"},{"timezone":"Europe/Amsterdam"}]`)
	if err := validateImportedRestaurantTimezones(valid); err != nil {
		t.Fatalf("loadable timezones rejected: %v", err)
	}
	invalid := json.RawMessage(`[{"timezone":"UTC"},{"timezone":"No/Such_Zone"}]`)
	if err := validateImportedRestaurantTimezones(invalid); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}

func semanticImportState(timezone, startsLocal string, durationMinutes, partySize int) map[string]json.RawMessage {
	startsAt := time.Date(2035, 1, 1, 12, 0, 0, 0, time.UTC)
	return map[string]json.RawMessage{
		"users": mustJSON([]map[string]any{{"id": "u_source"}}),
		"restaurants": mustJSON([]map[string]any{{
			"id": "r_source", "timezone": timezone,
			"slot_minutes": 30, "reservation_duration_minutes": durationMinutes,
		}}),
		"restaurant_opening_hours": mustJSON([]map[string]any{
			{"restaurant_id": "r_source", "weekday": 0, "opens": "00:00:00", "closes": "23:59:00"},
			{"restaurant_id": "r_source", "weekday": 1, "opens": "00:00:00", "closes": "23:59:00"},
			{"restaurant_id": "r_source", "weekday": 2, "opens": "00:00:00", "closes": "23:59:00"},
			{"restaurant_id": "r_source", "weekday": 3, "opens": "00:00:00", "closes": "23:59:00"},
			{"restaurant_id": "r_source", "weekday": 4, "opens": "00:00:00", "closes": "23:59:00"},
			{"restaurant_id": "r_source", "weekday": 5, "opens": "00:00:00", "closes": "23:59:00"},
			{"restaurant_id": "r_source", "weekday": 6, "opens": "00:00:00", "closes": "23:59:00"},
		}),
		"restaurant_tables": mustJSON([]map[string]any{{
			"id": "t_source", "restaurant_id": "r_source", "capacity": 4,
		}}),
		"reservations": mustJSON([]map[string]any{{
			"user_id": "u_source", "restaurant_id": "r_source", "table_id": "t_source",
			"party_size": partySize, "starts_at_local": startsLocal,
			"starts_at": startsAt, "ends_at": startsAt.Add(90 * time.Minute),
		}}),
		"auth_sessions":           mustJSON([]any{}),
		"reservation_occupancies": mustJSON([]any{}),
		"idempotency_receipts":    mustJSON([]any{}),
	}
}

func TestValidateImportedStateSemantics(t *testing.T) {
	if err := validateImportedStateSemantics(semanticImportState("UTC", "2035-01-01T12:00", 90, 2)); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	for _, test := range []struct {
		name            string
		timezone        string
		startsLocal     string
		durationMinutes int
		partySize       int
	}{
		{"timezone changes represented instant", "Europe/Berlin", "2035-01-01T12:00", 90, 2},
		{"local time changes represented instant", "UTC", "2035-01-01T13:00", 90, 2},
		{"duration changes represented end", "UTC", "2035-01-01T12:00", 120, 2},
		{"party exceeds table capacity", "UTC", "2035-01-01T12:00", 90, 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := semanticImportState(test.timezone, test.startsLocal, test.durationMinutes, test.partySize)
			if err := validateImportedStateSemantics(state); err == nil {
				t.Fatal("semantically invalid state accepted")
			}

			body, err := json.Marshal(map[string]any{
				"track": "tablekeeper", "format_version": 1, "state": state,
			})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/_test/import", strings.NewReader(string(body)))
			// A nil database proves rejection occurs before a transaction can
			// mutate destination state.
			(&App{}).importState(response, request)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body = %s", response.Code, response.Body.String())
			}
			var errorBody struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
				t.Fatal(err)
			}
			if errorBody.Error.Code != "validation_failed" {
				t.Fatalf("error code = %q, want validation_failed", errorBody.Error.Code)
			}
		})
	}
}
