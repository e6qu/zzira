# Confluence groups

Updated: 2026-09-11

A group gathers people so permissions and mentions can name many at once.
Confluence reads and writes them through the wiki surface; the groups
themselves are the directory groups the organization already has, so a group
made here is the same group the organization administration sees.

## Jira Cloud REST surface

All eight pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /wiki/rest/api/group` | Lists the groups. |
| `POST /wiki/rest/api/group` | Creates one, for an administrator. |
| `GET /wiki/rest/api/group/by-id` | Reads one by id. |
| `DELETE /wiki/rest/api/group/by-id` | Removes one, for an administrator. |
| `GET /wiki/rest/api/group/picker` | Searches by partial name. |
| `GET /wiki/rest/api/group/{groupId}/membersByGroupId` | The people in a group. |
| `POST /wiki/rest/api/group/userByGroupId` | Adds a person, for an administrator. |
| `DELETE /wiki/rest/api/group/userByGroupId` | Removes a person, for an administrator. |

## Reading is open, changing is not

Any workspace member may see what groups exist and who is in them, because that
is how a person decides who to mention or grant access to. Creating a group,
deleting one, and moving people in or out are administration.

## The two surfaces describe one membership

`GET /user/memberof` and `GET /group/{id}/membersByGroupId` are the same fact
read from either end. The test checks both after a single write, because two
reads of one membership that can disagree is worse than either one missing.

## Counting is opt-in

Confluence's list does not report a total; the picker reports one only when
`shouldReturnTotalSize=true`. Counting is work a caller should ask for, so it
is not done otherwise, and the test asserts the absence as well as the presence.

## Adding someone twice is not an error

The request asks for the person to be in the group, and afterwards they are.
Failing would make a client track what it had already done to avoid an error
that describes no problem. Removing someone who is not a member **is** a 404,
because there is nothing to remove.

## Evidence and current boundary

- `internal/confluence/groups_test.go` covers all eight operations, that reading
  is open to a member while every change needs administration, that someone
  outside the workspace is refused, the duplicate and empty name, the unknown
  group and unknown person, that adding twice succeeds while removing twice does
  not, that `memberof` and the member list agree after one write, that counting
  happens only when asked, and that memberships do not outlive the group.
- No migration: the `groups` and `group_members` tables already existed.

Confluence's `accessType` filter on the list, `expand` on the member read,
cursor paging, and groups in the browser journeys remain.
