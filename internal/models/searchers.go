package models

import (
	"slices"
	"strings"
)

// SearcherPrefix is the plugin prefix of Jira's custom field searcher keys.
const SearcherPrefix = "com.atlassian.jira.plugin.system.customfieldtypes:"

// customFieldSearchers are the searchers Jira allows for each field type.
var customFieldSearchers = map[string][]string{
	CustomFieldText:            {"textsearcher"},
	CustomFieldURL:             {"exacttextsearcher"},
	CustomFieldNumber:          {"exactnumber", "numberrange"},
	CustomFieldDate:            {"daterange"},
	CustomFieldDatetime:        {"datetimerange"},
	CustomFieldSelect:          {"multiselectsearcher"},
	CustomFieldMultiSelect:     {"multiselectsearcher"},
	CustomFieldCascadingSelect: {"cascadingselectsearcher"},
	CustomFieldUser:            {"userpickergroupsearcher"},
	CustomFieldMultiUser:       {"userpickergroupsearcher"},
	CustomFieldGroup:           {"grouppickersearcher"},
	CustomFieldMultiGroup:      {"multiselectsearcher"},
	CustomFieldLabels:          {"labelsearcher"},
	CustomFieldProject:         {"projectsearcher"},
	CustomFieldVersion:         {"versionsearcher"},
	CustomFieldMultiVersion:    {"versionsearcher"},
}

// NormalizeSearcherKey spells a searcher key with its plugin prefix.
func NormalizeSearcherKey(searcherKey string) string {
	searcherKey = strings.TrimSpace(searcherKey)
	if searcherKey == "" || strings.HasPrefix(searcherKey, SearcherPrefix) {
		return searcherKey
	}
	return SearcherPrefix + searcherKey
}

// ValidCustomFieldSearcher reports whether a searcher key is one Jira allows
// for the field type.
func ValidCustomFieldSearcher(fieldType, searcherKey string) bool {
	return slices.Contains(customFieldSearchers[fieldType], strings.TrimPrefix(NormalizeSearcherKey(searcherKey), SearcherPrefix))
}

// SearcherOperators lists the JQL operators a searcher allows, spelled as the
// query compiler spells them; nil leaves the field its type's operators.
func SearcherOperators(searcherKey string) []string {
	switch strings.TrimPrefix(NormalizeSearcherKey(searcherKey), SearcherPrefix) {
	case "textsearcher":
		return []string{"~", "!~", "empty", "notempty"}
	case "exacttextsearcher":
		return []string{"=", "!=", "in", "notin", "~", "!~", "empty", "notempty"}
	case "exactnumber":
		return []string{"=", "!=", "in", "notin", "empty", "notempty"}
	case "numberrange", "daterange", "datetimerange", "versionsearcher":
		return []string{"=", "!=", ">", ">=", "<", "<=", "in", "notin", "empty", "notempty"}
	case "multiselectsearcher", "cascadingselectsearcher", "labelsearcher", "userpickergroupsearcher", "grouppickersearcher", "projectsearcher":
		return []string{"=", "!=", "in", "notin", "empty", "notempty"}
	}
	return nil
}
