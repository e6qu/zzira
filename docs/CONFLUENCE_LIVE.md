# Confluence presence and live editing

Covers who else has a page or blog post open, notices when it changes, and a
shared live document that merges edits as people type. Everything goes over
HTTP polling from the browser; the server never pushes. Part of
[Confluence](CONFLUENCE_SITE_SURFACES.md).

| Route | Purpose |
| --- | --- |
| `POST /wiki/spaces/{space}/pages/{page}/presence` | Presence and update notices |
| `POST /wiki/spaces/{space}/blogposts/{blogpost}/presence` | Presence and update notices |
| `POST /wiki/spaces/{space}/pages/{page}/live` | Live document sync |
| `POST /wiki/spaces/{space}/blogposts/{blogpost}/live` | Live document sync |

Code: `internal/web/wiki_presence.go`, `internal/web/wiki_live.go`,
`internal/store/wiki_presence.go`, `internal/store/wiki_live.go`,
`web/static/wiki.js`.

## Presence

An open page or blog post, in the viewer or the editor, reports every 15
seconds whether the person is editing. The response is:

```json
{"present":[{"accountId":"…","displayName":"Ana","editing":true}],"version":3,"commentCount":5,"lastCommentAt":"2026-09-14T12:00:00Z"}
```

- **Who is here.** People who can see the content see each other ("Also here:
  …", with editors marked).
- **Leaving.** An entry that stops reporting disappears after 45 seconds.
  `leave=true` removes it at once.
- **Storage.** Entries live in the unlogged table `wiki_presence`
  (migration 184), so they do not survive a restart.
- **Update notices.** The first report records the version and comment count
  the page was opened at.
  - A higher version later tells a reader the content was updated, with a
    link to the latest version. An editor is told that a newer version was
    published.
  - A higher comment count tells a reader that new comments were added, with
    a link to them.
  - Notices appear in a polite live region.

## Live editing

Everyone editing a current (published) page or blog post shares one live
document. The request body is:

```json
{"session":"…","revision":3,"changes":[{"position":12,"delete":0,"insert":"today"}],"cursor":{"position":17,"end":17}}
```

- **Changes.** Each change replaces `delete` UTF-16 code units at `position`
  of the storage-format body with `insert`. A change that runs past the end of
  the text, or splits a character, is 400. Only the body is shared; the title
  is not.
- **Merging.** Changes are accepted only at the latest revision, and the
  response then says `"applied":true`.
  - **Behind the latest revision.** Nothing is applied. The response lists the
    `changes` the editor missed, in order. The editor applies them and rebases
    its own changes on top. Two insertions at the same place keep the earlier
    one first, and nobody's inserted text is deleted.
  - **Wrong session or too far behind.** An editor on another session, or more
    than 2,000 changes behind (the most kept), gets the whole `body`
    instead.
- **Drafts.** Every merged text that is valid storage format becomes the
  shared draft. Text caught mid-markup is shared but not saved as the draft.
  Deleting the content ends its live document.
- **Sessions.**
  - **Publishing.** Starts a new `session` at revision 0.
  - **Discarding the draft.** Closes the session.
  - **Open editors.** They load the new text and carry over their unsent
    edits. The editor's version field follows the session's `version`, so it
    can still publish after someone else has published.
- **Timing.** The editor sends changes 250 ms after typing stops and syncs
  every second. During IME composition, nothing is sent until composition
  ends.
- **Editor view.** The rich editor redraws merged markup in place and keeps
  the caret next to the same text. Source mode keeps the selection. A status
  line shows whether live editing is on or reconnecting.
- **Offline.** Unsent typing is saved in `localStorage`, together with the
  session, revision and last synced text. The entry is removed once
  everything is shared.
  - **Reopening.** An editor opened later, even offline from the page cache,
    starts from the saved text and merges it when it reconnects. Saved text
    that the page already contains is discarded.
  - **Another editor's entry.** An editor takes over another editor's entry
    only if that editor closed or has not synced for 10 seconds.
- **Carets.** Each sync sends the editor's `cursor`.
  - **Kept carets.** The server keeps a caret only from an editor at the
    latest revision. It moves each kept caret through later changes; a caret
    inside deleted text moves to after the replacement. A new session clears
    all carets.
  - **Returned carets.** The response's `cursors` lists everyone else who
    synced in the last 30 seconds.
  - **Display.** They are drawn as named carets. Assistive technology hears
    who is editing from the status line instead.

## Permissions

Presence needs view permission. Live editing needs edit permission; anyone
else gets 403 or 404.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Transport.** No server push (WebSocket or SSE). Presence polls every 15
  seconds and live editing polls every second.
- **Title.** The title is not edited live.
- **Unpublished drafts.** Pages and blog posts that were never published have
  no live session.
- **Other content.** Whiteboards and databases have no presence, carets or
  merged editing. Their forms overwrite each other.
- **Comments and reactions.** Comments, inline comments and reactions do not
  appear live. A reader is only told that new comments exist.
- **Merge granularity.** Merging works on storage-format text, not on the
  document structure. Confluence merges `atlas_doc_format` steps.

## See also

[PAGE_WRITING.md](PAGE_WRITING.md) · [CONTENT_DRAFTS.md](CONTENT_DRAFTS.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
