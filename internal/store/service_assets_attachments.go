package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// serviceAssetAttachmentLimit is the largest file kept on an object. An
// object's files are photographs, contracts and exports, not deliveries.
const serviceAssetAttachmentLimit = 32 << 20

// ServiceAssetObjectAttachments are the files kept with one object, oldest
// first.
func (s *Store) ServiceAssetObjectAttachments(ctx context.Context, ws, actor, objectID string) ([]models.ServiceAssetObjectAttachment, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, serviceAssetAttachmentSelect+`
		WHERE a.object_id::text=$1 ORDER BY a.created_at,a.id`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAssetAttachments(rows)
}

// ServiceAssetInventoryAttachments are the files on every object of one
// desk's inventory, by object id, so a page reads them in one query.
func (s *Store) ServiceAssetInventoryAttachments(ctx context.Context, ws, actor, deskID string) (map[string][]models.ServiceAssetObjectAttachment, error) {
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, serviceAssetAttachmentSelect+`
		JOIN service_asset_objects o ON o.id=a.object_id
		JOIN service_asset_schemas sc ON sc.id=o.schema_id
		JOIN service_desks sd ON sd.id=sc.service_desk_id
		WHERE sd.workspace_id=$1 AND sc.service_desk_id=$2 ORDER BY a.created_at,a.id`, ws, deskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files, err := collectAssetAttachments(rows)
	if err != nil {
		return nil, err
	}
	byObject := map[string][]models.ServiceAssetObjectAttachment{}
	for _, file := range files {
		byObject[file.ObjectID] = append(byObject[file.ObjectID], file)
	}
	return byObject, nil
}

const serviceAssetAttachmentSelect = `SELECT a.id::text,a.object_id::text,a.author_id,COALESCE(u.display_name,''),a.filename,a.media_type,a.size,a.blob_ref,
	to_char(a.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM service_asset_object_attachments a LEFT JOIN users u ON u.id=a.author_id `

func collectAssetAttachments(rows pgx.Rows) ([]models.ServiceAssetObjectAttachment, error) {
	files := []models.ServiceAssetObjectAttachment{}
	for rows.Next() {
		var file models.ServiceAssetObjectAttachment
		if err := rows.Scan(&file.ID, &file.ObjectID, &file.AuthorID, &file.AuthorName, &file.Filename,
			&file.MediaType, &file.Size, &file.BlobRef, &file.At); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

// ServiceAssetObjectAttachment is one file, which is how a download finds
// what to serve.
func (s *Store) ServiceAssetObjectAttachment(ctx context.Context, ws, actor, objectID, attachmentID string) (*models.ServiceAssetObjectAttachment, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	file := &models.ServiceAssetObjectAttachment{}
	err := s.Pool.QueryRow(ctx, serviceAssetAttachmentSelect+`WHERE a.id::text=$1 AND a.object_id::text=$2`, attachmentID, objectID).
		Scan(&file.ID, &file.ObjectID, &file.AuthorID, &file.AuthorName, &file.Filename, &file.MediaType, &file.Size, &file.BlobRef, &file.At)
	if err != nil {
		return nil, fmt.Errorf("that file is not on this object")
	}
	return file, nil
}

// SaveServiceAssetObjectAttachment records a file already written to the blob
// store. Agents of the desk keep files on its objects.
func (s *Store) SaveServiceAssetObjectAttachment(ctx context.Context, ws, actor, objectID, filename, mediaType string, size int64, blobRef string) (*models.ServiceAssetObjectAttachment, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	filename = strings.TrimSpace(filename)
	if filename == "" || len([]rune(filename)) > 255 {
		return nil, fmt.Errorf("a file needs a name of at most 255 characters")
	}
	if size < 0 || size > serviceAssetAttachmentLimit {
		return nil, fmt.Errorf("a file of at most 32 MB is required")
	}
	if strings.TrimSpace(mediaType) == "" {
		mediaType = "application/octet-stream"
	}
	file := &models.ServiceAssetObjectAttachment{
		ObjectID: objectID, AuthorID: actor, Filename: filename, MediaType: mediaType, Size: size, BlobRef: blobRef,
	}
	err := s.Pool.QueryRow(ctx, `INSERT INTO service_asset_object_attachments(object_id,author_id,filename,media_type,size,blob_ref)
		VALUES($1::uuid,$2,$3,$4,$5,$6)
		RETURNING id::text,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		objectID, actor, filename, mediaType, size, blobRef).Scan(&file.ID, &file.At)
	if err != nil {
		return nil, err
	}
	_ = s.Pool.QueryRow(ctx, `SELECT COALESCE(display_name,'') FROM users WHERE id=$1`, actor).Scan(&file.AuthorName)
	return file, nil
}

// DeleteServiceAssetObjectAttachment removes a file. Whoever put it there may,
// and so may a site administrator. It answers the blob to forget.
func (s *Store) DeleteServiceAssetObjectAttachment(ctx context.Context, ws, actor, objectID, attachmentID string) (string, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return "", err
	}
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return "", err
	}
	var blobRef string
	err = s.Pool.QueryRow(ctx, `DELETE FROM service_asset_object_attachments
		WHERE id::text=$1 AND object_id::text=$2 AND ($3 OR author_id=$4) RETURNING blob_ref`,
		attachmentID, objectID, admin, actor).Scan(&blobRef)
	if err != nil {
		return "", fmt.Errorf("that file is not one you can remove")
	}
	return blobRef, nil
}
