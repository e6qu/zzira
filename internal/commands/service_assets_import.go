package commands

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// serviceAssetImportRows is what one upload may carry. An inventory is read
// and rewritten whole, so a file larger than this is a mistake rather than a
// migration.
const serviceAssetImportRows = 1000

// ImportServiceAssetObjects reads objects for one schema out of a comma
// separated file. Its first row names the columns: Key and Label, optionally X
// and Y, and any of the schema's attributes by the name or the key the schema
// gave it. A row whose key already belongs to an object in the schema updates
// that object; every other row creates one.
//
// The whole file is checked before anything is written, and then written in
// one transaction, so an import never half-lands.
func (s *Service) ImportServiceAssetObjects(ctx context.Context, actorID, workspaceID, deskID, schemaID, file string) (*models.ServiceAssetImport, error) {
	inventory, err := s.Store.ServiceAssetInventory(ctx, workspaceID, actorID, deskID)
	if err != nil {
		return nil, err
	}
	var schema *models.ServiceAssetSchema
	for i := range inventory.Schemas {
		if inventory.Schemas[i].ID == schemaID {
			schema = &inventory.Schemas[i]
			break
		}
	}
	if schema == nil {
		return nil, fmt.Errorf("asset schema does not exist in this service project")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(file, "\ufeff")))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read the file: %w", err)
	}
	for len(records) > 0 && emptyRecord(records[len(records)-1]) {
		records = records[:len(records)-1]
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("the file needs a heading row and at least one object")
	}
	if len(records) > serviceAssetImportRows+1 {
		return nil, fmt.Errorf("import at most %d objects at a time", serviceAssetImportRows)
	}
	columns, err := serviceAssetImportColumns(records[0], schema)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*models.ServiceAssetObject{}
	placed := 0
	for i := range inventory.Objects {
		if inventory.Objects[i].SchemaID != schemaID {
			continue
		}
		byKey[strings.ToUpper(inventory.Objects[i].Key)] = &inventory.Objects[i]
		placed++
	}
	objects := make([]models.ServiceAssetObject, 0, len(records)-1)
	seen := map[string]int{}
	result := &models.ServiceAssetImport{}
	for row, record := range records[1:] {
		line := row + 2
		if emptyRecord(record) {
			continue
		}
		object, err := serviceAssetImportRow(record, columns, schema)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", line, err)
		}
		if earlier, repeated := seen[object.Key]; repeated {
			return nil, fmt.Errorf("row %d: %s is already row %d of this file", line, object.Key, earlier)
		}
		seen[object.Key] = line
		if existing, ok := byKey[object.Key]; ok {
			object.ID = existing.ID
			if !columns.coordinates {
				object.X, object.Y = existing.X, existing.Y
			}
			result.Updated++
		} else {
			if !columns.coordinates {
				object.X, object.Y = serviceAssetImportPlacement(placed)
			}
			placed++
			result.Created++
		}
		objects = append(objects, object)
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("the file needs a heading row and at least one object")
	}
	written, err := s.Store.ImportServiceAssetObjects(ctx, workspaceID, actorID, deskID, objects)
	if err != nil {
		return nil, err
	}
	result.Objects = written
	return result, nil
}

// serviceAssetImportPlacement lays a new object out on the topology canvas in
// rows, so an imported inventory is readable before anyone moves it.
func serviceAssetImportPlacement(index int) (int, int) {
	x := 40 + (index%6)*170
	y := 40 + (index/6)*130
	if y > 5000 {
		y = 5000
	}
	return x, y
}

func emptyRecord(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

type serviceAssetImportHeading struct {
	key, label, x, y int
	attributes       map[int]models.ServiceAssetAttribute
	coordinates      bool
}

func serviceAssetImportColumns(heading []string, schema *models.ServiceAssetSchema) (serviceAssetImportHeading, error) {
	columns := serviceAssetImportHeading{key: -1, label: -1, x: -1, y: -1, attributes: map[int]models.ServiceAssetAttribute{}}
	named := map[string]models.ServiceAssetAttribute{}
	for _, attribute := range schema.Attributes {
		named[strings.ToLower(attribute.Key)] = attribute
		named[strings.ToLower(attribute.Name)] = attribute
	}
	taken := map[string]bool{}
	for index, column := range heading {
		name := strings.ToLower(strings.TrimSpace(column))
		if name == "" {
			return columns, fmt.Errorf("column %d of the heading row has no name", index+1)
		}
		if taken[name] {
			return columns, fmt.Errorf("the heading row names %q twice", strings.TrimSpace(column))
		}
		taken[name] = true
		switch name {
		case "key":
			columns.key = index
		case "label", "name":
			columns.label = index
		case "x":
			columns.x = index
		case "y":
			columns.y = index
		default:
			attribute, ok := named[name]
			if !ok {
				return columns, fmt.Errorf("%q is not Key, Label, X, Y, or an attribute of %s", strings.TrimSpace(column), schema.Name)
			}
			columns.attributes[index] = attribute
		}
	}
	if columns.key < 0 || columns.label < 0 {
		return columns, fmt.Errorf("the heading row needs a Key column and a Label column")
	}
	if (columns.x < 0) != (columns.y < 0) {
		return columns, fmt.Errorf("give both an X and a Y column, or neither")
	}
	columns.coordinates = columns.x >= 0
	return columns, nil
}

func serviceAssetImportRow(record []string, columns serviceAssetImportHeading, schema *models.ServiceAssetSchema) (models.ServiceAssetObject, error) {
	field := func(index int) string {
		if index < 0 || index >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[index])
	}
	object := models.ServiceAssetObject{SchemaID: schema.ID, Key: strings.ToUpper(field(columns.key)), Label: field(columns.label), Values: map[string]string{}}
	if !serviceAssetObjectKeyPattern.MatchString(object.Key) {
		return object, fmt.Errorf("%q is not an object key", field(columns.key))
	}
	if object.Label == "" {
		return object, fmt.Errorf("%s has no label", object.Key)
	}
	if columns.coordinates {
		x, xErr := strconv.Atoi(field(columns.x))
		y, yErr := strconv.Atoi(field(columns.y))
		if xErr != nil || yErr != nil || x < 0 || x > 5000 || y < 0 || y > 5000 {
			return object, fmt.Errorf("X and Y are whole numbers between 0 and 5000")
		}
		object.X, object.Y = x, y
	}
	for index, attribute := range columns.attributes {
		object.Values[attribute.Key] = field(index)
	}
	for _, attribute := range schema.Attributes {
		if err := validateServiceAssetValue(attribute, object.Values[attribute.Key]); err != nil {
			return object, err
		}
	}
	return object, nil
}
