# Jira Service Management

Service projects store each request as a regular Jira work item, so workflow,
automation, search, issue security, releases and reports all apply to
requests. Request metadata adds the portal, request type, customer, channel and
the public/internal split of the conversation. Incident requests count toward DORA
time to restore ([REPORTS.md](REPORTS.md)). Status: [CLOUD_PARITY.md](CLOUD_PARITY.md).

## UI

| Page | Who | Purpose |
|---|---|---|
| `/service` | signed-in users | Help center: the portals the user may use, plus site-admin customization |
| `/service/portals/{desk}` | admitted customers | Portal: request types under their groups, in the order the desk arranged, and knowledge search |
| `/service/portals/{desk}/request/{requestType}` | admitted customers | Request form |
| `/service/requests/{key}` | reporter, participants, approvers, agents | Request view: status, fields, conversation, files, approvals, transitions, subscription, feedback; operations panels for agents |
| `/service/knowledge/{page}` | admitted customers | Rendered knowledge article |
| `/service/agent[/{desk}]` | desk agents | Queues and desk administration: agents, customers, organizations, request types, request type groups, request type fields, portal, knowledge, calendars, SLAs, operations, deployment gating |
| `/service/agent/{desk}/reports` | desk agents | Service report ([REPORTS.md](REPORTS.md)) |
| `/service/agent/{desk}/assets` | desk agents | Assets inventory and dependency map |

The browser journey is tested in light and dark themes, with axe WCAG A/AA
checks and 320 px reflow.

## Roles and permissions

| Role | Can |
|---|---|
| Site administrator | Everything on every desk. Creates and reactivates portal-only customers, invites customers, revokes portal access, deletes organizations, edits Assets, sets the operations policy, customizes the help center |
| Service desk administrator (administers the project) | Request types and their portal groups, request type fields, queues, desk customers, knowledge base links, calendars, SLAs, portal settings, per-desk attachment/feedback/notification switches, deployment gating. Request type properties also need agent access |
| Agent | Reads every request of the desk (`requestOwnership=ALL_REQUESTS`), raises requests for enrolled customers, adds internal notes, assigns and transitions requests, runs bulk queue actions, creates organizations, manages participants and approvals, reads Assets |
| Customer | Their own requests (as reporter, participant, organization member or approver), public comments and public files |

- **Who is an agent:** a workspace member who is both on the desk's roster and
  holds the **Service desk agent** project permission
  (`SERVICEDESK_AGENT`) in the desk's project, from that project's permission
  scheme ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)). Losing either one
  ends agent access at once. A site administrator is an agent on every desk.
  The default permission scheme grants Service desk agent to the project's
  Members role, so a desk on that scheme needs only the roster entry.
- **Desk roster:** site administrators add and remove active members. Removing
  an agent immediately takes away queue access, visibility of all requests,
  request management and internal comments for that desk.
- **Portal-only customers:** they hold only the site `atlassian/customer` role
  and never get Jira product access. Revoked access stays revoked;
  auto-enrollment does not restore it.
- **Portals:** a portal is open to every active site customer, or closed to
  direct members and members of linked organizations.
- **What a customer may do to the work item behind their request** -- read it,
  comment on it, attach a file -- comes from the project's permission scheme,
  through the service portal customer holder the default scheme grants
  ([PERMISSION_SCHEMES.md](PERMISSION_SCHEMES.md)). A scheme that drops that
  holder leaves the portal showing a comment box the site then refuses.
- **Raising a request enrolls whoever raised it** as a customer of the site,
  in its directory and holding the site's customer role unless they already
  hold another, which is the same account an administrator creates by hand.
- **REST visibility:** service desk lists and lookups answer only a desk's
  administrators, agents and admitted users; everyone else gets 403.
- **Comments:** comments made in the ordinary Jira issue view have no public
  marker, so they stay internal.

## Request types and portal fields

- **Seeded types:** new and existing desks get help, incident, problem and change
  request types.
  - Incident, problem and change requests get one fixed label: `incident`,
    `problem` or `change`.
  - Problem and change requests need a description.
