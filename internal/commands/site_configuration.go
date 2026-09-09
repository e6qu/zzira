package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
)

var ErrSiteConfigurationValidation = errors.New("site configuration validation")

func (s *Service) requireSiteAdmin(ctx context.Context, workspaceID, actorID string) error {
	ok, err := authz.IsWorkspaceAdmin(ctx, s.Store, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("administer Jira permission is required")
	}
	return nil
}

func (s *Service) jiraSiteConfiguration(ctx context.Context, workspaceID string) (*models.JiraSiteConfiguration, error) {
	return s.Store.JiraSiteConfiguration(ctx, workspaceID)
}

func validation(message string) error {
	return fmt.Errorf("%w: %s", ErrSiteConfigurationValidation, message)
}

func (s *Service) UpdateAnnouncementBanner(ctx context.Context, workspaceID, actorID string, banner models.AnnouncementBanner) error {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	banner.Message = strings.TrimSpace(banner.Message)
	if banner.Visibility != "public" && banner.Visibility != "private" {
		return validation("visibility must be public or private")
	}
	if utf8.RuneCountInString(banner.Message) > 65000 {
		return validation("message must be at most 65000 characters")
	}
	if banner.IsEnabled && banner.Message == "" {
		return validation("message is required when the banner is enabled")
	}
	return s.Store.UpdateAnnouncementBanner(ctx, workspaceID, actorID, banner)
}

func (s *Service) UpdateGlobalJiraConfiguration(ctx context.Context, workspaceID, actorID string, cfg models.JiraSiteConfiguration) error {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	return s.Store.UpdateGlobalJiraConfiguration(ctx, workspaceID, actorID, cfg)
}

func (s *Service) SelectTimeTrackingProvider(ctx context.Context, workspaceID, actorID, key string) error {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	if key != "Jira" {
		return validation("time tracking provider was not found")
	}
	return s.Store.UpdateTimeTrackingProvider(ctx, workspaceID, actorID, key)
}

func (s *Service) UpdateTimeTrackingOptions(ctx context.Context, workspaceID, actorID string, cfg models.TimeTrackingConfiguration) error {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	if cfg.WorkingHoursPerDay <= 0 || cfg.WorkingHoursPerDay > 24 {
		return validation("workingHoursPerDay must be greater than 0 and at most 24")
	}
	if cfg.WorkingDaysPerWeek <= 0 || cfg.WorkingDaysPerWeek > 7 {
		return validation("workingDaysPerWeek must be greater than 0 and at most 7")
	}
	if cfg.TimeFormat != "pretty" && cfg.TimeFormat != "days" && cfg.TimeFormat != "hours" {
		return validation("timeFormat must be pretty, days, or hours")
	}
	if cfg.DefaultUnit != "minute" && cfg.DefaultUnit != "hour" && cfg.DefaultUnit != "day" && cfg.DefaultUnit != "week" {
		return validation("defaultUnit must be minute, hour, day, or week")
	}
	return s.Store.UpdateTimeTrackingOptions(ctx, workspaceID, actorID, cfg)
}

func (s *Service) UpdateApplicationProperty(ctx context.Context, workspaceID, actorID, key, value string) error {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	property, ok := JiraApplicationPropertyDefinition(key)
	if !ok {
		return validation("application property was not found")
	}
	if len(value) > 8192 {
		return validation("application property value must be at most 8192 bytes")
	}
	if len(property.AllowedValues) > 0 {
		valid := false
		for _, allowed := range property.AllowedValues {
			if value == allowed {
				valid = true
				break
			}
		}
		if !valid {
			return validation("application property value is not allowed")
		}
	}
	return s.Store.UpdateApplicationProperty(ctx, workspaceID, actorID, key, value)
}

func (s *Service) UpdateNavigatorColumns(ctx context.Context, workspaceID, actorID string, columns []string) error {
	if err := s.requireSiteAdmin(ctx, workspaceID, actorID); err != nil {
		return err
	}
	if len(columns) > 50 {
		return validation("at most 50 columns are allowed")
	}
	allowed := map[string]bool{"issuekey": true, "summary": true, "description": true, "issuetype": true, "priority": true, "status": true, "assignee": true, "reporter": true, "created": true, "updated": true, "fixVersions": true, "versions": true, "components": true, "labels": true}
	custom, err := s.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, field := range custom {
		allowed[field.ID] = true
	}
	seen := map[string]bool{}
	for _, column := range columns {
		if !allowed[column] {
			return validation("column " + column + " was not found")
		}
		if seen[column] {
			return validation("columns must not contain duplicates")
		}
		seen[column] = true
	}
	return s.Store.UpdateNavigatorColumns(ctx, workspaceID, actorID, columns)
}

