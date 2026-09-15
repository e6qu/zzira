# Jira Service Management

Updated: 2026-09-08

ZZIRA service projects use regular Jira issues as their workflow, automation,
search, security, release, and reporting record. Service request metadata adds
the portal, request type, customer, channel, and public conversation boundary.
This means an incident raised in the help center can flow through the same
workflow and contributes to DORA recovery time through its `incident` label.

## Customer journey

Authenticated users can open `/service`, choose a service portal, search its
request types and linked knowledge articles, submit a request-type-specific
form, review their requests, add public replies,
upload and download customer-visible files, answer approvals assigned to them,
and execute currently available workflow transitions. The request page exposes
its status, request type, portal, channel, description, conversation, files,
approval state, notification preference, and satisfaction feedback. The browser journey is tested in light and dark themes,
with WCAG A/AA axe checks and 320 px reflow.

Service desk administrators link existing Confluence spaces to individual service
desks from the agent workspace. Published pages in those spaces appear as
portal suggestions when their title or storage body matches the customer's
search. Customers can open the rendered article without receiving Confluence
product access; closed-portal admission still applies. The global and per-desk
knowledge REST searches use the same visibility boundary and provide Jira's
optional highlight markers and article source/content links.

Assigned agents can read requests in their service desks by using
`requestOwnership=ALL_REQUESTS`, raise a request for an enrolled customer, and add
internal notes. Site administrators create or reactivate portal-only customer
records without granting Jira product access. They can open a portal to all
active site customers or close it to direct and organization membership, invite
and remove desk customers, and durably revoke portal-only access. Revocation
removes the customer role and cannot be undone by request auto-enrollment. Site
administrators are implicit service managers across every desk.
Administrators of a service project are that desk's service desk
administrators: without site administration they manage its request types,
forms, queues, customers, knowledge base, calendar and SLA goals, and open the
agent workspace for it. Inviting new customers also needs site administration,
and request type properties need agent access as well.

The REST service desk list and service desk lookups answer only to a desk's
administrators, agents and the users its portal admits; others receive 403.
Organizations carry Jira's system generated `uuid` and `created` date, and
customers appear as Jira's UserDTO, with self, `jiraRest` and avatar links and
without platform-only account fields. A customer can read only their
own requests and public comments; comments created through ordinary Jira issue
UI have no public marker and remain internal.

The agent workspace at `/service/agent` provides ordered all-open, unassigned,
assigned-to-me, SLA-attention, and administrator-defined custom queues for each
service desk. Managers create, edit, and delete custom queues with validated JQL;
agents see matching service requests in the query's requested order. Built-in
queues cannot be edited or removed. Live counts and queue contents
update from the canonical request issue. Agents can open the full request,
review internal notes, assign a request to themselves, unassign it, comment,
and execute its workflow actions. A queue's checkboxes, with a select-all box,
choose up to 100 requests for its action bar, which assigns them to an agent
of the desk or leaves them unassigned, moves each to a chosen status through a
transition its own workflow offers, or adds the same internal note or reply to
the customer to each. Requests that cannot change, such as one already in the
status or without a transition to it, are named with the reason while the rest
change. Agent access is assigned per service desk and
is shared by the queue UI, request UI, and REST permission checks. Site
administrators can add or remove active workspace members from the desk roster.
Revocation immediately removes queue access, all-request visibility, request
management and internal-comment visibility for that desk.

Service desk administrators configure each request type's portal fields from the agent
workspace. Summary is always present, required and visible. Description and the
custom fields available to the project can be shown, required, ordered and given
customer help text. They can also be hidden from the portal with a preset value,
which every new request takes; a hidden required field needs a preset. The
portal UI and JSM field metadata read the same durable configuration.

The portal form asks for each visible field in its own terms. Select and
cascading select fields offer the options of the context that reaches the
desk's project, with a cascading select's child options grouped under their
parent, and multi-select fields offer a checkbox per option. Date, URL and
number fields use matching inputs, and labels are separated by spaces. A user
picker takes a site member's email address and a multi-user picker several,
separated by commas; the portal finds the member without listing the site's
people. The request page shows the chosen options and people by name.

Group, project, version and team picker fields ask for a choice instead: a
group picker offers the site's groups, a project picker the projects the
requester can browse, a version picker the desk project's versions that are
not archived, and a team picker the site's Atlassian teams; the multi-group
and multi-version pickers take several. The portal
form, the request type's REST `validValues` and the request page's display
all list the same choices, and a submitted answer outside them is refused.

