# Screens

A screen is the reusable field layout behind a work item form: named tabs, each with an ordered list of fields. [Screen schemes](SCREEN_SCHEMES.md) decide which screen each form uses. Part of the [Jira platform](JIRA_PLATFORM.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All operations need *Administer Jira*, except the tab read noted below.

| Method and path | Behavior |
|---|---|
| `GET/POST /rest/api/3/screens` | Pages screens, or creates a screen with its first tab. Filters: `id`, `queryString`, `scope` (every screen is `GLOBAL`). `orderBy`: name or id. |
| `PUT/DELETE /rest/api/3/screens/{screenId}` | Changes name and description, or deletes a screen with its tabs and fields. |
| `GET /rest/api/3/screens/{screenId}/availableFields` | Fields from the catalog that are not yet on the screen. |
| `POST /rest/api/3/screens/addToDefault/{fieldId}` | Adds a field to the first tab of the default screen. |
| `GET /rest/api/3/field/{fieldId}/screens` | Pages the screens that show a field; `expand=tab` adds the tab. |
| `GET /rest/api/3/screens/tabs` | Tabs for the given `screenId`s (all screens if none), narrowed by `tabId`, paged with `startAt` and `maxResult` (100 at most). |
| `GET/POST /rest/api/3/screens/{screenId}/tabs` | Tabs in display order, or appends one. With `projectKey`, a project administrator may read a screen that the project's work type screen scheme uses. |
| `PUT/DELETE /rest/api/3/screens/{screenId}/tabs/{tabId}` | Renames a tab, or removes it with its fields. |
| `POST /rest/api/3/screens/{screenId}/tabs/{tabId}/move/{pos}` | Moves a tab to a zero-based position. |
| `GET/POST /rest/api/3/screens/{screenId}/tabs/{tabId}/fields` | A tab's fields in order, or adds one catalog field. |
| `DELETE /rest/api/3/screens/{screenId}/tabs/{tabId}/fields/{id}` | Removes a field. |
| `POST /rest/api/3/screens/{screenId}/tabs/{tabId}/fields/{id}/move` | Moves a field with `after`, or `position` `First`, `Earlier`, `Later` or `Last`. |

Screen names are unique per site, and tab names are unique per screen, both ignoring case. Ids are Jira-style numbers. Every change is written to the action log.

## Rules

- A screen always has at least one tab.
- A field appears at most once per screen, so moving it between tabs is an update.
- Removing a tab or field renumbers the rest, so positions stay dense.
- The default screen cannot be deleted, and neither can a screen that a screen scheme uses.
- Deleting a custom field removes it from every screen (trigger).

## Field catalog

A screen can only hold fields the forms can render. The system fields, in order: `summary`, `description`, `assignee`, `priority`, `labels`, `duedate`, `parent`, `components`, `fixVersions`, `versions`, `resolution`, `security` (Restrict to), `timetracking`, `issuetype` and `project` (`systemScreenFields`, `internal/store/screens.go`). After them come the site's custom fields. Adding any other field is refused. `issuetype`, `priority` and `resolution` offer the site's catalogues, and `parent` the work items one level above ([ISSUE_METADATA.md](ISSUE_METADATA.md)).

## Default screen

Every site has a `Default Screen` with one `Field Tab`. It holds every system field the forms render. A new custom field is added to it by trigger.

## UI

`/settings/screens`: create, rename and delete screens; add, reorder and remove tabs and fields.

## Code

`internal/api3/screens.go`, `internal/store/screens.go`, `internal/web/screens.go`, `migrations/133_screens.sql`; tests in `internal/api3/screens_test.go` and `e2e/screens.spec.ts`.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- A workflow transition screen does not point to a Screen. The `system:transition-screen` rule stores a comma-separated `fields` parameter (`Transition.ScreenFields`) instead of a screen id; see [WORKFLOW_RULES.md](WORKFLOW_RULES.md).
- Forms show a screen's fields as one flat list; tabs are not rendered.
- `GET .../tabs/{tabId}/fields` ignores `projectKey`, so project administrators cannot read tab fields.
- Reporter, Environment, Attachment and Linked issues are not in the field catalog.
- No screen copy action.