- **Administering request types (desk administrators, in the agent
  workspace):** add a request type on top of one of the project's work types,
  edit its name, description and help text, delete it, and choose the portal
  groups it appears in.
  - A name is 1 to 255 characters on one line and unique within the desk; the
    description and help text take at most 255 characters each.
  - Jira's REST create leaves the groups empty, so a request type raised over
    REST is not on the portal until an administrator puts it in a group.
  - Creating one for a desk or work type that is not there answers 404.
- **Portal groups:** the headings the portal lists request types under.
  - Add, rename and delete a group, and move it up or down; the portal and
    `requesttypegroup` follow that order.
  - A group that still holds request types is not deleted, because that would
    take them off the portal.
  - A request type can be in several groups, and its place inside each group
    moves up and down on its own.
  - All of these changes are audited.
- **Request type REST:** shows Jira's `SD_REQTYPE` headset avatar and
  `canCreateRequest`, and includes the form with `expand=field`.
  - Lists filter by `groupId`, repeated `serviceDeskId` and `restrictionStatus`.
    `OPEN` returns every type and `RESTRICTED` returns none.
  - `searchQuery` skips ungrouped types unless
    `includeHiddenRequestTypesInSearch=true`.
  - Deleting a request type clears it from its requests. The requests stay, and
    the audit entry records how many were affected.
- **Field configuration:** set per request type in the agent workspace.
  - Summary is always shown and required.
  - Description and the project's custom fields can be shown, required,
    ordered and given help text, or hidden with a preset value. A hidden
    required field needs a preset.
- **Portal inputs:**

  | Field type | Input |
  |---|---|
  | Select, cascading select | Options of the context that applies to the project (cascading children grouped under their parent) |
  | Multi-select | One checkbox per option |
  | Date, URL, number | Matching HTML inputs |
  | Labels | Space-separated |
  | User, multi-user picker | Site member email addresses (comma-separated); the site's people are not listed |
  | Group, multi-group picker | Site groups |
  | Project picker | Projects the requester can browse |
  | Version, multi-version picker | Unarchived versions of the desk project |
  | Team picker | Site Atlassian teams |
  | Assets object | The desk's objects labelled with their schema, optionally limited to one schema. The field context decides one object or several (checkboxes); ids are stored |

- **Validation:** the portal form, `validValues` and the request view list the
  same choices. An answer outside them is refused, as is an Assets object from
  another schema.
- **Conditional fields:** a field can be shown only when another visible,
  non-conditional select or multi-select field on the form has one of the
  chosen options. Portal and REST creation require a conditional field only
  while it is shown, and refuse an answer to a hidden one.
- **Field metadata:**
  - Each field's Jira schema: `type`, `custom`, `customId` and array `items`.
  - `validValues` for select fields, with cascading children.
  - `canRaiseOnBehalfOf` and `canAddRequestParticipants` are true only for
    agents.
  - Hidden fields and `presetValues` appear only with `expand=hiddenFields`, and
    only for desk administrators.
- **Creation:**
  - Descriptions are accepted as a string or as ADF.
  - Unconfigured, hidden or missing fields are refused.
  - Typed values are validated by the Jira command layer.
  - If saving request metadata fails, the new work item is deleted (and the
    deletion logged).
- **Validation endpoint:** returns `fieldErrors`. `errorMessage` and `reasonKey`
  are null when the request is valid.

## Requests

`GET /rest/servicedeskapi/request` lists the caller's requests, most recently
active first. Values of `requestOwnership` combine:

| Value | Requests |
|---|---|
| `OWNED_REQUESTS` | Raised by or for the caller |
| `PARTICIPATED_REQUESTS` | Caller is a participant |
| `ORGANIZATION` (+ `organizationId`), `ALL_ORGANIZATIONS` | Raised by members of the caller's organizations that the desk serves |
| `APPROVER` (+ `approvalStatus` `MY_PENDING_APPROVAL` or `MY_HISTORY_APPROVAL`) | Caller is an approver |
| `ALL_REQUESTS` | Every request on the caller's desks (site admins: every request) |

