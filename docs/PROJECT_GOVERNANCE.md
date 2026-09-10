# Jira project governance

Updated: 2026-09-10

This checkpoint adds one workspace-scoped source of truth for Jira project
categories, project properties, software feature states, notification sender
addresses, project types, and project key/name validation. Site and project
administration pages use the same permission-enforcing store mutations as the
Jira Cloud-compatible REST resources.

## Delivered behavior

- Site administrators create, rename, describe, and delete numeric project
  categories in Administration. Category names are unique without regard to
  case. Deleting a category preserves its projects, clears their category, and
  emits project synchronization actions in the same transaction.
- Project creation and detail updates accept Jira's numeric `categoryId`.
  Updating with `-1` removes the category. Project reads, lists, and searches
  return category beans and `categoryId` filtering uses the stored assignment.
- Project properties support ordered key discovery and arbitrary JSON
  get/create/update/delete with Jira's 255-character key and 32,768-byte value
  limits. Create returns `201`, update returns `200`, and delete returns `204`.
- Software projects expose a stable feature catalog and persist `ENABLED` or
  `DISABLED` state. Disabling Backlog or Reports removes the corresponding
  project navigation entry. Non-software projects reject the feature resource.
- Numeric project sender-email resources return the effective site default or
  a project override. An empty update restores the default; writes return
  `204`.
- Project-type discovery exposes the installed business, software, and service
  management products. Licensed-access variants require site access.
- Validation resources report invalid or occupied keys and generate available
  keys and names without leaking another workspace's projects.
- Every category, property, feature, sender, and category-assignment mutation
  is workspace scoped, administrator authorized, transactional, and paired
  with immutable action-log evidence.

## Jira v3 resources

| Resource family | Operations |
|---|---:|
| `/rest/api/3/projectCategory[/{id}]` | 5 |
| `/rest/api/3/project/{idOrKey}/properties[/{key}]` | 4 |
| `/rest/api/3/project/{idOrKey}/features[/{featureKey}]` | 2 |
| `/rest/api/3/project/{projectId}/email` | 2 |
| `/rest/api/3/project/type...` | 4 |
| `/rest/api/3/projectvalidate/...` | 3 |
| Project create/list/search/get/update integration | 5 |
| **Reviewed in this checkpoint** | **25** |

## Known limits

- Project-role administration now follows the assigned scheme's
  `ADMINISTER_PROJECTS` permission. Category, property, feature, and sender
  writes remain site-admin scoped until each family adopts the shared
  permission evaluator.
- Anonymous Browse Projects is not available, so project-property reads require
  an authenticated workspace member.
- The built-in software feature catalog is fixed. App-contributed project
  features, feature images, locked states, and runtime handling for Roadmap,
  Code, and Deployments remain.
- Sender addresses are syntax validated. Custom-domain ownership, verification
  warnings, bounce handling, and outbound notification delivery remain.
- Project types reflect ZZIRA's installed products; Atlassian license discovery
  and product-entitlement billing are outside the self-hosted boundary.
- Valid key generation is deterministic. Jira does not promise the exact
  replacement string, but clients that assume Atlassian's random choice may
  observe a different available key.
- Core project APIs remain partial while expansions, recent projects, scheme
  assignment, import/export, and the full template
  catalog are unfinished.

## Evidence

- `internal/api3/project_governance_test.go` covers allowed and denied users,
  wire bodies, status codes, workspace isolation primitives, numeric IDs,
  feature state, properties, sender email, validation, and category removal.
- `internal/api3/projects_test.go` covers shared project commands, paging,
  filtering, validation, default assignment, action logging, and project API
  integration.
- `e2e/projects.spec.ts` covers the site-admin and project-admin browser journey,
  REST coherence, navigation enforcement, validation recovery, and 320 px
  reflow.
- `migrations/127_project_governance.sql` defines durable category, property,
  feature, and sender state.
