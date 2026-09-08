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

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

var appKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{1,63}$`)
var connectAppKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)
var moduleKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,63}$`)

var allowedScopes = map[string]bool{
	"read:jira-work": true, "write:jira-work": true,
	"read:confluence-content": true, "write:confluence-content": true,
	"read:app-storage": true, "write:app-storage": true,
	"manage:webhooks": true,
}

var allowedAppWebhookEvents = map[string]bool{
	"jira:issue_created": true, "jira:issue_updated": true, "jira:issue_deleted": true,
	"comment_created": true, "comment_deleted": true, "attachment_created": true,
}

var moduleRequirements = map[string]struct {
	Location string
	Scope    string
}{
	"jira:globalPage":              {Location: "jira.navigation", Scope: "read:jira-work"},
	"jira:projectPage":             {Location: "jira.project.page", Scope: "read:jira-work"},
	"jira:projectAdminPage":        {Location: "jira.project.settings", Scope: "read:jira-work"},
	"jira:issuePanel":              {Location: "jira.issue.view", Scope: "read:jira-work"},
	"jira:issueContent":            {Location: "jira.issue.content", Scope: ""},
	"jira:dashboardGadget":         {Location: "jira.dashboard", Scope: "read:jira-work"},
	"jira:webItem":                 {Location: "jira.navigation", Scope: ""},
	"confluence:globalPage":        {Location: "confluence.navigation", Scope: "read:confluence-content"},
	"confluence:contentBylineItem": {Location: "confluence.content.byline", Scope: "read:confluence-content"},
	"confluence:webItem":           {Location: "confluence.navigation", Scope: ""},
}

type descriptorWire struct {
	Key               string                 `json:"key"`
	Name              string                 `json:"name"`
	BaseURL           string                 `json:"baseUrl"`
	Version           string                 `json:"version"`
	Scopes            []string               `json:"scopes"`
	Modules           []moduleWire           `json:"modules"`
	Lifecycle         map[string]string      `json:"lifecycle"`
	Webhooks          []webhookWire          `json:"webhooks"`
	ScheduledTriggers []scheduledTriggerWire `json:"scheduledTriggers"`
	IssueFields       []models.AppIssueField `json:"-"`
	Format            string                 `json:"-"`
}

type webhookWire struct {
	Key    string   `json:"key"`
	URL    string   `json:"url"`
	JQL    string   `json:"jql"`
	Events []string `json:"events"`
}

type scheduledTriggerWire struct {
	Key      string `json:"key"`
	URL      string `json:"url"`
	Interval string `json:"interval"`
}

type moduleWire struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}

func ParseDescriptor(raw []byte) (models.AppDescriptor, error) {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return models.AppDescriptor{}, fmt.Errorf("app descriptor must contain at most 256 KiB")
	}
	var shape struct {
		Modules        json.RawMessage `json:"modules"`
		Authentication json.RawMessage `json:"authentication"`
	}
	if err := json.Unmarshal(raw, &shape); err == nil && (len(shape.Authentication) > 0 || (len(bytes.TrimSpace(shape.Modules)) > 0 && bytes.TrimSpace(shape.Modules)[0] == '{')) {
		return parseConnectDescriptor(raw)
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
	return validateDescriptorWire(wire)
}

