package web

import (
	"log"
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

// BoardPage serves /board/{id} — the sprint board.
func (h *Handler) BoardPage(w http.ResponseWriter, r *http.Request, id string) {
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	columns, err := h.Store.BoardIssues(r.Context(), board.ID, user.ID)
	if err != nil {
		log.Printf("%s: %v", "board.go", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	statuses := map[string]models.Status{}
	for _, st := range board.ColumnStatusIDs {
		s, err := h.Store.StatusByID(r.Context(), st)
		if err != nil {
			log.Printf("%s: %v", "board.go", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		statuses[st] = s
	}
	boardData := boardViewData{Board: board, Columns: make([]boardColumn, 0, len(board.ColumnStatusIDs))}
	for _, st := range board.ColumnStatusIDs {
		s := statuses[st]
		boardData.Columns = append(boardData.Columns, boardColumn{
			StatusID: st, Name: s.Name, Category: s.Category, Issues: columns[st],
		})
	}
	writePage(w, "page_board", pageData{User: user, Data: boardData, Active: "board"})
}

// BoardFragment serves GET /board/{id}/fragment — the live-swap region.
func (h *Handler) BoardFragment(w http.ResponseWriter, r *http.Request, id string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	columns, err := h.Store.BoardIssues(r.Context(), board.ID, user.ID)
	if err != nil {
		log.Printf("%s: %v", "board.go", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	boardData := boardViewData{Board: board, Columns: make([]boardColumn, 0, len(board.ColumnStatusIDs))}
	for _, st := range board.ColumnStatusIDs {
		s, err := h.Store.StatusByID(r.Context(), st)
		if err != nil {
			log.Printf("%s: %v", "board.go", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		boardData.Columns = append(boardData.Columns, boardColumn{StatusID: st, Name: s.Name, Category: s.Category, Issues: columns[st]})
	}
	writeFragment(w, "board_fragment", boardData)
}

type boardColumn struct {
	StatusID string
	Name     string
	Category string
	Issues   []*models.Issue
}

type boardViewData struct {
	Board   *models.Board
	Columns []boardColumn
}

// RankIssue applies a drag: rank between neighbors, optionally new status.
func (h *Handler) RankIssue(w http.ResponseWriter, r *http.Request, boardID string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !boardHasStatus(board, r.PostFormValue("status")) {
		http.Error(w, "status is not a board column", http.StatusBadRequest)
		return
	}
	if err := h.Commands.SetIssueRank(r.Context(), user.ID, wsID,
		r.PostFormValue("issue"), r.PostFormValue("before"), r.PostFormValue("after"),
		r.PostFormValue("status")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func boardHasStatus(b *models.Board, statusID string) bool {
	for _, s := range b.ColumnStatusIDs {
		if s == statusID {
			return true
		}
	}
	return false
}
