# User-journey ledger

This ledger tracks complete user goals, persona by persona, and the browser specs (in `e2e/`) that prove them. A page or an API route on its own does not complete a journey. Product scope and API compatibility are covered in [CLOUD_PARITY.md](CLOUD_PARITY.md), and the remaining work in [PLAN.md](../PLAN.md).

**Legend.** ✅ the whole journey is tested in the browser. 🟡 a usable subset is tested, and gaps remain.

## Journeys

| Persona and goal | State | Browser evidence | Gaps |
|---|:-:|---|---|
| User signs in and orients | ✅ | `identity-providers`, `session-isolation`, `directories`, `v0` | — |
| Contributor finds work | ✅ | `v2`, `filters`, `directories` | — |
| Contributor creates and triages work | ✅ | `create`, `v1`, `v3`, `triage`, `issue_mentions`, `custom_field_options` | — |
| Contributor changes many work items at once | 🟡 | `navigator_bulk` (six fields across a selection in one task, watch, unwatch), `v2` (delete, move, transition) | Custom fields are editable only through REST; work type and status through bulk move and transition ([BULK_ISSUES](BULK_ISSUES.md#gaps)) |
| Contributor records how work was resolved | ✅ | `resolution` (transition screen, work item page, REST, a reader who may not move the work, accessibility) | — |
| Contributor plans and runs a sprint | ✅ | `backlog`, `board_columns`, `board_swimlanes`, `v4`, `projects` | — |
| Contributor works offline | 🟡 | `v0`, `v5`, `revocation`, `session-isolation` | Only work items are available offline; other entities and richer edits need a connection |
| Contributor follows code through release | 🟡 | `directories` (development information), `software`, `releases`, `plans_setup` (cross-project releases) | The release hub does not group deployments across projects; release gates; environment promotion ([RELEASES](RELEASES.md#gaps)) |
| Manager configures a project | ✅ | `projects` (details, sender, features, properties, components), `project_workflow_admin` (what a project administrator owns, the configuration page, the project's own workflow), `project_roles`, `permission_schemes`, `notification_schemes`, `issue_security_schemes` | — |
| Manager plans across teams | 🟡 | `timeline`, `plans_view`, `plans_teams`, `plans_setup` (create, scheduling, sources, exclusions, cross-project releases, access) | Auto-scheduler; rollups above epic; velocity-based capacity; plan views ([JIRA_SOFTWARE](JIRA_SOFTWARE.md#gaps)) |
| Agile coach diagnoses delivery | ✅ | `backlog` (sprint report, velocity), `reports_flow`, `reports_progress`, `reports_burndown`, `reports_workload`, `reports_delivery` (cycle time), `report_subscriptions`, `dashboard_reports` | — |
| Engineering manager reviews delivery | ✅ | `releases` (DORA metrics), `dora_mapping` (the environments, pipelines, incidents and excluded periods that count), `reports_delivery` (deployment frequency, cycle time), `report_subscriptions` | — |
| Manager builds an operating dashboard | ✅ | `dashboards`, `dashboard_reports`, `dashboard_subscriptions`, `v6` | Forge gadgets ([DASHBOARDS](DASHBOARDS.md#gaps)) |
| Admin shapes the work item model | 🟡 | `issue_metadata` (work types and their schemes, priorities, resolutions), `custom_field_contexts`, `custom_field_options`, `field_translations`, `field_configurations`, `screens`, `screen_schemes` | Work types, priorities, resolutions and statuses are not translated ([ISSUE_METADATA](ISSUE_METADATA.md#gaps)) |
| Admin structures work above the epic | 🟡 | `hierarchy` (add a level, move a work type onto it, parent an epic under it, roll the roadmap up through it) | Reports summarise an epic and its children, not a level above it; boards and backlogs are epic-level, as Jira's are ([ISSUE_METADATA](ISSUE_METADATA.md#work-type-hierarchy)) |
| Admin designs and publishes a workflow | ✅ | `directories` (workflow editor, statuses, workflow schemes, transition screens), `scheme_copies` (copying a scheme before changing it), `resolution` (a transition screen that asks for the resolution) | — |
| Admin automates work | 🟡 | `automation` (schedules, work item events including assigned and attachment added, webhooks, branches, conditions, the action catalog) | Triggers for work deleted or moved, versions, sprints and Confluence; the rest of the catalog ([AUTOMATION](AUTOMATION.md#gaps)) |
| Service customer requests help | 🟡 | `service`, `service_assets` | Assets-backed form behavior such as AQL filters |
| Service agent works a queue | 🟡 | `service`, `service_assets` | A queue is written in the site's own JQL, which is missing only JSM's `Organizations` ([JQL](JQL.md#gaps)) |
| Service manager runs a service | 🟡 | `service`, `service_assets` | Public Assets import and API |
| Admin starts a service project | 🟡 | `service` | The rest of the service project lifecycle |
| Knowledge user authors and discusses a page | 🟡 | `wiki`, `wiki_content_tree`, `wiki_drafts_purge`, `wiki_page_details`, `wiki_page_lifecycle`, `wiki_mentions`, `wiki_watches` | Full editor; moving inline comments with edited passages ([CONFLUENCE_SITE_SURFACES](CONFLUENCE_SITE_SURFACES.md)) |
| Knowledge team collaborates live | ✅ | `wiki_live_editing`, `wiki_presence` | — |
| Knowledge user diagrams or models data | 🟡 | `wiki_database`, `wiki_whiteboard` | Direct manipulation, advanced whiteboard objects, rich embeds, exports |
| Space manager governs knowledge | 🟡 | `wiki_space_admin` (details, custom roles, direct grants), `wiki_space_tools`, `wiki_page_lifecycle`, `classification_levels` | Space import |
| Site admin manages people, access and Jira settings | 🟡 | `admin`, `password`, `two_step`, `api_tokens`, `people`, `global_permissions`, `identity-providers`, `permission_schemes`, `permission_helper`, `notification_schemes`, `notification_helper`, `notification_preferences`, `notifications`, `issue_security_schemes` | SAML; two-step verification is an authenticator app with a typed key rather than a QR image; a forgotten password needs an administrator's sign-in link rather than a self-service page; authentication policies cover named people rather than groups; SCIM provisioning has no browser page of its own ([ADMIN](ADMIN.md), [SCIM](SCIM.md)) |
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
