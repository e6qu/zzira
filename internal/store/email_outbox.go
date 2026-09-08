package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type EmailDelivery struct {
	ID        int64
	Recipient string
	Subject   string
	Body      string
	Attempt   int
}

func (s *Store) ClaimEmailDelivery(ctx context.Context) (*EmailDelivery, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	delivery := &EmailDelivery{}
	err = tx.QueryRow(ctx, `
		WITH due AS (
		  SELECT id FROM email_outbox
		  WHERE (state='pending' AND next_attempt_at<=now())
		     OR (state='delivering' AND locked_at<now()-interval '5 minutes')
		  ORDER BY next_attempt_at,id
		  FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE email_outbox e SET state='delivering',locked_at=now()
		FROM due WHERE e.id=due.id
		RETURNING e.id,e.recipient,e.subject,e.body,e.attempt_count`).
		Scan(&delivery.ID, &delivery.Recipient, &delivery.Subject, &delivery.Body, &delivery.Attempt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return delivery, nil
}

func (s *Store) CompleteEmailDelivery(ctx context.Context, id int64, sendErr error) error {
	if sendErr == nil {
		_, err := s.Pool.Exec(ctx, `UPDATE email_outbox SET state='sent',sent_at=now(),locked_at=NULL,last_error='' WHERE id=$1 AND state='delivering'`, id)
		return err
	}
	message := sendErr.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE email_outbox SET
		  state=CASE WHEN attempt_count+1>=8 THEN 'dead' ELSE 'pending' END,
		  attempt_count=attempt_count+1,
		  next_attempt_at=now()+make_interval(secs => LEAST(3600, power(2,attempt_count)::int*5)),
		  locked_at=NULL,last_error=$2
		WHERE id=$1 AND state='delivering'`, id, message)
	return err
}
