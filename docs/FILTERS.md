# Saved filters and sharing

Updated: 2026-09-09

ZZIRA exposes the 19 saved-filter and filter-sharing operations in the pinned
Jira Cloud Platform REST v3 contract. The browser issue navigator and REST API
use the same workspace-scoped filter records.

## Delivered API behavior

- create, read, update, delete, owned-filter, favorite-filter, and paginated
  filter-search resources;
- exact `PUT` and `DELETE` favorite methods, while retaining the older
  ZZIRA `POST` alias for existing callers;
- private, authenticated/global, direct-user, directory-group, project, and
  project-role view shares;
- separate edit shares that grant filter updates without transferring
  ownership;
- stable share-permission identifiers with list, detail, create, and delete
  operations;
- per-filter issue-navigator columns, reset semantics, and Jira-shaped
  `ColumnItem` responses;
- owner transfer by the owner or a Jira administrator, with active workspace
  membership and case-insensitive name-conflict checks;
- per-user default sharing scope, including Jira's normalization of `GLOBAL`
  to `AUTHENTICATED`; and
- per-user favorites, favorite counts, visibility-filtered collections,
  case-insensitive exact or substring name search, owner/share filters, ID
  filters, ordering, and offset pagination;
- owner-managed daily or weekly UTC email schedules with active-member
  recipient validation and live FilterBean subscription expansion; and
- durable scheduled runs with stale-claim recovery, bounded
  permission-filtered JQL evaluation, per-recipient outbox deduplication,
  delivery retries, result counts, errors, and direct work-item links.

Private filters are visible only to their owner. A user must be able to view a
filter before favoriting it or reading its columns and permissions. Group
shares follow current directory membership. Project shares follow the
workspace's project visibility model; project-role shares additionally evaluate
the built-in Administrator and Member roles or an exact project role binding.

Filter creation, updates, deletion, share changes, and owner transfer write
organization audit events without storing the filter's JQL in the audit detail.
Filter names are unique per owner within a workspace.

## Browser journey

The **Saved filters** workspace destination presents visible filters with their
JQL, owner, favorite count, and separate view/edit access lanes. Owners can
edit details and JQL, choose issue-navigator columns, add or remove workspace,
person, directory-group, project, and project-role access, transfer ownership,
and delete a filter. Site administrators can inspect private filters and
recover ownership when an owner leaves. Each user can also choose whether new
filters start private or visible to everyone signed in. Filter owners can
schedule daily or Monday email results, choose active workspace recipients,
inspect the next and last run, and remove the schedule.

`e2e/filters.spec.ts` proves the connected REST-to-browser journey: create a
private filter through Jira REST, find and favorite it in the browser, update
its details and columns, create and remove an email schedule, share it,
transfer it as an administrator, and delete it after transferring it back.

## Current limits

The contract operations remain partial until the broader PR 1 JQL and search
work completes. The schedule editor intentionally exposes daily and weekly UTC
choices; arbitrary cron expressions, user-time-zone schedules, HTML email, and
administrative subscription controls remain. Anonymous global-filter access
remains outside the authenticated Jira REST handler.

The JQL parser currently supports the useful subset recorded in
[CLOUD_PARITY.md](CLOUD_PARITY.md). Saved filters accept only queries that this
parser can validate; later PR 1 checkpoints expand that grammar and evaluation.
