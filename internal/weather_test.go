package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTemperatureAtNineFromJSON(t *testing.T) {
	eventDate := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.Local)
	data := []byte(`[
		{"Date":"UTC,Lokalzeit","CS215_AirTemperature_degC":"Lufttemperatur (°C)"},
		{"UTC,Lokalzeit":"2026-11-21 07:59:00","Lokalzeit":"2026-11-21 08:59:00","CS215_AirTemperature_degC":5.5},
		{"UTC,Lokalzeit":"2026-11-21 08:00:00","Lokalzeit":"2026-11-21 09:00:00","CS215_AirTemperature_degC":7.25},
		{"UTC,Lokalzeit":"2026-11-21 08:01:00","Lokalzeit":"2026-11-21 09:01:00","CS215_AirTemperature_degC":8.5}
	]`)

	temperature, err := parseTemperatureAtNine(data, eventDate)
	if err != nil {
		t.Fatal(err)
	}
	if temperature != 7.25 {
		t.Errorf("temperature = %v, want 7.25", temperature)
	}
}

func TestParseTemperatureAtNineFromCSV(t *testing.T) {
	eventDate := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.Local)
	data := []byte("UTC,Lokalzeit,Lokalzeit,CS215_AirTemperature_degC\n" +
		"2026-11-21 07:59:00,2026-11-21 08:59:00,2026-11-21 08:59:00,5.5\n" +
		"2026-11-21 08:00:00,2026-11-21 09:00:00,2026-11-21 09:00:00,7.25\n")

	temperature, err := parseTemperatureAtNine(data, eventDate)
	if err != nil {
		t.Fatal(err)
	}
	if temperature != 7.25 {
		t.Errorf("temperature = %v, want 7.25", temperature)
	}
}

func TestGetTemperatureForEventCachesResponse(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		want := url.Values{
			"varsel[]":     {"PsychTemp"},
			"timestart":    {"2026-11-21"},
			"timestop":     {"2026-11-21"},
			"data_set_sel": {"Minute"},
		}
		if r.Form.Encode() != want.Encode() {
			t.Errorf("form = %q, want %q", r.Form.Encode(), want.Encode())
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"Lokalzeit":"2026-11-21 09:00:00","CS215_AirTemperature_degC":6.25}]`)
	}))
	defer server.Close()

	originalEndpoint := weatherEndpoint
	originalCacheDir := weatherCacheDir
	originalClient := weatherHTTPClient
	weatherEndpoint = server.URL
	weatherCacheDir = filepath.Join(t.TempDir(), "weather")
	weatherHTTPClient = server.Client()
	t.Cleanup(func() {
		weatherEndpoint = originalEndpoint
		weatherCacheDir = originalCacheDir
		weatherHTTPClient = originalClient
	})

	eventDate := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.Local)
	for i := 0; i < 2; i++ {
		temperature, err := GetTemperatureForEvent(eventDate)
		if err != nil {
			t.Fatal(err)
		}
		if temperature != 6.25 {
			t.Errorf("temperature = %v, want 6.25", temperature)
		}
	}
	if requestCount != 1 {
		t.Errorf("request count = %d, want 1", requestCount)
	}
	cachePath := filepath.Join(weatherCacheDir, "2026-11-21.csv")
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("cached weather file: %v", err)
	}
}

func TestSetCacheDir(t *testing.T) {
	originalCacheDir := weatherCacheDir
	t.Cleanup(func() {
		weatherCacheDir = originalCacheDir
	})

	SetCacheDir(filepath.Join("custom", "cache"))
	want := filepath.Join("custom", "cache", "weather")
	if weatherCacheDir != want {
		t.Errorf("weather cache dir = %q, want %q", weatherCacheDir, want)
	}
}
