package models

// DataClassificationLevel is one of an organization's data classification
// levels, shared by Jira projects and Confluence content on its sites.
type DataClassificationLevel struct {
	ID          string
	Status      string // PUBLISHED, ARCHIVED or DRAFT
	Rank        int
	Name        string
	Description string
	Guideline   string
	Color       string
}

// DefaultDataClassificationLevels are the levels every organization starts
// with, in rank order.
var DefaultDataClassificationLevels = []DataClassificationLevel{
	{ID: "public", Status: "PUBLISHED", Rank: 0, Name: "Public", Description: "Approved for public sharing", Guideline: "May be shared outside the organization.", Color: "GREEN"},
	{ID: "internal", Status: "PUBLISHED", Rank: 1, Name: "Internal", Description: "For organization members", Guideline: "Share only with authenticated organization members.", Color: "BLUE"},
	{ID: "confidential", Status: "PUBLISHED", Rank: 2, Name: "Confidential", Description: "Limited business information", Guideline: "Share only with people who need this information.", Color: "ORANGE"},
	{ID: "restricted", Status: "PUBLISHED", Rank: 3, Name: "Restricted", Description: "Highly sensitive information", Guideline: "Use explicit access controls and approved handling.", Color: "RED_BOLD"},
}

// DataClassificationColors are the colors a level can be shown in.
var DataClassificationColors = []string{"RED", "RED_BOLD", "ORANGE", "YELLOW", "GREEN", "BLUE", "NAVY", "TEAL", "PURPLE", "GREY", "LIME"}
