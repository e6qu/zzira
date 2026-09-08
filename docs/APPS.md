# ZZIRA app runtime

ZZIRA apps are installed per workspace by a site administrator. The runtime
accepts the native JSON descriptor below or a supported standard
`atlassian-connect.json` descriptor, encrypts the app's shared secret with
`ZZIRA_IDENTITY_ENCRYPTION_KEY`, persists granted scopes and modules, and keeps
installation, suspension, upgrade and uninstall events in both the app
lifecycle ledger and organization audit log.

```json
{
  "key": "example.operations",
  "name": "Operations companion",
  "baseUrl": "https://apps.example.com/operations",
  "version": "1.0.0",
  "scopes": ["read:jira-work", "read:app-storage", "write:app-storage", "manage:webhooks"],
  "modules": [
    {
      "key": "operations-page",
      "type": "jira:globalPage",
      "location": "jira.navigation",
      "title": "Operations companion",
      "body": "Incident and release context supplied by the app."
    }
  ],
  "lifecycle": {
    "installed": "/lifecycle/installed",
    "disabled": "/lifecycle/disabled",
    "uninstalled": "/lifecycle/uninstalled"
  },
  "webhooks": [
    {
      "key": "issue-events",
      "url": "/webhooks/issues",
      "events": ["jira:issue_created", "jira:issue_updated"],
      "jql": "project = OPS"
    }
  ],
  "scheduledTriggers": [
    {"key": "hourly-sync", "url": "/scheduled/hourly", "interval": "hour"}
  ]
}
```

A standard Connect descriptor can be installed without rewriting its top-level
shape:

```json
{
  "key": "example.connect.operations",
  "name": "Connect operations companion",
  "baseUrl": "https://apps.example.com/connect",
  "authentication": {"type": "jwt"},
  "scopes": ["READ", "WRITE"],
  "lifecycle": {"installed": "/installed", "uninstalled": "/uninstalled"},
  "modules": {
    "generalPages": [
      {"key": "operations", "url": "/operations", "name": {"value": "Operations"}}
    ],
    "jiraProjectPages": [
      {"key": "project-health", "url": "/project-health?project={project.key}", "iconUrl": "/project-health.svg", "weight": 40, "name": {"value": "Project health"}}
    ],
    "jiraProjectAdminTabPanels": [
      {"key": "project-controls", "url": "/project-controls", "location": "projectgroup3", "weight": 20, "params": {"source": "settings"}, "name": {"value": "Project controls"}}
    ],
    "jiraReports": [
      {"key": "delivery-risk", "url": "/delivery-risk?project={project.key}", "name": {"value": "Delivery risk"}, "description": {"value": "Release and incident risk"}, "reportCategory": "agile", "thumbnailUrl": "/delivery-risk.svg"}
    ],
    "jiraDashboardItems": [
      {"key": "release-health", "url": "/release-health?item={dashboardItem.id}", "name": {"value": "Release health"}, "description": {"value": "Current release health"}, "thumbnailUrl": "release-health.svg"}
    ],
    "webPanels": [
      {"key": "risk", "url": "/risk?issue={issue.key}", "location": "atl.jira.view.issue.right.context", "name": {"value": "Release risk"}}
    ],
    "webItems": [
      {"key": "shortcut", "url": "/shortcut", "location": "system.top.navigation.bar", "name": {"value": "Team shortcut"}}
    ],
    "contentBylineItems": [
      {"key": "review", "url": "/review?content={content.id}", "name": {"value": "Page review"}}
    ],
    "jiraIssueFields": [
      {"key": "risk-score", "name": {"value": "Risk score"}, "description": {"value": "Calculated release risk"}, "type": "number"}
    ],
    "jiraIssueContents": [
      {"key": "runbook", "name": {"value": "Incident runbook"}, "tooltip": {"value": "Add incident runbook"}, "icon": {"url": "/runbook.svg"}, "target": {"type": "web_panel", "url": "/runbook?issue={issue.key}"}}
    ],
    "jiraIssueContexts": [
      {"key": "delivery-context", "name": {"value": "Delivery context"}, "icon": {"url": "context.svg"}, "content": {"type": "label", "label": {"value": "3 linked deployments"}}, "target": {"type": "web_panel", "url": "/delivery-context?issue={issue.key}"}}
    ],
    "jiraIssueGlances": [
      {"key": "legacy-status", "name": {"value": "Legacy status"}, "icon": {"url": "status.svg"}, "content": {"type": "label", "label": {"value": "Ready"}}, "target": {"type": "web_panel", "url": "/legacy-status"}}
    ],
    "webhooks": [
      {"event": "jira:issue_updated", "url": "/webhooks/issues", "filter": "project = OPS"}
    ]
  }
}
```

