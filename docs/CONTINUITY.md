# Development continuity

Updated: 2026-09-07

This file is the local handoff for the Jira Cloud completion program. Keep it
short, factual, and current after each meaningful commit. Stable architecture
and final acceptance rules belong in [PLAN.md](../PLAN.md).

## Active delivery

- Branch: `feat/cloud-surface-completion`
- Base: `origin/main` after PR #65, scheduled automation
- Delivery shape: one pull request with ordered, independently green commits
- Last completed workstream: 10 of 15 — Jira Service Management API
- Active workstream: 11 of 15 — Jira Service Management journeys
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

Validation after field-value workflow conditions:

- `system:check-field-value` evaluates system and custom issue fields with
  Jira's `STRING`, `NUMBER`, `DATE`, `DATE_WITHOUT_TIME`, and `OPTIONID` modes
  and `>`, `>=`, `=`, `<=`, `<`, and `!=` comparators. Scalar, array, and
  option-object values share one normalized evaluator; missing fields do not
  accidentally satisfy negative comparisons.
- Workflow validation requires a field, a non-empty JSON value array, and known
  comparison settings. Create, update, search, preview, and capabilities
  round-trip the executable rule.
- The administrator editor provides system-field, comparator, type, and value
  controls and can combine the condition with actor or API restrictions. Unit
  coverage exercises every value shape and type; PostgreSQL REST integration
  proves matching work is available and a mismatched summary stays hidden; the
  Chromium workflow journey creates and publishes the visual configuration.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after update-field workflow post-functions:

- `system:update-field` now replaces or appends summary, description, labels,
  and supported custom fields, and replaces priority. Configuration
  validation rejects unknown fields, modes, and invalid append operations.
- Post-functions apply after validators and transition-screen input in workflow
  order. Their changes, assignee effects, and target status are normalized and
  persisted through the same optimistic issue update and sync action.
- The administrator editor provides label append/replace controls. Unit tests
  cover configuration, coexistence with assignee functions, and custom text
  append; PostgreSQL REST integration proves screen labels plus workflow labels
  and a summary append commit atomically; the focused Chromium journey creates
  and publishes the visual rule.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after copy-field workflow post-functions:

- `system:copy-value-from-other-field` copies system or custom values between
  executable fields on the same issue. Configuration validation rejects
  unknown and read-only targets and reports parent copying as unsupported until
  issue hierarchy is available.
- Copy actions execute in declared order with update-field actions, so later
  copies observe earlier screen or post-function values. The copied value,
  target status, and every other effect still produce one issue update and sync
  record.
- The administrator editor provides source and target controls. Unit tests
  cover configuration and effective-value copying; PostgreSQL REST integration
  appends a summary and then copies that result into description in the same
  transition; the focused Chromium journey creates and publishes the rule.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after previous-status conditions and validators:

- The immutable issue action log now supplies ordered prior statuses to the
  shared workflow context for REST discovery, browser rendering, automation,
  and transition execution. Only real status changes enter history.
- `system:previous-status-condition` supports Jira's single status, most-recent,
  include-current, and negated modes; `system:previous-status-validator`
  enforces any or most-recent history. Strict boolean and status-count
  validation prevents inert configurations.
- The administrator editor provides status selectors and history options. Unit
  tests cover ordered, recent, current, negated, invalid, and validator paths;
  PostgreSQL REST integration creates and verifies real status history before
  executing a governed transition; the focused Chromium journey creates and
  publishes both visual rule types.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after separation-of-duties conditions:

- The action log now supplies ordered from-status, to-status, and actor triples
  to every workflow evaluation path. `system:separation-of-duties` rejects the
  configured transition when the current actor performed that earlier status
  change and allows a different actor.
- Configuration validation requires both status IDs, capability discovery
  advertises the executable rule, and modern workflow resources round-trip it.
  The administrator editor provides paired status selectors.
- Unit tests prove same-actor denial, different-actor access, and incomplete
  configuration rejection. PostgreSQL REST integration verifies stored actor
  history and executes as a different actor; the focused Chromium journey
  creates and publishes the visual rule.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after Jira permission workflow validators:

- `system:check-permission-validator` now accepts every documented built-in
  Jira permission key and evaluates the transition actor against zzira's shared
  organization/site role bindings. Active members receive the delivered work
  permissions; site administrators also receive workflow and project
  administration permissions.
- Workflow validation rejects unknown keys, capability discovery and modern
  workflow resources expose and round-trip the executable validator, and the
  administrator editor provides the delivered permission choices.
- Unit tests prove denial, success, and invalid-key rejection. PostgreSQL REST
  integration verifies member and administrator grants and executes a stored
  `EDIT_ISSUES` validator; the focused Chromium journey creates and publishes
  the visual control.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after changed-field workflow validators:

- The `fieldChanged` mode of `system:validate-field-value` now requires its
  `fieldKey` to differ from the stored work-item value and returns the
  configured Jira `errorMessage` when it does not. System fields, ADF, lists,
  and custom JSON fields use value-aware comparisons. Unsupported group
  exemptions are rejected instead of being stored without effect.
- Modern workflow create, update, search, preview, and validation resources
  round-trip the rule. The visual editor exposes supported fields and
  automatically adds the chosen field to the transition screen.
- Unit tests prove semantic changed/unchanged evaluation and configuration
  rejection. PostgreSQL REST integration proves rejection when the submitted
  labels equal the stored value and success when they differ; the focused
  Chromium journey creates and publishes the rule while verifying the resulting
  screen summary.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after regular-expression workflow validators:

- The `fieldMatchesRegularExpression` mode of
  `system:validate-field-value` compiles the configured Jira `regexp`, matches
  scalar and list field values, and returns the configured `errorMessage`.
  Invalid expressions and incomplete configurations are rejected at save time.
- Transition-screen updates replace the stored field values in the validator
  context before rules run. Modern workflow resources round-trip the parameters,
  and the visual editor exposes field, expression, and message controls.
- Unit tests prove match, mismatch, message, and invalid-pattern paths.
  PostgreSQL REST integration distinguishes unchanged-field rejection,
  nonmatching changed values, and a successful matching label; the focused
  Chromium journey creates and publishes the visual rule.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after single-value workflow validators:

- The `fieldHasSingleValue` mode of `system:validate-field-value` evaluates the
  effective transition value: nonempty scalars and one-element arrays pass,
  while empty and multi-value fields fail. `excludeSubtasks` is parsed and
  validated as Jira's required boolean parameter.
- Modern workflow resources round-trip the rule and the administrator editor
  exposes both the field selector and subtask option.
- Unit tests prove empty, multiple, single, and invalid-configuration paths.
  PostgreSQL REST integration executes the stored scalar validator, and the
  focused Chromium journey creates and publishes the visual rule.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after date-field comparison workflow validators:

- The `dateFieldComparison` mode of `system:validate-field-value` compares two
  effective system or custom field values with Jira's six operators. Date-only
  mode ignores the time portion, while time-aware mode accepts RFC 3339 values
  and treats date-only values as midnight.
