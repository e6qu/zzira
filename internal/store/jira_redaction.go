package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

const apiTaskRedactIssueContent = "jira-issue-redaction"

var (
	// ErrRedactionValidation is a redaction request Jira rejects as invalid.
	ErrRedactionValidation = errors.New("invalid redaction request")
	// ErrRedactionJobNotFound is a redaction job id that names no job.
	ErrRedactionJobNotFound = errors.New("redaction job not found")
)

// RedactionMarker replaces each redacted character. Keeping the length means
// other redactions in the same value still point where the client computed.
const RedactionMarker = "█"

// RedactionRequest is one span of text to redact from an issue field value, a
// comment or a worklog.
type RedactionRequest struct {
	ExternalID   string `json:"externalId"`
	Reason       string `json:"reason"`
	EntityType   string `json:"entityType"`
	EntityID     string `json:"entityId"`
	IssueRef     string `json:"issueId"`
	ADFPointer   string `json:"adfPointer,omitempty"`
	ExpectedText string `json:"expectedText"`
	From         int    `json:"from"`
	To           int    `json:"to"`
}

// RedactionResult reports whether one redaction was applied.
type RedactionResult struct {
	ExternalID string `json:"externalId"`
	Successful bool   `json:"successful"`
}

// EnqueueRedaction queues a batch of redactions as one job.
func (s *Store) EnqueueRedaction(ctx context.Context, workspaceID, actorID string, requests []RedactionRequest) (APITask, error) {
	task, err := queuedAPITask(workspaceID, actorID, "Redact issue content", apiTaskRedactIssueContent, requests)
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

// RedactionJob finds a redaction job by id.
func (s *Store) RedactionJob(ctx context.Context, workspaceID, jobID string) (APITask, error) {
	task, err := s.APITaskByID(ctx, workspaceID, jobID)
	if err != nil || task.Kind != apiTaskRedactIssueContent {
		return APITask{}, ErrRedactionJobNotFound
	}
	return task, nil
}

func (s *Store) executeRedactionTask(ctx context.Context, task APITask) error {
	var requests []RedactionRequest
	if err := json.Unmarshal(task.Payload, &requests); err != nil {
		return fmt.Errorf("decode redactions: %w", err)
	}
	results := make([]RedactionResult, 0, len(requests))
	for _, request := range requests {
		applied, err := s.applyRedaction(ctx, task.WorkspaceID, task.SubmittedBy, request)
		if err != nil {
			return err
		}
		results = append(results, RedactionResult{ExternalID: request.ExternalID, Successful: applied})
	}
	return s.CompleteAPITask(ctx, task, "Redaction completed.", map[string]any{"results": results})
}

// redactSpan replaces text[from:to], counted in characters, when its SHA-256
// digest, Base64 encoded, matches the expected text. It returns the new text
// and the text removed.
func redactSpan(text string, from, to int, expected string) (string, string, bool) {
	runes := []rune(text)
	if from < 0 || to <= from || to > len(runes) {
		return "", "", false
	}
	removed := string(runes[from:to])
	digest := sha256.Sum256([]byte(removed))
	if base64.StdEncoding.EncodeToString(digest[:]) != expected {
		return "", "", false
	}
	return string(runes[:from]) + strings.Repeat(RedactionMarker, to-from) + string(runes[to:]), removed, true
}

// redactADF redacts a span of the text node an RFC 6901 pointer names in an
// Atlassian Document Format document.
func redactADF(doc json.RawMessage, pointer string, from, to int, expected string) (json.RawMessage, string, bool) {
	if !strings.HasPrefix(pointer, "/") || len(doc) == 0 {
		return nil, "", false
	}
	var root any
	if json.Unmarshal(doc, &root) != nil {
		return nil, "", false
	}
	tokens := strings.Split(pointer[1:], "/")
	for i, token := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	var parent any = root
	for _, token := range tokens[:len(tokens)-1] {
		switch node := parent.(type) {
		case map[string]any:
			parent = node[token]
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil, "", false
			}
			parent = node[index]
		default:
			return nil, "", false
		}
	}
	last := tokens[len(tokens)-1]
	var removed string
	switch node := parent.(type) {
	case map[string]any:
		text, ok := node[last].(string)
		if !ok {
			return nil, "", false
		}
		var replaced string
		if replaced, removed, ok = redactSpan(text, from, to, expected); !ok {
			return nil, "", false
		}
		node[last] = replaced
	case []any:
		index, err := strconv.Atoi(last)
		if err != nil || index < 0 || index >= len(node) {
			return nil, "", false
		}
		text, ok := node[index].(string)
		if !ok {
			return nil, "", false
		}
		var replaced string
		if replaced, removed, ok = redactSpan(text, from, to, expected); !ok {
			return nil, "", false
		}
		node[index] = replaced
	default:
		return nil, "", false
	}
	encoded, err := marshalWithoutEscaping(root)
	if err != nil {
		return nil, "", false
	}
	return encoded, removed, true
}

