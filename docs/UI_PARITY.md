# Jira UI, API, and Journey Parity

Updated: 2026-09-05

The target is now the whole Jira Cloud surface plus wiki/Confluence, diagrams,
reports, metrics, releases, custom dashboards, scheduled automation and apps.
[The Cloud parity ledger](CLOUD_PARITY.md) defines the expanded scope and known
compatibility gaps. The completed journeys below describe individual slices,
not full product fidelity.

Status: ✅ journey is complete and browser/API tested · 🟡 usable subset · ⛔ gap.
The endpoint-level source of truth remains [the API matrix](../api/conformance/MATRIX.md).

## Current foundation

| Journey | Status | Evidence / gap |
|---|---:|---|
| Sign in and orient | ✅ | Responsive sidebar-first shell, account and notification controls, dark mode, visible local-replica state |
| Browse projects and people | ✅ | Workspace project directory and overview, project tabs, people directory, self profile, and security-filtered assigned/reported work are browser-tested |
| Find work with JQL | ✅ | Project-scoped basic/JQL search, removable filter chips, starred saved filters, sortable/configurable columns, pagination, keyboard navigation, and contextual issue preview are browser-tested |
| Create work | ✅ | Shared createmeta schema drives project/type plus assignee, priority, labels, security and typed custom fields; validation recovery and create-another are browser/API tested |
| Triage an issue | ✅ | Inline system/custom fields, labels, security, watchers, links, unified activity filters, attachments/worklogs, and their Jira-shaped API transitions are browser- and integration-tested |
| Run a board | ✅ | Mouse/keyboard ranking, live updates, text/quick/assignee filters, configurable assignee swimlanes, WIP-limit feedback, card fields, and in-place issue preview are browser/API tested |
| Work offline | ✅ | Issue reads, edit outbox, reconnect drain and server-wins reconciliation are browser-proven; mutation UI is protected from in-flight replica refreshes |
| Review a dashboard | 🟡 | Fixed work overview plus configurable private/shared dashboards, favourites, layouts, JQL/filter gadgets, statistics, accessible pie charts and refresh; advanced reports and third-party gadgets remain |
| Plan a backlog/sprint | ✅ | Project backlog and open-sprint grouping, cross-group movement, rank controls, issue preview, sprint create/edit/start/complete, and equivalent Agile API lifecycle are browser/integration-tested; epic/version planning is a separate enhancement |
| Administer workflows | 🟡 | Workflow directory, read-only built-in diagram, custom workflow creation, transition add/remove, and project assignment are browser-tested; status creation and full schemes/conditions remain |
| Administer a project | 🟡 | Create Scrum/Kanban projects, edit details/lead/default assignment, and navigate to board/workflow settings; roles, schemes, fields, security and lifecycle remain |
| Administer scheduled automation | 🟡 | Admin-only rule directory/editor, schedule → JQL scope → action flow, enable/disable, run-now and detailed audit history; Cron, event triggers, conditions, branches, smart values and most Jira actions remain |
| Wiki | 🟡 | Space/page creation, storage editor, drafts, version checks/history, parent pages, search and trash/restore; see CLOUD_PARITY.md for API and permission limits |

## Delivery order

### P0 — daily Jira journeys

1. Navigator: ✅ delivered with basic/JQL switching, removable filter chips,
   saved-filter navigation, sortable/configurable columns, pagination,
   keyboard navigation, and side-panel preview.
2. Issue: ✅ delivered with inline editable system/custom fields, labels,
   security, watchers, links, unified Activity filters, and attachment/worklog
   management actions.
3. Create: ✅ delivered from a shared createmeta schema with project/type,
   assignee, priority, labels, security and typed custom fields, preserved
   validation state, and a repeatable “create another” loop.
4. Backlog: ✅ delivered with backlog/open-sprint grouping, ranking, sprint
   create/edit/start/complete, issue detail preview, and one shared Agile
   command path across the UI and REST API.
5. Board: ✅ delivered with shared quick filters, assignee filtering, WIP-limit
   feedback based on unfiltered totals, assignee swimlanes, configurable card
   fields, and issue preview without losing board position.

### P1 — team and admin journeys

1. ✅ Add project and people directories, project overviews/tabs, self/other
   user profiles, and an accessible workspace project switcher. Search, create,
   overview, backlog, board, and all-work links now follow the current project,
   and project context persists on workspace-level pages.
2. Add filter management and sharing/favourites UI over the delivered APIs.
3. ✅ Add notifications inbox and synchronized read state over
   `/rest/zzira/1/notifications`, including private item/all-read mutations,
   unread navigation badges, and an all/unread browser journey.
4. 🟡 Workflow browse/edit/project-assignment UI is delivered. Add the remaining
   project settings for details, people/roles, work types, fields, issue security,
   boards, webhooks, and audit-friendly validation.
5. Close remaining API matrix subsets for editmeta, role semantics, permission
   schemes, screens/schemes, and remaining Agile operations before
   labeling the Jira Software API boundary conformant.

## Quality gate for every parity slice

- One browser test covers the complete user journey and one API test covers the
  equivalent state transition through the shared command core.
- The server renderer and WASM renderer produce the same fragment golden.
- Keyboard use, visible focus, WCAG 2.2 AA, responsive layout, dark mode, reduced
  motion, offline behavior, and two-client convergence remain green.
- Unsupported Jira behavior returns an explicit Jira-shaped error; the UI does
  not expose controls backed by placeholders or silent fallbacks.

## Reference behavior

The information architecture follows Atlassian’s current sidebar navigation and
space tabs; lists remain scan/sort-oriented; issue activity groups comments,
history and worklogs; boards remain status-column workflows with direct card
movement. Primary references:

- <https://support.atlassian.com/jira-software-cloud/docs/what-is-the-new-navigation-in-jira/>
- <https://support.atlassian.com/jira-software-cloud/docs/what-is-the-list-view/>
- <https://support.atlassian.com/jira-software-cloud/docs/change-the-view-of-search-results/>
- <https://support.atlassian.com/jira-software-cloud/docs/save-your-search-as-a-filter/>
- <https://support.atlassian.com/jira-software-cloud/docs/what-are-the-different-types-of-activity-on-an-issue/>
- <https://support.atlassian.com/jira-software-cloud/docs/monitor-work-in-a-kanban-project/>
- <https://support.atlassian.com/jira-software-cloud/docs/customize-your-view-of-the-board-and-backlog/>
- <https://developer.atlassian.com/cloud/jira/software/rest/api-group-board/>
- <https://support.atlassian.com/jira-software-cloud/docs/use-your-kanban-backlog/>
- <https://support.atlassian.com/jira-software-cloud/docs/create-a-work-item-and-a-subtask/>
- <https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/>

## Release journey follow-up

Project releases now cover creation, editing, scope assignment, progress, notes,
release/unrelease, archive/unarchive and confirmed deletion. Fix/affects version
pickers are present in issue creation, and issue pages link to their versions.
The browser journey covers light/dark accessibility and 320px reflow.
[RELEASES.md](RELEASES.md) records remaining API, offline and permission limits.
