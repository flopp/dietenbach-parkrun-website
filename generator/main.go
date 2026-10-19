package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxImageDimension = 512
const latestEventCount = 5
const sheetsAPIURL = "https://sheets.googleapis.com"
const leafletAssetsBaseURL = "https://unpkg.com/leaflet@1.9.4"

var leafletAssets = []struct {
	sourcePath string
	outputPath string
}{
	{"dist/leaflet.css", "leaflet.css"},
	{"dist/leaflet.js", "leaflet.js"},
	{"dist/images/layers.png", "images/layers.png"},
	{"dist/images/layers-2x.png", "images/layers-2x.png"},
	{"dist/images/marker-icon.png", "images/marker-icon.png"},
	{"dist/images/marker-icon-2x.png", "images/marker-icon-2x.png"},
	{"dist/images/marker-shadow.png", "images/marker-shadow.png"},
	{"LICENSE", "LICENSE"},
}

var imageSource = regexp.MustCompile(`(src=["'])\.\./images/([^"']+)(["'])`)

type config struct {
	Google struct {
		APIKey  string `json:"api_key"`
		SheetID string `json:"sheet_id"`
	} `json:"google"`
}

type sheetData struct {
	Headers []string
	Rows    [][]string
}

type event struct {
	Index        string
	Date         string
	Runners      string
	ResultsURL   string
	ReportURL    string
	ReportAuthor string
	PhotosURL    string
}

type pageData struct {
	Events       []event
	LatestEvents []event
}

func main() {
	sourceDir := flag.String("source", "./www", "directory containing the site template and static assets")
	imageDir := flag.String("images", "./images", "directory containing source images")
	outputDir := flag.String("output", "./output", "directory to write the generated site")
	configPath := flag.String("config", "./secrets.json", "path to Google Sheets configuration")
	flag.Parse()

	if err := generate(*sourceDir, *imageDir, *outputDir, *configPath); err != nil {
		fmt.Fprintln(os.Stderr, "site generation failed:", err)
		os.Exit(1)
	}
}

func generate(sourceDir, imageDir, outputDir, configPath string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	return generateWithDependencies(sourceDir, imageDir, outputDir, configPath, client, sheetsAPIURL, leafletAssetsBaseURL)
}

func generateWithDependencies(sourceDir, imageDir, outputDir, configPath string, client *http.Client, apiURL, leafletBaseURL string) error {
	settings, err := readConfig(configPath)
	if err != nil {
		return err
	}
	events, err := fetchEvents(context.Background(), client, apiURL, settings)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	templatePath := filepath.Join(sourceDir, "index.html")
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read HTML template %q: %w", templatePath, err)
	}

	displayEvents, err := prepareEvents(events)
	if err != nil {
		return err
	}
	data := pageData{Events: displayEvents, LatestEvents: latestEvents(displayEvents)}
	rendered, err := renderTemplate(string(templateContent), data)
	if err != nil {
		return fmt.Errorf("render HTML template: %w", err)
	}
	html, err := processImages(rendered, imageDir, filepath.Join(outputDir, "img"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "index.html"), []byte(html), 0o644); err != nil {
		return fmt.Errorf("write generated HTML: %w", err)
	}

	eventsTemplatePath := filepath.Join(sourceDir, "events.html")
	eventsTemplate, err := os.ReadFile(eventsTemplatePath)
	if err != nil {
		return fmt.Errorf("read events page template %q: %w", eventsTemplatePath, err)
	}
	rendered, err = renderTemplate(string(eventsTemplate), data)
	if err != nil {
		return fmt.Errorf("render events page template: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "events.html"), []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write generated events page: %w", err)
	}

	for _, name := range []string{"style.css", "main.js", "course.geojson"} {
		if err := copyFile(filepath.Join(sourceDir, name), filepath.Join(outputDir, name)); err != nil {
			return fmt.Errorf("copy site asset %q: %w", name, err)
		}
	}
	if err := downloadLeafletAssets(client, leafletBaseURL, filepath.Join(outputDir, "leaflet")); err != nil {
		return err
	}

	fmt.Printf("Generated site in %s\n", outputDir)
	return nil
}

func downloadLeafletAssets(client *http.Client, baseURL, outputDir string) error {
	for _, asset := range leafletAssets {
		requestURL := strings.TrimRight(baseURL, "/") + "/" + asset.sourcePath
		response, err := client.Get(requestURL)
		if err != nil {
			return fmt.Errorf("download Leaflet asset %q: %w", asset.sourcePath, err)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			response.Body.Close()
			return fmt.Errorf("download Leaflet asset %q: server returned HTTP %s", asset.sourcePath, response.Status)
		}

		destination := filepath.Join(outputDir, filepath.FromSlash(asset.outputPath))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			response.Body.Close()
			return fmt.Errorf("create directory for Leaflet asset %q: %w", asset.outputPath, err)
		}
		if err := writeFile(destination, response.Body); err != nil {
			response.Body.Close()
			return fmt.Errorf("save Leaflet asset %q: %w", asset.outputPath, err)
		}
		if err := response.Body.Close(); err != nil {
			return fmt.Errorf("finish downloading Leaflet asset %q: %w", asset.outputPath, err)
		}
	}
	return nil
}

