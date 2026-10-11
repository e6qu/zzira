package web

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) editDialogEstimates(r *http.Request, ws string, in *commands.UpdateIssueInput) error {
	_, originalProvided := r.PostForm["originalEstimate"]
	_, remainingProvided := r.PostForm["remainingEstimate"]
	if !originalProvided && !remainingProvided {
		return nil
	}
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), ws)
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name        string
		destination **int64
	}{{"originalEstimate", &in.OriginalEstimate}, {"remainingEstimate", &in.RemainingEstimate}} {
		values, provided := r.PostForm[entry.name]
		if !provided || len(values) == 0 {
			continue
		}
		seconds := store.ClearEstimate
		if value := strings.TrimSpace(values[0]); value != "" {
			seconds, err = models.ParseJiraDuration(value, configuration.TimeTracking)
			if err != nil {
				return err
			}
		}
		*entry.destination = &seconds
	}
	return nil
}