Connect `READ` and write-capable scopes translate into the corresponding Jira
and Confluence grants. Supported Connect module families are `adminPages`, `generalPages`,
`jiraProjectPages`, `jiraProjectAdminTabPanels`, `jiraReports`,
`jiraDashboardItems`, `jiraIssueTabPanels`, Jira issue-view
`webPanels`, Confluence
`contentBylineItems`, scalar
`jiraIssueFields`, quick-add `jiraIssueContents`, collapsible
`jiraIssueContexts`, legacy `jiraIssueGlances`, Jira/Confluence navigation
`webItems`, and `webhooks`.
The parser rejects unsupported authentication modes, scopes, module families,
and web-panel locations instead of silently ignoring them.

Connect apps can manage tenant-specific remote issue panels, navigation web
items, scalar issue fields and keyed Jira webhooks through the
standard JWT-signed `GET`, `POST`, and `DELETE`
`/rest/atlassian-connect/1/app/module/dynamic` resource. `POST` accepts the
descriptor-shaped `webPanels`, `webItems`, `jiraIssueFields`, and `webhooks` objects and
registers the request atomically;
duplicate static/dynamic keys or any invalid entry reject the whole request.
`GET` returns the original grouped definitions. `DELETE` accepts repeated
`moduleKey` query parameters and removes every dynamic module when none are
provided. The runtime enforces the Connect limit of 100 modules per
installation. Dynamic webhooks require translated `READ`, one supported event,
a relative URL, and optional valid JQL. Conditions, property projection and
body exclusion are rejected until their delivery semantics are available.
Dynamic definitions survive uninstall/reinstall, while an upgrade that
promotes the same key to a static module removes the conflicting definition.
The same resource is available beneath both Jira and Confluence base paths.
Navigation web items support Jira `system.top.navigation.bar` and Confluence
`system.header/left` or `system.header/right`. They need no data scope to
render, open through the signed remote-page gateway, and reject unevaluated
conditions or unsupported locations explicitly.

Connect issue fields support `string`, `text`, `rich_text`, `number`, `date`,
and `datetime` descriptor types on the canonical text, number, and date-time
field model. Each field is workspace-owned and receives a stable
`customfield_NNNNN` ID. REST callers can also use the documented
`app-key__module-key` key for field discovery, create/edit/transition values,
and JQL. Active fields appear in issue create/edit/view metadata and the normal
Jira field resources. Uninstall hides them without deleting issue data;
reinstallation restores the same IDs, and dynamic-to-static promotion updates
the field in place. Select/read-only types, options, extraction definitions,
and field migration tasks remain explicit gaps.

Connect issue content validates the required key, name, tooltip, relative icon,
and relative `web_panel` target. Each active module appears beside the issue
description as an accessible quick-add action. Adding it persists an
issue-specific instance and opens the remote panel through the signed iframe
gateway with `issue.key`; removing it restores the quick-add action. Instances
are keyed by installation and descriptor module key, so reinstalling the app
does not discard the user's choice. Content-presence conditions and native-app
rendering flags remain unsupported and fail explicitly when they would change
host behavior.

Connect issue contexts validate their required key, name, label content,
relative icon and relative `web_panel` target. The selected module appears as a
collapsible panel below the issue field groups, retains its open state for the
current user, and renders its signed icon and remote content. The request
expands and supplies `issue.key`, `issue.id`, `project.key` and `project.id`.
Each app contributes its first eligible modern context, matching Jira's
single-context-per-app behavior. A standard issue property named
`com.atlassian.jira.issue:{appKey}:{moduleKey}:status` can add a positive
numeric badge, any of Jira's six lozenge appearances, or a signed relative
status icon. Badges above 99 display as `99+`; malformed status data is ignored
without hiding the context. Conditions and frontend change events remain
explicit gaps; conditions fail installation instead of being ignored.
Connect i18n display names and labels retain the documented 1,500-character
validation bound; native ZZIRA module titles retain their 255-character bound.
Legacy issue glances use the same validated icon, label and target contract.
When an app declares modern issue contexts, they replace its legacy glance
surface; otherwise ZZIRA shows only the first installed glance for that app,
matching Jira's single-glance behavior.

