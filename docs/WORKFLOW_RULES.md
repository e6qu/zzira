# Jira workflow rules

Workflow transitions carry Jira Cloud's rule shape: `ruleKey` and string
`parameters`, grouped as conditions, validators, post functions (`actions`),
triggers and one transition screen. `GET /rest/api/3/workflows/capabilities`
lists every rule zzira runs. Workflow create, update, preview and both
validation resources accept the same catalog and refuse rules they cannot run.

## System rules

zzira runs the 23 system rules Jira documents.

| Kind | Rules |
|---|---|
| Conditions | `restrict-issue-transition`, `restrict-from-all-users`, `check-field-value`, `previous-status-condition`, `separation-of-duties`, `parent-or-child-blocking-condition`, `block-in-progress-approval`, `jsd-approvals-block-until-approved`, `jsd-approvals-block-until-rejected` |
| Validators | `validate-field-value`, `previous-status-validator`, `check-permission-validator`, `parent-or-child-blocking-validator`, `proforma-forms-attached`, `proforma-forms-submitted` |
| Post functions | `change-assignee`, `update-field`, `copy-value-from-other-field`, `trigger-webhook`, `trigger-agent` |
| Triggers | `development-triggers` |
| Screens | `transition-screen`, `remind-people-to-update-fields` |

Approval conditions read the work item's service approvals.
`block-in-progress-approval` takes no parameters and blocks while any
approval is pending. `jsd-approvals-block-until-approved` and
`jsd-approvals-block-until-rejected` need `approvalConfigurationJson` holding a
JSON object. They allow the transition once no approval is pending and one has
been approved, or rejected. A blocked transition is neither listed nor
accepted.

`remind-people-to-update-fields` takes `remindingFieldIds`, `remindingMessage`
and `remindingAlwaysAsk`. Its fields are accepted as transition inputs, and the
work item page shows the message above them.

`trigger-agent` takes `agentId` and `promptValue`. zzira has no agents of its
own, so running the transition records an agent run request with the
transition's action. The request names the agent account and prompt, and the
agent must be an active app account of the site.

App rules (`connect:` and `forge:`) keep their app key and module key.

## Transition ids

Transitions are identified by Jira's numeric ids. A transition added without
an id, through the REST resources or the browser editor, takes the next
multiple of ten plus one above the workflow's highest id. The default workflow
uses 11, 21 and 31. Migration 196 renumbered transitions that earlier editors
had given generated ids, keeping each workflow's published and draft
definitions in step.

## Transition types

Transitions have Jira's three types:

- `DIRECTED` transitions run from the statuses their links name.
- `GLOBAL` transitions have no source status and run from every status of the
  workflow, including their own destination, as in Jira.
- The `INITIAL` transition (at most one per workflow) creates work items. New
  work starts in its destination status, and its validators and post
  functions run on the submitted values: a field required validator refuses
  the create, change-assignee and field update or copy post functions change
  the new work item, webhook and agent triggers run once it exists, and a
  `customIssueEventId` replaces the Issue created event.

The built-in workflow's initial transition is `1` "Create" into To Do. A
workflow stored without an initial transition starts work in To Do when it
uses that status, and otherwise in its first status; bulk moves that infer a
status use the same rule.

Transitions keep their `description`, `properties` and each link's
`fromPort` and `toPort`; a global or initial transition's single link has no
`fromStatusReference`. Validation reports `TRANSITION_TYPE_INVALID`,
`TRANSITION_INITIAL_DUPLICATE` and, for a global or initial transition that
names a source, `TRANSITION_SOURCE_INVALID`. The browser editor adds a global
transition with the "Any status" source and lists the Create and global
transitions beside the map; the Create transition cannot be deleted there.
