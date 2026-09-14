# Jira platform: apps, webhooks, workflows, plans, templates and site reads

This covers the Jira Cloud platform operations apps and administrators use
around the core issue APIs: app properties, Forge UI modifications, dynamic and
administrator webhooks, workflow version history, transition rules owned by
apps, data classification, licensing, the audit log and several site and
project reads. Automation's manually triggered rules and templates are in
[AUTOMATION.md](AUTOMATION.md).

Operations marked *app* accept only a request authenticated as an installed app
(a Connect JWT or the ZZIRA equivalent of a Forge app token); a user's token is
refused as in Jira.

## App properties

| Operation | Behavior |
| --- | --- |
| `GET /rest/atlassian-connect/1/addons/{addonKey}/properties` | *app* The calling app's property keys with `self` links. Another app's key is 401. |
| `GET/PUT/DELETE /rest/atlassian-connect/1/addons/{addonKey}/properties/{propertyKey}` | *app* Reads `{key,value}`; `PUT` stores any non-empty JSON up to 32,768 characters and answers 201 `Property created.` or 200 `Property updated.`; `DELETE` is 204 or 404. |
| `GET /rest/forge/1/app/properties` and `GET/PUT/DELETE /rest/forge/1/app/properties/{propertyKey}` | *app* The same storage for Forge apps; Connect apps are 403. |

Keys are 1 to 127 characters. The Connect key
`connect_client_key_019cdff3-8bfb-71fe-9628-875b700aebb8` is reserved: it reads
the installation's client key and cannot be written or deleted (403).
Properties belong to the installation and go with it on uninstall.

## UI modifications

`GET/POST /rest/api/3/uiModifications` and `PUT/DELETE
/rest/api/3/uiModifications/{uiModificationId}` are for Forge apps only; each
app sees only its own modifications. The list pages with `startAt` and
`maxResults` (at most 100) and expands `data` and `contexts`.

Names are required and at most 255 characters; `data` is at most 50,000
characters. A context names `projectId`, `issueTypeId` and a `viewType` of
`GIC`, `IssueView`, `IssueTransition` or their `AgentView` forms, with at most one
wildcard (a missing value), or `JSMRequestCreate` with `portalId` and
`requestTypeId`. Jira's limits apply: 3,000 modifications per app, 1,000
contexts per modification, 100 modifications per context and no duplicate
contexts. A `PUT` that sends `contexts` replaces them all.

## Webhooks

### Dynamic webhooks for apps

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/webhook` | *app* Registers webhooks for one URL and answers `webhookRegistrationResult` with `createdWebhookId` or `errors` per entry. Events are Jira's dynamic webhook set (issue, comment, issue property, sprint and version events); `jqlFilter` is required and parsed. A Connect app's URL must use its base URL. |
| `GET /rest/api/3/webhook` | *app* The app's webhooks with `expirationDate`, `fieldIdsFilter` and `issuePropertyKeysFilter`, paged at most 100. |
| `DELETE /rest/api/3/webhook` | *app* Deletes the listed `webhookIds`; 202. |
| `PUT /rest/api/3/webhook/refresh` | *app* Extends the listed webhooks and returns the new `expirationDate`. |
| `GET /rest/api/3/webhook/failed` | *app* Connect only: deliveries that failed for good in the last 72 hours, oldest first, each with its body, URL and `failureTime`; `after` and `next` continue the list. |

An app may register one URL and at most 100 webhooks. Webhooks expire after 30
days unless refreshed, and expired webhooks receive nothing. `fieldIdsFilter`
limits `jira:issue_updated` to changes of the listed fields, and
`issuePropertyKeysFilter` limits `issue_property_set` and `issue_property_deleted`
to the listed property keys. Setting a property to the value it already has sends
no event.

### Administrator webhooks

`GET/POST /rest/webhooks/1.0/webhook` and `GET/PUT/DELETE
/rest/webhooks/1.0/webhook/{id}` are Jira's administrator webhooks. The bean has
`name`, `url`, `events`, `filters["issue-related-events-section"]` (JQL),
`excludeBody`, `enabled`, `lastUpdated`, `lastUpdatedUser` and
`lastUpdatedDisplayName`; ids are numbers. The site administration page uses the
same resource.

### Delivery

Every request carries `X-Atlassian-Webhook-Identifier`, unique per delivery and
the same on retries. A webhook with `excludeBody` sends no body. Failed deliveries
are retried with backoff; after five attempts the delivery is abandoned and,
for app webhooks, appears in the failed list.

## Workflow history

Publishing a workflow records its definition as a new version. Versions are
kept for 60 days.

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/workflow/history/list` | Site administrators: `{workflowId}` gives `entries` newest first with `workflowId`, `workflowVersion`, `writtenAt` and `isIntermediate`; `expand=includeIntermediateWorkflows` adds intermediate saves. An unknown workflow is 400. |
| `POST /rest/api/3/workflow/history` | `{workflowId, version}` gives that version as a workflow document (statuses, transitions and rules as they were) with `version`, `updated` and `lastUpdateAuthorAAID`, plus the statuses it references. An unknown version is 400. |
| `POST /rest/api/3/workflows` | Bulk read by `workflowIds`, `workflowNames` or `projectAndIssueTypes`, returning the same documents and referenced statuses; an empty request returns every workflow. |
| `GET /rest/api/3/workflow/search` | Paged workflow search, unchanged. |

