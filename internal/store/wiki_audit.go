package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Confluence's audit log records what administrators did to the site. It is
// separate from the organization audit log, which belongs to the organization
// across its products and carries a different record.

var ErrWikiAuditValidation = errors.New("invalid audit record")

// WikiAuditRecord is one entry of the log.
type WikiAuditRecord struct {
	AuthorID          string
	AuthorName        string
	RemoteAddress     string
	CreationDate      time.Time
	Summary           string
	Description       string
	Category          string
	SysAdmin          bool
	SuperAdmin        bool
	AffectedObject    json.RawMessage
	ChangedValues     json.RawMessage
	AssociatedObjects json.RawMessage
}

// WikiAuditRetention is how long a record is kept, from its creation date.
type WikiAuditRetention struct {
	Number int
	Units  string
}

// chronoUnits are the periods Confluence accepts, with how long each lasts.
// The ones shorter than a second are accepted and stored, because a caller may
// name them, but they retain nothing in practice.
const foreverDuration = time.Duration(1<<62 - 1)

var chronoUnits = map[string]time.Duration{
	"NANOS": time.Nanosecond, "MICROS": time.Microsecond, "MILLIS": time.Millisecond,
	"SECONDS": time.Second, "MINUTES": time.Minute, "HOURS": time.Hour,
	"HALF_DAYS": 12 * time.Hour, "DAYS": 24 * time.Hour, "WEEKS": 7 * 24 * time.Hour,
	"MONTHS": 30 * 24 * time.Hour, "YEARS": 365 * 24 * time.Hour,
	"DECADES": 3650 * 24 * time.Hour, "CENTURIES": 36500 * 24 * time.Hour,
	// A duration longer than this does not fit, and anything this long is past
	// the one year Confluence allows anyway.
	"MILLENNIA": foreverDuration, "ERAS": foreverDuration, "FOREVER": foreverDuration,
}

func retentionDuration(retention WikiAuditRetention) (time.Duration, error) {
	unit, ok := chronoUnits[strings.ToUpper(strings.TrimSpace(retention.Units))]
	if !ok {
		return 0, fmt.Errorf("%w: %q is not a period this log understands", ErrWikiAuditValidation, retention.Units)
	}
	if retention.Number <= 0 {
		return 0, fmt.Errorf("%w: the retention period must be a positive number", ErrWikiAuditValidation)
	}
	return time.Duration(retention.Number) * unit, nil
}

// WikiAuditRetentionPeriod reports how long records are kept.
func (s *Store) WikiAuditRetentionPeriod(ctx context.Context, ws, actor string) (WikiAuditRetention, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return WikiAuditRetention{}, err
	}
	retention := WikiAuditRetention{Number: 3, Units: "MONTHS"}
	err := s.Pool.QueryRow(ctx, `SELECT audit_retention_number, audit_retention_units
		FROM wiki_site_settings WHERE workspace_id=$1`, ws).Scan(&retention.Number, &retention.Units)
	if errors.Is(err, pgx.ErrNoRows) {
		return retention, nil
	}
	return retention, err
}

// SetWikiAuditRetentionPeriod changes it. Confluence caps the period at a year,
// and shortening it removes the records that now fall outside — a record kept
// beyond its retention would be a record the site said it had deleted.
func (s *Store) SetWikiAuditRetentionPeriod(ctx context.Context, ws, actor string, retention WikiAuditRetention) (WikiAuditRetention, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return WikiAuditRetention{}, err
	}
	period, err := retentionDuration(retention)
	if err != nil {
		return WikiAuditRetention{}, err
	}
	if period > 365*24*time.Hour {
		return WikiAuditRetention{}, fmt.Errorf("%w: the retention period may be at most one year", ErrWikiAuditValidation)
	}
	units := strings.ToUpper(strings.TrimSpace(retention.Units))
	if _, err = s.Pool.Exec(ctx, `INSERT INTO wiki_site_settings(workspace_id,audit_retention_number,audit_retention_units)
		VALUES($1,$2,$3) ON CONFLICT (workspace_id)
		DO UPDATE SET audit_retention_number=EXCLUDED.audit_retention_number,
		              audit_retention_units=EXCLUDED.audit_retention_units`, ws, retention.Number, units); err != nil {
		return WikiAuditRetention{}, err
	}
	if _, err = s.Pool.Exec(ctx, `DELETE FROM wiki_audit_records
		WHERE workspace_id=$1 AND created_at < now() - $2::interval`,
		ws, fmt.Sprintf("%d milliseconds", period.Milliseconds())); err != nil {
		return WikiAuditRetention{}, err
	}
	return WikiAuditRetention{Number: retention.Number, Units: units}, nil
}

