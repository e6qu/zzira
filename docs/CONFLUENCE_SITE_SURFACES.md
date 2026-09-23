# Confluence

The Confluence overview: spaces, pages, blog posts, whiteboards, databases,
folders, Smart Links and custom content, served under `/wiki`. This doc links
to every Confluence doc and describes the whiteboard canvas and the site-wide
API surfaces that have no doc of their own. For status, see
[CLOUD_PARITY.md](CLOUD_PARITY.md). For remaining work, see
[PLAN.md](../PLAN.md).

## Where it lives

| Surface | Path | Code |
| --- | --- | --- |
| REST v2 | `/wiki/api/v2/…` | `internal/confluence/http.go` |
| REST v1 | `/wiki/rest/api/…` | `internal/confluence/v1.go` |
| Attachment downloads | `/wiki/download/attachments/…`, `/wiki/download/thumbnails/…` | `internal/confluence` |
| Browser UI | `/wiki`, `/wiki/spaces/{space}/…` | `internal/web/wiki*.go`, `internal/render/templates/wiki.gohtml`, `web/static/wiki.js` |
| Storage and permission checks | — | `internal/store/wiki_*.go` |
| CQL compiler | — | `internal/cql` |

## Docs

| Area | Doc |
| --- | --- |
| Spaces: types, statuses, creation, templates, space tools, HTML export | [CONFLUENCE_SPACES.md](CONFLUENCE_SPACES.md) |
| Space lifecycle: v1 create, update, delete, settings, theme, personal spaces | [SPACE_LIFECYCLE.md](SPACE_LIFECYCLE.md) |
| Space roles and role assignments | [CONFLUENCE_SPACE_ROLES.md](CONFLUENCE_SPACE_ROLES.md) |
| Direct space permission grants | [SPACE_PERMISSIONS.md](SPACE_PERMISSIONS.md) |
| Moving a site from grants to roles | [SPACE_PERMISSION_TRANSITION.md](SPACE_PERMISSION_TRANSITION.md) |
| Operations a caller may perform | [CONFLUENCE_OPERATIONS.md](CONFLUENCE_OPERATIONS.md) |
| Writing and reading pages | [PAGE_WRITING.md](PAGE_WRITING.md) |
| Drafts and deletion | [CONTENT_DRAFTS.md](CONTENT_DRAFTS.md) |
| Versions, macros and body conversion | [CONTENT_HISTORY.md](CONTENT_HISTORY.md) |
| Content tree: folders, whiteboards, databases, Smart Links | [CONTENT_TREE.md](CONTENT_TREE.md) |
| Moving, copying and archiving pages | [PAGE_MOVES.md](PAGE_MOVES.md) |
| Custom content | [CUSTOM_CONTENT.md](CUSTOM_CONTENT.md) |
| Content templates and blueprints | [CONTENT_TEMPLATES.md](CONTENT_TEMPLATES.md) |
| Content states | [CONTENT_STATES.md](CONTENT_STATES.md) |
| Relations and favourites | [CONTENT_RELATIONS.md](CONTENT_RELATIONS.md) |
| View analytics | [CONTENT_ANALYTICS.md](CONTENT_ANALYTICS.md) |
| Footer and inline comments | [CONFLUENCE_COMMENTS.md](CONFLUENCE_COMMENTS.md) |
| Tasks | [CONFLUENCE_TASKS.md](CONFLUENCE_TASKS.md) |
| Mentions, watches and email | [CONFLUENCE_NOTIFICATIONS.md](CONFLUENCE_NOTIFICATIONS.md) |
| Presence and live editing | [CONFLUENCE_LIVE.md](CONFLUENCE_LIVE.md) |
| Redaction | [CONFLUENCE_REDACTION.md](CONFLUENCE_REDACTION.md) |
| CQL search | [CQL_SEARCH.md](CQL_SEARCH.md) |
| Site settings and look and feel | [SITE_SETTINGS.md](SITE_SETTINGS.md) |
| Users | [WIKI_USERS.md](WIKI_USERS.md) |
| Groups | [WIKI_GROUPS.md](WIKI_GROUPS.md) |
| Audit log | [WIKI_AUDIT.md](WIKI_AUDIT.md) |
| Data classification (shared with Jira) | [CLASSIFICATION_LEVELS.md](CLASSIFICATION_LEVELS.md) |
| Forge and Connect apps | [APPS.md](APPS.md) |

