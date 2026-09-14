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
- **Editing together.** An editor is warned when someone else is editing the
  same content. The existing optimistic version check still refuses a save
  made against an older version, so the later save has to merge.
- **Updates.** The first report sets the version and comment count the open
  page started from.
  - A later, higher version tells a reader the content was updated, with a
    link to the latest version, and tells an editor that a newer version was
    published.
  - More comments tell a reader that new comments were added, with a link to
    show them.

The notices are a polite live region, so assistive technology announces them
without interrupting.
