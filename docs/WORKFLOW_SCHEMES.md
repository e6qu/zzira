# Workflow schemes

A workflow scheme routes each work type in a project to a workflow, with a default workflow for any type it does not name. Changes follow Jira's rules. Workflow content and transition rules are in [WORKFLOW_RULES.md](WORKFLOW_RULES.md). Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All paths are under `/rest/api/3/workflowscheme`.

| Path | Methods |
| --- | --- |
| (root) | `GET` (paged, `startAt`, `maxResults`), `POST` |
| `/{id}` | `GET`, `PUT`, `DELETE` |
| `/{id}/default`, `/{id}/workflow`, `/{id}/issuetype/{issueType}` | `GET`, `PUT`, `DELETE` |
| `/{id}/createdraft` | `POST` |
| `/{id}/draft`, `/{id}/draft/default`, `/{id}/draft/workflow`, `/{id}/draft/issuetype/{issueType}` | `GET`, `PUT`, `DELETE` |
| `/{id}/draft/publish` | `POST` |
| `/{id}/projectUsages` | `GET` |
| `/project` | `GET`, `PUT` |
| `/project/switch` | `POST`, answers 303 with a task |
| `/read`, `/update`, `/update/mappings` | `POST` |

## Changing a scheme

- An **inactive** scheme (one no project uses) changes immediately: name, description, default workflow and work type mappings.
- An **active** scheme is never edited in place. Edits go to its draft, which is created on first use, and only when the request sets `updateDraftIfNeeded: true`; otherwise the edit is refused. This applies to `PUT /workflowscheme/{id}` and to the default, workflow and work type mapping operations.
- Publishing a draft migrates work items whose status the new routing lacks, using the status mappings in the publish request.
- Only an active scheme can have a draft, and at most one. A draft records who last changed it and when. Draft reads include the published scheme's `originalDefaultWorkflow` and `originalIssueTypeMappings`.
- Each site has a **default workflow scheme**, which routes every type to the built-in workflow. It is read without an id, cannot be edited or deleted, and is not listed with the site's schemes.

## Reads

- `GET /workflowscheme/project` groups the requested projects by scheme. Unknown projects are skipped.
- Mapping reads (published and draft) list every workflow with its work types when `workflowName` is omitted. Published reads return the draft when `returnDraftIfExists` is set.
- `POST /workflowscheme/read` includes the `taskId` of any unfinished update, switch or publish.
- `POST /workflowscheme/update/mappings` lists, for each workflow the change moves work between, its statuses and the status new work starts in.

## Usages

- **Status project usages:** projects whose work items or boards use the status, and projects whose workflow or workflow scheme (default workflow, work type mappings, or their draft) reaches it.
- **Status workflow usages:** every site workflow whose published or draft definition reaches the status, by wire id, whether or not a project uses it.
- **Status work type usages:** types whose workflow in the project's scheme reaches the status.
- **Workflow scheme usages:** include schemes that reference the workflow only through a draft.
- Workflow search orders by name, creation time or last published update.

## UI

`/settings/workflow-schemes`: saving an inactive scheme publishes the change. Saving a scheme with projects creates a draft to review and publish. The page also assigns projects and deletes schemes.

## Code

`internal/api3/api3_workflow_schemes.go`, `api3_workflow_scheme_drafts.go`, `api3_workflow_scheme_published.go`, `api3_workflow_scheme_bulk.go`, `api3_workflow_usage.go`, `migrations/188_workflow_scheme_default_and_drafts.sql`; tests in `internal/api3/workflow_schemes_test.go`, `status_workflow_usages_test.go` and `e2e/directories.spec.ts`.
