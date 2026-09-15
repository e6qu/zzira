# Jira Automation rules and API

ZZIRA implements the Jira Cloud Automation rule-management surface at the
site gateway base path and runs a native, permission-aware subset of scheduled,
event-triggered and manually triggered rules. This is a compatibility slice, not the complete Atlassian Automation
runtime.

## API base path and operations

Discover the configured site's Cloud ID with:

```text
GET /_edge/tenant_info
```

Use it in either versioned site path:

```text
/gateway/api/automation/public/jira/{cloudid}/rest/v1
/gateway/api/automation/public/jira/{cloudid}/rest/latest
```

The following eight Jira Automation rule-management operations are available:

| Method | Path | Behavior |
|---|---|---|
| GET | `/rule/summary` | Cursor-paged rule summaries |
| POST | `/rule/summary` | Summary search by state, trigger, scope, or author |
| POST | `/rule` | Create a rule; omitted UUIDs become UUIDv7 values |
| GET | `/rule/{ruleUuid}` | Read a full rule and its connections |
| PUT | `/rule/{ruleUuid}` | Replace a rule while retaining the path UUID |
| DELETE | `/rule/{ruleUuid}` | Delete a disabled rule |
| PUT | `/rule/{ruleUuid}/state` | Enable or disable a rule |
| PUT | `/rule/{ruleUuid}/rule-scope` | Replace rule-scope ARIs |

API token and browser-session authentication use the same workspace identities
as Jira REST routes. Rule management requires workspace administration. Error
responses use Automation's `errors[]` shape with `id`, `status`, `code`,
`title`, and an optional `field`. `redactSensitiveFields=true` masks common
secret fields in rule and connection payloads.

ZZIRA stores complete rule component and connection JSON for read/write round
trips. Atlassian's primary entry point path, `/automation/public/jira/{cloudid}/rest/...`,
is served on the ZZIRA base URL next to the site gateway path. OAuth 2.0, Forge
principals and Atlassian organization roles are not implemented. Cursors are
opaque encoded offsets stamped with their issue time; they expire after one hour
(400) and can shift when rules are concurrently added or deleted.

## Executable scheduled subset

The workspace-admin UI is at `/settings/automation`. A native scheduled rule
uses trigger type `jira.jql.scheduled` or `jira.issue.scheduled` with this value:

```json
{
  "intervalMinutes": 60,
  "timezone": "Europe/Bucharest",
  "jql": "project = ZZ AND status != Done"
}
```

The editor and worker support fixed intervals from one minute through 30 days.
The timezone is retained in the rule, while fixed intervals are elapsed-time
schedules and therefore do not move at daylight-saving boundaries. Jira's Cron
schedule form remains a gap.

The worker executes these action component types in order:

| Component type | Value | Semantics |
|---|---|---|
| `jira.issue.add-label` | `{"label":"reviewed"}` | Adds the label if absent |
| `jira.issue.assign` | `{"accountId":"..."}` | Assigns an active member; `ACTOR` and `UNASSIGNED` are accepted |
| `jira.issue.transition` | `{"statusId":"10001"}` | Uses a valid current-workflow transition to the target status |
| `jira.issue.comment` | `{"comment":"Picked up by {{initiator.displayName}}"}` | Adds a comment as the rule actor |
| `jira.issue.edit` | `{"field":"summary","value":"[{{issue.key}}] {{issue.summary}}"}` | Sets the `summary` or `duedate` (yyyy-MM-dd; blank clears it) when it differs |

JQL evaluation and every mutation run as the stored rule actor. Issue security
therefore filters the matched set, and command-layer validation applies to
assignment and workflow changes. Runs are capped at 1,000 matching work items;
larger results fail before any actions run.

Other triggers, components, branches, smart values and connection payloads
remain available through the rule API, but the worker records an explicit failed
audit entry when asked to execute unsupported behavior. Cron schedules,
branching, issue and page creation, email and web requests, usage limits and the
rest of Jira's trigger, condition and action catalog remain gaps.

## Event triggers, conditions and smart values

A rule can instead start when work changes. Its trigger `value` may hold `jql`,
which the work item must match for the rule actor:

| Trigger type | Extra value | Starts when |
|---|---|---|
| `jira.issue.event.trigger:created` | none | A work item is created |
| `jira.issue.event.trigger:transitioned` | optional `fromStatusIds`, `toStatusIds` | A work item's status changes, from and to the listed statuses when given |
| `jira.issue.field.changed` | `fields`, 1 to 20 names such as `summary`, `priority`, `assignee`, `labels` | Any listed field changes |
| `jira.issue.event.trigger:commented` | none | A comment is added |

The worker reads new events from the action log in order and queues one run per
rule and event, so a retried batch never repeats a run. Enabling a rule never
replays earlier events. An event run acts on its work item only if the rule
actor can still see it, it is unarchived and in the rule's scope, and it matches
the trigger's JQL. A rule never starts from its own changes, and starts from
another rule's changes only when its `canOtherRuleTrigger` is true, as in Jira.

Components run in order. A `CONDITION` component that does not hold stops the
rule for that work item, and each action sees the work item as the previous
action left it. Scheduled, event and manual runs evaluate:

