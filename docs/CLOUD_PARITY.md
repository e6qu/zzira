# Compatibility ledger

Where ZZIRA stands against Jira Cloud, Jira Software, Jira Service Management and
Confluence Cloud. Remaining work is in [PLAN.md](../PLAN.md); every surface has its
own page in the [docs index](README.md).

## Contracts

Atlassian's published OpenAPI documents are vendored in [api/specs](../api/specs)
and pinned in [pins.json](../api/specs/pins.json) ([PINNED.md](../api/PINNED.md)).
[cloud-operations.json](../api/conformance/cloud-operations.json) is generated from
them (`python3 api/conformance/inventory.py`; CI runs it with `--check`).

| Contract | Operations |
|---|---:|
| Jira Cloud Platform REST v3 | 617 |
| Jira Software Cloud REST | 105 |
| Jira Service Management Cloud REST | 75 |
| Confluence Cloud REST v1 | 130 |
| Confluence Cloud REST v2 | 218 |
| Automation REST | 15 |
| Organizations REST | 47 |
| **Total** | **1,207** |

[cloud-coverage.json](../api/conformance/cloud-coverage.json) assesses every
operation: all 1,207 are served and partial, none missing or unassessed.
"Partial" means a tested, useful implementation whose every option and wire
detail is not yet certified. [MATRIX.md](../api/conformance/MATRIX.md) groups the
same evidence by family.

## Compatibility promise

A client of the site-scoped Jira, Jira Software, Jira Service Management or
Confluence APIs works by changing its base URL: same paths, methods,
authentication, bodies, status codes, headers, paging, expansions, ids,
permission errors and concurrency rules. Automation is served at
`/gateway/api/automation/public/...`. Clients that hardcode `api.atlassian.com` or
`auth.atlassian.com` need an endpoint override. Atlassian billing, Atlassian's AI
models and Atlassian-hosted Forge compute are out of scope and return explicit
errors.

## Status

