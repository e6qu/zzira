package main

import "testing"

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
