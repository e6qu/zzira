# Workflow rules

Workflow transitions use Jira Cloud's rule shape: a `ruleKey` and string `parameters`, grouped as conditions, validators, post functions (`actions`), triggers and one transition screen. `GET /rest/api/3/workflows/capabilities` lists every rule zzira runs. Workflow create, update, preview and both validation resources accept the same catalog and refuse rules they cannot run. Part of the [Jira platform](JIRA_PLATFORM.md); app-owned rules and workflow history are covered there ([transition rules owned by apps](JIRA_PLATFORM.md#transition-rules-owned-by-apps), [workflow history](JIRA_PLATFORM.md#workflow-history)). Workflows are routed to work types by [workflow schemes](WORKFLOW_SCHEMES.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

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

The `system:transition-screen` rule takes a comma-separated `fields` parameter listing the fields the transition collects (`Transition.ScreenFields`, `internal/workflow/workflow.go`). It does not reference a [screen](SCREENS.md). `GET /issue/{key}/transitions?expand=transitions.fields` reports those fields. Summary, description, labels, assignee, priority, resolution and the site's custom fields can be collected; a resolution needs Resolve issues ([ISSUE_METADATA.md](ISSUE_METADATA.md#on-work-items)).

`development-triggers` offers one trigger type: branch created.

App rules (`connect:` and `forge:`) keep their app key and module key.

## Transition ids

Transitions use Jira's numeric ids. A transition added without an id, through REST or the browser editor, gets the next multiple of ten, plus one, above the workflow's highest id (`NextTransitionID`, `internal/workflow/workflow.go`). The default workflow uses 11, 21 and 31.

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

## Who can work with workflows

Workflow resources follow Jira's permissions:

- Site administrators (Administer Jira) create, validate, update and inspect
  every workflow, global or project-scoped.
- A project's administrators (Administer projects) create, validate and update
  workflows scoped to that project and read their capabilities, but cannot
  create or change global workflows.
- People with Administer projects or View read-only workflow on a project find
  its project-scoped workflows in `GET /workflows/search` and
  `POST /workflows`, and preview the workflows the project uses with
  `POST /workflows/preview`. Global workflows appear only to site
  administrators.
- Anyone else is refused with 401, as Jira documents for these resources.
  Deleting a workflow and the classic `GET /workflow/search` stay with site
  administrators.

## UI

`/settings/workflows` lists workflows. `/settings/workflows/{id}` is the editor: statuses and transitions on a map, per-status approval and editable settings, drafts, and project assignment. Statuses are managed at `/settings/statuses`.

## Code

`internal/workflow/workflow.go`, `internal/api3/api3_workflow_capabilities.go`, `internal/web/directories.go`, `migrations/196_workflow_agent_runs_and_transition_ids.sql`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- A transition screen does not reference a Screen entity; Jira's rule takes a screen id, and the screen's tabs and layout apply.
- `development-triggers` has only the branch-created trigger. Jira also triggers on commits, pull requests, reviews and deployments.
