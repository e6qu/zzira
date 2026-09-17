# Confluence groups

Confluence's group API. The groups are the organization's directory groups (`groups`, `group_members`), the same groups the Jira API and organization administration manage. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md). For Jira's group API on the same data, see [people](PEOPLE.md).

## API

| Method and path | Behavior | Access |
| --- | --- | --- |
| `GET /wiki/rest/api/group` | Lists groups; `accessType`, `start`, `limit`. | member |
| `POST /wiki/rest/api/group` | Creates a group (`name`); 201. | site admin |
| `GET /wiki/rest/api/group/by-id?id=` | Reads one group. | member |
| `DELETE /wiki/rest/api/group/by-id?id=` | Deletes a group and its memberships. | site admin |
| `GET /wiki/rest/api/group/picker?query=` | Searches by partial name. | member |
| `GET /wiki/rest/api/group/{groupId}/membersByGroupId` | Lists members; `expand`, `start`, `limit`. | member |
| `POST /wiki/rest/api/group/userByGroupId?groupId=` | Adds a person (`accountId` in the body); 201. | site admin |
| `DELETE /wiki/rest/api/group/userByGroupId?groupId=&accountId=` | Removes a person; 204. | site admin |

A caller who is not a member of the workspace is refused (403).

## Behavior

- **Scope**: the list and the picker return only groups of the organization that owns the site. Reads, deletes and membership changes by group id do not check the organization. New groups go into that organization's first active directory, which is created if none exists.
- **Names**: 1 to 255 characters. A duplicate name is 400.
- **Paging**: `start` and `limit` (1 to 200, default 200). The list never reports a total; the picker and member list report `totalSize` only with `shouldReturnTotalSize=true`.
- **Membership**: adding an existing member succeeds. Removing someone who is not a member is 404, and so are an unknown group or person.
- **Consistency**: `GET /wiki/rest/api/user/memberof` and `membersByGroupId` read the same membership (see [Confluence users](WIKI_USERS.md)).

## Groups by access

`accessType` filters by the access the site's role bindings give a group:

| `accessType` | Groups holding |
| --- | --- |
| `user` | user, product-user, basic, contributor or viewer role on the site's Confluence |
| `admin` | admin or product-admin role on the site's Confluence |
| `site-admin` | administration of the site or its organization |

## Member expansions

`membersByGroupId` accepts `expand`:

- `operations`: site permissions the member holds (`use` the application; for administrators also `create` spaces and `administer` the application);
- `personalSpace`: the member's personal space, when the caller can see it;
- `isExternalCollaborator`.

Unexpanded properties are listed under `_expandable`. Any other value is 400.

## Tests

- `internal/confluence/groups_test.go`
- `internal/confluence/users_groups_test.go`

## Gaps

See [PLAN.md](../PLAN.md).

- Lookups by group id (`by-id`, `membersByGroupId`, `userByGroupId`) are not limited to the site's organization.

## See also

[Confluence users](WIKI_USERS.md), [people](PEOPLE.md), [space permissions](SPACE_PERMISSIONS.md), [audit log](WIKI_AUDIT.md).
