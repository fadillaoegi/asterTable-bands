package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestAvailabilityFiftyInFlightColdAndSustained(t *testing.T) {
	databaseURL := os.Getenv("TABLEKEEPER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TABLEKEEPER_TEST_DATABASE_URL is not set")
	}
	previousProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previousProcs)

	application, err := New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second

	weekdays := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	hours := make([]map[string]any, 0, len(weekdays))
	for _, weekday := range weekdays {
		hours = append(hours, map[string]any{"weekday": weekday, "opens": "00:00", "closes": "23:59"})
	}
	fixture := map[string]any{
		"users": []any{},
		"restaurants": []map[string]any{{
			"id": "load", "name": "load", "timezone": "Europe/Amsterdam",
			"slot_minutes": 30, "reservation_duration_minutes": 90,
			"cancellation_cutoff_minutes": 0, "opening_hours": hours,
			"tables": []map[string]any{{"id": "t1", "label": "1", "capacity": 4}},
		}},
		"reservations": []any{},
	}
	api := importTestAPI{t: t, client: client, base: server.URL}
	api.expect(http.MethodPost, "/_test/reset", "", "", fixture, http.StatusNoContent)

	type outcome struct {
		wave    int
		request int
		elapsed time.Duration
		err     error
	}
	for wave := 0; wave < 10; wave++ {
		results := make(chan outcome, 50)
		var group sync.WaitGroup
		waveStarted := time.Now()
		for requestNumber := 0; requestNumber < 50; requestNumber++ {
			group.Add(1)
			go func(requestNumber int) {
				defer group.Done()
				date := fmt.Sprintf("%04d-01-01", 2400+wave*50+requestNumber)
				started := time.Now()
				response, err := client.Get(server.URL + "/availability?restaurant_id=load&date=" + date + "&party_size=2")
				elapsed := time.Since(started)
				if err != nil {
					results <- outcome{wave: wave, request: requestNumber, elapsed: elapsed, err: err}
					return
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					results <- outcome{wave: wave, request: requestNumber, elapsed: elapsed, err: fmt.Errorf("status %d", response.StatusCode)}
					return
				}
				var body struct {
					RestaurantID string `json:"restaurant_id"`
					Date         string `json:"date"`
					Timezone     string `json:"timezone"`
					Slots        []struct {
						StartsLocal string   `json:"starts_at_local"`
						StartsAt    string   `json:"starts_at"`
						TableIDs    []string `json:"available_table_ids"`
					} `json:"slots"`
				}
				if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
					results <- outcome{wave: wave, request: requestNumber, elapsed: elapsed, err: err}
					return
				}
				if body.RestaurantID != "load" || body.Date != date || body.Timezone != "Europe/Amsterdam" || len(body.Slots) != 45 ||
					body.Slots[0].StartsLocal != date+"T00:00" || body.Slots[44].StartsLocal != date+"T22:00" ||
					len(body.Slots[0].TableIDs) != 1 || body.Slots[0].TableIDs[0] != "t1" || body.Slots[0].StartsAt == "" {
					results <- outcome{wave: wave, request: requestNumber, elapsed: elapsed, err: fmt.Errorf("incorrect availability response")}
					return
				}
				results <- outcome{wave: wave, request: requestNumber, elapsed: elapsed}
			}(requestNumber)
		}
		group.Wait()
		close(results)
		if elapsed := time.Since(waveStarted); elapsed >= 5*time.Second {
			t.Fatalf("wave %d took %s, want <5s", wave, elapsed)
		}
		var waveMax time.Duration
		for result := range results {
			if result.err != nil || result.elapsed >= 5*time.Second {
				t.Fatalf("wave %d request %d: elapsed=%s err=%v", result.wave, result.request, result.elapsed, result.err)
			}
			if result.elapsed > waveMax {
				waveMax = result.elapsed
			}
		}
		t.Logf("wave %d: 50/50 correct responses, max request %s", wave, waveMax)
	}
}
