# Development continuity

Updated: 2026-09-06

This file is the local handoff for the Jira Cloud completion program. Keep it
short, factual, and current after each meaningful commit. Stable architecture
and final acceptance rules belong in [PLAN.md](../PLAN.md).

## Active delivery

- Branch: `feat/cloud-surface-completion`
- Base: `origin/main` after PR #65, scheduled automation
- Delivery shape: one pull request with ordered, independently green commits
- Last completed workstream: 3 of 15 — provider-based authentication
- Active workstream: 4 of 15 — Jira administration and workflows
- Blockers: none

## Last verified baseline

The merged product includes core work items, projects, people, search, issue
triage, boards, backlog and sprint lifecycle, notifications, elementary workflow
administration, releases, configurable dashboards, initial wiki spaces/pages,
and durable fixed-interval automation. Password, API-token, browser-session,
generic OIDC, Google, tenant-scoped Microsoft, and Atlassian provider sign-in
are present.

The pinned contract inventory contains 1,207 operations:

| Contract | Operations |
|---|---:|
| Jira Cloud Platform REST v3 | 617 |
| Jira Software Cloud REST | 105 |
| Jira Service Management Cloud REST | 75 |
| Confluence Cloud REST v1 | 130 |
| Confluence Cloud REST v2 | 218 |
| Automation REST | 15 |
| Organizations REST | 47 |

This is an inventory denominator, not an implementation count. App descriptors
and module contracts need a separately versioned manual ledger because Atlassian
does not publish them as one OpenAPI document.

## Completed in this branch

- Replaced the obsolete V0–V6 future roadmap with the active one-PR execution
  map and removed landed follow-up sections.
- Added this handoff and assigned clear ownership to planning, parity, matrix,
  and surface documents.
- Pinned Jira Service Management, Confluence v1, Automation, and Organizations
  OpenAPI contracts, increasing the reproducible denominator from 940 to 1,207.
- Added contract family, host class, source metadata, stable operation IDs,
  strict pin validation, and focused generator tests.
- Added exact-operation coverage generation. The first reviewed assessment
  records Automation as 8 partial and 7 missing operations. The organization
  slices assess all 47 Organizations operations as partial; 1,145 operations
  remain explicitly unassessed rather than being inferred from route names.
- Added organization, site, product, internal-directory, directory-user, group,
  group-member, role-binding, and organization-audit persistence with automatic
  provisioning for new workspaces and a repair path for older workspaces.
- Migrated legacy workspace roles into centrally evaluated site and product
  bindings, including group-derived administration and immediate revocation.
- Added bearer-authenticated organization discovery, directory/group listing,
  group creation, and group-membership mutation endpoints with strict request,
  pagination, conflict, and authorization behavior.
- Added the `/admin` journey for organization context, product visibility,
  group creation, member assignment/removal, and the resulting audit trail.
- Fixed bootstrap administration so an existing member is promoted when the
  configured bootstrap identity already exists.
- Made bootstrap and first-time OIDC assignment prefer `ws_default`
  deterministically when additional workspaces exist.
- Added product-workspace discovery with Atlassian resource identifiers plus
  direct and group role assignment/revocation APIs. Effective user access
  reports direct and group-derived methods.
- Extended `/admin` so site administrators grant and revoke each directory
  group's Jira Software, Jira Service Management, and Confluence access.
- Added managed-account list/detail/invite/suspend/restore/remove APIs and UI.
  Suspension and removal revoke sessions and API tokens after the final active
  directory, mutations are audited, and administrators cannot suspend or remove
  themselves.
- Added group detail, filtered count, search, statistics, and audited deletion,
  plus directory-user search and statistics. Search supports documented
  identity, directory, membership, lifecycle, resource, role, domain, sort,
  expansion, all-directory, and opaque-pagination behavior.
- Completed invitation-time product roles and group membership with per-account
  result details and 206 partial responses. Optional email delivery now uses a
  configured SMTP sender and a durable, leased, bounded-retry outbox; absent or
  partial SMTP configuration fails explicitly.
- Added stored managed-account profiles and an audited administrator editing
  journey. Directory memberships now carry independent active/suspended state;
  access is removed only in that directory, and sessions and API tokens are
  revoked after the account loses its final active directory.