- **Default:** without `requestOwnership`, owned, participated and organization
  requests are listed.
- **Filters:** `requestStatus` (`OPEN_REQUESTS`, `CLOSED_REQUESTS`,
  `ALL_REQUESTS`) splits requests by resolution. `searchTerm` matches summaries
  and accepts `*` and `?`. `serviceDeskId` and `requestTypeId` also filter.
- **Errors (400):** an unknown value, `organizationId` without `ORGANIZATION`,
  `approvalStatus` without `APPROVER`, or `requestTypeId` without
  `serviceDeskId`.
- **Errors (404):** an unknown desk or request type.
- **Response:** always visible field values, reporter, current status (category
  `NEW`, `INDETERMINATE` or `DONE`) and created date.
  - Other parts are returned only with `expand`, and `_expands` lists the rest:
    `serviceDesk`, `requestType`, `participant`, `sla` (agents only),
    `status`, `attachment`, `action` and `comment`.
  - `status` is the history from the first status. `action` covers commenting
    and attaching for anyone who can see the request, and managing
    participants for the reporter and agents.
  - `comment` also takes `comment.attachment` and `comment.renderedBody`.
- **Status history:** `GET .../request/{issueIdOrKey}/status` returns it newest
  first.
- **Comments:** filter with `public` and `internal` (both true by default).
  Customers only ever see public comments.
- **Attachments:** identified by their links. Content supports `Range` and
  conditional requests. Thumbnails are scaled images, or Jira's default file
  icon for other files.
- **Transitions:** only transitions whose conditions hold are listed. An
  optional comment is validated before the request moves.
- **Participants:** added by the reporter or agents, by account ID or email,
  from active service customers. They see the public conversation and status,
  and lose access as soon as they are removed. The reporter cannot be added or
  removed as a participant.
- **Links:** agents link visible related work. Customers never see these links.

## Queues

- **Built-in queues:** all open, unassigned, assigned to me, and SLA attention.
  - They list unresolved requests and cannot be edited.
  - SLA attention shows requests in the last quarter of a goal, breached ones
    first.
- **Custom queues:** managers create, edit and delete queues with validated
  JQL. Results follow the query's order.
- **Counts:** live, and listings return only the fields the queue shows.
- **Request actions:** agents open a request, read internal notes, assign it to
  themselves or unassign it, comment, and run workflow transitions.
- **Bulk actions:** select up to 100 requests (select-all is available), then
  assign them to a desk agent or unassign them, move them to a status through
  each request's own workflow, or add the same internal note or customer reply.
  - Agent access is what admits these actions. They are not Jira's bulk change
    routes and do not use the Bulk change global permission
    ([BULK_ISSUES.md](BULK_ISSUES.md)).
  - Each request still goes through its own command, so Assign issues,
    Transition issues and Add comments apply per request. Requests that cannot
    change are listed with the reason; the rest are updated.
  - A status the request's workflow offers no transition into, an assignee who
    is not an agent of this desk, and a request from another desk are all
    reported and skipped.

## Customers and organizations

- **Organizations:** agents create them, add and remove active customers, store
  JSON entity properties, and link them to desks.
  - A linked organization admits its members to a closed portal.
  - Only site administrators delete organizations; the UI shows the button only
    to them.
  - Organizations carry Jira's generated `uuid` and `created`.
- **Customer visibility:** customers see only their own organizations and
  properties. Agents can filter and inspect the whole customer directory.
- **Customers in REST:** returned as Jira's UserDTO (self, `jiraRest` and avatar
  links, no platform-only fields).
- **Customer creation:** strict conflict handling. Creating a customer sends no
  email. Inviting one to a desk emails a help-center link.

## Knowledge base

- **Setup:** desk administrators link Confluence spaces to a desk.
- **Portal search:** published pages whose title or storage body matches appear
  as suggestions.
- **Access:** customers read the rendered article without Confluence access.
  Closed-portal admission still applies.