- Workflow validation requires both field keys, a strict `includeTime` boolean,
  and a supported operator. Modern resources round-trip the rule, and the
  administrator editor exposes both field keys, the operator, and time mode.
- Unit tests prove date-only and time-aware comparisons plus invalid
  configuration. PostgreSQL REST integration uses registered datetime custom
  fields, proves a reversed date blocks the transition atomically, restores the
  later date, and completes the matching path. The focused Chromium journey
  creates and publishes the visual rule.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after date-window workflow validators:

- The `windowDateComparison` mode of `system:validate-field-value` requires the
  first effective date to be no later than `numberOfDays` after its reference
  date. The boundary is inclusive and both date-only and RFC 3339 values compare
  by calendar date.
- Workflow validation requires both field keys and a nonnegative integer day
  limit. Modern resources round-trip the rule, and the administrator editor
  exposes the target field, reference field, and constrained day input.
- Unit tests prove the inclusive boundary, over-window failure, and negative
  configuration rejection. PostgreSQL REST integration executes the stored
  rule against registered datetime fields, and the focused Chromium journey
  creates and publishes the visual controls.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after issue hierarchy and parent/child workflow rules:

- Migration 052 adds an indexed, referentially constrained `parent_id` and the
  built-in Sub-task work type. Command validation requires every sub-task to
  use a visible non-sub-task parent in the same project and prevents parent
  deletion while children remain.
- Jira create, get, update and create-metadata resources accept and return the
  `parent` field by ID or key, expose the issue type `subtask` flag, and mark
  parent as required in Sub-task create metadata. Sync actions and changelogs
  retain the same persisted relationship.
- The browser create dialog refreshes when the work type changes, requires a
  parent for Sub-task, supports later parent reassignment, links child to
  parent, and lists direct sub-tasks on the parent work item.
- `system:parent-or-child-blocking-condition` blocks a parent while any child
  has a configured status. `system:parent-or-child-blocking-validator` blocks
  a child while its parent has a configured status. REST discovery, browser
  transition discovery, automation, and execution hydrate the same hierarchy
  facts; capabilities and modern workflow resources round-trip both rule keys.
- Unit tests cover rule truth tables and malformed blocker configuration. A
  PostgreSQL command journey proves persisted hierarchy and both transition
  directions, the Jira REST journey proves parent create/get/reassignment and
  required-parent validation, and focused Chromium journeys cover Sub-task UI
  creation/navigation and both workflow editor controls.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory and coverage checks, diff check, primary-page light/dark
  axe scan, and 320 px reflow gate pass.

Validation after parent-field workflow copying:

- `system:copy-value-from-other-field` now accepts Jira's `issueSource=PARENT`
  alongside `SAME`, with an omitted source still defaulting to `SAME`.
- Ordered post-function execution loads the persisted parent snapshot as the
  source while writing only into the child transition update. Missing parents
  fail the transition and unsupported source values fail workflow validation.
- The workflow editor exposes “This issue” and “Parent issue” source choices.
  Capabilities describe both sources, and stored workflow definitions preserve
  the Jira parameter unchanged.
- Unit tests cover source parsing and invalid values. The PostgreSQL hierarchy
  journey transitions a child and proves its parent summary was copied into the
  child description; the focused Chromium workflow editor journey passes with
  the parent source selected.

Validation after workflow-triggered webhook delivery:

- `system:trigger-webhook` accepts Jira's `webhookId` parameter in modern
  workflow create, update, validation, search, preview, and capability payloads.
  Empty IDs fail definition validation. The issue-update transaction locks each
  target and rejects missing or inactive registrations in the current workspace
  before mutating the issue, so registration deletion cannot race the commit.
- Transition execution records the ordered, de-duplicated target IDs in the
  same action-log transaction as the issue update. A webhook-only loop
  transition therefore still creates a durable `jira:issue_updated` action.
- The existing dispatcher and retry ledger deliver that action. The explicitly
  targeted registration bypasses its ordinary event subscription and JQL
  filters, while every unrelated registration retains those filters.
- The admin workflow editor lists active registered webhook URLs and persists
  the selected post-function. Unit tests cover parsing, validation, event
  classification and targeting; PostgreSQL integration proves active/inactive
  execution and delivery-filter bypass; the modern Jira REST journey proves
  API round-tripping and execution; the focused Chromium editor journey passes.

Validation after Advanced Forms workflow validators:

- Migration 053 adds issue-bound Advanced Form instances with template
  references, answers, internal/external visibility, open/submitted state,
  locking metadata, timestamps, issue cascade deletion, and workspace-scoped
  store access.
- The Forms Cloud issue contract is served at
  `/jira/forms/cloud/{cloudId}/issue/{issueIdOrKey}/form`: index, attach, get,
  answer save, delete, internal/external visibility, submit, and reopen are
  implemented with the documented request and response shapes. Submitted or
  locked forms reject answer writes until reopened.
- `system:proforma-forms-attached` and
  `system:proforma-forms-submitted` accept no parameters and execute against
  the persisted issue-form state. Modern workflow create, update, validation,
  search, preview, and capability resources preserve both official keys.
- The issue UI exposes the form list and attach, submit, reopen, and delete
  journey. The workflow editor exposes both validators. Unit tests cover the
  validator truth table and parameter rejection; a PostgreSQL API journey
  proves the Forms lifecycle drives transition failure and success; focused
  Chromium contributor and admin journeys pass.

Validation after Jira Software development information:

- Migration 054 adds workspace-scoped repositories and normalized commit,
  branch, and pull-request entities with issue associations, provider
  properties, current payloads, cascade deletion, time indexes, and monotonic
  per-entity update sequences.
- All six pinned `/rest/devinfo/0.10` operations are implemented: bulk ingest,
  current repository read, repository/entity deletion, property existence, and
  property bulk deletion. The `api.atlassian.com`-style
  `/jira/devinfo/0.1/cloud/{cloudId}` alias validates the site cloud ID.
- Replayed and stale updates are safe no-ops. Sequence-protected deletes cannot
  remove newer data. Advisory transaction locks ensure concurrent updates fire
  branch-created automation once, and `preventTransitions` suppresses it.
- `system:development-triggers` round-trips the official branch-created type in
  modern workflow create, update, search, and capability payloads. The admin
  editor persists it and issue pages list linked repositories, commits,
  branches, and pull requests.
- PostgreSQL integration covers ingestion, issue-key associations, stale
  updates, create-only triggering, suppression, current reads, property
  queries, sequence-aware deletion, cloud-ID routing, and workflow wire data.
  Focused Chromium contributor and admin journeys pass.

Validation after Jira Software builds and deployments:

- Migration 055 adds workspace-scoped, sequence-ordered build and deployment
  snapshots with issue associations, provider properties, pipeline and
  environment keys, current payloads, cascade deletion, and timeline indexes.
