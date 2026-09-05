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
| `jira.issue.transition` | `{"statusId":"st_done"}` | Uses a valid current-workflow transition to the target status |

JQL evaluation and every mutation run as the stored rule actor. Issue security
therefore filters the matched set, and command-layer validation applies to
assignment and workflow changes. Runs are capped at 1,000 matching work items;
larger results fail before any actions run.

Unknown triggers, components, conditions, branches, smart values, and connection
payloads remain available through the rule API, but the scheduled worker records
an explicit failed audit entry when asked to execute unsupported behavior. Event
triggers, Cron, condition evaluation, branching, issue/page creation, comments,
email/web requests, templates, manual-rule APIs, usage limits, and the complete
Jira action catalog remain gaps.

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
