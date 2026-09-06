package store

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceRequestSubscription(ctx context.Context, requestIssueID, userID string) (bool, error) {
	var subscribed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_request_subscriptions WHERE request_issue_id=$1 AND user_id=$2)`, requestIssueID, userID).Scan(&subscribed)
	return subscribed, err
}

func (s *Store) SetServiceRequestSubscription(ctx context.Context, requestIssueID, userID string, subscribed bool) error {
	if subscribed {
		_, err := s.Pool.Exec(ctx, `INSERT INTO service_request_subscriptions(request_issue_id,user_id)
			SELECT sr.issue_id,$2 FROM service_requests sr WHERE sr.issue_id=$1 ON CONFLICT DO NOTHING`, requestIssueID, userID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_request_subscriptions WHERE request_issue_id=$1 AND user_id=$2`, requestIssueID, userID)
	return err
}

func (s *Store) ServiceRequestSubscriberIDs(ctx context.Context, requestIssueID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.user_id FROM service_request_subscriptions s JOIN users u ON u.id=s.user_id WHERE s.request_issue_id=$1 AND u.active ORDER BY s.subscribed_at,s.user_id`, requestIssueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		values = append(values, id)
	}
	return values, rows.Err()
}

func scanServiceRequestFeedback(row interface{ Scan(...any) error }) (*models.ServiceRequestFeedback, error) {
	value := &models.ServiceRequestFeedback{}
	if err := row.Scan(&value.RequestIssueID, &value.ReporterID, &value.Type, &value.Rating, &value.Comment, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return nil, err
	}
	return value, nil
}

func (s *Store) ServiceRequestFeedback(ctx context.Context, requestIssueID string) (*models.ServiceRequestFeedback, error) {
	return scanServiceRequestFeedback(s.Pool.QueryRow(ctx, `SELECT request_issue_id,reporter_id,type,rating,comment,created_at,updated_at FROM service_request_feedback WHERE request_issue_id=$1`, requestIssueID))
}

func (s *Store) PutServiceRequestFeedback(ctx context.Context, workspaceID, requestIssueID, reporterID, feedbackType string, rating int, comment string) (*models.ServiceRequestFeedback, error) {
	value := &models.ServiceRequestFeedback{}
	err := s.Pool.QueryRow(ctx, `INSERT INTO service_request_feedback(request_issue_id,reporter_id,type,rating,comment)
		SELECT sr.issue_id,$3,$4,$5,$6 FROM service_requests sr JOIN issues i ON i.id=sr.issue_id JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sr.issue_id=$2 AND sr.customer_id=$3 AND st.category='done'
		ON CONFLICT(request_issue_id) DO UPDATE SET type=EXCLUDED.type,rating=EXCLUDED.rating,comment=EXCLUDED.comment,updated_at=now()
		RETURNING request_issue_id,reporter_id,type,rating,comment,created_at,updated_at`, workspaceID, requestIssueID, reporterID, feedbackType, rating, comment).Scan(
		&value.RequestIssueID, &value.ReporterID, &value.Type, &value.Rating, &value.Comment, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("feedback requires a completed request reported by this user: %w", err)
	}
	return value, nil
}

func (s *Store) DeleteServiceRequestFeedback(ctx context.Context, requestIssueID, reporterID string) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM service_request_feedback WHERE request_issue_id=$1 AND reporter_id=$2`, requestIssueID, reporterID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("feedback does not exist")
	}
	return nil
}
