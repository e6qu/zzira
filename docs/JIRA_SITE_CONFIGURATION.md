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
| GET | `/rest/api/3/application-properties` | Administer Jira | all properties, a single `key`, or `keyFilter` results |
| GET | `/rest/api/3/application-properties/advanced-settings` | Administer Jira | advanced editable properties |
| PUT | `/rest/api/3/application-properties/{id}` | Administer Jira | updated property |
| GET | `/rest/api/3/configuration` | Jira access | global feature flags and enabled time settings |
| GET/PUT | `/rest/api/3/configuration/timetracking` | Administer Jira | selected provider / `204` |
| GET | `/rest/api/3/configuration/timetracking/list` | Administer Jira | installed providers |
| GET/PUT | `/rest/api/3/configuration/timetracking/options` | Administer Jira | current / updated options |
| GET/PUT | `/rest/api/3/settings/columns` | Administer Jira | ordered column objects / empty `200` |

The issue navigator accepts Jira's repeated `columns` form fields, including
active workspace custom-field IDs. Empty input clears every default column.
Time settings validate supported units and formats and decimal work schedules.
When time tracking is disabled, the selected-provider route returns `204` and
the global configuration omits `timeTrackingConfiguration`.

The current provider catalog contains Jira's built-in provider. Marketplace
time-tracking-provider modules remain part of the app-platform PR. The editable
application-property catalog matches the properties documented by Jira Cloud;
some look-and-feel values are stored and returned but do not yet restyle every
ZZIRA surface. Exact Jira validation for paired Java/JavaScript date formats
also remains.

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
