package commands

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
)

var serviceAssetSchemaKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)
var serviceAssetAttributeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var serviceAssetObjectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,63}$`)

func validateServiceAssetAttributes(attributes []models.ServiceAssetAttribute) error {
	if len(attributes) == 0 || len(attributes) > 30 {
		return fmt.Errorf("define between 1 and 30 asset attributes")
	}
	seen := map[string]bool{}
	for i := range attributes {
		attribute := &attributes[i]
		attribute.Key = strings.TrimSpace(attribute.Key)
		attribute.Name = strings.TrimSpace(attribute.Name)
		attribute.Type = strings.ToLower(strings.TrimSpace(attribute.Type))
		if !serviceAssetAttributeKeyPattern.MatchString(attribute.Key) || attribute.Name == "" || utf8.RuneCountInString(attribute.Name) > 255 || seen[attribute.Key] {
			return fmt.Errorf("asset attributes need unique lowercase keys and names")
		}
		if !stringSet("text", "number", "date", "boolean", "select")[attribute.Type] {
			return fmt.Errorf("attribute %q has an unsupported type", attribute.Name)
		}
		seen[attribute.Key] = true
		if attribute.Type != "select" {
			attribute.Options = nil
			continue
		}
		if len(attribute.Options) == 0 || len(attribute.Options) > 50 {
			return fmt.Errorf("select attribute %q needs between 1 and 50 options", attribute.Name)
		}
		optionSeen := map[string]bool{}
		for optionIndex := range attribute.Options {
			attribute.Options[optionIndex] = strings.TrimSpace(attribute.Options[optionIndex])
			option := attribute.Options[optionIndex]
			if option == "" || utf8.RuneCountInString(option) > 100 || optionSeen[option] {
				return fmt.Errorf("select attribute %q needs unique non-empty options", attribute.Name)
			}
			optionSeen[option] = true
		}
	}
	return nil
}

func (s *Service) CreateServiceAssetSchema(ctx context.Context, actorID, workspaceID, deskID string, schema models.ServiceAssetSchema) (*models.ServiceAssetSchema, error) {
	schema.Key = strings.ToUpper(strings.TrimSpace(schema.Key))
	schema.Name = strings.TrimSpace(schema.Name)
	schema.Description = strings.TrimSpace(schema.Description)
	if !serviceAssetSchemaKeyPattern.MatchString(schema.Key) {
		return nil, fmt.Errorf("schema key must start with a letter and contain at most 32 uppercase letters, numbers, or underscores")
	}
	if schema.Name == "" || utf8.RuneCountInString(schema.Name) > 255 || utf8.RuneCountInString(schema.Description) > 2000 {
		return nil, fmt.Errorf("schema name is required and its description must contain at most 2000 characters")
	}
	if err := validateServiceAssetAttributes(schema.Attributes); err != nil {
		return nil, err
	}
	return s.Store.CreateServiceAssetSchema(ctx, workspaceID, actorID, deskID, schema)
}

func (s *Service) DeleteServiceAssetSchema(ctx context.Context, actorID, workspaceID, deskID, schemaID string) error {
	if strings.TrimSpace(schemaID) == "" {
		return fmt.Errorf("asset schema is required")
	}
	return s.Store.DeleteServiceAssetSchema(ctx, workspaceID, actorID, deskID, schemaID)
}

func validateServiceAssetValue(attribute models.ServiceAssetAttribute, value string) error {
	if utf8.RuneCountInString(value) > 10000 {
		return fmt.Errorf("%s accepts at most 10000 characters", attribute.Name)
	}
	if value == "" {
		if attribute.Required {
			return fmt.Errorf("%s is required", attribute.Name)
		}
		return nil
	}
	switch attribute.Type {
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return fmt.Errorf("%s must be a number", attribute.Name)
		}
	case "date":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return fmt.Errorf("%s must be a date", attribute.Name)
		}
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Errorf("%s must be true or false", attribute.Name)
		}
	case "select":
		valid := false
		for _, option := range attribute.Options {
			if value == option {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("choose a supported value for %s", attribute.Name)
		}
	}
	return nil
}

func (s *Service) SaveServiceAssetObject(ctx context.Context, actorID, workspaceID, deskID string, object models.ServiceAssetObject) (*models.ServiceAssetObject, error) {
	object.Key = strings.ToUpper(strings.TrimSpace(object.Key))
	object.Label = strings.TrimSpace(object.Label)
	if !serviceAssetObjectKeyPattern.MatchString(object.Key) {
		return nil, fmt.Errorf("object key must start with a letter and contain at most 64 uppercase letters, numbers, hyphens, or underscores")
	}
	if object.Label == "" || utf8.RuneCountInString(object.Label) > 255 || object.X < 0 || object.X > 5000 || object.Y < 0 || object.Y > 5000 {
		return nil, fmt.Errorf("object label and topology coordinates between 0 and 5000 are required")
	}
	inventory, err := s.Store.ServiceAssetInventory(ctx, workspaceID, actorID, deskID)
	if err != nil {
		return nil, err
	}
	var schema *models.ServiceAssetSchema
	for i := range inventory.Schemas {
		if inventory.Schemas[i].ID == object.SchemaID {
			schema = &inventory.Schemas[i]
			break
		}
	}
	if schema == nil {
		return nil, fmt.Errorf("asset schema does not exist in this service project")
	}
	if object.Values == nil {
		object.Values = map[string]string{}
	}
	attributes := map[string]models.ServiceAssetAttribute{}
	for _, attribute := range schema.Attributes {
		attributes[attribute.Key] = attribute
		value := strings.TrimSpace(object.Values[attribute.Key])
		object.Values[attribute.Key] = value
		if err := validateServiceAssetValue(attribute, value); err != nil {
			return nil, err
		}
	}
	for key := range object.Values {
		if _, ok := attributes[key]; !ok {
			return nil, fmt.Errorf("attribute %q does not belong to this schema", key)
		}
	}
	return s.Store.SaveServiceAssetObject(ctx, workspaceID, actorID, deskID, object)
}

func (s *Service) DeleteServiceAssetObject(ctx context.Context, actorID, workspaceID, deskID, objectID string) error {
	if strings.TrimSpace(objectID) == "" {
		return fmt.Errorf("asset object is required")
	}
	return s.Store.DeleteServiceAssetObject(ctx, workspaceID, actorID, deskID, objectID)
}

func (s *Service) CreateServiceAssetRelationship(ctx context.Context, actorID, workspaceID, deskID string, relation models.ServiceAssetRelationship) (*models.ServiceAssetRelationship, error) {
	relation.Relationship = strings.TrimSpace(relation.Relationship)
	if relation.From.ID == "" || relation.To.ID == "" || relation.From.ID == relation.To.ID || relation.Relationship == "" || utf8.RuneCountInString(relation.Relationship) > 100 {
		return nil, fmt.Errorf("choose two different objects and describe their relationship")
	}
	return s.Store.CreateServiceAssetRelationship(ctx, workspaceID, actorID, deskID, relation)
}

func (s *Service) DeleteServiceAssetRelationship(ctx context.Context, actorID, workspaceID, deskID, relationshipID string) error {
	if strings.TrimSpace(relationshipID) == "" {
		return fmt.Errorf("asset relationship is required")
	}
	return s.Store.DeleteServiceAssetRelationship(ctx, workspaceID, actorID, deskID, relationshipID)
}

func (s *Service) SetServiceRequestAsset(ctx context.Context, actorID, workspaceID, issueIDOrKey, objectID, role string, linked bool) error {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return err
	}
	if strings.TrimSpace(objectID) == "" || (linked && !stringSet("affected", "depends_on")[role]) {
		return fmt.Errorf("asset object and a supported request role are required")
	}
	return s.Store.SetServiceRequestAsset(ctx, workspaceID, actorID, issue.ID, objectID, role, linked)
}
