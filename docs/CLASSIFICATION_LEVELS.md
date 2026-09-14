# Data classification levels

Data classification levels belong to the organization. Its administrators
define them, and Jira projects and Confluence content on every site of the
organization use them.

## Levels
A level has:
- a name, unique within the organization;
- a description and a handling guideline;
- one of the colors `RED`, `RED_BOLD`, `ORANGE`, `YELLOW`, `GREEN`, `BLUE`,
  `NAVY`, `TEAL`, `PURPLE`, `GREY` or `LIME`;
- a status and a rank.

Every organization starts with Public, Internal, Confidential and Restricted.

A level's status moves one way through its life:

| Status | What it means |
| --- | --- |
| `DRAFT` | New levels start here. A draft can be edited but not chosen. |
| `PUBLISHED` | The level can be given to projects, spaces and content. |
| `ARCHIVED` | The level can no longer be chosen or edited. Anything that already carries it keeps it and still reports it. An archived level can be published again. |

A level that has been published never returns to draft.

## Administration
Site administrators manage levels in **Administration → Data classification
levels**. There they create a draft, edit it, publish, archive or restore it,
and move levels up or down the order.

## Where levels are used
- **Confluence.** `GET /wiki/api/v2/classification-levels` lists the
  published and archived levels in order.
  - Page, blog post, whiteboard and database classification writes, and a
    space's default, accept only published levels.
  - Content with no level of its own reads its space's default. Resetting a
    page, blog post, whiteboard or database clears its own level, so it
    inherits that default.
  - Every classification picker in the wiki lists the published levels, and
    content that keeps an archived level shows it as archived.
- **Jira.** `GET /rest/api/3/classification-levels` lists the organization's
  levels, filtered by status and ordered by rank.
  - The project classification configuration permits the published levels.
  - A project default must be a published level.