## Transition rules owned by apps

A workflow transition can hold app rules: rule keys
`connect:remote-workflow-post-function`, `connect:remote-workflow-condition`,
`connect:remote-workflow-validator` and their `forge:` forms, with parameters
`appKey`, `key`, `config`, and optionally `disabled` and `tag`. They are accepted
by workflow create, update and validation. ZZIRA has no remote module runtime,
so on a transition an app condition allows, an app validator passes and an app
post function does nothing.

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/workflow/rule/config` | *app* The calling app's rules by workflow: `types` (postfunction, condition, validator) is required; `keys`, `workflowNames`, `withTags` and `draft` filter; `expand=transition` adds the transition's id and name. Pages of at most 50. |
| `PUT /rest/api/3/workflow/rule/config` | *app* Replaces the `value`, `disabled` and `tag` of the app's rules. `updateResults` carries `ruleUpdateErrors` per rule id and `updateErrors` per workflow. A published change becomes a new workflow version. |
| `PUT /rest/api/3/workflow/rule/config/delete` | *app* Connect only: removes the listed `workflowRuleIds` of the app. |

Rules of other apps cannot be read or changed.

## Data classification and data policy

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/classification-levels` | The site's levels (Public, Internal, Confidential, Restricted), filtered by `status` and ordered by `orderBy=rank`, `+rank` or `-rank`. |
| `GET /rest/api/3/project/{projectIdOrKey}/classification-config` | Permitted levels, the project default and `containerOverrideEnabled`. |
| `GET/PUT/DELETE /rest/api/3/project/{projectIdOrKey}/classification-level/default` | Reads, sets (a published level `id`) or removes the project default. Changing it requires Administer projects; otherwise 401, as in Jira. |
| `GET /rest/api/3/data-policy` and `GET /rest/api/3/data-policy/project?ids=` | *app* Whether content is blocked for apps, site-wide and for 1 to 50 visible projects. No data policy blocks apps, so `anyContentBlocked` is false. |

Confluence databases use the same levels.

