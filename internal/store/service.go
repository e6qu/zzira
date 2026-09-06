package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func scanServiceDesk(row interface{ Scan(...any) error }) (*models.ServiceDesk, error) {
	desk := &models.ServiceDesk{}
	err := row.Scan(&desk.ID, &desk.WorkspaceID, &desk.ProjectID, &desk.ProjectKey, &desk.ProjectName, &desk.ProjectTypeKey, &desk.PortalName)
	return desk, err
}

const serviceDeskSelect = `SELECT sd.id,sd.workspace_id,p.id,p.key,p.name,p.project_type_key,sd.portal_name FROM service_desks sd JOIN projects p ON p.id=sd.project_id `

func (s *Store) ServiceDesks(ctx context.Context, workspaceID string) ([]models.ServiceDesk, error) {
	rows, err := s.Pool.Query(ctx, serviceDeskSelect+`WHERE sd.workspace_id=$1 ORDER BY sd.id::bigint`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceDesk, 0)
	for rows.Next() {
		desk, err := scanServiceDesk(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, *desk)
	}
	return values, rows.Err()
}

func (s *Store) ServiceDesk(ctx context.Context, workspaceID, id string) (*models.ServiceDesk, error) {
	return scanServiceDesk(s.Pool.QueryRow(ctx, serviceDeskSelect+`WHERE sd.workspace_id=$1 AND sd.id=$2`, workspaceID, id))
}

func scanServiceRequestType(row interface{ Scan(...any) error }) (*models.ServiceRequestType, error) {
	requestType := &models.ServiceRequestType{}
	err := row.Scan(&requestType.ID, &requestType.ServiceDeskID, &requestType.Name, &requestType.Description, &requestType.HelpText, &requestType.IssueTypeID, &requestType.GroupIDs)
	return requestType, err
}

const serviceRequestTypeSelect = `SELECT id,service_desk_id,name,description,help_text,issue_type_id,group_ids FROM service_request_types `

func (s *Store) ServiceRequestTypes(ctx context.Context, workspaceID, serviceDeskID, search string) ([]models.ServiceRequestType, error) {
	search = strings.TrimSpace(search)
	rows, err := s.Pool.Query(ctx, serviceRequestTypeSelect+`
		WHERE service_desk_id IN (SELECT id FROM service_desks WHERE workspace_id=$1)
		  AND ($2='' OR service_desk_id=$2) AND ($3='' OR name ILIKE '%'||$3||'%' OR description ILIKE '%'||$3||'%')
		ORDER BY service_desk_id::bigint,id::bigint`, workspaceID, serviceDeskID, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceRequestType, 0)
	for rows.Next() {
		requestType, err := scanServiceRequestType(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, *requestType)
	}
	return values, rows.Err()
}

func (s *Store) ServiceRequestType(ctx context.Context, workspaceID, serviceDeskID, id string) (*models.ServiceRequestType, error) {
	return scanServiceRequestType(s.Pool.QueryRow(ctx, serviceRequestTypeSelect+`
		WHERE service_desk_id=$2 AND id=$3 AND service_desk_id IN (SELECT id FROM service_desks WHERE workspace_id=$1)`, workspaceID, serviceDeskID, id))
}

func (s *Store) CreateServiceRequestType(ctx context.Context, workspaceID, serviceDeskID, name, description, helpText, issueTypeID string) (*models.ServiceRequestType, error) {
	requestType := &models.ServiceRequestType{}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO service_request_types(service_desk_id,name,description,help_text,issue_type_id)
		SELECT sd.id,$3,$4,$5,$6 FROM service_desks sd
		WHERE sd.workspace_id=$1 AND sd.id=$2
		RETURNING id,service_desk_id,name,description,help_text,issue_type_id,group_ids`, workspaceID, serviceDeskID, name, description, helpText, issueTypeID).Scan(
		&requestType.ID, &requestType.ServiceDeskID, &requestType.Name, &requestType.Description,
		&requestType.HelpText, &requestType.IssueTypeID, &requestType.GroupIDs)
	return requestType, err
}

func (s *Store) DeleteServiceRequestType(ctx context.Context, workspaceID, serviceDeskID, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_request_types WHERE id=$3 AND service_desk_id=$2 AND service_desk_id IN (SELECT id FROM service_desks WHERE workspace_id=$1)`, workspaceID, serviceDeskID, id)
	return err
}