- All nine pinned `/rest/builds/0.1` and `/rest/deployments/0.1` operations are
  implemented: bulk submission with per-item acceptance/rejection, composite
  key reads and idempotent sequence-aware deletes, property bulk deletion, and
  deployment gating-status reads. The `api.atlassian.com`-style cloudId aliases
  validate the target site.
- Work items show linked builds and deployments. Release pages roll the same
  evidence up from version scope with pipeline, environment, state and safe
  provider links.
- PostgreSQL integration covers issue-key/ID associations, unknown keys,
  current reads, stale updates, inclusive sequence-protected deletion,
  property cleanup, cloud-ID routing, gating status and per-item rejection.
  The full release Chromium journey proves issue and release evidence, light
  and dark accessibility checks, and 320 px reflow. The full PostgreSQL Go
  suite, vet, WASM build, inventory and coverage checks pass.

Validation after the first DORA report:

- Migration 056 records every accepted build and deployment update as an
  immutable, sequence-keyed fact and backfills current delivery snapshots.
  Stale retries cannot create facts.
- `/projects/{key}/reports/dora` offers 7, 30 and 90 day permission-filtered
  deployment frequency, commit-to-production lead time, production change
  failure rate and incident recovery time. Current deployment state is derived
  from the highest immutable update sequence for every deployment key.
- The production trend uses an accessible SVG with exact keyboard-reachable
  daily data, recent pipeline/environment events, calculation definitions and
  explicit empty states. The release Chromium journey covers the report in
  light and dark themes and at 320 px.
- PostgreSQL integration proves successful and rolled-back production counts,
  a 50% failure-rate case, a two-hour linked-commit lead time, a 2.5-hour
  incident recovery, and fact idempotency under a stale update.

Validation after the Jira Service Management project foundation:

- Migration 057 gives projects an explicit `software` or `service_desk` type
  and adds workspace-scoped service desks and request types. Creating a service
  project atomically provisions its desk plus default IT-help and incident
  request types.
- The project creation UI and Jira Platform project API accept the IT service
  management template and return `projectTypeKey: service_desk`.
- Nine pinned `/rest/servicedeskapi` operations cover product info, paged desk
  discovery/detail, global and desk request-type search, administrator create
  and delete, request-type detail, and request field metadata.
- A PostgreSQL contract journey proves tenant isolation, project typing,
  defaults, custom request-type lifecycle and form metadata. A Chromium admin
  journey creates the service project and discovers its customer form through
  Jira Service Management APIs.

Validation after the Jira Service Management customer request journey:

- Migration 058 adds workspace customers, issue-backed service requests and an
  explicit public/internal marker for request comments. Ordinary Jira comments
  remain internal unless deliberately shared through the service path.
- Eleven more pinned operations cover customer creation, request validation,
  create/list/detail, comment list/create/detail, current status and available/
  executed transitions. Administrators can query all requests, raise on behalf
  of enrolled customers and add internal notes; customers are restricted to
  their own requests and public comments.
- `/service` now provides help-center and portal discovery, request-type search,
  typed submission, owned request tracking, conversation and workflow actions.
  Incident request types create regular Jira issues labeled `incident`, joining
  the customer journey to DORA recovery time, automation, search and reporting.
- PostgreSQL integration proves validation, customer ownership, internal-note
  isolation, incident labeling and a real transition with an additional public
  comment. The Chromium journey passes WCAG A/AA axe checks in light and dark
  themes and reflows without document overflow at 320 px.
- Exact-operation review is now 159/1,207: 152 partial, 7 missing and 1,048
  unassessed.

Validation after the Jira Service Management agent queue journey:

- Migration 059 provisions ordered all-open, unassigned and assigned-to-me
  queues for existing and newly created service desks. Queue contents and live
  counts derive from canonical request issue status and assignee state.
- The three pinned service-desk queue list/detail/issues operations enforce the
  current agent boundary, expose Jira-compatible queue/JQL/field shapes and
  return queue-filtered issue beans. Site administrators are the initial agent
  role until project-scoped service roles are implemented.
- `/service/agent` adds service-desk and queue navigation, a keyboard-reachable
  request table, request drill-through, take-ownership and unassign actions.
  The combined customer-to-agent Chromium journey proves queue movement,
  light/dark accessibility and 320 px reflow.
- Exact-operation review is now 162/1,207: 155 partial, 7 missing and 1,045
  unassessed.

Validation after Jira Service Management request participants:

- Migration 060 stores non-reporter participants separately from the request
  customer. Participant access is evaluated by the same request lookup used by
  REST and web and is revoked by the remove transaction.
- The three pinned participant list/add/remove operations accept active service
  customers by account ID or email, restrict mutation to the reporter or an
  agent, reject reporter membership and return the updated paged user list.
- The request UI shows participants and lets an authorized reporter or agent
  add and remove them. PostgreSQL proves access grant/revocation; the combined
  Chromium journey covers UI add/remove alongside customer and queue flows.
- Exact-operation review is now 165/1,207: 158 partial, 7 missing and 1,042
  unassessed.

Validation after Jira Service Management project-scoped agents:

- Migration 061 assigns active workspace members to individual service desks.
  Site administrators remain implicit service managers across every desk.
- Queue navigation, queue REST operations, all-request reads, request detail,
  internal comments, participant management, assignment and raise-on-behalf all
  evaluate the same desk-scoped agent boundary. Revocation takes effect on the
  next request.
- `/service/agent/{desk}` includes an administrator-only roster and exposes only
  assigned desks to regular agents. The combined Chromium service journey
  passes roster add/remove, queue work, light/dark WCAG checks and 320 px reflow.
- The PostgreSQL service contract proves denied access before assignment,
  scoped access after assignment, customer/internal-comment separation,
  agent-created customers, raise-on-behalf, roster administration denial and
  immediate revocation. Exact-operation coverage remains 165/1,207 because
  agent roster administration is an internal authorization capability rather
  than a pinned JSM REST operation.

Validation after Jira Service Management calendars and SLA clocks:

- Migration 062 provisions a default business calendar plus first-response and
  resolution goals for every existing and future desk and starts durable cycles
  for existing and future requests.
- Calendar calculations honor IANA time zones, configured ISO weekdays, daily
  working windows and persisted holidays. Unit coverage proves overnight,
  weekend and holiday exclusion.
- A public agent response completes the first-response cycle; Done completes
  resolution; reopening starts another resolution cycle. The request journey
  shows current goal state, and administrators edit calendar and goal settings
  in the same responsive agent workspace.
- Both pinned agent-only SLA reads return Jira date/duration shapes, paged
  metric lists, completed cycles, ongoing cycles and not-found/permission
  errors. PostgreSQL covers configuration and lifecycle transitions; Chromium
  covers customer goal visibility and manager configuration.
- Exact-operation review is now 167/1,207: 160 partial, 7 missing and 1,040
  unassessed.

Validation after SLA attention and escalation:

- Migration 063 adds an SLA-attention queue and a per-cycle, per-recipient,
  per-stage escalation ledger. New and existing desks keep All open as their
  default queue.