| Condition type | Value | Holds when |
|---|---|---|
| `jira.jql.condition` | `{"jql":"priority = High"}` | The work item matches the JQL for the rule actor |
| `jira.issue.condition` | `{"field":"status","operator":"EQUALS","value":"In Progress"}` | The field compares as asked |

Fields conditions compare `status`, `priority`, `issuetype`, `assignee`,
`reporter`, `labels`, `summary` or `duedate`, ignoring case, with `EQUALS`,
`NOT_EQUALS`, `CONTAINS`, `IS_EMPTY` or `IS_NOT_EMPTY`. People match by account
ID or display name, statuses, priorities and work types by name or ID, and
labels by any label.

Label, assignee, comment, edit and condition values render smart values:
`{{issue.key}}`, `{{issue.summary}}`, `{{issue.status.name}}`,
`{{issue.priority.name}}`, `{{issue.issueType.name}}`, `{{issue.dueDate}}`,
`{{issue.labels}}`, `{{issue.assignee.displayName}}`,
`{{issue.assignee.accountId}}`, `{{issue.reporter.displayName}}`,
`{{issue.reporter.accountId}}`, `{{initiator.displayName}}`,
`{{initiator.accountId}}`, `{{rule.name}}`, `{{now}}` and `{{now.jiraDate}}`.
The initiator is the person whose change started an event run or who invoked a
manual rule. Unknown smart values render empty, as in Jira.

The rule editor at `/settings/automation` offers the scheduled and work item
event triggers with their options, work item fields conditions, and the label,
assign, transition, comment, edit summary and due date actions.

## Manually triggered rules

A rule whose trigger is `jira.manual.trigger.issue.action` can be run from a work
item. Its `value.inputPrompts` list the inputs the person running it gives
(`displayName`, `inputType`, `required`, `variableName`).

| Operation | Behavior |
| --- | --- |
| `POST .../rest/v1/rule/manual/search` | Site members: `objects` (issue ARIs `ari:cloud:jira:{cloudId}:issue/{id}`) or `cursor`, never both, and `limit` 1 to 100 (default 50). Returns enabled manual rules whose scope covers every object's project as `data` of `{id, name, userInputs}` with `links.self`, `next` and `prev`. An invisible issue is 403. |
| `GET .../rest/v1/rule/manual/search?cursor=` | Continues a search; only `cursor` and `limit` are accepted. |
| `POST .../rest/v1/rule/manual/{ruleId}/invocation` | Runs the rule for 1 to 50 objects with `userInputs`, returning a result per ARI: `SUCCESS`, `INVALID_TARGET_OBJECT` (not a visible issue), `INVALID_RULE_OR_OBJECT` (disabled, not manual or unsupported) or `INVALID_TARGET_SCOPE`. Missing required inputs are 400; an unknown rule is 404. |

Invocation runs the rule's actions immediately as the rule actor and records a
run in the audit log, like a scheduled run.

## Templates

`GET/POST .../rest/v1/template/search` lists the template catalog by `categories`
and `ruleHome` (the site ARI `ari:cloud:jira::site/{cloudId}` or a project ARI)
with cursors; `GET .../rest/v1/template/{templateId}` returns one template with
its categories and typed parameters. Site members can read the catalog.

| Template | Categories | Parameters |
| --- | --- | --- |
| `scheduled-label-stale-work` | popular, scheduled, work item management | `label` (TEXT), `days` (NUMBER) |
| `scheduled-assign-unassigned` | software, scheduled, work item management | `assigneeAccountId` (TEXT, required) |
| `manual-assign-to-me` | popular, manually triggered, work item management | none |
| `manual-transition` | software, manually triggered, work item management | `statusId` (TEXT, required) |

`POST .../rest/v1/template/create` (administrators) builds a rule from
`templateId`, `ruleHome`, `parameters` (`{type, value}` per key) and an optional
`state`, returning `{ruleUuid}`. Missing, mistyped and unknown parameters are 400,
as is a rule home outside the site. The catalog holds templates for the actions
the worker executes.

## Durability and audit behavior

Due rules enqueue a unique run for each rule and scheduled timestamp. Workers
claim rows with `FOR UPDATE SKIP LOCKED`; multiple server replicas cannot claim
the same healthy run. A `RUNNING` claim older than two minutes is reclaimable
after a process failure. Failed attempts use exponential backoff capped at 30
minutes. Ten consecutive failures disable the rule, matching Jira Cloud's
documented scheduled-rule safeguard. A successful or no-action run resets the
counter.

The supported actions express a desired state. Replaying after a crash sees the
label, assignee, or target status already applied and becomes a no-op. This gives
the executable subset idempotent recovery without weakening the shared command
layer. The audit log records scheduled time, state, attempts, duration, matched
and changed work-item counts, and the last error. “Run now” uses the same durable
queue as scheduled execution.

## References

- [Atlassian Automation REST API](https://developer.atlassian.com/cloud/automation/rest/)
- [Rule-management operations](https://developer.atlassian.com/cloud/automation/rest/api-group-rule-management/)
- [Automation API base paths](https://developer.atlassian.com/cloud/automation/api/base-paths/)
- [Jira Automation scheduled trigger](https://support.atlassian.com/cloud-automation/docs/jira-automation-triggers/)
- [Debug an automation rule](https://support.atlassian.com/cloud-automation/docs/debug-an-automation-rule/)
