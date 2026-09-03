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
| Find work with JQL | ✅ | Project-scoped basic/JQL search, removable filter chips, starred saved filters, sortable/configurable columns, pagination, keyboard navigation, and contextual issue preview are browser-tested |
| Create work | 🟡 | Project, type, summary, description; not yet fully driven by createmeta and no create-another flow |
| Triage an issue | ✅ | Inline system/custom fields, labels, security, watchers, links, unified activity filters, attachments/worklogs, and their Jira-shaped API transitions are browser- and integration-tested |
| Run a board | 🟡 | Mouse and keyboard rank/status changes, live updates, local card filtering, bounded column scrolling; quick filters, swimlanes, WIP limits and card configuration remain |
| Work offline | ✅ | Issue reads, edit outbox, reconnect drain and server-wins reconciliation are browser-proven; mutation UI is protected from in-flight replica refreshes |
| Review a dashboard | 🟡 | Status counts, assigned work and recent activity; no configurable dashboards |
| Plan a backlog/sprint | ⛔ | Agile endpoints exist but there is no browser backlog, sprint planning, start/complete flow or epic/version panel |
| Administer a project | ⛔ | Some workflow, field, security and webhook APIs exist; project settings has no end-to-end UI |

## Delivery order

### P0 — daily Jira journeys

1. Navigator: ✅ delivered with basic/JQL switching, removable filter chips,
   saved-filter navigation, sortable/configurable columns, pagination,
   keyboard navigation, and side-panel preview.
2. Issue: ✅ delivered with inline editable system/custom fields, labels,
   security, watchers, links, unified Activity filters, and attachment/worklog
   management actions.
3. Create: use createmeta/editmeta as the only form schema; add assignee,
   priority, labels, security/custom fields, validation, and “create another”.
4. Backlog: backlog/selected/sprint grouping, ranking, sprint create/edit/start/
   complete, and issue detail preview, backed by the existing Agile command path.
5. Board: quick filters, assignee filter, WIP limit feedback, swimlanes, card
   configuration, and a detail drawer without losing board position.

### P1 — team and admin journeys

1. Replace the hard-coded `ZZ` shell routes with a workspace project switcher.
2. Add filter management and sharing/favourites UI over the delivered APIs.
3. Add notifications inbox and read state over `/rest/zzira/1/notifications`.
4. Add project settings for details, people/roles, work types, fields, workflows,
   issue security, boards, webhooks, and audit-friendly validation.
5. Close API matrix subsets for createmeta/editmeta, role semantics, permission
   schemes, screens/schemes, and missing Agile update/state operations before
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
