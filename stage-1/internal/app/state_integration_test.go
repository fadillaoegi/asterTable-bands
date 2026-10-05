package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

type importTestAPI struct {
	t      *testing.T
	client *http.Client
	base   string
}

func (api importTestAPI) request(method, path, token, key string, body any) (*http.Response, []byte) {
	api.t.Helper()
	var encoded io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			api.t.Fatal(err)
		}
		encoded = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, api.base+path, encoded)
	if err != nil {
		api.t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := api.client.Do(request)
	if err != nil {
		api.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		api.t.Fatal(err)
	}
	return response, data
}

func (api importTestAPI) expect(method, path, token, key string, body any, status int) []byte {
	api.t.Helper()
	response, data := api.request(method, path, token, key, body)
	if response.StatusCode != status {
		api.t.Fatalf("%s %s status = %d, want %d; body = %s", method, path, response.StatusCode, status, data)
	}
	return data
}

func decodeTestJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func cloneTestJSON(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return decodeTestJSON(t, data)
}

func importTestRow(t *testing.T, export map[string]any, table string) map[string]any {
	t.Helper()
	state := export["state"].(map[string]any)
	rows := state[table].([]any)
	return rows[0].(map[string]any)
}

func TestImportSemanticValidationIsAtomic(t *testing.T) {
	databaseURL := os.Getenv("TABLEKEEPER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TABLEKEEPER_TEST_DATABASE_URL is not set")
	}
	application, err := New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	api := importTestAPI{t: t, client: server.Client(), base: server.URL}

	weekdays := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	hours := make([]map[string]any, 0, len(weekdays))
	for _, weekday := range weekdays {
		hours = append(hours, map[string]any{"weekday": weekday, "opens": "00:00", "closes": "23:59"})
	}
	restaurant := func(id, timezone string) map[string]any {
		return map[string]any{
			"id": id, "name": id, "timezone": timezone, "slot_minutes": 30,
			"reservation_duration_minutes": 90, "cancellation_cutoff_minutes": 0,
			"opening_hours": hours,
			"tables":        []map[string]any{{"id": id + "_t1", "label": "1", "capacity": 4}},
		}
	}
	sourceFixture := map[string]any{
		"users":        []map[string]any{{"id": "u_source", "email": "source@example.test", "password": "correct horse", "display_name": "Source"}},
		"restaurants":  []map[string]any{restaurant("r_source", "UTC")},
		"reservations": []any{},
	}
	api.expect(http.MethodPost, "/_test/reset", "", "", sourceFixture, http.StatusNoContent)
	loginBody := decodeTestJSON(t, api.expect(http.MethodPost, "/auth/login", "", "", map[string]any{
		"email": "source@example.test", "password": "correct horse",
	}, http.StatusOK))
	sourceToken := loginBody["token"].(string)
	api.expect(http.MethodPost, "/reservations", sourceToken, "semantic-source", map[string]any{
		"restaurant_id": "r_source", "table_id": "r_source_t1",
		"starts_at_local": "2035-01-01T12:00", "party_size": 2,
	}, http.StatusCreated)
	sourceExport := decodeTestJSON(t, api.expect(http.MethodGet, "/_test/export", "", "", nil, http.StatusOK))

	destinationFixture := map[string]any{
		"users":        []map[string]any{{"id": "u_destination", "email": "destination@example.test", "password": "correct horse", "display_name": "Destination"}},
		"restaurants":  []map[string]any{restaurant("r_destination", "Europe/Amsterdam")},
		"reservations": []any{},
	}
	prepareDestination := func(t *testing.T) (string, map[string]any) {
		api.expect(http.MethodPost, "/_test/reset", "", "", destinationFixture, http.StatusNoContent)
		login := decodeTestJSON(t, api.expect(http.MethodPost, "/auth/login", "", "", map[string]any{
			"email": "destination@example.test", "password": "correct horse",
		}, http.StatusOK))
		before := decodeTestJSON(t, api.expect(http.MethodGet, "/_test/export", "", "", nil, http.StatusOK))
		return login["token"].(string), before
	}

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"timezone changes represented instant", func(value map[string]any) {
			importTestRow(t, value, "restaurants")["timezone"] = "Europe/Berlin"
		}},
		{"local time changes represented instant", func(value map[string]any) {
			importTestRow(t, value, "reservations")["starts_at_local"] = "2035-01-01T13:00"
		}},
		{"duration changes represented end", func(value map[string]any) {
			importTestRow(t, value, "restaurants")["reservation_duration_minutes"] = 120
		}},
		{"party exceeds table capacity", func(value map[string]any) {
			importTestRow(t, value, "reservations")["party_size"] = 5
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			destinationToken, before := prepareDestination(t)
			corrupt := cloneTestJSON(t, sourceExport)
			test.mutate(corrupt)
			response, data := api.request(http.MethodPost, "/_test/import", "", "", corrupt)
			if response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body = %s", response.StatusCode, data)
			}
			errorBody := decodeTestJSON(t, data)
			if errorBody["error"].(map[string]any)["code"] != "validation_failed" {
				t.Fatalf("unexpected error: %s", data)
			}
			after := decodeTestJSON(t, api.expect(http.MethodGet, "/_test/export", "", "", nil, http.StatusOK))
			if !reflect.DeepEqual(after, before) {
				t.Fatal("rejected import changed destination export")
			}
			api.expect(http.MethodGet, "/reservations", destinationToken, "", nil, http.StatusOK)
		})
	}

	prepareDestination(t)
	api.expect(http.MethodPost, "/_test/import", "", "", sourceExport, http.StatusNoContent)
	imported := decodeTestJSON(t, api.expect(http.MethodGet, "/_test/export", "", "", nil, http.StatusOK))
	if !reflect.DeepEqual(imported, sourceExport) {
		t.Fatal("valid import did not exactly replace destination state")
	}
	api.expect(http.MethodGet, "/reservations", sourceToken, "", nil, http.StatusOK)
	api.expect(http.MethodPost, "/auth/login", "", "", map[string]any{
		"email": "source@example.test", "password": "correct horse",
	}, http.StatusOK)
}
