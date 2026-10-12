# User-journey ledger

This ledger tracks complete user goals, persona by persona, and the browser specs (in `e2e/`) that prove them. A page or an API route on its own does not complete a journey. Product scope and API compatibility are covered in [CLOUD_PARITY.md](CLOUD_PARITY.md), and the remaining work in [PLAN.md](../PLAN.md).

**Legend.** ✅ the whole journey is tested in the browser. 🟡 a usable subset is tested, and gaps remain.

## Journeys

| Persona and goal | State | Browser evidence | Gaps |
|---|:-:|---|---|
| User signs in and orients | ✅ | `identity-providers`, `session-isolation`, `directories`, `v0`, `mobile_theme` | — |
| Signed-out visitor reads public work | 🟡 | `anonymous_browse`, `anonymous_collections` | Browser filters and custom-field/worklog/history panels require signing in ([ANONYMOUS_ACCESS](ANONYMOUS_ACCESS.md#public-work-item-pages)) |
| Contributor finds work | ✅ | `v2`, `filters`, `directories`, `issue_preview_recovery` | — |
| Contributor creates and triages work | ✅ | `environment`, `reporter_optional_create`, `create`, `reporter_edit`, `v1`, `v3`, `issue_editor_loading`, `triage`, `issue_disclosure_sync`, `issue_mentions`, `mention_composition`, `custom_field_options`, `edit_dialog_fields`, `transition_screens`, `empty_screens`, `issue_choice_fields`, `custom_field_screens` | — |
| Contributor changes many work items at once | ✅ | `navigator_bulk`, `v2` | Work type and status are changed through bulk move and bulk transition, as Jira does ([BULK_ISSUES](BULK_ISSUES.md#in-the-navigator)) |
| Contributor records when work happened | ✅ | `worklog_started`, `v3` | Worklog group/role restrictions ([WORKLOGS](WORKLOGS.md#gaps)) |
| Contributor records how work was resolved | ✅ | `resolution` | — |
| Contributor plans and runs a sprint | ✅ | `backlog`, `board_columns`, `board_swimlanes`, `v4`, `projects` | — |
| Contributor works offline | 🟡 | `v0`, `v5`, `revocation`, `session-isolation`, `replica_capacity`, `replica_pool_isolation` | Only work items are available offline; other entities and richer edits need a connection |
| Contributor follows code through release | ✅ | `directories`, `software`, `releases`, `plans_setup` | Promotion itself is the delivery provider's, as in Jira; a provider asks the site before it deploys through deployment gating ([RELEASES](RELEASES.md#environments), [SERVICE_MANAGEMENT](SERVICE_MANAGEMENT.md)) |
| Manager configures a project | ✅ | `projects`, `project_workflow_admin`, `project_roles`, `permission_schemes`, `notification_schemes`, `issue_security_schemes` | — |
| Manager plans across teams | ✅ | `timeline`, `plans_view`, `plans_custom_fields`, `plans_lifecycle`, `plans_duplicate`, `plans_inferred_dates`, `plans_date_fields`, `plans_teams`, `plans_setup`, `plans_release_schedule` | Estimated-work release constraints, flexible dates, remaining custom field types and broader scenario edits ([JIRA_SOFTWARE](JIRA_SOFTWARE.md#gaps)) |
| Agile coach diagnoses delivery | ✅ | `backlog`, `reports_flow`, `reports_progress`, `reports_burndown`, `reports_workload`, `reports_delivery`, `report_subscriptions`, `dashboard_reports` | — |
| Engineering manager reviews delivery | ✅ | `releases`, `dora_mapping`, `reports_delivery`, `dashboard_reports`, `report_subscriptions` | — |
| Manager builds an operating dashboard | ✅ | `dashboards`, `shared_items`, `dashboard_refresh_recovery`, `wallboard_controls`, `dashboard_reports`, `dashboard_subscriptions`, `v6` | Forge gadgets ([DASHBOARDS](DASHBOARDS.md#gaps)) |
| Admin shapes the work item model | ✅ | `issue_metadata`, `issue_linking`, `work_type_alternatives`, `custom_field_contexts`, `custom_field_options`, `field_translations`, `issue_metadata_translations`, `field_renderers`, `field_configurations`, `screens`, `screen_schemes` | Work type scoping, priority update tasks and avatar catalogue ([ISSUE_METADATA](ISSUE_METADATA.md#gaps)) |
| Admin structures work above the epic | ✅ | `hierarchy` | Boards and backlogs are epic-level, as Jira's are ([ISSUE_METADATA](ISSUE_METADATA.md#work-type-hierarchy)) |
| Admin designs and publishes a workflow | ✅ | `directories`, `workflow_layout_recovery`, `scheme_copies`, `resolution`, `project_workflow_admin` | — |
| Admin automates work | 🟡 | `automation` | Confluence actions beyond writing, commenting on, labelling, appending to and archiving a page; the rest of the catalog, which now includes watchers, cloning and editing the assignee, resolution and estimates ([AUTOMATION](AUTOMATION.md#gaps)) |
| Service customer requests help | ✅ | `service`, `service_assets`, `service_request_sharing`, `service_form_recovery` | Refused drafts retain answers and sharing; cascading details follow their parent. AQL reads references, the object type hierarchy and `ORDER BY` ([SERVICE_MANAGEMENT](SERVICE_MANAGEMENT.md#assets-filters)) |
| Service agent works a queue | ✅ | `service`, `service_assets` | A queue is written in the site's own JQL, including JSM's `Organizations`, which reads the organization the customer shared the request with |
| Service manager runs a service | ✅ | `service`, `service_assets` | — |
| Admin starts a service project | ✅ | `service` | — |
| Knowledge user authors and discusses a page | 🟡 | `wiki`, `wiki_content_tree`, `wiki_drafts_purge`, `wiki_editor`, `wiki_page_details`, `wiki_page_lifecycle`, `wiki_mentions`, `mention_composition`, `wiki_watches` | The editor writes headings, quotes, code, links, lists, tables, panels, statuses, a table of contents and column layouts; macros the site does not draw are still written as storage ([PAGE_WRITING](PAGE_WRITING.md)) |
| Knowledge team collaborates live | ✅ | `wiki_live_editing`, `wiki_presence`, `wiki_draft_recovery`, `wiki_live_recovery` | Early unsynced typing survives reopening and damaged neighboring draft entries; recovery stays with the signed-in writer |
| Knowledge user diagrams or models data | 🟡 | `wiki_database`, `wiki_whiteboard` | Advanced whiteboard objects, rich embeds, exports ([CONFLUENCE_SITE_SURFACES](CONFLUENCE_SITE_SURFACES.md)) |
| Space manager governs knowledge | ✅ | `wiki_space_admin`, `wiki_space_tools`, `wiki_page_lifecycle`, `classification_levels` | An imported space is read from this site's own export, not from Confluence XML or a document ([CONFLUENCE_SPACES](CONFLUENCE_SPACES.md#gaps)) |
| Site admin manages people, access and Jira settings | ✅ | `admin`, `password`, `two_step`, `api_tokens`, `people`, `global_permissions`, `identity-providers`, `saml`, `passkeys`, `passkey_recovery`, `permission_schemes`, `permission_helper`, `notification_schemes`, `notification_helper`, `notification_preferences`, `notifications`, `issue_security_schemes` | A SCIM provider is pointed at the address `/admin` shows and holds a key issued there, as Atlassian's own is ([ADMIN](ADMIN.md), [SCIM](SCIM.md)) |
| Site admin manages apps | 🟡 | `apps` | Dialogs, web sections and keyboard shortcuts; Forge compute; workflow modules ([APPS](APPS.md)) |

Evidence names refer to `e2e/<name>.spec.ts`. `wire-ids`, `accessibility` and `ux_recovery` apply across journeys; see [WIRE_IDS.md](WIRE_IDS.md) and [ACCESSIBILITY.md](ACCESSIBILITY.md). Recovery checks cover visible form errors without losing drafts, dialog keyboard isolation and loading cancellation, blocked browser storage, and board column alignment.

## Quality gate for every journey

- The browser test starts where the persona starts and ends at the result they can see.
- API tests make the same state changes through the shared command path.
- Authorization is tested with one user who is allowed and one plausible user who is denied.
- The journey stays usable with keyboard only, and checks focus, accessible names, contrast, dark mode, reduced motion, and reflow at 320 px.
- Journeys that promise local-first behavior are tested offline and with two clients converging.
- Errors keep what the user entered and say which action fixes the problem.
- No enabled control is a placeholder.
