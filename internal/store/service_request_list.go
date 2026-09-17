package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// ServiceRequestListFilter selects the customer requests a person lists, as
// Jira Service Management's request search does. Each ownership flag adds the
// requests it describes; the other fields narrow the result.
type ServiceRequestListFilter struct {
	ViewerID string
	// Owned, Participated, Organizations and Approver add requests the viewer
	// raised, participates in, shares through an organization (OrganizationID
	// narrows to one) or approves (ApprovalStatus narrows to pending or past).
	Owned, Participated, Organizations, Approver bool
	OrganizationID, ApprovalStatus               string
	// AllRequests adds every request the viewer may manage: every request for a
	// site administrator, the requests of desks they are an agent of otherwise.
	AllRequests, SiteAdmin                                  bool
	ServiceDeskID, RequestTypeID, RequestStatus, SearchTerm string
}

// ServiceRequestList lists the requests a filter selects, most recently active
// first.
func (s *Store) ServiceRequestList(ctx context.Context, workspaceID string, filter ServiceRequestListFilter) ([]*models.ServiceRequest, error) {
	args := []any{workspaceID}
	arg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	// The viewer is bound only when a clause names them: PostgreSQL cannot
	// type a parameter the query never uses.
	viewerParameter := ""
	viewer := func() string {
		if viewerParameter == "" {
			viewerParameter = arg(filter.ViewerID)
		}
		return viewerParameter
	}
	ownership := []string{}
	if filter.Owned {
		ownership = append(ownership, "sr.customer_id="+viewer())
	}
	if filter.Participated {
		ownership = append(ownership, "EXISTS(SELECT 1 FROM service_request_participants p WHERE p.request_issue_id=sr.issue_id AND p.user_id="+viewer()+")")
	}
	if filter.Organizations {
		// A customer's request is shared with the organizations they belong
		// to that the desk serves.
		organization := "TRUE"
		if filter.OrganizationID != "" {
			organization = "dso.organization_id=" + arg(filter.OrganizationID)
		}
		ownership = append(ownership, `EXISTS(SELECT 1 FROM service_desk_organizations dso
			JOIN service_organization_users member ON member.organization_id=dso.organization_id AND member.user_id=`+viewer()+`
			JOIN service_organization_users reporter ON reporter.organization_id=dso.organization_id AND reporter.user_id=sr.customer_id
			WHERE dso.service_desk_id=sr.service_desk_id AND `+organization+`)`)
	}
	if filter.Approver {
		decision := "TRUE"
		switch filter.ApprovalStatus {
		case "MY_PENDING_APPROVAL":
			decision = "ap.decision='pending' AND a.final_decision='pending'"
		case "MY_HISTORY_APPROVAL":
			decision = "(ap.decision<>'pending' OR a.final_decision<>'pending')"
		}
		ownership = append(ownership, `EXISTS(SELECT 1 FROM service_request_approvals a JOIN service_request_approvers ap ON ap.approval_id=a.id
			WHERE a.request_issue_id=sr.issue_id AND ap.user_id=`+viewer()+` AND `+decision+`)`)
	}
	if filter.AllRequests {
		if filter.SiteAdmin {
			ownership = append(ownership, "TRUE")
		} else {
			ownership = append(ownership, "EXISTS(SELECT 1 FROM service_desk_agents agent WHERE agent.service_desk_id=sr.service_desk_id AND agent.user_id="+viewer()+")")
		}
	}
	if len(ownership) == 0 {
		return []*models.ServiceRequest{}, nil
	}
	conditions := []string{"sr.workspace_id=$1", "(" + strings.Join(ownership, " OR ") + ")"}
	if filter.ServiceDeskID != "" {
		conditions = append(conditions, "sr.service_desk_id="+arg(filter.ServiceDeskID))
	}
	if filter.RequestTypeID != "" {
		conditions = append(conditions, "sr.request_type_id="+arg(filter.RequestTypeID))
	}
	switch filter.RequestStatus {
	// A request is open while it has no resolution and closed once it has one,
	// as the desk's queues decide it.
	case "OPEN_REQUESTS":
		conditions = append(conditions, "i.resolution_id IS NULL")
	case "CLOSED_REQUESTS":
		conditions = append(conditions, "i.resolution_id IS NOT NULL")
	}
	if term := strings.TrimSpace(filter.SearchTerm); term != "" {
		pattern := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`, "*", "%", "?", "_").Replace(term)
		conditions = append(conditions, "i.summary ILIKE "+arg("%"+pattern+"%"))
	}
	rows, err := s.Pool.Query(ctx, serviceRequestMetadataSelect+`
		JOIN issues i ON i.id=sr.issue_id
		JOIN statuses st ON st.id=i.status_id
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY i.updated_at DESC,sr.issue_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]*models.ServiceRequest, 0)
	for rows.Next() {
		request, err := s.serviceRequestFromRow(ctx, workspaceID, rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

// ServiceRequestStatusEntry is a status a request attained and when.
type ServiceRequestStatusEntry struct {
	Status models.Status
	At     time.Time
}

// ServiceRequestStatusHistory lists the statuses a request has attained, oldest
// first: the status it was created in, then every change of status.
func (s *Store) ServiceRequestStatusHistory(ctx context.Context, workspaceID, issueID string) ([]ServiceRequestStatusEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH changes AS (
			SELECT a.seq,a.created_at,a.payload->'diff'->'status'->>'from' AS from_id,a.payload->'diff'->'status'->>'to' AS to_id
			FROM actions a
			WHERE a.workspace_id=$1 AND a.entity_type='issue' AND a.entity_id=$2
			  AND a.payload->'diff'->'status'->>'to' IS NOT NULL
			  AND a.payload->'diff'->'status'->>'from' IS DISTINCT FROM a.payload->'diff'->'status'->>'to'
		), attained AS (
			SELECT COALESCE((SELECT from_id FROM changes ORDER BY seq LIMIT 1),i.status_id) AS status_id,i.created_at AS at,0::bigint AS seq
			FROM issues i WHERE i.workspace_id=$1 AND i.id=$2
			UNION ALL
			SELECT to_id,created_at,seq FROM changes
		)
		SELECT st.id,st.name,st.category,attained.at
		FROM attained JOIN statuses st ON st.id=attained.status_id
		ORDER BY attained.seq`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []ServiceRequestStatusEntry{}
	for rows.Next() {
		var entry ServiceRequestStatusEntry
		if err := rows.Scan(&entry.Status.ID, &entry.Status.Name, &entry.Status.Category, &entry.At); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
