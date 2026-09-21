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
	// HTMLBody is the message's HTML alternative, or "" for plain text alone.
	HTMLBody string
	Attempt  int
	// Sender is the address this message comes from: the sender a project
	// chose, or "" for the site's own.
	Sender string
}

// queueProjectEmailSQL queues mail about a project's work with the sender
// address that project chose, provided the site has verified that address's
// domain -- which is the answer the project's email resource already gives
// its administrators. Anything else sends as the site's own sender.
const queueProjectEmailSQL = `INSERT INTO email_outbox(workspace_id,recipient,subject,body,html_body,dedupe_key,sender)
	VALUES($1,$2,$3,$4,$5,$6,COALESCE((
	  SELECT p.sender_email FROM projects p
	  WHERE p.id=$7 AND p.sender_email <> '' AND EXISTS (
	    SELECT 1 FROM organization_domains d JOIN sites si ON si.organization_id=d.organization_id
	    WHERE si.workspace_id=$1 AND lower(d.name)=lower(split_part(p.sender_email,'@',2)) AND d.claim_status='verified')
	),''))
	ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`

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
		RETURNING e.id,e.recipient,e.subject,e.body,e.html_body,e.attempt_count,e.sender`).
		Scan(&delivery.ID, &delivery.Recipient, &delivery.Subject, &delivery.Body, &delivery.HTMLBody, &delivery.Attempt, &delivery.Sender)
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

// QueueEmail adds a plain-text message to the delivery outbox.
func (s *Store) QueueEmail(ctx context.Context, workspaceID, recipient, subject, body string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body) VALUES($1,$2,$3,$4)`, workspaceID, recipient, subject, body)
	return err
}