| Surface | Built | Remaining |
|---|---|---|
| Identity | Password, sessions, self-service API tokens (labelled, expiring, revocable, shown once); OIDC (Google, Entra, Atlassian 3LO, custom providers with encrypted rotation); identity linking; back-channel logout; login audit; an instance that accepts only its identity provider's sessions ([shauth-sso.md](shauth-sso.md)) | [Plan 6](../PLAN.md#6-enterprise-identity): SAML, SCIM, authentication policies, managed accounts |
| Organization administration | All 47 Organizations operations; directories, groups, domains (DNS verification), IP allowlists, managed profiles, audit, product plans ([ADMIN.md](ADMIN.md)) | [Plan 6](../PLAN.md#6-enterprise-identity) |
| Permissions | Permission, notification and issue security schemes enforced in the command layer behind every work item change, including board drags, navigator bulk actions and service agent actions; project roles; global permissions; anonymous REST reads ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)) | Everything Jira's permission model covers is built |
| Projects | Create and edit for all project types; categories, properties, features, sender, templates; archive, trash, restore, delete; components; versions ([PROJECT_GOVERNANCE.md](PROJECT_GOVERNANCE.md), [PROJECT_LIFECYCLE.md](PROJECT_LIFECYCLE.md)) | [Plan 1](../PLAN.md#1-work-item-model): sender use, template refs |
| Fields and screens | Work types on a hierarchy an administrator extends above Epic, work type schemes, priorities with their schemes, and resolutions, all administered in the browser; custom fields, contexts and options; field configurations; screens and screen schemes ([ISSUE_METADATA.md](ISSUE_METADATA.md), [JIRA_PLATFORM.md](JIRA_PLATFORM.md)) | [Plan 1](../PLAN.md#1-work-item-model): per-language translations for work types, priorities, resolutions and statuses |
| Work items | Create, edit, transition, comments, links, attachments, votes, watches, worklogs, time tracking, resolution, properties, archive, bulk edit/move/transition/delete/watch ([ISSUE_SURFACE.md](ISSUE_SURFACE.md), [BULK_ISSUES.md](BULK_ISSUES.md)) | [Plan 1](../PLAN.md#1-work-item-model): classification, worklog fields |
| Workflows | Workflow editor, drafts and publishing; conditions, validators, post functions, transition screens; workflow schemes ([WORKFLOW_RULES.md](WORKFLOW_RULES.md), [WORKFLOW_SCHEMES.md](WORKFLOW_SCHEMES.md)) | [Plan 1](../PLAN.md#1-work-item-model), [Plan 13](../PLAN.md#13-apps): app rules executed |
| Search | JQL with history predicates and 54 built-in functions, app functions, autocomplete, saved filters, sharing, subscriptions ([JQL.md](JQL.md), [FILTERS.md](FILTERS.md)) | [Plan 5](../PLAN.md#5-jql-and-filters) |
| Boards and sprints | Scrum and Kanban boards, backlog, ranking, epics, estimation, column and swimlane configuration, board administrators, parallel sprints; a drag runs a real workflow transition ([AGILE_BOARDS.md](AGILE_BOARDS.md)) | [Plan 1](../PLAN.md#1-work-item-model): boards and backlogs stop at the epic level |
| Plans | Plan REST, sources, exclusions, scenarios, teams, capacity, dependencies ([JIRA_SOFTWARE.md](JIRA_SOFTWARE.md)) | [Plan 2](../PLAN.md#2-plans): auto-scheduler, browser setup, views; [Plan 1](../PLAN.md#1-work-item-model): plans and reports roll up through every hierarchy level |
| Releases | Release hub, readiness, approvals, related work, release notes, ordering, unresolved-work moves ([RELEASES.md](RELEASES.md)) | [Plan 3](../PLAN.md#3-cross-project-releases) |
| Reports and dashboards | Sprint, velocity, cumulative flow, control, epic, version, created vs resolved, resolution time, DORA and service reports; dashboards, gadgets, wallboards, email ([REPORTS.md](REPORTS.md), [DASHBOARDS.md](DASHBOARDS.md)) | [Plan 4](../PLAN.md#4-boards-reports-and-dora): more reports, configurable DORA |
| Automation | All 15 Automation operations; event, scheduled, manual and incoming-webhook triggers; conditions; 14 action types; templates; audit ([AUTOMATION.md](AUTOMATION.md)) | [Plan 7](../PLAN.md#7-automation): connections, usage limits, Confluence triggers, branches |
| Service Management | Portals, request types, forms, queues, SLAs, approvals, CSAT, customers, organizations, knowledge base, incidents, problems, changes, reports ([SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md)) | [Plan 9](../PLAN.md#9-service-management) |
| Assets | Per-desk schemas, objects, relationships, request impact, portal field ([SERVICE_MANAGEMENT.md](SERVICE_MANAGEMENT.md#assets)) | [Plan 8](../PLAN.md#8-assets): object types, AQL, REST API, imports |
| Confluence | All v1 and v2 operations served; spaces, pages, blog posts, comments, templates, history, CQL, tasks, analytics, redaction, audit ([CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md)) | [Plan 10](../PLAN.md#10-confluence): import, export, macros, permissions UI |
| Whiteboards and databases | Form-driven canvas with objects and connectors; databases ([CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md#whiteboards)) | [Plan 11](../PLAN.md#11-whiteboards-and-diagrams) |
| Live collaboration | Polled presence and body merge, carets, offline typing ([CONFLUENCE_LIVE.md](CONFLUENCE_LIVE.md)) | [Plan 12](../PLAN.md#12-live-collaboration) |
| Development integrations | Dev info, builds, deployments, feature flags, remote links, operations, security, deployment gating, rate limits ([JIRA_SOFTWARE.md](JIRA_SOFTWARE.md#development-and-devops-data)) | [Plan 13](../PLAN.md#13-apps): async processing, issue view panels |
| Apps | Connect and native descriptors, signed lifecycle, JWT, scopes, storage, 20 Connect module families, dynamic modules, JQL functions, webhooks ([APPS.md](APPS.md)) | [Plan 13](../PLAN.md#13-apps) |
| Local-first | Work item replica, outbox, offline edits, convergence, revocation purge | [Plan 14](../PLAN.md#14-local-first-closure) |
| Accessibility | Light and dark themes, keyboard, reflow, axe checks ([ACCESSIBILITY.md](ACCESSIBILITY.md)) | [Plan 15](../PLAN.md#15-certification): the same coverage for every journey, browser and locale |

## References

- [Jira Cloud Platform REST v3](https://developer.atlassian.com/cloud/jira/platform/rest/v3/)
- [Jira Software Cloud REST](https://developer.atlassian.com/cloud/jira/software/rest/)
- [Jira Service Management Cloud REST](https://developer.atlassian.com/cloud/jira/service-desk/rest/)
- [Confluence Cloud REST v2](https://developer.atlassian.com/cloud/confluence/rest/v2/)
- [Confluence Cloud REST v1](https://developer.atlassian.com/cloud/confluence/rest/v1/)
- [Automation REST](https://developer.atlassian.com/cloud/automation/rest/)
- [Organizations REST](https://developer.atlassian.com/cloud/admin/organization/rest/)
- [Connect app descriptor](https://developer.atlassian.com/cloud/jira/platform/connect-app-descriptor/)
- [Forge platform](https://developer.atlassian.com/platform/forge/introduction/the-forge-platform/)