// EnrollServiceCustomer marks an existing account as a portal customer for a
// workspace. Product access remains governed independently by role bindings.
func (s *Store) EnrollServiceCustomer(ctx context.Context, workspaceID, userID string) error {
	result, err := s.Pool.Exec(ctx, `
		INSERT INTO service_customers(workspace_id,user_id)
		SELECT $1,$2 WHERE EXISTS(SELECT 1 FROM users WHERE id=$2 AND active)
		ON CONFLICT(workspace_id,user_id) DO UPDATE SET active=TRUE`, workspaceID, userID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("customer account does not exist")
	}
	return nil
}

// CreateServiceCustomer provisions a portal-only account. It deliberately does
// not add product membership; invitation and authentication policy can grant
// the atlassian/customer role independently.
func (s *Store) CreateServiceCustomer(ctx context.Context, workspaceID, email, displayName string) (*models.User, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	userID := NewID("usr")
	err = tx.QueryRow(ctx, `
		INSERT INTO users(id,email,password_hash,display_name)
		VALUES($1,lower($2),'', $3)
		ON CONFLICT(email) DO UPDATE SET display_name=users.display_name
		RETURNING id`, userID, email, displayName).Scan(&userID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO service_customers(workspace_id,user_id) VALUES($1,$2)
		ON CONFLICT(workspace_id,user_id) DO UPDATE SET active=TRUE`, workspaceID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

func (s *Store) ServiceCustomer(ctx context.Context, workspaceID, userIDOrEmail string) (*models.User, error) {
	var userID string
	err := s.Pool.QueryRow(ctx, `
		SELECT u.id FROM service_customers sc JOIN users u ON u.id=sc.user_id
		WHERE sc.workspace_id=$1 AND sc.active AND (u.id=$2 OR lower(u.email)=lower($2))`, workspaceID, userIDOrEmail).Scan(&userID)
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

func (s *Store) CreateServiceRequest(ctx context.Context, workspaceID, issueID, serviceDeskID, requestTypeID, customerID, channel string) error {
	result, err := s.Pool.Exec(ctx, `
		INSERT INTO service_requests(issue_id,workspace_id,service_desk_id,request_type_id,customer_id,channel)
		SELECT i.id,$1,sd.id,rt.id,$5,$6
		FROM issues i JOIN service_desks sd ON sd.project_id=i.project_id
		JOIN service_request_types rt ON rt.service_desk_id=sd.id
		JOIN service_customers sc ON sc.workspace_id=$1 AND sc.user_id=$5 AND sc.active
		WHERE i.id=$2 AND i.workspace_id=$1 AND sd.id=$3 AND rt.id=$4`, workspaceID, issueID, serviceDeskID, requestTypeID, customerID, channel)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("request type, issue, and customer must belong to the service desk")
	}
	return nil
}

func (s *Store) serviceRequestFromRow(ctx context.Context, workspaceID string, row pgx.Row) (*models.ServiceRequest, error) {
	var issueID, deskID, requestTypeID, customerID, channel string
	request := &models.ServiceRequest{}
	if err := row.Scan(&issueID, &deskID, &requestTypeID, &customerID, &channel, &request.CreatedAt); err != nil {
		return nil, err
	}
	var err error
	request.Issue, err = s.IssueByIDOrKey(ctx, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	desk, err := s.ServiceDesk(ctx, workspaceID, deskID)
	if err != nil {
		return nil, err
	}
	request.ServiceDesk = *desk
	requestType, err := s.ServiceRequestType(ctx, workspaceID, deskID, requestTypeID)
	if err != nil {
		return nil, err
	}
	request.RequestType = *requestType
	request.Customer, err = s.UserByID(ctx, customerID)
	request.Channel = channel
	return request, err
}

const serviceRequestMetadataSelect = `SELECT sr.issue_id,sr.service_desk_id,sr.request_type_id,sr.customer_id,sr.channel,sr.created_at FROM service_requests sr `

func (s *Store) ServiceRequest(ctx context.Context, workspaceID, viewerID, issueIDOrKey string, allowAll bool) (*models.ServiceRequest, error) {
	return s.serviceRequestFromRow(ctx, workspaceID, s.Pool.QueryRow(ctx, serviceRequestMetadataSelect+`
		JOIN issues i ON i.id=sr.issue_id
		WHERE sr.workspace_id=$1 AND (sr.issue_id=$3 OR upper(i.key)=upper($3))
		  AND ($4 OR sr.customer_id=$2 OR EXISTS(SELECT 1 FROM service_request_participants p WHERE p.request_issue_id=sr.issue_id AND p.user_id=$2))`, workspaceID, viewerID, issueIDOrKey, allowAll))
}

func (s *Store) ServiceRequestParticipants(ctx context.Context, requestIssueID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id FROM service_request_participants WHERE request_issue_id=$1 ORDER BY added_at,user_id`, requestIssueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]*models.User, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		user, err := s.UserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) UpdateServiceRequestParticipants(ctx context.Context, workspaceID, requestIssueID string, userIDs []string, remove bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var reporterID string
	if err := tx.QueryRow(ctx, `SELECT customer_id FROM service_requests WHERE workspace_id=$1 AND issue_id=$2 FOR UPDATE`, workspaceID, requestIssueID).Scan(&reporterID); err != nil {
		return err
	}
	for _, userID := range userIDs {
		if userID == reporterID {
			return fmt.Errorf("the reporter cannot be a request participant")
		}
		var customer bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_customers WHERE workspace_id=$1 AND user_id=$2 AND active)`, workspaceID, userID).Scan(&customer); err != nil {
			return err
		}
		if !customer {
			return fmt.Errorf("participant %q is not an active service customer", userID)
		}
		if remove {
			result, err := tx.Exec(ctx, `DELETE FROM service_request_participants WHERE request_issue_id=$1 AND user_id=$2`, requestIssueID, userID)
			if err != nil {
				return err
			}
			if result.RowsAffected() == 0 {
				return fmt.Errorf("participant %q is not on the request", userID)
			}
		} else if _, err := tx.Exec(ctx, `INSERT INTO service_request_participants(request_issue_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, requestIssueID, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ServiceRequests(ctx context.Context, workspaceID, viewerID, serviceDeskID, requestTypeID string, allowAll bool) ([]*models.ServiceRequest, error) {
	rows, err := s.Pool.Query(ctx, serviceRequestMetadataSelect+`
		WHERE sr.workspace_id=$1 AND ($5 OR sr.customer_id=$2)
		  AND ($3='' OR sr.service_desk_id=$3) AND ($4='' OR sr.request_type_id=$4)
		ORDER BY sr.created_at DESC,sr.issue_id`, workspaceID, viewerID, serviceDeskID, requestTypeID, allowAll)
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

// ServiceRequestsForAgent returns requests only from desks assigned to viewerID.
func (s *Store) ServiceRequestsForAgent(ctx context.Context, workspaceID, viewerID, serviceDeskID, requestTypeID string) ([]*models.ServiceRequest, error) {
	rows, err := s.Pool.Query(ctx, serviceRequestMetadataSelect+`
		WHERE sr.workspace_id=$1
		  AND EXISTS(SELECT 1 FROM service_desk_agents a WHERE a.service_desk_id=sr.service_desk_id AND a.user_id=$2)
		  AND ($3='' OR sr.service_desk_id=$3) AND ($4='' OR sr.request_type_id=$4)
		ORDER BY sr.created_at DESC,sr.issue_id`, workspaceID, viewerID, serviceDeskID, requestTypeID)
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

func (s *Store) MarkServiceRequestComment(ctx context.Context, requestIssueID, commentID string, public bool) error {
	result, err := s.Pool.Exec(ctx, `
		INSERT INTO service_request_comments(comment_id,request_issue_id,public)
		SELECT $2,$1,$3 WHERE EXISTS(SELECT 1 FROM comments WHERE id=$2 AND issue_id=$1)`, requestIssueID, commentID, public)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("comment does not belong to request")
	}
	return nil
}

func (s *Store) ServiceRequestComments(ctx context.Context, requestIssueID string, includeInternal bool) ([]models.ServiceRequestComment, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id,c.issue_id,c.author_id,COALESCE(u.display_name,''),c.body,
		       to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),src.public
		FROM comments c LEFT JOIN users u ON u.id=c.author_id
		JOIN service_request_comments src ON src.comment_id=c.id
		WHERE src.request_issue_id=$1 AND ($2 OR src.public)
		ORDER BY c.created_at,c.id`, requestIssueID, includeInternal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := make([]models.ServiceRequestComment, 0)
	for rows.Next() {
		var value models.ServiceRequestComment
		if err := rows.Scan(&value.Comment.ID, &value.Comment.IssueID, &value.Comment.AuthorID, &value.Comment.AuthorName, &value.Comment.Body, &value.Comment.Created, &value.Public); err != nil {
			return nil, err
		}
		comments = append(comments, value)
	}
	return comments, rows.Err()
}

func (s *Store) ServiceRequestComment(ctx context.Context, requestIssueID, commentID string, includeInternal bool) (*models.ServiceRequestComment, error) {
	value := &models.ServiceRequestComment{}
	err := s.Pool.QueryRow(ctx, `
		SELECT c.id,c.issue_id,c.author_id,COALESCE(u.display_name,''),c.body,
		       to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),src.public
		FROM comments c LEFT JOIN users u ON u.id=c.author_id
		JOIN service_request_comments src ON src.comment_id=c.id
		WHERE src.request_issue_id=$1 AND c.id=$2 AND ($3 OR src.public)`, requestIssueID, commentID, includeInternal).Scan(
		&value.Comment.ID, &value.Comment.IssueID, &value.Comment.AuthorID, &value.Comment.AuthorName, &value.Comment.Body, &value.Comment.Created, &value.Public)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func scanServiceQueue(row interface{ Scan(...any) error }) (*models.ServiceQueue, error) {
	queue := &models.ServiceQueue{}
	err := row.Scan(&queue.ID, &queue.ServiceDeskID, &queue.Name, &queue.JQL, &queue.Kind, &queue.Fields, &queue.Position)
	return queue, err
}

const serviceQueueSelect = `SELECT q.id,q.service_desk_id,q.name,q.jql,q.kind,q.fields,q.position FROM service_queues q `

func (s *Store) ServiceQueues(ctx context.Context, workspaceID, serviceDeskID string) ([]models.ServiceQueue, error) {
	rows, err := s.Pool.Query(ctx, serviceQueueSelect+`
		JOIN service_desks sd ON sd.id=q.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY q.position,q.id::bigint`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	queues := make([]models.ServiceQueue, 0)
	for rows.Next() {
		queue, err := scanServiceQueue(rows)
		if err != nil {
			return nil, err
		}
		queues = append(queues, *queue)
	}
	return queues, rows.Err()
}

func (s *Store) ServiceQueue(ctx context.Context, workspaceID, serviceDeskID, queueID string) (*models.ServiceQueue, error) {
	return scanServiceQueue(s.Pool.QueryRow(ctx, serviceQueueSelect+`
		JOIN service_desks sd ON sd.id=q.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND q.id=$3`, workspaceID, serviceDeskID, queueID))
}

func (s *Store) ServiceQueueRequests(ctx context.Context, workspaceID, viewerID, serviceDeskID, queueID string) (*models.ServiceQueue, []*models.ServiceRequest, error) {
	queue, err := s.ServiceQueue(ctx, workspaceID, serviceDeskID, queueID)
	if err != nil {
		return nil, nil, err
	}
	requests, err := s.ServiceRequests(ctx, workspaceID, viewerID, serviceDeskID, "", true)
	if err != nil {
		return nil, nil, err
	}
	filtered := make([]*models.ServiceRequest, 0)
	for _, request := range requests {
		if request.Issue.Status.Category == "done" {
			continue
		}
		switch queue.Kind {
		case "unassigned":
			if request.Issue.Assignee != nil {
				continue
			}
		case "assigned_to_me":
			if request.Issue.Assignee == nil || request.Issue.Assignee.ID != viewerID {
				continue
			}
		}
		filtered = append(filtered, request)
	}
	queue.IssueCount = len(filtered)
	return queue, filtered, nil
}

func (s *Store) IsServiceAgent(ctx context.Context, workspaceID, serviceDeskID, userID string) (bool, error) {
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil || admin {
		return admin, err
	}
	member, err := s.IsMember(ctx, workspaceID, userID)
	if err != nil || !member {
		return false, err
	}
	var allowed bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM service_desk_agents a JOIN service_desks sd ON sd.id=a.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND a.user_id=$3)`, workspaceID, serviceDeskID, userID).Scan(&allowed)
	return allowed, err
}