- Completed the remaining directory-user contract operation with durable
  two-second visible product activity and exact per-product last-active dates,
  mapped product keys, organization-add timestamps, and cursor validation.
- Exposed immutable organization audit evidence through event query, polling,
  detail, and action-catalog APIs. Added text/action/actor/network/product/time
  filters, ascending polling cursors, and shared browser audit search.
- Added durable organization domain claims with strict DNS normalization,
  generated TXT challenges, fail-closed verification, removal, audit evidence,
  administrator UI, and exact domain list/detail API resources.
- Completed reviewed coverage of the Organizations contract with nine policy
  operations: policy CRUD/filtering, product-resource add/update/remove, and
  validation. Added IP/CIDR and resource ownership validation, audited state,
  and the administrator create/scope/enable/disable/delete journey.
- Added a simultaneous identity-provider registry for Shauth-compatible OIDC,
  Google OIDC, tenant-scoped Microsoft Entra OIDC, and Atlassian OAuth 2.0 3LO
  with its User Identity API. Provider callbacks use provider-bound single-use
  state, OIDC nonce and PKCE where supported, strict claim/profile validation,
  immutable issuer/subject identities, and exact tenant issuer checks.
- Allowed one account to link several configured-provider identities by their
  asserted email, recorded successful provider sessions in every applicable
  organization audit log, selected RP-initiated logout by session issuer, and preserved
  Shauth back-channel logout behavior.
- Added responsive login provider choice and read-only provider status in
  organization administration. Deployment secrets remain environment-owned and
  are never rendered into the application.
- Added a self-service profile journey to review each provider's issuer,
  asserted email, immutable subject, and connection time; connect an additional
  provider through a user-bound authorization transaction; and disconnect it.
  Disconnect revokes sessions for that issuer, preserves other-provider
  sessions, refuses the final external identity, and writes link/unlink audits.
- Added durable organization provider availability settings. Administrators can
  disable or enable each configured provider; disabling removes its login/link
  entry points, revokes every session from its issuer in the same transaction,
  and records the action. Startup reapplies the stored setting.
- Closed the browser-replica revocation boundary. Reconnect now verifies access
  before replaying queued commands; a 401 or 403 atomically clears private
  SQLite materializations, action checkpoints, and the remaining outbox, clears
  authenticated page caches, rotates the replica identity, and signs the user
  out. Offline network transitions cancel in-flight worker requests, and
  reconnect maintenance yields the JavaScript event loop before fetching.
- Added administrator-managed custom OpenID Connect providers. A deployment
  master key enables AES-256-GCM storage bound to workspace/provider context;
  registration and secret rotation validate live discovery before publishing
  the new configuration, secrets are never rendered, environment-owned keys
  cannot be overwritten, and deletion revokes issuer sessions. All lifecycle
  changes are durable and audited, including across server restart.
- Added versioned workflow drafts. Transition edits and deletes now change an
  editor draft while assigned projects continue enforcing the published
  definition. Administrators explicitly publish or discard; publishing bumps
  the version, both outcomes write organization audit evidence, and the
  workflow directory exposes pending drafts.
- Added a workspace status directory and Jira status REST subset. Administrators
  create, classify, describe, edit, and delete custom statuses; built-ins are
  protected, visible names are unique, all changes are audited, and deletion is
  blocked while issues, boards, published/draft workflows, or automation rules
  refer to the status. Editors and REST reads exclude other workspaces' custom
  statuses.
- Exposed paged Jira status project, workflow, and per-project issue-type usage
  resources from the same impact data, bringing every pinned status and status
  category operation under explicit reviewed evidence.
- Made every custom workflow workspace-owned. Migration 046 assigns legacy
  definitions from their project usage, UI and Jira API discovery exclude other
  sites, draft and publish writes require the owning workspace, and project
  assignment rejects foreign workflow IDs. The built-in default remains shared.
- Added workflow schemes with migration 047. Schemes route issue types to a
  published default or override workflow, isolate drafts from issue runtime,
  validate every workflow and issue type in the workspace, and refuse publish
  or project assignment when an existing issue status would be stranded. The
  administrator UI covers create, mapping edits, publish/discard, project usage,
  impact preview and assignment; seven core Jira workflow-scheme operations use
  the same storage and authorization boundaries.
