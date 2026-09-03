# Jira UI, API, and Journey Parity

Updated: 2026-09-03

This ledger defines “full parity” for ZZIRA as the daily Jira Software core:
planning, finding, creating, updating, and administering software work through
both the browser and Jira-compatible APIs. Jira Service Management, Marketplace
apps, proprietary gadgets, and Atlassian AI are separate product surfaces and
are outside this boundary unless the product scope changes explicitly.

Status: ✅ journey is complete and browser/API tested · 🟡 usable subset · ⛔ gap.
The endpoint-level source of truth remains [the API matrix](../api/conformance/MATRIX.md).

## Current foundation

| Journey | Status | Evidence / gap |
|---|---:|---|
| Sign in and orient | ✅ | Responsive sidebar-first shell, account menu, dark mode, visible local-replica state |
| Browse projects and people | ✅ | Workspace project directory and overview, project tabs, people directory, self profile, and security-filtered assigned/reported work are browser-tested |
| Find work with JQL | ✅ | Project-scoped basic/JQL search, removable filter chips, starred saved filters, sortable/configurable columns, pagination, keyboard navigation, and contextual issue preview are browser-tested |
| Create work | ✅ | Shared createmeta schema drives project/type plus assignee, priority, labels, security and typed custom fields; validation recovery and create-another are browser/API tested |
| Triage an issue | ✅ | Inline system/custom fields, labels, security, watchers, links, unified activity filters, attachments/worklogs, and their Jira-shaped API transitions are browser- and integration-tested |
| Run a board | 🟡 | Mouse and keyboard rank/status changes, live updates, local card filtering, bounded column scrolling; quick filters, swimlanes, WIP limits and card configuration remain |
| Work offline | ✅ | Issue reads, edit outbox, reconnect drain and server-wins reconciliation are browser-proven; mutation UI is protected from in-flight replica refreshes |
| Review a dashboard | 🟡 | Status counts, assigned work and recent activity; no configurable dashboards |
| Plan a backlog/sprint | ✅ | Project backlog and open-sprint grouping, cross-group movement, rank controls, issue preview, sprint create/edit/start/complete, and equivalent Agile API lifecycle are browser/integration-tested; epic/version planning is a separate enhancement |
| Administer workflows | 🟡 | Workflow directory, read-only built-in diagram, custom workflow creation, transition add/remove, and project assignment are browser-tested; status creation and full schemes/conditions remain |
| Administer a project | ⛔ | The workflow assignment slice is delivered; details, roles, fields, security, boards, and webhooks still lack a coherent settings journey |

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
5. Board: quick filters, assignee filter, WIP limit feedback, swimlanes, card
   configuration, and a detail drawer without losing board position.

### P1 — team and admin journeys

1. ✅ Add project and people directories, project overviews/tabs, and self/other
   user profiles. Next, replace the hard-coded recent `ZZ` shell routes with a
   workspace project switcher.
2. Add filter management and sharing/favourites UI over the delivered APIs.
3. Add notifications inbox and read state over `/rest/zzira/1/notifications`.
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
- <https://support.atlassian.com/jira-software-cloud/docs/use-your-kanban-backlog/>
- <https://support.atlassian.com/jira-software-cloud/docs/create-a-work-item-and-a-subtask/>
- <https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/>
