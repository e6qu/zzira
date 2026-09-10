package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrFilterValidation = errors.New("invalid filter")
	ErrFilterPermission = errors.New("you do not have permission to change this filter")
)

type FilterDetails struct {
	Name             string
	JQL              string
	Description      string
	Favourite        bool
	SharePermissions []FilterPermissionInput
	SharesProvided   bool
}

type FilterPermissionInput struct {
	Type          string
	Rights        int
	AccountID     string
	GroupID       string
	GroupName     string
	ProjectID     string
	ProjectRoleID string
}

type FilterSearch struct {
	Name           string
	OwnerID        string
	GroupID        string
	GroupName      string
	ProjectID      string
	IDs            map[string]bool
	Substring      bool
	OnlyOwned      bool
	OnlyFavourites bool
	Override       bool
	OrderBy        string
}

const filterAccess = `(
	f.owner_id=$2 OR EXISTS (
		SELECT 1 FROM filter_share_permissions fp
		WHERE fp.filter_id=f.id AND (
			fp.permission_type IN ('global','authenticated')
			OR (fp.permission_type='user' AND fp.account_id=$2)
			OR (fp.permission_type='group' AND EXISTS (
				SELECT 1 FROM group_members gm
				JOIN groups g ON g.id=gm.group_id
				JOIN directories d ON d.id=g.directory_id
				JOIN sites si ON si.organization_id=d.organization_id
				WHERE gm.group_id=fp.group_id AND gm.user_id=$2
				  AND si.workspace_id=f.workspace_id AND d.active))
			OR (fp.permission_type='project' AND EXISTS (
				SELECT 1 FROM projects p WHERE p.id=fp.project_id
				  AND p.workspace_id=f.workspace_id))
			OR (fp.permission_type='projectRole' AND EXISTS (
				SELECT 1 FROM projects p
				WHERE p.id=fp.project_id AND p.workspace_id=f.workspace_id
				  AND EXISTS (
					SELECT 1 FROM role_bindings rb
					WHERE rb.scope_type='project' AND rb.scope_id=p.id
					  AND rb.role_key=fp.project_role_id
					  AND (
						rb.principal_type='user' AND rb.principal_id=$2
						OR (rb.principal_type='group' AND EXISTS (
							SELECT 1 FROM group_members gm
							JOIN groups g ON g.id=gm.group_id
							JOIN directories d ON d.id=g.directory_id
							WHERE gm.group_id::TEXT=rb.principal_id AND gm.user_id=$2 AND d.active))
					  )
				  )
			))
		)
	)
)`

var filterWritable = `COALESCE(` + strings.Replace(filterAccess,
	"WHERE fp.filter_id=f.id AND (", "WHERE fp.filter_id=f.id AND fp.rights=2 AND (", 1) + `,FALSE)`

var filterSelect = `
SELECT f.id,f.name,COALESCE(f.jql,''),COALESCE(f.description,''),
       COALESCE(f.owner_id,''),COALESCE(u.display_name,''),
       EXISTS(SELECT 1 FROM filter_favourites ff WHERE ff.filter_id=f.id AND ff.user_id=$2),
       (SELECT count(*) FROM filter_favourites ff WHERE ff.filter_id=f.id),
       COALESCE(to_char(f.approximate_last_used AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
       COALESCE(f.columns,'{}'::TEXT[]),` + filterWritable + `
FROM filters f LEFT JOIN users u ON u.id=f.owner_id
WHERE f.workspace_id=$1`

func scanFilter(row pgx.Row) (*models.Filter, error) {
	f := &models.Filter{}
	err := row.Scan(&f.ID, &f.Name, &f.JQL, &f.Description, &f.OwnerID, &f.OwnerName,
		&f.Favourite, &f.FavouritedCount, &f.ApproximateLastUsed, &f.Columns, &f.Writable)
	return f, err
}

