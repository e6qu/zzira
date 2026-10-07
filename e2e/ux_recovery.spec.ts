import { expect, test, Page } from '@playwright/test';
import { apiAuthHeader } from './auth';

const email = 'demo@zzira.dev';

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
}

async function openIssue(page: Page) {
  const response = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `Recovery ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  expect(response.status()).toBe(201);
  const { key } = await response.json();
  await page.goto(`/browse/${key}`);
  return key as string;
}

for (const width of [1440, 320]) {
  test(`board cards sit beneath their column headings at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await login(page);
    const key = await openIssue(page);
    await page.goto('/board/brd_default');
    const card = page.locator(`.board-card[data-key="${key}"]`);
    await expect(card).toHaveCount(1);
    const column = card.locator('xpath=ancestor::section[@data-status][1]');
    const status = await column.getAttribute('data-status');
    const heading = page.locator(`.board-column-head[data-status="${status}"]`);
    const columnBox = (await column.boundingBox())!;
    const headingBox = (await heading.boundingBox())!;
    expect(Math.abs(columnBox.x - headingBox.x)).toBeLessThanOrEqual(1);
    expect(Math.abs(columnBox.width - headingBox.width)).toBeLessThanOrEqual(1);
    expect(columnBox.y).toBeGreaterThanOrEqual(headingBox.y + headingBox.height);
  });
}

test('a rejected edit explains the error, keeps the draft, and can be corrected', async ({ page }) => {
  await login(page);
  const key = await openIssue(page);
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit issue' });
  await dialog.getByLabel('Summary', { exact: true }).fill('   ');
  await dialog.getByLabel('Description', { exact: true }).fill('Keep this draft after rejection.');
  const rejected = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/issues/${key}/edit`));
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  expect((await rejected).status()).toBe(400);
  await expect(dialog.getByRole('alert')).toBeVisible();
  await expect(dialog.getByRole('alert')).toContainText(/summary/i);
  await expect(dialog.getByLabel('Description', { exact: true })).toHaveValue('Keep this draft after rejection.');
  await dialog.getByLabel('Summary', { exact: true }).fill('Corrected recovery draft');
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('.issue-summary')).toHaveText('Corrected recovery draft');
});

test('a network failure keeps the edit open and provides a retry path', async ({ page }) => {
  await login(page);
  const key = await openIssue(page);
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit issue' });
  await dialog.getByLabel('Summary', { exact: true }).fill('Retry this edit');
  await page.route(`**/issues/${key}/edit`, route => route.abort('connectionfailed'));
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText(/connection|try again/i);
  await expect(dialog.getByLabel('Summary', { exact: true })).toHaveValue('Retry this edit');
  await page.unroute(`**/issues/${key}/edit`);
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.locator('.issue-summary')).toHaveText('Retry this edit');
});

test('page shortcuts cannot mutate work behind an open dialog', async ({ page }) => {
  await login(page);
  await openIssue(page);
  const watch = page.locator('#issue-root .watch-button[aria-keyshortcuts="W"]');
  const initial = await watch.textContent();
  let watchRequests = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && /\/watch$/.test(new URL(request.url()).pathname)) watchRequests++;
  });
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit issue' });
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).focus();
  await page.keyboard.press('w');
  await page.keyboard.press('Control+[');
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  expect(watchRequests).toBe(0);
  await expect(watch).toHaveText(initial!);
});

test('dialog focus wraps from the dialog container and skips hidden controls', async ({ page }) => {
  await login(page);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await dialog.focus();
  await page.keyboard.press('Shift+Tab');
  await expect(dialog.getByRole('button', { name: 'Create issue', exact: true })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(dialog.getByRole('button', { name: 'Close dialog' })).toBeFocused();
});

test('closing a loading editor prevents a late response reopening it', async ({ page }) => {
  await login(page);
  const key = await openIssue(page);
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  await page.route(`**/issues/${key}/edit`, async route => {
    const response = await route.fetch();
    await gate;
    await route.fulfill({ response });
  });
  const loading = page.waitForRequest(r => r.url().endsWith(`/issues/${key}/edit`));
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await loading;
  await page.keyboard.press('Escape');
  await expect(page.locator('#modal-root')).toHaveCSS('display', 'none');
  release();
  await page.unrouteAll({ behavior: 'wait' });
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeFocused();
});

test('blocked browser storage still allows sign-in, theme changes, and creation', async ({ page }) => {
  await page.addInitScript(() => {
    for (const method of ['getItem', 'setItem', 'removeItem']) {
      Object.defineProperty(Storage.prototype, method, {
        value() { throw new DOMException('Storage blocked', 'SecurityError'); },
      });
    }
  });
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await login(page);
  const toggle = page.locator('[data-theme-toggle]');
  const initial = await toggle.getAttribute('aria-pressed');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-pressed', initial === 'true' ? 'false' : 'true');
  await page.locator('#global-create-issue').click();
  await expect(page.getByRole('dialog', { name: 'Create issue' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(errors).toEqual([]);
});