func readConfig(path string) (config, error) {
	file, err := os.Open(path)
	if err != nil {
		return config{}, fmt.Errorf("open Google Sheets config %q: %w", path, err)
	}
	defer file.Close()

	var settings config
	if err := json.NewDecoder(file).Decode(&settings); err != nil {
		return config{}, fmt.Errorf("decode Google Sheets config %q: %w", path, err)
	}
	if strings.TrimSpace(settings.Google.APIKey) == "" {
		return config{}, fmt.Errorf("Google Sheets config %q is missing google.api_key", path)
	}
	if strings.TrimSpace(settings.Google.SheetID) == "" {
		return config{}, fmt.Errorf("Google Sheets config %q is missing google.sheet_id", path)
	}
	return settings, nil
}

func fetchEvents(ctx context.Context, client *http.Client, apiURL string, settings config) (sheetData, error) {
	endpoint, err := url.Parse(strings.TrimRight(apiURL, "/"))
	if err != nil {
		return sheetData{}, fmt.Errorf("parse Google Sheets API URL: %w", err)
	}
	endpoint.Path += "/v4/spreadsheets/" + settings.Google.SheetID + "/values/'Parkrun'!A:ZZ"

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return sheetData{}, fmt.Errorf("create Google Sheets request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Goog-Api-Key", settings.Google.APIKey)
	response, err := client.Do(request)
	if err != nil {
		return sheetData{}, fmt.Errorf("fetch Parkrun sheet: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return sheetData{}, fmt.Errorf("Google Sheets API returned HTTP %s", response.Status)
	}

	var result struct {
		Values [][]any `json:"values"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 10<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return sheetData{}, fmt.Errorf("decode Parkrun sheet response: %w", err)
	}
	events, err := parseSheetValues(result.Values, time.Now())
	if err != nil {
		return sheetData{}, fmt.Errorf("parse Parkrun sheet rows: %w", err)
	}
	return events, nil
}

func parseSheetValues(values [][]any, today time.Time) (sheetData, error) {
	if len(values) == 0 {
		return sheetData{}, nil
	}

	columnCount := 0
	for _, row := range values {
		columnCount = max(columnCount, len(row))
	}
	headers := make([]string, columnCount)
	for column := range headers {
		if column < len(values[0]) {
			headers[column] = strings.TrimSpace(sheetCell(values[0][column]))
		}
		if headers[column] == "" {
			headers[column] = fmt.Sprintf("Spalte %d", column+1)
		}
	}

	indexColumn := findColumn(headers, "index")
	dateColumn := findColumn(headers, "date", "event date", "datum", "veranstaltungsdatum")
	if indexColumn < 0 {
		return sheetData{}, fmt.Errorf("missing INDEX column")
	}
	if dateColumn < 0 {
		return sheetData{}, fmt.Errorf("missing date column")
	}

	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	type eventRow struct {
		values []string
		date   time.Time
	}
	rows := make([]eventRow, 0, len(values)-1)
	for rowIndex, sourceRow := range values[1:] {
		row := make([]string, columnCount)
		for column, value := range sourceRow {
			row[column] = sheetCell(value)
		}
		indexValue := strings.TrimSpace(row[indexColumn])
		if indexValue == "" {
			continue
		}
		if _, err := strconv.Atoi(indexValue); err != nil {
			continue
		}
		dateValue := strings.TrimSpace(row[dateColumn])
		if dateValue == "" {
			continue
		}
		eventDate, err := parseEventDate(dateValue, today.Location())
		if err != nil {
			return sheetData{}, fmt.Errorf("row %d has invalid date %q: %w", rowIndex+2, dateValue, err)
		}
		if eventDate.After(today) {
			continue
		}
		rows = append(rows, eventRow{values: row, date: eventDate})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].date.After(rows[j].date)
	})
	sortedRows := make([][]string, len(rows))
	for index := range rows {
		sortedRows[index] = rows[index].values
	}
	return sheetData{Headers: headers, Rows: sortedRows}, nil
}

func prepareEvents(data sheetData) ([]event, error) {
	columns := map[string]int{}
	for _, name := range []string{"INDEX", "DATE", "RUNNERS", "RESULTS", "REPORT", "AUTHOR", "PHOTOS"} {
		index := findColumn(data.Headers, name)
		if index < 0 {
			return nil, fmt.Errorf("missing %s column in Parkrun sheet", name)
		}
		columns[name] = index
	}

	events := make([]event, 0, len(data.Rows))
	for _, row := range data.Rows {
		cell := func(name string) string {
			return strings.TrimSpace(row[columns[name]])
		}
		index := cell("INDEX")
		events = append(events, event{
			Index:        index,
			Date:         cell("DATE"),
			Runners:      cell("RUNNERS"),
			ResultsURL:   "https://www.parkrun.com.de/dietenbach/results/" + index + "/",
			ReportURL:    cell("REPORT"),
			ReportAuthor: cell("AUTHOR"),
			PhotosURL:    cell("PHOTOS"),
		})
	}
	return events, nil
}

func latestEvents(rows []event) []event {
	if len(rows) > latestEventCount {
		rows = rows[:latestEventCount]
	}
	return rows
}

func findColumn(headers []string, names ...string) int {
	for index, header := range headers {
		for _, name := range names {
			if strings.EqualFold(strings.TrimSpace(header), name) {
				return index
			}
		}
	}
	return -1
}

func parseEventDate(value string, location *time.Location) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02",
		"02.01.2006",
		"2.1.2006",
		"02/01/2006",
		"2/1/2006",
		"Jan 2, 2006",
		"January 2, 2006",
		time.RFC3339,
	} {
		date, err := time.ParseInLocation(layout, value, location)
		if err == nil {
			return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location), nil
		}
	}
	return time.Time{}, fmt.Errorf("expected a supported date format")
}

func sheetCell(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func renderTemplate(source string, data pageData) (string, error) {
	parsed, err := template.New("index").Parse(source)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func processImages(html, imageDir, outputImageDir string) (string, error) {
	matches := imageSource.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return html, nil
	}
	if err := os.MkdirAll(outputImageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image directory: %w", err)
	}

	processed := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := filepath.Clean(filepath.FromSlash(match[2]))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("invalid image path %q", match[2])
		}
		if _, done := processed[name]; done {
			continue
		}
		processed[name] = struct{}{}
		if err := processImage(filepath.Join(imageDir, name), filepath.Join(outputImageDir, name)); err != nil {
			return "", err
		}
	}

	return imageSource.ReplaceAllString(html, `${1}img/${2}${3}`), nil
}

func processImage(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open image %q: %w", source, err)
	}
	defer input.Close()

	decoded, err := jpeg.Decode(input)
	if err != nil {
		return fmt.Errorf("decode JPEG image %q: %w", source, err)
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxImageDimension && height <= maxImageDimension {
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind image %q: %w", source, err)
		}
		return writeFile(destination, input)
	}

	scale := float64(maxImageDimension) / float64(max(width, height))
	resizedWidth := max(1, int(float64(width)*scale))
	resizedHeight := max(1, int(float64(height)*scale))
	resized := resizeBilinear(decoded, resizedWidth, resizedHeight)

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create image directory for %q: %w", destination, err)
	}
	output, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("create resized image %q: %w", destination, err)
	}
	if err := jpeg.Encode(output, resized, &jpeg.Options{Quality: 82}); err != nil {
		output.Close()
		return fmt.Errorf("encode resized image %q: %w", destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("finish resized image %q: %w", destination, err)
	}
	return nil
}

func resizeBilinear(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceY := (float64(y)+0.5)*float64(bounds.Dy())/float64(height) - 0.5
		y0 := max(0, int(sourceY))
		y1 := min(bounds.Dy()-1, y0+1)
		yWeight := max(0, min(1, sourceY-float64(y0)))
		for x := 0; x < width; x++ {
			sourceX := (float64(x)+0.5)*float64(bounds.Dx())/float64(width) - 0.5
			x0 := max(0, int(sourceX))
			x1 := min(bounds.Dx()-1, x0+1)
			xWeight := max(0, min(1, sourceX-float64(x0)))

			topLeft := source.At(bounds.Min.X+x0, bounds.Min.Y+y0)
			topRight := source.At(bounds.Min.X+x1, bounds.Min.Y+y0)
			bottomLeft := source.At(bounds.Min.X+x0, bounds.Min.Y+y1)
			bottomRight := source.At(bounds.Min.X+x1, bounds.Min.Y+y1)
			result.SetRGBA(x, y, color.RGBA{
				R: interpolate(topLeft, topRight, bottomLeft, bottomRight, xWeight, yWeight, 0),
				G: interpolate(topLeft, topRight, bottomLeft, bottomRight, xWeight, yWeight, 1),
				B: interpolate(topLeft, topRight, bottomLeft, bottomRight, xWeight, yWeight, 2),
				A: 255,
			})
		}
	}
	return result
}

func interpolate(topLeft, topRight, bottomLeft, bottomRight color.Color, xWeight, yWeight float64, channel int) uint8 {
	component := func(value color.Color) float64 {
		r, g, b, _ := value.RGBA()
		switch channel {
		case 0:
			return float64(r) / 257
		case 1:
			return float64(g) / 257
		default:
			return float64(b) / 257
		}
	}
	top := component(topLeft)*(1-xWeight) + component(topRight)*xWeight
	bottom := component(bottomLeft)*(1-xWeight) + component(bottomRight)*xWeight
	return uint8(top*(1-yWeight) + bottom*yWeight + 0.5)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	return writeFile(destination, input)
}

func writeFile(destination string, input io.Reader) error {
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return nil
}
