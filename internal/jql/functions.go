package jql

import (
	"slices"
	"strings"
)

// builtInFunctions names, in lower case, every function the compiler resolves
// itself. Apps cannot register a function under one of these names.
var builtInFunctions = map[string]bool{}

func init() {
	for _, name := range []string{
		"approved", "approver", "breached", "cascadeOption", "closedSprints", "completed",
		"componentsLeadByUser", "currentLogin", "currentUser", "earliestUnreleasedVersion",
		"endOfDay", "endOfMonth", "endOfWeek", "endOfYear", "everBreached", "futureSprints",
		"issueHistory", "issuesWithRemoteLinksByGlobalId", "lastLogin", "latestReleasedVersion", "linkedIssues", "linkedWorkItems",
		"membersOf",
		"myApproval", "myPending", "myPendingApproval", "now", "openSprints", "paused",
		"pending", "pendingApprovalBy", "pendingBy", "projectsLeadByUser",
		"projectsWhereUserHasPermission", "projectsWhereUserHasRole", "releasedVersions",
		"remaining", "running", "spacesLeadByUser", "spacesWhereUserHasPermission",
		"spacesWhereUserHasRole", "standardIssueTypes", "standardWorkTypes", "startOfDay",
		"startOfMonth", "startOfWeek", "startOfYear", "subtaskIssueTypes", "subtaskWorkTypes",
		"unreleasedVersions", "updatedBy", "votedIssues", "votedWorkItems", "watchedIssues",
		"watchedWorkItems", "withinCalendarHours", "workItemHistory",
		"workItemsWithRemoteLinksByGlobalId",
	} {
		builtInFunctions[strings.ToLower(name)] = true
	}
}

// IsBuiltInFunction reports whether name, without parentheses, is a function
// the compiler resolves itself. The match ignores case, as JQL does.
func IsBuiltInFunction(name string) bool {
	return builtInFunctions[strings.ToLower(strings.TrimSpace(name))]
}

// BuiltInFunctionNames lists the built-in function names in lower case.
func BuiltInFunctionNames() []string {
	names := make([]string, 0, len(builtInFunctions))
	for name := range builtInFunctions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
