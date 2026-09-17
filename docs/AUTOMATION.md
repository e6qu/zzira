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
Fixed intervals are elapsed-time schedules and therefore do not move at
daylight-saving boundaries. A rule can instead follow a Quartz cron expression,
as Jira's scheduled trigger does, given as
`"schedule": {"method": "CRON_EXPRESSION", "cronExpression": "0 0 9 ? * MON-FRI"}`
or as a top-level `cronExpression`. It fires in the rule's timezone. The seconds
field must be a single number, exactly one of the day-of-month and day-of-week
fields must be `?`, and `L`, `W` and `#` are not supported. Local times skipped
by a daylight-saving change do not fire, a time missed while no worker ran runs
once, and disabling a rule clears its next time until it is enabled again.

The worker executes these action component types in order:

| Component type | Value | Semantics |
|---|---|---|
| `jira.issue.add-label` | `{"label":"reviewed"}` | Adds the label if absent |
| `jira.issue.remove-label` | `{"label":"triage"}` | Removes the label if the work item carries it. The label renders smart values, and a label the work item does not carry changes nothing |
| `jira.issue.assign` | `{"accountId":"..."}` or `{"method":"round-robin"}` | Assigns an active member; `ACTOR` and `UNASSIGNED` are accepted. A rule may instead name a `method` and let the rule pick: `round-robin` gives the work to whoever has waited longest, `balanced` to whoever carries the least unresolved work, and `random` to any of them. Candidates are the people the project's assignee picker offers, so a rule cannot assign work to someone a person could not, and a project offering nobody stops the rule |
| `jira.issue.transition` | `{"statusId":"10001"}` | Uses a valid current-workflow transition to the target status |
| `jira.issue.comment` | `{"comment":"Picked up by {{initiator.displayName}}"}` | Adds a comment as the rule actor |
| `jira.issue.edit` | `{"field":"summary","value":"[{{issue.key}}] {{issue.summary}}"}` | Sets the `summary`, `duedate` (yyyy-MM-dd; blank clears it), `priority` (by name or id), `description` or `labels` when it differs. Labels are comma-separated and replace what the work item carries, which is what Jira's edit does; adding one label without disturbing the rest is the add label action |
| `confluence.page.create` | `{"spaceKey":"TEAM"}` | Raises a page in the space named, as the rule actor: a space the actor cannot see, or may not create pages in, stops the rule. The key renders smart values, and an optional `title` does too, defaulting to the work item's key and summary. The page names the rule and the work it was raised for |
| `jira.issue.delete` | `{}` | Deletes the work item as the rule actor, so a rule deletes only what its actor may delete: without the project's Delete issues permission the rule stops and the run records why. Nothing after it runs for that work item, because it no longer exists. Sub-tasks are moved or deleted first, as they are everywhere else |
| `jira.issue.log-work` | `{"duration":"3h 30m"}` | Logs work against the work item as the rule actor. The duration renders smart values and is read with the site's own time tracking, so a week and a day mean what it says they mean; a duration of zero or less stops the rule |
| `jira.issue.create-subtask` | `{"summary":"Review {{issue.key}}"}` | Raises a sub-task of the work item, taking the project scheme's sub-task work type. The summary renders smart values. A sub-task of that name already under the work item changes nothing, so a rule that runs again does not mint a second one; a work item that is itself a sub-task, or a project offering no sub-task type, stops the rule |
| `jira.issue.create` | `{"issueTypeId":"it_task","summary":"Follow up on {{issue.key}}"}` | Raises a work item beside the one the rule runs for, in its project. The work type comes from the project's own scheme, so a rule cannot raise a type the project does not offer, and sub-task types are refused because they need a parent. The summary renders smart values. Work of that type and summary already in the project changes nothing, so a scheduled rule does not raise one every interval |
| `jira.issue.email` | `{"recipient":"assignee","body":"{{issue.key}} needs you"}` | Queues plain-text mail to the work item's `assignee`, `reporter` or `watchers` through the site's delivery outbox. The body renders smart values; the subject is the work item's key and summary, because a rule names one message. Recipients without an address, and deactivated accounts, are skipped, so work nobody is assigned or watching sends nothing |
| `jira.issue.link` | `{"linkTypeId":"lt_blocks","issueKey":"ZZ-7"}` | Links the work item to the one named, which takes the link type's inward phrase. The key renders smart values. A link that already holds, or a work item naming itself, changes nothing; a key of another site stops the rule |

JQL evaluation and every mutation run as the stored rule actor. Issue security
therefore filters the matched set, and command-layer validation applies to
assignment and workflow changes. Runs are capped at 1,000 matching work items;
larger results fail before any actions run.

Other triggers, components, branch types, smart values and connection payloads
remain available through the rule API, but the worker records an explicit failed
audit entry when asked to execute unsupported behavior. Page creation triggers,
usage limits and the rest of Jira's trigger, condition and action catalog
remain gaps. A rule sends a web request with the send web request action: it
names its method, as a link action names its link type, and carries the address,
which may hold smart values. A work item, when the rule has one, is sent as the
body in Jira's format. The answer reaches later actions as `{{webResponse}}`,
with `{{webResponse.status}}` for its status; an answer outside the 2xx range
fails the rule, and an address on a private network is refused. Work item creation starts a rule through the work item
created trigger below, and a request starts one through the incoming webhook
trigger above.

## The incoming webhook trigger

