package web

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

type wikiDatabasePageData struct {
	Space      *models.WikiSpace
	Database   *models.WikiContent
	Columns    []models.WikiDatabaseColumn
	Rows       []models.WikiDatabaseRow
	Views      []models.WikiDatabaseView
	ActiveView *models.WikiDatabaseView
	CanEdit    bool
}

func (h *Handler) WikiDatabasePage(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	space, database, ok := h.wikiDatabaseContext(w, r, ws, user.ID)
	if !ok {
		return
	}
	data, err := h.Store.WikiDatabaseData(r.Context(), ws, user.ID, database.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	canEdit, err := h.Store.CanUpdateWikiContent(r.Context(), ws, user.ID, database.ID, "database")
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}

	page := wikiDatabasePageData{Space: space, Database: database, Columns: data.Columns, Rows: data.Rows, Views: data.Views, CanEdit: canEdit}
	viewID := strings.TrimSpace(r.URL.Query().Get("view"))
	if viewID != "" {
		for i := range data.Views {
			if data.Views[i].ID == viewID {
				page.ActiveView = &data.Views[i]
				break
			}
		}
		if page.ActiveView == nil {
			http.Error(w, "Saved database view does not exist.", http.StatusNotFound)
			return
		}
		page.Rows = applyWikiDatabaseView(page.Rows, page.Columns, *page.ActiveView)
	}
	h.writeWorkspacePage(w, r, "page_wiki_database", user, ws, page, "wiki", "")
}

func applyWikiDatabaseView(rows []models.WikiDatabaseRow, columns []models.WikiDatabaseColumn, view models.WikiDatabaseView) []models.WikiDatabaseRow {
	result := slices.Clone(rows)
	if view.FilterKey != "" {
		filtered := result[:0]
		needle := strings.ToLower(view.FilterValue)
		for _, row := range result {
			if strings.Contains(strings.ToLower(row.Values[view.FilterKey]), needle) {
				filtered = append(filtered, row)
			}
		}
		result = filtered
	}
	if view.SortKey == "" {
		return result
	}
	columnType := "text"
	for _, column := range columns {
		if column.Key == view.SortKey {
			columnType = column.Type
			break
		}
	}
	slices.SortStableFunc(result, func(left, right models.WikiDatabaseRow) int {
		comparison := compareWikiDatabaseValues(left.Values[view.SortKey], right.Values[view.SortKey], columnType)
		if comparison == 0 {
			comparison = strings.Compare(left.ID, right.ID)
		}
		if view.SortDirection == "desc" {
			return -comparison
		}
		return comparison
	})
	return result
}

func compareWikiDatabaseValues(left, right, columnType string) int {
	if columnType == "number" {
		leftNumber, leftErr := strconv.ParseFloat(left, 64)
		rightNumber, rightErr := strconv.ParseFloat(right, 64)
		if leftErr == nil && rightErr == nil {
			switch {
			case leftNumber < rightNumber:
				return -1
			case leftNumber > rightNumber:
				return 1
			default:
				return 0
			}
		}
	}
	return strings.Compare(strings.ToLower(left), strings.ToLower(right))
}

func (h *Handler) WikiDatabaseColumnCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, database, ok := h.wikiDatabaseMutationContext(w, r)
	if !ok {
		return
	}
	options := []string{}
	for _, option := range strings.Split(r.PostFormValue("options"), ",") {
		if strings.TrimSpace(option) != "" {
			options = append(options, option)
		}
	}
	_, err := h.Commands.AddWikiDatabaseColumn(r.Context(), ws, user.ID, database.ID, models.WikiDatabaseColumn{
		Key: r.PostFormValue("key"), Name: r.PostFormValue("name"), Type: r.PostFormValue("type"), Options: options,
	})
	h.finishWikiDatabaseMutation(w, r, database, err)
}

func (h *Handler) WikiDatabaseColumnDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, database, ok := h.wikiDatabaseMutationContext(w, r)
	if !ok {
		return
	}
	err := h.Commands.DeleteWikiDatabaseColumn(r.Context(), ws, user.ID, database.ID, r.PathValue("column"))
	h.finishWikiDatabaseMutation(w, r, database, err)
}

func (h *Handler) WikiDatabaseRowSave(w http.ResponseWriter, r *http.Request) {
	user, ws, database, ok := h.wikiDatabaseMutationContext(w, r)
	if !ok {
		return
	}
	data, err := h.Store.WikiDatabaseData(r.Context(), ws, user.ID, database.ID)
	if err != nil {
		h.finishWikiDatabaseMutation(w, r, database, err)
		return
	}
	values := make(map[string]string, len(data.Columns))
	for _, column := range data.Columns {
		values[column.Key] = r.PostFormValue("value_" + column.Key)
	}
	_, err = h.Commands.SaveWikiDatabaseRow(r.Context(), ws, user.ID, database.ID, r.PathValue("row"), values)
	h.finishWikiDatabaseMutation(w, r, database, err)
}

func (h *Handler) WikiDatabaseRowDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, database, ok := h.wikiDatabaseMutationContext(w, r)
	if !ok {
		return
	}
	err := h.Commands.DeleteWikiDatabaseRow(r.Context(), ws, user.ID, database.ID, r.PathValue("row"))
	h.finishWikiDatabaseMutation(w, r, database, err)
}

func (h *Handler) WikiDatabaseViewSave(w http.ResponseWriter, r *http.Request) {
	user, ws, database, ok := h.wikiDatabaseMutationContext(w, r)
	if !ok {
		return
	}
	_, err := h.Commands.SaveWikiDatabaseView(r.Context(), ws, user.ID, database.ID, models.WikiDatabaseView{
		Name: r.PostFormValue("name"), SortKey: r.PostFormValue("sortKey"), SortDirection: r.PostFormValue("sortDirection"), FilterKey: r.PostFormValue("filterKey"), FilterValue: r.PostFormValue("filterValue"),
	})
	h.finishWikiDatabaseMutation(w, r, database, err)
}

func (h *Handler) WikiDatabaseViewDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, database, ok := h.wikiDatabaseMutationContext(w, r)
	if !ok {
		return
	}
	err := h.Commands.DeleteWikiDatabaseView(r.Context(), ws, user.ID, database.ID, r.PathValue("view"))
	h.finishWikiDatabaseMutation(w, r, database, err)
}

func (h *Handler) wikiDatabaseMutationContext(w http.ResponseWriter, r *http.Request) (*models.User, string, *models.WikiContent, bool) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return nil, "", nil, false
	}
	_, database, ok := h.wikiDatabaseContext(w, r, ws, user.ID)
	return user, ws, database, ok
}

func (h *Handler) wikiDatabaseContext(w http.ResponseWriter, r *http.Request, ws, actor string) (*models.WikiSpace, *models.WikiContent, bool) {
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return nil, nil, false
	}
	database, err := h.Store.WikiContent(r.Context(), ws, actor, r.PathValue("database"), "database")
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return nil, nil, false
	}
	if database.SpaceID != space.ID {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return space, database, true
}

func (h *Handler) finishWikiDatabaseMutation(w http.ResponseWriter, r *http.Request, database *models.WikiContent, err error) {
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+database.SpaceID+"/databases/"+database.ID)
}