A field can also be shown only for some answers. Administrators choose another
visible select or multi-select field of the same form, which is not conditional
itself, and the options of it that show the field. The portal hides the field
until one of those options is chosen. Portal and REST request creation require
a conditional field only while it is shown, and refuse an answer to a field the
other answers keep hidden.

Field metadata gives each field's Jira schema (type, custom field type,
`customId` and array `items`). Select, multi-select and cascading select fields
list the options of the context that reaches the desk's project as
`validValues`, with cascading children. `canRaiseOnBehalfOf` and
`canAddRequestParticipants` are true only for the desk's agents. Hidden fields
and their `presetValues` appear only to the desk's administrators who ask for
`expand=hiddenFields`.

UI and REST request creation reject unconfigured, hidden or missing fields,
validate typed values through the canonical Jira command layer, and store custom
answers on the backing issue. Request type lists filter by `groupId`, by
repeated `serviceDeskId`, and by `restrictionStatus`: `OPEN` keeps every zzira
request type and `RESTRICTED` keeps none. A `searchQuery` leaves out request
types in no group unless `includeHiddenRequestTypesInSearch` is true.

Agents can create customer organizations, add or remove active customers, store
JSON entity properties, and link organizations to the desks they work. A linked
organization admits its members to a closed portal. Customers see only their
own organizations and their properties; agents can filter and inspect the full
customer directory. The agent workspace includes customer invitation, portal
access, organization membership, and desk-link management.

Agents can request an approval from an active site user. Every approver has an
independent pending, approved, or declined decision. Any decline completes the
approval as declined; otherwise it completes only after every approver accepts.
A workflow status can also carry Jira's approval configuration. When a request
enters that status, an approval named after it opens for the users in the
configured user picker field, less the assignee or reporter when excluded, and
they are notified. It needs the configured number or percentage of approvals,
any decline declines it, and the configured approved or declined transition then
moves the request on. Administrators set or remove a status's approval from the
workflow editor's status approvals panel as well as through the workflow REST
API.
Only a pending assigned approver can answer, and an approver can open the request
even when they are neither its reporter nor a participant.

The shared JQL engine exposes the complete Jira Cloud approval-function family
over this same state. `approved()` and `pending()` select final step state;
`approver()` and `myApproval()` include pending and completed steps;
`myPendingApproval()` and `pendingApprovalBy()` require an unanswered approver;
and `myPending()` and `pendingBy()` retain users who already answered while the
step awaits someone else. Explicit users accept account IDs, usernames, email
addresses, and display names. Jira-supported `!=` forms exclude requests with
no approval field.

## Operations governance

Incident, problem, and change request types create an internal operations
profile alongside the backing Jira issue. Agents assess impact and likelihood
on a four-by-four matrix, assign an on-call owner, classify changes as standard,
normal, or emergency, and record planned windows and rollback instructions.
The request view shows the calculated Low, Medium, High, or Critical risk level.
Every agent can inspect the desk's active change calendar for the previous seven
and next 90 days. It derives conflicts from overlapping persisted windows,
excludes completed work, and links each change to its request. A planned change
also shows its conflicting active requests beside the operations assessment.
The agent workspace assembles an operations dependency map from the same issue
links agents manage on request pages. It includes links touching desk incidents,
problems, or changes, labels their direction, and omits either endpoint unless
the current agent can read both Jira issues.

Service desk administrators can turn on deployment gating from the agent
workspace. They connect a deployment provider, an installed app or any
provider, and choose the environment types to gate. Each deployment the
provider submits to a gated environment opens one change request in the desk,
raised by the desk's project lead for the submitter and naming the deployment
and its work items. The Jira Software gating status follows that request.
It is awaiting while approvals are pending, prevented once one is declined, and
allowed once approved or completed without approvals. It is invalid when the
request could not be opened. The details link the request, and ungated
deployments are allowed.

Agents can declare an operations request with the incident profile to be a
major incident. The request then gains a durable status-update timeline with
public and internal audiences. Reporters, participants, and approvers see only
public updates, while assigned desk agents see both and can publish updates.
Subscribers receive notifications through the same audience boundary. Every
publication records its audience in the organization audit log. Declassifying
the incident preserves its timeline and closes it to new publications.

