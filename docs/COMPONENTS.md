# Project components

Updated: 2026-09-09

ZZIRA stores Jira project components as project-scoped records with stable
numeric IDs, names, descriptions, optional active workspace leads, and Jira's
project-default, project-lead, component-lead, or unassigned default-assignee
mode. Names are unique within a project without regard to case.

The eight pinned Jira Cloud Platform component operations support global and
project collections, name search and ordering, offset pagination, create,
read, partial update, deletion, move-on-delete, and related-work-item counts.
Responses include the effective assignee and assignment validity. All writes
require workspace administration and emit both an ordered product action and
an organization audit event when the workspace belongs to a site.

The `components` issue field accepts Jira arrays of component IDs or names on
create and update. The command path validates project ownership, removes
duplicates, and stores canonical snapshots. Component renames refresh every
assigned work item in the same transaction. Deleting a component can remove it
from assigned work or replace it with another component in the same project;
both paths emit updated issue snapshots for sync clients.

Create and edit metadata expose the same project component choices to REST and
the browser create dialog. When the caller leaves the assignee at its default,
the first selected component decides the assignee before the project default is
considered. Search supports singular and plural component field aliases,
multi-value empty/negation behavior, and
`component in componentsLeadByUser([user])` against the canonical lead.

Project administrators manage components on the project settings page. The
browser journey creates a component, assigns its lead and default, edits it,
checks the Jira REST representation, and deletes it. The REST integration test
also proves canonical issue values, component-led assignment, paging, counts,
JQL, rename propagation, move-on-delete, and removal.

Current gaps are exact per-project Browse Projects and Administer Projects
permission-scheme enforcement, anonymous reads, Compass component sources,
archived/deleted Compass representations, and their edge-specific errors.
