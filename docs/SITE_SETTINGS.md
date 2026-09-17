# Confluence site settings

Confluence's look and feel for the site and for each space, the themes a site offers, and the system information it reports. Part of the [Confluence site surfaces](CONFLUENCE_SITE_SURFACES.md); Jira's site look and feel is in [JIRA_SITE_CONFIGURATION.md](JIRA_SITE_CONFIGURATION.md). For status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## API

All eight operations of the pinned Confluence v1 settings group are served (`internal/confluence/site_settings.go`).

| Method and path | Behavior |
| --- | --- |
| `GET /wiki/rest/api/settings/lookandfeel` | The site's settings, or a space's with `spaceKey`. |
| `PUT /wiki/rest/api/settings/lookandfeel` | Chooses which settings a space shows: `global`, `custom` or `theme`. `spaceKey` is required. |
| `POST /wiki/rest/api/settings/lookandfeel/custom` | Writes the custom settings for the site or a space. |
| `DELETE /wiki/rest/api/settings/lookandfeel/custom` | Resets the custom settings to the defaults. |
| `GET /wiki/rest/api/settings/systemInfo` | What the site reports about itself. `commitHash` is empty. |
| `GET /wiki/rest/api/settings/theme` | Themes a site or space may select. The default theme is not listed. |
| `GET /wiki/rest/api/settings/theme/selected` | The theme assigned to the whole site, or none. |
| `GET /wiki/rest/api/settings/theme/{themeKey}` | One theme, including the default. |

Changing the site's settings needs site administration; changing a space's needs administration of that space. Space themes are set through `/wiki/rest/api/space/{spaceKey}/theme` ([SPACE_LIFECYCLE.md](SPACE_LIFECYCLE.md)).

## Behavior

- **Writing custom settings does not select them.** `POST .../custom` stores what a space would show if it chose `custom`; `PUT .../lookandfeel` makes the choice. Resetting likewise leaves the selection unchanged.
- **The site's look is the global one,** so there is nothing to choose for the site and `PUT` requires `spaceKey`.
- **Choosing `theme` is refused** when the space has no theme.
- **The default theme is not a choice.** It is left out of the theme list but readable by key. A site with no theme assigned reports none rather than the default.
- **Values are stored as given,** not validated field by field.
- **Wiki pages apply the look.** A page uses the space's custom settings when the space selects them, otherwise the site's, otherwise ZZIRA's own look. The header takes the background and primary navigation colour; headings, links, borders and dividers take theirs in the light theme only, so the dark theme stays readable. Only plain colours (hex, `rgb()`/`rgba()`, colour names) are written into the page, so stored values cannot inject other CSS (`internal/web/wiki_look_and_feel.go`).

Storage: `migrations/152_site_look_and_feel.sql` (`wiki_look_and_feel`, `wiki_site_settings`).

## Gaps

Tracked in [PLAN.md](../PLAN.md).

- No way to assign a theme to the whole site: `wiki_site_settings.global_theme_key` is read but nothing writes it.
- No administration UI for the site or space look and feel; it is API-only.
- Themes come from ZZIRA's built-in set; apps cannot provide themes.

## Tests

- `internal/confluence/site_settings_test.go` (`TestSiteSettings`): all eight operations and the rules above.
- `internal/web/wiki_look_and_feel_test.go`: CSS generated from the settings.
- `e2e/wiki_space_tools.spec.ts`: a custom heading colour on a wiki page and its removal after reset.
