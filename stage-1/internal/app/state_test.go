package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
