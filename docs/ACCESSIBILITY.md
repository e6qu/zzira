# Accessibility

ZZIRA's browser UI targets WCAG 2.2 Level AA. Custom widgets follow the WAI-ARIA Authoring Practices. This is an engineering baseline enforced by tests, not a third-party certification. See [UI_PARITY.md](UI_PARITY.md) for the per-journey quality gate.

## Behavior

**Pages**
- Every page has a descriptive title, exactly one `h1`, and a main landmark.
- Workspace pages have a skip link (`.skip-link`) that becomes visible on keyboard focus and jumps past the application shell.

**Navigation**
- Workspace navigation marks the current page.
- It opens from the header or with `Control+[`.
- When it is off-canvas, it is taken out of the focus order.

**Dialogs**
- Create and edit dialogs have an accessible name.
- While a dialog is open, the page behind it is inert.
- Focus starts inside the dialog, and `Tab` stays inside it.
- `Escape` closes the dialog.
- When the dialog closes, focus returns to where it was, even if the work item fragment refreshed meanwhile.
- In long create forms, the action buttons stay visible while the fields scroll.

**Menus**
- Account and action menus are native disclosures (`<details>`).
- `Escape` closes a menu and returns focus to its summary.
- Clicking outside an open menu closes it.

**Boards and backlog**
- Board cards contain no nested interactive roles.
- Each card has a move button: pick the card up, move it with the arrow keys, and drop it.
- A disclosure menu offers up, down, previous-status and next-status moves without dragging.
- Every backlog move and rank action has a control that does not need dragging.
- Sprint controls have programmatic labels.
- Work item previews update a named complementary region.

**Forms and status**
- Every form control has a programmatic label.
- Sign-in fields declare their `autocomplete` purpose.
- Validation failures are shown in an alert.
- Changes to board filter results are announced in a polite status region.

**Work item page**
- Activity filters expose their pressed state.
- Newest/oldest sorting is a real button.
- The `w` (watch) shortcut does nothing while focus is in an editor.

**Design system** (`web/static/css`)
- Visible focus indicators.
- Controls at least 24 × 24 CSS pixels.
- Layout reflows at 320 CSS pixels wide.
- Dark theme contrast.
- `prefers-reduced-motion`, `prefers-contrast` and `forced-colors` support.

## Tests

`e2e/accessibility.spec.ts`:

**Automated scan**
- Runs axe with the WCAG 2.0, 2.1 and 2.2 A/AA rules, in light and dark themes, on:
  - the sign-in and signed-out pages;
  - the main authenticated pages: home, dashboard, projects, board, backlog, board settings, issue navigator, a work item, people, teams, workflows, schemes, statuses, fields, project settings and wiki.

**Behavior checks**
- Dialogs: inert background, focus containment, `Escape`, focus return.
- Menus: closing, and validation errors in the create and edit dialogs.
- Skip link, active navigation, and the off-canvas sidebar.
- Board move controls, both keyboard and non-drag.
- Minimum target size, and 320 px reflow without horizontal scrolling on the main pages.

`e2e/triage.spec.ts` covers the activity filter pressed state, sort order, and the keyboard watch shortcut.

To run the accessibility tests against a running, seeded server:

```sh
cd e2e
ZZIRA_URL=http://127.0.0.1:8080 npx playwright test accessibility.spec.ts
```

**Manual checks.** Automated checks cannot judge whether names are useful, whether reading order makes sense, or whether announcements are well timed. For each substantial UI change, check the whole journey:
- with only a keyboard;
- at 200% text zoom;
- with a screen reader;
- in the platform's forced-colors or high-contrast mode.

## References

- [WCAG 2.2](https://www.w3.org/TR/WCAG22/)
- [WAI-ARIA APG: Dialog (Modal)](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/)
- [WAI-ARIA APG: Disclosure](https://www.w3.org/WAI/ARIA/apg/patterns/disclosure/)
- [WAI-ARIA APG: Landmark regions](https://www.w3.org/WAI/ARIA/apg/practices/landmark-regions/)
