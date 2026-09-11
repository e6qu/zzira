package models

import "encoding/json"

type WikiBody struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

type WikiSpace struct {
	ID                         string `json:"id"`
	WorkspaceID                string `json:"-"`
	Key                        string `json:"key"`
	Name                       string `json:"name"`
	Description                string `json:"description"`
	AuthorID                   string `json:"authorId"`
	CreatedAt                  string `json:"createdAt"`
	Private                    bool   `json:"private"`
	DefaultClassificationLevel string `json:"-"`
}

type WikiSpaceRole struct {
	ID               string   `json:"id"`
	WorkspaceID      string   `json:"-"`
	Type             string   `json:"type"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	SpacePermissions []string `json:"spacePermissions"`
}

type WikiSpaceRoleAssignment struct {
	SpaceID       string `json:"-"`
	RoleID        string `json:"roleId"`
	PrincipalType string `json:"-"`
	PrincipalID   string `json:"-"`
}

func (a WikiSpaceRoleAssignment) Principal() map[string]string {
	return map[string]string{"principalType": a.PrincipalType, "principalId": a.PrincipalID}
}

func (s WikiSpace) DefaultClassificationName() string {
	return wikiClassificationName(s.DefaultClassificationLevel)
}

type WikiVersion struct {
	Number    int    `json:"number"`
	Message   string `json:"message"`
	MinorEdit bool   `json:"minorEdit"`
	AuthorID  string `json:"authorId"`
	CreatedAt string `json:"createdAt"`
}

type WikiPage struct {
	ID                  string      `json:"id"`
	WorkspaceID         string      `json:"-"`
	SpaceID             string      `json:"spaceId"`
	ParentID            string      `json:"parentId,omitempty"`
	Title               string      `json:"title"`
	Status              string      `json:"status"`
	Published           bool        `json:"published"`
	ClassificationLevel string      `json:"-"`
	AuthorID            string      `json:"authorId"`
	CreatedAt           string      `json:"createdAt"`
	Body                WikiBody    `json:"body"`
	Version             WikiVersion `json:"version"`
}

func (p WikiPage) ClassificationName() string {
	return wikiClassificationName(p.ClassificationLevel)
}

func wikiClassificationName(level string) string {
	switch level {
	case "public":
		return "Public"
	case "internal":
		return "Internal"
	case "confidential":
		return "Confidential"
	case "restricted":
		return "Restricted"
	default:
		return ""
	}
}

type WikiBlogPost struct {
	ID                  string      `json:"id"`
	WorkspaceID         string      `json:"-"`
	SpaceID             string      `json:"spaceId"`
	Title               string      `json:"title"`
	Status              string      `json:"status"`
	Published           bool        `json:"published"`
	Private             bool        `json:"private,omitempty"`
	ClassificationLevel string      `json:"-"`
	AuthorID            string      `json:"authorId"`
	CreatedAt           string      `json:"createdAt"`
	Body                WikiBody    `json:"body"`
	Version             WikiVersion `json:"version"`
}

func (p WikiBlogPost) ClassificationName() string {
	return wikiClassificationName(p.ClassificationLevel)
}

type WikiContent struct {
	ID                  string                `json:"id"`
	Type                string                `json:"type"`
	Status              string                `json:"status"`
	Title               string                `json:"title"`
	ParentID            string                `json:"parentId,omitempty"`
	ParentType          string                `json:"parentType,omitempty"`
	Position            int                   `json:"position"`
	AuthorID            string                `json:"authorId"`
	OwnerID             string                `json:"ownerId"`
	CreatedAt           string                `json:"createdAt"`
	SpaceID             string                `json:"spaceId"`
	EmbedURL            string                `json:"embedUrl,omitempty"`
	Private             bool                  `json:"private,omitempty"`
	ClassificationLevel string                `json:"classificationLevel,omitempty"`
	TemplateKey         string                `json:"templateKey,omitempty"`
	Locale              string                `json:"locale,omitempty"`
	CustomType          string                `json:"-"`
	Body                string                `json:"-"`
	BodyRepresentation  string                `json:"-"`
	Version             WikiVersion           `json:"version"`
	Properties          []WikiContentProperty `json:"-"`
}

func (c WikiContent) ClassificationName() string {
	switch c.ClassificationLevel {
	case "public":
		return "Public"
	case "internal":
		return "Internal"
	case "confidential":
		return "Confidential"
	case "restricted":
		return "Restricted"
	default:
		return ""
	}
}

type WikiContentProperty struct {
	ID          string          `json:"id"`
	ContentID   string          `json:"-"`
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Version     WikiVersion     `json:"version"`
	NextVersion int             `json:"-"`
}

type WikiRedactionPointer struct {
	Pointer string  `json:"pointer"`
	From    *int    `json:"from,omitempty"`
	To      *int    `json:"to,omitempty"`
	Reason  *string `json:"reason,omitempty"`
}

type WikiRedactionResult struct {
	Pointer     string `json:"pointer"`
	From        int    `json:"from"`
	To          int    `json:"to"`
	Reason      string `json:"reason,omitempty"`
	RedactionID string `json:"redactionId"`
}

type WikiBlogCustomContent struct {
	ID                 string      `json:"id"`
	Type               string      `json:"type"`
	Status             string      `json:"status"`
	Title              string      `json:"title"`
	SpaceID            string      `json:"spaceId"`
	PageID             string      `json:"pageId,omitempty"`
	BlogPostID         string      `json:"blogPostId"`
	AuthorID           string      `json:"authorId"`
	CreatedAt          string      `json:"createdAt"`
	BodyRepresentation string      `json:"-"`
	Body               WikiBody    `json:"body"`
	Version            WikiVersion `json:"version"`
}

type WikiContentRelation struct {
	Content       *WikiContent
	Depth         int
	ChildPosition int
}

type WikiFooterComment struct {
	ID                   string      `json:"id"`
	PageID               string      `json:"pageId,omitempty"`
	BlogPostID           string      `json:"blogPostId,omitempty"`
	SpaceID              string      `json:"-"`
	AttachmentID         string      `json:"attachmentId,omitempty"`
	ParentCommentID      string      `json:"parentCommentId,omitempty"`
	AuthorID             string      `json:"authorId"`
	AuthorName           string      `json:"-"`
	CreatedAt            string      `json:"createdAt"`
	UpdatedAt            string      `json:"-"`
	Body                 WikiBody    `json:"body"`
	Version              WikiVersion `json:"version"`
	CommentType          string      `json:"-"`
	InlineSelection      string      `json:"-"`
	InlineMatchCount     int         `json:"-"`
	InlineMatchIndex     int         `json:"-"`
	InlineMarkerRef      string      `json:"-"`
	ResolutionStatus     string      `json:"-"`
	ResolutionModifierID string      `json:"-"`
	ResolutionModifiedAt string      `json:"-"`
	ParentAuthorID       string      `json:"-"`
	ParentPrivate        bool        `json:"-"`
	ParentPublished      bool        `json:"-"`
}

type WikiFooterCommentVersion struct {
	WikiVersion
	Body WikiBody `json:"body"`
}

type WikiLabel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"-"`
}

