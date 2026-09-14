# Jira platform: app storage, UI modifications, webhooks, workflow history and site reads

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
limits `jira:issue_updated` to changes of the listed fields.

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

## Not yet provided

Plans and plan teams, project templates, Connect-to-Forge migration, the Connect
service registry and app custom field configuration and values are separate
work.

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
