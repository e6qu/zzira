# Confluence users

Updated: 2026-09-11

Confluence looks people up by account id or email, reports the groups they are
in, searches for them, and keeps arbitrary app data against them.

## An email address is administration

A workspace member may see who someone is. Only an administrator may see their
email address — which is why Confluence has separate `/user/email` endpoints
rather than an `email` field on the user read. That distinction is kept here:
the user reads never carry an email, and the email reads refuse a member.

The test asserts both halves: that the ordinary read has no `email` field at
all, and that a member asking the email endpoints gets 403.

## Jira Cloud REST surface

All 14 pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /wiki/rest/api/user` | One person by `accountId`. |
| `GET /wiki/rest/api/user/current` | The caller. |
| `GET /wiki/rest/api/user/anonymous` | The reader who is not signed in. |
| `GET /wiki/rest/api/user/bulk` | Several people at once. |
| `GET /wiki/rest/api/user/email` and `/email/bulk` | Email addresses, for an administrator. |
| `GET /wiki/rest/api/user/memberof` | The groups a person is in. |
| `GET /wiki/rest/api/search/user` | People matching a CQL query. |
| `GET /wiki/rest/api/user/{userId}/property` | A person's properties. |
| `GET/POST/PUT/DELETE /wiki/rest/api/user/{userId}/property/{key}` | One property; a new key answers 201 and a second create is a 409. |
| `POST /wiki/api/v2/users-bulk` | The v2 bulk read, taking its ids in the body. |

## Anonymous has no account

So the anonymous bean carries no `accountId`, and no link to one. Returning an
empty id, or a link to `?accountId=`, would describe an account that does not
exist.

## A bulk read skips what it cannot answer

An id that is not one of this workspace's people is left out rather than
failing the whole request, which is what makes a bulk read useful: a client
resolving a list of ids from stored data will always have a few that no longer
resolve.

## The user search

Confluence takes a CQL string. What this answers is the text a person is matched
on — `user.fullname ~ "…"` and `user ~ "…"` — against display name, nickname and
email. **A query naming a field this does not filter on is refused**, because
silently ignoring the filter would match everyone and look like a working
search.

## Whose properties are whose

A person's own data is theirs to write. Writing someone else's needs
administration, because a property is read back as if that person had set it.
Reading is open to any member, as Confluence has it.

## Evidence and current boundary

- `internal/confluence/users_test.go` covers all 14 operations, that the user
  reads carry no email and the email reads refuse a member, that anonymous has
  no account id, that a bulk read skips ids outside the workspace, the groups a
  person is in and the empty answer for someone in none, the refused unsupported
  CQL, the 201/409/200 property lifecycle, that a member cannot write another
  person's property while an administrator can, and that someone outside the
  workspace cannot enumerate its people.
- `migrations/148_wiki_user_properties.sql` is exercised from a clean PostgreSQL
  schema.

Confluence's `expand` on the user reads, cursor paging on the bulk reads, the
`sitePermissionTypeFilter` on the search, external collaborator accounts, and
the invite-by-email and check-access-by-email operations remain.
