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

Site administrators link existing Confluence spaces to individual service
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
administrators are implicit service managers across every desk. A customer can read only their
own requests and public comments; comments created through ordinary Jira issue
UI have no public marker and remain internal.

The agent workspace at `/service/agent` provides ordered all-open, unassigned,
assigned-to-me, SLA-attention, and administrator-defined custom queues for each
service desk. Managers create, edit, and delete custom queues with validated JQL;
agents see matching service requests in the query's requested order. Built-in
queues cannot be edited or removed. Live counts and queue contents
update from the canonical request issue. Agents can open the full request,
review internal notes, assign a request to themselves, unassign it, comment,
and execute its workflow actions. Agent access is assigned per service desk and
is shared by the queue UI, request UI, and REST permission checks. Site
administrators can add or remove active workspace members from the desk roster.
Revocation immediately removes queue access, all-request visibility, request
management and internal-comment visibility for that desk.

Site administrators configure each request type's portal fields from the agent
workspace. Summary is always present and required; description and
project-available text, number, or date-time custom fields can be shown,
required, ordered, and given customer help text. The portal UI and JSM field
metadata read the same durable configuration. UI and REST request creation
reject unconfigured or missing fields, validate typed values through the
canonical Jira command layer, and store custom answers on the backing issue.

Agents can create customer organizations, add or remove active customers, store
JSON entity properties, and link organizations to the desks they work. A linked
organization admits its members to a closed portal. Customers see only their
own organizations and their properties; agents can filter and inspect the full
customer directory. The agent workspace includes customer invitation, portal
access, organization membership, and desk-link management.

Agents can request an approval from an active site user. Every approver has an
independent pending, approved, or declined decision. Any decline completes the
approval as declined; otherwise it completes only after every approver accepts.
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

Agents can declare an operations request with the incident profile to be a
major incident. The request then gains a durable status-update timeline with
public and internal audiences. Reporters, participants, and approvers see only
public updates, while assigned desk agents see both and can publish updates.
Subscribers receive notifications through the same audience boundary. Every
publication records its audience in the organization audit log. Declassifying
the incident preserves its timeline and closes it to new publications.

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
conditions per SLA metric; the first matching condition wins and every cycle
snapshots the chosen goal name and duration so completed history remains stable.
Active cycles follow edits to their selected goal, and the default remains the
fallback. Managers also configure per-metric pause JQL in this workspace.
Recursive SLA-dependent pause conditions are rejected. Failed metadata association is
compensated by a logged issue deletion, so no orphaned ticket remains.

## Remaining fidelity

The implemented operations are assessed as partial. JQL support follows the
documented ZZIRA search subset, including array-aware label matching;
conditional form logic, select, user, and
Assets-backed portal fields, participant notifications,
approval workflow configuration, image thumbnail generation,
email delivery and notification preference administration, CSAT configuration,
service report comparisons, SLA goal distributions, exports and scheduled
delivery, complete public Assets object/schema/import API parity, full
status chronology, SLA rule reordering and advanced criteria,
portal invitation email delivery, Atlassian knowledge ranking/analytics, and asset import/reconciliation and review templates
remain. Customer creation grants only the
site `atlassian/customer` role and never silently grants Jira product access.
