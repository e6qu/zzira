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

- A screen always has at least one tab. It may have no fields: assigned empty screens hide optional controls; they do not mean the screen is unassigned. Create retains project, work type, summary and the mandatory parent for a subtask.
- A field appears at most once per screen, so moving it between tabs is an update.
- Removing a tab or field renumbers the rest, so positions stay dense.
- The default screen cannot be deleted, and neither can a screen that a screen scheme uses.
- Deleting a custom field removes it from every screen (trigger).

## Field catalog

A screen can only hold fields the forms can render. The system fields, in order: `summary`, `description`, `environment`, `assignee`, `reporter`, `priority`, `labels`, `duedate`, `parent`, `components`, `fixVersions`, `versions`, `resolution`, `security` (Restrict to), `timetracking`, `issuetype` and `project` (`systemScreenFields`, `internal/store/screens.go`). After them come the site's custom fields. Adding any other field is refused. `issuetype` and `resolution` offer the site's catalogues, `priority` the priorities of the project's priority scheme, and `parent` the work items one level above ([ISSUE_METADATA.md](ISSUE_METADATA.md)).

Reporter starts with the current user. With Modify reporter permission, the
create form offers other site members and None for an optional field. The
command checks that permission on submission and a refused create retains the
choice. Omitting Reporter defaults it to the creator; explicitly clearing it
requires a visible optional field.
Edit dialogs and inline Reporter controls follow the edit screen, field
configuration and Modify reporter permission. A transition screen can also
collect Reporter; people without that permission see its current value. Optional
Reporters can be cleared on edit or transition; required ones cannot. Changing
Reporter keeps the creator and records the old/new account in the changelog.
Former reporters who lose browse or security access receive a replica revocation
in the same transaction.

Environment is a system ADF field. Its create, edit and view controls follow the assigned screen and field configuration; transitions may collect it too. Unchanged textarea text preserves existing rich formatting. See [issue fields](ISSUE_FIELDS.md#environment).

## Default screen

Every site has a `Default Screen` with one `Field Tab`. It starts with the standard create fields, including Reporter. A new custom field is added to it by trigger.

## UI

`/settings/screens`: create, rename and delete screens; add, reorder and remove tabs and fields.

## Code

`internal/api3/screens.go`, `internal/store/screens.go`, `internal/web/screens.go`, `migrations/133_screens.sql`; tests in `internal/api3/screens_test.go` and `e2e/screens.spec.ts`.

Tab-field reads accept the same `projectKey` scope as tab reads: project
administrators can read fields on screens their project uses. Field mutations
remain restricted to site administrators.

Subtask creation always shows its required Parent beside Summary, including
when the create screen is empty or omits Parent. Field configuration cannot
hide that required context. Existing-work edit metadata still follows the edit
screen and field visibility.

## Tabs on forms

A screen with more than one tab is shown as tabs on the create form: one panel at a time, with the arrow keys moving between them. A screen with one tab is shown as a plain list, as a single tab is just a form.

## Custom fields on work items

Issue details use the view screen; the edit dialog uses the edit screen.
Description, priority, resolution, assignee, Reporter, labels, due date, parent,
security and time tracking follow view-screen and hidden-field rules. Existing
edit controls also apply the edit screen. Omitting Description from the editor
preserves its rich saved content.
Both apply the work type's field configuration and custom-field context.
A view-only field displays its saved value without an inline edit control.
Transition screens resolve their own fields, including fields absent from the
edit screen. Ordinary custom-field edits outside that screen are refused;
authorized screen-security overrides retain their existing behavior.

Regression evidence: `internal/web/custom_field_screens_test.go` and
`e2e/custom_field_screens.spec.ts`.

## Transition screens

A workflow transition can name a screen; the transition then asks for the fields that screen holds, and adding or removing a field on the screen changes the transition with no workflow edit and no new version. The workflow editor offers the site's screens beside its own field list ([WORKFLOW_RULES.md](WORKFLOW_RULES.md)).

**Copy.** The settings page copies one, named "Copy of X" (then "Copy 2 of X"), carrying its configuration and nothing else: the copy is assigned to no project and is never the site default.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- Attachment and Linked work items are not in the field catalog.
