package models

import "encoding/json"

// IssueForm is an Advanced Forms (formerly ProForma) instance attached to an
// issue. Design remains template-owned; the instance stores answers and state.
type IssueForm struct {
	ID         string          `json:"id"`
	IssueID    string          `json:"-"`
	TemplateID string          `json:"-"`
	Name       string          `json:"name"`
	Internal   bool            `json:"internal"`
	Submitted  bool            `json:"submitted"`
	Locked     bool            `json:"lock"`
	Answers    json.RawMessage `json:"-"`
	Updated    string          `json:"updated"`
}