- Added atomic workflow-scheme switching with explicit status replacement. The
  administrator preview offers only statuses in each target workflow; the Jira
  switch request accepts per-issue-type mappings, updates affected work items
  with synchronized status actions, assigns the published scheme in the same
  transaction, and returns Jira's 303 task representation. Migration 048
  persists that task so the Location can be polled after redirects or a server
  restart; task access is limited to its submitter and administrators.
- Added fourteen Jira workflow-scheme draft resources. Administrators can
  explicitly create, read, partially update, discard, validate, and publish a
  draft; default, issue-type, and workflow-group mappings share the same
  workspace validation and audited store path. Normal publish returns a
  durable queued task while validation-only requests leave state untouched.
- Added ten published workflow-scheme mapping resources. Default, issue-type,
  and workflow-group mutations validate assigned project impact before they
  version and audit the live scheme; project usage returns workspace-scoped
  opaque cursor pages with Jira's 1–200 result limit.
- Added Jira's three bulk workflow-scheme operations. Bulk reads deduplicate
  explicit and project-derived scheme IDs and return workflow metadata and
  document versions; required-mapping analysis reports the actual statuses
  affected across assigned projects; updates enforce optimistic versions and
  return durable tasks while refusing an unsafe unmapped change.
- Completed status replacement for draft publish and bulk update. Both request
  shapes now migrate every affected issue across all assigned projects, emit
  ordinary synchronized status actions, update the scheme, increment its
  version, write audit evidence, and persist the completed task in one database
  transaction. Workflow-level mappings expand by old/new workflow pair and
  issue-type overrides take precedence.

Validation after the organization foundation:

- 7 focused Python conformance tests pass.
- Inventory and coverage generated files pass their `--check` modes.
- All Go packages pass with the PostgreSQL integration suite enabled.
- `go vet ./...` passes.
- `GOOS=js GOARCH=wasm go build ./...` passes.
- All 45 Chromium user-journey tests pass, including WCAG scans in light and
  dark themes and 320 px reflow for the new administration page.
- `git diff --check` passes.

Validation after product access and role assignments:

- All Go packages pass with the PostgreSQL integration suite enabled.
- `go vet ./...`, the WebAssembly build, and all conformance checks pass.
- The focused administration browser journey passes with account lifecycle,
  product grant, effective membership, audit, revocation, accessibility, and
  reflow coverage.

Validation after group and directory search completion:

- All Go packages pass with the PostgreSQL integration suite enabled.
- `go vet ./...`, the WebAssembly build, and all conformance checks pass.
- The focused Chromium administration journey passes group deletion, audit,
  WCAG scans in light and dark themes, and 320 px reflow.
- Exact coverage at this checkpoint was 46 of 1,207 operations assessed: 39
  partial, 7 missing, and 1,161 explicitly unassessed.

Validation after invitation access and delivery:

- All Go packages pass with migration 034 and the PostgreSQL integration suite
  enabled; SMTP protocol delivery and durable outbox state are covered.
- `go vet ./...`, the WebAssembly build, and all conformance checks pass.
- The focused Chromium journey passes invitation-time product/group access,
  account cleanup, WCAG scans, dark mode, and 320 px reflow.

Validation after managed profiles and directory-scoped suspension:

- Focused store, organization API, web, authorization, and server package tests
  pass with migration 035 and PostgreSQL enabled.
- The focused Chromium administration journey passes profile editing, account
  lifecycle, WCAG scans in light and dark themes, and 320 px reflow.
- `git diff --check` passes.

Validation after directory-user activity completion:

- The focused organization API integration test passes with migration 036,
  empty and populated product activity, mapped Jira Service Management keys,
  timestamps, missing users, and malformed cursor behavior.
- The focused Chromium administration journey proves that a visible product
  page records activity only after its two-second threshold.
- Exact coverage is 47 of 1,207 operations assessed: 40 partial, 7 missing, and
  1,160 explicitly unassessed.

Validation after organization audit completion:

- The organization API integration journey passes query validation and filters,
  500-item limits, ascending polling and next cursors, event detail, missing
  events, and the action catalog.
- The focused Chromium administration journey passes audit text/action filters,
  clear-filter navigation, WCAG scans, dark mode, and 320 px reflow.
- Exact coverage is 51 of 1,207 operations assessed: 44 partial, 7 missing, and
  1,156 explicitly unassessed.

Validation after organization domain claims:

