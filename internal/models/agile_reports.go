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

// BurndownSprint is one sprint in an epic or release burndown: what the
// work looked like when the sprint ended.
type BurndownSprint struct {
	Sprint Sprint
	// Added is the estimate of work that entered the scope during the
	// sprint, Completed the estimate of what was finished in it, and
	// Remaining what was left unfinished when it ended.
	Added     float64
	Completed float64
	Remaining float64
}

// BurndownReport is Jira's epic burndown and release burndown: the scope
// burning down sprint by sprint, with the scope changes that moved it.
type BurndownReport struct {
	Statistic string
	Sprints   []BurndownSprint
	// Remaining is where the work stands now, after the last sprint.
	Remaining float64
	// Unestimated is how much of the work carries no estimate, because a
	// burndown that ignores it would read as progress.
	Unestimated int
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
	Key         string
	Summary     string
	CompletedAt string
	// IssueType is the work type's name, which the cycle time report groups
	// by. The control chart leaves it empty.
	IssueType    string
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

// WorkloadRow is one group of unresolved work in a workload report: a
// person, a work type or a status, with what is left to do in it.
type WorkloadRow struct {
	// Key is the group's id, empty for work that belongs to no group --
	// unassigned work, work with no version.
	Key              string
	Name             string
	Issues           int
	RemainingSeconds int64
}

// WorkloadReport is Jira's user and version workload reports: unresolved
// work grouped by who has it, and for a version also by what type it is.
type WorkloadReport struct {
	Rows             []WorkloadRow
	Types            []WorkloadRow
	Issues           int
	RemainingSeconds int64
	// Estimated is how much of the unresolved work carries a remaining
	// estimate, because a workload of zero means nothing without it.
	Estimated int
}

// TimeTrackingRow is one work item in the time tracking report.
type TimeTrackingRow struct {
	Key, Summary     string
	OriginalSeconds  int64
	RemainingSeconds int64
	SpentSeconds     int64
	// AccuracySeconds is the original estimate less what the work has cost
	// so far: negative when the work has run over.
	AccuracySeconds int64
}

// TimeTrackingReport is Jira's time tracking report: the estimates and the
// time spent on the work of a project or a version.
type TimeTrackingReport struct {
	Rows             []TimeTrackingRow
	OriginalSeconds  int64
	RemainingSeconds int64
	SpentSeconds     int64
	AccuracySeconds  int64
}

// GroupByRow is one group in the single level group by report.
type GroupByRow struct {
	Key, Name string
	Issues    int
	// Percent is the share of the report's work items in this group,
	// rounded to a whole number.
	Percent int
}

// GroupByReport is Jira's single level group by report: the work a project
// holds, counted by one field.
type GroupByReport struct {
	Field  string
	Rows   []GroupByRow
	Issues int
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

// RecentlyCreatedDay is the work created on a day, split by whether it is
// resolved now.
type RecentlyCreatedDay struct {
	Date                 string
	Resolved, Unresolved int
}

// RecentlyCreatedReport is Jira's recently created chart.
type RecentlyCreatedReport struct {
	Days              []RecentlyCreatedDay
	Created, Resolved int
}

// AverageAgeDay is the work unresolved at the end of a day and how old it was
// on average.
type AverageAgeDay struct {
	Date           string
	Unresolved     int
	AverageSeconds int64
}

// AverageAgeReport is Jira's average age chart.
type AverageAgeReport struct {
	Days []AverageAgeDay
}

// TimeSinceDay counts the work whose date fell on a day.
type TimeSinceDay struct {
	Date  string
	Count int
}

// TimeSinceReport is Jira's time since chart for one date field.
type TimeSinceReport struct {
	Field string
	Days  []TimeSinceDay
	Total int
}