func marshalWithoutEscaping(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// applyRedaction redacts one span, records it under its external id, and
// scrubs the removed text from the recorded history of what it was redacted from.
func (s *Store) applyRedaction(ctx context.Context, workspaceID, actorID string, request RedactionRequest) (bool, error) {
	var issueID string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM issues WHERE workspace_id=$1 AND (id=$2 OR jira_id::text=$2 OR upper(key)=upper($2))`, workspaceID, strings.TrimSpace(request.IssueRef)).Scan(&issueID); err != nil {
		return false, nil
	}
	var seen bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_redactions WHERE workspace_id=$1 AND external_id=$2)`, workspaceID, request.ExternalID).Scan(&seen); err != nil || seen {
		return false, err
	}
	var removed string
	var applied bool
	var err error
	historyEntity := issueID
	switch request.EntityType {
	case "issuefieldvalue":
		removed, applied, err = s.redactIssueField(ctx, workspaceID, actorID, issueID, request)
	case "issue-comment":
		var comment *models.Comment
		if comment, err = s.CommentByRef(ctx, workspaceID, request.EntityID); err != nil || comment.IssueID != issueID {
			return false, nil
		}
		historyEntity = comment.ID
		body, text, ok := redactADF(comment.Body, request.ADFPointer, request.From, request.To, request.ExpectedText)
		if !ok {
			return false, nil
		}
		removed, applied, err = text, true, s.replaceCommentBody(ctx, workspaceID, actorID, comment.ID, body)
	case "issue-worklog":
		var worklog *models.Worklog
		if worklog, err = s.WorklogByID(ctx, workspaceID, request.EntityID); err != nil || worklog.IssueID != issueID {
			return false, nil
		}
		historyEntity = worklog.ID
		body, text, ok := redactADF(worklog.Comment, request.ADFPointer, request.From, request.To, request.ExpectedText)
		if !ok {
			return false, nil
		}
		removed, applied, err = text, true, s.replaceWorklogComment(ctx, workspaceID, actorID, worklog.ID, body)
	default:
		return false, nil
	}
	if err != nil || !applied {
		return false, err
	}
	if err = s.scrubActionHistory(ctx, workspaceID, historyEntity, removed); err != nil {
		return false, err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO issue_redactions(workspace_id,external_id,issue_id,entity_type,entity_id,reason,redacted_by)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (workspace_id,external_id) DO NOTHING`,
		workspaceID, request.ExternalID, issueID, request.EntityType, request.EntityID, request.Reason, actorID)
	return err == nil, err
}

// redactIssueField redacts the summary, the description, the environment or a
// custom field value, plain text or rich text.
func (s *Store) redactIssueField(ctx context.Context, workspaceID, actorID, issueID string, request RedactionRequest) (string, bool, error) {
	var summary string
	var description, fieldsJSON []byte
	if err := s.Pool.QueryRow(ctx, `SELECT summary, description, COALESCE(fields,'{}'::jsonb) FROM issues WHERE workspace_id=$1 AND id=$2`, workspaceID, issueID).Scan(&summary, &description, &fieldsJSON); err != nil {
		return "", false, err
	}
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(fieldsJSON, &fields)
	redactValue := func(raw json.RawMessage) (json.RawMessage, string, bool) {
		var text string
		if request.ADFPointer == "" && json.Unmarshal(raw, &text) == nil {
			replaced, removed, ok := redactSpan(text, request.From, request.To, request.ExpectedText)
			if !ok {
				return nil, "", false
			}
			encoded, err := marshalWithoutEscaping(replaced)
			return encoded, removed, err == nil
		}
		return redactADF(raw, request.ADFPointer, request.From, request.To, request.ExpectedText)
	}
	var removed string
	var ok bool
	switch field := request.EntityID; {
	case field == "summary":
		if request.ADFPointer != "" {
			return "", false, nil
		}
		if summary, removed, ok = redactSpan(summary, request.From, request.To, request.ExpectedText); !ok {
			return "", false, nil
		}
	case field == "description":
		var redacted json.RawMessage
		if redacted, removed, ok = redactADF(description, request.ADFPointer, request.From, request.To, request.ExpectedText); !ok {
			return "", false, nil
		}
		description = redacted
	case field == "environment" || strings.HasPrefix(field, "customfield_"):
		raw, present := fields[field]
		if !present {
			return "", false, nil
		}
		var redacted json.RawMessage
		if redacted, removed, ok = redactValue(raw); !ok {
			return "", false, nil
		}
		fields[field] = redacted
	default:
		return "", false, nil
	}
	encodedFields, err := marshalWithoutEscaping(fields)
	if err != nil {
		return "", false, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE issues SET summary=$3, description=$4, fields=$5, updated_seq=$6 WHERE workspace_id=$1 AND id=$2`,
		workspaceID, issueID, summary, description, []byte(encodedFields), seq); err != nil {
		return "", false, err
	}
	issue, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
	if err != nil {
		return "", false, err
	}
	payload, err := json.Marshal(models.IssueUpdatePayload{Diff: map[string]models.ChangeItem{}, Issue: *issue})
	if err != nil {
		return "", false, err
	}
	if err = appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return "", false, err
	}
	return removed, true, tx.Commit(ctx)
}

