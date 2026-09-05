package models

type WikiBody struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

type WikiSpace struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	AuthorID    string `json:"authorId"`
	CreatedAt   string `json:"createdAt"`
	Private     bool   `json:"private"`
}

type WikiVersion struct {
	Number    int    `json:"number"`
	Message   string `json:"message"`
	MinorEdit bool   `json:"minorEdit"`
	AuthorID  string `json:"authorId"`
	CreatedAt string `json:"createdAt"`
}

type WikiPage struct {
	ID          string      `json:"id"`
	WorkspaceID string      `json:"-"`
	SpaceID     string      `json:"spaceId"`
	ParentID    string      `json:"parentId,omitempty"`
	Title       string      `json:"title"`
	Status      string      `json:"status"`
	Published   bool        `json:"published"`
	AuthorID    string      `json:"authorId"`
	CreatedAt   string      `json:"createdAt"`
	Body        WikiBody    `json:"body"`
	Version     WikiVersion `json:"version"`
}
