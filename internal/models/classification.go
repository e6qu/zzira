package models

// DataClassificationLevel is one of the site's data classification levels,
// shared by Jira projects and Confluence content.
type DataClassificationLevel struct {
	ID          string
	Status      string // PUBLISHED, ARCHIVED or DRAFT
	Rank        int
	Name        string
	Description string
	Guideline   string
	Color       string
}

// DataClassificationLevels are the site's classification levels in rank order.
var DataClassificationLevels = []DataClassificationLevel{
	{ID: "public", Status: "PUBLISHED", Rank: 0, Name: "Public", Description: "Approved for public sharing", Guideline: "May be shared outside the organization.", Color: "GREEN"},
	{ID: "internal", Status: "PUBLISHED", Rank: 1, Name: "Internal", Description: "For organization members", Guideline: "Share only with authenticated organization members.", Color: "BLUE"},
	{ID: "confidential", Status: "PUBLISHED", Rank: 2, Name: "Confidential", Description: "Limited business information", Guideline: "Share only with people who need this information.", Color: "ORANGE"},
	{ID: "restricted", Status: "PUBLISHED", Rank: 3, Name: "Restricted", Description: "Highly sensitive information", Guideline: "Use explicit access controls and approved handling.", Color: "RED_BOLD"},
}

// DataClassificationLevelByID finds a classification level.
func DataClassificationLevelByID(id string) (DataClassificationLevel, bool) {
	for _, level := range DataClassificationLevels {
		if level.ID == id {
			return level, true
		}
	}
	return DataClassificationLevel{}, false
}
