# Confluence site settings

Updated: 2026-09-12

The site's look and feel, the themes it offers, and what it reports about
itself.

## Jira Cloud REST surface

All eight pinned operations are implemented. An audit against a running server
found none of them working.

| Method and path | Behavior |
|---|---|
| `GET /wiki/rest/api/settings/lookandfeel` | The settings for the site, or for one space with `spaceKey`. |
| `PUT /wiki/rest/api/settings/lookandfeel` | Chooses which settings a space shows. |
| `POST /wiki/rest/api/settings/lookandfeel/custom` | Writes the custom settings for the site or a space. |
| `DELETE /wiki/rest/api/settings/lookandfeel/custom` | Returns the custom settings to the defaults. |
| `GET /wiki/rest/api/settings/systemInfo` | What the site reports about itself. |
| `GET /wiki/rest/api/settings/theme` | The themes a site or space may select. |
| `GET /wiki/rest/api/settings/theme/selected` | The theme assigned to the whole site. |
| `GET /wiki/rest/api/settings/theme/{themeKey}` | One theme. |

## Writing the custom settings does not select them

They are two acts, and Confluence keeps them apart. Writing stores what a space
would show if it chose `custom`; choosing is `PUT /settings/lookandfeel`. Keeping
them separate is what lets an administrator prepare a look before switching to
it, and what lets a space switch back to a custom look it set up earlier.

Resetting likewise returns the custom values to the defaults **without** changing
which settings are selected, which is Confluence's own wording for it.

## The site's own look is the global one

So `PUT /settings/lookandfeel` requires a `spaceKey`: there is nothing to choose
for the site itself. A space may show the global settings, its own custom ones,
or its theme's — and choosing `theme` is refused when the space has no theme,
because there would be nothing to show.

## The default theme is not a choice

`GET /settings/theme` leaves it out: it is what a site shows when no theme is
chosen, rather than a theme to choose. It can still be read by key, because a
space may be showing it and a client needs its name.

A site with no theme assigned reports none rather than reporting the default,
which is the same distinction the space theme read makes.

## Evidence and current boundary

- `internal/confluence/site_settings_test.go` covers all eight operations, that
  the whole look and feel structure is answered, that writing the custom
  settings does not select them and resetting does not unselect, that the site
  cannot choose a look because its look is the global one, that a space with no
  theme cannot show a theme and can once it has one, that the default theme is
  absent from the list but readable by key, and that changing any of it is
  administration.
- `migrations/152_site_look_and_feel.sql` is exercised from a clean PostgreSQL
  schema.

The look and feel values are stored and returned as given rather than validated
field by field, so a colour this product does not render is kept and reported
faithfully. The `commitHash` the system information carries is empty, themes
come from the set this site installs rather than from apps, and assigning a
theme to the whole site has no pinned operation to set it.
