import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email = 'demo@zzira.dev', password = 'demo1234') {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
}
async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('create, configure, share, refresh, copy and delete a dashboard', async ({ page, browser }) => {
  await login(page);
  await page.locator('.nav-dashboards').click();
  const name = `Delivery ${Date.now()}`;
  await page.getByLabel('Dashboard name', { exact: true }).fill(name);
  await page.getByLabel('Description', { exact: true }).fill('Team delivery and priorities');
  await accessible(page);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);
  const dashboardURL = page.url().split('?')[0];
  const id = dashboardURL.split('/').pop()!;
  await page.getByRole('button', { name: 'Add Pie chart', exact: true }).click();
  await page.getByLabel('JQL query').fill('project = ZZ');
  await page.getByLabel('Group charts by').selectOption('status');
  await page.getByLabel('Maximum list results').fill('1');
  await accessible(page);
  await page.getByRole('button', { name: 'Save gadget query' }).click();
  await expect(page.locator('.gadget-counts')).toBeVisible();
  await expect(page.locator('.gadget-pie')).toBeVisible();
  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Filter results', exact: true }).click();
  await page.getByLabel('JQL query').fill('project = ZZ');
  await page.getByLabel('Maximum list results').fill('5');
  await page.getByRole('button', { name: 'Save gadget query' }).click();
  await expect(page.locator('.gadget-issues li').first()).toBeVisible();
  await page.getByRole('link', { name: 'Configure Filter results', exact: true }).click();
  await page.getByLabel('Gadget title', { exact: true }).fill('Delivery queue');
  await page.getByLabel('Column (from 0)').selectOption('1');
  await page.getByLabel('Accent colour').selectOption('purple');
  await page.getByRole('button', { name: 'Save appearance and position' }).click();
  await expect(page.locator('.dashboard-column').nth(1)).toContainText('Delivery queue');
  await page.getByRole('link', { name: 'Edit dashboard', exact: true }).click();
  await page.getByLabel('Layout', { exact: true }).selectOption('AB');
  await page.getByLabel('Automatic refresh', { exact: true }).selectOption('60000');
  await page.getByRole('button', { name: 'Save layout' }).click();
  await expect(page.locator('#dashboard-grid')).toHaveClass(/layout-AB/);
  const viewerContext = await browser.newContext();
  const viewer = await viewerContext.newPage();
  try {
    await login(viewer, 'ana@zzira.dev', 'ana12345');
    expect((await viewer.request.get(`/rest/api/3/dashboard/${id}`)).status()).toBe(404);
    await page.getByRole('link', { name: 'Edit dashboard', exact: true }).click();
    await page.getByRole('group', { name: 'Who can view?', exact: true }).getByLabel('Everyone in this workspace', { exact: true }).check();
    await page.getByRole('button', { name: 'Save dashboard', exact: true }).click();
    await viewer.goto(dashboardURL);
    await expect(viewer.getByRole('heading', { level: 1 })).toHaveText(name);
    await expect(viewer.getByRole('link', { name: 'Edit dashboard', exact: true })).toHaveCount(0);
    await expect(viewer.locator('.dashboard-gadget')).toHaveCount(2);
    await viewer.getByRole('button', { name: 'Add favourite', exact: true }).click();
    await expect(viewer.getByRole('button', { name: 'Remove favourite', exact: true })).toBeVisible();
    await accessible(viewer);
    await viewer.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(viewer.getByRole('status').filter({ hasText: 'Updated just now.' })).toBeVisible();
    await page.getByRole('link', { name: 'Edit dashboard', exact: true }).click();
    await page.getByRole('group', { name: 'Who can view?', exact: true }).getByLabel('Everyone in this workspace', { exact: true }).uncheck();
    await page.getByRole('button', { name: 'Save dashboard', exact: true }).click();
    await viewer.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(viewer.locator('#dashboard-refresh-status')).toContainText('no longer available');
    await expect(viewer.locator('.dashboard-gadget')).toHaveCount(0);
  } finally { await viewerContext.close(); }
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Copy dashboard' }).click();
  await page.getByLabel('Copy name', { exact: true }).fill(`${name} copy`);
  await page.getByRole('button', { name: 'Create copy', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(`${name} copy`);
  await expect(page.locator('.dashboard-gadget')).toHaveCount(2);
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Delete dashboard' }).click();
  await page.getByRole('button', { name: 'Delete dashboard permanently' }).click();
  await expect(page).toHaveURL('/dashboards');
  await page.goto(dashboardURL);
  // Online-only dashboards must never be retained in the service-worker page cache.
  const cached = await page.evaluate(async () => {
    const paths: string[] = [];
    for (const key of await caches.keys()) for (const request of await (await caches.open(key)).keys()) paths.push(new URL(request.url).pathname);
    return paths.filter(path => path === '/dashboards' || path.startsWith('/dashboards/'));
  });
  expect(cached).toEqual([]);
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Delete dashboard' }).click();
  await page.getByRole('button', { name: 'Delete dashboard permanently' }).click();
  expect((await page.request.get(`/rest/api/3/dashboard/${id}`)).status()).toBe(404);
});
