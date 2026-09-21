# Project governance

Project creation, categories, properties, software features, sender
addresses, project types and key/name validation. Browser administration and
the Jira REST resources use the same permission-checked store mutations; every
write is workspace-scoped, transactional and recorded in the action log. Part
of the [Jira platform](JIRA_PLATFORM.md); see
[CLOUD_PARITY.md](CLOUD_PARITY.md) for status. Archive, trash and delete are in
[PROJECT_LIFECYCLE.md](PROJECT_LIFECYCLE.md).

## API

| Route | Behavior |
|---|---|
| `POST /rest/api/3/project` | Create a project (see below) |
| `GET /rest/api/3/project`, `GET /project/search`, `GET/PUT /project/{idOrKey}` | List, search, read and update; `categoryId` filters and assigns (`-1` removes) |
| `GET/POST /rest/api/3/projectCategory`, `GET/PUT/DELETE /projectCategory/{id}` | Categories |
| `GET /rest/api/3/project/{idOrKey}/properties`, `GET/PUT/DELETE …/properties/{key}` | Project properties |
| `GET /rest/api/3/project/{idOrKey}/features`, `PUT …/features/{featureKey}` | Software features |
| `GET/PUT /rest/api/3/project/{projectId}/email` | Sender address |
| `GET /rest/api/3/project/type`, `/type/accessible`, `/type/{key}`, `/type/{key}/accessible` | Project types |
| `GET /rest/api/3/projectvalidate/key`, `/validProjectKey`, `/validProjectName` | Key and name validation and generation |

## Creating projects

- Accepts every template Jira documents for a project type: Scrum, Kanban and
  basic software templates (team-managed ones included), every service
  management template and every business template. Every project is created
  company-managed; Scrum and Kanban templates get a matching board. Customer
  service templates are refused (no customer service product).
- The request can name `permissionScheme`, `notificationScheme`,
  `issueSecurityScheme`, `workflowScheme`, `issueTypeScheme`,
  `issueTypeScreenScheme` and `fieldScheme` (or the deprecated
  `fieldConfigurationScheme`), and a system `avatarId`, all assigned in the
  creating transaction. An unknown scheme or avatar refuses the request, names
  the field and creates nothing.
- The deprecated `lead` is accepted in place of `leadAccountId`, but not
  alongside a different one.

## Behavior

- **Categories.** Numeric IDs; names unique case-insensitively. Deleting a
  category keeps its projects, clears their category and emits project sync
  actions in the same transaction. Project reads return category beans.
- **Properties.** Keys up to 255 characters, JSON values up to 32,768 bytes.
  Create 201, update 200, delete 204.
- **Features.** Software projects have a fixed catalog with persisted
  `ENABLED`/`DISABLED` state and a served `imageUri`. Disabling Backlog or
  Reports removes that navigation entry; Sprints removes sprint creation from
  the backlog; Code hides development information on work items; Deployments
  hides builds and deployments on work items and releases; Roadmap removes the
  Timeline from navigation and answers its page with 404. Other project types
  reject the resource.
- **Sender address.** Returns the site default or the project override. An
  empty update restores the default; writes answer 204. Addresses are syntax
  checked.
- **Project types.** Types follow the site's products: `software` with Jira
  Software, `service_desk` with Jira Service Management, `business` with any
  Jira product. `…/accessible` answers 404 to a person whose application roles
  do not reach that product.
- **Validation** reports invalid or taken keys and generates available keys
  and names without revealing other workspaces' projects. Generated keys are
  deterministic, where Atlassian's are random.

## Permissions

| Action | Needs |
|---|---|
| Category writes | Site or organization administrator |
| Property, feature and sender writes | Administer projects on the project |
| Property reads | Browse projects (anonymous when allowed; see [ANONYMOUS_ACCESS.md](ANONYMOUS_ACCESS.md)) |
| Licensed-access type reads | Site access |

## UI

- **Administration** (`/admin`): create, rename, describe and delete
  categories.
- **Project settings** (`/projects/{key}/settings`): details, category,
  features, sender address and properties. A project's administrators reach
  it; saving the project as a site-wide template, and archiving or trashing
  it, are site administration and are shown only to site administrators.
  When the project shares a workflow, the page offers to start one of its own.
- **Configuration** (`/projects/{key}/settings/configuration`): every scheme
  the project routes through -- permission, notification, issue security,
  workflow scheme and workflow, work type, work type screen, field
  configuration and priority -- with the page that changes each one, and the
  site default named where the project has no scheme of its own.
- **Projects** (`/projects`, `/projects/new`): directory and creation.

## Gaps

See [PLAN.md](../PLAN.md).

- The project sender address is stored but not used as the From address of
  notification email; no custom-domain verification or bounce handling.
- Connect apps cannot contribute project features.

## Tests

`internal/api3/project_governance_test.go`, `internal/api3/projects_test.go`,
`e2e/projects.spec.ts`, `e2e/project_workflow_admin.spec.ts`.

## See also

[PROJECT_LIFECYCLE.md](PROJECT_LIFECYCLE.md) · [PROJECT_ROLES.md](PROJECT_ROLES.md) ·
[COMPONENTS.md](COMPONENTS.md) · [PROJECT_VERSIONS.md](PROJECT_VERSIONS.md) ·
[CLASSIFICATION_LEVELS.md](CLASSIFICATION_LEVELS.md) ·
[PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)