During a major incident, desk agents give the Incident commander,
Communications lead and Technical lead roles to agents of the desk; each
assignment notifies its new holder. Agents also keep a list of stakeholders,
site members or email addresses outside the site, and can publish stakeholder
updates, a third audience. Stakeholder updates stay with the response team on
the request, off the customer timeline, and are emailed once to every
stakeholder address without adding stakeholders to the request. Role and
stakeholder changes are recorded in the organization audit log.

Service managers configure each desk's CAB threshold, approver roster, incident
review deadline, bounded on-call shifts, and ordered major-incident escalation
steps. Each escalation step selects an active workspace responder and a delay
from the current declaration. The minute scheduler atomically records and
notifies every due responder once for each declaration, while agents see sent
and waiting steps on the incident. Reclassifying a request as a new major
incident starts a new delivery generation.

An active shift assigns its owner when a new operations request arrives. A
change at or above the threshold creates one durable Change advisory board
approval for the configured members. Incident reviews start pending with a
calculated due date; agents can advance the review and persist its findings.
Policy, rotation, escalation, and assessment changes are permission checked and
recorded in the organization audit log.

## Assets and request impact

Each service desk has an administrator-managed inventory inside its durable
Assets workspace. A schema defines up to 30 required or optional text, number,
date, boolean, and select attributes. Objects validate their values against that
schema and keep explicit canvas coordinates. Administrators can create and
delete schemas, create, edit, move, and delete objects, and connect two objects
with a named directional relationship. Schema and object deletion cascade to
their relationships and request links. Every mutation writes its state and
ordered action in one transaction.

The Assets workspace presents the same dependency data as a scalable SVG map
and an accessible relationship table. The source of an arrow depends on its
target. Assigned desk agents can inspect the inventory but only site
administrators can change it; portal customers cannot read inventory data.

On an agent-visible request, an agent can mark an object as directly affected or
as a request dependency. Impact analysis walks the reverse dependency graph so
every upstream object is listed with its shortest relationship depth. Traversal
is cycle-safe, bounded to eight levels, desk-scoped, and ordered consistently.
Direct links remain editable on the request while inferred impact stays derived
from the current topology.

The portal and REST API share Jira's canonical attachment records and blob
store. Uploads first receive a one-use service-desk temporary ID, then become a
public or internal comment attachment in one finalize operation. Customers see
and download only public files; assigned agents see both. This boundary also
applies through the ordinary Jira attachment metadata and content routes.
Unclaimed temporary blobs expire after 24 hours and an hourly worker removes
their metadata and bytes.

Temporary uploads come from the desk's agents and the customers its portal
admits. They follow the site's attachment switch and upload size limit, and
each desk's own switch: service desk administrators turn attachments off for
one desk from the agent workspace, which also removes the file field from that
desk's request conversations.

Service desk administrators can likewise turn customer satisfaction feedback off
for a desk from the agent workspace. The portal then stops asking for ratings,
and the feedback REST operation refuses new ratings for that desk. Connect apps
may leave or delete feedback on a reporter's behalf.

Service desk agents and administrators create customer organizations, but only
site administrators, who hold the Jira administrator permission, delete them;
the agent workspace offers deletion only to them. Knowledge base searches page
with Jira's opaque cursors, and `GET /rest/servicedeskapi/info` answers without
credentials.

Reporters, participants, approvers, and agents can subscribe to a request they
can view. Reporters and newly added participants start subscribed, approval
assignment also subscribes the approver, and each viewer can mute or resume
updates. Public comments, attachments, status changes, and approval decisions
create private inbox notifications for subscribed viewers other than the actor.
Internal notes notify subscribed agents only. Notification actions synchronize
through the existing per-user notification stream and open the portal request. Each
notification is also queued as an email through the delivery outbox, and newly
added participants are told they were added. Inviting a customer to a desk
emails them a link to its help center; creating a customer sends no email, as
in Jira.

After a request reaches Done, its reporter can submit, revise, read, or delete a
one-to-five customer satisfaction rating with an optional comment. Agents and
other request viewers can read the result but cannot change it. Subscribed
agents receive a notification when the reporter leaves feedback.

Reporters and agents can add active service customers as request participants
by account ID or email. Participants appear on the request, can read its public
conversation and status, and lose that access immediately when removed. The
reporter remains a distinct role and cannot be added or removed as a participant.

