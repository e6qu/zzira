package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/jackc/pgx/v5"
)

// A content item's history is the versions behind it: one can be restored as
// the latest, one can be removed, and a macro can be read as it was in any of
// them.

var ErrWikiHistoryValidation = errors.New("invalid content history request")

// RestoreWikiPageVersion makes a historical version the latest. Confluence
// creates a new version holding the historical content rather than moving the
// page back, so the history stays a record of everything that happened.
func (s *Store) RestoreWikiPageVersion(ctx context.Context, ws, actor, pageID string, versionNumber int, message string, restoreTitle bool) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := lockWritablePage(ctx, tx, ws, actor, pageID, "current")
	if err != nil {
		return 0, err
	}
	if versionNumber < 1 || versionNumber > page.Version {
		return 0, fmt.Errorf("%w: there is no version %d to restore", ErrWikiHistoryValidation, versionNumber)
	}
	var historicalTitle, historicalBody string
	if err = tx.QueryRow(ctx, `SELECT title, body FROM wiki_page_versions
		WHERE page_id::text=$1 AND version=$2`, pageID, versionNumber).Scan(&historicalTitle, &historicalBody); err != nil {
		return 0, err
	}
	title := page.Title
	if restoreTitle {
		title = historicalTitle
	}
	next := page.Version + 1
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET title=$2,body=$3,version=$4 WHERE id::text=$1`,
		pageID, title, historicalBody, next); err != nil {
		return 0, err
	}
	if message == "" {
		message = fmt.Sprintf("Restored version %d", versionNumber)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message)
		VALUES($1::bigint,$2,$3,$4,'current',$5,$6)`, pageID, next, title, historicalBody, actor, message); err != nil {
		return 0, err
	}
	return next, tx.Commit(ctx)
}

