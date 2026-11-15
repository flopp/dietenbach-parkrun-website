package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/chromedp/chromedp"
)

func TestPublishToWordpressInBrowser(t *testing.T) {
	if os.Getenv("CHROMEDP_INTEGRATION_TEST") != "1" {
		t.Skip("set CHROMEDP_INTEGRATION_TEST=1 to run the local Chrome integration test")
	}

	const content = "<p>Ein &amp; zwei\n\"drei\"</p>"
	submitted := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("%s %s", r.Method, r.URL)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/wp-admin":
			fmt.Fprint(w, `<form method="post" action="/login"><input id="user_login" name="user"><input id="user_pass" name="password" type="password"><button id="wp-submit">Login</button></form>`)
		case "/login":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if r.Form.Get("user") != "test-user" || r.Form.Get("password") != "test-password" {
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/wp-admin/dashboard", http.StatusSeeOther)
		case "/wp-admin/dashboard":
			fmt.Fprint(w, `<title>Dashboard</title><body class="wp-admin">Dashboard</body>`)
		case "/wp-admin/post.php":
			if r.URL.Query().Get("post") != "42" || r.URL.Query().Get("action") != "edit" {
				http.Error(w, "wrong editor", http.StatusBadRequest)
				return
			}
			if r.Method == http.MethodPost {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					http.Error(w, "bad form", http.StatusBadRequest)
					return
				}
				submitted <- r.Form.Get("content")
				fmt.Fprint(w, `<div id="message">Updated</div>`)
				return
			}
			fmt.Fprint(w, `<button id="content-html" type="button">Text</button><form method="post"><textarea id="content" name="content">old content</textarea><button id="publish">Update</button></form>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var settings config
	settings.Wordpress.Site = server.URL + "/wp-admin"
	settings.Wordpress.Username = "test-user"
	settings.Wordpress.Password = "test-password"
	settings.Wordpress.TargetPost = "42"

	browserCtx, cancelBrowser := chromedp.NewContext(context.Background())
	defer cancelBrowser()
	ctx, cancel := context.WithTimeout(browserCtx, 20*time.Second)
	defer cancel()
	if err := publishToWordpressInBrowser(ctx, content, settings); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-submitted:
		if got != content {
			t.Errorf("published content = %q, want %q", got, content)
		}
	default:
		t.Error("editor did not submit the content")
	}
}

func TestCafeIsDisplayedOnEventsPage(t *testing.T) {
	events, _, err := prepareEvents(sheetData{
		Headers: []string{"INDEX", "DATE", "SPECIAL", "RUNNERS", "RESULTS", "REPORT", "AUTHOR", "PHOTOS", "CAFE"},
		Rows:    [][]string{{"42", "2026-11-07", "", "27", "", "", "", "", "Küchenschelle"}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	template, err := os.ReadFile(filepath.Join("..", "www", "events.html"))
	if err != nil {
		t.Fatal(err)
	}
	html, err := renderTemplate(string(template), pageData{Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<th scope=\"col\">Café</th>") || !strings.Contains(html, "<td>Küchenschelle</td>") {
		t.Errorf("events page does not display the cafe column: %s", html)
	}
}

func TestWordPressEventsTemplateRendersEvents(t *testing.T) {
	events, _, err := prepareEvents(sheetData{
		Headers: []string{"INDEX", "DATE", "SPECIAL", "RUNNERS", "RESULTS", "REPORT", "AUTHOR", "PHOTOS"},
		Rows:    [][]string{{"42", "2026-11-07", "Halloween", "27", "", "", "", ""}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	template, err := os.ReadFile(filepath.Join("..", "www", "events-wordpress.html"))
	if err != nil {
		t.Fatal(err)
	}
	html, err := renderTemplate(string(template), pageData{Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<td class="Results-table-td">#42 / 2026-11-07</td>`) || !strings.Contains(html, `<td class="Results-table-td">Halloween</td>`) {
		t.Errorf("WordPress events page does not display event data: %s", html)
	}
}

func TestEventResultsURLRequiresNumericIndexAndNonFutureDate(t *testing.T) {
	today := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.Local)
	futureDate := today.AddDate(0, 0, 1).Format("2006-01-02")
	for _, test := range []struct {
		index string
		date  string
		want  string
	}{
		{"42", "2026-11-14", "https://www.parkrun.com.de/dietenbach/results/42/"},
		{"not numeric", "2026-11-14", ""},
		{"43", futureDate, ""},
	} {
		if got := eventResultsURL(test.index, test.date, today); got != test.want {
			t.Errorf("eventResultsURL(%q, %q) = %q, want %q", test.index, test.date, got, test.want)
		}
	}
}