Request validation answers Jira's validation result: `fieldErrors` lists each
failing field with its message, and `errorMessage` and `reasonKey` are null for
a valid payload.

## Service goals and calendars

Every service desk starts with a Monday-to-Friday 09:00–17:00 UTC business
calendar, a four-business-hour first-response goal and an eight-business-hour
resolution goal. Service desk administrators can change the calendar name, IANA time
zone, working days, daily window and both goal durations from the agent
workspace. Each SLA counts time between Jira's start and stop conditions, which
managers choose per metric with the goal and pause condition: Issue Created,
Entered Status for each of the project's statuses, Assignee From Unassigned,
To Unassigned and Changed, Comment By Customer and For Customers, Due Date
Set, Cleared and Changed, and Resolution Set and Cleared. Every SLA needs at
least one start and one stop condition. A matching stop condition stops the
running cycle, and a matching start condition starts a new cycle when none is
running, so a reopened request starts another resolution cycle. New desks and
existing metrics keep Jira's defaults: time to first response starts at Issue
Created and stops at a comment for customers, and time to resolution starts at
Issue Created or Resolution Cleared and stops at Resolution Set. Condition
changes are audited and apply to the events that follow.

Managers also add their own SLAs, such as time to approve, with a name, a goal
and their start and stop conditions, and delete them again; time to first
response and time to resolution are built in and stay. A new SLA runs on the
desk's calendar, gets its default goal, follows the events after it is added,
and is searchable by its name with the SLA JQL functions, for example
`"Time to approve" = breached()`. Deleting it removes its goals and cycles.
Creating and deleting SLAs are audited.

Changing an SLA's conditions, or adding an SLA, recalculates that SLA for
every open request of the desk from the request's history, as Jira does: its
creation, its status, assignee, due date and resolution changes, and its
public comments are replayed in order against the conditions, and the SLA's
cycles are rebuilt with their historical start and stop times on its default
goal before goals and pauses are settled again. A comment counts as for
customers when its author manages the request now. Completed requests keep
their cycles, and pause conditions apply from the recalculation onwards
because they are evaluated against the request's current state.

Administrators add, rename, or remove dated holidays in the same calendar
workspace. Changes are scoped to the selected service desk and audited. SLA
calculation, queue urgency, customer-visible goal state, and the escalation
worker all read those persisted exclusions.

Agents and managers can open a desk report with 7, 30, or 90 day windows. It
combines daily request intake, current open/resolved load, breached request
counts, and CSAT averages from canonical service records. An accessible bar
chart and its exact table expose the daily series.

The request page shows on-track, paused, breached and completed goal state.
Elapsed and breach time skip non-working days and persisted holidays and honor
time-zone transitions. Managers can define a validated JQL pause condition for
each metric. Matching requests open a durable pause interval on creation or
transition, configuration changes immediately reconcile every active cycle,
and resuming retains the interval for historical calculations. The two Jira SLA REST operations are agent-only and
return Jira-compatible date, duration, completed-cycle and ongoing-cycle
shapes. A durable minute worker emits one approaching-goal and one breached
notification per clock and recipient, with transactionally synchronized
notification actions. The SLA attention queue shows requests inside the final
quarter of a goal and sorts breached requests first. Jira's seven SLA JQL
functions query the same calendar, cycle, goal snapshot, and pause state used by
these REST and worker journeys.

Request types show Jira's headset icon as an `SD_REQTYPE` universal avatar, say whether the caller can raise requests with them (`canCreateRequest`), and include their form with `expand=field`. Deleting a request type removes it from the requests that used it; those
requests remain, showing no request type, and the deletion is audited with the
number of requests it touched. Agents' queue listings return each request with
only the fields its queue is configured to show.

## Listing and reading requests

`GET /rest/servicedeskapi/request` lists a person's requests, most recently
active first. `requestOwnership` selects them, and several values combine:

- `OWNED_REQUESTS` — requests the person raised, or that were raised for them.
- `PARTICIPATED_REQUESTS` — requests they participate in.
- `ORGANIZATION` with `organizationId`, or `ALL_ORGANIZATIONS` — requests raised by
  members of an organization the person belongs to and the desk serves.
