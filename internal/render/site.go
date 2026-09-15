package render

import (
	"github.com/e6qu/zzira/internal/models"
	"html/template"
)

// SiteLook is the look and feel a site's application properties set.
type SiteLook struct {
	Title, LogoURL, FaviconURL                string
	NavigationBackground, NavigationHighlight string
	// ShowTitle shows the application title beside the logo, as Jira's
	// "Show application title" setting does; the logo link always names it.
	ShowTitle bool
	// FaviconHiResURL is the high-resolution favicon for devices that ask
	// for a larger icon.
	FaviconHiResURL string
	// ButtonStyle colours primary buttons with Jira's hero button background.
	ButtonStyle template.CSS
	// DateComplete and DateDay are Go layouts for displayed times and days.
	DateComplete, DateDay string
}

// DefaultSiteLook is ZZIRA's own look.
var DefaultSiteLook = SiteLook{Title: models.DefaultSiteTitle, DateComplete: models.DefaultCompleteDateLayout, DateDay: models.DefaultDayDateLayout}

// SiteLooker is page data that carries the site's look and feel.
type SiteLooker interface {
	SiteLook() SiteLook
}

// siteLook is the look and feel of the page being rendered; pages rendered
// without a site, such as sign-in, use ZZIRA's own.
func siteLook(root any) SiteLook {
	if looker, ok := root.(SiteLooker); ok {
		return looker.SiteLook()
	}
	return DefaultSiteLook
}
