# Confluence notifications: mentions, watches and email

People hear about Confluence content in two ways: someone mentions them, or
they watch the content, its space or one of its labels. Both put an item in
the inbox at `/notifications` and send the same message by email.

## Mentions

A mention is Confluence's user link in storage format:

```xml
<ac:link><ri:user ri:account-id="…" /><ac:plain-text-link-body>Ana</ac:plain-text-link-body></ac:link>
```

- Pages, blog posts, footer comments and inline comments can mention people.
  The renderer shows a mention as `@Name` linking to the person's profile.
  A mention with no link body is labelled with the person's display name
  when the page is shown.
- A person is notified the first time a mention of them reaches readers:
  - when content is published with it, or
  - when a published version or a comment edit adds it.
  Keeping a mention in later versions notifies nobody again, and nor do
  drafts until they are published.
- Nobody is notified for mentioning themselves. Nor is anyone who cannot
  see the content: outside the site, excluded by page restrictions or space
  permissions, or unable to see a private post.
- In the editor, typing `@` (or pressing **Mention**) offers the site's
  people; choosing one writes the mention. Comment and blog post boxes,
  which take storage format, offer the same list and insert the markup.

## Watches

`/wiki/rest/api/user/watch/{content,space,label}/…` read and change watches.

- **Content** watches cover pages, blog posts, whiteboards, databases,
  folders, Smart Links and custom content.
  - Comments and attachments are watched through the page or blog post they
    are on; watching one directly is refused with 400.
- **Who may act for someone else** (`accountId`, `key` or `username`) follows
  Confluence's rule:
  - a site administrator, for any watch;
  - an administrator of the space that the watched space or content belongs
    to, for that space and its content;
  - for label watches, which span spaces, only site administrators.
- `/content/{id}/notification/child-created` lists a piece of content's own
  watchers, and `/content/{id}/notification/created` lists its space's.
  Each watch's `type` is the kind of content (`page`, `blogpost`,
  `whiteboard`, …) or `space`.
- **What watchers hear about:**
  - a page created under a watched page, and non-minor updates to pages;
  - publishing a blog post, and non-minor updates to one;
  - new footer and inline comments on a page, blog post or custom content.
- **One item per change:** a watcher who matches through several watches
  still gets one item. A watcher whom the same change mentions gets only the
  mention.
- **Visibility:** watchers who cannot see the changed content or comment
  are not told about it.
- Blog posts and pages have **Watch** buttons; spaces and labels keep theirs.

## Email

Every Confluence inbox item is also emailed to the person it is for.

1. The item is written with `email_state = 'pending'`.
2. `WikiNotificationEmailRunner` moves pending items into the email outbox:
   - one message per item, de-duplicated by notification id;
   - the subject reads, for example, `Ana updated blog post "Weekly"`;
   - the body links to the page or blog post.
3. The mail runner delivers the outbox when SMTP is configured.
4. People without an email address, or deactivated since, are skipped.

Notifications written before this change stay inbox-only.

Notifications leading to a blog post use `/wiki/blogposts/{id}`, which
redirects to the post in its space.
