# Confluence spaces

This doc covers space types and statuses, the v2 space list and create
endpoints, space templates, the space permission report, and the space tools
in the browser: templates, analytics, archiving, deleting and HTML export.
Part of [Confluence](CONFLUENCE_SITE_SURFACES.md). The v1 create, update and
delete endpoints, the space trash and the space directory's lists are in
[SPACE_LIFECYCLE.md](SPACE_LIFECYCLE.md).

## Types and statuses

- **Types.** `global`, `collaboration`, `knowledge_base`, `personal`,
  `system`, `onboarding`, `xflow_sample_space`.
- **Statuses.** `current`, `archived`, `trashed`.
- **Where they appear.** Both are stored, both can be set through the v1 space
  update, and both appear on every space bean.

## Listing spaces

`GET /wiki/api/v2/spaces` filters by:

- `ids`, `keys` and `labels`;
- `type` and `status` (an unknown value is 400);
- `favorited-by` and `not-favorited-by`.

It also takes `sort`, `description-format`, `include-icon`, `cursor` and
`limit`.

- **Single space.** `GET /wiki/api/v2/spaces/{id}` also takes
  `include-operations`, `include-properties`, `include-permissions`,
  `include-role-assignments` and `include-labels`.
- **Stars.** Starring a space creates a `favourite`
  [relation](CONTENT_RELATIONS.md).

## Creating a space

`POST /wiki/api/v2/spaces` needs a `key`, an `alias`, or both. A space created
with only an alias takes the upper-cased alias as its key.
`currentActiveAlias` reports the alias.

Access is set up by exactly one of the following. Giving more than one is 400.

- **Nothing given.** Default [roles](CONFLUENCE_SPACE_ROLES.md) apply:
  authenticated users get the member role and product admins get the admin
  role.
  - `createPrivateSpace` instead limits the space to its creator.
- **`roleAssignments`.** The space gets exactly these assignments.
  - At least one assigned role must include `administer/space`.
  - If the only assignment is the creator as administrator, the space is
    private.
  - Roles must exist. Users, groups and access classes must belong to the site.
- **`copySpaceAccessConfiguration`.** The space copies another space's role
  assignments, direct grants and privacy. The caller must administer that
  space.

`templateKey` creates the space from a space template. The template sets the
space type and writes the homepage:

| Template key | Type | Homepage |
| --- | --- | --- |
| `com.atlassian.confluence.plugins.confluence-space-blueprints:documentation-space-blueprint` | global | Documentation |
| `com.atlassian.confluence.plugins.confluence-space-blueprints:team-space-blueprint` | collaboration | Team home |
| `com.atlassian.confluence.plugins.confluence-knowledge-base:knowledge-base-space-blueprint` | knowledge_base | Knowledge base |
| `com.atlassian.confluence.plugins.confluence-software-blueprints:software-project-space-blueprint` | collaboration | Project overview |

## Permission report

`GET /wiki/api/v2/spaces/{id}/permissions` and `include-permissions` list
what the space actually grants:

- every permission of every role assigned in the space, for the principal it
  is assigned to (`user`, `group`, or `role` for an access class);
- every [direct grant](SPACE_PERMISSIONS.md).

Each permission appears once. Its id stays the same for a given principal,
permission and space.

## Space tools in the browser

The space page (`/wiki/spaces/{space}`) links to the following tools:

- **Templates** (`/wiki/spaces/{space}/templates`).
  - **What it lists.** The space's own content templates, the site templates
    the space inherits, and the blueprints, with any site or space changes
    applied.
  - **Managing templates.** Space administrators create, edit and delete the
    space's own templates. A template has a name, a description, a type (page
    or blog post), a storage-format body and labels.
  - **Using a template.** Every page template offers **Create page from**,
    which opens the editor with the template's body. Templates from another
    space cannot be used. See [templates](CONTENT_TEMPLATES.md).
