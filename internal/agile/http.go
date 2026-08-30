package agile

import (
	"encoding/json"
	"log"
	"net/http"
)

// jiraError writes the published Jira error shape.
func jiraError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"errorMessages": []string{message}})
}

// jiraFieldError writes {"errorMessages": [...], "errors": {field: msg}}.
func jiraFieldError(w http.ResponseWriter, status int, errors map[string]string) {
	writeJSON(w, status, map[string]any{
		"errorMessages": []string{},
		"errors":        errors,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("agile: encode response: %v", err)
	}
}
