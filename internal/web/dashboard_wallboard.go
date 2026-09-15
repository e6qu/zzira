package web

import (
	"errors"
	"log"
	"math/rand/v2"
	"net/http"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// wallboardGadget is a gadget on a wallboard, with where it falls in its
// rotating group.
type wallboardGadget struct {
	Tile            dashboardTile
	Position, Count int
}

// wallboardSlide is one dashboard shown as a wallboard. Within a column,
// consecutive gadgets of one colour form a group that shows one gadget at a
// time, as Jira's wallboard does.
type wallboardSlide struct {
	Dashboard *models.Dashboard
	Columns   [][][]wallboardGadget
}

type wallboardData struct {
	Title, ExitURL, Exit string
	Slides               []wallboardSlide
	IntervalSeconds      int
	RefreshMS            int
	Slideshow, Rotates   bool
}

// wallboardSlide loads a dashboard's gadgets as someone sees them, grouped for
// the wallboard. A dashboard they can no longer view is reported as missing.
func (h *Handler) wallboardSlide(r *http.Request, ws, userID string, d *models.Dashboard) (wallboardSlide, bool, error) {
	slide := wallboardSlide{Dashboard: d, Columns: make([][][]wallboardGadget, d.Columns())}
	gadgets, err := h.Store.DashboardGadgets(r.Context(), ws, userID, d.ID)
	if err != nil {
		return slide, false, err
	}
	// Gadgets are placed read-only: a wallboard offers no editing controls.
	tiles := h.dashboardTiles(r, ws, userID, d.ID, gadgets, false)
	rotates := false
	for index, g := range gadgets {
		column := &slide.Columns[g.Position.Column]
		if last := len(*column) - 1; last >= 0 && (*column)[last][0].Tile.Gadget.Color == g.Color {
			(*column)[last] = append((*column)[last], wallboardGadget{Tile: tiles[index]})
			rotates = true
			continue
		}
		*column = append(*column, []wallboardGadget{{Tile: tiles[index]}})
	}
	for _, column := range slide.Columns {
		for _, group := range column {
			for i := range group {
				group[i].Position, group[i].Count = i+1, len(group)
			}
		}
	}
	return slide, rotates, nil
}

// DashboardWallboard shows a dashboard full screen without navigation, for a
// team's shared display.
func (h *Handler) DashboardWallboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	d, err := h.Store.Dashboard(r.Context(), ws, user.ID, r.PathValue("id"))
	if err != nil {
		status, msg := dashboardWebError(err)
		http.Error(w, msg, status)
		return
	}
	slide, rotates, err := h.wallboardSlide(r, ws, user.ID, d)
	if err != nil {
		status, msg := dashboardWebError(err)
		http.Error(w, msg, status)
		return
	}
	data := wallboardData{Title: d.Name, ExitURL: "/dashboards/" + d.ID, Exit: "Exit wallboard", Slides: []wallboardSlide{slide}, IntervalSeconds: 30, RefreshMS: d.RefreshMS, Rotates: rotates}
	h.writeWorkspacePage(w, r, "page_dashboard_wallboard", user, ws, data, "dashboards", "")
}

// DashboardWallboardSlideshow shows the site's slide show: each configured
// dashboard the viewer can see, in turn.
func (h *Handler) DashboardWallboardSlideshow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	show, err := h.Store.DashboardWallboardSlideshow(r.Context(), ws)
	if err != nil {
		log.Print("wallboard slide show: ", err)
		http.Error(w, "Could not load the wallboard slide show.", 500)
		return
	}
	data := wallboardData{Title: "Wallboard slide show", ExitURL: "/dashboards", Exit: "Exit slide show", IntervalSeconds: show.IntervalSeconds, Slideshow: true}
	for _, id := range show.DashboardIDs {
		d, err := h.Store.Dashboard(r.Context(), ws, user.ID, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		var slide wallboardSlide
		var rotates bool
		if err == nil {
			slide, rotates, err = h.wallboardSlide(r, ws, user.ID, d)
		}
		if err != nil {
			status, msg := dashboardWebError(err)
			http.Error(w, msg, status)
			return
		}
		data.Slides = append(data.Slides, slide)
		data.Rotates = data.Rotates || rotates
	}
	if show.RandomOrder {
		rand.Shuffle(len(data.Slides), func(i, j int) { data.Slides[i], data.Slides[j] = data.Slides[j], data.Slides[i] })
	}
	data.Rotates = data.Rotates || len(data.Slides) > 1
	h.writeWorkspacePage(w, r, "page_dashboard_wallboard", user, ws, data, "dashboards", "")
}