## Licensing and audit

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/instance/license` | The site's Jira applications (`jira-software`, `jira-servicedesk`) with plan `PAID`. |
| `GET /rest/api/3/license/approximateLicenseCount` and `/product/{applicationKey}` | Site administrators: active, non-app users of the site, or of a licensed application; unlicensed applications count 0 and unknown keys are 400. |
| `GET /rest/api/3/auditing/record` | Site administrators: the audit log newest first with `filter` words, `from`/`to` (epoch milliseconds or ISO dates), `offset` and `limit` up to 1,000. Records carry `summary`, `category`, `objectItem`, `authorKey`, `remoteAddress` and `created`. |

## Site and project reads

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/serverInfo` | `baseUrl`, `version` and `versionNumbers`, `deploymentType` `Cloud`, build and server times and the display URLs. |
| `GET /rest/api/3/label` | Labels visible to the caller as a PageBean, up to 1,000 per page. |
| `GET /rest/api/3/project/{projectIdOrKey}/statuses` | Each work type of the project with the statuses of its workflow. |
| `GET /rest/api/3/project/{projectId}/hierarchy` | The project's work types grouped by level (Epic, Base, Subtask); the project must be given by numeric id. |
| `POST /rest/internal/api/latest/worklog/bulk` | Given 1 to 1,000 `{issueId, worklogId}` pairs, returns the ones that exist. |

## Plans and teams

Plans (Advanced planning) are for Jira administrators. Plan, plan-only team and
issue source ids are Jira's numbers; Atlassian team ids are UUIDs.

| Operation | Behavior |
| --- | --- |
| `GET /rest/api/3/plans/plan` | Active plans, with `includeTrashed` and `includeArchived`, paged by an opaque `cursor` with `nextPageCursor`, at most 50. |
| `POST /rest/api/3/plans/plan` | Creates a plan from `name`, `issueSources` (Board, Project or Filter by id), `scheduling` (estimation StoryPoints, Days or Hours; start and end date fields; inferred dates; dependencies), `exclusionRules`, `crossProjectReleases`, `customFields`, `leadAccountId` and View/Edit `permissions` for groups (by name, or id with `useGroupId`) and people. Every referenced board, project, filter, release, field, work type, status and person must exist; unknown fields are 400. Answers 201 with the id. |
| `GET /rest/api/3/plans/plan/{planId}` | The plan with Jira's defaults filled in and `lastSaved`. |
| `PUT /rest/api/3/plans/plan/{planId}` | An RFC 6902 JSON Patch against the plan document (`add`, `remove`, `replace`, `move`, `copy`, `test`), validated like a new plan. Issue sources that stay keep their ids. 409 when the plan is not active. |
| `PUT .../archive`, `PUT .../trash` | Archives or trashes an active plan; otherwise 409. |
| `POST .../duplicate` | Copies an active plan with its issue sources and teams under a new name. |
| `GET .../team` | Plan-only and Atlassian teams in the plan, cursor-paged. |
| `POST .../team/planonly`, `GET/PUT/DELETE .../team/planonly/{planOnlyTeamId}` | A plan-only team with name, planning style (Scrum or Kanban), issue source, sprint length, capacity and members; PUT is a JSON Patch. |
| `POST .../team/atlassian`, `GET/PUT/DELETE .../team/atlassian/{atlassianTeamId}` | Adds an existing Atlassian team with its planning settings; an unknown team is 404 and a team already in the plan is 400. |

Every team operation on a plan that is not active is 409. Plans are a planning
record: ZZIRA has no timeline view that schedules work from them.

Atlassian teams are kept on the site's **People › Teams** page. Anyone can start
a team; its members and site administrators add and remove members or delete
it. Deleting a team removes it from every plan.

## Custom project templates

| Operation | Behavior |
| --- | --- |
| `POST /rest/api/3/project-template/save-template` | Saves a template from a project: `templateName` (up to 50 characters, unique), `templateDescription` (up to 150) and `templateFromProjectRequest` with `projectId`, `templateType` and `templateGenerationOptions`. Answers `projectTemplateKey` with `key` and `uuid`. |
| `GET /rest/api/3/project-template/live-template` | A template by `templateKey`, or the LIVE template of `projectId` (id or key), as Jira's ProjectTemplateModel: archetype, default board view, the configuration as `snapshotTemplate`, generation options, type and, for LIVE templates, `liveTemplateProjectIdReference`. |
| `PUT /rest/api/3/project-template/edit-template` | Changes the name, description and generation options. |
| `DELETE /rest/api/3/project-template/remove-template` | Removes a template. |
| `POST /rest/api/3/project-template` | Creates a company-managed project from `details` and a `template` whose `project` capability names the project type and references existing permission, notification, work type, work type screen, field layout, workflow and issue security schemes by `{"type":"ID","id":...}`. The details and schemes are validated first; the project is then created in a task (303 with `Location`) that applies each scheme. |

