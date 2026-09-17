# Data classification levels

Data classification levels belong to the organization and apply to Jira
projects and work items and to Confluence content on every site of that
organization. Part of the [Jira platform](JIRA_PLATFORM.md) and
[Confluence](CONFLUENCE_SITE_SURFACES.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## Levels

A level has a name (unique in the organization), a description, a handling
guideline, a rank, a status and one of the colors `RED`, `RED_BOLD`, `ORANGE`,
`YELLOW`, `GREEN`, `BLUE`, `NAVY`, `TEAL`, `PURPLE`, `GREY` or `LIME`. Every
organization starts with Public, Internal, Confidential and Restricted.

| Status | Meaning |
| --- | --- |
| `DRAFT` | New levels start here. Editable, not selectable. |
| `PUBLISHED` | Selectable for projects, spaces and content. |
| `ARCHIVED` | Not selectable or editable. Existing uses keep and report it. Can be published again. |

A published level never returns to draft.

## API

| Route | Behavior |
|---|---|
| `GET /rest/api/3/classification-levels` | Levels filtered by `status`, ordered by rank |
| `GET /rest/api/3/project/{projectIdOrKey}/classification-config` | Permitted (published) levels and the project default |
| `GET/PUT/DELETE /rest/api/3/project/{projectIdOrKey}/classification-level/default` | Project default; must be a published level; changes need Administer projects |
| `GET /wiki/api/v2/classification-levels` | Published and archived levels, in order |
| `GET/PUT/DELETE /wiki/api/v2/spaces/{id}/classification-level/default` | Space default |
| `GET/PUT /wiki/api/v2/{pages,blogposts,whiteboards,databases}/{id}/classification-level`, `POST …/reset` | Content level |

Details of the Jira routes are in [JIRA_PLATFORM.md](JIRA_PLATFORM.md#data-classification-and-data-policy).

## Behavior

- **Jira.** A new work item takes its project's default level. Bulk move maps
  or infers levels (see [BULK_ISSUES.md](BULK_ISSUES.md)).
- **Confluence.** Content and space-default writes accept only published
  levels. Content without its own level reads its space's default; reset
  clears the content's own level so it inherits again. Every wiki
  classification picker lists published levels, and content holding an
  archived level shows it as archived.

## UI

**Administration → Data classification levels** (`/admin`): create drafts,
edit, publish, archive, restore and reorder. Available to site and
organization administrators. Confluence pages, blog posts, whiteboards,
databases and space settings have classification pickers.

## Gaps

See [PLAN.md](../PLAN.md).

- A work item's level cannot be read or changed: no issue field, REST value,
  work item page control or JQL clause.
- No project settings control for the project default (REST only).
- `organizationDefaultClassificationLevel` is always null; there is no
  organization default.
- Levels are managed by site administrators as well as organization
  administrators; Atlassian limits this to organization administrators.

## See also

[PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md) ·
[CONFLUENCE_SPACES.md](CONFLUENCE_SPACES.md) · [ADMIN.md](ADMIN.md)
