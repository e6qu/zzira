# Drafts and deletion for pages and blog posts

Updated: 2026-09-14

Confluence moves a page or blog post through drafts and three kinds of
deletion, and a blog post reads the way a page does.

## Drafts of published content

A published page or blog post may have one unpublished draft beside it,
shared by everyone who may edit the content. `PUT` with `status: "draft"` at
version 1 saves it, replacing any draft already there, and leaves the
published version exactly as readers see it. `get-draft=true` reads the draft;
it comes back in draft status at version 1. Publishing — a `PUT` in current
status, or Save page in the editor — replaces the draft.

The page editor offers Save as draft on a published page. The page shows that
an unpublished draft is waiting, with Edit draft, which opens the editor on the
draft, and Discard draft.

## Three deletions

- **Delete** sends current or archived content to the trash.
- **`draft=true`** discards a draft for good. On published content it throws
  away the waiting draft; on a page or blog post that was never published it
  removes the content, which never reaches the trash. A never-published draft
  that other content hangs off is refused rather than leaving that content
  without a parent.
- **`purge=true`** takes trashed content out of the trash. It needs space
  administration, as Confluence documents, and moves the content to the
  deleted state.

A plain delete on a draft is refused with a pointer to `draft=true`, a plain
delete on trashed content with a pointer to `purge=true`, and asking for both
at once is refused.

Deleted content is for the space's administrators: they see it in
`status=deleted` reads and lists, and `PUT` in current status restores it with
its content intact, as Confluence's update documents. Everyone else gets a
404.

## Blog posts read like pages

A blog post reads in every primary body format, at an earlier `version`
(reported as `historical`), and with every `include-*` flag a page has:
labels, properties, operations, likes, versions, collaborators, the reader's
star and web resources, and the version can be left out. Blog posts are
written flat or nested, as storage, the document format or wiki markup, like
pages. Lists take the statuses Confluence lists blog posts in — current,
trashed and deleted — and sorting compares ids as numbers, which the earlier
sort did not.

## Labels on any content

The v1 label add and remove act on pages, blog posts and attachments alike,
following what the content id names; a prefixed name such as `my:plan`
removes that prefix's label. The label lookup lists pages, blog posts and
attachments, or one kind with `type`; page templates carry no labels here, so
`type=page_template` finds nothing.

## Evidence

- `internal/confluence/content_drafts_test.go` covers drafts of published pages
  and blog posts and their version rule, publishing replacing a draft,
  discarding drafts and never-published content, the delete refusals, purge
  and its space-administration rule, deleted visibility in reads and lists,
  restoring deleted pages and blog posts, blog post formats, versions and
  every include flag, the blog status list, and v1 labels on blog posts with
  the typed label lookup.
- `e2e/wiki_drafts_purge.spec.ts` saves a draft of a published page in the
  browser, sees the published page unchanged, edits and discards the draft,
  and trashes and purges the page.