- **REST search:** global and per-desk search use the same visibility rules,
  Jira's optional highlight markers, source and content links, and opaque
  cursors.

## Approvals

- **Manual approvals:** agents request approval from any active site user.
  - Each approver's decision is independent: pending, approved or declined.
  - Any decline declines the approval; otherwise it completes when everyone
    approves.
- **Status approvals:** a workflow status can carry Jira's approval
  configuration, set in the workflow editor's status approvals panel or
  through workflow REST. Entering the status:
  - Opens an approval named after the status.
  - Takes approvers from the configured user picker, or from group picker
    groups (their active members). The pre-populated field is used while the
    approvers field is empty.
  - Leaves out the assignee or reporter if configured to.
  - Needs a set number or percentage of approvals, or, under
    `numberPerPrincipal`, that many from each group (at most the group's size).
    Any decline declines it.
  - Moves the request through the configured approved or declined transition
    when it completes.
- **Answering:** only a pending approver can answer. Approvers can open the
  request even if they are not the reporter or a participant.
- **JQL:** `approved()`, `pending()`, `approver()`, `myApproval()`,
  `myPendingApproval()`, `pendingApprovalBy()`, `myPending()` and
  `pendingBy()`.
  - Users can be given as account ID, username, email or display name.
  - `!=` excludes requests that have no approval field.
  - See [JQL.md](JQL.md).

## Notifications and subscriptions

- **Subscriptions:** reporters, participants, approvers and agents can subscribe
  to requests they can view, and mute or resume.
  - Reporters and newly added participants start subscribed, and so do
    approvers when assigned.
- **What notifies:** public comments, attachments, status changes and approval
  decisions notify subscribed viewers other than the person who acted.
  Internal notes notify only subscribed agents.
- **Delivery:** notifications arrive in the per-user inbox (synchronized, and
  they open the portal request) and are queued as email through the outbox.
- **Participants:** newly added participants are told they were added.
- **Desk switches:** desk administrators can turn off any of Jira's customer
  notifications: Customer invited, Request created, Public comment added,
  Customer-visible status changed, Participant added, Approval required.
  - All start on.
  - A disabled notification stops both the inbox entry and the email to
    customers; agents still get theirs.
  - Organizations are linked to desks rather than to single requests, so
    there is no Organization added notification.

## Attachments and feedback

- **Storage:** the portal and REST use Jira's attachment records and blob store.
- **Uploads:** an upload first gets a single-use temporary ID, then becomes a
  public or internal comment attachment in one finalize call.
  - Temporary uploads come from desk agents and admitted customers.
  - They follow the site's attachment switch and size limit, and the desk's own
    switch. Turning the desk switch off also removes the file field.
  - Unclaimed uploads expire after 24 hours; an hourly worker deletes them.
- **Visibility:** customers see public files only, and agents see both. The same
  rule applies on Jira's attachment routes.
- **Satisfaction feedback:** once a request is Done, its reporter can submit,
  revise, read or delete a 1–5 rating with an optional comment.
  - Other viewers can read it.
  - Subscribed agents are notified.
  - Desk administrators can turn feedback off; the portal then stops asking and
    REST refuses new ratings.
  - Connect apps can leave or delete feedback on the reporter's behalf.

## SLAs and calendars

- **Defaults:** each desk starts with a Monday–Friday 09:00–17:00 UTC calendar,
  a 4-hour time to first response and an 8-hour time to resolution (business
  hours).
- **Calendars:** administrators edit the name, IANA time zone, working days,
  daily hours and dated holidays.
  - A desk can have several calendars.
  - The default calendar, and any calendar a goal uses, cannot be removed.
- **Conditions:** set per SLA.
  - Start and stop: Issue Created; Entered Status (any project status);
    Assignee From Unassigned, To Unassigned and Changed; Comment By Customer and
    For Customers; Due Date Set, Cleared and Changed; Resolution Set and
    Cleared.
  - Each SLA needs at least one start and one stop condition.
  - A stop ends the running cycle. A start opens a new cycle only when none is
    running, so a reopened request starts a new resolution cycle.
  - Defaults: time to first response runs from Issue Created to Comment For
    Customers; time to resolution runs from Issue Created or Resolution Cleared
    to Resolution Set.
- **Custom SLAs:** managers add their own (for example time to approve) and can
  delete them. The two built-in SLAs cannot be deleted.
  - A custom SLA uses the default calendar and goal, and applies to events from
    then on.
  - It can be searched by name, for example `"Time to approve" = breached()`.
  - Deleting it removes its goals and cycles.
- **Recalculation:** changing an SLA's conditions, or adding an SLA, replays
  every open request's history against it: creation, status, assignee, due date
  and resolution changes, and public comments.
  - Cycles are rebuilt with their original times.
  - A comment counts as for customers if its author currently manages the
    request.
  - Completed requests keep their cycles.
  - Pause conditions that test only status are replayed; other pause
    conditions apply from the recalculation onward.
- **Goals:** ordered JQL goals per SLA. The first match wins and the default
  goal is the fallback.
  - Each goal can name its own calendar.
  - A cycle records the goal name, duration and calendar it started with, so
    later edits do not rewrite history.
  - Reordering goals affects cycles that start afterwards. Active cycles follow
    edits to their own goal.
- **Pauses:** set with validated JQL per SLA. SLA-dependent pause conditions are
  refused.
  - A matching request opens a pause interval when it is created or
    transitioned.
  - Configuration changes are applied to active cycles at once.
- **Calculation:** skips non-working time and holidays, and handles time-zone
  transitions.
  - The request view shows on track, paused, breached or completed.
  - A minute worker sends one approaching-goal and one breached notification
    per clock and recipient.
- **REST:** the two SLA operations are for agents only and return Jira's date,
  duration, completed-cycle and ongoing-cycle shapes.
- **JQL:** Jira's seven SLA functions use the same state.
- **Audit:** all configuration changes are audited.

## Operations: incidents, problems, changes

- **Operations profile:** incident, problem and change requests get one.
  - Agents set impact × likelihood on a 4×4 matrix (Low, Medium, High or
    Critical risk), an on-call owner, the change type (standard, normal or
    emergency), planned windows and a rollback plan.
- **Change calendar:** covers the past 7 days and the next 90. It flags
  overlapping windows, leaves out completed work, and shows conflicts on the
  change itself.
- **Dependency map:** built from links between desk incidents, problems and
  changes. It shows direction, and a link appears only if the agent can read
  both work items.
- **Operations policy (site administrators, per desk):**
  - CAB risk threshold and approver roster. A change at or above the threshold
    opens one Change advisory board approval.
  - Incident review due period. Reviews start pending with a due date, and
    agents advance them and record findings.
  - Time-bounded on-call shifts. The active shift's owner is assigned new
    operations requests.
  - Ordered major-incident escalation steps, each a responder plus a delay. A
    minute scheduler notifies each due responder once per declaration.
    Re-declaring starts a new round.
- **Major incidents:** agents declare an incident a major incident.
  - It gains a timeline with public, internal and stakeholder updates.
    Reporters, participants and approvers see only public updates, and desk
    agents see all of them and publish.
  - Agents assign Incident commander, Communications lead and Technical lead to
    desk agents; each new holder is notified.
  - Stakeholders are site members or outside email addresses. Stakeholder
    updates are emailed once and stay off the customer timeline.
  - Declassifying keeps the timeline but closes it to new updates.
  - Publications, role changes and stakeholder changes go to the organization
    audit log.
- **Deployment gating (desk administrators):** choose a provider (an installed
  app or any provider) and the environment types to gate.
  - Each deployment to a gated environment opens one change request, raised by
    the project lead for the submitter.
  - Gating status is awaiting while approvals are pending, prevented after a
    decline, allowed once approved (or completed with no approvals), and
    invalid if the request could not be opened. Ungated deployments are
    allowed.
  - See [JIRA_SOFTWARE.md](JIRA_SOFTWARE.md).

## Assets

- **Workspace:** each site has one Assets workspace, and each desk has its own
  inventory in it. `GET /rest/servicedeskapi/assets/workspace` (also at the
  deprecated `/insight/workspace`) names it, and the Assets API below is served
  beneath it.
- **Schemas:** a key, a name and 1–30 attributes (text, number, date, boolean,
  or select with 1–50 options).
  - A schema is also the object type; there is no separate type hierarchy.
- **Objects:** a key, a label, values checked against the schema, and canvas
  coordinates.
- **Relationships:** named and directed between two objects. The source depends
  on the target.
- **Deletion:** deleting a schema or object also deletes its relationships and
  request links.
- **Transactions:** every change writes the state and a synced action in one
  transaction.
- **History:** an object's card carries what has happened to it -- added,
  changed (with the fields that changed), deleted -- read from those actions
  rather than kept a second time. The REST resource reports the same.
- **Access:** agents can view. Only site administrators create, edit, move or
  delete. Customers cannot read inventory.
- **Workspace page:** shows the dependency map (SVG) and an accessible
  relationship table.
- **Requests:** an agent marks an object as affected or as a dependency.
  - Impact analysis walks reverse dependencies up to 8 levels. It is safe with
    cycles, stays within the desk, and reports the shortest depth.
  - Direct links can be edited; inferred impact is derived.
- **Object fields:** see [Request types and portal fields](#request-types-and-portal-fields).
- **Import (site administrators):** the Assets page loads a comma separated
  file of objects for one schema. Its first row names the columns: `Key` and
  `Label`, optionally `X` and `Y`, and any attribute of the schema, named as
  the schema names it or by the key it is stored under.
  - A row whose key already belongs to an object in that schema updates it and
    keeps its place on the canvas; every other row creates an object, laid out
    in rows after the ones already there.
  - The file is read and checked whole, and then written in one transaction, so
    a refused row leaves the inventory exactly as it was. At most 1000 objects
    and 4 MB at a time.
  - **This file is the whole schema** reconciles instead of adding: an object
    of that schema the file leaves out is deleted, with its relationships and
    the request links that named it, in the same transaction.

### Assets API

Served at `/jsm/assets/workspace/{workspaceId}/v1`, where the workspace is the
one `GET /rest/servicedeskapi/assets/workspace` names. A schema is also its
object type, so `objectschema` and `objecttype` answer for the same ids. An
agent of the desk reads; only site administrators write. An id in a desk the
caller does not agent answers 404, the same as an id that was never there.

| Operation | What it does |
| --- | --- |
| `GET /objectschema/list` | Every schema the caller agents, with its object count |
| `GET /objectschema/{id}` | One schema |
| `GET /objectschema/{id}/objecttypes/flat` | The schema as its one object type, with its attributes |
| `GET /objecttype/{id}/attributes` | The schema's attributes, typed, with select options |
| `POST /object/navlist/aql` | `{"qlQuery","objectTypeId","startAt","maxResults"}`; answers `objectEntries` and a total |
| `POST /object/create` | `{"objectTypeId","objectKey","label","attributes","position"}` |
| `GET /object/{id}` | One object, its attributes and its place on the canvas |
| `PUT /object/{id}` | Changes only what the body names |
| `DELETE /object/{id}` | Deletes the object, its relationships and its request links |
| `GET /object/{id}/referenceinfo` | The relationships it is either end of, inbound and outbound |
| `GET /object/{id}/connectedTickets` | The requests that name it |
| `GET /object/{id}/history` | What has happened to the object, oldest first: who wrote it, when, and which fields that write changed |
| `POST /objectschema/create` | `{"name","objectSchemaKey","description","serviceDeskId","attributes"}`; an attribute with no type is text |
| `DELETE /objectschema/{id}` | Deletes the schema, its objects and everything that named them |
| `POST /objectschema/{id}/import` | The import above, as `{"file"}` or the body itself. `{"reconcile":true}` (or `?reconcile=true`) makes the file the whole schema: an object it leaves out is deleted |

An attribute is written and read under the key its schema gave it, and a value
is checked against the attribute's type exactly as the Assets page checks it.

## Portal and help center settings

- **Portal (desk administrators):**
  - Name: one line, up to 255 characters.
  - Introduction: up to 1,000 characters.
  - Logo: a site path or an http(s) URL.
  - These appear on the portal and in the help center list.
  - The portal lists request types under their group headings, in the order the
    desk's administrators arranged; a request type in no group is not offered.
    The portal search narrows the request types inside their groups.
  - The **Agents can add announcements to this portal** setting lets agents
    post a portal announcement: a title of up to 255 characters and a message
    of up to 2,000. A message needs a title, and clearing both removes the
    announcement.
- **Help center (site administrators, from *Customize help center*):**
  - Name, home page title, logo and banner image.
  - Banner, link and button colour; banner text colour; navigation background
    and text colours.
  - A home page announcement.
  - Colours are hex. A colour pair applies only if it keeps 4.5:1 contrast, and
    the banner/link/button colour styles controls only if white text stays
    readable on it.
- **Audit:** all settings changes are audited.

## REST coverage

All 75 pinned `/rest/servicedeskapi` operations are implemented and assessed
as partial. They cover:
- `GET /rest/servicedeskapi/info` (no credentials needed).
- Desk and request type discovery and administration, request type groups,
  permission checks and properties.
- Knowledge search and Assets workspace discovery.
- Customers, desk customers, organizations, members, properties and desk links.
- Request validation, creation, listing and detail.
- Comments, status, transitions, participants, queues (with optional counts)
  and SLAs.
- Approvals, temporary uploads and attachments, subscriptions, and feedback.

## Assets filters

An Assets object field on a request type's form is scoped to one schema, and
narrowed further by an AQL filter written where the schema is chosen:

```
objectType = "Business services" AND Tier IN ("1", "2")
"Owner" = Platform AND Runbook IS NOT EMPTY
NOT (objectType = Vendors)
```

- `objectType` (also `type`, `schema`) is the object's schema, `Name` (also
  `Label`) its label and `Key` its key; anything else is one of its attributes,
  named as the schema names it or by the key it is stored under.
- The comparisons are `=`, `!=`, `IN`, `NOT IN`, `LIKE` (contains),
  `IS EMPTY` and `IS NOT EMPTY`, joined with `AND`, `OR`, `NOT` and brackets.
  Values are quoted with `"` or `'`, doubling the quote to include one.
- A filter is read when it is saved, so a form never carries one the site
  cannot read, and a field whose filter fails offers nothing rather than
  everything.
- What AQL has that this does not: references between objects, functions such
  as `objectTypeAndChildren()`, dot paths through reference attributes, and
  `ORDER BY`.

## Gaps

See [PLAN.md](../PLAN.md).
- Assets: attachments and comments on an object.
- Assets object type hierarchy, typed reference attributes and AQL in JQL
  (`aqlFunction()`).
- Request type restrictions (`RESTRICTED` returns nothing).
- Email channel: requests created from incoming mail. Channels are `portal`
  (the default), whatever a REST caller passes in `channel`, and `api` for
  deployment-gating changes.
- Customizable customer notification email templates.
- Knowledge base ranking and article analytics.
- Incident review templates.
- Sharing a request with one organization when it is raised. `Organizations`
  is searchable ([JQL.md](JQL.md)) and reads the organizations the customer
  belongs to that the desk serves, which is what the portal shows their
  colleagues; picking one request at a time is what remains. Everything a
  queue, an SLA goal or an automation rule may write is the site's own JQL.

## See also

- [JIRA_PLATFORM.md](JIRA_PLATFORM.md)
- [AUTOMATION.md](AUTOMATION.md)
- [REPORTS.md](REPORTS.md)
- [WORKFLOW_RULES.md](WORKFLOW_RULES.md)
- [ATTACHMENTS.md](ATTACHMENTS.md)
- [CONFLUENCE_SITE_SURFACES.md](CONFLUENCE_SITE_SURFACES.md)
