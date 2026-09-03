package models

import "encoding/json"

// Rank is the LexoRank ordering key; carried on issue snapshots so replicas
// order board columns without extra state.
const (
	EntityBoard        = "board"
	EntitySprint       = "sprint"
	EntitySprintIssue  = "sprint_issue"
	EntityWatcher      = "watcher"
	EntityNotification = "notification"
)

type Board struct {
	ID              string   `json:"id"`
	ProjectID       string   `json:"-"`
	ProjectKey      string   `json:"-"`
	ProjectName     string   `json:"-"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	ColumnStatusIDs []string `json:"columnStatusIds"`
	FilterJQL       string   `json:"filterJql"`
}

type Sprint struct {
	ID        string `json:"id"`
	BoardID   string `json:"-"`
	Name      string `json:"name"`
	State     string `json:"state"` // future | active | closed
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	Goal      string `json:"goal,omitempty"`
}

type SprintIssue struct {
	SprintID string `json:"sprintId"`
	IssueID  string `json:"issueId"`
	Rank     string `json:"rank"`
}

// Notification is a per-user synced entity. Sync filtering keys off TargetUser.
type Notification struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	TargetUser  string `json:"userId"`
	ActorID     string `json:"actorId"`
	ActorName   string `json:"actorName"`
	Kind        string `json:"kind"` // assigned | mentioned | watched
	EntityType  string `json:"entityType"`
	EntityID    string `json:"entityId"`
	Message     string `json:"message"`
	Created     string `json:"created"`
}

type NotificationPayload struct {
	Notification Notification `json:"notification"`
}

type WatcherPayload struct {
	IssueID   string `json:"issueId"`
	AccountID string `json:"accountId"`
}

type BoardUpsertPayload struct {
	Board Board `json:"board"`
}

type SprintUpsertPayload struct {
	Sprint Sprint `json:"sprint"`
}

type SprintIssuePayload struct {
	SprintID string `json:"sprintId"`
	IssueID  string `json:"issueId"`
	Rank     string `json:"rank"`
	Removed  bool   `json:"removed,omitempty"`
}

// RankUpdatePayload: rank changes materialize but stay out of the changelog.
type RankUpdatePayload struct {
	IssueID string `json:"issueId"`
	Rank    string `json:"rank"`
}

// IssueJSON is a JSON round-trip helper for worker-side payload construction.
func IssueJSON(i Issue) json.RawMessage {
	b, _ := json.Marshal(IssueUpsertPayload{Issue: i})
	return b
}
