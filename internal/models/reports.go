package models

// DORADay is one UTC calendar bucket in the delivery-frequency chart.
type DORADay struct {
	Date             string
	Label            string
	Deployments      int
	Failures         int
	X                int
	FailureX         int
	DeploymentHeight int
	FailureHeight    int
	DeploymentY      int
	FailureY         int
}

// DORAReport is the permission-filtered project delivery report.
type DORAReport struct {
	WindowDays          int
	Since               string
	Until               string
	DeploymentFrequency int
	DeploymentsPerWeek  float64
	FailedChanges       int
	TotalChanges        int
	ChangeFailureRate   float64
	LeadTimeSeconds     int64
	LeadTimeSamples     int
	LeadTimeDisplay     string
	MTTRSeconds         int64
	RecoveredIncidents  int
	MTTRDisplay         string
	ChartWidth          int
	Daily               []DORADay
	Recent              []DeliveryItem
	// EnvironmentTypes and Pipelines are the mapping the numbers were read
	// through, so the report says what it counted rather than implying every
	// deployment.
	EnvironmentTypes []string
	Pipelines        int
	// ExcludedPeriods is how many stretches of days the project leaves out,
	// so the report says that some delivery was not counted.
	ExcludedPeriods int
	// IncidentJQL is what the project counts as an incident, or "" when its
	// service desk's own incident requests are what counts.
	IncidentJQL string
}
