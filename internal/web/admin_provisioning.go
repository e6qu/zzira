package web

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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
	// Keys are the provisioning keys this directory has issued, and Issued
	// is a key just made, shown once because the site keeps only its hash.
	Keys   []adminProvisioningKey
	Issued string
}

// adminProvisioningKey is one key a provider can provision this directory
// with.
type adminProvisioningKey struct {
	ID       string
	Name     string
	Created  string
	LastUsed string
	Revoked  bool
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
	keys, err := h.Store.DirectoryAPIKeys(ctx, directory.ID)
	if err != nil {
		return adminProvisioning{}, err
	}
	for _, key := range keys {
		shown := adminProvisioningKey{ID: key.ID, Name: key.Name, Created: displayMoment(key.CreatedAt, layout), Revoked: key.RevokedAt != nil}
		if key.LastUsedAt != nil {
			shown.LastUsed = displayMoment(*key.LastUsedAt, layout)
		}
		view.Keys = append(view.Keys, shown)
	}
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

// AdminProvisioningKeys issues and revokes the keys a provider provisions this
// site's directory with. A key is shown once, because the site keeps only its
// hash -- a key that is lost is replaced rather than recovered.
func (h *Handler) AdminProvisioningKeys(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil || organization == nil {
		http.Error(w, "Could not read the organization.", http.StatusInternalServerError)
		return
	}
	switch r.PostFormValue("action") {
	case "issue":
		directories, err := h.Store.DirectoriesByOrganization(r.Context(), organization.ID)
		if err != nil || len(directories) == 0 {
			http.Error(w, "Could not read the directory.", http.StatusInternalServerError)
			return
		}
		plain, hash, err := authn.NewAPIToken()
		if err != nil {
			http.Error(w, "Could not issue a key.", http.StatusInternalServerError)
			return
		}
		if _, err := h.Store.CreateDirectoryAPIKey(r.Context(), organization.ID, user.ID, directories[0].ID,
			r.PostFormValue("name"), hash); err != nil {
			h.adminProvisioningBack(w, r, "", err.Error())
			return
		}
		// The key travels back in the URL once, as a password reset link
		// does: it is what the person is here to copy, and the site cannot
		// show it again.
		h.adminProvisioningBack(w, r, plain, "")
	case "revoke":
		if err := h.Store.RevokeDirectoryAPIKey(r.Context(), organization.ID, strings.TrimSpace(r.PostFormValue("key"))); err != nil {
			h.adminProvisioningBack(w, r, "", err.Error())
			return
		}
		h.adminProvisioningBack(w, r, "", "")
	default:
		http.Error(w, "unknown provisioning key action", http.StatusBadRequest)
	}
}

// adminProvisioningBack returns to the provisioning section, carrying a key
// that was just issued or what went wrong.
func (h *Handler) adminProvisioningBack(w http.ResponseWriter, r *http.Request, issued, failure string) {
	query := url.Values{}
	switch {
	case failure != "":
		query.Set("error", strings.TrimPrefix(failure, store.ErrDirectoryKey.Error()+": "))
	case issued != "":
		query.Set("issued", issued)
	default:
		query.Set("saved", "Provisioning key revoked")
	}
	redirectLocal(w, r, "/admin?"+query.Encode()+"#admin-provisioning")
}