- The organization API integration journey passes empty/populated domain lists,
  normalization, invalid input, opaque cursor rejection, detail/not-found, and
  verified claim state with migration 037.
- The focused Chromium administration journey passes domain add/remove, TXT
  challenge visibility, WCAG scans, dark mode, and 320 px reflow.
- Exact coverage is 53 of 1,207 operations assessed: 46 partial, 7 missing, and
  1,154 explicitly unassessed.

Validation after organization policy management:

- The organization API integration journey passes all nine policy operations,
  nested wire shapes, IP/CIDR validation, product ownership, type filtering,
  resource metadata updates, status changes, deletion and audit evidence with
  migration 038.
- The focused Chromium administration journey passes policy create, product
  scope, enable and deletion, WCAG scans, dark mode, and 320 px reflow.
- All 47 Organizations operations now have reviewed partial assessments. Exact
  coverage is 62 of 1,207: 55 partial, 7 missing, and 1,145 unassessed.

Validation after the provider registry and login journey:

- All Go packages pass with PostgreSQL migration 039, including provider-bound
  replay state, multi-provider identity linking, organization login audit, exact
  Atlassian JSON token/profile exchange, and provider configuration behavior.
- `go vet ./...`, the WebAssembly build, all seven conformance tests, and both
  inventory `--check` modes pass.
- All 46 Chromium user-journey tests pass against a provider-enabled server,
  including the provider-choice/admin-status journey, WCAG light/dark checks,
  and 320 px reflow.
- Exact API coverage remains 62 of 1,207 because browser provider login is not
  one of the pinned product REST operations.

Validation after explicit identity linking:

- Focused PostgreSQL tests pass provider-bound link state, identity metadata,
  link/unlink audit evidence, last-provider protection, and issuer-scoped
  browser-session revocation with migrations 040 and 041.
- The provider browser journey covers the responsive profile connection panel
  as well as login choice and administrator provider status.

Validation after provider availability administration:

- Focused store and registry tests pass durable settings, unknown-key rejection,
  issuer-scoped session revocation, audit evidence, disabled login discovery,
  and re-enable behavior with migration 042.
- The provider Chromium journey disables Atlassian, proves a fresh signed-out
  browser can no longer select it, re-enables it, and proves it is restored.
- The full PostgreSQL Go suite, vet, WebAssembly build, conformance tests, and
  generated inventory/coverage checks pass; the existing administration journey
  still passes its WCAG light/dark and 320 px checks.

Validation after browser-replica revocation:

- Focused sync policy tests cover accepted, retryable, unauthorized, and
  forbidden responses; the browser-replica protocol exposes bare 401 responses
  while preserving Basic challenges for ordinary API clients.
- The Chromium journey proves that suspension while an edit is queued offline
  never replays that edit and clears the private SQLite replica, action log,
  checkpoint, outbox, page cache, and replica identifier before signed-out
  navigation.
- The revocation, sign-out/session-isolation, rapid OPFS navigation, and all V1
  edit/comment/transition/offline-drain journeys pass: 8 tests total.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  both generated evidence checks, and `git diff --check` pass. Exact API
  coverage remains 62 of 1,207 because this closes local sync semantics rather
  than a pinned public product operation.

Validation after encrypted provider administration:

- Cryptography tests prove authenticated round trips, associated-context
  binding, tamper rejection, and absence of plaintext in the envelope.
- PostgreSQL tests cover migration 043, encrypted registration persistence,
  rotation/deletion audit records, and cleanup; registry tests cover live
  publish/replace/delete and rejection of environment-provider overrides.
- Two Chromium provider journeys pass built-in availability plus custom OIDC
  discovery, encrypted registration, immediate login choice, secret rotation
  without disclosure, and deletion.
- The full PostgreSQL Go suite, vet, WebAssembly compilation, seven conformance
  tests, both generated evidence checks, and `git diff --check` pass. Ten
  Chromium provider/admin/accessibility journeys pass, including light/dark
  WCAG scans, keyboard behavior, target sizing, and 320 px reflow.

Validation after workflow draft/publish:

- PostgreSQL workflow tests prove published definitions remain unchanged while
  a draft is edited, publishing promotes the draft and increments its version,
  discarding preserves the published definition, and project runtime resolves
  only the published version with migration 044.