// RecordWikiAudit writes an entry. Confluence lets an administrator add one, so
// a client integrating its own administration can keep the site's log complete.
func (s *Store) RecordWikiAudit(ctx context.Context, ws, actor string, record WikiAuditRecord) (WikiAuditRecord, error) {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return WikiAuditRecord{}, err
	}
	record.Summary = strings.TrimSpace(record.Summary)
	if record.Summary == "" || len(record.Summary) > 255 {
		return WikiAuditRecord{}, fmt.Errorf("%w: a summary of 1 to 255 characters is required", ErrWikiAuditValidation)
	}
	if record.CreationDate.IsZero() {
		record.CreationDate = time.Now().UTC()
	}
	if record.AuthorID == "" {
		record.AuthorID = actor
	}
	for _, value := range []*json.RawMessage{&record.ChangedValues, &record.AssociatedObjects} {
		if len(*value) == 0 {
			*value = json.RawMessage(`[]`)
		}
	}
	var affected any
	if len(record.AffectedObject) > 0 {
		affected = []byte(record.AffectedObject)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WikiAuditRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_audit_records
		(workspace_id,author_id,author_name,remote_address,created_at,summary,description,category,
		 sys_admin,super_admin,affected_object,changed_values,associated_objects)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id::text`,
		ws, nullableActor(record.AuthorID), record.AuthorName, record.RemoteAddress, record.CreationDate,
		record.Summary, record.Description, record.Category, record.SysAdmin, record.SuperAdmin,
		affected, []byte(record.ChangedValues), []byte(record.AssociatedObjects)).Scan(&id); err != nil {
		return WikiAuditRecord{}, err
	}
	// Read back inside the transaction: a row is only visible to the
	// transaction that wrote it until the commit.
	saved, err := scanWikiAuditRecord(tx.QueryRow(ctx, wikiAuditSelect+` WHERE r.id::text=$1`, id))
	if err != nil {
		return WikiAuditRecord{}, err
	}
	return saved, tx.Commit(ctx)
}

func nullableActor(id string) any {
	if id == "" {
		return nil
	}
	return id
}

const wikiAuditSelect = `SELECT COALESCE(r.author_id,''),
	COALESCE(NULLIF(r.author_name,''), COALESCE(u.display_name,'')),
	r.remote_address, r.created_at, r.summary, r.description, r.category,
	r.sys_admin, r.super_admin, COALESCE(r.affected_object,'null'::jsonb),
	r.changed_values, r.associated_objects
	FROM wiki_audit_records r LEFT JOIN users u ON u.id=r.author_id`

func scanWikiAuditRecord(row pgx.Row) (WikiAuditRecord, error) {
	var record WikiAuditRecord
	err := row.Scan(&record.AuthorID, &record.AuthorName, &record.RemoteAddress, &record.CreationDate,
		&record.Summary, &record.Description, &record.Category, &record.SysAdmin, &record.SuperAdmin,
		&record.AffectedObject, &record.ChangedValues, &record.AssociatedObjects)
	return record, err
}

// WikiAuditQuery narrows a read of the log.
type WikiAuditQuery struct {
	Start  time.Time
	End    time.Time
	Search string
}

// WikiAuditRecords reads the log. Records older than the retention period are
// not returned, because the site says it keeps them only that long.
func (s *Store) WikiAuditRecords(ctx context.Context, ws, actor string, query WikiAuditQuery) ([]WikiAuditRecord, error) {
	retention, err := s.WikiAuditRetentionPeriod(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	period, err := retentionDuration(retention)
	if err != nil {
		return nil, err
	}
	var start, end any
	if !query.Start.IsZero() {
		start = query.Start
	}
	if !query.End.IsZero() {
		end = query.End
	}
	rows, err := s.Pool.Query(ctx, wikiAuditSelect+`
		WHERE r.workspace_id=$1
		  AND r.created_at >= now() - $2::interval
		  AND ($3::timestamptz IS NULL OR r.created_at >= $3)
		  AND ($4::timestamptz IS NULL OR r.created_at <= $4)
		  AND ($5='' OR r.summary ILIKE '%' || $5 || '%' OR r.description ILIKE '%' || $5 || '%'
		       OR r.category ILIKE '%' || $5 || '%')
		ORDER BY r.created_at DESC, r.id DESC`,
		ws, fmt.Sprintf("%d milliseconds", period.Milliseconds()), start, end, query.Search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []WikiAuditRecord{}
	for rows.Next() {
		record, scanErr := scanWikiAuditRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// WikiAuditRecordsSince reads a period back from now, which is the same read
// with the start date worked out for the caller.
func (s *Store) WikiAuditRecordsSince(ctx context.Context, ws, actor string, number int, units, search string) ([]WikiAuditRecord, error) {
	if number <= 0 {
		number = 3
	}
	if strings.TrimSpace(units) == "" {
		units = "MONTHS"
	}
	period, err := retentionDuration(WikiAuditRetention{Number: number, Units: units})
	if err != nil {
		return nil, err
	}
	return s.WikiAuditRecords(ctx, ws, actor, WikiAuditQuery{Start: time.Now().UTC().Add(-period), Search: search})
}
