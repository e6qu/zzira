package apps

import (
	"encoding/json"
	"fmt"
	"net/url"
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

type connectIssueFieldWire struct {
	Key         string          `json:"key"`
	Name        connectNameWire `json:"name"`
	Description connectNameWire `json:"description"`
	Type        string          `json:"type"`
}

type connectRemoteModuleWire struct {
	Key      string          `json:"key"`
	URL      string          `json:"url"`
	Location string          `json:"location"`
	Name     connectNameWire `json:"name"`
}

type connectIssueTabPanelWire struct {
	Key        string            `json:"key"`
	URL        string            `json:"url"`
	Name       connectNameWire   `json:"name"`
	Weight     int               `json:"weight"`
	Params     map[string]string `json:"params"`
	Conditions []json.RawMessage `json:"conditions"`
}

type connectAdminPageWire struct {
	Key        string            `json:"key"`
	URL        string            `json:"url"`
	Name       connectNameWire   `json:"name"`
	Location   string            `json:"location"`
	Weight     int               `json:"weight"`
	Params     map[string]string `json:"params"`
	Cacheable  bool              `json:"cacheable"`
	FullPage   bool              `json:"fullPage"`
	Conditions []json.RawMessage `json:"conditions"`
}

type connectWebItemWire struct {
	connectRemoteModuleWire
	Conditions []json.RawMessage `json:"conditions"`
}

type connectIssueContentWire struct {
	Key     string          `json:"key"`
	Name    connectNameWire `json:"name"`
	Tooltip connectNameWire `json:"tooltip"`
	Icon    struct {
		URL string `json:"url"`
	} `json:"icon"`
	Target struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"target"`
	ContentPresentConditions []json.RawMessage `json:"contentPresentConditions"`
}

type connectIssueContextWire struct {
	Key  string          `json:"key"`
	Name connectNameWire `json:"name"`
	Icon struct {
		URL string `json:"url"`
	} `json:"icon"`
	Content struct {
		Type  string `json:"type"`
		Label struct {
			Value string `json:"value"`
		} `json:"label"`
	} `json:"content"`
	Target struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"target"`
	Conditions []json.RawMessage `json:"conditions"`
}

type connectIssueContextMeta struct {
	IconURL string `json:"iconUrl"`
	Label   string `json:"label"`
}

type connectProjectPageWire struct {
	Key        string            `json:"key"`
	URL        string            `json:"url"`
	IconURL    string            `json:"iconUrl"`
	Name       connectNameWire   `json:"name"`
	Weight     int               `json:"weight"`
	Conditions []json.RawMessage `json:"conditions"`
}

type connectProjectPageMeta struct {
	IconURL string `json:"iconUrl"`
}

type connectProjectAdminPageWire struct {
	Key        string            `json:"key"`
	URL        string            `json:"url"`
	Location   string            `json:"location"`
	Name       connectNameWire   `json:"name"`
	Weight     int               `json:"weight"`
	Params     map[string]string `json:"params"`
	Conditions []json.RawMessage `json:"conditions"`
}

type connectReportWire struct {
	Key            string          `json:"key"`
	URL            string          `json:"url"`
	Name           connectNameWire `json:"name"`
	Description    connectNameWire `json:"description"`
	ReportCategory string          `json:"reportCategory"`
	ThumbnailURL   string          `json:"thumbnailUrl"`
}

type connectReportMeta struct {
	Description  string `json:"description"`
	Category     string `json:"category"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}

type connectDashboardItemWire struct {
	Key          string            `json:"key"`
	URL          string            `json:"url"`
	Name         connectNameWire   `json:"name"`
	Description  connectNameWire   `json:"description"`
	ThumbnailURL string            `json:"thumbnailUrl"`
	Configurable bool              `json:"configurable"`
	Refreshable  bool              `json:"refreshable"`
	Conditions   []json.RawMessage `json:"conditions"`
}

type connectDashboardItemMeta struct {
	Description  string `json:"description"`
	ThumbnailURL string `json:"thumbnailUrl"`
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
	supported := map[string]bool{"adminPages": true, "generalPages": true, "jiraProjectPages": true, "jiraProjectAdminTabPanels": true, "jiraReports": true, "jiraDashboardItems": true, "jiraIssueTabPanels": true, "webPanels": true, "contentBylineItems": true, "webhooks": true, "jiraIssueFields": true, "jiraJqlFunctions": true, "webItems": true, "jiraIssueContents": true, "jiraIssueContexts": true, "jiraIssueGlances": true}
	for moduleType, payload := range connect.Modules {
		if !supported[moduleType] {
			return models.AppDescriptor{}, fmt.Errorf("Connect module %q is not supported yet", moduleType)
		}
		switch moduleType {
		case "adminPages":
			var modules []connectAdminPageWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect adminPages: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectAdminPage(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraIssueTabPanels":
			var modules []connectIssueTabPanelWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraIssueTabPanels: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectIssueTabPanel(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraIssueGlances":
			var modules []connectIssueContextWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraIssueGlances: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectIssueViewContext(module, "jira:issueGlance")
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraIssueContexts":
			var modules []connectIssueContextWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraIssueContexts: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectIssueContext(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraDashboardItems":
			var modules []connectDashboardItemWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraDashboardItems: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectDashboardItem(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraReports":
			var modules []connectReportWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraReports: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectReport(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraProjectAdminTabPanels":
			var modules []connectProjectAdminPageWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraProjectAdminTabPanels: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectProjectAdminPage(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraProjectPages":
			var modules []connectProjectPageWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraProjectPages: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectProjectPage(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraIssueContents":
			var modules []connectIssueContentWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraIssueContents: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectIssueContent(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
		case "jiraIssueFields":
			var fields []connectIssueFieldWire
			if err := json.Unmarshal(payload, &fields); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraIssueFields: %w", err)
			}
			for _, field := range fields {
				translated, err := translateConnectIssueField(field, false)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.IssueFields = append(wire.IssueFields, translated)
			}
		case "jiraJqlFunctions":
			var functions []jqlFunctionWire
			if err := json.Unmarshal(payload, &functions); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect jiraJqlFunctions: %w", err)
			}
			wire.JQLFunctions = append(wire.JQLFunctions, functions...)
		case "webItems":
			var modules []connectWebItemWire
			if err := json.Unmarshal(payload, &modules); err != nil {
				return models.AppDescriptor{}, fmt.Errorf("invalid Connect webItems: %w", err)
			}
			for _, module := range modules {
				translated, err := translateConnectWebItem(module)
				if err != nil {
					return models.AppDescriptor{}, err
				}
				wire.Modules = append(wire.Modules, translated)
			}
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

func translateConnectAdminPage(module connectAdminPageWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	module.URL = strings.TrimSpace(module.URL)
	module.Location = strings.TrimSpace(module.Location)
	if module.Location == "" {
		module.Location = "advanced_menu_section/advanced_section"
	}
	if !connectModuleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || len(module.Name.Value) > 1500 || !validAppCallbackPath(module.URL) {
		return moduleWire{}, fmt.Errorf("Connect admin page needs a valid key, name, and relative URL")
	}
	if module.Location != "advanced_menu_section/advanced_section" {
		return moduleWire{}, fmt.Errorf("Connect admin page %q uses unsupported location %q", module.Key, module.Location)
	}
	if module.Cacheable || module.FullPage || len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect admin page %q uses unsupported cacheable, fullPage, or conditions behavior", module.Key)
	}
	var err error
	if module.URL, err = appendConnectParams(module.URL, module.Params); err != nil {
		return moduleWire{}, fmt.Errorf("Connect admin page %q: %w", module.Key, err)
	}
	weight := module.Weight
	if weight == 0 {
		weight = 100
	}
	return moduleWire{Key: module.Key, Type: "jira:adminPage", Location: "jira.admin", Title: module.Name.Value, URL: module.URL, Position: weight}, nil
}

func translateConnectIssueTabPanel(module connectIssueTabPanelWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	module.URL = strings.TrimSpace(module.URL)
	if !connectModuleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || len(module.Name.Value) > 1500 || !validAppCallbackPath(module.URL) {
		return moduleWire{}, fmt.Errorf("Connect issue tab panel needs a valid key, name, and relative URL")
	}
	if len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect issue tab panel %q uses unsupported conditions", module.Key)
	}
	var err error
	if module.URL, err = appendConnectParams(module.URL, module.Params); err != nil {
		return moduleWire{}, fmt.Errorf("Connect issue tab panel %q: %w", module.Key, err)
	}
	weight := module.Weight
	if weight == 0 {
		weight = 100
	}
	return moduleWire{Key: module.Key, Type: "jira:issueTabPanel", Location: "jira.issue.activity", Title: module.Name.Value, URL: module.URL, Position: weight}, nil
}

func translateConnectIssueContext(module connectIssueContextWire) (moduleWire, error) {
	return translateConnectIssueViewContext(module, "jira:issueContext")
}

func translateConnectIssueViewContext(module connectIssueContextWire, translatedType string) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	module.Icon.URL = strings.TrimSpace(module.Icon.URL)
	module.Content.Type = strings.TrimSpace(module.Content.Type)
	module.Content.Label.Value = strings.TrimSpace(module.Content.Label.Value)
	module.Target.Type = strings.TrimSpace(module.Target.Type)
	module.Target.URL = strings.TrimSpace(module.Target.URL)
	icon := module.Icon.URL
	if icon != "" && !strings.HasPrefix(icon, "/") {
		icon = "/" + icon
	}
	if !moduleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || len(module.Name.Value) > 1500 || module.Content.Type != "label" || module.Content.Label.Value == "" || len(module.Content.Label.Value) > 1500 || module.Target.Type != "web_panel" || !validAppCallbackPath(module.Target.URL) || !validAppCallbackPath(icon) {
		return moduleWire{}, fmt.Errorf("Connect issue context or glance needs a valid key, name, label content, relative icon, and web_panel target")
	}
	if len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect issue context %q uses unsupported conditions", module.Key)
	}
	meta, _ := json.Marshal(connectIssueContextMeta{IconURL: module.Icon.URL, Label: module.Content.Label.Value})
	return moduleWire{Key: module.Key, Type: translatedType, Location: "jira.issue.context", Title: module.Name.Value, Body: string(meta), URL: module.Target.URL}, nil
}

func translateConnectDashboardItem(module connectDashboardItemWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.URL = strings.TrimSpace(module.URL)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	module.Description.Value = strings.TrimSpace(module.Description.Value)
	module.ThumbnailURL = strings.TrimSpace(module.ThumbnailURL)
	thumbnail := module.ThumbnailURL
	if thumbnail != "" && !strings.HasPrefix(thumbnail, "/") {
		thumbnail = "/" + thumbnail
	}
	if !moduleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || module.Description.Value == "" || len(module.Name.Value) > 1500 || len(module.Description.Value) > 1500 || !validAppCallbackPath(module.URL) || !validAppCallbackPath(thumbnail) {
		return moduleWire{}, fmt.Errorf("Connect dashboard item needs a valid key, name, description, relative URL, and thumbnailUrl")
	}
	if module.Configurable || module.Refreshable || len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect dashboard item %q uses unsupported configuration, refresh, or conditions", module.Key)
	}
	meta, _ := json.Marshal(connectDashboardItemMeta{Description: module.Description.Value, ThumbnailURL: module.ThumbnailURL})
	return moduleWire{Key: module.Key, Type: "jira:dashboardGadget", Location: "jira.dashboard", Title: module.Name.Value, Body: string(meta), URL: module.URL}, nil
}

func translateConnectReport(module connectReportWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.URL = strings.TrimSpace(module.URL)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	module.Description.Value = strings.TrimSpace(module.Description.Value)
	module.ReportCategory = strings.ToLower(strings.TrimSpace(module.ReportCategory))
	module.ThumbnailURL = strings.TrimSpace(module.ThumbnailURL)
	if module.ReportCategory == "" {
		module.ReportCategory = "other"
	}
	if !moduleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || module.Description.Value == "" || len(module.Name.Value) > 1500 || len(module.Description.Value) > 1500 || !validAppCallbackPath(module.URL) || !map[string]bool{"agile": true, "issue_analysis": true, "forecast_management": true, "other": true}[module.ReportCategory] || (module.ThumbnailURL != "" && !validAppCallbackPath(module.ThumbnailURL)) {
		return moduleWire{}, fmt.Errorf("Connect report needs a valid key, name, description, relative URL, category, and optional relative thumbnailUrl")
	}
	meta, _ := json.Marshal(connectReportMeta{Description: module.Description.Value, Category: module.ReportCategory, ThumbnailURL: module.ThumbnailURL})
	return moduleWire{Key: module.Key, Type: "jira:report", Location: "jira.report", Title: module.Name.Value, Body: string(meta), URL: module.URL}, nil
}

func translateConnectProjectAdminPage(module connectProjectAdminPageWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.URL = strings.TrimSpace(module.URL)
	module.Location = strings.TrimSpace(module.Location)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	group := map[string]int{"projectgroup1": 1000, "projectgroup2": 2000, "projectgroup3": 3000, "projectgroup4": 4000}[module.Location]
	if !moduleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || len(module.Name.Value) > 1500 || !validAppCallbackPath(module.URL) || group == 0 {
		return moduleWire{}, fmt.Errorf("Connect project admin tab needs a valid key, name, relative URL, and projectgroup1 through projectgroup4 location")
	}
	if len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect project admin tab %q uses unsupported conditions", module.Key)
	}
	var err error
	if module.URL, err = appendConnectParams(module.URL, module.Params); err != nil {
		return moduleWire{}, fmt.Errorf("Connect project admin tab %q: %w", module.Key, err)
	}
	return moduleWire{Key: module.Key, Type: "jira:projectAdminPage", Location: "jira.project.settings", Title: module.Name.Value, URL: module.URL, Position: group + module.Weight}, nil
}

func appendConnectParams(moduleURL string, params map[string]string) (string, error) {
	if len(params) == 0 {
		return moduleURL, nil
	}
	values := url.Values{}
	for key, value := range params {
		if strings.TrimSpace(key) == "" {
			return "", fmt.Errorf("parameter keys cannot be empty")
		}
		values.Set(key, value)
	}
	separator := "?"
	if strings.Contains(moduleURL, "?") {
		separator = "&"
	}
	return moduleURL + separator + values.Encode(), nil
}

func translateConnectProjectPage(module connectProjectPageWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.URL = strings.TrimSpace(module.URL)
	module.IconURL = strings.TrimSpace(module.IconURL)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	if !moduleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || len(module.Name.Value) > 1500 || !validAppCallbackPath(module.URL) || !validAppCallbackPath(module.IconURL) {
		return moduleWire{}, fmt.Errorf("Connect project page needs a valid key, name, relative URL, and relative iconUrl")
	}
	if len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect project page %q uses unsupported conditions", module.Key)
	}
	weight := module.Weight
	if weight == 0 {
		weight = 100
	}
	meta, _ := json.Marshal(connectProjectPageMeta{IconURL: module.IconURL})
	return moduleWire{Key: module.Key, Type: "jira:projectPage", Location: "jira.project.page", Title: module.Name.Value, Body: string(meta), URL: module.URL, Position: weight}, nil
}

func translateConnectIssueContent(module connectIssueContentWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	module.Tooltip.Value = strings.TrimSpace(module.Tooltip.Value)
	module.Icon.URL = strings.TrimSpace(module.Icon.URL)
	module.Target.Type = strings.TrimSpace(module.Target.Type)
	module.Target.URL = strings.TrimSpace(module.Target.URL)
	if !moduleKeyPattern.MatchString(module.Key) || module.Name.Value == "" || len(module.Name.Value) > 255 || module.Tooltip.Value == "" || len(module.Tooltip.Value) > 1500 || module.Icon.URL == "" || module.Target.Type != "web_panel" || !validAppCallbackPath(module.Target.URL) {
		return moduleWire{}, fmt.Errorf("Connect issue content needs a valid key, name, tooltip, icon, and relative web_panel target")
	}
	if !validAppCallbackPath(module.Icon.URL) {
		return moduleWire{}, fmt.Errorf("Connect issue content %q requires a relative icon URL", module.Key)
	}
	if len(module.ContentPresentConditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect issue content %q uses unsupported presence conditions", module.Key)
	}
	return moduleWire{Key: module.Key, Type: "jira:issueContent", Location: "jira.issue.content", Title: module.Name.Value, URL: module.Target.URL}, nil
}

func translateConnectWebItem(module connectWebItemWire) (moduleWire, error) {
	module.Key = strings.TrimSpace(module.Key)
	module.URL = strings.TrimSpace(module.URL)
	module.Location = strings.TrimSpace(module.Location)
	module.Name.Value = strings.TrimSpace(module.Name.Value)
	translated := moduleWire{Key: module.Key, URL: module.URL, Title: module.Name.Value}
	switch module.Location {
	case "system.top.navigation.bar":
		translated.Type, translated.Location = "jira:webItem", "jira.navigation"
	case "system.header/left", "system.header/right":
		translated.Type, translated.Location = "confluence:webItem", "confluence.navigation"
	default:
		return moduleWire{}, fmt.Errorf("Connect web item %q uses unsupported location %q", module.Key, module.Location)
	}
	if len(module.Conditions) > 0 {
		return moduleWire{}, fmt.Errorf("Connect web item %q uses unsupported conditions", module.Key)
	}
	return translated, nil
}

func translateConnectIssueField(field connectIssueFieldWire, dynamic bool) (models.AppIssueField, error) {
	field.Key = strings.TrimSpace(field.Key)
	field.Name.Value = strings.TrimSpace(field.Name.Value)
	field.Description.Value = strings.TrimSpace(field.Description.Value)
	field.Type = strings.ToLower(strings.TrimSpace(field.Type))
	fieldType := ""
	switch field.Type {
	case "string", "text", "rich_text":
		fieldType = models.CustomFieldText
	case "number":
		fieldType = models.CustomFieldNumber
	case "date", "datetime":
		fieldType = models.CustomFieldDatetime
	case "single_select":
		// A multi-select has no equivalent here, so it stays refused rather
		// than being quietly downgraded to a single choice.
		fieldType = models.CustomFieldSelect
	}
	if !moduleKeyPattern.MatchString(field.Key) || field.Name.Value == "" || len(field.Name.Value) > 255 || len(field.Description.Value) > 2000 || fieldType == "" {
		return models.AppIssueField{}, fmt.Errorf("Connect issue field needs a valid key, name, description, and supported string, text, rich_text, single_select, number, date, or datetime type")
	}
	return models.AppIssueField{Key: field.Key, Name: field.Name.Value, Description: field.Description.Value, Type: fieldType, Dynamic: dynamic, Active: true}, nil
}

func connectIssuePanelLocation(location string) bool {
	switch strings.TrimSpace(location) {
	case "atl.jira.view.issue.right.context", "atl.jira.view.issue.left.context", "atl.jira.view.issue.details":
		return true
	default:
		return false
	}
}
