package web

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

type appModulePageData struct {
	Module *models.AppModule
}

func (h *Handler) AppModulePage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	module, err := h.Store.ActiveAppModule(r.Context(), workspaceID, r.PathValue("module"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.writeWorkspacePage(w, r, "page_app_module", user, workspaceID, appModulePageData{Module: module}, "app-module:"+module.ID, "")
}
