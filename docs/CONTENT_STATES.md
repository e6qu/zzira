# Content states

Updated: 2026-09-11

A content state is the label a page carries beyond its text — "Rough draft",
"Ready for review", "Needs legal". It is how a writer says where the page is up
to without changing a word of it.

## Two kinds, kept apart

Confluence has two, and they answer different questions.

**Space content states** are what a space suggests: the shared vocabulary
everyone working there uses. **Custom content states** are made by a writer as
they work, and belong to that writer — another person's list is their own.

They are not merged into one set, because a client needs to know which is which:
the editor offers every state the space suggests, and only the writer's few most
recent custom ones.

**No pinned operation writes a space content state.** Configuring them is a
space administration screen Confluence does not expose in this REST surface, so
the suggested set here is the product's default rather than something an
administrator has chosen. That is why they are defined in code and not a table.
Custom states are created by the write that uses them — a pinned operation — so
they are rows.

## Jira Cloud REST surface

All eight pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /wiki/rest/api/content-states` | The custom states the caller has made. |
| `GET /wiki/rest/api/content/{id}/state` | The state on one status of a page. |
| `PUT /wiki/rest/api/content/{id}/state` | Sets a state by id, or describes a new custom one. |
| `DELETE /wiki/rest/api/content/{id}/state` | Removes the state. |
| `GET /wiki/rest/api/content/{id}/state/available` | What this page could be set to. |
| `GET /wiki/rest/api/space/{spaceKey}/state` | The states the space suggests. |
| `GET /wiki/rest/api/space/{spaceKey}/state/settings` | Whether states are allowed, and which. |
| `GET /wiki/rest/api/space/{spaceKey}/state/content` | The content in a state. |

## Setting a state publishes a version

Confluence's wording is that setting a state "publishes the content without
changing the body", and that is what happens: the page gets a new version
carrying the new state and the same text. Removing a state publishes a version
too, so the history shows when the state went as well as when it arrived.

The state therefore lives on the version, not only on the page. The test checks
both halves of that — the version count grows and the body does not change.

## What the write refuses

**An id and a description together.** Naming an existing state and describing a
new one are two different requests; supplying both is a contradiction rather
than a precedence question.

**Neither.** A state has to be identified somehow.

**A name over 20 characters, or a colour that is not a hex value.** Both are
Confluence's own limits, and a state that cannot be displayed is not a state.

**A missing status on the write.** Setting a state on a draft and on the
published page are different acts, so Confluence requires the status to be
named. The reads default to the published page.

Re-using a name the writer has used before updates that state rather than
accumulating a new one on every edit, which is what keeps a writer's list short
enough for the editor to offer.

## Evidence and current boundary

- `internal/confluence/content_states_test.go` covers all eight operations, the
  separation between the two kinds, that another writer does not see someone's
  custom states, that re-using a name keeps one state, every refusal above, that
  each change publishes a version while leaving the body alone, and that the
  space's content listing follows a page into and out of a state.
- `migrations/146_content_states.sql` is exercised from a clean PostgreSQL
  schema.

Configuring a space's suggested states, restricting which states a space allows,
the `expand` parameter on the space content listing, content states on blog
posts and on custom content, and states in the browser journeys remain.