type WikiRestrictionSubject struct {
	Type        string `json:"type"`
	ID          string `json:"id,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type WikiPageRestriction struct {
	Operation string                   `json:"operation"`
	Users     []WikiRestrictionSubject `json:"users"`
	Groups    []WikiRestrictionSubject `json:"groups"`
}

type WikiAttachment struct {
	ID              string                   `json:"id"`
	PageID          string                   `json:"pageId,omitempty"`
	BlogPostID      string                   `json:"blogPostId,omitempty"`
	SpaceID         string                   `json:"-"`
	FileID          string                   `json:"fileId"`
	Filename        string                   `json:"title"`
	MediaType       string                   `json:"mediaType"`
	Comment         string                   `json:"comment"`
	Size            int64                    `json:"fileSize"`
	Status          string                   `json:"status"`
	AuthorID        string                   `json:"authorId"`
	CreatedAt       string                   `json:"createdAt"`
	Version         WikiVersion              `json:"version"`
	Labels          []WikiLabel              `json:"-"`
	Properties      []WikiAttachmentProperty `json:"-"`
	ParentAuthorID  string                   `json:"-"`
	ParentPrivate   bool                     `json:"-"`
	ParentPublished bool                     `json:"-"`
}

type WikiAttachmentProperty struct {
	ID           string          `json:"id"`
	AttachmentID string          `json:"-"`
	Key          string          `json:"key"`
	Value        json.RawMessage `json:"value"`
	Version      WikiVersion     `json:"version"`
	NextVersion  int             `json:"-"`
}

type WikiAttachmentVersion struct {
	WikiVersion
	Filename  string `json:"title"`
	MediaType string `json:"mediaType"`
	Comment   string `json:"comment"`
	Size      int64  `json:"fileSize"`
}

type WikiTask struct {
	ID            string   `json:"id"`
	LocalID       string   `json:"localId"`
	SpaceID       string   `json:"spaceId"`
	PageID        string   `json:"pageId"`
	Status        string   `json:"status"`
	Body          WikiBody `json:"-"`
	CreatedBy     string   `json:"createdBy"`
	CreatedName   string   `json:"-"`
	AssignedTo    string   `json:"assignedTo,omitempty"`
	AssignedName  string   `json:"-"`
	CompletedBy   string   `json:"completedBy,omitempty"`
	CompletedName string   `json:"-"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	DueAt         string   `json:"dueAt,omitempty"`
	CompletedAt   string   `json:"completedAt,omitempty"`
}
