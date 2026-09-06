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

Administrators can read all requests by using `requestOwnership=ALL_REQUESTS`,
raise a request for an enrolled customer, add internal notes, and create
portal-only customer records. A customer can read only their own requests and
public comments; comments created through ordinary Jira issue UI have no public
marker and remain internal.

The agent workspace at `/service/agent` provides ordered all-open, unassigned,
and assigned-to-me queues for each service desk. Live counts and queue contents
update from the canonical request issue. Agents can open the full request,
review internal notes, assign a request to themselves, unassign it, comment,
and execute its workflow actions. Until project-scoped agent roles are added,
this workspace and the queue APIs require site-administrator access.

Reporters and agents can add active service customers as request participants
by account ID or email. Participants appear on the request, can read its public
conversation and status, and lose that access immediately when removed. The
reporter remains a distinct role and cannot be added or removed as a participant.

## REST coverage

The current `/rest/servicedeskapi` slice implements:

- product info, service desk and request type discovery and administration;
- customer creation;
- request validation, creation, owned/all listing, and detail by issue ID or key;
- public and internal comment list, create, and detail;
- current request status; and
- condition-aware available transitions and transition execution with an
  optional comment; and
- queue list/detail/issues with optional live counts.
- participant list, add, and remove with participant-shaped visibility.

Request creation accepts string or Atlassian document format descriptions and
stores the backing issue through the shared command layer. Incident request
types automatically receive the `incident` label. Failed metadata association
is compensated by a logged issue deletion, so no orphaned ticket remains.

## Remaining fidelity

The implemented operations are assessed as partial. Custom queues and arbitrary
queue JQL, project-scoped agent roles, dynamic form/custom-field
values, participant notifications and organizations, request attachments, approvals,
notifications, feedback, full status chronology, SLAs, calendars, queues,
agent roles, portal invitation activation, knowledge suggestions, Assets, and
incident/problem/change configuration remain. Customer creation records a
portal-only account but does not silently grant Jira product access; a future
invitation policy will activate authentication with an `atlassian/customer`
role.
