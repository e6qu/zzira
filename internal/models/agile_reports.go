package models

// SprintReportIssue is one work item's place in a sprint report.
type SprintReportIssue struct {
	Key       string
	Summary   string
	IssueType string
	Status    string
	// EstimateStart is the estimate when the work joined the sprint and
	// EstimateEnd the estimate when the report ends; empty means unestimated.
	EstimateStart string
	EstimateEnd   string
	// AddedAfterStart marks work added once the sprint was underway.
	AddedAfterStart bool
}

// EstimateChanged reports whether the estimate moved during the sprint.
func (i SprintReportIssue) EstimateChanged() bool {
	return i.EstimateStart != i.EstimateEnd
}

// SprintBurndownEvent is one change to a sprint's remaining work.
type SprintBurndownEvent struct {
	At        string
	Label     string
	IssueKey  string
	Change    float64
	Remaining float64
	Scope     float64
}

// SprintReport is Jira's sprint report: what the sprint finished, left, and
// lost, with the burndown of its remaining estimate.
type SprintReport struct {
	Sprint Sprint
	// Statistic names what estimates count, such as Story point estimate.
	Statistic string
	// Start is when the sprint started and End when the report stops: the
	// completion time of a closed sprint, otherwise now.
	Start, End string

	Completed        []SprintReportIssue
	NotCompleted     []SprintReportIssue
	CompletedOutside []SprintReportIssue
	Removed          []SprintReportIssue

	CompletedTotal    float64
	NotCompletedTotal float64
	RemovedTotal      float64
	Burndown          []SprintBurndownEvent
}

// VelocitySprint is one closed sprint in the velocity chart.
type VelocitySprint struct {
	Sprint     Sprint
	Commitment float64
	Completed  float64
}

// VelocityReport compares commitment with completed work for recent closed
// sprints, oldest first.
type VelocityReport struct {
	Statistic string
	Sprints   []VelocitySprint
}