var jiraApplicationPropertyCatalog = []models.ApplicationProperty{
	{ID: "jira.clone.prefix", Key: "jira.clone.prefix", Name: "Clone prefix", Description: "The text prefixed to the summary of cloned work items.", Type: "string", DefaultValue: "CLONE -"},
	{ID: "jira.date.picker.java.format", Key: "jira.date.picker.java.format", Name: "Server date picker format", Description: "The server-side date picker format.", Type: "string", DefaultValue: "d/MMM/yy"},
	{ID: "jira.date.picker.javascript.format", Key: "jira.date.picker.javascript.format", Name: "Browser date picker format", Description: "The browser date picker format.", Type: "string", DefaultValue: "%e/%b/%y"},
	{ID: "jira.date.time.picker.java.format", Key: "jira.date.time.picker.java.format", Name: "Server date-time picker format", Description: "The server-side date and time picker format.", Type: "string", DefaultValue: "dd/MMM/yy h:mm a"},
	{ID: "jira.date.time.picker.javascript.format", Key: "jira.date.time.picker.javascript.format", Name: "Browser date-time picker format", Description: "The browser date and time picker format.", Type: "string", DefaultValue: "%e/%b/%y %I:%M %p"},
	{ID: "jira.issue.actions.order", Key: "jira.issue.actions.order", Name: "Work item activity order", Description: "The default order of comments and history.", Type: "string", DefaultValue: "asc", AllowedValues: []string{"asc", "desc"}},
	{ID: "jira.view.issue.links.sort.order", Key: "jira.view.issue.links.sort.order", Name: "Work item link order", Description: "The sort order for work item links.", Type: "string", DefaultValue: "type, status, priority"},
	{ID: "jira.comment.collapsing.minimum.hidden", Key: "jira.comment.collapsing.minimum.hidden", Name: "Collapsed comment threshold", Description: "The number of comments required before comments collapse.", Type: "number", DefaultValue: "4"},
	{ID: "jira.newsletter.tip.delay.days", Key: "jira.newsletter.tip.delay.days", Name: "Newsletter prompt delay", Description: "Days before showing the newsletter prompt; -1 disables it.", Type: "number", DefaultValue: "7"},
	{ID: "jira.lf.date.time", Key: "jira.lf.date.time", Name: "Time display format", Description: "The display format for times.", Type: "string", DefaultValue: "h:mm a"},
	{ID: "jira.lf.date.day", Key: "jira.lf.date.day", Name: "Day display format", Description: "The display format for days.", Type: "string", DefaultValue: "EEEE h:mm a"},
	{ID: "jira.lf.date.complete", Key: "jira.lf.date.complete", Name: "Complete date format", Description: "The complete date and time display format.", Type: "string", DefaultValue: "dd/MMM/yy h:mm a"},
	{ID: "jira.lf.date.dmy", Key: "jira.lf.date.dmy", Name: "Date display format", Description: "The display format for dates.", Type: "string", DefaultValue: "dd/MMM/yy"},
	{ID: "jira.date.time.picker.use.iso8061", Key: "jira.date.time.picker.use.iso8061", Name: "ISO week", Description: "Use Monday as the first day of the week.", Type: "boolean", DefaultValue: "false", AllowedValues: []string{"true", "false"}},
	{ID: "jira.lf.logo.url", Key: "jira.lf.logo.url", Name: "Logo URL", Description: "The application logo URL.", Type: "string", DefaultValue: "/images/icon-jira-logo.png"},
	{ID: "jira.lf.logo.show.application.title", Key: "jira.lf.logo.show.application.title", Name: "Show application title", Description: "Show the application title in navigation.", Type: "boolean", DefaultValue: "false", AllowedValues: []string{"true", "false"}},
	{ID: "jira.lf.favicon.url", Key: "jira.lf.favicon.url", Name: "Favicon URL", Description: "The favicon URL.", Type: "string", DefaultValue: "/favicon.ico"},
	{ID: "jira.lf.favicon.hires.url", Key: "jira.lf.favicon.hires.url", Name: "High-resolution favicon URL", Description: "The high-resolution favicon URL.", Type: "string", DefaultValue: "/images/64jira.png"},
	{ID: "jira.lf.navigation.bgcolour", Key: "jira.lf.navigation.bgcolour", Name: "Navigation background", Description: "The navigation background color.", Type: "string", DefaultValue: "#0747A6"},
	{ID: "jira.lf.navigation.highlightcolour", Key: "jira.lf.navigation.highlightcolour", Name: "Navigation highlight", Description: "The navigation text and logo color.", Type: "string", DefaultValue: "#DEEBFF"},
	{ID: "jira.lf.hero.button.base.bg.colour", Key: "jira.lf.hero.button.base.bg.colour", Name: "Hero button background", Description: "The hero button background color.", Type: "string", DefaultValue: "#3b7fc4"},
	{ID: "jira.title", Key: "jira.title", Name: "Application title", Description: "The title shown for the Jira application.", Type: "string", DefaultValue: "Jira"},
	{ID: "jira.option.globalsharing", Key: "jira.option.globalsharing", Name: "Global sharing", Description: "Allow filters and dashboards to be shared with signed-in users.", Type: "boolean", DefaultValue: "true", AllowedValues: []string{"true", "false"}},
	{ID: "xflow.product.suggestions.enabled", Key: "xflow.product.suggestions.enabled", Name: "Product suggestions", Description: "Show suggestions for other Atlassian products.", Type: "boolean", DefaultValue: "true", AllowedValues: []string{"true", "false"}},
	{ID: "jira.issuenav.criteria.autoupdate", Key: "jira.issuenav.criteria.autoupdate", Name: "Instant search criteria updates", Description: "Update results immediately when search criteria changes.", Type: "boolean", DefaultValue: "true", AllowedValues: []string{"true", "false"}},
}

func JiraApplicationPropertyDefinition(key string) (models.ApplicationProperty, bool) {
	for _, property := range jiraApplicationPropertyCatalog {
		if property.Key == key {
			return property, true
		}
	}
	return models.ApplicationProperty{}, false
}

func JiraApplicationProperties(values map[string]string) []models.ApplicationProperty {
	out := make([]models.ApplicationProperty, len(jiraApplicationPropertyCatalog))
	copy(out, jiraApplicationPropertyCatalog)
	for i := range out {
		out[i].Value = out[i].DefaultValue
		if value, ok := values[out[i].Key]; ok {
			out[i].Value = value
		}
	}
	return out
}
