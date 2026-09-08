package models

import (
	"encoding/json"
	"time"
)

type AppDescriptor struct {
	Key, Name, BaseURL, Version string
	Scopes                      []string
	Modules                     []AppModule
	Lifecycle                   map[string]string
	Webhooks                    []AppWebhook
	ScheduledTriggers           []AppScheduledTrigger
}

type AppInstallation struct {
	ID, WorkspaceID, PrincipalID, Key, Name, BaseURL, Version, Status, InstalledBy string
	SecretCiphertext                                                               []byte
	Descriptor                                                                     json.RawMessage
	Scopes                                                                         []string
	Modules                                                                        []AppModule
	Lifecycle                                                                      map[string]string
	Webhooks                                                                       []AppWebhook
	ScheduledTriggers                                                              []AppScheduledTrigger
	OutboundDeliveries                                                             []AppOutboundDelivery
	InstalledAt, UpdatedAt                                                         time.Time
}

type AppWebhook struct {
	ID, InstallationID, AppKey, Key, Path, JQL string
	Events                                     []string
	LastSeq                                    int64
}

type AppScheduledTrigger struct {
	ID, Key, Path, Interval string
	NextRunAt               time.Time
}

type AppOutboundDelivery struct {
	ID, InstallationID, AppKey, BaseURL, Kind, ModuleKey, Event, Path, State, LastError string
	SecretCiphertext                                                                    []byte
	Payload                                                                             json.RawMessage
	Attempts, ResponseCode                                                              int
	AvailableAt, CreatedAt                                                              time.Time
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