func validateDescriptorWire(wire descriptorWire) (models.AppDescriptor, error) {
	wire.Key, wire.Name, wire.BaseURL, wire.Version = strings.TrimSpace(wire.Key), strings.TrimSpace(wire.Name), strings.TrimSpace(wire.BaseURL), strings.TrimSpace(wire.Version)
	if wire.Format == "" {
		wire.Format = "zzira"
	}
	validKey := appKeyPattern.MatchString(wire.Key)
	if wire.Format == "connect" {
		validKey = connectAppKeyPattern.MatchString(wire.Key)
	}
	if !validKey {
		if wire.Format == "connect" {
			return models.AppDescriptor{}, fmt.Errorf("Connect app key must contain 1 to 64 letters, numbers, dots, underscores, or hyphens")
		}
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
	descriptor := models.AppDescriptor{Key: wire.Key, Name: wire.Name, BaseURL: baseURL.String(), Version: wire.Version, Format: wire.Format}
	for scope := range scopes {
		descriptor.Scopes = append(descriptor.Scopes, scope)
	}
	sort.Strings(descriptor.Scopes)
	issueFieldKeys := map[string]bool{}
	for _, field := range wire.IssueFields {
		if issueFieldKeys[field.Key] {
			return models.AppDescriptor{}, fmt.Errorf("Connect issue field keys must be unique")
		}
		issueFieldKeys[field.Key] = true
		descriptor.IssueFields = append(descriptor.IssueFields, field)
	}
	moduleKeys := issueFieldKeys
	for _, input := range wire.Modules {
		input.Key, input.Type, input.Location, input.Title, input.URL = strings.TrimSpace(input.Key), strings.TrimSpace(input.Type), strings.TrimSpace(input.Location), strings.TrimSpace(input.Title), strings.TrimSpace(input.URL)
		requirement, ok := moduleRequirements[input.Type]
		if !ok || requirement.Location != input.Location {
			return models.AppDescriptor{}, fmt.Errorf("module %q has an unsupported type or location", input.Key)
		}
		if requirement.Scope != "" && !scopes[requirement.Scope] {
			return models.AppDescriptor{}, fmt.Errorf("module %q requires scope %s", input.Key, requirement.Scope)
		}
		if !moduleKeyPattern.MatchString(input.Key) || moduleKeys[input.Key] || input.Title == "" || len(input.Title) > 255 || len(input.Body) > 20000 || (input.Body == "" && !validAppCallbackPath(input.URL)) || (input.Body != "" && input.URL != "") {
			return models.AppDescriptor{}, fmt.Errorf("module keys must be unique and valid; title and body limits must be respected")
		}
		moduleKeys[input.Key] = true
		descriptor.Modules = append(descriptor.Modules, models.AppModule{Key: input.Key, Type: input.Type, Location: input.Location, Title: input.Title, Body: input.Body, RemoteURL: input.URL, Position: input.Position})
	}
	descriptor.Lifecycle = map[string]string{}
	for event, path := range wire.Lifecycle {
		if !map[string]bool{"installed": true, "enabled": true, "disabled": true, "upgraded": true, "uninstalled": true}[event] || !validAppCallbackPath(path) {
			return models.AppDescriptor{}, fmt.Errorf("lifecycle callbacks need a supported event and a relative URL path")
		}
		descriptor.Lifecycle[event] = path
	}
	if len(wire.Webhooks) > 20 {
		return models.AppDescriptor{}, fmt.Errorf("an app may declare at most 20 webhooks")
	}
	if len(wire.Webhooks) > 0 && !scopes["manage:webhooks"] {
		return models.AppDescriptor{}, fmt.Errorf("declarative webhooks require scope manage:webhooks")
	}
	for _, webhook := range wire.Webhooks {
		webhook.Key, webhook.URL, webhook.JQL = strings.TrimSpace(webhook.Key), strings.TrimSpace(webhook.URL), strings.TrimSpace(webhook.JQL)
		if !moduleKeyPattern.MatchString(webhook.Key) || moduleKeys[webhook.Key] || !validAppCallbackPath(webhook.URL) || len(webhook.Events) == 0 || len(webhook.Events) > 20 || len(webhook.JQL) > 2000 {
			return models.AppDescriptor{}, fmt.Errorf("webhooks need a unique key, relative URL, and 1 to 20 events")
		}
		if webhook.JQL != "" {
			if _, err := jql.Parse(webhook.JQL); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("webhook %q has invalid JQL: %w", webhook.Key, err)
			}
		}
		eventSeen := map[string]bool{}
		for index, event := range webhook.Events {
			event = strings.TrimSpace(event)
			if !allowedAppWebhookEvents[event] || eventSeen[event] {
				return models.AppDescriptor{}, fmt.Errorf("webhook %q contains an unsupported or duplicate event", webhook.Key)
			}
			webhook.Events[index] = event
			eventSeen[event] = true
		}
		moduleKeys[webhook.Key] = true
		descriptor.Webhooks = append(descriptor.Webhooks, models.AppWebhook{Key: webhook.Key, Path: webhook.URL, Events: webhook.Events, JQL: webhook.JQL})
	}
	if len(wire.ScheduledTriggers) > 5 {
		return models.AppDescriptor{}, fmt.Errorf("an app may declare at most five scheduled triggers")
	}
	fiveMinute := 0
	for _, trigger := range wire.ScheduledTriggers {
		trigger.Key, trigger.URL, trigger.Interval = strings.TrimSpace(trigger.Key), strings.TrimSpace(trigger.URL), strings.TrimSpace(trigger.Interval)
		if !moduleKeyPattern.MatchString(trigger.Key) || moduleKeys[trigger.Key] || !validAppCallbackPath(trigger.URL) || !map[string]bool{"fiveMinute": true, "hour": true, "day": true, "week": true}[trigger.Interval] {
			return models.AppDescriptor{}, fmt.Errorf("scheduled triggers need a unique key, relative URL, and supported interval")
		}
		if trigger.Interval == "fiveMinute" {
			fiveMinute++
		}
		moduleKeys[trigger.Key] = true
		descriptor.ScheduledTriggers = append(descriptor.ScheduledTriggers, models.AppScheduledTrigger{Key: trigger.Key, Path: trigger.URL, Interval: trigger.Interval})
	}
	if fiveMinute > 1 {
		return models.AppDescriptor{}, fmt.Errorf("an app may declare only one five-minute scheduled trigger")
	}
	return descriptor, nil
}

func validAppCallbackPath(value string) bool {
	if len(value) == 0 || len(value) > 2048 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() == false && parsed.Host == "" && parsed.User == nil && parsed.Fragment == ""
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