func (s *Store) IsAnyServiceAgent(ctx context.Context, workspaceID, userID string) (bool, error) {
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil || admin {
		return admin, err
	}
	member, err := s.IsMember(ctx, workspaceID, userID)
	if err != nil || !member {
		return false, err
	}
	var allowed bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM service_desk_agents a JOIN service_desks sd ON sd.id=a.service_desk_id
		WHERE sd.workspace_id=$1 AND a.user_id=$2)`, workspaceID, userID).Scan(&allowed)
	return allowed, err
}

func (s *Store) CanManageServiceRequest(ctx context.Context, workspaceID, userID, issueIDOrKey string) (bool, error) {
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil || admin {
		return admin, err
	}
	member, err := s.IsMember(ctx, workspaceID, userID)
	if err != nil || !member {
		return false, err
	}
	var allowed bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM service_requests sr JOIN issues i ON i.id=sr.issue_id
		JOIN service_desk_agents a ON a.service_desk_id=sr.service_desk_id
		WHERE sr.workspace_id=$1 AND (sr.issue_id=$3 OR upper(i.key)=upper($3)) AND a.user_id=$2)`, workspaceID, userID, issueIDOrKey).Scan(&allowed)
	return allowed, err
}

func (s *Store) ServiceDesksForAgent(ctx context.Context, workspaceID, userID string) ([]models.ServiceDesk, error) {
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	if admin {
		return s.ServiceDesks(ctx, workspaceID)
	}
	member, err := s.IsMember(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return []models.ServiceDesk{}, nil
	}
	rows, err := s.Pool.Query(ctx, serviceDeskSelect+`
		JOIN service_desk_agents a ON a.service_desk_id=sd.id
		WHERE sd.workspace_id=$1 AND a.user_id=$2 ORDER BY sd.id::bigint`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceDesk, 0)
	for rows.Next() {
		desk, err := scanServiceDesk(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, *desk)
	}
	return values, rows.Err()
}

