package models

// Rank is the LexoRank ordering key; carried on issue snapshots so replicas
// order board columns without extra state.
const (
	EntityBoard          = "board"
	EntitySprint         = "sprint"
	EntitySprintIssue    = "sprint_issue"
	EntityWatcher        = "watcher"
	EntityVote           = "vote"
	EntityNotification   = "notification"
	EntityServiceRequest = "service_request"
)

// BoardSwimlane is one lane of a board that groups by query: a name and the
// JQL that decides which work items stand in it. The first lane whose query
// matches a work item takes it, and what no lane matches stands in a last
// lane of its own.
type BoardSwimlane struct {
	Name string `json:"name"`
	JQL  string `json:"jql"`
	// Position is the lane's place in the board's order, from zero.
	Position int `json:"-"`
}

// BoardColumn is one column of a board: a name and the statuses whose work
// items stand in it. Jira maps several statuses to one column -- a work item
// in any of them belongs to that column -- and this product mapped exactly
// one, which is why a column could not be named or grouped.
type BoardColumn struct {
	Name      string   `json:"name"`
	StatusIDs []string `json:"statusIds"`
	// Limit is the column's maximum work items; 0 is no limit.
	Limit int `json:"limit,omitempty"`
}

type Board struct {
	ID               string             `json:"id"`
	ProjectID        string             `json:"projectId"`
	ProjectKey       string             `json:"-"`
	ProjectName      string             `json:"-"`
	WorkspaceID      string             `json:"-"`
	Name             string             `json:"name"`
	Type             string             `json:"type"`
	Columns          []BoardColumn      `json:"columns"`
	FilterJQL        string             `json:"filterJql"`
	QuickFilters     []BoardQuickFilter `json:"quickFilters,omitempty"`
	SwimlaneStrategy string             `json:"swimlaneStrategy"`
	Swimlanes        []BoardSwimlane    `json:"swimlanes,omitempty"`
	CardFields       []string           `json:"cardFields,omitempty"`
	// JiraID and FilterJiraID are the board and board filter ids clients see.
	JiraID       int64 `json:"-"`
	FilterJiraID int64 `json:"-"`
	// EstimationFieldID is the number field a scrum board estimates with.
	EstimationFieldID   string `json:"-"`
	EstimationFieldName string `json:"-"`
	// SourceFilterID is the saved filter the board was created from, if any.
	SourceFilterID     string `json:"-"`
	SourceFilterJiraID int64  `json:"-"`
	// ProjectTypeKey is the type of the project the board is located in.
	ProjectTypeKey string `json:"-"`
}

// StatusIDs are every status the board shows, in column order. Most callers
// want this rather than the columns themselves: a board's scope is the work
// items in any of its statuses.
func (b *Board) StatusIDs() []string {
	ids := make([]string, 0, len(b.Columns))
	for _, column := range b.Columns {
		ids = append(ids, column.StatusIDs...)
	}
	return ids
}

// ColumnOfStatus is the index of the column a status stands in, or -1.
func (b *Board) ColumnOfStatus(statusID string) int {
	for index, column := range b.Columns {
		for _, id := range column.StatusIDs {
			if id == statusID {
				return index
			}
		}
	}
	return -1
}

// BoardAdmin is one holder of a board's administration rights. Jira Software
// administers a board by user and by group, and shows both on the board.
type BoardAdmin struct {
	ID        int64
	Type      string
	AccountID string
	UserName  string
	GroupID   string
	GroupName string
}

type BoardQuickFilter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	JQL         string `json:"jql"`
	Position    int    `json:"position"`
	// JiraID is the id clients see; it is stored with the filter.
	JiraID int64 `json:"jiraId,omitempty"`
}

type Sprint struct {
	ID        string `json:"id"`
	BoardID   string `json:"boardId"`
	Name      string `json:"name"`
	State     string `json:"state"` // future | active | closed
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	// ActivatedDate and CompleteDate are when the sprint actually started and
	// completed, as opposed to its planned dates.
	ActivatedDate string `json:"-"`
	CompleteDate  string `json:"completeDate,omitempty"`
	CreatedDate   string `json:"createdDate,omitempty"`
	Goal          string `json:"goal,omitempty"`
	JiraID        int64  `json:"-"`
	// BoardJiraID is the id clients know the sprint's board by.
	BoardJiraID int64 `json:"-"`
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
	Read        bool   `json:"read"`
	ReadAt      string `json:"readAt,omitempty"`
}

type NotificationPayload struct {
	Notification Notification `json:"notification"`
}

type WatcherPayload struct {
	IssueID   string `json:"issueId"`
	AccountID string `json:"accountId"`
}

type VotePayload struct {
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
