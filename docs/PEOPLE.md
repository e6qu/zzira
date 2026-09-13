# People, groups and avatars

Jira's people and identity API: the people on a site and how to find them, the
groups they belong to, their own preferences, properties and issue table
columns, the product access they hold, and the avatars of projects, issue types
and priorities.

People are identified by `accountId` everywhere. A person on another site does
not exist here: looking them up is 404, and adding them to a group is 404.

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

Every user carries `self` and `avatarUrls`. An email address is shown to the
person themself and to administrators; others see the record without it.
Reading someone else needs *Browse users and groups*.

`POST /user` for an address that already has access answers 200 with that
person. A new address is invited and answers 201. `products` accepts
`jira-software`, `jira-servicedesk` and `jira-product-discovery`.

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

A group belongs to the organization that owns the site. It is never visible from
another organization's site.

Deleting a group with a swap group moves everything the group held onto the swap
group, as Jira does, and gives its members to the swap group. That covers
permission scheme grants, issue security level members, project role default
actors, product and site role bindings, filter share permissions, Confluence page
restrictions, space permissions and space roles, and service request type groups.
Without a swap group, those grants are removed with it. A group cannot be
swapped for itself.

## Myself, preferences, properties and columns

`GET /rest/api/3/myself` adds `locale` and supports `expand=groups,applicationRoles`.

`/rest/api/3/mypreferences?key=` reads, stores (`PUT`, plain-text body of at most
255 characters) and deletes one of the caller's preferences. A missing key is
404. `/rest/api/3/mypreferences/locale` reads the locale and sets it from a
supported list; any other locale is 400. With no locale set, the browser's
`Accept-Language` decides.

`/rest/api/3/user/properties?accountId=` lists property keys, and
`/user/properties/{key}` reads, stores and removes a JSON value. `PUT` answers 201
for a new key and 200 for a replaced one. Only the person or an administrator may
use them.

`/rest/api/3/user/columns` reads the caller's issue table columns (`label` and
`value`), falling back to the site's navigator columns. `PUT` stores them from
form data (`columns=summary&columns=status`), and `DELETE` resets them.
Administrators may name another person with `accountId`.

## Application roles

`GET /rest/api/3/applicationrole` and `/applicationrole/{key}` are for
administrators. `jira-software` and `jira-servicedesk` report the groups granted
the product and `userCount`: the people with access directly or through one of
those groups.

## Avatars

Projects, issue types and priorities share one avatar store.

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

An upload needs `X-Atlassian-Token: no-check` and a JPEG, GIF or PNG body.
Issue type avatars uploaded through `/issuetype/{id}/avatar2` live in the same
store. Priority and issue type icons are served at `/static/img/` and
`/images/icons/priorities/`.

## Evidence

- `internal/api3/people.go`, `internal/api3/user_query.go`
- `internal/store/jira_people.go`, `jira_groups.go`, `universal_avatars.go`, `application_roles.go`
- `migrations/164_jira_people.sql`
- `internal/api3/people_test.go`
