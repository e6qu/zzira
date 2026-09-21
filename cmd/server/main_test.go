package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strconv"
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

// The routes are registered in main, where nothing but starting the server
// exercises them: a pattern that conflicts with another makes ServeMux panic
// at registration, and the only symptom is a server that never listens. This
// reads the patterns main registers and registers them on a mux of its own,
// so Go's own conflict rules answer before a deployment does.
func TestRegisteredRoutesDoNotConflict(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	patterns := make([]string, 0, 512)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle" {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "mux" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatalf("route pattern %s: %v", literal.Value, err)
		}
		patterns = append(patterns, pattern)
		return true
	})
	if len(patterns) < 100 {
		t.Fatalf("found %d route patterns in main.go, which is not the router", len(patterns))
	}
	mux := http.NewServeMux()
	for _, pattern := range patterns {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("registering %q: %v", pattern, recovered)
				}
			}()
			mux.Handle(pattern, http.NotFoundHandler())
		}()
	}
}