- The Chromium workflow journey creates a workflow, adds a transition, observes
  the inactive-draft explanation, publishes version 2, assigns it to a project,
  and verifies the project uses it.
- The full PostgreSQL Go suite, vet, WebAssembly compilation, seven conformance
  tests, both generated inventory/coverage checks, and `git diff --check` pass.
  The light/dark WCAG scan of every primary page and the 320 px reflow journey
  also pass.

Validation after status lifecycle administration:

- PostgreSQL tests cover migration 045, workspace visibility, validation,
  duplicate and built-in protection, workflow impact detection, deletion only
  after reference cleanup, and create/update/delete audit evidence.
- The Jira REST integration journey covers member/admin authorization, bulk
  create/read/update/delete, lookup by ID/name, search/category filtering,
  status categories, descriptions, conflicts, protected deletion, and the three
  project/workflow/issue-type usage resources.
- The Chromium administrator journey covers directory navigation, built-in
  protection, creation, categorization, description, impact counts, editing,
  and safe deletion.
- The full PostgreSQL Go suite, vet, WebAssembly compilation, seven conformance
  tests, both generated evidence checks, and `git diff --check` pass. The new
  page passes the shared light/dark WCAG scan, WCAG 2.2 target-size check, and
  320 px reflow journey.
- Exact reviewed API coverage is 75 of 1,207 operations: 68 partial, 7 missing,
  and 1,132 explicitly unassessed.

Validation after workflow workspace isolation:

- PostgreSQL workflow tests prove custom definitions cannot be read, listed, or
  assigned from another workspace while the shared default remains available.
- The Chromium administrator workflow journey still passes create, draft edit,
  publish version 2, and project assignment after migration 046.
- The full PostgreSQL Go suite, vet, WebAssembly compilation, seven conformance
  tests, generated inventory/coverage checks, and `git diff --check` pass.

Validation after workflow schemes:

- PostgreSQL tests prove incompatible status detection, blocked assignment,
  safe assignment, issue-type runtime selection, draft isolation, publishing,
  version increments, and audit-backed mutations with migration 047.
- The Jira REST integration journey covers administrator authorization,
  list/create/get/update/delete, project association read/write, active delete
  conflicts, and workspace scoping for the seven assessed operations.
- The Chromium administrator journey creates a scheme, edits and publishes its
  mappings, previews project impact, assigns the scheme, and sees project usage.
- The full PostgreSQL Go suite, vet with an isolated build cache, WebAssembly
  compilation, seven conformance tests, generated inventory/coverage checks,
  and `git diff --check` pass. The scheme directory and a real scheme editor
  pass shared light/dark WCAG scans, WCAG 2.2 target sizing, and 320 px reflow.
Validation after workflow-scheme status migration:

- PostgreSQL store and Jira REST integration tests prove unmapped switches are
  rejected and mapped switches atomically migrate status, emit sync actions,
  assign the scheme, and return the required 303 task fields and Location.
- The Chromium administrator journey builds a workflow without In Progress,
  creates an affected work item, previews the impact, chooses To Do as its
  replacement, assigns the scheme, and verifies the migrated REST resource.
- All 50 Chromium journeys pass in suite order, including shared light/dark
  WCAG scans, target sizing, 320 px reflow, session revocation, and subsequent
  password-authenticated member API journeys.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance
  tests, generated inventory/coverage checks, and `git diff --check` pass.
Validation after workflow-scheme draft resources:

- The PostgreSQL Jira integration journey exercises every draft resource and
  method, missing draft/mapping behavior, duplicate draft conflicts,
  validation-only publish, durable publish tasks, and published runtime safety.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance
  tests, generated inventory/coverage checks, and `git diff --check` pass.

Validation after published workflow-scheme mappings:

- The PostgreSQL Jira integration journey exercises all published mapping
  reads and mutations, inactive safe edits, project assignment, and paged
  project usage alongside the existing draft and switch paths.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance
  tests, generated inventory/coverage checks, and `git diff --check` pass.

Validation after bulk workflow-scheme operations:

- PostgreSQL integration covers mixed project/scheme reads, required mapping
  discovery, unsafe update rejection, safe durable-task update, and stale
  document-version conflicts.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance
  tests, generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 111 of 1,207 operations: 104 partial, 7
  missing, and 1,096 explicitly unassessed.