- `APPROVER` — requests the person approves. `approvalStatus`
  `MY_PENDING_APPROVAL` keeps approvals still waiting on them;
  `MY_HISTORY_APPROVAL` keeps those they decided or that are complete.
- `ALL_REQUESTS` — every request of the desks an agent serves, or every request
  for a site administrator.

Without `requestOwnership`, owned, participated and organization requests are
listed. `requestStatus` (`OPEN_REQUESTS`, `CLOSED_REQUESTS`, `ALL_REQUESTS`),
`searchTerm` (matched against summaries, with `*` and `?` wildcards),
`serviceDeskId` and `requestTypeId` narrow the list. An unknown value, an
`organizationId` without `ORGANIZATION`, an `approvalStatus` without `APPROVER`,
or a `requestTypeId` without its `serviceDeskId` answers 400. An unknown desk or
request type answers 404.

A request always carries its visible field values, reporter, current status
and created date. Status categories are Jira's keys: `NEW`, `INDETERMINATE` or
`DONE`. The other parts appear only when expanded, and `_expands` lists those
that were not:

- `serviceDesk` and `requestType`;
- `participant`, a page of participants;
- `sla`, for agents;
- `status`, the chronology from the status the request was created in;
- `attachment`;
- `action` — commenting and attaching for everyone who can see the request, and
  managing participants for its reporter and agents;
- `comment`, with `comment.attachment` and `comment.renderedBody`.

`GET /rest/servicedeskapi/request/{issueIdOrKey}/status` lists the same
chronology, most recent status first.

Request comments filter by `public` and `internal`, both true by default;
customers only ever see public comments. Comment attachments and rendered
bodies appear only when expanded. Request attachments are identified by their
links, as in Jira. Their content honours `Range` and conditional requests, and
their thumbnails are scaled images (Jira's default file thumbnail for other
files). A transition's additional comment is checked before the request moves,
so a comment refused for its length leaves the request where it was.

## REST coverage

The current `/rest/servicedeskapi` slice implements:

- product info, service desk and request type discovery and administration;
- durable Assets workspace discovery through both the current and deprecated
  Insight paths;
- request type groups, permission checks, and administrator-owned JSON entity
  properties;
- linked knowledge-base article search and rendered article viewing;
- customer creation, strict conflict handling and portal-only revocation;
- desk customer invitation, list, add and closed-portal removal;
- organization lifecycle, member and JSON property management, plus desk links;
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

All 75 operations in the pinned Jira Service Management Cloud REST contract
have now been reviewed and are represented by explicit partial assessments.

Request creation accepts string or Atlassian document format descriptions and
request-type-specific text, number, and date-time custom fields, then stores the
backing issue through the shared command layer. Existing and new desks seed
help, incident, problem, and change request types. Operations requests receive
one deterministic `incident`, `problem`, or `change` label, problem/change
descriptions are required, and agents can link visible related Jira work from
the service request without exposing those links to portal customers. Managers can define ordered JQL
conditions per SLA metric and move each one up or down; the first matching
condition wins, a reorder applies to cycles that start afterwards, and every cycle
snapshots the chosen goal name and duration so completed history remains stable.
Active cycles follow edits to their selected goal, and the default remains the
fallback. Managers also configure per-metric pause JQL in this workspace.
Recursive SLA-dependent pause conditions are rejected. Failed metadata association is
compensated by a logged issue deletion, so no orphaned ticket remains.

## Customer notifications

Service desk administrators choose which of Jira's customer notifications the
desk sends, from the Customers section of the agent workspace: Customer
invited, Request created, Public comment added, Customer-visible status
changed, Participant added and Approval required. Every notification starts
on. A turned-off notification stops reaching customers, both in their
notifications and by email, while agents keep their own updates. Request
created confirms to the reporter that the request arrived. Service projects
link customer organizations to the desk rather than sharing single requests,
so there is no Organization added notification to send.

## Remaining fidelity

The implemented operations are assessed as partial. JQL support follows the
documented ZZIRA search subset, including array-aware label matching;
Assets-backed portal pickers,
approval workflow configuration, customer notification email templates and Organization added notifications, CSAT configuration,
SLA goal distributions, complete public Assets object/schema/import API parity, historical pause replay for recalculated SLAs, Atlassian knowledge ranking/analytics, and asset import/reconciliation and review templates
remain. Customer creation grants only the
site `atlassian/customer` role and never silently grants Jira product access.
