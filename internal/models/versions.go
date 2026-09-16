package models

import "encoding/json"

// Version is also the snapshot stored in issue fields and the sync log.
type Version struct {
	ID          string `json:"id"`
	ProjectID   string `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StartDate   string `json:"startDate,omitempty"`
	ReleaseDate string `json:"releaseDate,omitempty"`
	Released    bool   `json:"released"`
	Archived    bool   `json:"archived"`
	Position    int64  `json:"-"`
	// DriverID is the account responsible for the release.
	DriverID string `json:"driver,omitempty"`
	// MoveUnfixedIssuesToID is the version unresolved work moves to when this
	// version is released; clients see it as a self link, never as an id.
	MoveUnfixedIssuesToID string `json:"-"`
}

// VersionApprover is a person asked to approve a release.
type VersionApprover struct {
	AccountID     string `json:"accountId"`
	Status        string `json:"status"`
	Description   string `json:"description,omitempty"`
	DeclineReason string `json:"declineReason,omitempty"`
}

func (v Version) State() string {
	if v.Archived {
		return "Archived"
	}
	if v.Released {
		return "Released"
	}
	return "Unreleased"
}

type VersionProgress struct {
	ToDo       int `json:"toDo"`
	InProgress int `json:"inProgress"`
	Done       int `json:"done"`
	Unmapped   int `json:"unmapped"`
}

func (p VersionProgress) Total() int { return p.ToDo + p.InProgress + p.Done + p.Unmapped }
func (p VersionProgress) Percent() int {
	if p.Total() == 0 {
		return 0
	}
	return p.Done * 100 / p.Total()
}

func (i Issue) VersionRefs(field string) []Version {
	var versions []Version
	if raw := i.Fields[field]; raw != nil {
		if json.Unmarshal(raw, &versions) != nil {
			return nil
		}
	}
	return versions
}