Validation after workflow-scheme publish/update migration:

- PostgreSQL integration validates publish mappings without mutation, applies
  them on publish, restores the active definition, rejects an unmapped bulk
  update, applies an issue-type override on bulk update, and preserves stale
  version rejection and the later project-switch migration path.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance
  tests, regenerated inventory/coverage checks, and `git diff --check` pass.

Validation after workflow usage resources:

- PostgreSQL integration covers project, workflow-scheme, and project issue-type
  usage responses for a workflow, including published default resolution,
  workspace isolation, administrator authorization, opaque paging validation,
  and missing project handling.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 114 of 1,207 operations: 107 partial, 7
  missing, and 1,093 explicitly unassessed.

Validation after workflow administration guards:

- PostgreSQL integration rejects deletion of the system workflow and workflows
  referenced by projects, published schemes, or draft schemes; deletion of an
  inactive workspace workflow removes it and writes one organization audit
  event atomically.
- The Jira integration journey covers member denial, validation failures,
  missing workflows, successful 204 deletion, and authenticated new-editor
  discovery.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 116 of 1,207 operations: 109 partial, 7
  missing, and 1,091 explicitly unassessed.

Validation after modern workflow search:

- PostgreSQL Jira integration covers published workflow search by name,
  project, and active state; deterministic name ordering; validated offset
  paging and next links; global/project scope behavior; related status
  categories; and expanded transition links.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 117 of 1,207 operations: 110 partial, 7
  missing, and 1,090 explicitly unassessed.

Validation after workflow capability discovery:

- PostgreSQL Jira integration resolves capability requests by workflow ID and
  by project plus issue type, rejects invalid or mixed selectors, enforces
  administrator access, and reports only the global project types and empty
  rule catalogs the current runtime actually supports.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 118 of 1,207 operations: 111 partial, 7
  missing, and 1,089 explicitly unassessed.

Validation after modern workflow validation:

- PostgreSQL Jira integration covers valid and invalid create payloads,
  existing-status references, directed transition topology, duplicate and
  unsupported elements, global scope, name conflicts, update lookup, stale
  document versions, validation-level filtering, member denial, and malformed
  requests without mutating workflow state.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 120 of 1,207 operations: 113 partial, 7
  missing, and 1,087 explicitly unassessed.

Validation after modern workflow mutations:

- PostgreSQL Jira integration creates and updates global directed workflows
  over existing statuses, returns workflow/status/document-version beans,
  rejects duplicate names and stale versions, prevents active updates that
  strand work items in removed statuses, and verifies create/update audit
  events.
- Multi-workflow integration cases prove a later create conflict or stale
  update rolls back every earlier workflow and audit write in the same request.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 122 of 1,207 operations: 115 partial, 7
  missing, and 1,085 explicitly unassessed.

Validation after workflow preview:

- PostgreSQL Jira integration previews only published workflows associated
  with the requested project, preserves ID/name/issue-type lookup order,
  coalesces query context, and returns directed transitions, versions, and
  related status metadata; invalid selectors and unassociated workflows fail.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.
- Exact reviewed API coverage is 123 of 1,207 operations: 116 partial, 7
  missing, and 1,084 explicitly unassessed.

Validation after workflow-owned status creation:

- PostgreSQL Jira integration validates proposed global statuses without
  persistence, creates workflow/status batches with generated references and
  separate status/workflow audit events, and creates statuses during workflow
  updates.
- Create conflicts and later stale updates prove newly inserted statuses,
  workflows, versions, and audit events all roll back with the surrounding
  batch.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.

Validation after workflow status mappings:

- PostgreSQL Jira integration rejects an active workflow update that would
  strand a work item, then accepts the same topology with default and
  project/issue-type mappings, proves the scoped mapping wins, migrates the
  work item atomically, and emits the ordinary status-diff sync action.
- Mapping validation resolves existing and request-created status references,
  rejects targets outside the updated workflow, and records the migrated issue
  count with the workflow update audit event.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and `git diff --check` pass.

Validation after executable workflow rules:

- Modern Jira workflow create/update validation accepts and persists recursive
  `ALL`/`ANY` condition groups, required-field validators, and change-assignee
  post-functions, generates omitted rule IDs, and rejects every rule or
  parameter that the runtime cannot execute.
