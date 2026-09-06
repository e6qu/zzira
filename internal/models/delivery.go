package models

import (
	"encoding/json"
	"time"
)

// SoftwareBuild is the current Jira Software build snapshot for a pipeline key.
type SoftwareBuild struct {
	PipelineID           string
	BuildNumber          int64
	UpdateSequenceNumber int64
	IssueKeys            []string
	DisplayName          string
	URL                  string
	State                string
	LastUpdated          time.Time
	Properties           json.RawMessage
	Payload              json.RawMessage
}

// SoftwareDeployment is the current Jira Software deployment snapshot for an
// environment and monotonically increasing deployment key.
type SoftwareDeployment struct {
	PipelineID               string
	EnvironmentID            string
	DeploymentSequenceNumber int64
	UpdateSequenceNumber     int64
	IssueKeys                []string
	DisplayName              string
	URL                      string
	State                    string
	EnvironmentName          string
	EnvironmentType          string
	LastUpdated              time.Time
	Properties               json.RawMessage
	Payload                  json.RawMessage
}

// DeliveryItem is build or deployment evidence shown on work items and releases.
type DeliveryItem struct {
	Kind            string
	PipelineID      string
	DisplayName     string
	URL             string
	State           string
	EnvironmentName string
	EnvironmentType string
	LastUpdated     string
}
