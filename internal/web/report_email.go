package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// reportActions is what every report page offers besides the report itself:
// its CSV download and the caller's scheduled emails of it.
type reportActions struct {
	CSVURL, Target, EmailError string
	Subscriptions              []models.ReportSubscription
	Members                    []*models.User
	CurrentUserID              string
}

var reportEmailErrors = map[string]string{
	"access":  "Everyone who receives this report must be able to open it.",
	"invalid": "Choose a delivery and at most 50 recipients who are active members of this site.",
	"missing": "That report email no longer exists.",
}

func (h *Handler) reportActions(r *http.Request, workspaceID string, user *models.User) (reportActions, error) {
	target, err := reportTarget(r.URL.Path, r.URL.RawQuery)
	if err != nil {
		return reportActions{}, err
	}
	actions := reportActions{CSVURL: csvURL(r), Target: target, CurrentUserID: user.ID, EmailError: reportEmailErrors[r.URL.Query().Get("emailError")]}
	if actions.Subscriptions, err = h.Store.ReportSubscriptions(r.Context(), workspaceID, user.ID, target); err != nil {
		return reportActions{}, err
	}
	actions.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
	return actions, err
}

var reportPath = regexp.MustCompile(`^/(projects/[A-Za-z0-9_-]{1,64}/reports/(dora|sprint|velocity|cumulative-flow|control-chart|epic|version|created-vs-resolved|resolution-time|user-workload|version-workload|time-tracking|group-by)|service/agent/[A-Za-z0-9_.:-]{1,128}/reports)$`)

var errNotAReport = errors.New("not a report")

// reportTarget is a report page with its choices, in one canonical form so
// the same report with the same choices is always the same email.
func reportTarget(path, rawQuery string) (string, error) {
	if !reportPath.MatchString(path) {
		return "", errNotAReport
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", errNotAReport
	}
	query.Del("format")
	query.Del("emailError")
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	if len(target) > 2000 {
		return "", errNotAReport
	}
	return target, nil
}

type reportRecipientKey struct{}

// inProcessResponse keeps the answer of a request served in-process.
type inProcessResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	title  string
}

func (r *inProcessResponse) Header() http.Header { return r.header }

func (r *inProcessResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *inProcessResponse) Write(data []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(data)
}

func (r *inProcessResponse) setReportTitle(title string) { r.title = title }

var errReportUnavailable = errors.New("the report could not be opened")

// RenderReport draws a report's CSV as a member sees it, through the same
// route that serves the download, and names the report. Routes must be
// the application's routes.
func (h *Handler) RenderReport(ctx context.Context, userID, target string) (string, string, error) {
	path, rawQuery, _ := strings.Cut(target, "?")
	if clean, err := reportTarget(path, rawQuery); err != nil || clean != target {
		return "", "", errNotAReport
	}
	if h.Routes == nil {
		return "", "", errors.New("report routes are not configured")
	}
	query, _ := url.ParseQuery(rawQuery)
	query.Set("format", "csv")
	// The request is served in-process by Routes and never sent over a
	// network; its path matched the report allowlist above.
	request, err := http.NewRequestWithContext(context.WithValue(ctx, reportRecipientKey{}, userID), http.MethodGet, path+"?"+query.Encode(), nil) // #nosec G704 -- in-process request to an allowlisted local report path, served by Routes.ServeHTTP without any network client.
	if err != nil {
		return "", "", err
	}
	response := &inProcessResponse{header: http.Header{}}
	h.Routes.ServeHTTP(response, request)
	if response.status != http.StatusOK || !strings.HasPrefix(response.header.Get("Content-Type"), "text/csv") || response.title == "" {
		return "", "", errReportUnavailable
	}
	return response.title, response.body.String(), nil
}

// ReportEmail schedules or removes an email of the report the form is on, and
// returns to that report.
func (h *Handler) ReportEmail(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	path, rawQuery, _ := strings.Cut(r.PostFormValue("report"), "?")
	target, err := reportTarget(path, rawQuery)
	if err != nil {
		http.Error(w, "Choose a report to email.", http.StatusBadRequest)
		return
	}
	problem := ""
	switch r.PostFormValue("action") {
	case "subscribe":
		recipients := r.PostForm["recipient"]
		if len(recipients) > 50 {
			problem = "invalid"
			break
		}
		for _, recipient := range append([]string{user.ID}, recipients...) {
			if _, _, renderErr := h.RenderReport(r.Context(), strings.TrimSpace(recipient), target); renderErr != nil {
				problem = "access"
				break
			}
		}
		if problem != "" {
			break
		}
		schedule := r.PostFormValue("schedule")
		if custom := strings.TrimSpace(r.PostFormValue("cron")); custom != "" {
			schedule = custom
		}
		if _, err = h.Store.SaveReportSubscription(r.Context(), workspaceID, user.ID, target, schedule, r.PostFormValue("timezone"), recipients); errors.Is(err, store.ErrReportSubscriptionValidation) {
			problem = "invalid"
		} else if err != nil {
			http.Error(w, "Could not schedule the report email.", http.StatusInternalServerError)
			return
		}
	case "unsubscribe":
		subscriptionID, parseErr := strconv.ParseInt(r.PostFormValue("subscriptionId"), 10, 64)
		if parseErr != nil {
			problem = "missing"
		} else if err = h.Store.DeleteReportSubscription(r.Context(), workspaceID, user.ID, target, subscriptionID); errors.Is(err, pgx.ErrNoRows) {
			problem = "missing"
		} else if err != nil {
			http.Error(w, "Could not remove the report email.", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "Choose whether to schedule or remove the email.", http.StatusBadRequest)
		return
	}
	if problem != "" {
		path, rawQuery, _ = strings.Cut(target, "?")
		query, _ := url.ParseQuery(rawQuery)
		query.Set("emailError", problem)
		target = path + "?" + query.Encode()
	}
	redirectLocal(w, r, target)
}