// DeleteWikiPageVersion removes a historical version. The content of that
// version is not undone: its changes are already carried by the versions after
// it, which is what Confluence means by rolling them up into the next version.
// The current version cannot be deleted, because there would be nothing to roll
// its changes into.
func (s *Store) DeleteWikiPageVersion(ctx context.Context, ws, actor, pageID string, versionNumber int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := lockWritablePage(ctx, tx, ws, actor, pageID, "current")
	if err != nil {
		return err
	}
	if versionNumber == page.Version {
		return fmt.Errorf("%w: the current version cannot be deleted", ErrWikiHistoryValidation)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_page_versions WHERE page_id::text=$1 AND version=$2`, pageID, versionNumber)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

// WikiMacro is a macro read out of a content body.
type WikiMacro struct {
	Name       string
	Body       string
	Parameters map[string]string
}

// WikiMacroFromVersion finds a macro in a historical version by the id the
// editor gave it when it was created.
func (s *Store) WikiMacroFromVersion(ctx context.Context, ws, actor, pageID string, versionNumber int, macroID string) (WikiMacro, error) {
	if _, err := s.WikiPage(ctx, ws, actor, pageID); err != nil {
		return WikiMacro{}, err
	}
	var body string
	if err := s.Pool.QueryRow(ctx, `SELECT v.body FROM wiki_page_versions v
		WHERE v.page_id::text=$1 AND v.version=$2`, pageID, versionNumber).Scan(&body); err != nil {
		return WikiMacro{}, err
	}
	macro, ok := findStorageMacro(body, macroID)
	if !ok {
		return WikiMacro{}, pgx.ErrNoRows
	}
	return macro, nil
}

// findStorageMacro reads a structured macro out of the storage format. The
// macro id is what the editor stamped on it, which is how a client addresses
// one across versions.
func findStorageMacro(body, macroID string) (WikiMacro, bool) {
	at := 0
	for {
		open := strings.Index(body[at:], "<ac:structured-macro")
		if open < 0 {
			return WikiMacro{}, false
		}
		open += at
		tagEnd := strings.IndexByte(body[open:], '>')
		if tagEnd < 0 {
			return WikiMacro{}, false
		}
		tagEnd += open
		tag := body[open : tagEnd+1]
		closing := strings.Index(body[tagEnd:], "</ac:structured-macro>")
		inner := ""
		next := len(body)
		if closing >= 0 {
			inner = body[tagEnd+1 : tagEnd+closing]
			next = tagEnd + closing + len("</ac:structured-macro>")
		}
		if storageAttribute(tag, "ac:macro-id") == macroID {
			return WikiMacro{
				Name:       storageAttribute(tag, "ac:name"),
				Body:       macroRichTextBody(inner),
				Parameters: macroParameters(inner),
			}, true
		}
		at = next
	}
}

func storageAttribute(tag, name string) string {
	marker := name + `="`
	at := strings.Index(tag, marker)
	if at < 0 {
		return ""
	}
	rest := tag[at+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func macroParameters(inner string) map[string]string {
	parameters := map[string]string{}
	at := 0
	for {
		open := strings.Index(inner[at:], "<ac:parameter")
		if open < 0 {
			return parameters
		}
		open += at
		tagEnd := strings.IndexByte(inner[open:], '>')
		if tagEnd < 0 {
			return parameters
		}
		tagEnd += open
		name := storageAttribute(inner[open:tagEnd+1], "ac:name")
		closing := strings.Index(inner[tagEnd:], "</ac:parameter>")
		if closing < 0 {
			return parameters
		}
		if name != "" {
			parameters[name] = inner[tagEnd+1 : tagEnd+closing]
		}
		at = tagEnd + closing + len("</ac:parameter>")
	}
}

func macroRichTextBody(inner string) string {
	for _, wrapper := range []string{"ac:rich-text-body", "ac:plain-text-body"} {
		open := strings.Index(inner, "<"+wrapper+">")
		if open < 0 {
			continue
		}
		start := open + len(wrapper) + 2
		closing := strings.Index(inner[start:], "</"+wrapper+">")
		if closing < 0 {
			continue
		}
		return inner[start : start+closing]
	}
	return ""
}

// Body conversion. Confluence converts between the formats it lists and keeps
// the result for five minutes, so a caller that asked for one has time to come
// back for it.

const bodyConversionLifetime = 5 * time.Minute

// convertibleTo says which conversions Confluence supports. Anything else is
// refused rather than answered with a body in the wrong format.
var convertibleTo = map[string]map[string]bool{
	"atlas_doc_format": {"editor": true, "export_view": true, "storage": true, "styled_view": true, "view": true},
	"storage":          {"atlas_doc_format": true, "editor": true, "export_view": true, "styled_view": true, "view": true},
	"editor":           {"storage": true},
}

// ConvertWikiBody converts one body. The formats this product stores are the
// storage HTML and the document format, and the view formats are that HTML
// rendered for reading.
func ConvertWikiBody(value, from, to string) (string, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" {
		from = "storage"
	}
	targets, ok := convertibleTo[from]
	if !ok || !targets[to] {
		return "", fmt.Errorf("%w: %s cannot be converted to %s", ErrWikiHistoryValidation, from, to)
	}
	source := value
	if from == "atlas_doc_format" {
		source = adf.ToHTML(json.RawMessage(value))
	}
	switch to {
	case "atlas_doc_format":
		return string(adf.FromHTML(source)), nil
	case "storage", "editor":
		return source, nil
	case "view", "export_view", "styled_view":
		// The reading formats are the stored markup with macros rendered as
		// the text they carry, which is what a reader sees.
		return renderStorageForReading(source), nil
	default:
		return "", fmt.Errorf("%w: %s is not a format this site produces", ErrWikiHistoryValidation, to)
	}
}

// renderStorageForReading turns the storage macros into the content they show.
func renderStorageForReading(source string) string {
	out := source
	for {
		open := strings.Index(out, "<ac:structured-macro")
		if open < 0 {
			return out
		}
		closing := strings.Index(out[open:], "</ac:structured-macro>")
		if closing < 0 {
			return out
		}
		end := open + closing + len("</ac:structured-macro>")
		inner := out[open:end]
		rendered := macroRichTextBody(inner)
		if rendered == "" {
			rendered = ""
		}
		out = out[:open] + rendered + out[end:]
	}
}

// WikiBodyConversion is one conversion task.
type WikiBodyConversion struct {
	ID             string
	Status         string
	Representation string
	Value          string
	Error          string
}

// StartWikiBodyConversion converts a body and keeps the result. Confluence
// answers an id immediately; the work here is quick enough to finish first, so
// the task is already complete when the caller comes back for it.
func (s *Store) StartWikiBodyConversion(ctx context.Context, ws, actor, value, from, to string) (string, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return "", err
	}
	id := NewID("conv")
	converted, convertErr := ConvertWikiBody(value, from, to)
	status, message := "COMPLETED", ""
	if convertErr != nil {
		status, message, converted = "FAILED", convertErr.Error(), ""
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO wiki_body_conversions(id,workspace_id,status,representation,value,error,completed_at)
		VALUES($1,$2,$3,$4,$5,$6,now())`, id, ws, status, to, converted, message); err != nil {
		return "", err
	}
	return id, nil
}

// WikiBodyConversionResult reads a conversion back. A result older than five
// minutes is gone, which is how long Confluence keeps one.
func (s *Store) WikiBodyConversionResult(ctx context.Context, ws, actor, id string) (WikiBodyConversion, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return WikiBodyConversion{}, err
	}
	var conversion WikiBodyConversion
	err := s.Pool.QueryRow(ctx, `SELECT id,status,representation,value,error FROM wiki_body_conversions
		WHERE workspace_id=$1 AND id=$2 AND completed_at > now() - $3::interval`,
		ws, id, fmt.Sprintf("%d seconds", int(bodyConversionLifetime.Seconds()))).
		Scan(&conversion.ID, &conversion.Status, &conversion.Representation, &conversion.Value, &conversion.Error)
	return conversion, err
}
