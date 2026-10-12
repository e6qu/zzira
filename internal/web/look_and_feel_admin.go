package web

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// lookAndFeelField is one application property the look and feel page edits,
// with the label and help a person reads beside it.
type lookAndFeelField struct {
	Key, Label, Help, Value, Placeholder string
	Colour                               bool
}

// lookAndFeelPageData is the look and feel settings page.
type lookAndFeelPageData struct {
	Fields    []lookAndFeelField
	ShowTitle bool
	Notice    string
	Error     string
}

// lookAndFeelKeys are the properties the page owns, in the order it shows
// them. Jira names each one; a site that leaves one empty keeps ZZIRA's own.
var lookAndFeelKeys = []lookAndFeelField{
	{Key: "jira.title", Label: "Site title", Help: "Shown in the browser tab and beside the logo.", Placeholder: models.DefaultSiteTitle},
	{Key: "jira.lf.logo.url", Label: "Logo URL", Help: "An address this site can load, such as /static/img/logo.svg."},
	{Key: "jira.lf.favicon.url", Label: "Favicon URL", Help: "The icon a browser shows for this site."},
	{Key: "jira.lf.favicon.hires.url", Label: "High-resolution favicon URL", Help: "Used by devices that ask for a larger icon."},
	{Key: "jira.lf.navigation.bgcolour", Label: "Navigation background", Help: "A colour name, #rgb, #rrggbb or rgb().", Colour: true},
	{Key: "jira.lf.navigation.highlightcolour", Label: "Navigation highlight", Help: "Marks the page a person is on.", Colour: true},
	{Key: "jira.lf.hero.button.base.bg.colour", Label: "Primary button colour", Help: "White label text must stay readable on it, so a pale colour is refused.", Colour: true},
	{Key: "jira.lf.date.complete", Label: "Complete date format", Help: "A Java pattern, such as dd/MMM/yy h:mm a.", Placeholder: "dd/MMM/yy h:mm a"},
	{Key: "jira.lf.date.dmy", Label: "Day/Month/Year format", Help: "A Java pattern, such as dd/MMM/yy.", Placeholder: "dd/MMM/yy"},
}

// LookAndFeelPage shows the site's branding and date formats.
func (h *Handler) LookAndFeelPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load the site configuration.", http.StatusInternalServerError)
		return
	}
	data := lookAndFeelPageData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	for _, field := range lookAndFeelKeys {
		field.Value = configuration.ApplicationProperties[field.Key]
		data.Fields = append(data.Fields, field)
	}
	data.ShowTitle = configuration.ApplicationProperties["jira.lf.logo.show.application.title"] == "true"
	h.writeWorkspacePage(w, r, "page_look_and_feel", user, workspaceID, data, "look-and-feel", "")
}

// LookAndFeelMutation saves the branding and date formats, refusing a colour
// the pages could not draw or a date pattern they could not read rather than
// storing it and quietly ignoring it afterwards.
func (h *Handler) LookAndFeelMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	reset := r.PostFormValue("action") == "reset"
	notice := "Look and feel saved."
	if reset {
		notice = "Look and feel restored to the defaults."
	}
	for _, field := range lookAndFeelKeys {
		value := strings.TrimSpace(r.PostFormValue(field.Key))
		if reset {
			value = ""
		}
		if value != "" {
			// A colour has to be one the pages can measure as well as draw:
			// the contrast checks below, and the themes, work from hex and
			// rgb() values. A bare word passes the shared pattern -- it could
			// be a CSS name -- but nothing here can compute with it.
			if field.Colour {
				if _, parsed := relativeLuminance(value); !parsed || !lookAndFeelColour.MatchString(value) {
					h.redirectMetadata(w, r, "/settings/look-and-feel", "", &lookAndFeelError{field.Label + " must be a hex colour such as #0052cc, or an rgb() value."})
					return
				}
			}
			if field.Key == "jira.lf.hero.button.base.bg.colour" && whiteTextContrast(value) < 4.5 {
				h.redirectMetadata(w, r, "/settings/look-and-feel", "", &lookAndFeelError{"White label text would not be readable on that primary button colour."})
				return
			}
			if strings.HasPrefix(field.Key, "jira.lf.date.") {
				if _, ok := models.JavaDateLayout(value); !ok {
					h.redirectMetadata(w, r, "/settings/look-and-feel", "", &lookAndFeelError{field.Label + " is not a date pattern this site can read."})
					return
				}
			}
		}
		if err := h.Store.UpdateApplicationProperty(r.Context(), workspaceID, user.ID, field.Key, value); err != nil {
			h.redirectMetadata(w, r, "/settings/look-and-feel", "", err)
			return
		}
	}
	shown := "false"
	if !reset && r.PostFormValue("jira.lf.logo.show.application.title") != "" {
		shown = "true"
	}
	if err := h.Store.UpdateApplicationProperty(r.Context(), workspaceID, user.ID, "jira.lf.logo.show.application.title", shown); err != nil {
		h.redirectMetadata(w, r, "/settings/look-and-feel", "", err)
		return
	}
	h.redirectMetadata(w, r, "/settings/look-and-feel", notice, nil)
}

// lookAndFeelError is a refusal a person reads on the page.
type lookAndFeelError struct{ message string }

func (e *lookAndFeelError) Error() string { return e.message }