Connect issue tab panels validate their required key, name and relative URL,
accept the standard `params` map, use the default weight of 100, and preserve explicit weight when
ordering multiple tabs. Each active module joins Comments, Work log and History
in the issue activity switcher. Selecting it hides the native activity composer
and ledger and then loads a sandboxed signed iframe with expanded `issue.key`,
`issue.id`, `project.key` and `project.id` context. Returning to a native filter
restores its previous sort direction. Conditions remain an explicit gap and
fail descriptor installation instead of being ignored.

Connect project pages validate the required key, name, relative URL and
relative `iconUrl`, and honor descriptor weight when ordering multiple app
pages. Each active page appears in the current project's navigation and opens
in a project-scoped, sandboxed signed iframe. The remote request expands and
supplies both `project.key` and `project.id`; changing projects therefore opens
the same module with the selected project context. The project navigation
renders `iconUrl` through the authenticated signed-asset endpoint and retains
the built-in fallback for native modules. Project-page conditions remain an
explicit gap.

Connect project administration tabs validate the four standard
`projectgroup1` through `projectgroup4` locations, preserve group and weight
ordering, and append descriptor `params` to the signed remote URL. They appear
only in project settings for administrators and receive the same verified
`project.key` and `project.id` context as project pages. Conditions remain an
explicit gap and fail descriptor installation instead of being ignored.

Connect `adminPages` validate their 1–100 character key, i18n name, relative
URL, optional parameters and weight. Active pages join the site administration
navigation only for administrators and open in the shared sandboxed,
JWT-signed remote frame. The default Jira administration location is supported;
custom locations, conditions, cacheable requests, full-page presentation,
icons and the singleton `configurePage` remain explicit gaps.

Connect reports validate their key, name, description, relative URL, optional
relative thumbnail and the `agile`, `issue_analysis`, `forecast_management` or
`other` category. They join the selected project's report directory beside the
built-in DORA report. Opening an app report uses the common sandboxed frame and
expands, supplies and signs both `project.key` and `project.id`. The report
directory renders the descriptor thumbnail through an authenticated endpoint
that signs the validated app-relative image request.

Connect dashboard items validate their required key, name, description,
relative URL and thumbnail URL. They join the custom-dashboard gadget catalog
with their descriptor metadata and use the existing add, position, copy,
property and removal lifecycle. Remote items render in the common sandboxed
frame with expanded and signed `dashboard.id`, `dashboardItem.id`,
`dashboardItem.key` and `dashboardItem.viewType` context. Configuration
callbacks, refresh opt-in and conditions remain explicit gaps; descriptors that
request unsupported behavior fail installation. The gadget catalog renders the
descriptor thumbnail through the same authenticated signed-image endpoint used
by reports.

Supported scopes are `read:jira-work`, `write:jira-work`,
`read:confluence-content`, `write:confluence-content`, `read:app-storage`,
`write:app-storage`, and `manage:webhooks`. Supported module contracts are Jira
global and project pages, issue panels, issue activity tabs, issue contexts/glances and dashboard gadgets,
plus Confluence global pages and content byline items. Active global pages join
product navigation, issue panels and contexts join visible work-item views, app
gadgets can be added and positioned on custom dashboards, and byline items join
published wiki pages. Native module
text is host-rendered and escaped. A module with a relative `url`, including a
translated Connect module, opens beneath its descriptor `baseUrl` in a
sandboxed HTTPS iframe. ZZIRA supplies `xdm_e`, `xdm_c`, `cp`, `lic`, and `cv`
context parameters, expands supported issue/content placeholders, and signs
the exact request with a short-lived HS256 Connect JWT whose issuer is the
workspace client key.

Every app request includes:

- `X-Zzira-App-Key`: the installed descriptor key when calling product APIs;
- `X-Zzira-App-Timestamp`: Unix seconds within five minutes of the server;
- `X-Zzira-App-Request-Id`: a unique 8–128 character idempotency key;
- `X-Zzira-App-Signature`: lowercase hexadecimal HMAC-SHA256.