## Whiteboards

A whiteboard is a node in the [content tree](CONTENT_TREE.md). The v2 API
creates, reads and deletes it and serves its ancestors, descendants,
operations and properties (`/wiki/api/v2/whiteboards…`). `templateKey` and
`locale` are stored and shown, but a template does not add any objects to the
canvas.

The canvas is edited only in the browser, at
`/wiki/spaces/{space}/whiteboards/{whiteboard}`, with plain form posts:

| Element | Fields and limits |
| --- | --- |
| Object | `type` (`sticky`, `text`, `shape`), title (≤ 255), body (≤ 10,000), `color` (`yellow`, `blue`, `green`, `pink`, `gray`), `x`/`y` 0–5000, width 80–1200, height 60–1200. Needs a title or a body. |
| Connector | A directed line from one object to a different object, with an optional label (≤ 255) and `style` `solid` or `dashed`. It is drawn from centre to centre and removed with either object. |

- **Routes.** `POST …/objects`, `…/objects/{object}` and `…/objects/{object}/delete`
  add, update and delete objects. `POST …/connectors` and
  `…/connectors/{connector}/delete` add and delete connectors.
- **Rendering.** The page draws the canvas as an SVG with a 1400 × 800
  viewBox. The same objects and connectors are also listed as accessible
  forms.
- **Moving an object.** An object is dragged on the canvas and the move is
  saved as it is let go, through the same route the form below posts to; a
  connector drawn to it follows while it moves, and a move that cannot be
  saved puts the object back where it was. Everything the drag does is also
  on the form, which is what a keyboard uses.
- **Permissions.** Changing the canvas needs update permission on a current
  whiteboard. An archived whiteboard is read-only until restored.
- **Storage.** Objects and connectors are stored in `wiki_whiteboard_objects`
  and `wiki_whiteboard_connectors` (migration 103).

## Content ids

Pages, blog posts, comments and attachments take their ids from one sequence,
`wiki_content_global_id` (migration 158). Hierarchical content (folders,
databases, whiteboards, custom content) is numbered from 10¹².

Ids that collided before that migration are kept. A bare id resolves in this
order: page, blog post, comment, attachment, hierarchical content. The content
it resolves to is then read under its own visibility rules. If the caller may
not see that content, the answer never substitutes other content that shares
the id.

## Comment properties

`GET|POST /wiki/api/v2/comments/{comment-id}/properties` and
`GET|PUT|DELETE …/properties/{property-id}` apply to footer and inline
comments.

- **Contract.** Same as page properties: one property per key, any JSON value,
  and a new version on every change. An update that does not name the next
  version gets 409.
- **Listing.** Filters by `key` and sorts by `key` or `-key`.
- **Permissions.** Reading needs permission to view the comment. Writing needs
  permission to edit it: its author or an administrator, in a space that
  allows comment updates. A caller who can see the comment but not edit it
  gets 403. A caller who cannot see it gets 404.

## Forge app properties

`GET /wiki/api/v2/app/properties` and
`GET|PUT|DELETE /wiki/api/v2/app/properties/{propertyKey}`.

- **Access.** Only an app request (`asApp()`) gets through. Anyone else gets
  401, administrators included. Each app sees only its own properties.
- **Scopes.** Reading needs `read:app-data:confluence`. Writing needs
  `write:app-data:confluence`.
