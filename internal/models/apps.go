package models

import (
	"encoding/json"
	"time"
)

type AppDescriptor struct {
	Key, Name, BaseURL, Version, Format string
	Scopes                              []string
	Modules                             []AppModule
	Lifecycle                           map[string]string
	Webhooks                            []AppWebhook
	ScheduledTriggers                   []AppScheduledTrigger
	IssueFields                         []AppIssueField
}

type AppIssueField struct {
	ID, InstallationID, AppKey, Key, Name, Type, Description string
	Dynamic, Active                                          bool
}

type AppInstallation struct {
	ID, WorkspaceID, PrincipalID, Key, Name, BaseURL, Version, Format, Status, InstalledBy string
	SecretCiphertext                                                                       []byte
	Descriptor                                                                             json.RawMessage
	Scopes                                                                                 []string
	Modules                                                                                []AppModule
	Lifecycle                                                                              map[string]string
	Webhooks                                                                               []AppWebhook
	ScheduledTriggers                                                                      []AppScheduledTrigger
	IssueFields                                                                            []AppIssueField
	OutboundDeliveries                                                                     []AppOutboundDelivery
	InstalledAt, UpdatedAt                                                                 time.Time
}

type AppWebhook struct {
	ID, InstallationID, AppKey, Key, Path, JQL string
	Events                                     []string
	LastSeq                                    int64
	Dynamic, ExcludeBody                       bool
}

type AppScheduledTrigger struct {
	ID, Key, Path, Interval string
	NextRunAt               time.Time
}

type AppOutboundDelivery struct {
	ID, InstallationID, AppKey, BaseURL, Format, Kind, ModuleKey, Event, Path, State, LastError string
	SecretCiphertext                                                                            []byte
	Payload                                                                                     json.RawMessage
	Attempts, ResponseCode                                                                      int
	AvailableAt, CreatedAt                                                                      time.Time
}

type AppModule struct {
	ID, InstallationID, AppKey, AppName, BaseURL string
	SecretCiphertext                             []byte
	Key, Type, Location, Title, Body, RemoteURL  string
	IconURL                                      string
	ContextLabel                                 string
	ContextStatusType, ContextStatusLabel        string
	ContextStatusClass, ContextStatusIconURL     string
	ContextStatusAccessibleLabel                 string
	Position                                     int
	Dynamic                                      bool
}

type AppIssueContent struct {
	Module AppModule
	Added  bool
}

type AppDynamicModule struct {
	Type, Key  string
	Descriptor json.RawMessage
	Module     AppModule
	Webhook    AppWebhook
	IssueField AppIssueField
}

type AppStorageValue struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Version   int64           `json:"version"`
	UpdatedAt time.Time       `json:"updatedAt"`
}