- Issue REST and browser views hide actor-restricted transitions. The shared
  command path rechecks the condition, returns configured validator errors,
  validates selected assignees, rejects stale issue snapshots, and commits the
  status and post-function assignee change as one issue update, changelog
  record, and sync action.
- PostgreSQL integration creates the rules through the modern Jira API, proves
  reporter and non-reporter visibility, validator recovery, atomic execution,
  capabilities, search, and project preview round-tripping.
- The full PostgreSQL Go suite passes. Vet, WebAssembly, conformance, generated
  evidence, and diff checks are run before the slice commit.

Validation after workflow designer persistence:

- Workflow JSON now preserves the modern Jira description, start-point,
  loop-container, status-property, and per-status layout fields. Create,
  update, search, and preview round-trip those fields, while validation rejects
  duplicate status membership and non-finite or out-of-range coordinates.
- The administrator workflow page is a connected transit map of only the
  workflow's statuses. Pointer dragging and arrow-key movement redraw curved
  routes live, serialize position saves, create isolated drafts without a page
  reload, and reveal publish/discard controls as soon as the first save lands.
- PostgreSQL tests prove layout drafts do not leak into the published runtime,
  publish promotes all layout metadata, and omitted update metadata preserves
  published workflow descriptions and container positions.
- The targeted Chromium administrator journey moves a status by keyboard,
  observes the saved draft, adds a transition, publishes both changes as one
  version, and assigns the workflow. The full primary-page axe light/dark scan,
  target-size check, and 320 px reflow journey pass.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks, and diff checks pass on the final tree.

Validation after durable Jira task execution:

- Workflow-scheme switch, draft publish, and bulk update requests now validate
  synchronously, persist an `ENQUEUED` operation payload, and return the Jira
  303 task resource before any workflow or issue state changes.
- A server-managed worker claims tasks with `SKIP LOCKED`, reports `RUNNING`,
  commits the workflow mutation and `COMPLETE` result atomically, records
  terminal failures, and reclaims abandoned running work after its lease.
- `POST /rest/api/3/task/{taskId}/cancel` returns 202 for enqueued or running
  work. If cancellation wins before commit, the worker rolls back every issue,
  project, audit, sync, and scheme change; completed tasks remain immutable.
- PostgreSQL store and API journeys cover all three producers, submitter/admin
  visibility, queued polling, successful results, enqueued and running
  cancellation, failed operations, completed-task rejection, and simulated
  restart recovery.
- The full PostgreSQL Go suite, vet, WebAssembly build, all seven conformance
  tests, generated inventory/coverage checks, migration, and diff checks pass.

Validation after project-scoped statuses:

- Migration 050 gives custom statuses an optional project owner, with separate
  case-insensitive uniqueness for global and per-project names. Creation
  validates that the project belongs to the active workspace, and updates
  preserve the original scope.
- Jira status creation and response resources support `GLOBAL` and `PROJECT`
  scope shapes. Bulk lookup, by-name selection, and paged search expose the
  correct scope; `projectId` and `includeGlobalStatuses` isolate each project
  while preserving all filters in `nextPage`.
- Issue updates and workflow/scheme status migrations reject a project status
  outside the issue's project. Project issue navigation and scheme impact
  choices use the same visibility boundary.
- The administrator directory offers a global or named-project scope and labels
  every built-in, global, and project status. The focused Chromium journey
  creates, edits, and deletes a project status and checks its ownership label.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after project-scoped workflows and capabilities:

- Migration 051 adds optional project ownership without imposing a new unique
  index on legacy installations that may contain repeated workflow names.
  New global and per-project names are serialized and checked within their
  scope, while foreign project owners are rejected.
- Modern create and validation accept Jira GLOBAL and PROJECT scope payloads,
  create related statuses in the same scope, preserve scope on update, and
  return scoped workflow and status resources. Search filters GLOBAL/PROJECT
  definitions and treats project ownership as a project association.
- Capability lookup by workflow or project/work-item type reports the resolved
  editor scope. Runtime workflow resolution, direct assignment, status
  validation, preview, and deletion usage all honor the owner boundary.
  Global workflow schemes accept only global definitions.
- The workflow directory creates either scope. A project workflow is assigned
  to its owner immediately, exposes global plus owner statuses in its editor,
  and identifies its scope in both the directory and editor. The focused
  Chromium journey creates, edits, publishes, and verifies the active project
  workflow.