- **Writes.** `PUT` returns 201 when it creates a property and 200 when it
  replaces one. The body is the value. A key longer than 127 characters is
  400.
- **Listing.** In key order, 50 per page by default (`limit` up to 250), with a
  cursor `next` link.
- **Deletes.** Deleting a missing property is not an error.
- **Storage.** Kept separately from the app's general storage. Removed when
  the app is uninstalled.

## Admin key

`GET|POST|DELETE /wiki/api/v2/admin-key`.

- **What it does.** An organization or site administrator needs an active key
  to bypass page restrictions: to read and edit restricted content and to get
  restriction-aware notifications. The admin role alone does not bypass them.
  Space administration and comment moderation depend on the role, not the key.
- **`POST`.** Issues or refreshes the caller's key. `durationInMinutes` is
  0–60, where 0 or an empty body means 10. Any other value is 400.
- **`GET`.** Returns `accountId` and `expirationTime`, or 404 when the caller
  has no unexpired key.
- **`DELETE`.** Returns 204, even when there is no key.
- **Non-administrators.** All three return 404.
- **Expiry.** An expired key grants nothing. Expiry is checked on every
  request.

## Convert content ids to types

`POST /wiki/api/v2/content/convert-ids-to-types` with
`{"contentIds": ["1234", 5678]}` returns
`{"results": {"1234": "page", "5678": "inline-comment"}}`.

- **Input.** Up to 100 ids, as strings or numbers. Each duplicate is answered
  once.
- **Types.** `page`, `blogpost`, `attachment`, `footer-comment`,
  `inline-comment`, the v2 type of hierarchical content (such as `folder`),
  or the app-defined type of custom content.
- **Missing or hidden content** maps to `null`.

## Access by email

`POST /wiki/api/v2/user/access/check-access-by-email` and
`…/invite-by-email`. Any site member may call them, with up to 100 addresses.
Having access means an active account that is a member of the site.

- **Check.** Returns `emailsWithoutAccess` and `invalidEmails`.
- **Invite.** Invalid addresses are skipped, and so are people who already
  have access.
  - A new address is invited into the organization's first active directory,
    with site access.
  - An active account that is already in the directory is given site access.
  - A suspended account stays suspended.
  - All of this finishes before the response is sent.

## Data policy metadata

`GET /wiki/api/v2/data-policies/metadata` is for apps only. It always returns
`{"anyContentBlocked": false}`, as does the per-space read
`/wiki/api/v2/data-policies/spaces`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Whiteboard canvas API.** There is no REST API for whiteboard objects and
  connectors, and no bulk import or export.
- **Whiteboard editing.** An object is dragged on the canvas and every field
  is also typed into its form, but there is no resize, and no pan or zoom:
  objects placed beyond the 1400 × 800 view are not shown.
- **Whiteboard elements.** No shape kinds (rectangle, ellipse, diamond and so
  on), freehand drawing, images, frames, sections, lines without endpoints,
  connector anchors or routing, or arrowhead choice.
- **Diagram layout.** No automatic diagram or graph layout (tree, flow or
  grid), no alignment or snapping, and no grouping.
- **Whiteboard templates.** Templates add no content to the canvas.
- **Whiteboard collaboration.** No live collaboration (presence, cursors or
  merged edits), no version history, and no comments or reactions on
  whiteboards.
- **Whiteboard features.** No voting, timer, or conversion of stickies to
  Jira work items or pages.
- **Whiteboard export.** No export to image or PDF.
- **Invite email.** `invite-by-email` sends no invitation email. Only the
  organization administration invite sends one.
- **Admin keys.** Keys are per workspace. There is no organization-wide key.
- **Data security policies.** Blocking app access to content is not modelled.

## See also

[CLOUD_PARITY.md](CLOUD_PARITY.md) · [UI_PARITY.md](UI_PARITY.md) ·
[ADMIN.md](ADMIN.md) · [PEOPLE.md](PEOPLE.md)
