package models

import (
	"encoding/json"
	"time"
)

type AppDescriptor struct {
	Key, Name, BaseURL, Version string
	Scopes                      []string
	Modules                     []AppModule
}

type AppInstallation struct {
	ID, WorkspaceID, Key, Name, BaseURL, Version, Status, InstalledBy string
	SecretCiphertext                                                  []byte
	Descriptor                                                        json.RawMessage
	Scopes                                                            []string
	Modules                                                           []AppModule
	InstalledAt, UpdatedAt                                            time.Time
}

type AppModule struct {
	ID, InstallationID, AppKey, AppName string
	Key, Type, Location, Title, Body    string
	Position                            int
}

type AppStorageValue struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Version   int64           `json:"version"`
	UpdatedAt time.Time       `json:"updatedAt"`
}
