package apps

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

type connectDescriptorWire struct {
	Key            string                     `json:"key"`
	Name           string                     `json:"name"`
	BaseURL        string                     `json:"baseUrl"`
	APIVersion     int                        `json:"apiVersion"`
	Authentication connectAuthenticationWire  `json:"authentication"`
	Scopes         []string                   `json:"scopes"`
	Lifecycle      map[string]string          `json:"lifecycle"`
	Modules        map[string]json.RawMessage `json:"modules"`
}

type connectAuthenticationWire struct {
	Type string `json:"type"`
}

type connectNameWire struct {
	Value string `json:"value"`
}

type connectRemoteModuleWire struct {
	Key      string          `json:"key"`
	URL      string          `json:"url"`
	Location string          `json:"location"`
	Name     connectNameWire `json:"name"`
}

type connectWebhookWire struct {
	Key          string          `json:"key"`
	Event        string          `json:"event"`
	URL          string          `json:"url"`
	Filter       string          `json:"filter"`
	ExcludeBody  bool            `json:"excludeBody"`
	PropertyKeys []string        `json:"propertyKeys"`
	Conditions   json.RawMessage `json:"conditions"`
}

func parseConnectDescriptor(raw []byte) (models.AppDescriptor, error) {
	var connect connectDescriptorWire
	if err := json.Unmarshal(raw, &connect); err != nil {
		return models.AppDescriptor{}, fmt.Errorf("invalid Connect descriptor: %w", err)
	}
	authenticationType := strings.TrimSpace(connect.Authentication.Type)
	if authenticationType == "" {
		authenticationType = "jwt"
	}
	if !strings.EqualFold(authenticationType, "jwt") {
		return models.AppDescriptor{}, fmt.Errorf("Connect descriptors must declare JWT authentication")
	}
	apiVersion := connect.APIVersion
	if apiVersion == 0 {
		apiVersion = 1
	}
	if apiVersion != 1 {
		return models.AppDescriptor{}, fmt.Errorf("unsupported Connect apiVersion %d", apiVersion)
	}
	wire := descriptorWire{Key: connect.Key, Name: connect.Name, BaseURL: connect.BaseURL, Version: "connect-v1", Lifecycle: connect.Lifecycle, Format: "connect"}
	scopes := map[string]bool{}
	for _, rawScope := range connect.Scopes {
		scope := strings.ToUpper(strings.TrimSpace(rawScope))
		switch scope {
		case "NONE":
		case "READ":
			scopes["read:jira-work"], scopes["read:confluence-content"] = true, true
		case "WRITE", "DELETE", "PROJECT_ADMIN", "ADMIN":
			scopes["read:jira-work"], scopes["write:jira-work"] = true, true
			scopes["read:confluence-content"], scopes["write:confluence-content"] = true, true
		default:
			return models.AppDescriptor{}, fmt.Errorf("Connect scope %q is not supported", rawScope)
		}
	}
	for scope := range scopes {
		wire.Scopes = append(wire.Scopes, scope)
	}
	sort.Strings(wire.Scopes)
	supported := map[string]bool{"generalPages": true, "webPanels": true, "contentBylineItems": true, "webhooks": true}
	for moduleType, payload := range connect.Modules {
		if !supported[moduleType] {
			return models.AppDescriptor{}, fmt.Errorf("Connect module %q is not supported yet", moduleType)
		}
		switch moduleType {
		case "webhooks":
			var hooks []connectWebhookWire
			if err := json.Unmarshal(payload, &hooks); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect webhooks: %w", err)
			}
			for index, hook := range hooks {
				wire.Webhooks = append(wire.Webhooks, webhookWire{Key: fmt.Sprintf("connect-webhook-%d", index+1), URL: hook.URL, JQL: hook.Filter, Events: []string{hook.Event}})
			}
			scopes["manage:webhooks"] = true
		case "generalPages", "webPanels", "contentBylineItems":
			var modules []connectRemoteModuleWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect %s: %w", moduleType, err)
			}
			for _, module := range modules {
				translated := moduleWire{Key: module.Key, URL: module.URL, Title: module.Name.Value}
				switch moduleType {
				case "generalPages":
					translated.Type, translated.Location = "jira:globalPage", "jira.navigation"
				case "contentBylineItems":
					translated.Type, translated.Location = "confluence:contentBylineItem", "confluence.content.byline"
				case "webPanels":
					if !connectIssuePanelLocation(module.Location) {
						return models.AppDescriptor{}, fmt.Errorf("Connect web panel %q uses unsupported location %q", module.Key, module.Location)
					}
					translated.Type, translated.Location = "jira:issuePanel", "jira.issue.view"
				}
				wire.Modules = append(wire.Modules, translated)
			}
		}
	}
	wire.Scopes = wire.Scopes[:0]
	for scope := range scopes {
		wire.Scopes = append(wire.Scopes, scope)
	}
	return validateDescriptorWire(wire)
}

func connectIssuePanelLocation(location string) bool {
	switch strings.TrimSpace(location) {
	case "atl.jira.view.issue.right.context", "atl.jira.view.issue.left.context", "atl.jira.view.issue.details":
		return true
	default:
		return false
	}
}
