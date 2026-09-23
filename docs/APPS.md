# Apps

Site administrators install remotely hosted apps per site from **Administration
› Apps** (`POST /admin/apps`). Apps are described by a native JSON descriptor or
a standard `atlassian-connect.json`.

At install, the runtime:
- encrypts the shared secret with `ZZIRA_IDENTITY_ENCRYPTION_KEY`;
- stores granted scopes and modules;
- creates an app principal;
- records install, suspend, upgrade and uninstall in the app lifecycle ledger
  and the organization audit log.

Apps call product APIs, receive signed lifecycle, webhook and schedule
callbacks, and render modules as signed iframes. Status:
[CLOUD_PARITY.md](CLOUD_PARITY.md).

## Descriptors

Native descriptor:

```json
{
  "key": "example.operations",
  "name": "Operations companion",
  "baseUrl": "https://apps.example.com/operations",
  "version": "1.0.0",
  "scopes": ["read:jira-work", "read:app-storage", "write:app-storage", "manage:webhooks"],
  "modules": [
    {"key": "operations-page", "type": "jira:globalPage", "location": "jira.navigation",
     "title": "Operations companion", "body": "Incident and release context."}
  ],
  "jqlFunctions": [
    {"key": "risk-issues", "name": "riskIssues", "url": "/jql/risk",
     "arguments": [{"name": "level", "required": true}], "types": ["issue"], "operators": ["in", "not in"]}
  ],
  "lifecycle": {"installed": "/lifecycle/installed", "uninstalled": "/lifecycle/uninstalled"},
  "webhooks": [{"key": "issue-events", "url": "/webhooks/issues",
                "events": ["jira:issue_created", "jira:issue_updated"], "jql": "project = OPS"}],
  "scheduledTriggers": [{"key": "hourly-sync", "url": "/scheduled/hourly", "interval": "hour"}]
}
```

**Native scopes:**
- Jira: `read:jira-work`, `write:jira-work`, `delete:jira-work`,
  `admin:jira-project`, `admin:jira`, `act-as-user:jira`.
- Confluence: `read:confluence-content`, `write:confluence-content`,
  `delete:confluence-content`, `admin:confluence`,
  `read:app-data:confluence`, `write:app-data:confluence`.
- Both: `access:email-addresses`.
- App features: `read:app-storage`, `write:app-storage`, `manage:webhooks`.

**Native module types:**

| Type | Placement |
|---|---|
| `jira:globalPage` | Product navigation |
| `jira:projectPage` | Project page |
| `jira:projectAdminPage` | Project settings |
| `jira:adminPage` | Site administration |
| `jira:report` | Report directory |
| `jira:issuePanel` | Issue view panel |
| `jira:issueTabPanel` | Issue activity tab |
| `jira:issueContent` | Issue quick-add |
| `jira:issueContext`, `jira:issueGlance` | Issue context panel |
| `jira:dashboardGadget` | Custom dashboard gadget |
| `jira:webItem` | Jira navigation item |
| `confluence:globalPage` | Confluence navigation |
| `confluence:contentBylineItem` | Page byline |
| `confluence:webItem` | Confluence navigation item |

**Module content:**
- **`body`:** host-rendered, escaped text (up to 20,000 characters).
- **Relative `url`:** a signed iframe.
- **Both:** reports, gadgets, project pages, contexts and glances may carry a
  `body` and a `url`.
- **Titles:** up to 255 characters.

**Connect descriptors** are accepted in their own shape. `authentication`
must be `jwt`, which is also the default:

```json
{
  "key": "example.connect.operations",
  "name": "Connect operations companion",
  "baseUrl": "https://apps.example.com/connect",
  "authentication": {"type": "jwt"},
  "scopes": ["READ", "WRITE"],
  "lifecycle": {"installed": "/installed", "uninstalled": "/uninstalled"},
  "modules": {
    "generalPages": [{"key": "operations", "url": "/operations", "name": {"value": "Operations"}}],
    "webPanels": [{"key": "risk", "url": "/risk?issue={issue.key}",
                   "location": "atl.jira.view.issue.right.context", "name": {"value": "Release risk"}}],
    "jiraIssueFields": [{"key": "risk-score", "name": {"value": "Risk score"}, "type": "number"}],
    "webhooks": [{"event": "jira:issue_updated", "url": "/webhooks/issues", "filter": "project = OPS"}]
  }
}
```

