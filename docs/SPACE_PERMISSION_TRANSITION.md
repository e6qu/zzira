# Confluence space permission transition

These endpoints move a site from [direct grants](SPACE_PERMISSIONS.md) to
[space roles](CONFLUENCE_SPACE_ROLES.md) in three steps: scan, decide, apply.
Part of [Confluence](CONFLUENCE_SITE_SURFACES.md). Code:
`internal/confluence/permission_transition.go`,
`internal/store/wiki_permission_transition.go`, migration 150.

## API

| Method and path | Behavior |
| --- | --- |
| `POST /wiki/api/v2/space-permissions/transition/combinations` | Queues the scan. Returns `202`. |
| `GET /wiki/api/v2/space-permissions/transition/combinations` | Lists the combinations the scan found. |
| `POST /wiki/api/v2/space-permissions/transition/role-assignments` | Queues role assignments for combinations. Returns `202`. |
| `POST /wiki/api/v2/space-permissions/transition/access-removals` | Queues removal of whole combinations. Returns `202`. |
| `GET /wiki/api/v2/space-permissions/transition/tasks/{taskId}` | Reports a transition task. |

A `202` response carries `taskId`, `status: IN_PROGRESS` and `statusUrl`. The
task read returns `IN_PROGRESS`, `COMPLETED` or `FAILED`; only `FAILED`
includes an `errorMessage`.
Every endpoint needs site administration.

## Behavior

- **Scan.** Groups every direct grant on the site into combinations. A
  combination is a distinct set of permissions held by a subject in a space.
  - **Id.** The id (`cmb_…`) is a hash of the sorted permission set, so a
    rescan keeps the same ids and earlier decisions stay valid.
  - **Counts.** `principalCount` counts distinct subjects: one person holding
    the set in three spaces counts once. `spaceCount` counts the spaces where
    the set appears. `principalTypes` lists the subject types that hold it.
  - **Excluded sets.** A combination whose set matches an existing system or
    custom role is not listed.
- **Listing.** Sorted by principal count, highest first.
  - **Paging.** Pages with `limit` (1–250, default 25) and an opaque `cursor`,
    which the response returns while more results remain.
  - **Errors.** A limit out of range, or a cursor this endpoint did not issue,
    is 400.
- **Role assignments.** For each combination, the request gives a decision per
  principal type: either a `roleId` or `removeAccess`. A decision with neither
  is 400. In the selected spaces, each subject whose grants match the
  combination is handled as follows:
  - **Role decisions.** The subject gets the chosen role, and its direct
    grants are deleted.
  - **Remove-access decisions.** The subject's grants are deleted and no role
    is assigned.
  - **Types with no decision.** Principal types the request does not mention
    keep their grants.
- **Access removals.** Deletes the grants of the given combinations and
  assigns nothing.
- **Space selection.** `spaceType` is one of `ALL` (the default), `SPECIFIC`,
  `ALL_EXCEPT_SPECIFIC`, `PERSONAL` or `ALL_EXCEPT_PERSONAL`.
  - **Naming spaces.** Selected spaces may be named by id or by key.
  - **Required spaces.** `SPECIFIC` and `ALL_EXCEPT_SPECIFIC` need at least
    one space.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Principal types.** Grants hold only users and groups, so the `GUEST`,
  `ANONYMOUS` and `APP` principal types never appear and cannot be
  transitioned.
- **Browser.** There is no transition UI; it is API only.

## Tests

`internal/confluence/permission_transition_test.go`

## See also

[SPACE_LIFECYCLE.md](SPACE_LIFECYCLE.md#space-role-mode) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
