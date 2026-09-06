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
upload and download customer-visible files, answer approvals assigned to them,
and execute currently available workflow transitions. The request page exposes
its status, request type, portal, channel, description, conversation, files,
approval state, notification preference, and satisfaction feedback. The browser journey is tested in light and dark themes,
with WCAG A/AA axe checks and 320 px reflow.

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

Agents can request an approval from an active site user. Every approver has an
independent pending, approved, or declined decision. Any decline completes the
approval as declined; otherwise it completes only after every approver accepts.
Only a pending assigned approver can answer, and an approver can open the request
even when they are neither its reporter nor a participant.

The portal and REST API share Jira's canonical attachment records and blob
store. Uploads first receive a one-use service-desk temporary ID, then become a
public or internal comment attachment in one finalize operation. Customers see
and download only public files; assigned agents see both. This boundary also
applies through the ordinary Jira attachment metadata and content routes.
Unclaimed temporary blobs expire after 24 hours and an hourly worker removes
their metadata and bytes.

Reporters, participants, approvers, and agents can subscribe to a request they
can view. Reporters and newly added participants start subscribed, approval
assignment also subscribes the approver, and each viewer can mute or resume
updates. Public comments, attachments, status changes, and approval decisions
create private inbox notifications for subscribed viewers other than the actor.
Internal notes notify subscribed agents only. Notification actions synchronize
through the existing per-user notification stream and open the portal request.

After a request reaches Done, its reporter can submit, revise, read, or delete a
one-to-five customer satisfaction rating with an optional comment. Agents and
other request viewers can read the result but cannot change it. Subscribed
agents receive a notification when the reporter leaves feedback.

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
- paged SLA list and metric detail with business-calendar cycles;
- approval list, detail, and assigned-user decisions; and
- temporary upload, request/comment attachment listing, finalize-with-comment,
  content, and thumbnail reads;
- per-user request subscription status, subscribe, and unsubscribe; and
- customer satisfaction feedback create, read, update, and delete.

Request creation accepts string or Atlassian document format descriptions and
stores the backing issue through the shared command layer. Incident request
types automatically receive the `incident` label. Failed metadata association
is compensated by a logged issue deletion, so no orphaned ticket remains.

## Remaining fidelity

The implemented operations are assessed as partial. Custom queues and arbitrary
queue JQL, dynamic form/custom-field values, participant notifications and
organizations, approval workflow configuration, image thumbnail generation,
email delivery and notification preference administration, CSAT configuration
and aggregate service reports, full
status chronology, conditional SLA goal criteria, calendar holidays,
portal invitation activation, knowledge suggestions, Assets, and
incident/problem/change configuration remain. Customer creation records a
portal-only account but does not silently grant Jira product access; a future
invitation policy will activate authentication with an `atlassian/customer`
role.
