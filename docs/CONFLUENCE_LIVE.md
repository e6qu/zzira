# Confluence live presence and updates

An open page or blog post, in view or in the editor, reports itself to
`POST /wiki/spaces/{space}/pages/{page}/presence` (or
`…/blogposts/{blogpost}/presence`) every 15 seconds. It says whether the person
is editing. The answer is the content's live state:

```json
{"present":[{"accountId":"…","displayName":"Ana","editing":true}],"version":3,"commentCount":5,"lastCommentAt":"2026-09-14T12:00:00Z"}
```

- **Presence.** People who can see the content appear to one another: the
  page says "Also here: …", marking who is editing. Presence that stops
  being reported is gone within 45 seconds, and `leave=true` removes it at
  once. It is kept in an unlogged table because nothing in it needs to
  survive a restart.
- **Editing together.** An editor is told when someone else is editing the
  same content. On a published page or blog post their changes merge as they
  type (see below).
- **Updates.** The first report sets the version and comment count the open
  page started from.
  - A later, higher version tells a reader the content was updated, with a
    link to the latest version, and tells an editor that a newer version was
    published.
  - More comments tell a reader that new comments were added, with a link to
    show them.

The notices are a polite live region, so assistive technology announces them
without interrupting.

## Live editing

Everyone editing a published page or blog post shares one live document,
exchanged at `POST /wiki/spaces/{space}/pages/{page}/live` (or
`…/blogposts/{blogpost}/live`) with
`{"session":"…","revision":3,"changes":[{"position":12,"delete":0,"insert":"today"}]}`.

- **Changes.** A change replaces `delete` UTF-16 code units at `position` of
  the page's storage markup with `insert`, measured the way browsers measure
  strings. A change that reaches past the text or splits a character is 400.
- **Merging.** Changes are accepted only at the document's latest revision,
  and the answer says `"applied":true`. Otherwise nothing is applied and the
  answer carries the `changes` the editor missed, in revision order; the
  editor applies them to the text it last synced and rebases its own change on
  top, so an insertion at the same place goes after the one already there and
  text someone else inserted is never deleted. An editor from another session,
  or too far behind the 2,000 changes kept, gets the whole `body` instead.
- **Drafts.** Every merged text that is valid storage becomes the content's
  shared draft, so reopening the editor or publishing keeps it; text mid-way
  through markup is shared but not drafted. Deleting the content ends its
  live document.
- **Sessions.** Publishing a new version restarts the session from the content
  (a new `session` at revision 0), and discarding the draft closes it; editors
  still open load the new text and carry their unsent edits over. The editor
  keeps its version field on the session's `version`, so publishing after
  someone else published still works.
- **The editor.** Changes are sent a quarter of a second after typing stops
  and at least every second. The rich editor redraws merged markup in place
  and keeps the caret beside the same text; source mode keeps its selection.
  Input methods finish composing before anything is merged. A status line says
  live editing is on, or that it is reconnecting and nothing typed is lost.
- **Offline.** Typing not yet shared is kept on the device with the session,
  revision and text it was last synced from, after every exchange, and
  forgotten once everything is shared. An editor opened on that page, even
  offline from the site's page cache, starts from the kept text and merges it
  with what it missed when it connects, as it would unsent typing; kept text
  the page already contains is discarded. Each open editor keeps its own
  entry and refreshes it with every exchange, so another editor of the same
  page only takes typing left by one that closed or stopped for ten seconds.
- **Carets.** Each exchange also sends `"cursor":{"position":…,"end":…}`,
  the editor's caret or selection in the text it sent. The server keeps it
  only from an editor holding the latest revision, moves every kept caret
  through each change accepted after it (a caret inside deleted text lands
  after what replaced it), forgets them all when the session restarts, and
  answers with `cursors`: the account, name and place of everyone else who
  synced in the last 30 seconds. The editor draws each as a named caret over
  the rich editor or the source textarea, moved through its own unsent typing;
  the carets are hidden from assistive technology, which hears who is editing
  from the status line.

Only people who can edit the page or blog post may join; anyone else is 403
or 404.