Validation after executable transition screens:

- Workflow transitions persist and round-trip a `system:transition-screen`
  configuration. Capability discovery advertises it as a Screen rule, and
  create/update validation rejects missing fields or duplicate rule IDs.
- Jira transition discovery reports `hasScreen` and field metadata, including
  required markers shared with field validators. Transition requests reject
  fields outside the configured screen and decode summary, ADF description,
  labels, assignee, priority, and custom values.
- Allowed screen changes, the target status, validators, and assignee
  post-functions execute through one optimistic issue update, producing one
  synchronized changelog transaction. PostgreSQL integration covers hidden
  fields, submitted labels, required-field validation, status movement, and
  post-function assignment.
- The workflow editor offers accessible screen-field choices and identifies
  the screen on its diagram. The focused Chromium workflow journey configures,
  publishes, and verifies a labels screen.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after visual workflow rule editing:

- The workflow transition editor now creates actor restrictions for the
  reporter or current assignee, required-field validators, current-user or
  unassigned assignee post-functions, and transition screens from one form.
  These controls produce the same executable system rules accepted by the Jira
  workflow API and enforced by runtime transitions.
- The workflow diagram summarizes conditions, validators, post-functions, and
  screen fields on each transition so an administrator can review the active
  behavior without reopening the form.
- The focused Chromium journey creates a project workflow, configures all four
  rule categories, saves the draft, publishes it, and verifies the diagram
  summary.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after atomic multi-status writes:

- Jira status create, update, and delete requests now execute through one store
  transaction. Validation, scope locks, row locks, status writes, and audit
  records either all commit or all roll back.
- Batch updates preserve project ownership and support simultaneous status-name
  swaps after checking the final name set. Deletes enforce Jira's 1-to-50 ID
  request boundary and preflight built-in and usage protection for every item.
- PostgreSQL store and REST integration tests prove failed create, update, and
  delete batches leave earlier members and their audit records unchanged; they
  also cover successful create/delete batches and simultaneous renames.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, and diff check pass.

Validation after API-aware transition restrictions:

- `system:restrict-from-all-users` is now an executable condition with Jira's
  `users` and `usersAndAPI` modes. The shared evaluator distinguishes REST
  execution from browser and automation execution, so API-only transitions
  stay absent from the user-facing issue journey while remaining available to
  integrations.
- Modern workflow create, update, validation, search, preview, and capability
  resources round-trip the rule. The administrator editor offers API-only and
  fully blocked choices alongside reporter and assignee restrictions.
- Unit coverage proves both request-source modes and rejects unknown values.
  PostgreSQL REST integration creates, publishes, discovers, and executes an
  API-only transition, while the Chromium editor journey creates and publishes
  the same restriction visually.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

## Current change

1. Add executable field-value workflow conditions and visual controls.
2. Continue the reviewed Jira Platform contract operation ledger alongside the
   vertical workflow slices.

## Resume here

1. Run `git status --short --branch` and read this file.
2. Run `python3 api/conformance/inventory.py --check` before changing contract
   inputs.
3. Continue the first unfinished item in **Current change**.
4. Run the focused inventory tests, `go test ./...`, the WASM build, and
   `git diff --check` before the workstream commit.
5. Update this file with the completed workstream, exact validation, next
   workstream, and any real blocker.

## Evidence map

- [Cloud/API status](CLOUD_PARITY.md)
- [Endpoint groups](../api/conformance/MATRIX.md)
- [Operation coverage](../api/conformance/cloud-coverage.json)
- [UI and persona journeys](UI_PARITY.md)
- [Automation behavior](AUTOMATION.md)
- [Dashboard behavior](DASHBOARDS.md)
- [Release behavior](RELEASES.md)
- [SSO behavior](shauth-sso.md)

## Continuity rules

- Do not infer API completeness from the grouped endpoint matrix.
- Do not mark a journey complete without a browser test spanning its user goal.
- Do not claim base-URL-only compatibility for clients that hardcode a central
  Atlassian identity or administration host.
- Do not carry placeholders, silent fallbacks, dead code, or swallowed errors.
- Preserve unrelated user changes and inspect the working tree before editing.
- Answer review comments in their original threads, resolve addressed threads,
  and require the full CI matrix to pass before merge.
