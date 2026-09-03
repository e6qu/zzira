package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenderHelpersReturnCleanErrorResponses(t *testing.T) {
	tests := []struct {
		name   string
		render func(http.ResponseWriter, string, any)
		view   string
	}{
		{name: "page", render: writePage, view: "page_board"},
		{name: "fragment", render: writeFragment, view: "board_fragment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.render(response, test.view, pageData{})
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}
			if response.Body.String() != "internal error\n" {
				t.Fatalf("body contains partial template output: %q", response.Body.String())
			}
		})
	}
}