func (s *Store) loadFilterPermissions(ctx context.Context, filters []*models.Filter) error {
	if len(filters) == 0 {
		return nil
	}
	ids := make([]string, 0, len(filters))
	byID := make(map[string]*models.Filter, len(filters))
	for _, filter := range filters {
		ids = append(ids, filter.ID)
		byID[filter.ID] = filter
		filter.SharePermissions = []models.FilterSharePermission{}
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT fp.filter_id,fp.id,fp.permission_type,fp.rights,
		       COALESCE(fp.account_id,''),COALESCE(u.display_name,''),
		       COALESCE(fp.group_id::TEXT,''),COALESCE(g.name,''),
		       COALESCE(fp.project_id,''),COALESCE(p.key,''),COALESCE(p.name,''),
		       COALESCE(fp.project_role_id,''),
		       COALESCE(pr.name,fp.project_role_id,'')
		FROM filter_share_permissions fp
		LEFT JOIN users u ON u.id=fp.account_id
		LEFT JOIN groups g ON g.id=fp.group_id
		LEFT JOIN projects p ON p.id=fp.project_id
		LEFT JOIN project_roles pr ON pr.workspace_id=p.workspace_id AND pr.id::text=fp.project_role_id
		WHERE fp.filter_id=ANY($1::TEXT[]) ORDER BY fp.filter_id,fp.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var filterID string
		permission := models.FilterSharePermission{}
		if err := rows.Scan(&filterID, &permission.ID, &permission.Type, &permission.Rights,
			&permission.AccountID, &permission.AccountName, &permission.GroupID, &permission.GroupName,
			&permission.ProjectID, &permission.ProjectKey, &permission.ProjectName,
			&permission.ProjectRoleID, &permission.ProjectRole); err != nil {
			return err
		}
		if filter := byID[filterID]; filter != nil {
			filter.SharePermissions = append(filter.SharePermissions, permission)
		}
	}
	return rows.Err()
}

func (s *Store) FilterByID(ctx context.Context, workspaceID, userID, id string) (*models.Filter, error) {
	filter, err := scanFilter(s.Pool.QueryRow(ctx, filterSelect+` AND f.id=$3 AND `+filterAccess, workspaceID, userID, id))
	if err != nil {
		return nil, err
	}
	if err = s.loadFilterPermissions(ctx, []*models.Filter{filter}); err != nil {
		return nil, err
	}
	if err = s.loadFilterSubscriptions(ctx, []*models.Filter{filter}, userID); err != nil {
		return nil, err
	}
	return filter, nil
}

