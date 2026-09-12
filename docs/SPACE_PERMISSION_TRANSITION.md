# Moving a site from space permission grants to roles

Updated: 2026-09-12

A site that grants space permissions one at a time can be moved to roles. These
five operations are how Confluence does it, and they only became implementable
once both models existed — [space permissions](SPACE_PERMISSIONS.md) added the
grants that these migrate away from.

## Three steps, because the decision is a person's

**Find what people actually hold.** A scan reads every direct grant and groups
them into *combinations*: distinct sets of permissions. A site with a thousand
grants usually has a handful of real combinations, and that is what makes the
decision tractable.

**Decide once per combination.** An administrator says, for each combination
and each kind of principal holding it, which role it becomes — or that the
access goes away.

**Apply it.** The decisions are carried out in the background, because a site
can have a great many spaces.

Separating them is the point: a migration nobody looked at first is how a site
loses access to its own content.

## Jira Cloud REST surface

All five pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `POST /wiki/api/v2/space-permissions/transition/combinations` | Queues the scan. |
| `GET /wiki/api/v2/space-permissions/transition/combinations` | The combinations the scan found. |
| `POST /wiki/api/v2/space-permissions/transition/role-assignments` | Queues the decisions. |
| `POST /wiki/api/v2/space-permissions/transition/access-removals` | Queues removing access for whole combinations. |
| `GET /wiki/api/v2/space-permissions/transition/tasks/{taskId}` | Reports one transition task. |

## A combination is named by its contents

The id is a hash of the sorted permission set, so the same set found again keeps
the same id and an administrator's half-finished decision is not invalidated by
a rescan. The name is workspace-independent, so the workspace is part of the
key rather than the id.

A combination whose permissions already match a space role is left out of the
listing: it has somewhere to go already, and offering it would be asking a
question that is answered.

## Counting

One person holding the same set in three spaces is **one principal in three
spaces**, not three principals. The space count is what says how widely a
combination is held; the principal count says how many people or groups are
affected. Conflating them would overstate both.

## What the apply step does

For each subject whose grants match a combination, in the selected spaces: the
chosen role is assigned, and **the direct grants go**. The whole point is to
leave the site governed by roles rather than by both at once — grants left
behind would keep answering permission checks, and the migration would not have
migrated anything.

A principal type the administrator said nothing about keeps what it has.
Silently changing it would be a decision nobody made.

Access removal drops the grants and assigns nothing.

## Space selection

`ALL`, `SPECIFIC` and `ALL_EXCEPT_SPECIFIC` are honored, and a selected space
may be named by id or key. `PERSONAL` and `ALL_EXCEPT_PERSONAL` are **refused**:
this product has no personal spaces — a space belongs to the workspace, not to a
person — and treating some other space as personal would quietly migrate the
wrong ones.

## Evidence and current boundary

- `internal/confluence/permission_transition_test.go` covers all five
  operations end to end: two combinations told apart by their permission sets,
  the principal and space counts, that the transition is administration, every
  refusal including both personal-space selections, that assigning a role in
  one selected space leaves another space untouched, that the transitioned
  subject's grants go while an untransitioned subject in the same space keeps
  theirs, and that a removal drops grants without assigning a role.
- `migrations/150_space_permission_transition.sql` is exercised from a clean
  PostgreSQL schema.

Cursor paging on the combination listing, the `GUEST`, `ANONYMOUS`, `APP` and
user-class principal types, and reporting progress part-way through a large
apply remain.