func (s *Store) ServiceDeskAgents(ctx context.Context, workspaceID, serviceDeskID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.user_id FROM service_desk_agents a JOIN service_desks sd ON sd.id=a.service_desk_id WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY a.created_at,a.user_id`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	userIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	users := make([]*models.User, 0, len(userIDs))
	for _, id := range userIDs {
		user, err := s.UserByID(ctx, id)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (s *Store) SetServiceDeskAgent(ctx context.Context, workspaceID, serviceDeskID, userID string, enabled bool) error {
	if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return fmt.Errorf("agent must be an active workspace member")
	}
	if enabled {
		result, err := s.Pool.Exec(ctx, `INSERT INTO service_desk_agents(service_desk_id,user_id) SELECT sd.id,$3 FROM service_desks sd WHERE sd.workspace_id=$1 AND sd.id=$2 ON CONFLICT DO NOTHING`, workspaceID, serviceDeskID, userID)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			var exists bool
			if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_desks WHERE workspace_id=$1 AND id=$2)`, workspaceID, serviceDeskID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("service desk does not exist")
			}
		}
		return nil
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_desk_agents a USING service_desks sd WHERE a.service_desk_id=sd.id AND sd.workspace_id=$1 AND sd.id=$2 AND a.user_id=$3`, workspaceID, serviceDeskID, userID)
	return err
}