func TestPrepareEventsIncludesRowsAndMarksFutureEvents(t *testing.T) {
	today := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.Local)
	futureDate := today.AddDate(0, 0, 1).Format("2006-01-02")
	events, latest, err := prepareEvents(sheetData{
		Headers: []string{"INDEX", "DATE", "SPECIAL", "RUNNERS", "RESULTS", "REPORT", "AUTHOR", "PHOTOS"},
		Rows: [][]string{
			{"42", "2026-11-14", "", "11", "", "", "", ""},
			{"not numeric", "2026-11-14", "", "12", "", "", "", ""},
			{"43", futureDate, "", "13", "", "", "", ""},
		},
	}, today)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 3 || len(latest) != 3 {
		t.Fatalf("prepared %d events and %d latest events, want 3 of each", len(events), len(latest))
	}
	if events[0].Future || !events[2].Future {
		t.Error("future flag was not set for the future event")
	}
	if events[0].ResultsURL != "https://www.parkrun.com.de/dietenbach/results/42/" {
		t.Errorf("past event ResultsURL = %q", events[0].ResultsURL)
	}
	if events[1].ResultsURL != "https://www.parkrun.com.de/dietenbach/results/not numeric/" || !events[2].Future {
		t.Errorf("prepared events do not preserve source row values and future status: %#v", events)
	}
}

