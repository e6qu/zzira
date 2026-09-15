# Confluence spaces: kinds, states, access and templates

## Kinds and states
- **Space types:** `global`, `collaboration`, `knowledge_base`, `personal`,
  `system`, `onboarding` and `xflow_sample_space`.
- **Statuses:** `current`, `archived` and `trashed`.
- Both are stored, can be set through the v1 space update, and are reported
  on every space bean.

## Listing spaces
`GET /wiki/api/v2/spaces` filters by:
- `ids`, `keys` and `labels`;
- `type` and `status` (unknown values are refused with 400);
- `favorited-by` and `not-favorited-by`.

Starring a space is a `favourite` relation from the person to the space, the
same relation the relations API reads and writes.

## Creating a space
`POST /wiki/api/v2/spaces` takes a `key`, an `alias`, or both.
- A space named only by an alias takes its key from it, upper-cased.
- `currentActiveAlias` reports the alias.

Access comes from exactly one of these:
- **Default roles.** With nothing given, members get the member role and
  product admins the admin role. `createPrivateSpace` instead limits the
  space to its creator.
- **`roleAssignments`.** The space gets exactly the given roles.
  - At least one assigned role must include `administer/space`.
  - A space whose only assignment is its creator as an administrator is
    private.
  - Roles must exist, and users, groups and access classes must belong to
    the site.
- **`copySpaceAccessConfiguration`.** The space takes the role assignments,
  direct permission grants and privacy of the named space. The caller must
  administer that space.

Giving more than one of these is refused with 400.

`templateKey` starts the space from a space template, which sets its type and
writes its homepage:

| Template key | Type | Homepage |
| --- | --- | --- |
| `com.atlassian.confluence.plugins.confluence-space-blueprints:documentation-space-blueprint` | global | Documentation |
| `com.atlassian.confluence.plugins.confluence-space-blueprints:team-space-blueprint` | collaboration | Team home |
| `com.atlassian.confluence.plugins.confluence-knowledge-base:knowledge-base-space-blueprint` | knowledge_base | Knowledge base |
| `com.atlassian.confluence.plugins.confluence-software-blueprints:software-project-space-blueprint` | collaboration | Project overview |

## Permissions
`GET /spaces/{id}/permissions` and `include-permissions` report what the space
actually holds:
- every permission of every role assigned in the space, for the principal it
  is assigned to (`user`, `group`, or `role` for an access class);
- every direct grant.

Each permission appears once, with an id that stays the same for that
principal and permission in that space.

## Space tools in the browser
A space's page links to its **Templates** and **Analytics**, and its
administrators can archive the space or restore it to current from there.

The templates page lists the space's own content templates, the site's
templates the space inherits, and the blueprints with any site or space changes
applied. Space administrators create, edit and delete the space's own templates:
a name, a description, whether it is for pages or blog posts, a storage-format
body and labels. Every page template offers **Create page from**, which opens the
page editor with the template's body; a template from another space cannot be
used.

The analytics page counts views, and distinct viewers, of the space's current
pages and blog posts over the last 7, 30 or 90 days, most viewed first and at
most 50. It counts only content the reader can see, so the numbers never reveal
restricted pages.

Archiving sets the space's status to `archived` and shows it as an archived
space; restoring returns it to `current`.

Space administrators can also export a space from its page. **Export to HTML**
queues a task that writes the space's current pages and blog posts that the
administrator can see into a zip: an `index.html` linking every page and blog
post, and one HTML file for each with its body rendered as the space shows it.
Attachments are not included. The space page lists the administrator's latest
five exports with their state, and a finished export downloads only for the
person who asked for it; an email with the download link is sent when it is
ready. Importing a space is not supported.
