# People, groups and avatars

Jira's people API: finding people, groups and their members, the caller's own preferences, properties and columns, application roles, and the avatars of projects, work types and priorities. Part of the [Jira platform](JIRA_PLATFORM.md); directory administration (invitations, suspension, product access) is in [ADMIN.md](ADMIN.md), and sign-in in [shauth-sso.md](shauth-sso.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

People are identified by `accountId`. A person on another site does not exist here: reading them or adding them to a group is 404.

## Users

| Method | Path | |
| --- | --- | --- |
| `GET` | `/rest/api/3/user?accountId=` | `expand=groups,applicationRoles` |
| `POST` | `/rest/api/3/user` | administrators; `emailAddress` and `products` are required |
| `DELETE` | `/rest/api/3/user?accountId=` | administrators; removes the person from the site |
| `GET` | `/rest/api/3/user/bulk?accountId=&accountId=` | paginated |
| `GET` | `/rest/api/3/user/bulk/migration` | usernames and keys do not exist on Cloud, so only account ids map |
| `GET` | `/rest/api/3/users`, `/rest/api/3/users/search` | everyone: active, inactive and app accounts |
| `GET` | `/rest/api/3/user/groups?accountId=` | |
| `GET` | `/rest/api/3/user/email`, `/user/email/bulk` | apps only; a person calling them is 400 |

- Every user carries `self` and `avatarUrls`.
- Email addresses are shown only to the person and to administrators.
- Reading someone else needs *Browse users and groups* (see [ANONYMOUS_ACCESS.md](ANONYMOUS_ACCESS.md) for the effect on searches).
- `POST /user` for an address that already has access is 200 with that person; a new address is invited (201). `products` accepts `jira-software`, `jira-servicedesk` and `jira-product-discovery`.

## Finding people

| Path | Finds |
| --- | --- |
| `/rest/api/3/user/search` | active people by `query`, `accountId` or `property` (at least one; `query` and `accountId` not together) |
| `/rest/api/3/user/assignable/search` | people assignable in a `project`, or on an `issueKey` / `issueId` |
| `/rest/api/3/user/assignable/multiProjectSearch` | people assignable in every one of `projectKeys` |
| `/rest/api/3/user/viewissue/search` | people who can browse an `issueKey` (security level included) or a `projectKey` |
| `/rest/api/3/user/picker` | `users`, `total` and `header`, with the match wrapped in `<strong>` |
| `/rest/api/3/groupuserpicker` | the same for people and groups together |

`query` matches a display name, or an email address from its start. `property`
takes `key.path=value`.

### Structured user query

`/rest/api/3/user/search/query` returns a page of users, and `/query/key` returns
`accountId` and `key` pairs. The query language is Jira's:

```
is assignee of PROJ
is watcher of (PROJ-1, PROJ-2)
[propertyKey].path.to.value is "value"
is reporter of PROJ AND [prefs].team is "core" OR is voter of PROJ
```

Relations are `assignee`, `reporter`, `watcher`, `voter`, `commenter` and
`transitioner`. `AND` binds tighter than `OR`. An unknown relation, project or
issue, or a malformed query, is 400.

## Groups

| Method | Path | |
| --- | --- | --- |
| `GET` | `/rest/api/3/group?groupId=` or `groupname=` | `expand=users` |
| `POST` | `/rest/api/3/group` | administrators |
| `DELETE` | `/rest/api/3/group?groupId=&swapGroupId=` | administrators; `groupname` and `swapGroup` also accepted |
| `GET` | `/rest/api/3/group/bulk` | filter by `groupId` and `groupName` |
| `GET` | `/rest/api/3/group/member` | `includeInactiveUsers` |
| `POST` / `DELETE` | `/rest/api/3/group/user` | administrators |
| `GET` | `/rest/api/3/groups/picker` | `query`, `exclude`, `excludeId`, `accountId`, `caseInsensitive` |

Groups belong to the site's organization and are invisible from other organizations.

Deleting a group with a swap group moves its members and everything it held to the swap group: permission scheme grants, issue security level members, project role default actors, product and site role bindings, filter share permissions, Confluence page restrictions, space permissions and space roles, and request type groups. Without a swap group those grants are removed. A group cannot be swapped for itself.

## Myself, preferences, properties and columns

| Path | Behavior |
| --- | --- |
| `GET /rest/api/3/myself` | Adds `locale`; `expand=groups,applicationRoles`. |
| `/rest/api/3/mypreferences?key=` | GET, PUT (plain text, max 255 characters), DELETE one preference. Missing key: 404. `user.notify.own.changes` and `user.autowatch.disabled` are the My changes and Autowatch settings the profile page edits ([NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md)). |
| `/rest/api/3/mypreferences/locale` | Reads or sets the locale from a supported list; others are 400. Unset, the browser's `Accept-Language` decides. |
| `/rest/api/3/user/properties?accountId=`, `/user/properties/{key}` | Lists keys; reads, stores (201 new, 200 replaced) and removes JSON values. Only the person or an administrator. |
| `/rest/api/3/user/columns` | The caller's issue table columns (`label`, `value`), falling back to the site's navigator columns ([JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md)). `PUT` takes form data (`columns=summary&columns=status`); `DELETE` resets. Administrators may pass `accountId`. |

## Application roles

`GET /rest/api/3/applicationrole` and `/applicationrole/{key}` (administrators) report `jira-software` and `jira-servicedesk` with the groups granted the product and `userCount`: people with access directly or through those groups.

## Avatars

Projects, work types and priorities share one avatar store.

| Method | Path | |
| --- | --- | --- |
| `GET` | `/rest/api/3/avatar/{type}/system` | `project`, `issuetype`, `priority`, `user` |
| `GET` / `POST` | `/rest/api/3/universal_avatar/type/{type}/owner/{id}` | `system` and `custom`; upload for administrators |
| `DELETE` | `/rest/api/3/universal_avatar/type/{type}/owner/{id}/avatar/{avatarId}` | system avatars are 403 |
| `GET` | `/rest/api/3/universal_avatar/view/type/{type}` | the default for the type |
| `GET` | `/rest/api/3/universal_avatar/view/type/{type}/avatar/{id}` | `size` from `xsmall` to `xlarge` |
| `GET` | `/rest/api/3/universal_avatar/view/type/{type}/owner/{id}` | the owner's selected avatar |
| `GET` | `/rest/api/3/project/{projectIdOrKey}/avatars` | |
| `POST` | `/rest/api/3/project/{projectIdOrKey}/avatar2` | project administrators |
| `PUT` | `/rest/api/3/project/{projectIdOrKey}/avatar` | selects an avatar by `id` |
| `DELETE` | `/rest/api/3/project/{projectIdOrKey}/avatar/{id}` | |

Uploads need `X-Atlassian-Token: no-check` and a JPEG, GIF or PNG body. Work type avatars uploaded through `/issuetype/{id}/avatar2` ([ISSUE_METADATA.md](ISSUE_METADATA.md)) use the same store. Priority and work type icons are served at `/static/img/` and `/images/icons/priorities/`.

## Code and tests

- `internal/api3/people.go`, `internal/api3/user_query.go`
- `internal/store/jira_people.go`, `jira_groups.go`, `universal_avatars.go`, `application_roles.go`
- `migrations/164_jira_people.sql`
- `internal/api3/people_test.go`
