package models

import (
	"encoding/json"
	"time"
)

// DevelopmentRepository is the durable Jira Software development-information
// aggregate sent by source-control integrations.
type DevelopmentRepository struct {
	ID               string
	UpdateSequenceID int64
	Name             string
	URL              string
	Properties       json.RawMessage
	Payload          json.RawMessage
	Entities         []DevelopmentEntity
}

// DevelopmentEntity is a commit, branch, or pull request associated with one
// or more Jira work items.
type DevelopmentEntity struct {
	RepositoryID     string
	Type             string
	ID               string
	UpdateSequenceID int64
	IssueKeys        []string
	Name             string
	URL              string
	Status           string
	OccurredAt       *time.Time
	Payload          json.RawMessage
}

// DevelopmentTriggerEvent identifies a newly accepted entity that may run a
// workflow development trigger after the ingestion transaction commits.
type DevelopmentTriggerEvent struct {
	Type      string
	IssueKeys []string
}

// DevelopmentItem is the issue-facing development summary.
type DevelopmentItem struct {
	RepositoryID   string
	RepositoryName string
	Type           string
	ID             string
	Name           string
	URL            string
	Status         string
	Occurred       string
}
