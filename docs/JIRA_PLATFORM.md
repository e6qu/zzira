# Jira platform

The Jira platform is the layer Jira Software and Jira Service Management share: projects, work items and their fields, workflows, schemes, search, people and permissions, plus the platform APIs apps and administrators use. This page indexes the platform docs and documents the platform operations that have no doc of their own: app properties, UI modifications, webhooks, workflow history, app transition rules, data policy, licensing and audit, site reads, plans and teams, project templates, Connect migration, the service registry and app field values. For status against Jira Cloud, see [CLOUD_PARITY.md](CLOUD_PARITY.md). Products built on the platform: [Jira Software](JIRA_SOFTWARE.md), [Jira Service Management](SERVICE_MANAGEMENT.md). Administration: [ADMIN.md](ADMIN.md).

## Platform docs

| Area | Docs |
| --- | --- |
| Work items | [Work item surface](ISSUE_SURFACE.md) (comments, links, watchers, changelogs, archiving) · [Bulk operations](BULK_ISSUES.md) · [Attachments](ATTACHMENTS.md) · [Time tracking](TIME_TRACKING.md) · [Worklogs](WORKLOGS.md) |
| Fields | [Fields](ISSUE_FIELDS.md) · [Work types, priorities, resolutions](ISSUE_METADATA.md) · [Custom field contexts](CUSTOM_FIELD_CONTEXTS.md) · [Custom field options](CUSTOM_FIELD_OPTIONS.md) · [App field options](APP_FIELD_OPTIONS.md) · [Field configurations](FIELD_CONFIGURATIONS.md) · [Field association schemes](FIELD_ASSOCIATION_SCHEMES.md) · [Screens](SCREENS.md) · [Screen schemes](SCREEN_SCHEMES.md) |
| Workflows | [Workflow rules](WORKFLOW_RULES.md) · [Workflow schemes](WORKFLOW_SCHEMES.md) · [Workflow history](#workflow-history) |
| Search | [JQL and search](JQL.md) · [Saved filters](FILTERS.md) · [Dashboards API](DASHBOARDS_API.md) · [Dashboards UI](DASHBOARDS.md) |
| Projects | [Components](COMPONENTS.md) · [Versions](PROJECT_VERSIONS.md) · [Releases](RELEASES.md) · [Governance](PROJECT_GOVERNANCE.md) (categories, properties, features, sender email) · [Lifecycle](PROJECT_LIFECYCLE.md) (archive, trash, restore) · [Project roles](PROJECT_ROLES.md) |
| Access | [Permission schemes](PERMISSION_SCHEMES.md) · [Issue security schemes](ISSUE_SECURITY_SCHEMES.md) · [Anonymous access](ANONYMOUS_ACCESS.md) · [Classification levels](CLASSIFICATION_LEVELS.md) |
| Notifications | [Notification schemes](NOTIFICATION_SCHEMES.md) |
| People | [People, groups and avatars](PEOPLE.md) · [Identity provider sign-in](shauth-sso.md) |
| Site | [Jira site configuration](JIRA_SITE_CONFIGURATION.md) · [Ids clients see](WIRE_IDS.md) · [Organization and site administration](ADMIN.md) |
| Extensibility | [Apps](APPS.md) · [Automation](AUTOMATION.md) |

Operations marked *app* accept only a request authenticated as an installed app (a Connect JWT or ZZIRA's Forge app token); a user's token is refused, as in Jira.

## App properties

| Operation | Behavior |
| --- | --- |
| `GET /rest/atlassian-connect/1/addons/{addonKey}/properties` | *app* The calling app's property keys with `self` links. Another app's key is 401. |
| `GET/PUT/DELETE /rest/atlassian-connect/1/addons/{addonKey}/properties/{propertyKey}` | *app* Reads `{key,value}`. `PUT` stores any non-empty JSON up to 32,768 characters: 201 `Property created.` or 200 `Property updated.` `DELETE` is 204 or 404. |
| `GET /rest/forge/1/app/properties`, `GET/PUT/DELETE /rest/forge/1/app/properties/{propertyKey}` | *app* The same store for Forge apps; Connect apps get 403. |

Keys are 1 to 127 characters. `connect_client_key_019cdff3-8bfb-71fe-9628-875b700aebb8` is reserved: it reads the installation's client key and cannot be written or deleted (403). Properties are removed on uninstall.

## UI modifications

`GET/POST /rest/api/3/uiModifications` and `PUT/DELETE /rest/api/3/uiModifications/{uiModificationId}` are Forge-only; each app sees only its own. The list pages with `startAt` and `maxResults` (max 100) and expands `data` and `contexts`.

- `name` is required, max 255 characters; `data` max 50,000 characters.
- A context names `projectId`, `issueTypeId` and a `viewType` (`GIC`, `IssueView`, `IssueTransition` or their `AgentView` forms) with at most one wildcard, or `JSMRequestCreate` with `portalId` and `requestTypeId`.
- Limits: 3,000 modifications per app, 1,000 contexts per modification, 100 modifications per context, no duplicate contexts.
- A `PUT` with `contexts` replaces them all.

## Webhooks

### Dynamic webhooks for apps

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/webhook` | *app* Registers webhooks for one URL; answers `webhookRegistrationResult` with `createdWebhookId` or `errors` per entry. Events are Jira's dynamic set (issue, comment, issue property, sprint, version). `jqlFilter` is required and parsed. A Connect app's URL must be under its base URL. |
| `GET /rest/api/3/webhook` | *app* The app's webhooks with `expirationDate`, `fieldIdsFilter`, `issuePropertyKeysFilter`; pages of max 100. |
| `DELETE /rest/api/3/webhook` | *app* Deletes the listed `webhookIds`; 202. |
| `PUT /rest/api/3/webhook/refresh` | *app* Extends the listed webhooks; returns the new `expirationDate`. |
| `GET /rest/api/3/webhook/failed` | *app*, Connect only. Deliveries that failed for good in the last 72 hours, oldest first, with body, URL and `failureTime`; `after` and `next` page. |

- One URL and at most 100 webhooks per app.
- Webhooks expire after 30 days unless refreshed; expired webhooks receive nothing.
- `fieldIdsFilter` limits `jira:issue_updated` to changes of the listed fields; `issuePropertyKeysFilter` limits `issue_property_set` and `issue_property_deleted` to the listed keys.
- Setting a property to its current value sends no event.

### Administrator webhooks

`GET/POST /rest/webhooks/1.0/webhook` and `GET/PUT/DELETE /rest/webhooks/1.0/webhook/{id}`. The bean has `name`, `url`, `events`, `filters["issue-related-events-section"]` (JQL), `excludeBody`, `enabled`, `lastUpdated`, `lastUpdatedUser` and `lastUpdatedDisplayName`; ids are numeric. The site administration page uses the same resource.

### Delivery

- Every request carries `X-Atlassian-Webhook-Identifier`, unique per delivery and unchanged on retries.
- `excludeBody` sends no body.
- Failures retry with backoff. After five attempts the delivery is abandoned and, for app webhooks, listed as failed (`internal/store/mutations.go`, `maxWebhookAttempts`).

## Workflow history

Publishing a workflow records a new version. Versions are kept for 60 days.

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/workflow/history/list` | Site administrators. `{workflowId}` gives `entries`, newest first, with `workflowId`, `workflowVersion`, `writtenAt`, `isIntermediate`. `expand=includeIntermediateWorkflows` adds intermediate saves. Unknown workflow: 400. |
| `POST /rest/api/3/workflow/history` | `{workflowId, version}` gives that version as a workflow document (statuses, transitions, rules) with `version`, `updated`, `lastUpdateAuthorAAID` and the referenced statuses. Unknown version: 400. |
| `POST /rest/api/3/workflows` | Bulk read by `workflowIds`, `workflowNames` or `projectAndIssueTypes`, with referenced statuses. An empty request returns every workflow. |
| `GET /rest/api/3/workflow/search` | Site administrators. Published global classic workflows (team-managed excluded), filtered by `workflowName`, `queryString`, `isActive`; ordered by `name`, `created` or `updated`; each identified by `{name, entityId}`. `expand` adds `transitions`, `transitions.rules`, `transitions.properties`, `statuses`, `statuses.properties`, `default`, `schemes`, `projects`, `hasDraftWorkflow` and `operations` (`canEdit`; `canDelete` while no project or scheme uses it). |

Jira has no `POST /rest/api/3/workflow` or `/rest/api/3/workflow/project/{key}`, and neither does ZZIRA. Rule details are in [WORKFLOW_RULES.md](WORKFLOW_RULES.md).

## Transition rules owned by apps

A transition can hold app rules: `connect:remote-workflow-post-function`, `connect:remote-workflow-condition`, `connect:remote-workflow-validator` and their `forge:` forms, with parameters `appKey`, `key`, `config`, and optional `disabled` and `tag`. Workflow create, update and validation accept them. ZZIRA has no remote module runtime, so an app condition allows, an app validator passes and an app post function does nothing.

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/workflow/rule/config` | *app* The app's rules by workflow. `types` (postfunction, condition, validator) is required; `keys`, `workflowNames`, `withTags`, `draft` filter; `expand=transition` adds transition id and name. Pages of max 50. |
| `PUT /rest/api/3/workflow/rule/config` | *app* Replaces `value`, `disabled` and `tag` of the app's rules. `updateResults` carries `ruleUpdateErrors` per rule and `updateErrors` per workflow. A published change is a new workflow version. |
| `PUT /rest/api/3/workflow/rule/config/delete` | *app*, Connect only. Removes the listed `workflowRuleIds`. |

An app cannot read or change another app's rules.

## Data classification and data policy

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/classification-levels` | The organization's levels, filtered by `status`, ordered by `orderBy=rank`, `+rank` or `-rank`. |
| `GET /rest/api/3/project/{projectIdOrKey}/classification-config` | Permitted levels, the project default and `containerOverrideEnabled`. |
| `GET/PUT/DELETE /rest/api/3/project/{projectIdOrKey}/classification-level/default` | Reads, sets (a published level `id`) or removes the project default. Writing needs Administer projects; otherwise 401, as in Jira. |
| `GET /rest/api/3/data-policy`, `GET /rest/api/3/data-policy/project?ids=` | *app* Whether content is blocked for apps, site-wide and for 1 to 50 visible projects. `anyContentBlocked` is always false because no data security policy exists (see [ADMIN.md](ADMIN.md#gaps)). |

Levels are defined in site administration; see [CLASSIFICATION_LEVELS.md](CLASSIFICATION_LEVELS.md). Confluence uses the same levels.

## Licensing and audit

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/instance/license` | The site's Jira applications (`jira-software`, `jira-servicedesk`) with plan `PAID`. |
| `GET /rest/api/3/license/approximateLicenseCount`, `.../product/{applicationKey}` | Site administrators. Active non-app users of the site or of a licensed application; unlicensed applications count 0; unknown keys are 400. |
| `GET /rest/api/3/auditing/record` | Site administrators. The audit log, newest first, with `filter`, `from`/`to` (epoch ms or ISO date), `offset` and `limit` (max 1,000). Records carry `summary`, `category`, `objectItem`, `authorKey`, `remoteAddress`, `created`. |

## Site and project reads

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/serverInfo` | `baseUrl`, `version`, `versionNumbers`, `deploymentType` `Cloud`, build and server times, display URLs. |
| `GET /rest/api/3/label` | Labels visible to the caller, a PageBean of up to 1,000. |
| `GET /rest/api/3/project/{projectIdOrKey}/statuses` | Each work type of the project with its workflow's statuses. |
| `GET /rest/api/3/project/{projectId}/hierarchy` | The project's work types grouped by the site's [hierarchy levels](ISSUE_METADATA.md#work-type-hierarchy), top down. Numeric project id only. |
| `POST /rest/internal/api/latest/worklog/bulk` | Given 1 to 1,000 `{issueId, worklogId}` pairs, returns those that exist. |

## Plans and teams

Plans (Advanced planning) are for Jira administrators. Plan, plan-only team and issue source ids are numeric; Atlassian team ids are UUIDs.

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/plans/plan` | Active plans; `includeTrashed`, `includeArchived`; opaque `cursor` / `nextPageCursor`, max 50. |
| `POST /rest/api/3/plans/plan` | Creates a plan from `name`, `issueSources` (Board, Project or Filter), `scheduling` (StoryPoints, Days or Hours; start and end date fields; inferred dates; dependencies), `exclusionRules`, `crossProjectReleases`, `customFields`, `leadAccountId` and View/Edit `permissions` for groups (by name, or id with `useGroupId`) and people. Every referenced object must exist; unknown fields are 400. 201 with the id. |
| `GET /rest/api/3/plans/plan/{planId}` | The plan with Jira's defaults and `lastSaved`. |
| `PUT /rest/api/3/plans/plan/{planId}` | RFC 6902 JSON Patch (`add`, `remove`, `replace`, `move`, `copy`, `test`), validated like a new plan. Kept issue sources keep their ids. 409 when the plan is not active. |
| `PUT .../archive`, `PUT .../trash` | Archives or trashes an active plan; otherwise 409. |
| `POST .../duplicate` | Copies an active plan with its issue sources and teams under a new name. |
| `GET .../team` | Plan-only and Atlassian teams in the plan, cursor-paged. |
| `POST .../team/planonly`, `GET/PUT/DELETE .../team/planonly/{planOnlyTeamId}` | A plan-only team: name, planning style (Scrum or Kanban), issue source, sprint length, capacity, members. `PUT` is a JSON Patch. |
| `POST .../team/atlassian`, `GET/PUT/DELETE .../team/atlassian/{atlassianTeamId}` | Adds an existing Atlassian team with planning settings. Unknown team: 404; already in the plan: 400. |

Every team operation on an inactive plan is 409.

**UI.** **Plans** in the navigation lists the active plans a person can view; `/plans/{planId}` shows one. The page gathers the work the plan's boards (by board filter), projects and saved filters give the viewer; drops what the exclusion rules name (work items, work types, releases, statuses, status categories, sub-tasks, and work resolved more than `numberOfDaysToShowCompletedIssues` days ago); nests child work under included epics; and lays it across whole months using the scheduling start and end fields (Due date, the site's Target start and Target end, or a chosen date field). A teams table shows each team's planning style, sprint length, capacity and member count. Site administrators and the plan lead view and edit; View and Edit permissions grant the same; anyone else gets 404.

**Team field.** Every site has Jira's Team field (`com.atlassian.teams:rm-teams-custom-field-team`). Work items take a team by id or `{"id"}` and read it back as `{id, name, title, avatarUrl, isVisible, isShared}`; JQL matches by team id or name. A work item belongs to a plan team when its Team field holds that team's Atlassian team.

**Scenarios.** Every plan has a Default scenario (the plan's `scenarioId`). Planners with edit access create more (blank or copied), rename, recolor and delete them; the default cannot be deleted. Editing from the plan changes summary, start and end dates, team, sprint or estimate in the chosen scenario only; changed values are flagged in the scenario's color, and setting a value back to Jira's forgets the change. **Review changes** lists changes by work item with who made them. Saving selected changes updates Jira as the planner, with their permissions and field rules, optionally without notifications; a change that cannot be saved (for example, assignment to a plan-only team) stays with its reason. Discarding forgets the selected changes.

**Capacity.**
- Estimates are Story point estimate for story-point plans, or original estimate in hours, or days of the site's working hours, for time plans.
- A Scrum team whose issue source is a board plans that board's active and future sprints. Sprint capacity is the team's story points per sprint (default 30), or weekly hours (default 200) times sprint length in weeks.
- A Kanban team plans twelve one-week iterations from this week with time estimates.
- Work consumes a Scrum iteration when its team and sprint match; it consumes a Kanban week by the share of its dates in that week.
- Each iteration shows capacity, planned and completed estimates, unestimated work and over-capacity state. A planner can override one iteration's capacity in a scenario.

**Dependencies.** Blocks links between work in the plan are dependencies: the outward item blocks the inward one. A dependency is off track when the blocker ends after the blocked work starts, or is planned into a later sprint, or into the same sprint unless the plan allows concurrent scheduling (scheduling dependencies `Concurrent`). Work items show what they block and are blocked by, red when off track.

**Teams.** Atlassian teams live on **People › Teams**. Anyone can start a team; its members and site administrators add and remove members or delete it. Deleting a team removes it from every plan.

## Custom project templates

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/project-template/save-template` | Saves a template from a project: `templateName` (max 50, unique), `templateDescription` (max 150), `templateFromProjectRequest` with `projectId`, `templateType`, `templateGenerationOptions`. Answers `projectTemplateKey` with `key` and `uuid`. |
| `GET /rest/api/3/project-template/live-template` | A template by `templateKey`, or the LIVE template of `projectId`, as Jira's ProjectTemplateModel (archetype, default board view, `snapshotTemplate`, generation options, type, and `liveTemplateProjectIdReference` for LIVE). |
| `PUT /rest/api/3/project-template/edit-template` | Changes name, description and generation options. |
| `DELETE /rest/api/3/project-template/remove-template` | Removes a template. |
| `POST /rest/api/3/project-template` | Creates a company-managed project from `details` and a `template` whose `project` capability names the project type and references existing permission, notification, work type, work type screen, field layout, workflow and issue security schemes by `{"type":"ID","id":...}`. Details and schemes are validated first; the project is created in a task (303 with `Location`). |

A LIVE template reports its project's current configuration; a SNAPSHOT keeps what it was saved with. Templates are site-administrator operations.

## Connect app migration

A site administrator opens a data transfer for an active Connect app under **Administration › Apps › Data migration**. The app sends the transfer id in `Atlassian-Transfer-Id`; an unknown transfer or another app's transfer is 403.

| Operation | Behavior |
| --- | --- |
| `PUT /rest/atlassian-connect/1/migration/field` | Sets values of the app's issue fields (`StringIssueField`, `TextIssueField`, `RichTextIssueField`, `NumberIssueField`, `SingleSelectIssueField`, `MultiSelectIssueField`) by numeric `fieldID` and `issueID`, up to 200 fields, through the ordinary issue update. Each `MultiSelectIssueField` entry adds one option. |
| `PUT /rest/atlassian-connect/1/migration/properties/{entityType}` | Up to 50 properties by numeric entity id for issues, comments, worklogs, work types, projects, boards, sprints and dashboard items; all or none. `UserProperty` is 400 (accounts have no numeric id). |
| `POST /rest/atlassian-connect/1/migration/workflow/rule/search` | The app's rules among up to 10 `ruleIds` of `workflowEntityId`, grouped as post functions, conditions and validators, with `invalidRules`; `expand=transition` adds the transition. |
| `GET/POST /rest/atlassian-connect/1/migration/{connectKey}/{jiraIssueFieldsKey}/task` | Submit and follow the Connect-to-Forge field migration task: 202 when queued, 409 while one runs; a completed migration repeats only with `retriggerCompletedMigration=true`. GET returns Jira's TaskProgress. Connect and Forge fields share one record, so the task verifies the field and reports how many values it holds. |

## Service registry

`GET /rest/atlassian-connect/1/service-registry?serviceIds=` returns up to 20 services by id (ids starting `b:` are Base64) with tier, revision and organization id; Connect apps only. Site administrators maintain services on **Service management › Services** (name, description, one of four tiers); each change advances the revision.

## App custom field configuration and values

| Operation | Behavior |
| --- | --- |
| `GET/PUT /rest/api/3/app/field/{fieldIdOrKey}/context/configuration` | Configuration and schema of an app field per context, filtered by one of `id`, `fieldContextId`, `issueId`, or `projectKeyOrId` with `issueTypeId`; paged. `PUT` replaces up to 1,000 configurations. |
| `POST /rest/api/3/app/field/context/configuration/list` | The same for several fields by id or `appKey__moduleKey`, with `customFieldId` per entry. |
| `PUT /rest/api/3/app/field/{fieldIdOrKey}/value`, `POST /rest/api/3/app/field/value` | The providing app sets values, each field and work item once, through the ordinary issue update (validated, recorded in the changelog). |

Configuration is for site administrators and the providing app; values are for the providing app only. `generateChangelog=false` keeps the write out of the changelog; `generateAppEvents=false` keeps it from app and administrator webhooks (replicas still get the value). Custom field changes appear in the changelog as Jira's `custom` items with field name and `fieldId`. App select-list options are in [APP_FIELD_OPTIONS.md](APP_FIELD_OPTIONS.md).

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- Remote app modules do not run: app workflow conditions, validators and post functions are stored but have no effect.
- Project templates cannot create new schemes, boards, fields, roles, work types or workflows inline (`REF` identifiers and the other capabilities are 400), and cannot create team-managed (`PROJECT` scope) projects.

## References

- [App properties](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-app-properties/)
- [UI modifications (apps)](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-ui-modifications-apps/)
- [Webhooks](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-webhooks/)
- [Workflows](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-workflows/)
- [Workflow transition rules](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-workflow-transition-rules/)
- [Classification levels](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-classification-levels/)
- [App data policies](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-app-data-policies/)
- [License metrics](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-license-metrics/)
- [Audit records](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-audit-records/)
