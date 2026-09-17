# Jira site configuration

Jira-wide settings for a site: the announcement banner, optional work item features, time tracking, default issue navigator columns, and the editable application properties (including the look and feel). Site and organization administrators manage them under `/admin` › Jira configuration; the same state is served through Jira Cloud REST v3. Every change writes an organization audit event and a workspace action in the same transaction. Part of the [Jira platform](JIRA_PLATFORM.md) and [site administration](ADMIN.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

| Method | Path | Permission | Response |
| --- | --- | --- | --- |
| GET/PUT | `/rest/api/3/announcementBanner` | Administer Jira | Banner object / 204 |
| GET | `/rest/api/3/application-properties` | Administer Jira | All properties, one `key`, or those whose whole key matches the `keyFilter` regex. `permissionLevel` `ADMIN` or `SYSADMIN` lists every editable property; `SYSADMIN_ONLY` lists none. |
| GET | `/rest/api/3/application-properties/advanced-settings` | Administer Jira | Advanced editable properties |
| PUT | `/rest/api/3/application-properties/{id}` | Administer Jira | Updated property |
| GET | `/rest/api/3/configuration` | Jira access | Global feature flags and, when enabled, time tracking settings |
| GET/PUT | `/rest/api/3/configuration/timetracking` | Administer Jira | Selected provider (204 when time tracking is off) / 204 |
| GET | `/rest/api/3/configuration/timetracking/list` | Administer Jira | Jira's provider and those active apps declare |
| GET/PUT | `/rest/api/3/configuration/timetracking/options` | Administer Jira | Current / updated options |
| GET/PUT | `/rest/api/3/settings/columns` | Administer Jira | Ordered columns / empty 200 |

## Behavior

**Features.** Attachments, work item linking, subtasks, time tracking, unassigned work items, voting, watching and parallel sprints can be turned off, and the attachment size limit set. Commands refuse the disabled actions. Work item pages hide the matching controls but still let people remove existing watches, votes and links.

**Announcement banner.** Public or signed-in only, optionally dismissible. It shows below the global header. Dismissal is remembered per banner hash, so an edited banner shows again.

**Time tracking.**
- Hours per day and days per week accept decimals. Display format and default unit are validated.
- The provider catalog holds Jira's built-in `Jira` provider and every provider an active app declares through Connect's `jiraTimeTrackingProviders`.
- An app provider's key is `{appKey}__{moduleKey}`. Its `url` is the app admin page named by `adminPageKey`, at `/plugins/servlet/ac/{appKey}/{adminPageKey}`. A descriptor naming an undeclared admin page is refused.
- Selecting a provider turns time tracking on. An unknown provider, or one from a suspended app, is 400.
- With time tracking off, `/configuration` omits `timeTrackingConfiguration`.

See [TIME_TRACKING.md](TIME_TRACKING.md).

**Navigator columns.**
- `PUT /settings/columns` takes Jira's repeated `columns` form fields. Each must be a navigable field: a system field (`issuekey`, `summary` through `workratio` and the aggregate time fields) or a site custom field. Anything else is 404.
- Empty input clears the defaults.
- The same catalog, with Jira's labels, validates filter columns, personal columns ([PEOPLE.md](PEOPLE.md)) and the admin form.

**Application properties.** The editable catalog matches the properties Jira Cloud documents. Updates are validated by kind:
- display date patterns must be Java patterns the site can render;
- browser picker formats must use supported `%` directives (each half of a picker pair is validated alone, so either can change first);
- colours must be hex;
- logo and favicon URLs must be site paths or http(s) URLs;
- the title must fit on one line.

**Look and feel.** The application properties style every Jira page. Unset properties keep ZZIRA's own look.
- The application title names every page and the header's logo link. It shows beside the logo when `jira.lf.logo.show.application.title` is on.
- A configured logo, favicon and high-resolution favicon replace ZZIRA's.
- The navigation colours style the header.
- The hero button colour styles primary buttons in the light theme only when white labels keep at least 4.5:1 contrast. Jira's default `#3b7fc4` does not, so ZZIRA's buttons stay.
- The complete and day date formats control how automation runs, trash deadlines and rendered search dates display.

## Tests

- `internal/api3/site_configuration_test.go`: authorization, defaults, persistence, status codes, filtering, form encoding, validation, disabled time tracking, audit events and actions.
- `e2e/admin.spec.ts`: publishes and dismisses a banner, changes time tracking and columns, updates the application title, reads each back through REST, and checks accessibility and 320px reflow.
