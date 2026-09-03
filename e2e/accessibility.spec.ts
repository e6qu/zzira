import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

// Accessibility specs run before the journey files on a fresh CI database.
// Own the minimum issue/board fixture instead of relying on another spec's
// side effects or a developer database that happens to contain issues.
test.beforeAll(async ({ request }) => {
  const created = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      fields: {
        project: { key: 'ZZ' },
        summary: `Accessibility fixture ${Date.now()}`,
        issuetype: { name: 'Task' },
      },
    },
  });
  expect(created.status()).toBe(201);
});

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

async function expectNoWCAGViolations(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => {
    const result = await (window as any).axe.run(document, {
      runOnly: {
        type: 'tag',
        values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'],
      },
    });
    return result.violations.map((violation: any) => ({
      id: violation.id,
      impact: violation.impact,
      help: violation.help,
      nodes: violation.nodes.map((node: any) => ({
        target: node.target,
        failureSummary: node.failureSummary,
      })),
    }));
  });
  expect(violations).toEqual([]);
}

async function firstIssueHref(page: Page) {
  await page.goto('/issues/ZZ');
  const href = await page.locator('.issue-list .key-cell a').first().getAttribute('href');
  expect(href).toMatch(/^\/browse\/ZZ-/);
  return href!;
}

test('WCAG A/AA: every primary page passes axe in light and dark themes', async ({ page }) => {
  await page.goto('/signed-out');
  await expect(page).toHaveTitle('Signed out · ZZIRA');
  await expect(page.locator('h1')).toHaveCount(1);
  await expectNoWCAGViolations(page);

  await page.goto('/login');
  await expect(page).toHaveTitle('Log in · ZZIRA');
  await expect(page.locator('h1')).toHaveCount(1);
  await expectNoWCAGViolations(page);

  await login(page);
  const issueHref = await firstIssueHref(page);
  const pages = [
    '/', '/dashboard', '/projects', '/projects/ZZ', '/people', '/profile',
    '/settings/workflows', '/settings/workflows/wf_default',
    '/issues/ZZ', '/board/brd_default/backlog', '/board/brd_default', issueHref,
  ];

  for (const path of pages) {
    await page.goto(path);
    await expect(page.locator('h1')).toHaveCount(1);
    await expect(page).not.toHaveTitle('ZZIRA');
    await expectNoWCAGViolations(page);

    const toggle = page.locator('[data-theme-toggle]');
    if (await toggle.getAttribute('aria-pressed') === 'false') await toggle.click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expectNoWCAGViolations(page);
  }
});

test('ARIA dialog pattern: inert background, contained focus, Escape, and focus return', async ({ page }) => {
  await login(page);
  const trigger = page.locator('.header-create');
  await trigger.click();

  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await expect(page.locator('.app-shell')).toHaveJSProperty('inert', true);
  await expect(page.locator('.global-header')).toHaveJSProperty('inert', true);
  await expectNoWCAGViolations(page);

  for (let index = 0; index < 12; index += 1) {
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() =>
      document.querySelector('[role="dialog"]')?.contains(document.activeElement),
    )).toBe(true);
  }

  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await expect(page.locator('.app-shell')).toHaveJSProperty('inert', false);
});

test('dynamic menus, validation errors, and edit dialog remain accessible', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', 'not-the-password');
  await page.click('button[type=submit]');
  await expect(page.getByRole('alert')).toBeVisible();
  await expectNoWCAGViolations(page);

  await login(page);
  await page.locator('.user-menu summary').click();
  await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible();
  await expectNoWCAGViolations(page);
  await page.keyboard.press('Escape');
  await expect(page.locator('.user-menu')).not.toHaveAttribute('open', '');
  await expect(page.locator('.user-menu summary')).toBeFocused();

  await page.locator('.user-menu summary').click();
  await page.locator('#main-content').click({ position: { x: 5, y: 5 } });
  await expect(page.locator('.user-menu')).not.toHaveAttribute('open', '');

  await page.locator('#global-create-issue').click();
  await page.fill('#create-summary', 'Accessible validation state');
  await page.locator('.create-more summary').click();
  await page.fill('#create-labels', 'two words');
  await page.getByRole('dialog', { name: 'Create issue' }).getByRole('button', { name: 'Create issue' }).click();
  await expect(page.getByRole('alert')).toContainText('labels must be');
  await expectNoWCAGViolations(page);
  await page.keyboard.press('Escape');

  const issueHref = await firstIssueHref(page);
  await page.goto(issueHref);
  await page.locator('.more-menu summary').click();
  await expect(page.getByRole('button', { name: 'Delete issue' })).toBeVisible();
  await page.locator('.issue-summary').click();
  await expect(page.locator('.more-menu')).not.toHaveAttribute('open', '');

  const edit = page.getByRole('button', { name: 'Edit', exact: true });
  await edit.click();
  await expect(page.getByRole('dialog', { name: 'Edit issue' })).toBeVisible();
  await expect(page.locator('#edit-summary')).toBeFocused();
  await expect(page.locator('.app-shell')).toHaveJSProperty('inert', true);
  const box = await page.getByRole('dialog', { name: 'Edit issue' }).boundingBox();
  expect(box).not.toBeNull();
  expect(box!.y).toBeGreaterThanOrEqual(0);
  expect(box!.y + box!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
  await expectNoWCAGViolations(page);
  await page.keyboard.press('Escape');
  await expect(edit).toBeFocused();

  await page.goto('/board/brd_default/backlog');
  const backlogMenu = page.locator('.backlog-item-menu').first();
  await backlogMenu.locator('summary').click();
  await expect(backlogMenu).toHaveAttribute('open', '');
  await page.getByRole('heading', { name: 'Backlog', level: 1 }).click();
  await expect(backlogMenu).not.toHaveAttribute('open', '');
});

