# Jira Automation

Automation rules are managed through Atlassian's Automation REST API and the
rule editor, and a durable worker runs scheduled, event, incoming-webhook and
manually triggered rules as the rule actor. Rules can hold any component JSON;
the worker executes the catalog below and records a failed run for anything
else. Status: [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

Base paths (the Cloud ID comes from `GET /_edge/tenant_info`):

- `/gateway/api/automation/public/jira/{cloudid}/rest/{v1|latest}`
- `/automation/public/jira/{cloudid}/rest/{v1|latest}`

| Method | Path | Who | Behavior |
|---|---|---|---|
| GET | `/rule/summary` | admin | Cursor-paged rule summaries |
| POST | `/rule/summary` | admin | Summary search by state, trigger, scope or author |
| POST | `/rule` | admin | Create; omitted UUIDs become UUIDv7 |
| GET | `/rule/{ruleUuid}` | admin | Full rule and its connections |
| PUT | `/rule/{ruleUuid}` | admin | Replace, keeping the path UUID |
| DELETE | `/rule/{ruleUuid}` | admin | Delete a disabled rule (else 400 `automation.rule.must_be_disabled`) |
| PUT | `/rule/{ruleUuid}/state` | admin | `ENABLED` or `DISABLED` |
| PUT | `/rule/{ruleUuid}/rule-scope` | admin | Replace rule-scope ARIs |
| POST | `/rule/manual/search` | member | Manual rules for `objects` (issue ARIs) or `cursor`, `limit` 1–100 (default 50) |
| GET | `/rule/manual/search?cursor=` | member | Continue a search; only `cursor` and `limit` |
| POST | `/rule/manual/{ruleId}/invocation` | member | Run for 1–50 objects with `userInputs` |
| GET/POST | `/template/search` | member | Template catalog by `categories` and `ruleHome`, cursor-paged |
| GET | `/template/{templateId}` | member | One template with categories and typed parameters |
| POST | `/template/create` | admin | Rule from `templateId`, `ruleHome`, `parameters` (`{type, value}`), optional `state`; returns `{ruleUuid}` |

- **Auth:** API tokens and browser sessions, as on Jira REST. "admin" is
  workspace administration. OAuth 2.0, Forge principals and organization roles
  are not accepted.
- **Errors:** Automation's `errors[]` shape (`id`, `status`, `code`, `title`,
  optional `field`).
- **Redaction:** `redactSensitiveFields=true` masks common secret fields in rule
  and connection payloads.
- **Cursors:** opaque offsets that expire after one hour (400) and can shift
  when rules are added or deleted concurrently.
- **Manual search:** returns enabled manual rules whose scope covers every
  object's project, as `data` of `{id, name, userInputs}` with `links.self`,
  `next`, `prev`. An invisible issue is 403.
- **Invocation:** runs immediately and records a run. Per-ARI results are
  `SUCCESS`, `INVALID_TARGET_OBJECT`, `INVALID_RULE_OR_OBJECT` or
  `INVALID_TARGET_SCOPE`. A missing required input is 400; an unknown rule is
  404.
- **Templates:** a missing, mistyped or unknown parameter, or a rule home
  outside the site, is 400.

## UI

- `/settings/automation`: rule list, editor, audit log and **Run now**
  (administrators).
- `/settings/automation/templates`: template gallery. It creates a rule for the
  site or a project, enabled or disabled, and validates parameters as the API
  does.
- **Run automation** on a work item: the manual rules whose scope covers its
  project, each with the questions it asks. The rules are read when the control
  is opened rather than with the work item, which is read far more often than a
  rule is run on it. A required question that was not answered refuses the run
  and says so under the control; a rule that ran re-renders the work item,
  because it has just changed it.

The editor offers every trigger, condition and action in the tables below and
one branch placed last (its conditions, then its actions). A create variable
action names its variable in the row's **More for this action** fold; a branch
over work matching JQL takes its query beside the link types. If a rule holds anything the editor cannot show,
saving is turned off; change such rules through the API.

## Triggers

