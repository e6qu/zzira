# Confluence users

Confluence's user API: look people up by account id, read email addresses (administrators only), list a person's groups, search people, and store per-user properties. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md). For Jira's user API on the same people, see [people](PEOPLE.md).

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/rest/api/user?accountId=` | One person. |
| `GET /wiki/rest/api/user/current` | The caller. |
| `GET /wiki/rest/api/user/anonymous` | The signed-out reader. |
| `GET /wiki/rest/api/user/bulk?accountId=` | Several people. |
| `GET /wiki/rest/api/user/email?accountId=`, `/user/email/bulk` | Email addresses. Workspace administrators only (403 otherwise). |
| `GET /wiki/rest/api/user/memberof?accountId=` | The person's groups (`start`, `limit`). |
| `GET /wiki/rest/api/search/user?cql=` | People search. |
| `GET /wiki/rest/api/user/{userId}/property` | The person's properties, by key. |
| `GET` / `POST` / `PUT` / `DELETE /wiki/rest/api/user/{userId}/property/{key}` | One property. `POST` of a new key is 201; `POST` of an existing key is 409. |
| `POST /wiki/api/v2/users-bulk` | Up to 250 people (`accountIds` in the body; 1 to 250, otherwise 400). |

Access by email (`/wiki/rest/api/user/access/check-access-by-email`, `/invite-by-email`) is documented in [Confluence site surfaces](CONFLUENCE_SITE_SURFACES.md).

All operations require workspace membership; a non-member cannot enumerate people.

## Behavior

- **Emails**: user beans never carry `email`. Only the `/user/email` endpoints return it.
- **Anonymous**: the anonymous bean has no `accountId` and no link to one.
- **Bulk reads**: account ids that are not people in this workspace are skipped, not errors.
- **Properties**: anyone in the workspace can read them. A person writes their own; writing someone else's needs workspace administration (403).
- **External collaborators**: a person whose Confluence access on the site is the guest role, directly or through a group, reports `isExternalCollaborator: true` on every user bean.

## User search

`cql` supports only `user.fullname ~ "…"` and `user ~ "…"`, matched against display name, nickname and email. Any other CQL is 400, so an unsupported filter is never silently ignored.

- `sitePermissionTypeFilter`: `none` (default, licensed users), `externalCollaborator` (guests) or `all`.
- `start`, `limit` (0 to 1000, default 25); `totalSize` is the count before paging.
- `expand` accepts `operations`, `personalSpace` and `isExternalCollaborator`, as on [group members](WIKI_GROUPS.md#member-expansions).

## Storage

People and memberships come from the workspace directory. Properties are in `wiki_user_properties`.

## Tests

- `internal/confluence/users_test.go`
- `internal/confluence/users_groups_test.go`

## Gaps

See [PLAN.md](../PLAN.md).

- `expand` is accepted and ignored on `user`, `user/current`, `user/anonymous` and `user/bulk`.
- `user/bulk` ignores `start`, `limit` and `cursor` and returns every requested person.
- CQL user search beyond the two `~` forms.

## See also

[Confluence groups](WIKI_GROUPS.md), [people](PEOPLE.md), [anonymous access](ANONYMOUS_ACCESS.md), [CQL search](CQL_SEARCH.md).
