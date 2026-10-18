package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateCopiesAssetsAndResizesReferencedImages(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "www")
	imageDir := filepath.Join(root, "images")
	outputDir := filepath.Join(root, "output")
	configPath := filepath.Join(root, "secrets.json")
	for _, dir := range []string{sourceDir, imageDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	template := `<img src="../images/park.jpg"><script src="main.js"></script>{{if .Events}}<table><thead><tr><th>Nr.</th><th>Datum</th><th>Teilnehmer</th><th>Ergebnisse</th><th>Bericht</th><th>Fotos</th></tr></thead><tbody>{{range .LatestEvents}}<tr><td>{{.Index}}</td><td>{{.Date}}</td><td>{{.Runners}}</td><td><a href="{{.ResultsURL}}">Ergebnisse</a></td><td>{{if .ReportURL}}<a href="{{.ReportURL}}">Bericht</a>{{end}}{{if .ReportAuthor}} · {{.ReportAuthor}}{{end}}</td><td>{{if .PhotosURL}}<a href="{{.PhotosURL}}">Album öffnen</a>{{end}}</td></tr>{{end}}</tbody></table>{{end}}<a href="events.html">All events</a>`
	if err := os.WriteFile(filepath.Join(sourceDir, "index.html"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsTemplate := `<table><thead><tr><th>Nr.</th><th>Datum</th><th>Teilnehmer</th><th>Ergebnisse</th><th>Bericht</th><th>Fotos</th></tr></thead><tbody>{{range .Events}}<tr><td>{{.Index}}</td><td>{{.Date}}</td><td>{{.Runners}}</td><td><a href="{{.ResultsURL}}">Ergebnisse</a></td><td>{{if .ReportURL}}<a href="{{.ReportURL}}">Bericht</a>{{end}}{{if .ReportAuthor}} · {{.ReportAuthor}}{{end}}</td><td>{{if .PhotosURL}}<a href="{{.PhotosURL}}">Album öffnen</a>{{end}}</td></tr>{{end}}</tbody></table>`
	if err := os.WriteFile(filepath.Join(sourceDir, "events.html"), []byte(eventsTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"google":{"api_key":"test-key","sheet_id":"test-sheet"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"style.css", "main.js", "course.geojson"} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJPEG(t, filepath.Join(imageDir, "park.jpg"), 2000, 1000)
	writeJPEG(t, filepath.Join(imageDir, "unused.jpg"), 10, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v4/spreadsheets/test-sheet/values/'Parkrun'!A:ZZ"; got != want {
			t.Errorf("Sheets API path = %q, want %q", got, want)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "test-key" {
			t.Errorf("Sheets API key header = %q, want test-key", got)
		}
		if r.URL.Query().Has("key") {
			t.Error("API key should not be included in the request URL")
		}
		fmt.Fprint(w, `{"values":[["INDEX","DATE","RUNNERS","RESULTS","REPORT","AUTHOR","PHOTOS"],["1","2000-10-11","11","","","",""],["2","2000-10-12","12","","","",""],["3","2000-10-13","13","","","",""],["4","2000-10-14","14","","","",""],["5","2000-10-15","15","","","",""],["6","2000-10-16","<script>alert(1)</script>","","https://example.com/report","Author <script>","https://photos.google.com/album"],["7","2000-10-17","17","","","",""],["8","2999-10-17","18","","","",""],["","2000-10-16","5","","","",""],["not numeric","2000-10-16","35","","","",""]]}`)
	}))
	defer server.Close()

	if err := generateWithDependencies(sourceDir, imageDir, outputDir, configPath, server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}

	html, err := os.ReadFile(filepath.Join(outputDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `<img src="img/park.jpg"><script src="main.js"></script>`) {
		t.Fatalf("generated HTML did not rewrite image path: %s", html)
	}
	for _, heading := range []string{"Nr.", "Datum", "Teilnehmer", "Ergebnisse", "Bericht", "Fotos"} {
		if !strings.Contains(string(html), "<th>"+heading+"</th>") {
			t.Errorf("generated HTML is missing %q heading: %s", heading, html)
		}
	}
	if !strings.Contains(string(html), `<a href="https://www.parkrun.com.de/dietenbach/results/7/">Ergebnisse</a>`) {
		t.Errorf("generated HTML is missing the constructed results link: %s", html)
	}
	if !strings.Contains(string(html), `<a href="https://example.com/report">Bericht</a> · Author &lt;script&gt;`) {
		t.Errorf("generated HTML is missing escaped report and author: %s", html)
	}
	if !strings.Contains(string(html), `<a href="https://photos.google.com/album">Album öffnen</a>`) {
		t.Errorf("generated HTML is missing photo album link: %s", html)
	}
	if strings.Contains(string(html), "<script>alert(1)</script>") {
		t.Errorf("generated HTML did not escape sheet data: %s", html)
	}
	if strings.Contains(string(html), "<tr><td>1</td>") || strings.Contains(string(html), "<tr><td>2</td>") {
		t.Errorf("main page included events outside the latest five: %s", html)
	}
	if strings.Contains(string(html), "<tr><td>8</td>") {
		t.Errorf("generated HTML included a future event or event without INDEX: %s", html)
	}
	if got := strings.Count(string(html), "<tr>"); got != latestEventCount+1 {
		t.Errorf("main page table rows including header = %d, want %d", got, latestEventCount+1)
	}
	if !strings.Contains(string(html), `href="events.html"`) {
		t.Errorf("main page is missing link to all-events page: %s", html)
	}

	allEventsHTML, err := os.ReadFile(filepath.Join(outputDir, "events.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(allEventsHTML), "<tr>"); got != 8 {
		t.Errorf("all-events page rows = %d, want 8", got)
	}
	if !strings.Contains(string(allEventsHTML), "<td>11</td>") || !strings.Contains(string(allEventsHTML), "<td>17</td>") {
		t.Errorf("all-events page does not include the full event range: %s", allEventsHTML)
	}

	imageFile, err := os.Open(filepath.Join(outputDir, "img", "park.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(imageFile)
	imageFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Bounds().Dx(); got != maxImageDimension {
		t.Errorf("resized image width = %d, want %d", got, maxImageDimension)
	}
	if got := decoded.Bounds().Dy(); got != 256 {
		t.Errorf("resized image height = %d, want 256", got)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "img", "unused.jpg")); !os.IsNotExist(err) {
		t.Errorf("unreferenced image should not be copied, stat error = %v", err)
	}
	for _, name := range []string{"style.css", "main.js", "course.geojson"} {
		content, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Errorf("asset %s was not copied: %v", name, err)
		} else if string(content) != name {
			t.Errorf("asset %s = %q, want %q", name, content, name)
		}
	}
}

func TestParseSheetValuesPadsRowsAndSkipsBlankRows(t *testing.T) {
	events, err := parseSheetValues([][]any{
		{"INDEX", "Date"},
		{"1", "2026-10-17", "Extra"},
		{"", ""},
	}, time.Date(2026, time.October, 18, 15, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := strings.Join(events.Headers, ","), "INDEX,Date,Spalte 3"; got != want {
		t.Errorf("headers = %q, want %q", got, want)
	}
	if len(events.Rows) != 1 || strings.Join(events.Rows[0], ",") != "1,2026-10-17,Extra" {
		t.Errorf("rows = %#v, want one padded event row", events.Rows)
	}
}

func TestParseSheetValuesSkipsFutureAndInvalidIndexes(t *testing.T) {
	events, err := parseSheetValues([][]any{
		{"INDEX", "DATE", "Participants"},
		{"101", "2026-10-17", "25"},
		{"102", "2026-10-19", "30"},
		{"", "2026-10-16", "20"},
		{"not numeric", "2026-10-16", "35"},
		{"12.5", "2026-10-16", "40"},
		{"103", "18.10.2026", "45"},
	}, time.Date(2026, time.October, 18, 9, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Rows) != 2 {
		t.Fatalf("got %d rows, want 2: %#v", len(events.Rows), events.Rows)
	}
	if events.Rows[0][0] != "103" || events.Rows[1][0] != "101" {
		t.Errorf("included event indexes = %q, %q; want newest-first 103, 101", events.Rows[0][0], events.Rows[1][0])
	}
}

func TestParseSheetValuesRequiresIndexAndDateColumns(t *testing.T) {
	for _, values := range [][][]any{
		{{"Date", "Participants"}, {"2026-10-17", "20"}},
		{{"INDEX", "Participants"}, {"101", "20"}},
	} {
		if _, err := parseSheetValues(values, time.Now()); err == nil {
			t.Errorf("parseSheetValues(%v) succeeded, want missing-column error", values)
		}
	}
}

func TestFetchEventsReportsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	var settings config
	settings.Google.APIKey = "test-key"
	settings.Google.SheetID = "test-sheet"

	_, err := fetchEvents(context.Background(), server.Client(), server.URL, settings)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("fetchEvents error = %v, want HTTP 401", err)
	}
}

func TestProcessImagesRejectsPathsOutsideImageDirectory(t *testing.T) {
	_, err := processImages(`<img src="../images/../../secret.jpg">`, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "invalid image path") {
		t.Fatalf("processImages error = %v, want invalid image path error", err)
	}
}

func writeJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			imageData.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 80, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, imageData, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