// replaceCommentBody stores a redacted comment body without marking it edited.
func (s *Store) replaceCommentBody(ctx context.Context, workspaceID, actorID, commentID string, body json.RawMessage) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE comments SET body=$3, updated_seq=$4 WHERE workspace_id=$1 AND id=$2`, workspaceID, commentID, []byte(body), seq); err != nil {
		return err
	}
	comment, err := scanComment(tx.QueryRow(ctx, commentJoin+`WHERE c.id=$1`, commentID))
	if err != nil {
		return err
	}
	payload, err := json.Marshal(models.CommentUpsertPayload{Comment: *comment})
	if err != nil {
		return err
	}
	if err = appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityComment, EntityID: commentID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// replaceWorklogComment stores a redacted worklog comment without marking it edited.
func (s *Store) replaceWorklogComment(ctx context.Context, workspaceID, actorID, worklogID string, comment json.RawMessage) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE worklogs SET comment=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, worklogID, []byte(comment)); err != nil {
		return err
	}
	worklog, err := scanWorklog(tx.QueryRow(ctx, worklogJoin+`WHERE w.id=$1`, worklogID))
	if err != nil {
		return err
	}
	payload, err := json.Marshal(models.WorklogUpsertPayload{Worklog: *worklog})
	if err != nil {
		return err
	}
	if err = appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWorklog, EntityID: worklogID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// scrubActionHistory replaces redacted text in the string values of an
// entity's recorded actions, so the changelog no longer holds it.
func (s *Store) scrubActionHistory(ctx context.Context, workspaceID, entityID, removed string) error {
	if removed == "" {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT seq, payload FROM actions WHERE workspace_id=$1 AND entity_id=$2 AND strpos(payload::text, $3) > 0`, workspaceID, entityID, removed)
	if err != nil {
		return err
	}
	type update struct {
		seq     int64
		payload json.RawMessage
	}
	updates := []update{}
	marker := strings.Repeat(RedactionMarker, len([]rune(removed)))
	for rows.Next() {
		var seq int64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			rows.Close()
			return err
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		encoded, marshalErr := marshalWithoutEscaping(replaceInStrings(value, removed, marker))
		if marshalErr != nil {
			rows.Close()
			return marshalErr
		}
		updates = append(updates, update{seq, encoded})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err = s.Pool.Exec(ctx, `UPDATE actions SET payload=$3 WHERE workspace_id=$1 AND seq=$2`, workspaceID, u.seq, []byte(u.payload)); err != nil {
			return err
		}
	}
	return nil
}

// replaceInStrings replaces text only inside string values, never in keys or structure.
func replaceInStrings(value any, old, replacement string) any {
	switch node := value.(type) {
	case string:
		return strings.ReplaceAll(node, old, replacement)
	case []any:
		for i := range node {
			node[i] = replaceInStrings(node[i], old, replacement)
		}
		return node
	case map[string]any:
		for key, child := range node {
			node[key] = replaceInStrings(child, old, replacement)
		}
		return node
	}
	return value
}