- The minute SLA runner recomputes open clocks from business-calendar time and
  atomically writes deduplicated approaching-goal or breached notifications and
  their sync actions. Restart and concurrent replicas cannot duplicate a stage.
- The attention queue includes requests in the last quarter of any active goal,
  sorts breached requests first and shows the contributing clock in the agent
  table. Explicit desk agents, the current assignee and an implicit service
  manager receive escalation notifications.
- PostgreSQL integration proves warning and breach delivery, retry
  deduplication and queue membership. The combined Chromium journey confirms
  SLA-attention discovery in the responsive queue workspace.

Validation after request approvals and attachments:

- Migration 064 adds multi-user approvals, one-use service-desk temporary
  uploads, canonical Jira attachment links, comment association and explicit
  public/internal visibility.
- Agents request approval in the request view; only a pending assigned approver
  can approve or decline. Any decline finishes the approval and unanimous
  acceptance approves it. Approver assignment also grants request visibility.
- REST implements approval list/detail/decision, temporary multipart upload,
  attachment finalization with a comment, request/comment lists, content and
  thumbnail reads. Internal visibility is enforced through both JSM and Jira
  attachment routes.
- The portal accepts a file with a public or internal comment and shows linked
  files and approval actions in the request journey. An hourly worker deletes
  unclaimed temporary metadata and blobs after 24 hours.
- Exact-operation review is now 176/1,207: 169 partial, 7 missing and 1,031
  unassessed.

Validation after request notifications and feedback:

- Migration 065 adds per-viewer request subscriptions and reporter-owned CSAT.
  Existing and new reporters and participants start subscribed; approval
  assignment subscribes the approver.
- Public comments/files, status and approval changes notify subscribed viewers
  other than the actor. Internal comments notify subscribed agents only.
  Delivery uses the existing private notification records and ordered sync
  actions, and inbox links return to the service request.
- The three pinned subscription operations share request visibility and mutate
  only the caller's preference. The three feedback operations enforce a
  one-to-five `csat` rating on completed requests, reporter-only writes/deletes,
  and viewer reads.
- The request page exposes mute/resume controls and a completed-request
  satisfaction journey. PostgreSQL covers delivery and internal-note isolation;
  Chromium covers both controls with accessibility and responsive checks.
- Exact-operation review is now 182/1,207: 175 partial, 7 missing and 1,025
  unassessed.

Validation after customer and organization lifecycle:

- Migrations 066 and 067 add open/closed portal admission, direct desk
  customers, site customer organizations, membership, JSON properties, desk
  links, and a durable portal-only revocation marker.
- Customer creation now grants only the site customer role and requires site
  administration. Revocation removes the role, direct desk and organization
  access, and cannot be reversed by the request auto-enrollment path.
- All pinned organization and customer lifecycle routes are implemented:
  customer create/skip/revoke; organization list/create/detail/delete,
  properties and users; desk customer list/add/remove/invite/skip; and desk
  organization list/add/remove.
- Closed desks admit direct customers and linked-organization members. The
  agent workspace manages access mode, invitations, direct membership,
  organizations, organization customers and desk links. Customers see their
  own organizations in the help center.
- The full PostgreSQL Go suite, vet, WebAssembly build, seven conformance tests,
  generated inventory/coverage checks and `git diff --check` pass. The focused
  Chromium service journey covers customer invitation, access-mode changes,
  organization creation/membership/linking, customer visibility, WCAG scans,
  dark mode and 320 px reflow.
- Exact-operation review is now 203/1,207: 196 partial, 7 missing and 1,004
  unassessed. Jira Service Management has 64 of 75 pinned operations reviewed.

Validation after Jira Service Management contract completion:

- Migration 068 adds ordered request-type groups, linked Confluence spaces and
  a durable Assets-compatible workspace namespace for existing and future
  service desks.
- The final eleven pinned operations implement Assets/Insight workspace
  discovery, global and per-desk knowledge search, rendered article viewing,
  request-type permission checks, request-type property CRUD and group reads.
- Site administrators link Confluence spaces from the agent workspace.
  Published matching pages appear as customer portal suggestions and open in a
  permission-shaped knowledge article journey without granting Confluence
  product access.
- PostgreSQL contract coverage proves status shapes, validation, administrator
  and customer permissions, highlight markers and persisted values. The
  Chromium service journey passes knowledge linking/search/viewing, WCAG scans
  and 320 px reflow alongside the existing customer, agent and manager flow.
- The full PostgreSQL-enabled Go suite, `go vet`, WebAssembly build, seven
  conformance tests, generated inventory/coverage checks and `git diff --check`
  pass.
- Exact-operation review is now 214/1,207: 207 partial, 7 missing and 993
  unassessed. All 75 pinned Jira Service Management operations are reviewed.

Validation after custom service queues:

- Migration 069 adds manager-defined queues while retaining protected built-in
  all-open, SLA-attention, unassigned and assigned-to-me views.
- Administrators create, edit and delete queues with validated JQL. Queue
  evaluation uses the shared search compiler, issue-security scope and JQL
  ordering, then limits results to requests in the selected service desk.
- Queue lifecycle writes organization audit events and is included in both
  audit action catalogs. PostgreSQL proves permission denial, invalid JQL,
  filtering, updates, deletion and audit evidence.
- The combined Chromium service journey passes queue create/filter/rename/delete,
  WCAG scans and 320 px reflow.
- The full PostgreSQL-enabled Go suite, `go vet`, WebAssembly build, seven
  conformance tests, generated inventory/coverage checks and `git diff --check`
  pass.

Validation after service request-type forms:

- Migration 070 persists ordered fields, requirements and help text for every
  request type, and seeds summary plus description for existing and newly
  provisioned service projects.
- Site administrators configure summary, description and project-available
  text, number and date-time custom fields from the agent workspace. The JSM
  field metadata and portal form read the same configuration.
- REST validation rejects missing, unknown and unavailable fields. UI and API
  submission use the canonical Jira create path, so typed custom answers remain
  available to Jira search, synchronization and downstream integrations.
- The combined Chromium journey configures a required number field, submits it
  through the portal, reads it on the request, passes WCAG scans and reflows at
  320 px. PostgreSQL contract coverage proves metadata, validation and storage.

Validation after service calendar holiday administration:

- Managers add, rename and remove dated holidays from each desk's calendar.
  Commands enforce workspace-admin access, strict ISO dates and bounded names;
  store mutations scope through the desk and write searchable organization
  audit events.
- Existing SLA calculation already excludes persisted calendar holidays. The
  request clocks, attention queue and escalation runner therefore consume
  manager changes without a second configuration path.
- PostgreSQL coverage proves authorization, persistence, deletion and both
  audit records. The combined Chromium service journey passes holiday
  add/delete, WCAG scans and 320 px reflow.

Validation after the initial service report workspace:

- `/service/agent/{desk}/reports` gives service agents and managers selectable
  7, 30, and 90 day summaries of request intake, current open/resolved load,
  requests with breached SLA cycles, and CSAT average/response count.
