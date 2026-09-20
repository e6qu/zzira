# Confluence notifications: mentions, watches and email

People are notified about Confluence content when someone mentions them, or
when they watch the content, its space or one of its labels. Each
notification creates an item in the inbox at `/notifications` and sends the
same message by email. Part of [Confluence](CONFLUENCE_SITE_SURFACES.md).

## Mentions

A mention is Confluence's user link in storage format:

```xml
<ac:link><ri:user ri:account-id="…" /><ac:plain-text-link-body>Ana</ac:plain-text-link-body></ac:link>
```

- **Where.** Pages, blog posts, footer comments and inline comments. It
  renders as `@Name` linking to the profile. A mention with no link body
  shows the person's display name.
- **When.** A person is notified the first time a mention of them reaches
  readers: when content is published with it, or when a published version or
  comment edit adds it.
  - A mention that stays in later versions does not notify again.
  - Drafts notify nobody until they are published.
- **Who is not notified.**
  - The person who wrote the mention.
  - Anyone who cannot see the content: people outside the site, or excluded
    by page restrictions, space permissions or a private blog post.
- **Editor.** Typing `@` or pressing **Mention** lists the site's people. The
  comment and blog post boxes insert the storage markup.

## Watches

`/wiki/rest/api/user/watch/{content,space,label}/…` reads and changes watches.
`GET /wiki/rest/api/space/{spaceKey}/watch` lists a space's watchers.

- **Watchable content.** Pages, blog posts, whiteboards, databases, folders,
  Smart Links and custom content.
  - Comments and attachments are watched through the page or blog post they
    belong to. Watching one directly is 400.
- **Acting for someone else** (`accountId`, `key` or `username`):
  - **Any watch.** Site administrators.
  - **Spaces and their content.** Administrators of that space.
  - **Label watches.** Site administrators only, because labels span spaces.
- **Watcher lists.**
  - `GET /wiki/rest/api/content/{id}/notification/child-created` lists the
    content's own watchers.
  - `…/notification/created` lists its space's watchers.
  - Each watch's `type` is the content type (`page`, `blogpost`,
    `whiteboard`, …) or `space`.
- **What notifies watchers.**
  - **Pages.** A page created under a watched page, and non-minor page
    updates.
  - **Blog posts.** Publishing, and non-minor updates.
  - **Comments.** New footer and inline comments on a page, blog post or
    custom content.
- **One item per change.** Someone who matches several watches gets one item.
  Someone who is also mentioned in the change gets only the mention.
- **Visibility.** Watchers who cannot see the changed content or comment are
  not notified, and one who loses access afterwards stops being told: a
  notification names its content, so the inbox hides the items it already
  holds about content that is no longer readable, and the sync stream stops
  carrying them, including on a replica built from scratch.
- **Autowatch.** Writing a page or blog post, or commenting on one, watches
  it. A draft is watched when it is published, since there is nothing to
  watch before that, and a comment on an attachment or on custom content
  watches nothing, as there is no page to watch. It is a personal setting on
  your profile, separate from Jira's, and Confluence keeps it on by default.
- **UI.** Pages, blog posts, spaces and labels have **Watch** buttons, and
  the profile page carries the autowatch setting.

## Email

1. Each Confluence inbox item is written with `email_state = 'pending'`
   (migration 178).
2. `WikiNotificationEmailRunner` (`internal/store/wiki_notification_email.go`,
   started in `cmd/server/main.go`) moves pending items to the email outbox.
   It writes one message per item, deduplicated by notification id. Subjects
   look like `Ana updated blog post "Weekly"`, and the body links to the
   content.
3. The mail runner delivers the outbox when SMTP is configured.
4. People with no email address, or who have been deactivated, are skipped
   (`email_state = 'skipped'`).

Links to blog posts use `/wiki/blogposts/{id}`, which redirects to the post in
its space.

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- **Withdrawing what was already sent.** A replica that already holds a
  notification about content the reader has since lost access to keeps it
  until that replica is rebuilt: the sync stream stops carrying the item,
  but nothing retracts it the way a work item's tombstone does.
- **Email preferences.** No per-user Confluence email settings, no daily or
  weekly digest, and no "notify me about my own actions" option.
- **Likes and shares.** Likes do not notify the author. There is no
  **Share** action that notifies people.
- **Other events.** Watchers are not notified when content is moved, deleted,
  archived or has a new attachment.

## See also

[CONFLUENCE_COMMENTS.md](CONFLUENCE_COMMENTS.md) ·
[CONFLUENCE_TASKS.md](CONFLUENCE_TASKS.md) ·
[NOTIFICATION_SCHEMES.md](NOTIFICATION_SCHEMES.md) ·
[CLOUD_PARITY.md](CLOUD_PARITY.md)
