# Accessibility baseline

Updated: 2026-09-03

ZZIRA targets WCAG 2.2 Level AA for its browser UI and follows the WAI-ARIA
Authoring Practices for custom interaction patterns. This is an engineering
baseline and regression policy, not a third-party accessibility certification.

## Shipped behavior

- Every full page has a descriptive title, one primary heading, and a main
  landmark; workspace pages also provide a keyboard-visible skip link past the
  repeated application shell.
- Workspace navigation exposes its current page, can be operated from the header
  or with `Control+[`, and is removed from the focus order while off-canvas.
- Create and edit dialogs have an accessible name, inert background, initial
  focus, contained `Tab` navigation, `Escape` dismissal, and resilient focus
  return even when the live issue fragment refreshes. Long metadata-driven
  create forms keep their actions visible while the field region scrolls.
- Native disclosures back account and action menus; `Escape` closes an open menu
  and returns focus to its summary, while pointer interaction outside any open
  menu dismisses it.
- Board cards avoid nested interactive roles. A dedicated move button supports
  keyboard pickup/arrow/drop, while a native disclosure provides up, down,
  previous-status, and next-status actions without dragging.
- Backlog planning uses native disclosure sections and forms; every move and
  rank operation has a non-drag control, sprint lifecycle controls have
  programmatic labels, and issue previews update a named complementary region.
- Form controls have programmatic labels, authentication fields advertise their
  autocomplete purpose, validation failures use an alert, and changing board
  filter results are announced through a polite status region.
- Issue activity filters expose pressed state, newest/oldest sorting remains a
  real button, and the watch shortcut is suppressed while focus is in an editor.
- Focus indicators, 24 CSS-pixel control targets, 320 CSS-pixel reflow, dark
  theme contrast, reduced-motion behavior, increased contrast, and forced-colors
  affordances are part of the design system baseline.

## Regression coverage

`e2e/accessibility.spec.ts` runs axe against login and all primary authenticated
screens—including backlog, project, people/profile, and workflow pages—in light and dark
themes. It also directly exercises behavior that a
DOM scanner cannot prove:

- dialog inertness, focus containment, dismissal, and focus restoration;
- menu dismissal and create-form validation-error states, including expanded
  metadata fields;
- skip-link and active-navigation behavior;
- off-canvas navigation at a 320px viewport;
- board keyboard controls and non-drag movement controls;
- backlog menu dismissal, minimum target sizes, and narrow-screen reflow;
- issue activity filter state and the keyboard watch journey;
- minimum control target dimensions.

Run the automated gate against a seeded local server:

```sh
cd e2e
ZZIRA_URL=http://127.0.0.1:8081 npx playwright test accessibility.spec.ts
```

For each substantial UI change, manually verify the complete journey with only
a keyboard, at 200% text zoom, with a screen reader, and in the platform's
forced-colors/high-contrast mode. Automated checks cannot determine whether all
names are useful, reading order is intuitive, or announcements are appropriately
timed in every assistive-technology/browser combination.

## Standards references

- [Web Content Accessibility Guidelines (WCAG) 2.2](https://www.w3.org/TR/WCAG22/)
- [WAI-ARIA Authoring Practices: Dialog (Modal)](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/)
- [WAI-ARIA Authoring Practices: Disclosure](https://www.w3.org/WAI/ARIA/apg/patterns/disclosure/)
- [WAI-ARIA landmark regions](https://www.w3.org/WAI/ARIA/apg/practices/landmark-regions/)
