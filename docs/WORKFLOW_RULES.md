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