A LIVE template records a project and reports that project's current
configuration; a SNAPSHOT keeps the configuration it was saved with. Templates
are site administrator operations. Creating new schemes, boards, fields, roles,
work types or workflows inside the template request (`REF` identifiers and the
other capabilities) and team-managed (`PROJECT` scope) projects are answered
with 400.

## Connect app migration

A site administrator opens a data transfer for an active Connect app on
**Administration › Apps › Data migration**. The app sends the transfer id in the
`Atlassian-Transfer-Id` header; an unknown transfer or another app's transfer
is 403.

| Operation | Behavior |
| --- | --- |
| `PUT /rest/atlassian-connect/1/migration/field` | Sets the values of the app's issue fields: `StringIssueField`, `TextIssueField`, `RichTextIssueField`, `NumberIssueField`, `SingleSelectIssueField` and `MultiSelectIssueField` values by numeric `fieldID` and `issueID`, for up to 200 fields, through the ordinary issue update; every `MultiSelectIssueField` entry for an issue and field adds one option to its list. |
| `PUT /rest/atlassian-connect/1/migration/properties/{entityType}` | Up to 50 properties by Jira's numeric entity id for issues, comments, worklogs, work types, projects, boards, sprints and dashboard items, all or none. `UserProperty` is 400 because accounts have no numeric id. |
| `POST /rest/atlassian-connect/1/migration/workflow/rule/search` | The calling app's rules among up to 10 `ruleIds` of the workflow `workflowEntityId`, grouped as post functions, conditions and validators, with `invalidRules` for the rest; `expand=transition` adds the transition. |
| `GET/POST /rest/atlassian-connect/1/migration/{connectKey}/{jiraIssueFieldsKey}/task` | Connect and Forge apps submit and follow the task migrating an issue field to its Forge custom field: 202 when queued, 409 while one runs, a completed migration is only repeated with `retriggerCompletedMigration=true`, and GET returns Jira's TaskProgress. Connect and Forge fields share one record here, so the task verifies the field and reports how many values it holds. |

## Service registry

`GET /rest/atlassian-connect/1/service-registry?serviceIds=` returns up to 20
services by id (ids starting `b:` are Base64) with their tier, revision and
organization id, for Connect apps only. Site administrators maintain services
on **Service management › Services** with a name, description and one of four
tiers; each change advances the revision.

## App custom field configuration and values

| Operation | Behavior |
| --- | --- |
| `GET/PUT /rest/api/3/app/field/{fieldIdOrKey}/context/configuration` | The configuration and schema of an app field in each of its contexts, filtered by `id`, `fieldContextId`, `issueId` or `projectKeyOrId` with `issueTypeId` (one filter at a time), paged. PUT replaces up to 1000 configurations. |
| `POST /rest/api/3/app/field/context/configuration/list` | The same for several fields by id or `appKey__moduleKey`, with `customFieldId` on each entry. |
| `PUT /rest/api/3/app/field/{fieldIdOrKey}/value`, `POST /rest/api/3/app/field/value` | The app that provides a field sets its value on issues, each field and issue combination once; values go through the ordinary issue update, so they are validated and recorded in the changelog. |

Configuration is for site administrators and the providing app; values are for
the providing app only. `generateChangelog=false` keeps the write out of the
issue's changelog and `generateAppEvents=false` keeps it from app and
administrator webhooks; replicas still receive the new value. Custom field
changes appear in the changelog as Jira's `custom` items with the field name and
`fieldId`.

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
