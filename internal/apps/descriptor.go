package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

var appKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{1,63}$`)
var moduleKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,63}$`)

var allowedScopes = map[string]bool{
	"read:jira-work": true, "write:jira-work": true,
	"read:confluence-content": true, "write:confluence-content": true,
	"read:app-storage": true, "write:app-storage": true,
	"manage:webhooks": true,
}

var moduleRequirements = map[string]struct {
	Location string
	Scope    string
}{
	"jira:globalPage":              {Location: "jira.navigation", Scope: "read:jira-work"},
	"jira:issuePanel":              {Location: "jira.issue.view", Scope: "read:jira-work"},
	"jira:dashboardGadget":         {Location: "jira.dashboard", Scope: "read:jira-work"},
	"confluence:globalPage":        {Location: "confluence.navigation", Scope: "read:confluence-content"},
	"confluence:contentBylineItem": {Location: "confluence.content.byline", Scope: "read:confluence-content"},
}

type descriptorWire struct {
	Key     string       `json:"key"`
	Name    string       `json:"name"`
	BaseURL string       `json:"baseUrl"`
	Version string       `json:"version"`
	Scopes  []string     `json:"scopes"`
	Modules []moduleWire `json:"modules"`
}

type moduleWire struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

func ParseDescriptor(raw []byte) (models.AppDescriptor, error) {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return models.AppDescriptor{}, fmt.Errorf("app descriptor must contain at most 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire descriptorWire
	if err := decoder.Decode(&wire); err != nil {
		return models.AppDescriptor{}, fmt.Errorf("invalid app descriptor: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return models.AppDescriptor{}, err
	}
	wire.Key, wire.Name, wire.BaseURL, wire.Version = strings.TrimSpace(wire.Key), strings.TrimSpace(wire.Name), strings.TrimSpace(wire.BaseURL), strings.TrimSpace(wire.Version)
	if !appKeyPattern.MatchString(wire.Key) {
		return models.AppDescriptor{}, fmt.Errorf("app key must be 2 to 64 lowercase letters, numbers, dots, or hyphens")
	}
	if wire.Name == "" || len(wire.Name) > 255 || wire.Version == "" || len(wire.Version) > 64 {
		return models.AppDescriptor{}, fmt.Errorf("app name and version are required")
	}
	baseURL, err := url.Parse(wire.BaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.Fragment != "" {
		return models.AppDescriptor{}, fmt.Errorf("app baseUrl must be an absolute HTTPS URL without credentials or a fragment")
	}
	scopes := map[string]bool{}
	for _, scope := range wire.Scopes {
		scope = strings.TrimSpace(scope)
		if !allowedScopes[scope] {
			return models.AppDescriptor{}, fmt.Errorf("unsupported app scope %q", scope)
		}
		scopes[scope] = true
	}
	descriptor := models.AppDescriptor{Key: wire.Key, Name: wire.Name, BaseURL: baseURL.String(), Version: wire.Version}
	for scope := range scopes {
		descriptor.Scopes = append(descriptor.Scopes, scope)
	}
	sort.Strings(descriptor.Scopes)
	moduleKeys := map[string]bool{}
	for _, input := range wire.Modules {
		input.Key, input.Type, input.Location, input.Title = strings.TrimSpace(input.Key), strings.TrimSpace(input.Type), strings.TrimSpace(input.Location), strings.TrimSpace(input.Title)
		requirement, ok := moduleRequirements[input.Type]
		if !ok || requirement.Location != input.Location {
			return models.AppDescriptor{}, fmt.Errorf("module %q has an unsupported type or location", input.Key)
		}
		if !scopes[requirement.Scope] {
			return models.AppDescriptor{}, fmt.Errorf("module %q requires scope %s", input.Key, requirement.Scope)
		}
		if !moduleKeyPattern.MatchString(input.Key) || moduleKeys[input.Key] || input.Title == "" || len(input.Title) > 255 || len(input.Body) > 20000 {
			return models.AppDescriptor{}, fmt.Errorf("module keys must be unique and valid; title and body limits must be respected")
		}
		moduleKeys[input.Key] = true
		descriptor.Modules = append(descriptor.Modules, models.AppModule{Key: input.Key, Type: input.Type, Location: input.Location, Title: input.Title, Body: input.Body})
	}
	return descriptor, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("app descriptor must contain one JSON object")
		}
		return fmt.Errorf("invalid app descriptor: %w", err)
	}
	return nil
}