- **Analytics** (`/wiki/spaces/{space}/analytics`).
  - **What it shows.** Views and distinct viewers of the space's current
    pages and blog posts over the last 7, 30 or 90 days. It lists up to 50
    items, most viewed first.
  - **Visibility.** Only content the reader can see is counted. See
    [analytics](CONTENT_ANALYTICS.md).
- **Archive and restore** (`POST /wiki/spaces/{space}/status`, space
  administrators only). Archiving sets the space status to `archived`.
  Restoring sets it back to `current`. An archived space leaves the general
  list in the space directory for the directory's archived list, and leaves
  search; its content is kept. A space in the trash cannot be archived.
- **Delete** (`POST /wiki/spaces/{space}/trash`, space administrators only).
  The space goes to the trash rather than away, and a site administrator
  restores it or deletes it permanently from there. See
  [lifecycle](SPACE_LIFECYCLE.md).
- **Export to HTML** (`POST /wiki/spaces/{space}/exports`, space
  administrators only). This queues a background task (`wiki-space-export`)
  that builds a zip containing:
  - `index.html`, which links to every exported page and blog post;
  - `pages/{id}.html` and `blogposts/{id}.html` for each current page and blog
    post the exporter can see. Each body is rendered as the space shows it,
    followed by a list of attachments;
  - `attachments/{id}/{filename}`: the current attachments the exporter can
    see, up to 100 MiB in total. Attachments over the limit are listed but not
    included.

  - `space.json`: the same pages and blog posts in the storage the site keeps,
    with each page's parent, its labels, the comments under it, the files the
    archive carries for it, who may read and edit it (people by email, groups
    by name, because an account id means nothing on another site), and what
    the page said before now -- every earlier version with its author, its
    date and what they said about the edit. It is what an import reads. The
    HTML is for reading; the manifest is for moving.

  The space page lists the exporter's five latest exports and their status.
  The download (`GET /wiki/spaces/{space}/exports/{task}.zip`) is available
  only to the person who requested the export. When the export is ready,
  that person is emailed a link. Code: `internal/store/wiki_space_export.go`.
- **Import a space** (`POST /wiki/spaces/import`, site administrators only),
  from the wiki home page. It reads an export's `space.json` and makes a new
  space from it: every page in the tree it was in, then the blog posts. The key
  and name are the importer's -- a space is usually read back beside the one it
  came from -- and default to the export's. A page's labels, the comments under
  it and the files the archive carries come back with it; a comment is written
  by the person importing, under a line naming who wrote it where it came from,
  because the site it came from is not this one. A page with a past is written
  the way it was written -- its oldest version first, then each later one -- so
  its history comes with it, every version owned by the importer and saying who
  wrote it where it came from. Who may read and edit a page comes back too,
  matching a person by their email and a group by its name; somebody this site
  does not have is named on the space the import made, because a page that
  loses a restriction is a page more people can read. The drafts nobody
  published are not carried, and neither is a file the export left out for
  being too large. An archive with no manifest, or one written by
  a later version of the site, is refused rather than making an empty space.
  Code: `internal/store/wiki_space_import.go`.

The space page also handles watching, classification, space properties, role
assignments and content state settings.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Import.** A space is read back from this site's own export ([above](#space-tools-in-the-browser)); no Confluence XML, no
  HTML or Markdown, and no Word or other document import.
- **Export formats.** No XML export (full or custom) for backup or migration,
  no site export, and no PDF, Word or CSV export of a space.
- **Export contents.** The HTML export has no page hierarchy, comments,
  whiteboards, databases, folders or custom content.

## Browser

Renaming a space, rewriting its description and choosing its home page live on
the space page; see [lifecycle](SPACE_LIFECYCLE.md#browser).

## Tests

`internal/confluence/spaces_test.go`, `internal/store/wiki_space_export_test.go`,
`internal/store/wiki_space_trash_test.go`, `internal/web/wiki_space_trash_test.go`

## See also

[CONTENT_TREE.md](CONTENT_TREE.md) · [CQL_SEARCH.md](CQL_SEARCH.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
