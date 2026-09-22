package web

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// A provider writes people and groups into the directory over SCIM, and until
// now the only way to see whether it had was to read the database: the API
// answered a provider, and nothing answered an administrator. This is what an
// administrator needs -- where to point the provider, whether it has written,
// and who it has written.

// adminProvisioning is the user provisioning section of the admin page.
type adminProvisioning struct {
	// DirectoryID and BaseURL are where a provider is pointed.
	DirectoryID string
	BaseURL     string
	// Connected reports that a provider has written to this directory.
	Connected bool
	// LastWritten is when it last did, read as the site reads dates.
	LastWritten string
	// People and Groups are what the provider manages.
	People []adminProvisionedPerson
	Groups []adminProvisionedGroup
}

// adminProvisionedPerson is one account a provider created.
type adminProvisionedPerson struct {
	DisplayName string
	Email       string
	ExternalID  string
	Active      bool
	Groups      string
	Updated     string
}

// adminProvisionedGroup is one group a provider manages.
type adminProvisionedGroup struct {
	Name       string
	ExternalID string
	Members    int
	Updated    string
}

// provisioning reads what a provider has written into the site's directory.
// A site no provider has touched still shows where one would be pointed,
// because that is the question an administrator setting one up is asking.
func (h *Handler) provisioning(ctx context.Context, workspaceID string, directory *models.Directory, layout string) (adminProvisioning, error) {
	view := adminProvisioning{
		DirectoryID: directory.ID,
		BaseURL:     strings.TrimSuffix(h.BaseURL, "/") + "/scim/directory/" + directory.ID,
	}
	managed, err := h.Store.DirectorySCIMManaged(ctx, workspaceID)
	if err != nil {
		return adminProvisioning{}, err
	}
	view.Connected = managed
	people, err := h.Store.SCIMUsers(ctx, directory.ID, "", "")
	if err != nil {
		return adminProvisioning{}, err
	}
	latest := time.Time{}
	for _, person := range people {
		// Everyone in the directory is a SCIM user as far as the API is
		// concerned; the ones a provider created are those it gave an
		// external id, and those are the ones this page is about.
		if person.User == nil || person.ExternalID == "" {
			continue
		}
		names := make([]string, 0, len(person.Groups))
		for _, group := range person.Groups {
			names = append(names, group.Name)
		}
		sort.Strings(names)
		if person.Updated.After(latest) {
			latest = person.Updated
		}
		view.People = append(view.People, adminProvisionedPerson{
			DisplayName: person.User.DisplayName, Email: person.User.Email, ExternalID: person.ExternalID,
			Active: person.User.Active, Groups: strings.Join(names, ", "),
			Updated: displayMoment(person.Updated, layout),
		})
	}
	groups, err := h.Store.SCIMGroups(ctx, directory.ID, "", "")
	if err != nil {
		return adminProvisioning{}, err
	}
	for _, group := range groups {
		if group.Group == nil || group.ExternalID == "" {
			continue
		}
		if group.Updated.After(latest) {
			latest = group.Updated
		}
		view.Groups = append(view.Groups, adminProvisionedGroup{
			Name: group.Group.Name, ExternalID: group.ExternalID, Members: len(group.Members),
			Updated: displayMoment(group.Updated, layout),
		})
	}
	sort.Slice(view.People, func(first, second int) bool {
		return strings.ToLower(view.People[first].DisplayName) < strings.ToLower(view.People[second].DisplayName)
	})
	sort.Slice(view.Groups, func(first, second int) bool {
		return strings.ToLower(view.Groups[first].Name) < strings.ToLower(view.Groups[second].Name)
	})
	if !latest.IsZero() {
		view.LastWritten = displayMoment(latest, layout)
	}
	return view, nil
}

// displayMoment reads a time the way the site reads dates, and answers
// nothing for a time that was never set.
func displayMoment(at time.Time, layout string) string {
	if at.IsZero() {
		return ""
	}
	return at.In(time.Local).Format(layout)
}