The signed bytes are:

```text
timestamp + "\n" + requestId + "\n" + upper(method) + "\n" + requestTarget + "\n" + hex(sha256(body))
```

`requestTarget` is the escaped path followed by `?` and the original raw query
when one is present, so filters and pagination are signed as well as the body.

Existing Connect clients may authenticate product and app-storage requests with
`Authorization: JWT <token>` instead of ZZIRA headers. ZZIRA accepts HS256 app
tokens with mandatory `iss`, `iat`, `exp`, and `qsh` claims, derives the app key
from `iss`, opens that installation's encrypted shared secret, and verifies the
signature before trusting any claim. QSH validation follows Connect canonical
method, product-context path, sorted query/form parameter, repeated-value and
percent-encoding rules. Context JWTs are not accepted on product APIs. The
verified request still receives the installation's normal product scopes and
stable app principal.

The runtime stores each valid request ID for 24 hours and rejects replay before
executing a signed request. Lifecycle callbacks use
`POST /apps/{appKey}/lifecycle/{enabled|disabled|upgraded|uninstalled}`. Upgrade
bodies are complete descriptors. An app may remove scopes during a signed
upgrade; added scopes require a new administrator-authorized installation.

Isolated JSON storage uses `GET`, `PUT`, and `DELETE`
`/apps/{appKey}/storage/{key}`. Reads require `read:app-storage`; writes and
deletes require `write:app-storage`. Values carry a monotonically increasing
version, and uninstall deletes the app's storage, scopes and rendered modules.

Descriptors can declare relative outbound callback paths. Lifecycle supports
`installed`, `enabled`, `disabled`, `upgraded`, and `uninstalled`. Webhooks
support issue create/update/delete, comment create/delete and attachment create
events; declaring them requires `manage:webhooks`. Each webhook begins at its
installation or upgrade watermark, may carry a JQL filter, and advances through
the workspace action stream transactionally. Scheduled triggers support
`fiveMinute`, `hour`, `day`, and `week`, with no more than five schedules and
one five-minute schedule in an app.

The outbound worker sends JSON with `POST` to the descriptor base URL plus the
relative callback path, preserving a path already present in `baseUrl`. It uses
the same timestamp, request ID and HMAC headers described above, adds
`X-Zzira-App-Key` and `X-Zzira-App-Event`, and signs the callback path and raw
query. Connect descriptors additionally receive an `Authorization: JWT` token
issued by the workspace client key. Lifecycle and webhook failures retry durably with
bounded exponential backoff and stop after five attempts. A failed scheduled
invocation is terminal; the next configured occurrence still runs. Database
row claims and a recovery lease make delivery safe across restarts and multiple
server replicas. Reinstallation clears callbacks left by the old credential
generation. Site administrators can inspect callback declarations, the next
scheduled time and the five most recent delivery states and errors.

Each installation owns a stable `app_principal_*` account. A signed request to
Jira REST v3, Agile REST, Jira Service Management REST, Confluence REST v1, or
Confluence REST v2 runs as that account after the runtime verifies
`X-Zzira-App-Key`. Safe HTTP methods require the corresponding
`read:jira-work` or `read:confluence-content` grant; mutations require the
matching `write:*` grant. The app account follows the same workspace, issue
security, page restriction and command-audit paths as other callers. It is
disabled on uninstall and restored with the same ID on an authorized
reinstallation.

Connect apps can use Jira's standard single-issue property routes to create,
replace, list, read and delete JSON values. The implementation preserves the
255-character key and 32,768-byte value limits plus Jira's create/update status
codes. This provides the storage contract required by issue-context status metadata;
bulk issue-property mutations remain a separate API slice.

The runtime is a ZZIRA execution contract for remotely hosted apps. Remaining
Connect module families, the remaining dynamic module types and webhook
options, `configurePage`, custom admin-page behavior, project/page-admin, issue-tab and issue-context conditions, dashboard-item
configuration/refresh/conditions, workflow modules, select/read-only issue
fields and option APIs,
descriptor-driven upgrade migrations, and Atlassian-hosted Forge compute remain
separate future slices.
