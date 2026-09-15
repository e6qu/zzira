package models

// TimelineItem is one work item scheduled on a project timeline.
type TimelineItem struct {
	Issue *Issue
	// StartDate and DueDate are yyyy-MM-dd days; empty means unscheduled.
	StartDate string
	DueDate   string
	Children  []TimelineItem
}

// ProjectTimeline is a project's epics in rank order with their child work.
type ProjectTimeline struct {
	// StartFieldID is the site's Start date field, empty when it has none.
	StartFieldID string
	Epics        []TimelineItem
}
