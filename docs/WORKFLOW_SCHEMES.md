# Workflow schemes

A workflow scheme routes each work item type in a project to a workflow, with
a default workflow for every type it does not name. ZZIRA follows Jira's rules
for changing one.

## Changing a scheme

- An **inactive** scheme, one no project uses, changes directly: its name,
  description, default workflow and type mappings update at once.
- An **active** scheme, one a project uses, never changes under the project's
  work. An edit goes to the scheme's draft, created on first use, but only when
  the request sets `updateDraftIfNeeded` to `true`; without it the edit is
  refused. This applies to `PUT /workflowscheme/{id}` and to the default,
  workflow and issue type mapping operations. Publishing the draft migrates
  work whose status the new routing does not include, using the status mappings
  the publish request supplies.
- Only an active scheme can have a draft, and only one. A draft records who last
  changed it and when, and reads of it carry the published scheme's
  `originalDefaultWorkflow` and `originalIssueTypeMappings`.
- Each site has a **default workflow scheme**, which routes every type to the
  built-in workflow. It is read without an id, as Jira returns it, cannot be
  edited or deleted, and is not listed among the site's schemes.

The settings page follows the same rules: saving an inactive scheme publishes
the change, while a scheme with projects saves a draft to review and publish.

## Reads

- `GET /workflowscheme` pages with `startAt` and `maxResults`.
- `GET /workflowscheme/project` groups the requested projects by the scheme each
  uses.
- The workflow mapping reads, published and draft, list every workflow with its
  work item types when no `workflowName` is given, and the published reads use
  the draft when `returnDraftIfExists` asks.
- `POST /workflowscheme/read` names the `taskId` of an unfinished update, switch
  or publish of the scheme.
- `POST /workflowscheme/update/mappings` lists, for each workflow the change
  moves work between, its statuses and the status new work starts in.

## Usages

A status's project usages include projects whose work or boards use it and
projects whose own workflow or workflow scheme — default workflow, work type
mappings, or their draft — reaches it. Workflow usages list every site workflow
whose published or draft definition reaches the status, named by the workflow
ids clients see, whether or not a project uses the workflow; work type usages
include the types whose workflow in the project's scheme reaches the status. A
workflow's scheme usages count schemes that reference it only through a draft,
and workflow search orders by name, creation or last published update.
`internal/api3/status_workflow_usages_test.go` covers these.

## Evidence

- `internal/api3/workflow_schemes_test.go` covers direct and draft edits, the
  refusals, draft metadata, default-scheme protection, paging, grouped project
  associations, unnamed mapping reads, task ids and status mappings.
- `e2e/directories.spec.ts` saves an inactive scheme directly, assigns it, then
  saves and publishes a draft.
- `migrations/188_workflow_scheme_default_and_drafts.sql` provisions the default
  scheme for every site and records draft modifications.
