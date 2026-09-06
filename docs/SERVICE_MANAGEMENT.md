# Jira Service Management

Updated: 2026-09-06

ZZIRA service projects use regular Jira issues as their workflow, automation,
search, security, release, and reporting record. Service request metadata adds
the portal, request type, customer, channel, and public conversation boundary.
This means an incident raised in the help center can flow through the same
workflow and contributes to DORA recovery time through its `incident` label.

## Customer journey

Authenticated users can open `/service`, choose a service portal, search its
request types, submit a typed request, review their requests, add public replies,
and execute currently available workflow transitions. The request page exposes
its status, request type, portal, channel, description, and conversation. The
browser journey is tested in light and dark themes, with WCAG A/AA axe checks
and 320 px reflow.

Assigned agents can read requests in their service desks by using
`requestOwnership=ALL_REQUESTS`, raise a request for an enrolled customer, add
internal notes, and create portal-only customer records. Site administrators
are implicit service managers across every desk. A customer can read only their
own requests and public comments; comments created through ordinary Jira issue
UI have no public marker and remain internal.

The agent workspace at `/service/agent` provides ordered all-open, unassigned,
and assigned-to-me queues for each service desk. Live counts and queue contents
update from the canonical request issue. Agents can open the full request,
review internal notes, assign a request to themselves, unassign it, comment,
and execute its workflow actions. Agent access is assigned per service desk and
is shared by the queue UI, request UI, and REST permission checks. Site
administrators can add or remove active workspace members from the desk roster.
Revocation immediately removes queue access, all-request visibility, request
management and internal-comment visibility for that desk.

Reporters and agents can add active service customers as request participants
by account ID or email. Participants appear on the request, can read its public
conversation and status, and lose that access immediately when removed. The
reporter remains a distinct role and cannot be added or removed as a participant.

## Service goals and calendars

Every service desk starts with a Monday-to-Friday 09:00–17:00 UTC business
calendar, a four-business-hour first-response goal and an eight-business-hour
resolution goal. Site administrators can change the calendar name, IANA time
zone, working days, daily window and both goal durations from the agent
workspace. New requests start both durable clocks. The first public agent reply
completes the response cycle, reaching a Done status completes the resolution
cycle, and reopening starts another resolution cycle.

The request page shows on-track, paused, breached and completed goal state.
Elapsed and breach time skip non-working days and persisted holidays and honor
time-zone transitions. The two Jira SLA REST operations are agent-only and
return Jira-compatible date, duration, completed-cycle and ongoing-cycle
shapes. A durable minute worker emits one approaching-goal and one breached
notification per clock and recipient, with transactionally synchronized
notification actions. The SLA attention queue shows requests inside the final
quarter of a goal and sorts breached requests first. Holiday administration,
conditional goals, status-driven pauses and multiple calendars remain.

## REST coverage

The current `/rest/servicedeskapi` slice implements:

- product info, service desk and request type discovery and administration;
- customer creation;
- request validation, creation, owned/all listing, and detail by issue ID or key;
- public and internal comment list, create, and detail;
- current request status; and
- condition-aware available transitions and transition execution with an
  optional comment; and
- queue list/detail/issues with optional live counts;
- participant list, add, and remove with participant-shaped visibility; and
- paged SLA list and metric detail with business-calendar cycles.

Request creation accepts string or Atlassian document format descriptions and
stores the backing issue through the shared command layer. Incident request
types automatically receive the `incident` label. Failed metadata association
is compensated by a logged issue deletion, so no orphaned ticket remains.

## Remaining fidelity

The implemented operations are assessed as partial. Custom queues and arbitrary
queue JQL, dynamic form/custom-field values, participant notifications and
organizations, request attachments, approvals, notifications, feedback, full
status chronology, conditional SLA goal criteria, calendar holidays,
portal invitation activation, knowledge suggestions, Assets, and
incident/problem/change configuration remain. Customer creation records a
portal-only account but does not silently grant Jira product access; a future
invitation policy will activate authentication with an `atlassian/customer`
role.