The parser rejects any authentication mode, scope, module family, location or
option it does not support; nothing is silently ignored. Connect name and label
values can be up to 1,500 characters.

## Connect module families

| Family | Behavior |
|---|---|
| `generalPages` | Global page in product navigation |
| `adminPages` | Site administration navigation, administrators only. Only the default location. `cacheable` and `fullPage` are refused. Accepts `params` and `weight` |
| `profilePages` | A page about one person, listed on their profile under **App pages** and opened at `/people/{accountId}/apps/{module}`. The frame is told `profileUser.accountId` and `profileUser.name`, both read from the directory rather than from the link |
| `configurePage` | The one page an app offers for setting itself up, linked as **Configure** beside the app on `/admin` rather than put in a menu, and opened at `/admin/apps/configure/{appKey}` by site administrators. The key defaults to `configure` and the name to "Configure" |
| `jiraProjectPages` | Project navigation, ordered by `weight`, with a signed `iconUrl`. Context: `project.key`, `project.id` |
| `jiraProjectAdminTabPanels` | Project settings for administrators, at `projectgroup1`–`projectgroup4`, ordered by group and then weight. `params` are appended to the URL |
| `jiraReports` | Project report directory. Category is `agile`, `issue_analysis`, `forecast_management` or `other`. Signed thumbnail. Context: project |
| `jiraDashboardItems` | Gadget catalog with signed thumbnail. Context: `dashboard.id`, `dashboardItem.id`, `dashboardItem.key`, `dashboardItem.viewType`. `configurable` adds **Configure** for dashboard editors (sends the `jira_dashboard_item_edit` event); `refreshable` adds **Refresh** |
| `jiraIssueTabPanels` | Tab beside Comments, Work log and History. Default weight is 100 and `params` are accepted. Selecting it hides the native composer. Context: issue and project |
| `webPanels` | Issue view at `atl.jira.view.issue.right.context`, `atl.jira.view.issue.left.context` or `atl.jira.view.issue.details` |
| `contentBylineItems` | Confluence page byline. Context: `content.id` |
| `webItems` | Jira `system.top.navigation.bar`, or Confluence `system.header/left` or `system.header/right`. Needs no data scope |
| `jiraIssueFields` | `string`, `text`, `rich_text`, `number`, `date`, `datetime`, `single_select`, `multi_select` (selects: [APP_FIELD_OPTIONS.md](APP_FIELD_OPTIONS.md)) |
| `jiraJqlFunctions` | App-evaluated JQL functions ([below](#jql-functions)) |
| `jiraIssueContents` | Quick-add action beside the description that opens a `web_panel` target |
| `jiraIssueContexts` | Collapsible panel under the issue fields, one per app, with an optional status badge |
| `jiraIssueGlances` | Legacy context panel; replaced by the app's `jiraIssueContexts` if it declares any |
| `jiraProjectPermissions`, `jiraGlobalPermissions` | App permissions ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)) |
| `jiraTimeTrackingProviders` | Time tracking providers ([JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md)) |
| `jiraEntityProperties` | Issue property extractions, searchable in JQL ([JQL.md](JQL.md#entity-property-search)) |
| `webhooks` | One event each, with an optional JQL `filter` (grants `manage:webhooks`) |

**Frames:**
- **Sandbox:** every remote module is a sandboxed HTTPS iframe under `baseUrl`.
- **Context parameters:** the host adds `xdm_e`, `xdm_c`, `cp`, `lic` and `cv`,
  and expands the supported placeholders.
- **Signing:** each request carries a short-lived HS256 Connect JWT whose issuer
  is the site client key.
- **Icons and thumbnails:** served through authenticated endpoints that sign the
  app-relative image request (`/app-modules/{module}/thumbnail|icon|status-icon`).

**Issue fields:**
- **IDs:** each field is owned by the site and gets a stable `customfield_NNNNN`
  ID. The `app-key__module-key` alias works in field discovery, create, edit,
  transition and JQL.
- **Where they appear:** in create, edit and view metadata, and in Jira's field
  resources.
- **Uninstall and reinstall:** uninstalling hides the fields but keeps their
  data, and reinstalling restores the same IDs.
- **Dynamic to static:** a dynamic field that becomes static is updated in
  place.

**Issue contents:**
- **Validation:** key, name, tooltip, relative icon and relative `web_panel`
  target are required.
- **Adding:** stores a per-issue instance, keyed by installation and module key,
  and opens the panel with `issue.key`.
- **Removing:** brings the quick-add action back.
- **Refused:** content-presence conditions, and native-app rendering flags that
  would change host behavior.

**Issue contexts:**
- **Panel:** its open or closed state is kept per user. Context: `issue.key`,
  `issue.id`, `project.key`, `project.id`.
- **Status badge:** the issue property
  `com.atlassian.jira.issue:{appKey}:{moduleKey}:status` sets it:
  - a positive number (shown as `99+` above 99);
  - one of Jira's six lozenge appearances;
  - or a signed relative icon.

  Malformed status data is ignored.
- **Legacy glances:** without modern contexts, only an app's first glance is
  shown.

## Connect conditions

Web panels, general pages, byline items, web items, admin pages, project pages,
project settings tabs, issue tab panels, issue contexts and glances, and
dashboard items can declare `conditions`.
- **Structure:** each entry is a single condition (optionally `invert`ed) or a
  group joined by `type` `AND`/`OR`. Every top-level entry must hold.

| Condition | Holds when |
|---|---|
| `user_is_logged_in` | Someone is signed in |
| `user_is_admin`, `user_is_sysadmin` | The viewer administers the site |
| `has_project_permission` | The viewer holds `params.permission` in the module's project |
| `has_issue_permission` | The viewer holds `params.permission` on the module's work item |
| `is_issue_assigned_to_current_user` | The work item is assigned to the viewer |
| `is_issue_reported_by_current_user` | The viewer reported the work item |
| `is_issue_unassigned` | The work item has no assignee |

- **Missing context:** a project or work item condition fails where the module
  has none, for example in site navigation.
- **Failing conditions:** the module is hidden from navigation, the issue view,
  bylines and the gadget catalog, and its page and frame return 404.
- **Frames:** conditions are evaluated against the project or work item named in
  the frame's context.
- **Invalid conditions:** any other condition, or a permission condition without
  a permission, fails installation or dynamic registration.

## Dynamic modules

`GET`, `POST` and `DELETE` on `/rest/atlassian-connect/1/app/module/dynamic`
(also under `/wiki`) use Connect JWT auth.
- **`POST`:** accepts `webPanels`, `webItems`, `jiraIssueFields`, `webhooks`,
  `jiraIssueGlances`, `jiraIssueContexts`, `jiraIssueContents` and
  `jiraEntityProperties`.
  - It is atomic: any invalid entry, or a key that duplicates a static or
    dynamic key, rejects the whole request.
- **`GET`:** returns the definitions grouped as they were registered.
- **`DELETE`:** removes the modules named by repeated `moduleKey` parameters, or
  all dynamic modules if none are named.
- **Limit:** 100 modules per installation.
- **Dynamic webhooks:** need translated `READ`, one supported event, a relative
  URL and optional valid JQL. `conditions`, `propertyKeys` and `excludeBody`
  are refused.
- **Lifetime:** definitions survive uninstall and reinstall. An upgrade that
  makes the same key static removes the dynamic definition.
- **Conditions:** dynamic modules keep their conditions, as static ones do.

## JQL functions

- **Declaration:** `jiraJqlFunctions` (or native `jqlFunctions`) records the
  key, name, relative URL, ordered arguments, field types and operators. The
  function then appears in JQL autocomplete.
- **Evaluation:** on first use, and after seven idle days, ZZIRA posts the
  clause and a precomputation ID to the app, signed with JWT and the callback
  headers.
  - The JQL the app returns replaces the clause and is cached.
  - Apps can also update values through
    `/rest/api/3/jql/function/computation`.
- **Safety:** every fragment is compiled by the JQL engine, nesting is bounded,
  and results keep the searcher's visibility.

## Authentication

**Native signed requests** carry four headers:
- `X-Zzira-App-Key`: the installed descriptor key.
- `X-Zzira-App-Timestamp`: Unix seconds, within 5 minutes of server time.
- `X-Zzira-App-Request-Id`: 8–128 characters, unique. It is kept for 24 hours;
  a replay is rejected.
- `X-Zzira-App-Signature`: lowercase hex HMAC-SHA256 of

```text
timestamp + "\n" + requestId + "\n" + upper(method) + "\n" + requestTarget + "\n" + hex(sha256(body))
```

`requestTarget` is the escaped path, plus `?` and the raw query when there is
one.

**Connect JWT:**
- **Accepted token:** product and storage calls may use
  `Authorization: JWT <token>`, an HS256 token with required `iss`, `iat`,
  `exp` and `qsh`.
- **Verification:** the app key comes from `iss`. The signature is checked
  against that installation's secret before any claim is trusted.
- **`qsh`:** follows Connect's canonical rules for method, product-context
  path, sorted query and form parameters, repeated values and
  percent-encoding.
- **Context JWTs:** refused on product APIs.

**App principal:** each installation has a stable `app_principal_*` account.
- Signed calls to Jira v3, Agile, JSM, DevOps provider and Confluence v1/v2
  APIs run as that account.
- It goes through the same issue security, page restrictions and command audit
  as any other caller.
- It is disabled on uninstall, and restored with the same ID when the app is
  reinstalled with authorization.

## Operation scopes

Each pinned Jira, Jira Software, JSM and Confluence operation needs the Connect
scope Atlassian assigns to it.
- **Source:** the table is generated from the pinned specs by
  `api/conformance/app_scopes.py`, and CI runs its `--check`.
- **Unpinned paths:** a product path that is not a pinned operation needs read
  for `GET` and write for any other method.

| Connect scope | Jira grant | Confluence grant |
|---|---|---|
| `READ` | `read:jira-work` | `read:confluence-content` |
| `WRITE` | `write:jira-work` | `write:confluence-content` |
| `DELETE` | `delete:jira-work` | `delete:confluence-content` |
| `PROJECT_ADMIN` | `admin:jira-project` | — |
| `SPACE_ADMIN`, `ADMIN` | `admin:jira` | `admin:confluence` |
| `ACT_AS_USER` | `act-as-user:jira` | — |
| `ACCESS_EMAIL_ADDRESSES` | `access:email-addresses` | `access:email-addresses` |
| `NONE` | none | none |
| `INACCESSIBLE` | refused to every app | refused to every app |

**Nesting:** administering covers deleting, deleting covers writing, and
writing covers reading. In Jira, site administration also covers project
administration.

**Descriptor translation:**
- `WRITE` and above grant read and write in both products.
- `DELETE` adds delete in both products.
- `PROJECT_ADMIN` adds Jira delete and `admin:jira-project`.
- `SPACE_ADMIN` adds delete in both products and `admin:confluence`.
- `ADMIN` adds all of these plus `admin:jira`.

## Lifecycle, storage, webhooks and schedules

- **Lifecycle:** `installed`, `enabled`, `disabled`, `upgraded`, `uninstalled`.
  - Apps report events with `POST /apps/{appKey}/lifecycle/{enabled|disabled|upgraded|uninstalled}`.
  - An upgrade body is a complete descriptor.
  - A signed upgrade may drop scopes. Adding scopes needs a new installation
    authorized by an administrator.
- **Storage:** `GET`, `PUT` and `DELETE` on `/apps/{appKey}/storage/{key}`.
  - Reads need `read:app-storage`; writes and deletes need
    `write:app-storage`.
  - Values carry an increasing version.
  - Uninstalling deletes storage, scopes and rendered modules.
- **Native webhooks:** events are `jira:issue_created`, `jira:issue_updated`,
  `jira:issue_deleted`, `comment_created`, `comment_deleted` and
  `attachment_created`, 1–20 per webhook, with optional JQL. Declaring them
  needs `manage:webhooks`.
  - Each webhook starts from its install or upgrade watermark and advances
    through the action stream in the same transaction.
- **Scheduled triggers:** `fiveMinute`, `hour`, `day` or `week`. At most five
  per app, and at most one `fiveMinute`.
- **Outbound delivery:** a JSON `POST` to `baseUrl` + the callback path.
  - Signed with the native headers plus `X-Zzira-App-Key` and
    `X-Zzira-App-Event`.
  - Connect apps also get `Authorization: JWT`.
  - Lifecycle and webhook failures retry with bounded backoff, up to five
    attempts.
  - A failed scheduled run is not retried; the next run still happens.
  - Row claims and a recovery lease keep delivery safe across restarts and
    replicas. Reinstalling clears callbacks tied to the old credentials.
- **Admin view:** administrators see callback declarations, the next scheduled
  time, and the last five delivery states and errors.
- **Issue properties:** Connect apps use Jira's issue property routes (keys up
  to 255 characters, values up to 32,768 bytes, Jira status codes), including
  the bulk routes ([ISSUE_SURFACE.md](ISSUE_SURFACE.md)).

## Connect JavaScript API

Remote pages load `AP` from `/atlassian-connect/all.js`.
- **Messaging:** calls go as messages to the host named in the frame's `xdm_e`.
  The host answers only frames it placed on the page, from the app's own
  origin.
- **Calls:**
  - `AP.resize(width, height)` accepts heights of 40–4,000 px, and
    `AP.sizeToParent()` fills the container.
  - `AP.context.getContext()` returns the Jira context, for example
    `jira.dashboard.id` and `jira.dashboardItem.id`.
  - `AP.events.on`, `once` and `off` handle host events such as
    `jira_dashboard_item_edit`.
  - `AP.jira.setDashboardItemTitle(title)`,
    `AP.jira.isDashboardItemEditable()` and
    `AP.jira.openDashboardItemEditor()` work on dashboard items.
  - `AP.request(options)` calls a product API as the current viewer, through
    `POST /app-modules/{module}/request`. It supports jQuery-style
    `success`/`error` callbacks or a promise of `{body, xhr}`.
    - It is allowed only for app-callable operations within the installation's
      scopes. Other paths return 400, and operations outside the scopes return
      403.
    - Dynamic module management still needs the app's own credentials.
  - `AP.require(['request', 'events', 'jira', 'context'], callback)` is
    available for older apps.

## DevOps provider rate limits

Bulk submissions to the development information, builds, deployments, feature
flags, remote links, security, operations and DevOps components APIs are
limited per caller.
- **Headers:** every response carries `X-RateLimit-Limit`,
  `X-RateLimit-Remaining` and `X-RateLimit-Reset` (ISO 8601).
- **When exceeded:** the API returns 429 with `Retry-After`, in that API's
  error shape.
- **Scope:** limit windows are stored in the database and shared by all
  servers.
- **Default:** 1,000 requests a minute.

## Gaps

See [PLAN.md](../PLAN.md).
- Forge: hosted compute, the manifest, UI Kit and Custom UI, and Forge modules.
  Forge app properties and UI modifications APIs exist
  ([JIRA_PLATFORM.md](JIRA_PLATFORM.md)).
- Jira Connect families not built:
  - `dialogs`, `webSections`, `keyboardShortcuts`;
  - `jiraWorkflowConditions`, `jiraWorkflowValidators`,
    `jiraWorkflowPostFunctions`;
  - `jiraSearchRequestViews`, `jiraBackgroundScripts`,
    `jiraProjectTabPanels`.
- Confluence Connect families not built:
  - `dynamicContentMacros`, `staticContentMacros`;
  - `customContent`, `blueprints`, `spaceToolsTabs`;
  - `confluenceContentProperties`.
- Admin pages: custom locations, `cacheable`, `fullPage` and icons.
- Other conditions: anything beyond the evaluated set, and content-presence
  conditions.
- Issue context frontend change events.
- Read-only issue field types. Field migration via descriptor.
- Dynamic webhook `conditions`, `propertyKeys` and `excludeBody`.
- Descriptor-driven upgrade migrations.

## See also

- [ADMIN.md](ADMIN.md)
- [JIRA_PLATFORM.md](JIRA_PLATFORM.md)
- [DASHBOARDS.md](DASHBOARDS.md)
- [APP_FIELD_OPTIONS.md](APP_FIELD_OPTIONS.md)
- [JQL.md](JQL.md)
- [CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md)