- The daily intake chart has a descriptive image role and an exact,
  keyboard-reachable table. SLA breaches use the existing calendar-aware cycle
  calculation, including manager-configured holidays.
- PostgreSQL coverage proves aggregation, allowed windows and CSAT inclusion.
  The Chromium manager journey verifies values, window selection, WCAG scans
  and 320 px reflow.

Validation after conditional service SLA goals:

- Migration 071 gives every SLA metric a durable default plus up to 50 ordered
  JQL conditions. New and reopened cycles snapshot the first matching goal;
  default goals provide the fallback, completed cycles retain their historical
  target, and edits update only active cycles assigned to that goal.
- The shared JQL engine now supports Jira's multi-value `labels` field for
  equality, inequality, membership, text and empty checks. Criteria reject
  query ordering; rule evaluation follows each goal's persisted creation order.
- Manager create/update/delete operations enforce workspace administration and
  emit searchable organization audit records. The request UI exposes the
  selected goal name while JSM SLA responses retain their pinned DTO shape.
  PostgreSQL and Chromium coverage prove the
  manager configuration and customer request journey, including WCAG and
  320 px reflow.

Validation after service report segmentation:

- Service agents and managers filter each 7, 30, or 90 day report by request
  type, channel, and open/resolved state. The same scoped predicates drive the
  headline totals, CSAT, daily volume, breach calculation, and exact breakdown
  tables; window changes retain active filters.
- Request-type and channel tables remain available alongside the accessible
  daily chart, including explicit empty states. PostgreSQL coverage proves
  status, request-type and channel segmentation plus invalid-filter rejection.
- The combined Chromium service journey filters to the single resolved request,
  preserves the filter while changing window, passes axe WCAG checks, and
  reflows without document overflow at 320 px.

Validation after service operations intake and linkage:

- Migration 072 backfills existing desks with ordered Problems and Changes
  groups plus `Investigate a problem` and `Request a change` forms. New service
  projects provision the same four help/incident/problem/change entry points.
- Problem and change descriptions are required. Shared request creation assigns
  exactly one deterministic operations label, keeping Jira JQL, automation,
  queues, DORA and service reports on the canonical work item.
- Agents link and unlink visible related Jira work from a request; portal
  customers cannot see or mutate this internal operations graph. PostgreSQL
  coverage proves provisioning, validation, labels and link persistence. The
  Chromium journey discovers the new portal types, submits a change, links it
  to an incident, and retains WCAG and 320 px checks.

Validation after Confluence page footer comments:

- Migration 073 adds page-scoped footer comments, arbitrary-depth replies and
  immutable comment versions. Current page and space visibility applies to
  every read and write; private-space comments stay out of both REST results
  and local-first action pages.
- Seven pinned Confluence Cloud v2 operations now cover global, page, direct
  child and single-comment reads plus create, optimistic update and delete.
  Writes accept both documented body shapes, validate the storage markup, keep
  author ownership, allow administrator moderation, and return the pinned
  status, pagination and Location behavior.
- The page journey presents accessible threaded discussion with reply, edit and
  cascading-delete controls. PostgreSQL coverage proves private-space
  isolation, author/admin authorization, stale-write rejection, safe markup,
  pagination shapes and lifecycle behavior; Chromium proves the complete
  authoring journey, axe checks and 320 px reflow.
- Exact reviewed API coverage is 221 of 1,207 operations: 214 partial, 7
  missing, and 986 unassessed. Confluence coverage begins at 7 of 348 reviewed.

Validation after footer-comment history, operations and likes:

- Migration 074 adds idempotent per-user comment likes with cascade cleanup and
  private-space-safe local-first actions. Page reads load engagement and version
  data in two bounded queries rather than once per comment.
- Five more pinned v2 operations expose comment versions, version navigation,
  author/admin permitted operations, like counts and paginated liker account
  IDs. Single-comment reads also honor the historical `version` selector.
- The page discussion lets every viewer like/unlike a comment and inspect edit
  history. PostgreSQL tests cover both like states, history bodies, operation
  authorization and private action filtering; Chromium covers like/unlike and
  the visible version message with axe and reflow retained.
- Exact reviewed API coverage is 226 of 1,207 operations: 219 partial, 7
  missing, and 981 unassessed. Confluence coverage is 12 of 348 reviewed.

Validation after Confluence labels:

- Migration 075 adds reusable workspace labels plus page and space
  associations. Adds are canonical, bounded and idempotent; removal and every
  changed association emit permission-filtered local-first actions. Orphaned
  label records never appear in discovery results.
- Seven legacy v1 operations add/remove content and space labels, return the
  expected LabelArray/LabelDetails shapes, and enforce workspace administration
  for space changes. Five v2 operations provide visible global, page, space,
  space-content and label-to-page discovery with prefix/ID filters, documented
  sorts and cursor pagination.
- The page journey adds comma-separated labels, exposes compact removable
  chips, and retains them across page edits. PostgreSQL coverage crosses v1
  writes with v2 reads and proves private page/action isolation; Chromium proves
  add/remove, persistence, axe accessibility and 320 px reflow.
- Exact reviewed API coverage is 238 of 1,207 operations: 231 partial, 7
  missing, and 969 unassessed. Confluence coverage is 24 of 348 reviewed.

Validation after Confluence page restrictions:

- Migration 076 adds independent read and update grants for active workspace
  users and groups from the site's organization directories. Page authors and
  workspace administrators retain recovery access; every page, version,
  comment, like, label and action-log read applies the current read grants, and
  page, trash and label mutations apply the current update grants.
- All 12 pinned legacy v1 restriction operations support complete replacement,
  additive grants, clearing, operation-shaped reads, direct group status and
  mutation, and `accountId` plus legacy user lookup. Responses include stable
  hashes, legacy collection beans, relative links and boolean status results.
- The page access workspace exposes separate view/edit choices for people and
  groups and explains unrestricted columns. PostgreSQL covers author, member,
  administrator and group access, permission revocation and private actions;
  Chromium proves a two-account hidden → view-only → editor handoff, retained
  authoring, axe accessibility, dark mode and 320 px reflow.
- Exact reviewed API coverage is 250 of 1,207 operations: 243 partial, 7
  missing, and 957 unassessed. Confluence coverage is 36 of 348 reviewed.

Validation after versioned Confluence page attachments:

- Migration 077 stores page attachment identity and immutable file versions;
  blobs stream through the existing bounded filesystem abstraction with orphan
  cleanup. Current read and update restrictions protect metadata, bytes and
  mutations, and attachment actions inherit page visibility.
- Five legacy v1 and seven v2 operations cover upload/create-or-update,
  replacement, metadata update, redirect/download, global and page listing,
  version reads, permitted operations and deletion. The page UI exposes upload,
  replacement, download and deletion with retained comments and versions.
- PostgreSQL and filesystem integration exercise the complete three-version
  lifecycle, historical bytes, both API generations and deletion. Chromium
  covers upload and replacement within the two-account restricted-page journey.
