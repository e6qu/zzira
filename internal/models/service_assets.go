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