test('keyboard navigation: skip link, active location, and responsive sidebar state', async ({ page }) => {
  await login(page);
  await page.goto('/dashboard');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('#main-content')).toBeFocused();
  await expect(page.getByRole('link', { name: 'Your work' })).toHaveAttribute('aria-current', 'page');

  await page.setViewportSize({ width: 320, height: 720 });
  const sidebar = page.locator('#workspace-navigation');
  const toggle = page.locator('[data-nav-toggle]');
  await expect(sidebar).toHaveJSProperty('inert', true);
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(sidebar).toHaveJSProperty('inert', false);
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await page.keyboard.press('Escape');
  await expect(sidebar).toHaveJSProperty('inert', true);
  await expect(toggle).toBeFocused();

  const viewportDoesNotOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
  );
  expect(viewportDoesNotOverflow).toBe(true);
  await expectNoWCAGViolations(page);
});

test('board cards expose keyboard and non-drag movement controls without nested controls', async ({ page }) => {
  await login(page);
  await page.goto('/board/brd_default');

  const card = page.locator('.board-card').first();
  const moveButton = card.locator('[data-card-drag]');
  await expect(card).toHaveAttribute('aria-labelledby', /board-card-title-/);
  await expect(moveButton).toHaveAttribute('aria-describedby', 'board-keyboard-help');
  await moveButton.focus();
  await page.keyboard.press('Space');
  await expect(moveButton).toHaveAttribute('aria-pressed', 'true');
  await expect(card).toHaveClass(/is-grabbed/);
  await page.keyboard.press('Space');
  await expect(moveButton).toHaveAttribute('aria-pressed', 'false');

  await card.locator('.board-card-move summary').click();
  await expect(card.getByRole('button', { name: 'Move to next status' })).toBeVisible();
  const nestedInteractive = await card.evaluate((node) =>
    Array.from(node.querySelectorAll('a,button,summary')).filter((control) =>
      control.querySelector('a,button,summary'),
    ).length,
  );
  expect(nestedInteractive).toBe(0);
  await expectNoWCAGViolations(page);
});

test('controls meet WCAG 2.2 minimum target size', async ({ page }) => {
  await login(page);
  const issueHref = await firstIssueHref(page);
  for (const path of [
    '/', '/projects', '/projects/ZZ', '/people', '/profile',
    '/settings/workflows', '/settings/workflows/wf_default',
    '/issues/ZZ', '/board/brd_default/backlog', '/board/brd_default', issueHref,
  ]) {
    await page.goto(path);
    const columnPicker = page.locator('.column-picker summary');
    if (await columnPicker.count()) await columnPicker.click();
    const undersized = await page.locator('button, input, select, textarea, summary, .workspace-nav a').evaluateAll((nodes) =>
      nodes.flatMap((node) => {
        const rect = node.getBoundingClientRect();
        const style = getComputedStyle(node);
        if (!node.getClientRects().length || style.display === 'none' || style.visibility === 'hidden' || (node as HTMLElement).inert) return [];
        return rect.width < 24 || rect.height < 24
          ? [{ element: node.outerHTML.slice(0, 160), width: rect.width, height: rect.height }]
          : [];
      }),
    );
    expect(undersized).toEqual([]);
  }
});

test('primary pages reflow without document-level horizontal scrolling at 320px', async ({ page }) => {
  await login(page);
  const issueHref = await firstIssueHref(page);
  await page.setViewportSize({ width: 320, height: 720 });
  for (const path of [
    '/', '/projects', '/projects/ZZ', '/people', '/profile',
    '/settings/workflows', '/settings/workflows/wf_default',
    '/issues/ZZ', '/board/brd_default/backlog', '/board/brd_default', issueHref,
  ]) {
    await page.goto(path);
    const viewportDoesNotOverflow = await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    );
    expect(viewportDoesNotOverflow, `${path} should reflow at 320px`).toBe(true);
  }
});
