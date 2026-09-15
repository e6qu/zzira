package web

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// wantsCSV reports whether a report request asks for its data as a CSV file.
func wantsCSV(r *http.Request) bool {
	return r.URL.Query().Get("format") == "csv"
}

// csvURL is the request's own report with the same choices, as a CSV file.
func csvURL(r *http.Request) string {
	query := url.Values{}
	for key, values := range r.URL.Query() {
		query[key] = append([]string(nil), values...)
	}
	query.Del("emailError")
	query.Set("format", "csv")
	return r.URL.Path + "?" + query.Encode()
}

var csvFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// spreadsheetSafe keeps a cell from being read as a formula when the file is
// opened in a spreadsheet: text starting with =, +, -, @, a tab or a carriage
// return is prefixed with an apostrophe.
func spreadsheetSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

// writeReportCSV sends rows as a CSV attachment named after the report and
// today's date.
func writeReportCSV(w http.ResponseWriter, name string, header []string, rows [][]string) {
	file := strings.Trim(csvFileName.ReplaceAllString(name, "-"), "-") + "-" + time.Now().UTC().Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if titled, ok := w.(interface{ setReportTitle(string) }); ok {
		titled.setReportTitle(name)
	}
	writer := csv.NewWriter(w)
	_ = writer.Write(header)
	for _, row := range rows {
		safe := make([]string, len(row))
		for index, value := range row {
			safe[index] = spreadsheetSafe(value)
		}
		_ = writer.Write(safe)
	}
	writer.Flush()
}