- Exact reviewed API coverage is 262 of 1,207 operations: 255 partial, 7
  missing, and 945 unassessed. Confluence coverage is 48 of 348 reviewed.

Validation after Confluence attachment properties and labels:

- Migration 078 adds attachment-scoped JSON properties with immutable
  optimistic versions and attachment-to-workspace-label associations. Shared
  commands validate property values and label names; page update restrictions
  protect every mutation while page read restrictions protect direct, expanded
  and reverse-lookup reads.
- Five v2 property operations cover create, filtered and sorted collection
  reads, item reads, version-checked updates and deletion. Two v2 label
  operations cover attachment labels and label-to-attachment discovery. The
  attachment resource now expands both collections and returns a usable page
  link containing its space and page IDs.
- The page UI displays attachment labels and JSON properties and lets editors
  add/remove labels plus create, edit and delete properties. PostgreSQL tests
  cover duplicate and stale updates, reverse lookup, expansions and deletion;
  restricted attachment metadata is excluded from global API and action-log
  reads. Chromium covers the full metadata journey with axe, dark-mode and
  320 px reflow checks retained.
- Exact reviewed API coverage is 269 of 1,207 operations: 262 partial, 7
  missing, and 938 unassessed. Confluence coverage is 55 of 348 reviewed.

Validation after Confluence attachment comments and thumbnails:

- Migration 079 generalizes the existing immutable footer-comment lifecycle to
  attachment targets while preserving the parent page used for space, content
  restriction and action-log filtering. Replies retain their target; item,
  history, operation and like resources work for both page and attachment
  comments, and deleting an attachment cascades its discussion.
- The v2 attachment-comment collection validates optional attachment versions,
  supports storage bodies, documented sorts and cursor pagination. The v2
  thumbnail operation redirects to an authenticated byte path with bounded
  input size and decoded pixels, version selection, 1–4096 dimensions and
  GIF/JPEG/PNG-to-PNG rendering.
- Page attachments expose a threaded discussion journey that uses the shared
  comment commands. PostgreSQL and filesystem integration covers attachment
  comments, replies, cascade deletion, restricted-content action privacy and a
  decoded 3×2 PNG result; Chromium covers comment creation alongside attachment
  labels and properties.
- Exact reviewed API coverage is 271 of 1,207 operations: 264 partial, 7
  missing, and 936 unassessed. Confluence coverage is 57 of 348 reviewed.

Validation after Confluence content, space and label watches:

- Migration 080 stores one durable subscription model for page content, space
  keys and label names. Current users manage their own watches; workspace
  administrators can use Cloud `accountId` and deprecated username/key
  selectors for another active directory member. Mutations are idempotent and
  changed state emits a watcher-private sync action.
- All 12 pinned Confluence v1 watch operations now support status, mutation,
  watcher discovery, legacy required watcher fields, numeric content IDs,
  validated pagination and each operation's documented XSRF header. Page,
  space, and label targets retain current content visibility boundaries.
- Published page changes deliver one synchronized in-app notification per
  visible watcher across overlapping content, parent-content, space, and label
  subscriptions. Minor edits suppress delivery. Notifications resolve through
  a permission-checked wiki redirect; email delivery remains future work.
- Space, page, and label watch controls are available in the knowledge UI.
  PostgreSQL integration covers self/admin targeting, legacy lookup, XSRF,
  pagination, deduplication, minor edits and private action filtering. Chromium
  covers watch persistence, a second editor's update, one notification, its
  return to the page, accessibility, dark mode and 320 px reflow.
- Exact reviewed API coverage is 283 of 1,207 operations: 276 partial, 7
  missing, and 924 unassessed. Confluence coverage is 69 of 348 reviewed.

Validation after Confluence page inline comments:

- Migration 081 extends the durable comment model with page inline anchors,
  match coordinates and open, reopened, resolved or dangling state while
  keeping footer and inline collections separate.
- Twelve pinned Confluence v2 operations now cover global and page collections,
  creation, item update/delete, children, operations, likes and immutable
  versions. Top-level creation verifies the exact storage-text match; replies
  inherit the parent anchor; edits and resolution use optimistic versions.
- Inline actions remain private when page restrictions change. PostgreSQL
  integration covers validation, replies, collection separation, resolution,
  likes, versions, deletion and restricted-content reads/action filtering.
- The page UI adds exact-passage discussions, replies, resolve/reopen and delete
  controls. The clean Chromium author journey covers creation and resolution,
  then passes the complete wiki lifecycle, WCAG scan, dark theme and 320 px
  reflow. Its scan also prompted a 32 px minimum label-chip target fix.
- Exact reviewed API coverage is 295 of 1,207 operations: 288 partial, 7
  missing, and 912 unassessed. Confluence coverage is 81 of 348 reviewed.

Validation after Confluence page tasks:

- Migration 082 adds durable page tasks with local IDs, storage bodies, status,
  creator, optional assignee and due date, and completion actor/time. Cascading
  page deletion and status invariants keep task lifecycle state coherent.
- All three pinned Confluence v2 task operations now provide visible global and
  item reads plus edit-permission status updates. The list validates and applies
  every documented task, content, identity, status, blank-body and epoch-time
  filter, including repeated/comma-separated values and cursor pagination.
- Task changes emit page-scoped actions. PostgreSQL integration covers blank
  tasks, storage bodies, cumulative filters, completion metadata, invalid
  values and restricted-page item/action privacy. Omitted PostgreSQL array
  parameters are explicitly treated as empty filters.
- Page authors can add a task, choose an active workspace assignee and due date,
  then complete or reopen it beside the page. The clean Chromium journey covers
  task assignment and completion alongside the full wiki lifecycle, WCAG scan,
  dark theme and 320 px reflow.
- Exact reviewed API coverage is 298 of 1,207 operations: 291 partial, 7
  missing, and 909 unassessed. Confluence coverage is 84 of 348 reviewed.

Validation after Confluence page hierarchy resources:

- Recursive page hierarchy reads share the current space, draft/publication and
  direct-user/group restriction boundaries. Descendants carry relative depth
  and stable creation-order sibling positions; ancestors return highest first.
- Four pinned v2 operations now expose page children, generic direct children,
  depth-bounded descendants and ancestors with the documented minimal beans,
  child sort choices, limit and cursor validation. Two pinned v1 operations
  expose the legacy descendant map and typed page array with depth/start/limit.
- PostgreSQL integration covers a two-level page tree, direct-versus-recursive
  results, ancestor ordering, v1 shapes, invalid depth, cleanup safety and 404s
  for restricted roots. The existing Chromium author journey already covers
  parent selection, child creation and the permission-filtered page tree.
- Exact reviewed API coverage is 304 of 1,207 operations: 297 partial, 7
  missing, and 903 unassessed. Confluence coverage is 90 of 348 reviewed.

Validation after Confluence folders:

- Migration 083 adds a global-ID hierarchical-content substrate for folders,
  databases, Smart Links and whiteboards, immutable content versions, and
  optimistic versioned JSON properties. A stored root page carries page
  restrictions through nested non-page content without copying grants.
