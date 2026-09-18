# User-journey ledger

This ledger tracks complete user goals, persona by persona, and the browser specs (in `e2e/`) that prove them. A page or an API route on its own does not complete a journey. Product scope and API compatibility are covered in [CLOUD_PARITY.md](CLOUD_PARITY.md), and the remaining work in [PLAN.md](../PLAN.md).

**Legend.** ✅ the whole journey is tested in the browser. 🟡 a usable subset is tested, and gaps remain.

## Journeys

| Persona and goal | State | Browser evidence | Gaps |
|---|:-:|---|---|
| User signs in and orients | ✅ | `identity-providers`, `session-isolation`, `directories`, `v0` | — |
| Contributor finds work | ✅ | `v2`, `filters`, `directories` | — |
| Contributor creates and triages work | ✅ | `create`, `v1`, `v3`, `triage`, `issue_mentions`, `custom_field_options` | — |
| Contributor records how work was resolved | 🟡 | `resolution` (transition screen, work item page, REST) | No denied-user or accessibility pass over the transition screen |
| Contributor plans and runs a sprint | ✅ | `backlog`, `v4`, `projects` | Column configuration; swimlanes beyond assignee ([AGILE_BOARDS](AGILE_BOARDS.md#gaps)) |
| Contributor works offline | 🟡 | `v0`, `v5`, `revocation`, `session-isolation` | Only work items are available offline; other entities and richer edits need a connection |
| Contributor follows code through release | 🟡 | `directories` (development information), `software`, `releases` | Cross-project releases; release gates; environment promotion ([RELEASES](RELEASES.md#gaps)) |
| Manager configures a project | 🟡 | `projects`, `project_roles`, `permission_schemes`, `notification_schemes`, `issue_security_schemes` | Project templates and delegated administration do not cover every setting |
| Manager plans across teams | 🟡 | `timeline`, `plans_view`, `plans_teams` | Auto-scheduler; plan creation and configuration in the browser; rollups above epic; releases in plans; velocity-based capacity; plan views ([JIRA_SOFTWARE](JIRA_SOFTWARE.md#gaps)) |
| Agile coach diagnoses delivery | 🟡 | `backlog` (sprint report, velocity), `reports_flow`, `reports_progress`, `report_subscriptions`, `dashboard_reports` | Release burndown, epic burndown, workload and time tracking reports ([REPORTS](REPORTS.md#gaps)) |
| Engineering manager reviews delivery | 🟡 | `releases` (DORA metrics), `report_subscriptions` | Choosing which environments and incidents count toward DORA metrics ([REPORTS](REPORTS.md#gaps)) |
| Manager builds an operating dashboard | ✅ | `dashboards`, `dashboard_reports`, `dashboard_subscriptions`, `v6` | Forge gadgets ([DASHBOARDS](DASHBOARDS.md#gaps)) |
| Admin shapes the work item model | 🟡 | `issue_metadata` (work types and their schemes, priorities, resolutions), `custom_field_contexts`, `custom_field_options`, `field_configurations`, `screens`, `screen_schemes` | Per-language field translations ([ISSUE_METADATA](ISSUE_METADATA.md#gaps)) |
| Admin structures work above the epic | 🟡 | `hierarchy` (add a level, move a work type onto it, parent an epic under it) | Boards, backlogs, plans and reports stop at the epic level; no `hierarchyLevel` in JQL ([ISSUE_METADATA](ISSUE_METADATA.md#work-type-hierarchy)) |
| Admin designs and publishes a workflow | 🟡 | `directories` (workflow editor, statuses, workflow schemes, transition screens), `resolution` (a transition screen that asks for the resolution) | Workflow scheme administration is incomplete |
| Admin automates work | 🟡 | `automation` | Much of the trigger, condition and action catalog ([AUTOMATION](AUTOMATION.md)) |
| Service customer requests help | 🟡 | `service`, `service_assets` | Assets-backed form behavior such as AQL filters |
| Service agent works a queue | 🟡 | `service`, `service_assets` | Queues support only part of JQL |
| Service manager runs a service | 🟡 | `service`, `service_assets` | Public Assets import and API |
| Admin starts a service project | 🟡 | `service` | The rest of the service project lifecycle |
| Knowledge user authors and discusses a page | 🟡 | `wiki`, `wiki_content_tree`, `wiki_drafts_purge`, `wiki_page_details`, `wiki_page_lifecycle`, `wiki_mentions`, `wiki_watches` | Full editor; moving inline comments with edited passages ([CONFLUENCE_SITE_SURFACES](CONFLUENCE_SITE_SURFACES.md)) |
| Knowledge team collaborates live | ✅ | `wiki_live_editing`, `wiki_presence` | — |
| Knowledge user diagrams or models data | 🟡 | `wiki_database`, `wiki_whiteboard` | Direct manipulation, advanced whiteboard objects, rich embeds, exports |
| Space manager governs knowledge | 🟡 | `wiki_space_tools`, `wiki_page_lifecycle`, `classification_levels` | Space import |
| Site admin manages people, access and Jira settings | 🟡 | `admin`, `people`, `global_permissions`, `identity-providers`, `permission_schemes`, `permission_helper`, `notification_schemes`, `notification_helper`, `notification_preferences`, `notifications`, `issue_security_schemes` | Remaining enterprise identity journeys ([ADMIN](ADMIN.md)) |
| Site admin manages apps | 🟡 | `apps` | Several Connect module families; Forge compute; workflow modules ([APPS](APPS.md)) |

Evidence names refer to `e2e/<name>.spec.ts`. `wire-ids` and `accessibility` apply across all journeys; see [WIRE_IDS.md](WIRE_IDS.md) and [ACCESSIBILITY.md](ACCESSIBILITY.md).

## Quality gate for every journey

- The browser test starts where the persona starts and ends at the result they can see.
- API tests make the same state changes through the shared command path.
- Authorization is tested with one user who is allowed and one plausible user who is denied.
- The journey stays usable with keyboard only, and checks focus, accessible names, contrast, dark mode, reduced motion, and reflow at 320 px.
- Journeys that promise local-first behavior are tested offline and with two clients converging.
- Errors keep what the user entered and say which action fixes the problem.
- No enabled control is a placeholder.
