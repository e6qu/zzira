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

// FlowColumn is one board column in the cumulative flow diagram.
type FlowColumn struct {
	StatusID string
	Name     string
}

// FlowDay counts the board's work in each column at the end of a day, in
// column order.
type FlowDay struct {
	Date   string
	Counts []int
}

// CumulativeFlow is how a board's work was spread across its columns each day.
type CumulativeFlow struct {
	Columns []FlowColumn
	Days    []FlowDay
}

// CycleSample is one work item's trip from starting to done.
type CycleSample struct {
	Key          string
	Summary      string
	CompletedAt  string
	CycleSeconds int64
}

// ControlChart is the cycle time of the board's work completed in a window.
type ControlChart struct {
	Samples        []CycleSample
	AverageSeconds int64
	MedianSeconds  int64
}

// ProgressPoint is how much of a body of work existed and was done at the end
// of a day.
type ProgressPoint struct {
	Date      string
	Total     float64
	Completed float64
}

// ProgressReport is Jira's epic or version report: the work's estimate over
// time, what is done and what remains.
type ProgressReport struct {
	Statistic string
	Start     string
	End       string
	Points    []ProgressPoint

	Completed  []SprintReportIssue
	Incomplete []SprintReportIssue

	TotalEstimate     float64
	CompletedEstimate float64
	// Unestimated counts work without an estimate in the statistic.
	Unestimated int
	Progress    VersionProgress
}

// CreatedResolvedDay counts the work created and resolved on a day, and the
// running totals since the window began.
type CreatedResolvedDay struct {
	Date              string
	Created, Resolved int
	CreatedTotal      int
	ResolvedTotal     int
}

// CreatedResolvedReport is Jira's created vs. resolved issues report.
type CreatedResolvedReport struct {
	Days                        []CreatedResolvedDay
	CreatedTotal, ResolvedTotal int
}

// ResolutionDay is the work resolved on a day and how long it took on average.
type ResolutionDay struct {
	Date           string
	Resolved       int
	AverageSeconds int64
}

// ResolutionTimeReport is Jira's resolution time report.
type ResolutionTimeReport struct {
	Days           []ResolutionDay
	Resolved       int
	AverageSeconds int64
}
