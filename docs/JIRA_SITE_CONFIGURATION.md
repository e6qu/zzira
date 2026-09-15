# Jira site configuration

Updated: 2026-09-09

ZZIRA persists Jira-wide configuration per workspace and exposes the same state
through the administration UI and Jira Cloud REST v3 routes. Site and
organization administrators manage the announcement banner, optional work-item
features, time tracking, issue navigator columns, and documented editable
application properties from `/admin`. Every mutation appends an organization
audit event and an ordered workspace action in the same transaction.

## REST contract

| Method | Path | Authorization | Response |
|---|---|---|---|
| GET/PUT | `/rest/api/3/announcementBanner` | Administer Jira | Jira banner object / `204` |
| GET | `/rest/api/3/application-properties` | Administer Jira | all properties, a single `key`, or the properties whose whole key matches the `keyFilter` regular expression; `permissionLevel` `ADMIN` or `SYSADMIN` lists every editable property, `SYSADMIN_ONLY` none |
| GET | `/rest/api/3/application-properties/advanced-settings` | Administer Jira | advanced editable properties |
| PUT | `/rest/api/3/application-properties/{id}` | Administer Jira | updated property |
| GET | `/rest/api/3/configuration` | Jira access | global feature flags and enabled time settings |
| GET/PUT | `/rest/api/3/configuration/timetracking` | Administer Jira | selected provider / `204` |
| GET | `/rest/api/3/configuration/timetracking/list` | Administer Jira | Jira's provider and those active apps install |
| GET/PUT | `/rest/api/3/configuration/timetracking/options` | Administer Jira | current / updated options |
| GET/PUT | `/rest/api/3/settings/columns` | Administer Jira | ordered column objects / empty `200` |

The issue navigator accepts Jira's repeated `columns` form fields naming
navigable fields: Jira's navigable system fields, from `issuekey` and
`summary` to `workratio` and the aggregate time fields, and the site's custom
fields. A value that is not a navigable field answers 404. Empty input clears
every default column. The same catalog, with Jira's field labels, validates
filter columns, personal columns and the administration form.
Time settings validate supported units and formats and decimal work schedules.
When time tracking is disabled, the selected-provider route returns `204` and
the global configuration omits `timeTrackingConfiguration`.

The provider catalog holds Jira's built-in provider (`Jira`) and every
provider an active app declares through Connect's `jiraTimeTrackingProviders`
module. An app provider's key is the app key and module key joined by two
underscores, and its `url` is the app admin page its `adminPageKey` names, at
`/plugins/servlet/ac/{appKey}/{adminPageKey}`. A descriptor naming an admin page
the app does not declare is refused. Selecting a provider enables time
tracking; an unknown provider, or one whose app is suspended, answers 400. The editable
application-property catalog matches the properties documented by Jira Cloud.
The look and feel applies across the site:
- The application title names every page and the header's logo link, and
  shows beside the logo once *Show application title*
  (`jira.lf.logo.show.application.title`) is on, as in Jira.
- A configured logo, favicon and high-resolution favicon replace ZZIRA's, and
  the navigation colours style the header.
- The hero button background colours primary buttons in the light theme, when
  their white labels keep at least 4.5:1 contrast on it; a lighter colour,
  such as Jira's default `#3b7fc4`, leaves ZZIRA's accessible buttons in place.
- The complete and day date formats decide how automation runs, trash
  deadlines and rendered search dates display.

Unset properties keep ZZIRA's own look. Updates are validated by kind:
- display date patterns must be Java patterns the site can show, and browser
  picker formats must use supported `%` directives;
- colours must be hex;
- logo and favicon URLs must be site paths or http(s) URLs;
- the title must fit on one line.

The Java and browser formats of a picker pair are each validated on their own,
so either can be changed first.

## Browser journey

The site administrator can publish a public or signed-in announcement and make
it dismissible. Enabled announcements appear directly below the global header;
dismissal is remembered only for that banner hash, so edited announcements are
shown again. The same administrator can toggle supported Jira work-item
features, set work hours/days and display units, select the ordered navigator
columns, and edit advanced application properties. Core issue commands enforce
disabled attachment, linking, subtask, time-tracking, unassigned, voting, and
watching features, and issue pages hide the corresponding unavailable controls
while preserving removal of existing subscriptions and relationships.

The Playwright administration journey publishes and dismisses a banner, changes
time tracking and navigator columns, updates an application title property,
reads each result through its Jira REST route, checks accessibility and 320px
reflow, and restores shared fixture settings. The PostgreSQL API journey covers
member/admin authorization, default state, persistence, status codes, filtering,
form encoding, validation, disabled time tracking, and matching audit/actions.
