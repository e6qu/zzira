package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// The organization's data classification levels are managed from site
// administration: levels are created as drafts, edited, published, archived,
// restored and put in order.

func classificationInput(r *http.Request) store.DataClassificationLevelInput {
	return store.DataClassificationLevelInput{Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), Guideline: r.PostFormValue("guideline"), Color: r.PostFormValue("color")}
}

func writeClassificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrClassificationValidation):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, pgx.ErrNoRows):
		http.Error(w, "classification level not found", http.StatusNotFound)
	case errors.Is(err, store.ErrProjectPermission):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		http.Error(w, "classification level change failed", http.StatusInternalServerError)
	}
}

func (h *Handler) CreateAdminClassificationLevel(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Store.CreateDataClassificationLevel(r.Context(), workspaceID, user.ID, classificationInput(r)); err != nil {
		writeClassificationError(w, err)
		return
	}
	redirectLocal(w, r, "/admin?saved="+url.QueryEscape("Classification level created as a draft")+"#admin-classification-levels")
}

func (h *Handler) UpdateAdminClassificationLevel(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	id := r.PathValue("levelId")
	var err error
	saved := "Classification level saved"
	switch r.PostFormValue("action") {
	case "", "save":
		_, err = h.Store.UpdateDataClassificationLevel(r.Context(), workspaceID, user.ID, id, classificationInput(r))
	case "publish", "restore":
		_, err = h.Store.SetDataClassificationLevelStatus(r.Context(), workspaceID, user.ID, id, "PUBLISHED")
		saved = "Classification level published"
	case "archive":
		_, err = h.Store.SetDataClassificationLevelStatus(r.Context(), workspaceID, user.ID, id, "ARCHIVED")
		saved = "Classification level archived"
	case "up", "down":
		err = h.Store.MoveDataClassificationLevel(r.Context(), workspaceID, user.ID, id, r.PostFormValue("action") == "up")
		saved = "Classification levels reordered"
	default:
		http.Error(w, "choose save, publish, archive, restore, up or down", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeClassificationError(w, err)
		return
	}
	redirectLocal(w, r, "/admin?saved="+url.QueryEscape(saved)+"#admin-classification-levels")
}
