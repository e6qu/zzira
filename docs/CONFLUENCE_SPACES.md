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
