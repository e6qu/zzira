# Confluence site surfaces

This covers the last sixteen pinned `confluence-v2` operations: content
properties on comments, Forge app properties, the admin key, content id
conversion, access by email, and the data policy metadata. With them, every
pinned Confluence operation — v1 and v2 — is assessed.

## Content ids are unique across content

Confluence content ids are unique across every kind of content. Here each table
used to number its own rows, so page 1 and blog post 1 could both exist. That
was visible at the API — a bare content id could name either — and it leaked:
the analytics resolver fell through from a page the reader could not see to a
blog post that happened to share its id.

Since migration 158, pages, blog posts, comments and attachments draw ids from
one sequence that starts above every id already used. Hierarchical content
(folders, databases, whiteboards, custom content) already numbers from 10¹².

Ids created before that can still collide, and are left as they are: renumbering
would break every link and stored reference a client holds. A bare id is resolved
by what exists, not by what the caller may see, in a fixed order — page, blog
post, comment, attachment, hierarchical content — and the resolved content is
then read through its own visibility rules. Content a caller may not open is
never answered with different content that shares its id.

## Comment properties

| Method | Path |
| --- | --- |
| `GET` | `/wiki/api/v2/comments/{comment-id}/properties` |
| `POST` | `/wiki/api/v2/comments/{comment-id}/properties` |
| `GET` | `/wiki/api/v2/comments/{comment-id}/properties/{property-id}` |
| `PUT` | `/wiki/api/v2/comments/{comment-id}/properties/{property-id}` |
| `DELETE` | `/wiki/api/v2/comments/{comment-id}/properties/{property-id}` |

The same contract as page and blog post properties, on footer and inline comments
alike: a key names one property per comment, values are any JSON, every change
is a new version, and an update must name the next version or it is a 409 —
which is what stops two editors silently overwriting each other.

Reading needs permission to see the comment. Writing needs permission to edit it:
its author, or an administrator, in a space that allows comment updates — the
same rule the comment is edited under. Someone who may see a comment but not edit
it is refused with 403; a comment the caller may not see is a 404.

The listing filters by `key` and sorts by `key` or `-key`.

## Forge app properties

| Method | Path |
| --- | --- |
| `GET` | `/wiki/api/v2/app/properties` |
| `GET` | `/wiki/api/v2/app/properties/{propertyKey}` |
| `PUT` | `/wiki/api/v2/app/properties/{propertyKey}` |
| `DELETE` | `/wiki/api/v2/app/properties/{propertyKey}` |

Values an app keeps under its own keys. Only a request authenticated as the app
reaches them — Forge's `asApp()` — so a person, even an administrator, is refused
with 401. Each app sees only its own properties.

- `PUT` answers 201 when it creates a property and 200 when it replaces one. The
  body is the value itself, and may be any JSON.
- A key is at most 127 characters; a longer one is a 400.
- The listing returns 50 properties unless given a `limit` up to 250, in key
  order, with a `next` link carrying an opaque cursor.
- Deleting a property that is not there leaves the app in the state it asked
  for, and is not an error.

Forge classifies these under the app-data scopes, so an app needs
`read:app-data:confluence` to read them and `write:app-data:confluence` to change
them; the content scopes do not reach them. Properties are kept apart from the
app's general storage, so a property and a storage entry with the same key never
overwrite each other, and they are removed with the installation.

## Admin key

| Method | Path |
| --- | --- |
| `GET` | `/wiki/api/v2/admin-key` |
| `POST` | `/wiki/api/v2/admin-key` |
| `DELETE` | `/wiki/api/v2/admin-key` |

An admin key gives an organization or site administrator temporary access to all
content, including content restricted to other people.

**The admin role alone no longer bypasses restrictions.** Before this change an
administrator saw and could edit every restricted page unconditionally. In
Confluence an administrator without a key sees restricted content only as anyone
else would, so the restriction bypass in the page visibility rules — reading,
editing and restriction-aware notification delivery — now requires an active key
as well as the admin role. Space administration and comment moderation are
unchanged: they belong to the role, not to content restrictions.

- `POST` issues a key, replacing any the caller holds with a fresh expiry. The
  body is optional; an empty body or `durationInMinutes` of 0 means ten minutes,
  and the most is 60. Anything outside 0–60 is a 400.
- `GET` returns `accountId` and `expirationTime`, and 404 when the caller holds no
  unexpired key.
- `DELETE` ends the key; ending one that is not there is still 204.
- Anyone who is not an administrator is answered 404 by all three, as Confluence
  answers them.

An expired key opens nothing, and is checked on every read rather than cleaned up
on a schedule.

## Convert content ids to types

`POST /wiki/api/v2/content/convert-ids-to-types`

For a client migrating from v1, which stored bare ids. v1 called every comment
`comment`; v2 distinguishes `footer-comment` from `inline-comment`, and that is
the distinction this answers.

```json
{ "contentIds": ["1234", 5678] }
→ { "results": { "1234": "page", "5678": "inline-comment" } }
```

- Ids may be strings or numbers, up to 100 of them. A duplicate is answered once.
- Built-in types are `page`, `blogpost`, `attachment`, `footer-comment` and
  `inline-comment`. Hierarchical content is reported by its v2 type, such as
  `folder`; custom content by the app-defined type it was created as.
- Content the caller may not view, or that does not exist, maps to `null`, so the
  answer never confirms that hidden content exists.

## Access by email

| Method | Path |
| --- | --- |
| `POST` | `/wiki/api/v2/user/access/check-access-by-email` |
| `POST` | `/wiki/api/v2/user/access/invite-by-email` |

Anyone who can use the site may ask which of up to 100 addresses have no access,
and invite them. Access is membership of the site: an active account that belongs
to it.

- The check returns `emailsWithoutAccess` and `invalidEmails`. A duplicate address
  is answered once.
- The invite ignores invalid addresses and leaves people who already have access
  alone, as Confluence documents. A new address is invited into the
  organization's directory with site access. An account already in the directory
  but not the site is given access rather than invited again; a suspended account
  stays suspended.
- Confluence documents the invite as asynchronous. Here the invitations are
  complete before the response, which a client treating it as asynchronous
  cannot tell apart.

## Data policy metadata

`GET /wiki/api/v2/data-policies/metadata` answers `{"anyContentBlocked": false}`.
Only an app may ask. No data policy restricts content here, so no content is ever
blocked for an app — the answer, matching the per-space data policy read, not an
omission.

## Boundary

- Pre-existing colliding content ids are resolved in a fixed order rather than
  renumbered.
- The invite does not send notification email; accounts are created and given
  access, and an invitation email is sent only through the organization
  administration invite.
- The admin key is stored per workspace. There is no separate organization-wide
  key spanning several sites.
