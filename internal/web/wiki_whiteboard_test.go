package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestWhiteboardMutationAcknowledgesOnlySuccessfulSaves(t *testing.T) {
	whiteboard := &models.WikiContent{ID: "123", SpaceID: "456"}
	for _, tc := range []struct {
		name   string
		ajax   bool
		err    error
		status int
	}{
		{"drag saved", true, nil, http.StatusNoContent},
		{"form saved", false, nil, http.StatusSeeOther},
		{"drag refused", true, store.ErrWikiValidation, http.StatusBadRequest},
		{"drag forbidden", true, store.ErrProjectPermission, http.StatusForbidden},
		{"form refused", false, store.ErrWikiValidation, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/wiki/spaces/456/whiteboards/123/objects/789", nil)
			if tc.ajax {
				request.Header.Set("X-Requested-With", "zzira")
			}
			response := httptest.NewRecorder()
			(&Handler{}).finishWikiWhiteboardMutation(response, request, whiteboard, tc.err)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			if tc.status == http.StatusSeeOther {
				if target := response.Header().Get("Location"); target != "/wiki/spaces/456/whiteboards/123" {
					t.Fatalf("redirect = %q", target)
				}
			} else if response.Header().Get("Location") != "" {
				t.Fatal("save acknowledgement or refusal must not redirect")
			}
			if tc.status == http.StatusNoContent && response.Body.Len() != 0 {
				t.Fatal("save acknowledgement must not contain a page")
			}
		})
	}
}
