package internal

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var weatherEndpoint = "https://weather.uni-freiburg.de/getData_json_onevar_export2_FRWRTM.php"
var weatherCacheDir = filepath.Join(".cache", "weather")
var weatherHTTPClient = &http.Client{Timeout: 30 * time.Second}

func SetCacheDir(cacheDir string) {
	weatherCacheDir = filepath.Join(cacheDir, "weather")
}

func GetTemperatureForEvent(eventDate time.Time) (float64, error) {
	date := eventDate.Format("2006-01-02")
	cachePath := filepath.Join(weatherCacheDir, date+".csv")
	if cached, err := os.ReadFile(cachePath); err == nil {
		if temperature, err := parseTemperatureAtNine(cached, eventDate); err == nil {
			return temperature, nil
		}
	}

	form := url.Values{
		"varsel[]":     {"PsychTemp"},
		"timestart":    {date},
		"timestop":     {date},
		"data_set_sel": {"Minute"},
	}
	request, err := http.NewRequest(http.MethodPost, weatherEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, fmt.Errorf("create weather request for %s: %w", date, err)
	}
	request.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("Origin", "https://weather.uni-freiburg.de")
	request.Header.Set("Referer", "https://weather.uni-freiburg.de/")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")

	response, err := weatherHTTPClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("get weather for event on %s: %w", date, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("get weather for event on %s: HTTP status %s", date, response.Status)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("read weather for event on %s: %w", date, err)
	}
	temperature, err := parseTemperatureAtNine(body, eventDate)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(weatherCacheDir, 0o755); err != nil {
		return 0, fmt.Errorf("create weather cache directory: %w", err)
	}

	if err := os.WriteFile(cachePath, body, 0o644); err != nil {
		return 0, fmt.Errorf("cache weather for event on %s: %w", date, err)
	}
	return temperature, nil
}

func parseTemperatureAtNine(data []byte, eventDate time.Time) (float64, error) {
	date := eventDate.Format("2006-01-02")
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return 0, fmt.Errorf("weather response for event on %s is empty", date)
	}

	if data[0] == '[' {
		var rows []map[string]any
		if err := json.Unmarshal(data, &rows); err != nil {
			return 0, fmt.Errorf("parse weather response for event on %s: %w", date, err)
		}
		var candidates []temperatureCandidate
		for _, row := range rows {
			timestamp, ok := jsonTimestamp(row, eventDate.Location())
			if !ok {
				continue
			}
			value, ok := jsonTemperature(row)
			if !ok {
				continue
			}
			candidates = append(candidates, temperatureCandidate{timestamp: timestamp, temperature: value})
		}
		return nearestTemperature(candidates, eventDate, date)
	}

	reader := csv.NewReader(bytes.NewReader(data))
	records, err := reader.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("parse weather CSV for event on %s: %w", date, err)
	}
	if len(records) < 2 {
		return 0, fmt.Errorf("weather CSV for event on %s contains no data", date)
	}

	headers := records[0]
	timeColumn := findHeader(headers, "Lokalzeit")
	if timeColumn < 0 {
		timeColumn = findHeader(headers, "UTC")
	}
	temperatureColumn := findTemperatureHeader(headers)
	if timeColumn < 0 || temperatureColumn < 0 {
		return 0, fmt.Errorf("weather CSV for event on %s has unexpected columns", date)
	}

	var candidates []temperatureCandidate
	for _, record := range records[1:] {
		if timeColumn >= len(record) || temperatureColumn >= len(record) {
			continue
		}
		timestamp, ok := parseWeatherTimestamp(record[timeColumn], eventDate.Location(), timeColumn == findHeader(headers, "UTC"))
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(record[temperatureColumn]), ",", "."), 64)
		if err == nil {
			candidates = append(candidates, temperatureCandidate{timestamp: timestamp, temperature: value})
		}
	}
	return nearestTemperature(candidates, eventDate, date)
}

type temperatureCandidate struct {
	timestamp   time.Time
	temperature float64
}

func jsonTimestamp(row map[string]any, location *time.Location) (time.Time, bool) {
	if value, ok := row["Lokalzeit"].(string); ok {
		if timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", value, location); err == nil {
			return timestamp, true
		}
	}
	for key, value := range row {
		if key == "UTC,Lokalzeit" {
			if timestampValue, ok := value.(string); ok {
				if timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", timestampValue, time.UTC); err == nil {
					return timestamp.In(location), true
				}
			}
		}
	}
	return time.Time{}, false
}

func jsonTemperature(row map[string]any) (float64, bool) {
	if value, ok := row["CS215_AirTemperature_degC"]; ok {
		switch typed := value.(type) {
		case float64:
			return typed, true
		case string:
			if parsed, err := strconv.ParseFloat(strings.ReplaceAll(typed, ",", "."), 64); err == nil {
				return parsed, true
			}
		}
	}
	for key, value := range row {
		if strings.Contains(strings.ToLower(key), "temperature") || strings.Contains(strings.ToLower(key), "temp") {
			switch typed := value.(type) {
			case float64:
				return typed, true
			case string:
				if parsed, err := strconv.ParseFloat(strings.ReplaceAll(typed, ",", "."), 64); err == nil {
					return parsed, true
				}
			}
		}
	}
	return 0, false
}

func nearestTemperature(candidates []temperatureCandidate, eventDate time.Time, date string) (float64, error) {
	target := time.Date(
		eventDate.Year(), eventDate.Month(), eventDate.Day(),
		9, 0, 0, 0, eventDate.Location(),
	)
	nearest := temperatureCandidate{}
	found := false
	for _, candidate := range candidates {
		if candidate.timestamp.Format("2006-01-02") != date {
			continue
		}
		if !found || absDuration(candidate.timestamp.Sub(target)) < absDuration(nearest.timestamp.Sub(target)) {
			nearest = candidate
			found = true
		}
	}
	if !found {
		return 0, fmt.Errorf("weather data for event on %s contains no 9:00 temperature", date)
	}
	return nearest.temperature, nil
}

func absDuration(duration time.Duration) time.Duration {
	if duration < 0 {
		return -duration
	}
	return duration
}

func parseWeatherTimestamp(value string, location *time.Location, utc bool) (time.Time, bool) {
	parseLocation := location
	if utc {
		parseLocation = time.UTC
	}
	timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(value), parseLocation)
	if err != nil {
		return time.Time{}, false
	}
	if utc {
		timestamp = timestamp.In(location)
	}
	return timestamp, true
}

func findHeader(headers []string, name string) int {
	for i, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), name) ||
			strings.Contains(strings.ToLower(header), strings.ToLower(name)) {
			return i
		}
	}
	return -1
}

func findTemperatureHeader(headers []string) int {
	for i, header := range headers {
		lower := strings.ToLower(header)
		if strings.Contains(lower, "temperature") || strings.Contains(lower, "temp") {
			return i
		}
	}
	return -1
}