| Type | Value | Starts when |
|---|---|---|
| `jira.jql.scheduled`, `jira.issue.scheduled` | `intervalMinutes` (1–43,200) or a cron schedule; `timezone`; `jql` | On schedule, for each matching work item (at most 1,000, else the run fails before acting) |
| `jira.issue.event.trigger:created` | optional `jql` | A work item is created |
| `jira.issue.event.trigger:transitioned` | optional `fromStatusIds`, `toStatusIds`, `jql` | Status changes (between the listed statuses, if given) |
| `jira.issue.field.changed` | `fields` (1–20, e.g. `summary`, `priority`, `assignee`, `labels`, `sprint`), optional `jql` | Any listed field changes. `sprint` changes when work joins or leaves a sprint, as it does in Jira; ranking work already in one is not a change |
| `jira.issue.event.trigger:commented` | optional `jql` | A comment is added |
| `jira.issue.event.trigger:linked` | optional `jql` | A link is added; one run, for the outward work item |
| `jira.issue.event.trigger:assigned` | optional `jql` | The assignee changes, including to nobody |
| `jira.issue.attachment.added` | optional `jql` | An attachment is added, for the work item it was added to |
| `jira.webhook.trigger` | `issuesFromWebhook` | `POST /pro/hooks/{token}` |
| `jira.issue.event.trigger:moved` | optional `jql` | A work item moves to another project, for the work item that moved |
| `jira.issue.event.trigger:deleted` | none | A work item is deleted; the run has no work item and reads `{{deletedIssue.*}}` |
| `jira.version.event.trigger:created`, `:updated`, `:released` | none | A version is created, changed, or released; the run has no work item and reads `{{version.*}}` |
| `jira.sprint.event.trigger:started`, `:completed` | none | A sprint starts or completes; the run has no work item and reads `{{sprint.*}}` |
| `confluence.page.created` | none | A page is published in the wiki; the run has no work item and reads `{{page.*}}` |
| `confluence.page.updated` | none | A published page is written again; `{{page.version.number}}` says which version |
| `confluence.page.commented` | none | A comment is added to a page, footer or inline; the run reads `{{comment.*}}` |
| `confluence.page.labelled` | none | A label is attached to a page; the run reads `{{label.name}}` |
| `confluence.blogpost.created` | none | A blog post is published; the run reads `{{blogPost.*}}` |
| `jira.manual.trigger.issue.action` | `inputPrompts` (`displayName`, `inputType`, `required`, `variableName`) | Somebody runs it from a work item, through the API or **Run automation**; the answers are `{{userInputs.*}}` |

**Scheduling.** Fixed intervals measure elapsed time, so they ignore
daylight-saving shifts. A cron schedule is given as
`"schedule": {"method": "CRON_EXPRESSION", "cronExpression": "0 0 9 ? * MON-FRI"}`
or as a top-level `cronExpression`, and runs in the rule's timezone.
- The seconds field must be a single number.
- Exactly one of day-of-month and day-of-week must be `?`.
- `L`, `W` and `#` are refused.
- Local times skipped by a daylight-saving change do not fire.
- A missed time runs once.
- Disabling a rule clears its next run time.

**Wiki content.** A rule a page starts runs as its actor and reads the page as
its actor: a page in a space the actor may not read starts nothing at all, so
neither its title nor its body reaches a rule's actions. A draft is not a page
yet and a trashed one is not a change, so neither starts anything; only the
first comment on a page is a comment (editing one is not), and only attaching a
label is labelling. A label or a comment on an attachment, a blog post or
custom content is not a page event.

**Events with no work item.** A deletion, a version and a sprint do not happen
to a work item a rule can read: the deleted one is gone, and the others are not
work items at all. Such a rule runs once, with no work item, as an incoming
webhook that named none does -- so it takes no JQL (400 through the API, a
refusal in the editor), and an action that needs a work item fails and says so.
Raising work and sending a request are the two that do not. What the event
happened to travels with the run rather than being read back later, because by
then the deleted work item is gone and the version is whatever it is now.

A version reports its release once: saving a version that is already released
is an update, not another release. A sprint reports starting and completing the
same way, from the state it was in before the change.

**Events.** The worker reads the action log in order and queues one run per rule
and event, so a retried batch never repeats a run. Enabling a rule does not
replay earlier events. An event run acts only if the actor can still see the
work item, the item is unarchived, in the rule's scope and matches `jql`. A rule
never triggers itself; another rule's changes trigger it only when
`canOtherRuleTrigger` is true.

**Incoming webhook.** The rule is given `/pro/hooks/{token}` once it is saved,
and keeps that address when edited again. The token is the credential. If the
rule has a `webhook_secret`, the request's `X-Automation-Webhook-Token` must
match it (constant-time comparison).
- A body with `issues` queues one run per named work item that exists (at most
  100); otherwise one run with no work item.
- The body is `{{webhookData}}`, and paths into it such as
  `{{webhookData.release.version}}` work too.
- A component that needs a work item fails when there is none.
- A disabled rule does not run; an unknown token is 404.

## Conditions

A `CONDITION` component that fails stops the rule for that work item.

| Type | Value | Holds when |
|---|---|---|
| `jira.jql.condition` | `{"jql": "priority = High"}` | The work item matches, for the rule actor |
| `jira.issue.condition` | `{"field", "operator", "value"}` | The field compares as asked |
| `jira.issue.related.condition` | `{"relatedType": "sub-tasks"\|"parent"\|"linked", "jql", "linkTypes"}` | Related work matches; a blank `jql` holds if any related work exists |