func (s *Store) Filters(ctx context.Context, workspaceID, userID string, search FilterSearch) ([]*models.Filter, error) {
	access := filterAccess
	if search.Override {
		access = `TRUE`
	}
	rows, err := s.Pool.Query(ctx, filterSelect+` AND `+access, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	filters := []*models.Filter{}
	for rows.Next() {
		filter, err := scanFilter(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		filters = append(filters, filter)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = s.loadFilterPermissions(ctx, filters); err != nil {
		return nil, err
	}
	if err = s.loadFilterSubscriptions(ctx, filters, userID); err != nil {
		return nil, err
	}
	filtered := filters[:0]
	for _, filter := range filters {
		if search.OnlyOwned && filter.OwnerID != userID || search.OnlyFavourites && !filter.Favourite {
			continue
		}
		if search.OwnerID != "" && filter.OwnerID != search.OwnerID {
			continue
		}
		if len(search.IDs) > 0 && !search.IDs[filter.ID] {
			continue
		}
		if search.Name != "" {
			name, query := strings.ToLower(filter.Name), strings.ToLower(search.Name)
			if search.Substring && !strings.Contains(name, query) || !search.Substring && name != query {
				continue
			}
		}
		if (search.GroupID != "" || search.GroupName != "") && !filterHasGroup(filter, search.GroupID, search.GroupName) {
			continue
		}
		if search.ProjectID != "" && !filterHasProject(filter, search.ProjectID) {
			continue
		}
		filtered = append(filtered, filter)
	}
	sortFilters(filtered, search.OrderBy)
	return filtered, nil
}

func (s *Store) ListFilters(ctx context.Context, workspaceID, userID string) ([]*models.Filter, error) {
	return s.Filters(ctx, workspaceID, userID, FilterSearch{OrderBy: "favourite"})
}

// GroupsForWorkspace returns active directory groups that can be selected as a
// saved-filter share target. Membership expansion remains in the access query.
func (s *Store) GroupsForWorkspace(ctx context.Context, workspaceID string) ([]*models.Group, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id::TEXT,g.directory_id::TEXT,g.name,g.description,
		       to_char(g.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(g.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       (SELECT count(*) FROM group_members gm WHERE gm.group_id=g.id)
		FROM groups g
		JOIN directories d ON d.id=g.directory_id AND d.active
		WHERE EXISTS (
			SELECT 1 FROM sites s
			WHERE s.organization_id=d.organization_id AND s.workspace_id=$1)
		ORDER BY g.name,g.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []*models.Group{}
	for rows.Next() {
		group := &models.Group{}
		if err := rows.Scan(&group.ID, &group.DirectoryID, &group.Name, &group.Description,
			&group.CreatedAt, &group.UpdatedAt, &group.MemberCount); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func filterHasGroup(filter *models.Filter, id, name string) bool {
	for _, permission := range filter.SharePermissions {
		if permission.Type == "group" && (id != "" && permission.GroupID == id ||
			name != "" && strings.EqualFold(permission.GroupName, name)) {
			return true
		}
	}
	return false
}

func filterHasProject(filter *models.Filter, id string) bool {
	for _, permission := range filter.SharePermissions {
		if (permission.Type == "project" || permission.Type == "projectRole") &&
			(permission.ProjectID == id || permission.ProjectKey == id) {
			return true
		}
	}
	return false
}

func sortFilters(filters []*models.Filter, orderBy string) {
	desc := strings.HasPrefix(orderBy, "-")
	field := strings.TrimPrefix(orderBy, "-")
	if field == "" {
		field = "name"
	}
	sort.SliceStable(filters, func(i, j int) bool {
		left, right := filters[i], filters[j]
		comparison := 0
		switch field {
		case "id":
			comparison = strings.Compare(left.ID, right.ID)
		case "owner":
			comparison = strings.Compare(strings.ToLower(left.OwnerName), strings.ToLower(right.OwnerName))
		case "favouriteCount":
			if left.FavouritedCount < right.FavouritedCount {
				comparison = -1
			} else if left.FavouritedCount > right.FavouritedCount {
				comparison = 1
			}
		case "favourite":
			if left.Favourite != right.Favourite {
				if left.Favourite {
					comparison = -1
				} else {
					comparison = 1
				}
			}
		default:
			comparison = strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
		}
		if comparison == 0 {
			comparison = strings.Compare(left.ID, right.ID)
		}
		if desc {
			return comparison > 0
		}
		return comparison < 0
	})
}

func validateFilterDetails(details *FilterDetails) error {
	details.Name = strings.TrimSpace(details.Name)
	details.JQL = strings.TrimSpace(details.JQL)
	if details.Name == "" || utf8.RuneCountInString(details.Name) > 255 {
		return fmt.Errorf("%w: name must contain 1–255 characters", ErrFilterValidation)
	}
	if utf8.RuneCountInString(details.Description) > 16384 || utf8.RuneCountInString(details.JQL) > 20000 {
		return fmt.Errorf("%w: description or JQL is too long", ErrFilterValidation)
	}
	if len(details.SharePermissions) > 100 {
		return fmt.Errorf("%w: at most 100 share permissions are supported", ErrFilterValidation)
	}
	if _, err := jql.Parse(details.JQL); err != nil {
		return fmt.Errorf("%w: %v", ErrFilterValidation, err)
	}
	return nil
}

func filterDatabaseError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return fmt.Errorf("%w: filter names and share permissions must be unique", ErrFilterValidation)
	}
	return err
}

func filterAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, filterID string, detail string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT s.organization_id,$2,$3,'filter',$4,jsonb_build_object('detail',$5::TEXT)
		FROM sites s WHERE s.workspace_id=$1`, workspaceID, actorID, action, filterID, detail)
	return err
}

func (s *Store) CreateManagedFilter(ctx context.Context, id, workspaceID, ownerID string, details FilterDetails) (*models.Filter, error) {
	if err := validateFilterDetails(&details); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		INSERT INTO filters(id,workspace_id,name,jql,description,owner_id,favourite)
		SELECT $1,$2,$3,$4,$5,$6,FALSE
		WHERE EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$2 AND user_id=$6)`,
		id, workspaceID, details.Name, details.JQL, details.Description, ownerID)
	if err != nil {
		return nil, filterDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrFilterPermission
	}
	if details.Favourite {
		if _, err = tx.Exec(ctx, `INSERT INTO filter_favourites(filter_id,user_id) VALUES($1,$2)`, id, ownerID); err != nil {
			return nil, err
		}
	}
	permissions := details.SharePermissions
	if !details.SharesProvided {
		var scope string
		err = tx.QueryRow(ctx, `SELECT scope FROM filter_default_share_scopes WHERE workspace_id=$1 AND user_id=$2`, workspaceID, ownerID).Scan(&scope)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if scope == "AUTHENTICATED" {
			permissions = []FilterPermissionInput{{Type: "authenticated", Rights: 1}}
		}
	}
	for _, permission := range permissions {
		if _, err = addFilterPermission(ctx, tx, workspaceID, id, permission); err != nil {
			return nil, err
		}
	}
	if err = filterAudit(ctx, tx, workspaceID, ownerID, "filter.created", id, details.Name); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.FilterByID(ctx, workspaceID, ownerID, id)
}

func (s *Store) CreateFilter(ctx context.Context, id, workspaceID, name, query, description, ownerID string) (*models.Filter, error) {
	return s.CreateManagedFilter(ctx, id, workspaceID, ownerID, FilterDetails{Name: name, JQL: query, Description: description, SharesProvided: true})
}

func (s *Store) CreateFavouriteFilter(ctx context.Context, id, workspaceID, name, query, description, ownerID string) (*models.Filter, error) {
	return s.CreateManagedFilter(ctx, id, workspaceID, ownerID, FilterDetails{Name: name, JQL: query, Description: description, Favourite: true, SharesProvided: true})
}

func (s *Store) UpdateManagedFilter(ctx context.Context, workspaceID, userID, id string, details FilterDetails) (*models.Filter, error) {
	if err := validateFilterDetails(&details); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owner string
	var writable bool
	if err = tx.QueryRow(ctx, `SELECT owner_id,`+filterWritable+` FROM filters f WHERE f.workspace_id=$1 AND f.id=$3 FOR UPDATE`,
		workspaceID, userID, id).Scan(&owner, &writable); err != nil {
		return nil, err
	}
	if !writable {
		return nil, ErrFilterPermission
	}
	if owner != userID && details.SharesProvided {
		return nil, ErrFilterPermission
	}
	if _, err = tx.Exec(ctx, `UPDATE filters SET name=$2,jql=$3,description=$4 WHERE id=$1`, id, details.Name, details.JQL, details.Description); err != nil {
		return nil, filterDatabaseError(err)
	}
	if details.SharesProvided {
		if _, err = tx.Exec(ctx, `DELETE FROM filter_share_permissions WHERE filter_id=$1`, id); err != nil {
			return nil, err
		}
		for _, permission := range details.SharePermissions {
			if _, err = addFilterPermission(ctx, tx, workspaceID, id, permission); err != nil {
				return nil, err
			}
		}
	}
	if err = filterAudit(ctx, tx, workspaceID, userID, "filter.updated", id, details.Name); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.FilterByID(ctx, workspaceID, userID, id)
}

func (s *Store) UpdateFilter(ctx context.Context, workspaceID, userID, id, name, query, description string) (*models.Filter, error) {
	return s.UpdateManagedFilter(ctx, workspaceID, userID, id, FilterDetails{Name: name, JQL: query, Description: description})
}

func (s *Store) DeleteFilter(ctx context.Context, workspaceID, userID, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name string
	if err = tx.QueryRow(ctx, `SELECT name FROM filters WHERE id=$1 AND workspace_id=$2 AND owner_id=$3 FOR UPDATE`, id, workspaceID, userID).Scan(&name); err != nil {
		return err
	}
	if err = filterAudit(ctx, tx, workspaceID, userID, "filter.deleted", id, name); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM filters WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetFilterFavourite(ctx context.Context, workspaceID, userID, id string, favourite bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var visible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM filters f WHERE f.workspace_id=$1 AND f.id=$3 AND `+filterAccess+`)`, workspaceID, userID, id).Scan(&visible)
	if err != nil {
		return err
	}
	if !visible {
		return pgx.ErrNoRows
	}
	if favourite {
		_, err = tx.Exec(ctx, `INSERT INTO filter_favourites(filter_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, userID)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM filter_favourites WHERE filter_id=$1 AND user_id=$2`, id, userID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func addFilterPermission(ctx context.Context, tx pgx.Tx, workspaceID, filterID string, input FilterPermissionInput) (*models.FilterSharePermission, error) {
	input.Type = strings.TrimSpace(input.Type)
	if input.Type == "loggedin" {
		input.Type = "authenticated"
	}
	if input.Rights == 0 {
		input.Rights = 1
	}
	if input.Rights != 1 && input.Rights != 2 {
		return nil, fmt.Errorf("%w: rights must be 1 or 2", ErrFilterValidation)
	}
	var accountID, groupID, projectID, roleID any
	switch input.Type {
	case "global", "authenticated":
		var other bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM filter_share_permissions WHERE filter_id=$1 AND permission_type NOT IN ('global','authenticated'))`, filterID).Scan(&other); err != nil {
			return nil, err
		}
		if other {
			return nil, fmt.Errorf("%w: global or authenticated sharing cannot be combined with targeted shares", ErrFilterValidation)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM filter_share_permissions WHERE filter_id=$1`, filterID); err != nil {
			return nil, err
		}
	case "user":
		if input.AccountID == "" {
			return nil, fmt.Errorf("%w: accountId is required", ErrFilterValidation)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2)`, workspaceID, input.AccountID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: user is not a workspace member", ErrFilterValidation)
		}
		accountID = input.AccountID
	case "group":
		if input.GroupID == "" && input.GroupName == "" || input.GroupID != "" && input.GroupName != "" {
			return nil, fmt.Errorf("%w: exactly one groupId or groupname is required", ErrFilterValidation)
		}
		query, value := `g.id::TEXT=$2`, input.GroupID
		if input.GroupName != "" {
			query, value = `lower(g.name)=lower($2)`, input.GroupName
		}
		var id string
		err := tx.QueryRow(ctx, `SELECT g.id::TEXT FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites s ON s.organization_id=d.organization_id WHERE s.workspace_id=$1 AND `+query, workspaceID, value).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("%w: group was not found", ErrFilterValidation)
		}
		groupID = id
	case "project", "projectRole":
		if input.ProjectID == "" {
			return nil, fmt.Errorf("%w: projectId is required", ErrFilterValidation)
		}
		var id string
		if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND (id=$2 OR key=$2)`, workspaceID, input.ProjectID).Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: project was not found", ErrFilterValidation)
		}
		projectID = id
		if input.Type == "projectRole" {
			if input.ProjectRoleID == "" {
				return nil, fmt.Errorf("%w: projectRoleId is required", ErrFilterValidation)
			}
			role, parseErr := strconv.ParseInt(input.ProjectRoleID, 10, 64)
			if parseErr != nil || role < 1 {
				return nil, fmt.Errorf("%w: projectRoleId must be a positive integer", ErrFilterValidation)
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM project_roles WHERE workspace_id=$1 AND id=$2)`,
				workspaceID, role).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("%w: project role was not found", ErrFilterValidation)
			}
			roleID = input.ProjectRoleID
		}
	default:
		return nil, fmt.Errorf("%w: unsupported share type", ErrFilterValidation)
	}
	if input.Type != "global" && input.Type != "authenticated" {
		var broad bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM filter_share_permissions WHERE filter_id=$1 AND permission_type IN ('global','authenticated'))`, filterID).Scan(&broad); err != nil {
			return nil, err
		}
		if broad {
			return nil, fmt.Errorf("%w: remove global or authenticated sharing first", ErrFilterValidation)
		}
	}
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO filter_share_permissions(filter_id,permission_type,account_id,group_id,project_id,project_role_id,rights)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, filterID, input.Type, accountID, groupID, projectID, roleID, input.Rights).Scan(&id)
	if err != nil {
		return nil, filterDatabaseError(err)
	}
	return &models.FilterSharePermission{ID: id, Type: input.Type, Rights: input.Rights}, nil
}

func (s *Store) AddFilterPermission(ctx context.Context, workspaceID, userID, id string, input FilterPermissionInput) (*models.FilterSharePermission, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM filters WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, id).Scan(&owner); err != nil {
		return nil, err
	}
	if owner != userID {
		return nil, ErrFilterPermission
	}
	permission, err := addFilterPermission(ctx, tx, workspaceID, id, input)
	if err != nil {
		return nil, err
	}
	if err = filterAudit(ctx, tx, workspaceID, userID, "filter.permission-added", id, permission.Type); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	filter, err := s.FilterByID(ctx, workspaceID, userID, id)
	if err != nil {
		return nil, err
	}
	for i := range filter.SharePermissions {
		if filter.SharePermissions[i].ID == permission.ID {
			return &filter.SharePermissions[i], nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (s *Store) DeleteFilterPermission(ctx context.Context, workspaceID, userID, id string, permissionID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		DELETE FROM filter_share_permissions fp USING filters f
		WHERE fp.filter_id=f.id AND f.workspace_id=$1 AND f.id=$2 AND f.owner_id=$3 AND fp.id=$4`,
		workspaceID, id, userID, permissionID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err = filterAudit(ctx, tx, workspaceID, userID, "filter.permission-deleted", id, fmt.Sprint(permissionID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FilterColumns(ctx context.Context, workspaceID, userID, id string) ([]string, error) {
	filter, err := s.FilterByID(ctx, workspaceID, userID, id)
	if err != nil {
		return nil, err
	}
	if filter.Columns == nil {
		return nil, pgx.ErrNoRows
	}
	return filter.Columns, nil
}

func (s *Store) SetFilterColumns(ctx context.Context, workspaceID, userID, id string, columns []string) error {
	if len(columns) == 0 || len(columns) > 50 {
		return fmt.Errorf("%w: choose between 1 and 50 columns", ErrFilterValidation)
	}
	allowed := map[string]bool{"key": true, "summary": true, "issuetype": true, "status": true, "priority": true, "assignee": true, "reporter": true, "created": true, "updated": true}
	seen := map[string]bool{}
	for _, column := range columns {
		if !allowed[column] && !strings.HasPrefix(column, "customfield_") || seen[column] {
			return fmt.Errorf("%w: invalid or duplicate column", ErrFilterValidation)
		}
		seen[column] = true
	}
	result, err := s.Pool.Exec(ctx, `UPDATE filters SET columns=$4 WHERE workspace_id=$1 AND id=$2 AND owner_id=$3`, workspaceID, id, userID, columns)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrFilterPermission
	}
	return nil
}

func (s *Store) ResetFilterColumns(ctx context.Context, workspaceID, userID, id string) error {
	result, err := s.Pool.Exec(ctx, `UPDATE filters SET columns=NULL WHERE workspace_id=$1 AND id=$2 AND owner_id=$3`, workspaceID, id, userID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrFilterPermission
	}
	return nil
}

func (s *Store) ChangeFilterOwner(ctx context.Context, workspaceID, actorID, id, accountID string, actorAdmin bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owner, name string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(owner_id,''),name FROM filters WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, id).Scan(&owner, &name); err != nil {
		return err
	}
	if owner == "" {
		return fmt.Errorf("%w: the system filter owner cannot be changed", ErrFilterValidation)
	}
	if owner != actorID && !actorAdmin {
		return ErrFilterPermission
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2)`, workspaceID, accountID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return pgx.ErrNoRows
	}
	var duplicate bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM filters
		WHERE workspace_id=$1 AND owner_id=$2 AND lower(name)=lower($3) AND id<>$4)`,
		workspaceID, accountID, name, id).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return fmt.Errorf("%w: the new owner already has a filter with this name", ErrFilterValidation)
	}
	if _, err = tx.Exec(ctx, `UPDATE filters SET owner_id=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, id, accountID); err != nil {
		return filterDatabaseError(err)
	}
	if err = filterAudit(ctx, tx, workspaceID, actorID, "filter.owner-changed", id, name+":"+accountID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FilterDefaultShareScope(ctx context.Context, workspaceID, userID string) (string, error) {
	var scope string
	err := s.Pool.QueryRow(ctx, `SELECT scope FROM filter_default_share_scopes WHERE workspace_id=$1 AND user_id=$2`, workspaceID, userID).Scan(&scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return "PRIVATE", nil
	}
	return scope, err
}

func (s *Store) SetFilterDefaultShareScope(ctx context.Context, workspaceID, userID, scope string) error {
	scope = strings.ToUpper(scope)
	if scope == "GLOBAL" {
		scope = "AUTHENTICATED"
	}
	if scope != "PRIVATE" && scope != "AUTHENTICATED" {
		return ErrFilterValidation
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO filter_default_share_scopes(workspace_id,user_id,scope) VALUES($1,$2,$3)
		ON CONFLICT(workspace_id,user_id) DO UPDATE SET scope=excluded.scope`, workspaceID, userID, scope)
	return err
}
