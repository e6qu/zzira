package models

type ServiceAssetAttribute struct {
	Key, Name, Type string
	Required        bool
	Options         []string
}

type ServiceAssetSchema struct {
	ID, AssetsWorkspaceID, ServiceDeskID, Key, Name, Description string
	Attributes                                                   []ServiceAssetAttribute
}

type ServiceAssetObject struct {
	ID, SchemaID, SchemaKey, SchemaName, Key, Label string
	Values                                          map[string]string
	X, Y                                            int
}

type ServiceAssetRelationship struct {
	ID, Relationship string
	From, To         ServiceAssetObject
}

type ServiceRequestAsset struct {
	Object ServiceAssetObject
	Role   string
	Direct bool
	Depth  int
}

type ServiceAssetInventory struct {
	Schemas       []ServiceAssetSchema
	Objects       []ServiceAssetObject
	Relationships []ServiceAssetRelationship
}

// ServiceAssetImport reports what an import of objects did, so the page that
// sent the file can say how many objects it wrote.
type ServiceAssetImport struct {
	Created, Updated int
	// Deleted counts the objects a reconciling import took away because the
	// file left them out.
	Deleted int
	Objects []ServiceAssetObject
}

// ServiceAssetObjectRequest is a request that names an object, as the Assets
// API reports an object's connected tickets.
type ServiceAssetObjectRequest struct {
	IssueID, Key, Summary, Role string
}

// ServiceAssetObjectChange is one thing that happened to an object: who did
// it, when, and what it left the object looking like. An object's history is
// read from the site's own action log, which every write already records.
type ServiceAssetObjectChange struct {
	Seq       int64
	At        string
	ActorID   string
	ActorName string
	Operation string
	// Changed names the fields this write altered, against the write before
	// it. The first write of an object changes nothing: it is the object.
	Changed []string
	Object  ServiceAssetObject
}

// ServiceAssetObjectComment is something somebody said about an object.
type ServiceAssetObjectComment struct {
	ID, ObjectID string
	AuthorID     string
	AuthorName   string
	Body         string
	At           string
}