- **Fields:** `status`, `priority`, `issuetype`, `assignee`, `reporter`,
  `labels`, `summary`, `duedate`, `resolution`, `created`, `resolved`,
  `parent`, `key`. Comparisons ignore case.
- **Operators:** `EQUALS`, `NOT_EQUALS`, `CONTAINS`, `NOT_CONTAINS`,
  `STARTS_WITH`, `ENDS_WITH`, `IS_ONE_OF`, `IS_NOT_ONE_OF` (comma-separated),
  `GREATER_THAN`, `LESS_THAN`, `IS_EMPTY`, `IS_NOT_EMPTY`.
  - `GREATER_THAN` and `LESS_THAN` work only on `duedate`, `created` and
    `resolved`. They compare ISO dates or times, and compare by day when one
    side is a date.
- **How values match:**
  - People: account ID or display name.
  - Status, priority, work type and resolution: name or ID.
  - Parent: key, summary or ID.
  - Labels: any one label.

## Actions

Actions run in order, as the rule actor, through the normal command layer.
Each action sees the work item as the previous one left it. Values render smart
values. Most actions set a desired state, so a replayed action does nothing.

| Type | Value | Behavior |
|---|---|---|
| `jira.issue.add-label` | `label` | Adds if absent |
| `jira.issue.remove-label` | `label` | Removes if present |
| `jira.issue.assign` | `accountId` (`ACTOR`, `UNASSIGNED`) or `method` | `round-robin` picks whoever waited longest, `balanced` the fewest unresolved items, `random` anyone. Candidates are the project's assignee picker; no candidates stops the rule |
| `jira.issue.transition` | `statusId` | Uses a valid transition of the current workflow |
| `jira.issue.comment` | `comment` | Adds a comment |
| `jira.issue.edit` | `field`, `value` | Sets `summary`, `duedate` (yyyy-MM-dd, blank clears), `priority` (name or ID), `description` or `labels` (comma-separated, replaces all) |
| `jira.issue.log-work` | `duration` (e.g. `3h 30m`), optional `comment` | Uses the site's time tracking settings; zero or less stops the rule |
| `jira.issue.delete` | `{}` | Needs Delete issues. Sub-tasks are handled as in a normal delete. Nothing after it runs |
| `jira.issue.create-subtask` | `summary` | Uses the scheme's sub-task type. A sub-task with the same summary counts as done. Fails on a sub-task, or when the project has no sub-task type |
| `jira.issue.create` | `issueTypeId`, `summary`, optional `projectId` | Creates with a type from the project's scheme, not a sub-task type. The project is the one named, else the triggering work item's, else the single project the rule is scoped to; without any of those the action fails rather than the run. The same type and summary already existing counts as done |
| `jira.issue.link` | `linkTypeId`, `issueKey` | The named item takes the inward side. Existing links and self-links count as done; a key from another site fails |
| `jira.issue.email` | `recipient` (`assignee`, `reporter`, `watchers`), `body` | Plain text through the mail outbox. Subject is key and summary. Skips people without an address and deactivated accounts |
| `jira.issue.outgoing-webhook` | `method` (GET, POST, PUT, DELETE), `url`, optional `body` and `headers` | Sends the request. A rule that writes a `body` sends that, with smart values rendered, whatever the method; otherwise POST and PUT carry the work item in Jira format (or `{}`). `headers` is a name-to-value map, rendered the same way, and sets what it names -- `Host` and `Content-Length` belong to the connection and are refused, as is a name HTTP does not allow. Response is `{{webResponse}}` / `{{webResponse.status}}` (64 KiB kept). Non-2xx fails; private-network hosts are refused; 20 s timeout |
| `confluence.page.create` | `spaceKey`, optional `title` | Creates a page as the actor (fails without view and create rights). Default title is the key and summary; the page names the rule and the work item |
| `jira.create.variable` | `variableName`, `variableValue` | Names a value for the rest of the rule, read as `{{variableName}}`. The value is rendered before it is stored, so a variable can be built from other smart values. A name is a letter then letters, digits, `_` or `-`, at most 64, and cannot be one of the smart values a rule already has (`issue`, `triggerIssue`, `initiator`, `rule`, `now`, `webResponse`, `webhookData`, `userInputs`, `version`, `sprint`, `deletedIssue`). Setting one again replaces it. At most 50 per run, 32,768 characters each. Changes nothing about the work, so a rule whose only action is this one reports no action |

`jira.issue.create`, `jira.issue.outgoing-webhook`, `jira.create.variable` and
`confluence.page.create` run without a work item. The others need one.

## Branches

