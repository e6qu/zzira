# Comments

Updated: 2026-09-14

Footer and inline comments on pages, blog posts, attachments and custom
content, written and read the way page bodies are.

## Body forms and formats

A comment is written flat or nested, as storage, the document format or wiki
markup, and is kept as storage. A single comment reads back in any primary
format — storage, the document format, `view`, `export_view`,
`anonymous_export_view`, `styled_view` or `editor` — and comment collections
and comment versions in storage or the document format, which are the formats
Confluence offers for them.

## What a comment read includes

A single footer or inline comment read takes `include-properties`,
`include-operations`, `include-likes`, `include-versions` and
`include-version=false`. The footer comment read used to refuse all of them.

## Statuses

Comment collections accept every status Confluence names for them. A comment
here is current until it is deleted, so a filter naming only other statuses
finds nothing rather than being refused.

## Inline comments follow their passage

An inline comment is anchored to a passage and to which of that passage's
occurrences it was left on. When the page or blog post changes — an edit, a
restored version or a redaction — each comment is anchored again:

- a passage that still appears keeps its comment, and the comment's match
  count and index follow the new body;
- a passage that no longer appears leaves the comment `dangling`, which is how
  Confluence marks it, and `resolution-status=dangling` finds it;
- a dangling comment whose passage comes back is anchored again and reopens.

Replicas receive every comment whose anchor changed.

## Comments on custom content

Custom content takes footer comments as pages, blog posts and attachments do:
`customContentId` on create, replies inheriting it, and the custom content's
comment list returning them. A comment on custom content is visible to whoever
may see the custom content — its space, its privacy and the page it sits
beneath — and replicas receive it with the custom content's privacy.

## Stars are favourite relations

Starring a page or blog post creates Confluence's `favourite` relation from the
user to the content, which is what the relations API reads and what CQL's
`favourite` field searches. Stars used to live in a table of their own, which
neither saw; existing stars were carried into relations.

## Evidence

`internal/confluence/comment_lifecycle_test.go` covers a comment written in
wiki markup and read as storage, the document format and view, the list
formats, every include flag and version omission, the status filter, an inline
comment surviving the removal of one occurrence, dangling when its passage is
removed and reattaching when it returns, comments and replies on custom
content, a comment naming two targets, and a star read back through the
relations API and CQL.