func TestSiteTemplatesReferenceFingerprintedBlades(t *testing.T) {
	sourceDir := filepath.Join("..", "www")
	outputDir := t.TempDir()
	frameworkPath, err := copyFingerprintAsset(sourceDir, outputDir, "blades.min.css")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"index.html", "events.html"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(sourceDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(source), `href="blades.min.css"`) {
				t.Fatalf("%s does not reference the Blades stylesheet", name)
			}
			html := fingerprintAssetReferences(string(source), frameworkPath, "style.test.css", "main.test.js")
			if !strings.Contains(html, `href="`+frameworkPath+`"`) || strings.Contains(html, `href="blades.min.css"`) {
				t.Errorf("%s does not reference the fingerprinted Blades stylesheet", name)
			}
		})
	}
}

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

	template := `<link rel="stylesheet" href="blades.min.css"><link rel="stylesheet" href="style.css"><img src="../images/park.jpg"><script src="main.js"></script>{{if .Events}}<table><thead><tr><th>Nr.</th><th>Datum</th><th>Teilnehmer</th><th>Ergebnisse</th><th>Bericht</th><th>Fotos</th></tr></thead><tbody>{{range .LatestEvents}}<tr><td>{{.Index}}</td><td>{{.Date}}</td><td>{{.Runners}}</td><td><a href="{{.ResultsURL}}">Ergebnisse</a></td><td>{{if .ReportURL}}<a href="{{.ReportURL}}">Bericht</a>{{end}}{{if .ReportAuthor}} · {{.ReportAuthor}}{{end}}</td><td>{{if .PhotosURL}}<a href="{{.PhotosURL}}">Album öffnen</a>{{end}}</td></tr>{{end}}</tbody></table>{{end}}<a href="events.html">All events</a>`
	if err := os.WriteFile(filepath.Join(sourceDir, "index.html"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsTemplate := `<link rel="stylesheet" href="blades.min.css"><link rel="stylesheet" href="style.css"><table><thead><tr><th>Nr.</th><th>Datum</th><th>Special</th><th>Café</th><th>Teilnehmer</th><th>Ergebnisse</th><th>Bericht</th><th>Fotos</th></tr></thead><tbody>{{range .Events}}<tr><td>{{.Index}}</td><td>{{.Date}}</td><td>{{.Special}}</td><td>{{.Cafe}}</td><td>{{.Runners}}</td><td><a href="{{.ResultsURL}}">Ergebnisse</a></td><td>{{if .ReportURL}}<a href="{{.ReportURL}}">Bericht</a>{{end}}{{if .ReportAuthor}} · {{.ReportAuthor}}{{end}}</td><td>{{if .PhotosURL}}<a href="{{.PhotosURL}}">Album öffnen</a>{{end}}</td></tr>{{end}}</tbody></table>`
	if err := os.WriteFile(filepath.Join(sourceDir, "events.html"), []byte(eventsTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	wordPressEventsTemplate := `<table><tbody>{{range .Events}}<tr><td>{{.Index}}</td><td>{{.Date}}</td></tr>{{end}}</tbody></table>`
	if err := os.WriteFile(filepath.Join(sourceDir, "events-wordpress.html"), []byte(wordPressEventsTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"google":{"api_key":"test-key","sheet_id":"test-sheet"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"style.css", "blades.min.css", "main.js", "course.geojson", "counter.php", ".htaccess", "blades.LICENSE.md", "pico.LICENSE.md"} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJPEG(t, filepath.Join(imageDir, "park.jpg"), 2000, 1000)
	writeJPEG(t, filepath.Join(imageDir, "unused.jpg"), 10, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/leaflet/") {
			fmt.Fprintf(w, "Leaflet asset: %s", strings.TrimPrefix(r.URL.Path, "/leaflet/"))
			return
		}
		if got, want := r.URL.Path, "/v4/spreadsheets/test-sheet/values/'Parkrun'!A:ZZ"; got != want {
			t.Errorf("Sheets API path = %q, want %q", got, want)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "test-key" {
			t.Errorf("Sheets API key header = %q, want test-key", got)
		}
		if r.URL.Query().Has("key") {
			t.Error("API key should not be included in the request URL")
		}
		fmt.Fprint(w, `{"values":[["INDEX","DATE","SPECIAL","RUNNERS","RESULTS","REPORT","AUTHOR","PHOTOS","CAFE"],["1","2000-10-11","","11","","","","",""],["2","2000-10-12","","12","","","","",""],["3","2000-10-13","","13","","","","",""],["4","2000-10-14","","14","","","","",""],["5","2000-10-15","","15","","","","",""],["6","2000-10-16","","<script>alert(1)</script>","","https://example.com/report","Author <script>","https://photos.google.com/album","Küchenschelle"],["7","2000-10-17","","17","","","","",""],["8","2999-10-17","","18","","","","",""],["","2000-10-16","","5","","","","",""],["not numeric","2000-10-16","","35","","","","",""]]}`)
	}))
	defer server.Close()

	settings, err := readConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Date(2000, time.October, 18, 12, 0, 0, 0, time.Local)
	if err := generateWithDependencies(sourceDir, imageDir, outputDir, settings, server.Client(), server.URL, server.URL+"/leaflet", today); err != nil {
		t.Fatal(err)
	}

	html, err := os.ReadFile(filepath.Join(outputDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	styleChecksum := sha256.Sum256([]byte("style.css"))
	bladesChecksum := sha256.Sum256([]byte("blades.min.css"))
	scriptChecksum := sha256.Sum256([]byte("main.js"))
	stylesheetPath := "style." + hex.EncodeToString(styleChecksum[:]) + ".css"
	bladesPath := "blades.min." + hex.EncodeToString(bladesChecksum[:]) + ".css"
	scriptPath := "main." + hex.EncodeToString(scriptChecksum[:]) + ".js"
	if !strings.Contains(string(html), `<link rel="stylesheet" href="`+bladesPath+`"><link rel="stylesheet" href="`+stylesheetPath+`"><img src="img/park.jpg"><script src="`+scriptPath+`"></script>`) {
		t.Fatalf("generated HTML did not rewrite image path: %s", html)
	}
	for _, heading := range []string{"Nr.", "Datum", "Teilnehmer", "Ergebnisse", "Bericht", "Fotos"} {
		if !strings.Contains(string(html), "<th>"+heading+"</th>") {
			t.Errorf("generated HTML is missing %q heading: %s", heading, html)
		}
	}
	if !strings.Contains(string(html), `<a href="https://www.parkrun.com.de/dietenbach/results/5/">Ergebnisse</a>`) {
		t.Errorf("generated HTML is missing a results link from the latest source rows: %s", html)
	}
	if !strings.Contains(string(html), "<tr><td>1</td>") || !strings.Contains(string(html), "<tr><td>2</td>") {
		t.Errorf("main page did not preserve the first source rows: %s", html)
	}
	if strings.Contains(string(html), "<tr><td>6</td>") || strings.Contains(string(html), "<tr><td>8</td>") {
		t.Errorf("main page included events outside the latest five source rows: %s", html)
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
	if !strings.Contains(string(allEventsHTML), `<a href="https://example.com/report">Bericht</a> · Author &lt;script&gt;`) {
		t.Errorf("all-events HTML is missing escaped report and author: %s", allEventsHTML)
	}
	if !strings.Contains(string(allEventsHTML), `<a href="https://photos.google.com/album">Album öffnen</a>`) {
		t.Errorf("all-events HTML is missing photo album link: %s", allEventsHTML)
	}
	if strings.Contains(string(allEventsHTML), "<script>alert(1)</script>") {
		t.Errorf("all-events HTML did not escape sheet data: %s", allEventsHTML)
	}
	if got := strings.Count(string(allEventsHTML), "<tr>"); got != 11 {
		t.Errorf("all-events page rows = %d, want 11 including every source row", got)
	}
	if !strings.Contains(string(allEventsHTML), "<td>1</td>") || !strings.Contains(string(allEventsHTML), "<td>not numeric</td>") || !strings.Contains(string(allEventsHTML), "<td>8</td>") {
		t.Errorf("all-events page does not preserve the source rows: %s", allEventsHTML)
	}
	if !strings.Contains(string(allEventsHTML), "<th>Café</th>") || !strings.Contains(string(allEventsHTML), "<td>Küchenschelle</td>") {
		t.Errorf("all-events page does not display the cafe column: %s", allEventsHTML)
	}
	if !strings.Contains(string(allEventsHTML), `href="`+stylesheetPath+`"`) || !strings.Contains(string(allEventsHTML), `href="`+bladesPath+`"`) {
		t.Errorf("all-events page does not reference the fingerprinted stylesheet: %s", allEventsHTML)
	}

	wordPressEventsHTML, err := os.ReadFile(filepath.Join(outputDir, "events-wordpress.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(wordPressEventsHTML), "<tr>"); got != 10 {
		t.Errorf("WordPress events page rows = %d, want all 10 source events", got)
	}
	if !strings.Contains(string(wordPressEventsHTML), "<td>8</td><td>2999-10-17</td>") {
		t.Errorf("WordPress events page did not render the future source row: %s", wordPressEventsHTML)
	}
	for _, page := range [][]byte{html, allEventsHTML} {
		if strings.Contains(string(page), `href="events-wordpress.html"`) {
			t.Errorf("site page links to the unlinked WordPress events page: %s", page)
		}
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
	for _, name := range []string{"course.geojson", "counter.php", ".htaccess", "blades.LICENSE.md", "pico.LICENSE.md"} {
		content, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Errorf("asset %s was not copied: %v", name, err)
		} else if string(content) != name {
			t.Errorf("asset %s = %q, want %q", name, content, name)
		}
	}
	for name, want := range map[string]string{
		stylesheetPath: "style.css",
		bladesPath:     "blades.min.css",
		scriptPath:     "main.js",
	} {
		content, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Errorf("fingerprinted asset %s was not written: %v", name, err)
		} else if string(content) != want {
			t.Errorf("fingerprinted asset %s = %q, want %q", name, content, want)
		}
	}
	for _, asset := range leafletAssets {
		content, err := os.ReadFile(filepath.Join(outputDir, "leaflet", filepath.FromSlash(asset.outputPath)))
		if err != nil {
			t.Errorf("Leaflet asset %s was not downloaded: %v", asset.outputPath, err)
		} else if want := "Leaflet asset: " + asset.sourcePath; string(content) != want {
			t.Errorf("Leaflet asset %s = %q, want %q", asset.outputPath, content, want)
		}
	}
}

func TestParseSheetValuesPadsRowsAndPreservesBlankRows(t *testing.T) {
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
	if len(events.Rows) != 2 || strings.Join(events.Rows[0], ",") != "1,2026-10-17,Extra" || strings.Join(events.Rows[1], ",") != ",," {
		t.Errorf("rows = %#v, want both padded source rows", events.Rows)
	}
}

func TestParseSheetValuesPreservesFutureAndInvalidIndexesInSourceOrder(t *testing.T) {
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
	if len(events.Rows) != 6 {
		t.Fatalf("got %d rows, want all 6 source rows: %#v", len(events.Rows), events.Rows)
	}
	for index, want := range []string{"101", "102", "", "not numeric", "12.5", "103"} {
		if got := events.Rows[index][0]; got != want {
			t.Errorf("row %d index = %q, want %q", index, got, want)
		}
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
