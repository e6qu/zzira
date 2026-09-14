# Scheduled automation and Jira Automation API

ZZIRA implements the Jira Cloud Automation rule-management surface at the
site gateway base path and runs a native, permission-aware subset of scheduled
rules. This is a compatibility slice, not the complete Atlassian Automation
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
trips. The central `api.atlassian.com/automation/public/...` hostname is outside
the ZZIRA base URL and is not served. OAuth 2.0, Forge principals, Atlassian
organization roles, and one-hour cursor expiry are not implemented. Cursors are
opaque encoded offsets and can shift when rules are concurrently added or
deleted.

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

JQL evaluation and every mutation run as the stored rule actor. Issue security
therefore filters the matched set, and command-layer validation applies to
assignment and workflow changes. Runs are capped at 1,000 matching work items;
larger results fail before any actions run.

Unknown triggers, components, conditions, branches, smart values, and connection
payloads remain available through the rule API, but the scheduled worker records
an explicit failed audit entry when asked to execute unsupported behavior. Event
triggers, Cron, condition evaluation, branching, issue/page creation, comments,
email/web requests, usage limits, and the complete Jira action catalog remain
gaps.

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