- All 12 pinned Confluence v2 folder operations now cover create/read/delete,
  page/folder ancestors, bounded descendants, sorted cursor-paged direct
  children, permission-shaped operations, and property list/create/read/update/
  delete. Nonempty folders and pages with attached folders cannot be trashed.
- Folder and property changes share atomic action records and action-feed
  privacy. PostgreSQL integration covers nested hierarchy, expansions,
  duplicate keys, stale property versions, deletion order, and private-space
  isolation.
- The space UI creates folders below pages or folders, shows their parent and
  creation time, prevents nonempty deletion, searches across content, and
  exposes an accessible delete flow. The clean-database Chromium wiki journey
  passes, including WCAG scans, dark theme and 320 px reflow. Its persistent
  label-watch and notification selectors are now repeat-run safe.
- Exact reviewed API coverage is 316 of 1,207 operations: 309 partial, 7
  missing, and 891 unassessed. Confluence coverage is 102 of 348 reviewed.

Validation after Confluence Smart Links:

- All 12 pinned Confluence v2 Smart Link operations reuse the hierarchical
  content and optimistic property substrate for create/read/delete, ancestors,
  descendants, direct children, operations and property lifecycle resources.
- Smart Links accept optional absolute HTTP/HTTPS URLs without credentials;
  unsafe schemes and credential-bearing URLs fail before persistence. Folder
  and Smart Link reads now demonstrate heterogeneous nesting in both
  directions while retaining root-page permission inheritance.
- PostgreSQL integration covers every Smart Link route, supported expansions,
  URL validation, page/folder/link ancestry, a folder child, nonempty deletion,
  optimistic property updates and private-space action isolation. The shared
  handler refactor keeps folder wire behavior covered by the same suite.
- The space journey adds searchable Smart Links under pages, folders or links,
  opens destinations with safe external-link attributes, and exposes guarded
  deletion. A clean-database Chromium run passes the full wiki journey,
  accessibility scans, dark theme and 320 px reflow.
- Exact reviewed API coverage is 328 of 1,207 operations: 321 partial, 7
  missing, and 879 unassessed. Confluence coverage is 114 of 348 reviewed.

Validation after Confluence databases:

- Migration 084 adds creator-private state and durable classification level
  state to the shared hierarchical-content model. Private database content,
  its properties and its action records remain visible only to the creator.
- All 15 pinned Confluence v2 database operations now cover public/private
  create, read/delete, ancestors, bounded descendants, cursor-paged direct
  children, permission-shaped operations, optimistic JSON properties, and
  classification read/set/reset. Classification currently uses four built-in
  published levels; organization-defined level administration remains.
- Database containers interoperate with pages, folders and Smart Links in the
  heterogeneous tree. PostgreSQL integration covers expansions, nesting,
  nonempty deletion, property conflicts, classification validation and reset,
  private reads, and action-feed isolation.
- The space UI creates public or creator-private databases under any delivered
  parent type, searches them, shows privacy and classification state, updates
  classification, and exposes guarded deletion. The Chromium journey covers
  private creation below a folder, classification, accessibility and reflow.
- Exact reviewed API coverage is 343 of 1,207 operations: 336 partial, 7
  missing, and 864 unassessed. Confluence coverage is 129 of 348 reviewed.

Validation after Confluence whiteboards:

- Migration 085 adds whiteboard template-key and locale state to the shared
  hierarchical-content model. The command path validates every template and
  locale enumerated by the pinned Confluence v2 contract, including the rule
  that a locale accompanies a template.
- All 15 pinned Confluence v2 whiteboard operations now cover public/private
  create, read/delete, ancestors, bounded descendants, cursor-paged direct
  children, permission-shaped operations, optimistic JSON properties, and
  classification read/set/reset. Canvas objects and editing are still separate
  product work because the pinned REST surface only models its container.
- PostgreSQL integration covers template persistence, invalid template/locale
  combinations, heterogeneous children, expansions, nonempty deletion,
  properties, classification, private reads and action-feed isolation.
- The space UI creates blank or template-backed whiteboards with all documented
  locales under any delivered parent type, shows template/privacy/classification
  state, updates classification, searches whiteboards and guards deletion. The
  clean Chromium wiki journey covers a localized incident-postmortem canvas,
  classification, accessibility, dark theme and 320 px reflow.
- Exact reviewed API coverage is 358 of 1,207 operations: 351 partial, 7
  missing, and 849 unassessed. Confluence coverage is 144 of 348 reviewed.

Validation after Service Management operations governance:

- Migration 086 adds per-desk CAB and incident-review policy, active-member CAB
  rosters, bounded on-call shifts, and request operations profiles for incident,
  problem, and change intake.
- Agents assess impact and likelihood on a four-by-four risk matrix, assign
  on-call ownership, capture change type/window/rollback data, and track
  post-incident review due dates, status, and findings. New operations requests
  inherit the active on-call owner, and incidents start with a pending review.
- Managers configure CAB thresholds and membership, incident-review deadlines,
  and rotations. Changes at or above the threshold create exactly one durable
  Change advisory board approval. Settings, shifts, and assessments are
  permission checked and written to the organization audit log.
- The Service Management Chromium journey covers manager policy and rotation
  setup, incident ownership/risk/review, and automatic CAB approval for a
  planned high-risk change, including idempotent reassessment and shift removal.
  PostgreSQL integration on a fresh migration, the full Go suite, vet, the WASM
  build, seven conformance checks, accessibility, dark theme and 320 px reflow
  passed.
- This is product behavior beyond the pinned public JSM contract, so exact API
  coverage remains 358 of 1,207 and JSM remains 75 of 75 reviewed.

Validation after Confluence blog posts:

- Migration 087 adds public and author-private blog posts with draft, published
  and trashed states plus immutable version history.
- Eight pinned Confluence v2 operations now cover global and per-space blog
  listing, create/read/update/delete, version listing and version detail.
  Optimistic version checks prevent lost updates, published-title conflicts are
  enforced per space, trash can be restored or permanently purged, and private
  posts stay out of reads and synchronized action feeds for other members.
- The Knowledge UI now gives authors a complete blog journey from space
  navigation through create, read, edit, history, trash, restore and purge. The
  clean Chromium journey and PostgreSQL integration cover that lifecycle.
- Exact reviewed API coverage is 366 of 1,207 operations: 359 partial, 7
  missing, and 841 unassessed. Confluence coverage is 152 of 348 reviewed.

Validation after Confluence blog metadata:

- Migration 088 adds blog-post labels, member likes, optimistic versioned JSON
  properties and built-in data classification. All metadata mutations are
  permission checked and enter the synchronized action feed.
- Thirteen more pinned v2 operations cover blog labels and label discovery,
  like counts/users, operation discovery, property CRUD and classification
  read/set/reset. Private metadata and its parent post remain invisible to other
  members in direct reads, global label discovery and synchronized actions.
- The author UI exposes like/unlike, classification, label add/remove and app
  property create/update/delete with responsive and accessible controls.
