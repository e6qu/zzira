# Releases

The release hub plans a project's versions, tracks their scope and delivery evidence, and produces release notes. This page covers the browser journey and how releases behave. The version REST API is in [PROJECT_VERSIONS.md](PROJECT_VERSIONS.md). This page is part of [Jira Software](JIRA_SOFTWARE.md); see [CLOUD_PARITY.md](CLOUD_PARITY.md) for status.

## UI

| Page | Purpose |
|---|---|
| `/projects/{key}/releases` | Lists the project's versions with status filters. On the unfiltered list, project administrators can create versions and move them up or down. |
| `/projects/{key}/releases/{version}` | One version: its details, scope, progress, delivery evidence, driver, approvals and release notes. |

**On the list**
- Every workspace member sees the release hub in project navigation.
- Moving a version up or down uses buttons, not dragging; the buttons work from the keyboard and without scripts. They appear only on the unfiltered list, because a version moves within the project's whole order.

**On a version's page**
- **Project administrators** can:
  - edit the name, description and dates;
  - release, unrelease, archive, unarchive and delete the version (delete asks for confirmation);
  - set the driver and add or remove approvers.
- **Members** can add or remove work items they can see, which writes `fixVersions` through the ordinary edit command and so needs **Edit issues** and **Resolve issues** in the work item's project.
- **Approvers** approve or decline, optionally giving a reason.

## Behavior

**Permissions**
- Managing versions needs the project's **Administer projects** permission from its permission scheme ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)), so project roles and groups that hold it can manage versions.
- Putting a work item in a version, or taking it out, needs Edit issues and Resolve issues on that work item.
- Counts, scope lists and release notes include only work items the viewer can see.
- A version change can update restricted work items, but those updates are hidden from members who cannot read them.

**Scope and progress**
- The page shows to do, in progress and done counts by status category, as Jira does.
- *Unresolved* means the resolution is empty. This is what `unresolvedIssueCount` counts and what releasing a version moves.
- A work item that reaches a done status without a chosen resolution takes the site's default; leaving the done category clears it. A resolution set on a transition screen or by an edit wins over the default. See [ISSUE_METADATA.md](ISSUE_METADATA.md#on-work-items).

**Releasing**
- A release can move the version's unresolved work items to another version in the same project. On the page this is a choice in the release form; through the API it is `moveUnfixedIssuesTo` on a version update.
- Resolved work items stay with the released version.
- The move happens only at the moment of release.
- `moveUnfixedIssuesTo` is refused when creating a version.

**Deleting and merging**
- Deleting clears version references and keeps the work items.
- `removeAndSwap` and `mergeto` can move fix and affects references to another version in the same project.
- If a request is invalid, the whole operation is rolled back.

**Release notes**
- Notes group the version's work items by work type.
- The viewer chooses which work types to include and a format:
  - styled, shown on the page;
  - plain text: `** Type` headings over `* [KEY] - Summary` lines, as in Jira;
  - Markdown.

**Delivery evidence**
- Builds and deployments linked to the version's visible work items are rolled up on the page: pipeline, environment and latest outcome.
- The data comes from the [Jira Software DevOps APIs](JIRA_SOFTWARE.md#development-and-devops-data).

**Work item fields**
- `fields.fixVersions` and `fields.versions` accept ids or names on create and update.
- Updates also accept `update` set, add and remove operations.
- The create dialog has both pickers, and work item pages link to the assigned versions.
- JQL `fixVersion` and `affectedVersion` accept an id or a name with `=`, `!=`, `IN`, `IS EMPTY` and `IS NOT EMPTY`. See [JQL.md](JQL.md).

**Consistency**
- Version changes and work item version writes lock the project row, so they happen one at a time.
- Replacing or clearing references, the version state, work item snapshots and action log entries are committed together.
- Renaming, releasing or archiving a version refreshes the snapshots of the affected work items, and replicas receive those updates.
- Concurrent additions to different versions do not overwrite each other.

**Dates**
- Dates are ISO dates.
- "Overdue" is calculated in UTC.
- An empty string clears a date; `null` means the date was not sent.

**Project ids**
- Projects with legacy non-numeric ids leave out `projectId` in version responses. To create a version in one of these projects, send the project key in `project`.

## Gaps

- A version still belongs to exactly one project (`project_versions.project_id`); a plan's cross-project release groups such versions ([Jira Software](JIRA_SOFTWARE.md#plans)). The hub and the version page read the group back from the plans the viewer may see: the list notes what a version ships with, and the version page lists the other projects' versions with their dates, progress and release state, and says when they are not all due on the same day.
- No grouping of deployments across projects, no configurable release gates, and no environment promotion policies.
- Dates are shown in fixed English format; the user's locale and the site time zone are not applied.
- The release hub needs a connection. Versions are not stored in the offline replica.
- Versions are paged by offset, so pages can shift when versions change between requests.
- No drag-and-drop reordering.

Remaining work is tracked in [PLAN.md](../PLAN.md).

## See also

- [REPORTS.md](REPORTS.md): the version report and the DORA report.
- Code: `internal/web/releases.go`, `internal/web/release_notes.go`, `internal/store/versions.go`, `internal/store/version_approvers.go`, `internal/api3/versions.go`.
- Browser test: `e2e/releases.spec.ts`.