A `BRANCH` of type `jira.issue.related` runs its `children` (conditions and
actions) once for each work item it finds that the actor can see.
- `value.relatedType` picks `sub-tasks`, `parent`, `linked` or `jql`.
- `linkTypes` narrows linked work by phrase (`blocks`, `is blocked by`) or by
  link type name.
- `jql` is the query a `jql` branch runs for. Smart values in it are rendered
  first, so a branch can ask about what an earlier action found. A `jql` branch
  needs no work item, so a rule a version, a sprint or a deletion started can
  still branch over work.
- Limits: at most 100 items, and no nested branches.
- Inside the branch, `issue` means the item the branch is running for and
  `triggerIssue` means the work item the rule started from.
- A condition that fails skips only that item.
- A variable a branch names belongs to the branch: each item starts from what
  the rule had named outside it, and what the branch names is gone afterwards.
- A related work items *condition* takes `sub-tasks`, `parent` or `linked`
  only; asking what a query matches is what `jira.jql.condition` is for.

## Smart values

`{{issue.key}}`, `{{issue.summary}}`, `{{issue.status.name}}`,
`{{issue.priority.name}}`, `{{issue.issueType.name}}`, `{{issue.dueDate}}`,
`{{issue.labels}}`, `{{issue.assignee.displayName|accountId}}`,
`{{issue.reporter.displayName|accountId}}`,
`{{initiator.displayName|accountId}}`,
`{{triggerIssue.key}}`, `{{triggerIssue.summary}}`, `{{rule.name}}`,
`{{now}}`, `{{now.jiraDate}}`, `{{webResponse}}`, `{{webResponse.body}}`,
`{{webResponse.status}}`, `{{webhookData}}` and `{{webhookData.<path>}}`.

An event with no work item carries what it happened to:
`{{deletedIssue.key}}`, `{{deletedIssue.summary}}`, `{{deletedIssue.reason}}`,
`{{version.name}}`, `{{version.released}}`, `{{sprint.name}}`,
`{{sprint.goal}}`, `{{sprint.state}}`, and any other path into them.

A wiki event carries the content it happened to: `{{page.title}}`,
`{{page.id}}`, `{{page.spaceId}}`, `{{page.version.number}}`,
`{{blogPost.title}}`, `{{comment.body.value}}`, `{{label.name}}`, and any other
path into them.

A rule's own variables are read by the name the create variable action gave
them: `{{release_note}}`.

A manual run carries what it was asked: `{{userInputs.<variableName>}}` is the
answer to that rule's question, and is empty when the question was optional and
went unanswered.

The initiator is whoever made the change behind an event run, or whoever
invoked a manual rule. Unknown values render empty. Rendered text is limited to
32,768 characters.

## Templates

| Template | Categories | Parameters |
|---|---|---|
| `scheduled-label-stale-work` | popular, scheduled, work item management | `label` (TEXT), `days` (NUMBER) |
| `scheduled-assign-unassigned` | software, scheduled, work item management | `assigneeAccountId` (TEXT, required) |
| `manual-assign-to-me` | popular, manually triggered, work item management | none |
| `manual-transition` | software, manually triggered, work item management | `statusId` (TEXT, required) |

## Execution and audit

- **Runs:** each run is unique per rule and scheduled time, or per rule and
  event. Workers claim runs with `FOR UPDATE SKIP LOCKED`, and a `RUNNING`
  claim older than two minutes can be reclaimed. **Run now** uses the same
  queue.
- **Retries:** exponential backoff capped at 30 minutes. Ten failures in a row
  disable the rule; a successful or no-action run resets the count.
- **Audit log:** scheduled time, state (`SUCCESS`, `NO_ACTIONS`, `FAILED`),
  attempts, duration, how many work items matched and changed, and the last
  error.
- **Visibility:** JQL and every change run as the rule actor, so issue security
  and command validation apply.

## Gaps

See [PLAN.md](../PLAN.md).
- Connections: stored, returned and redacted by the API, but no action uses
  them.
- Confluence actions beyond creating a page, and triggers for spaces,
  attachments and custom content.
- Usage limits: no monthly execution quota or per-rule usage tracking.
- The rest of Jira's trigger, condition, action and branch catalog, including
  for-each branches over a smart value's list, and branching over the work a
  rule created.
- Running a manual rule over a selection of work items: **Run automation** runs
  it on the work item it is on, while the API takes up to fifty objects.

## See also

- [JIRA_PLATFORM.md](JIRA_PLATFORM.md)
- [SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md)
- [JQL.md](JQL.md)
- [Atlassian Automation REST API](https://developer.atlassian.com/cloud/automation/rest/)
- [Automation triggers](https://support.atlassian.com/cloud-automation/docs/jira-automation-triggers/)