A rule can instead start from a request. A rule whose trigger type is
`jira.webhook.trigger` is given an address of its own, `/pro/hooks/{token}`,
shown in the editor once the rule is saved. A POST to that address runs the
rule. The token is the credential, as it is in Jira, so the address is a
secret; a rule that also carries a `webhook_secret` checks it, in constant
time, against the request's `X-Automation-Webhook-Token` header.

The trigger's `value` holds `issuesFromWebhook`, which says whether the request
names the work items to run for. A body naming `issues` queues one run for each
work item it names, at most a hundred, skipping any the site does not hold; a
body naming none queues one run with no work item at all. The whole body, which
may be empty, reaches the rule's actions as `{{webhookData}}`, and a path
within it as `{{webhookData.release.version}}`; a path the body does not hold
renders empty, as any unknown smart value does.

An action or condition that needs a work item fails when the request named
none, rather than being skipped, and the run records why. A disabled rule is
not run by its address, and an address naming no rule is not found. Saving a
rule keeps the address it already has.

## Event triggers, conditions and smart values

A rule can instead start when work changes. Its trigger `value` may hold `jql`,
which the work item must match for the rule actor:

| Trigger type | Extra value | Starts when |
|---|---|---|
| `jira.issue.event.trigger:created` | none | A work item is created |
| `jira.issue.event.trigger:transitioned` | optional `fromStatusIds`, `toStatusIds` | A work item's status changes, from and to the listed statuses when given |
| `jira.issue.field.changed` | `fields`, 1 to 20 names such as `summary`, `priority`, `assignee`, `labels` | Any listed field changes |
| `jira.issue.event.trigger:commented` | none | A comment is added |
| `jira.issue.event.trigger:linked` | none | A link is added. A link joins two work items but starts one run, for the outward work item — the side that acts, as an outward work item blocks its inward one |

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
| `jira.issue.related.condition` | `{"relatedType":"linked","jql":"status = Done"}` | Related work matches the JQL: `sub-tasks`, `parent` or `linked`. A blank query holds when any related work exists at all. Linked work may be narrowed by `linkTypes` |

Fields conditions compare `status`, `priority`, `issuetype`, `assignee`,
`reporter`, `labels`, `summary`, `duedate`, `resolution`, `created`,
`resolved`, `parent` or `key`, ignoring case, with `EQUALS`, `NOT_EQUALS`,
`CONTAINS`, `NOT_CONTAINS`, `STARTS_WITH`, `ENDS_WITH`, `IS_ONE_OF`,
`IS_NOT_ONE_OF`, `GREATER_THAN`, `LESS_THAN`, `IS_EMPTY` or `IS_NOT_EMPTY`.
`IS_ONE_OF` and `IS_NOT_ONE_OF` take the values separated by commas.
`GREATER_THAN` and `LESS_THAN` read the ISO day or time that `duedate`,
`created` or `resolved` holds, comparing the days when one side is a day and
the other a time, and hold for nothing on any other field. People match by
account ID or display name, statuses, priorities, work types and resolutions by
name or ID, a parent by its key, summary or ID, and labels by any label.

Label, assignee, comment, edit and condition values render smart values:
`{{issue.key}}`, `{{issue.summary}}`, `{{issue.status.name}}`,
`{{issue.priority.name}}`, `{{issue.issueType.name}}`, `{{issue.dueDate}}`,
`{{issue.labels}}`, `{{issue.assignee.displayName}}`,
`{{issue.assignee.accountId}}`, `{{issue.reporter.displayName}}`,
`{{issue.reporter.accountId}}`, `{{initiator.displayName}}`,
`{{initiator.accountId}}`, `{{triggerIssue.key}}`, `{{triggerIssue.summary}}`,
`{{rule.name}}`, `{{now}}` and `{{now.jiraDate}}`.
The initiator is the person whose change started an event run or who invoked a
manual rule. Unknown smart values render empty, as in Jira.

A `BRANCH` component of type `jira.issue.related` runs its `children`, which
are conditions and actions, once for each related work item the rule actor can
see: the work item's `sub-tasks`, its `parent`, or work `linked` to it, chosen
by `value.relatedType`. A linked branch may list `linkTypes`, matched against
the link as the work item reads it, such as `blocks` or `is blocked by`, or
against the link type's name; without them every link counts. A branch covers
up to 100 work items and cannot contain another branch. Inside it, `issue`
smart values describe the related work item and `{{triggerIssue.key}}` and
`{{triggerIssue.summary}}` the work item the rule started from, and a condition
that does not hold skips only that related work item.

The rule editor at `/settings/automation` offers the scheduled, work item
event and incoming webhook triggers with their options, work item fields and JQL conditions, the
label, assign, transition, comment, edit summary, description, due date and log
work actions, and one
related work items branch placed after them with its own field and JQL
conditions followed by its actions. It does not show triggers, conditions,
actions or branches it cannot edit, such as the manual trigger, a branch whose
conditions come after its actions, or a branch before other components; for
those rules it turns saving off so nothing is lost, and they are changed
through the rule API.

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

Administrators can also browse the catalog at `/settings/automation/templates`.
Each template shows its categories and a form to name the rule, choose the whole
site or a project as its home, fill in its parameters (people and statuses are
chosen from lists) and create it enabled or disabled. The page checks
parameters as the API does and reports a missing required value, a value of the
wrong type or a rule name already in use.

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