- Exact reviewed API coverage is 379 of 1,207 operations: 372 partial, 7
  missing, and 828 unassessed. Confluence coverage is 165 of 348 reviewed.

Validation after Confluence blog governance:

- Migration 089 adds durable UUID redaction metadata, registered custom-content
  types and blog-contained custom-content storage for the future app runtime.
- Current-version redaction validates timestamps, version numbers, Unicode
  ranges and storage markup; merges overlaps; creates a new version; optionally
  removes the selected text from earlier versions; emits permission-shaped sync;
  and writes an organization audit record without retaining removed text.
- Known custom-content types return sorted, cursor-paged, permission-filtered
  collections. Unknown types and private parent posts return 404.
- The author UI provides exact-text title/body redaction with an explicit
  history-cleaning choice. PostgreSQL integration covers stale requests,
  history cleanup, audit evidence and privacy. History cleanup is the explicit,
  audited compliance exception to otherwise immutable content versions.
- Exact reviewed API coverage is 381 of 1,207 operations: 374 partial, 7
  missing, and 826 unassessed. Confluence coverage is 167 of 348 reviewed.

Validation after Confluence blog attachments:

- Migration 090 generalizes the existing attachment record to exactly one page
  or blog-post parent while retaining immutable file versions and blob storage.
- Blog attachment collection, detail, versions, operation discovery, thumbnails
  and authenticated downloads enforce space and public/private post visibility.
- The blog UI supports upload, replacement and deletion, including version and
  comment feedback. PostgreSQL integration covers filtering, parent shape,
  operation discovery and private-post isolation.
- Exact reviewed API coverage is 382 of 1,207 operations: 375 partial, 7
  missing, and 825 unassessed. Confluence coverage is 168 of 348 reviewed.

Validation after Confluence blog discussions:

- Migration 091 adds a mutually exclusive blog-post target to the shared
  versioned footer/inline comment record and preserves page and attachment
  comment behavior.
- Blog footer comments support threads, replies and the common update, delete,
  version, like and operation APIs. Inline discussions validate exact storage
  passages and support replies plus resolve/reopen state.
- Public/private post visibility shapes reads and synchronized comment actions,
  including after the parent is purged. The UI covers authoring, replies and
  inline resolution, and the clean Chromium journey exercises the workflow.
- Exact reviewed API coverage is 384 of 1,207 operations: 377 partial, 7
  missing, and 823 unassessed. Confluence coverage is 170 of 348 reviewed.

Validation after Confluence page governance:

- Migration 092 adds durable page classification, active-user likes, UUID
  redaction records and page-contained registered custom content.
- Nine more pinned v2 operations cover the ordered classification-level
  catalog; page classification read/set/reset; like count/users; permission
  operations; guarded redaction; and typed custom-content discovery.
- Redaction checks the current timestamp and version, Unicode ranges and
  rendered storage markup, creates a new page version, can scrub matching text
  from prior versions, records an organization audit event and emits a
  permission-filtered sync action. Like actions retain page visibility and
  direct-user/group restrictions in sync.
- The author UI exposes like/unlike, page classification and exact-text
  redaction with optional history cleaning. PostgreSQL integration covers
  catalog order, validation, persistence, paging shapes, custom-content type
  isolation, stale redaction, history cleanup and private-space isolation.
- Exact reviewed API coverage is 393 of 1,207 operations: 386 partial, 7
  missing, and 814 unassessed. Confluence coverage is 179 of 348 reviewed.

Validation after Confluence page properties:

- Migration 093 adds uniquely keyed page JSON properties with immutable
  property-version history.
- Five pinned v2 operations cover permission-scoped list/filter/sort, create,
  exact read, optimistic update and delete. Mutations require page edit access,
  commit their sync record atomically and preserve private-space and page
  restriction filtering.
- The page UI lists app properties and lets editors create, update and delete
  them with explicit JSON and version feedback. PostgreSQL integration covers
  duplicate keys, stale writes, CRUD and private-page action isolation; the
  Chromium wiki journey covers creation and versioned update.
- Exact reviewed API coverage is 398 of 1,207 operations: 391 partial, 7
  missing, and 809 unassessed. Confluence coverage is 184 of 348 reviewed.

Validation after Confluence page version detail:

- Three more pinned v2 operations cover ordered, cursor-paged page version
  metadata, exact version detail with previous/next linkage, and a title-only
  update through the canonical optimistic page command.
- Version reads preserve page and draft visibility. Title updates require
  current status to match, preserve body and hierarchy, create an immutable
  page version and emit the existing permission-filtered page action.
- PostgreSQL integration covers descending history, exact detail, immutable
  version increment and rejected status mismatch. The existing page history UI
  and Chromium lifecycle consume the same stored versions.
- Exact reviewed API coverage is 401 of 1,207 operations: 394 partial, 7
  missing, and 806 unassessed. Confluence coverage is 187 of 348 reviewed.

Validation after Confluence core page lifecycle:

- Five core v2 operations now have reviewed behavior for page collection,
  create, expanded and historical read, optimistic update and trash.
- Collections accept multiple visible statuses, ID/space/title/subtype filters,
  documented page sort orders, storage bodies and cursor pagination. Exact
  reads can select a historical version and expand labels, app properties,
  operations, likes, versions, favorite status, collaborators and direct
  children, or omit the current version.
- Page creation explicitly rejects unsupported private, embedded and live-doc
  modes. Updates preserve same-space hierarchy, cycle checks, immutable
  history, watchers and sync. Trash keeps heterogeneous-child safety; draft
  deletion and permanent purge remain.
- PostgreSQL integration covers accepted expansions and historical bodies plus
  invalid booleans, filters, sorts, status mismatch and capability errors. The
  existing Chromium page journey covers create/read/update/trash/restore.
- Exact reviewed API coverage is 406 of 1,207 operations: 399 partial, 7
  missing, and 801 unassessed. Confluence coverage is 192 of 348 reviewed.

Validation after Confluence space governance:

- Four pinned v2 operations now cover reading, setting and clearing a space's
  default classification plus role-shaped space operation discovery.
- Workspace administrators choose one of the built-in levels in REST or the
  accessible space UI. New pages and blog posts inherit that level atomically;
  clearing the default affects future content without rewriting existing data.
- PostgreSQL integration covers missing/default/reset states, invalid levels,
  member denial, administrator operations and both inheritance paths. The
  Chromium wiki journey manages a default and observes inherited blog metadata.
- Exact reviewed API coverage is 410 of 1,207 operations: 403 partial, 7
  missing, and 797 unassessed. Confluence coverage is 196 of 348 reviewed.

## Current change

1. Resume Confluence space collections, permissions and other administration
   operations.
2. Continue Service Management with dependency mapping, major-incident
   communications, change-conflict calendars and escalation policy.
3. Expand the app runtime from registered custom-content discovery into signed
   installation, scopes and module rendering.

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
- [Reports and DORA behavior](REPORTS.md)
- [Service Management behavior](SERVICE_MANAGEMENT.md)
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
