package main

import (
	"testing"

	"github.com/e6qu/zzira/internal/demo"
)

func TestServingWorkspaceSlug(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
		valid bool
	}{
		{name: "configured", value: "acme", want: "acme", valid: true},
		{name: "missing"},
		{name: "newline injection", value: "acme\nforged log entry"},
		{name: "terminal escape", value: "acme\x1b[2J"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := servingWorkspaceSlug(func(string) string { return tt.value })
			if (err == nil) != tt.valid {
				t.Fatalf("servingWorkspaceSlug() err=%v, valid=%v", err, tt.valid)
			}
			if got != tt.want {
				t.Fatalf("servingWorkspaceSlug()=%q, want %q", got, tt.want)
			}
		})
	}
}

// An instance serves only the workspace WORKSPACE_SLUG names, so -mode=demo
// has to be able to seed that one. The flag is the operator's one-off
// override; the environment is the deployment's own answer; the scenario's
// slug is what a scenario applied to nothing in particular builds.
func TestDemoWorkspaceSlug(t *testing.T) {
	scenario := &demo.Scenario{Site: demo.Site{Slug: "northwind", Name: "Northwind"}}
	tests := []struct {
		name, flag, env, want string
	}{
		{name: "nothing named takes the scenario's own slug", want: "northwind"},
		{name: "the deployment's workspace", env: "zzira", want: "zzira"},
		{name: "the flag overrides the deployment", flag: "staging", env: "zzira", want: "staging"},
		{name: "the flag alone", flag: "staging", want: "staging"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := demoWorkspaceSlug(tt.flag, func(string) string { return tt.env }, scenario)
			if got != tt.want {
				t.Fatalf("demoWorkspaceSlug()=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestBlobDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "unset", env: nil, want: "data/attachments"},
		{name: "data dir", env: map[string]string{"DATA_DIR": "/data"}, want: "/data"},
		{name: "empty data dir", env: map[string]string{"DATA_DIR": ""}, want: "data/attachments"},
		{
			name: "blob dir is not a knob",
			env:  map[string]string{"BLOB_DIR": "/elsewhere", "DATA_DIR": "/data"},
			want: "/data",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blobDir(func(key string) string { return tt.env[key] })
			if got != tt.want {
				t.Fatalf("blobDir = %q, want %q", got, tt.want)
			}
		})
	}
}
