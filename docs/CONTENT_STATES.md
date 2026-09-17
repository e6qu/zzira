# Content states

A content state is a status label on a page ("Rough draft", "Ready for review") that says where the page stands without changing its text. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md); status in [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Two kinds

- **Space content states** are the states a space suggests. Every space suggests the product defaults: Rough draft (`1`), In progress (`2`), Ready for review (`3`), Published (`4`). They are defined in code (`internal/store/wiki_content_states.go`).
- **Custom content states** are created by a writer as they set them and belong to that writer; other people do not see them. Setting a name the writer has used before (case-insensitive) updates that state instead of adding another.

## API

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/rest/api/content-states` | The caller's custom states, most recent first. |
| `GET /wiki/rest/api/content/{id}/state` | The state on one status of a page (`status`, default `current`). |
| `PUT /wiki/rest/api/content/{id}/state?status=` | Sets a state: `{"id"}` for an existing one, or `{"name", "color"}` for a custom one. |
| `DELETE /wiki/rest/api/content/{id}/state` | Removes the state. |
| `GET /wiki/rest/api/content/{id}/state/available` | Every space state, plus the writer's three most recent custom states. |
| `GET /wiki/rest/api/space/{spaceKey}/state` | The states the space suggests. |
| `GET /wiki/rest/api/space/{spaceKey}/state/settings` | Whether states, custom states and space states are allowed, and the suggested states when allowed. Space administrators only. |
| `GET /wiki/rest/api/space/{spaceKey}/state/content?state-id=` | Pages in a state. `start`, `limit` (1 to 100, default 25), `expand`; expanding `body.export_view` or `body.styled_view` caps `limit` at 25. |

Responses carry `contentState` and `lastUpdated`. `status` is `current`, `draft` or `archived`.

## Behavior

- Setting or removing a state publishes a new version with the same body, so history records when the state changed.
- `PUT` refuses (400):
  - both an id and a name/colour, or neither;
  - a name outside 1 to 20 characters, or a colour that is not a hex value;
  - a missing `status` (reads default to `current`);
  - a state the space's settings do not allow.

## Space settings

Space administrators choose whether pages carry content states at all, and whether writers may use space states and custom states. UI: the space page's content state settings section (`POST /wiki/spaces/{space}/content-state-settings`). Columns: `wiki_spaces.content_states_allowed`, `custom_content_states_allowed`, `space_content_states_allowed`.

## Tests

`internal/confluence/content_states_test.go`

## Gaps

See [PLAN.md](../PLAN.md).

- Configuring which states a space suggests (the set is fixed to the defaults).
- Content states on blog posts and custom content.
- Setting or showing a page's state in the page view and editor.
- `state/available` does not apply the space's settings.

## See also

[Content history](CONTENT_HISTORY.md), [drafts](CONTENT_DRAFTS.md), [space lifecycle](SPACE_LIFECYCLE.md).
