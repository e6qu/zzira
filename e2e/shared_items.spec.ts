import { apiAuthHeader } from './auth';
import { expect, test, Page, Browser } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('an administrator hands on and removes the items people share', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36).toUpperCase();
  const filterName = `Shared filter ${stamp}`;
  const dashboardName = `Shared dashboard ${stamp}`;

  const filter = await page.request.post('/rest/api/3/filter', {
    headers, data: { name: filterName, jql: 'project = ZZ', sharePermissions: [{ type: 'global' }] },
  });
  expect(filter.status()).toBe(200);
  const dashboard = await page.request.post('/rest/api/3/dashboard', {
    headers, data: { name: dashboardName, sharePermissions: [{ type: 'loggedin' }], editPermissions: [] },
  });
  expect(dashboard.status()).toBe(200);

  // Both appear on the page, with who owns them and who they reach.
  await page.goto('/settings/shared-items');
  const filterCard = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: filterName, exact: true }) });
  const dashboardCard = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: dashboardName, exact: true }) });
  await expect(filterCard).toContainText('Demo User');
  await expect(filterCard).toContainText('Everyone signed in');
  await expect(dashboardCard).toContainText('Everyone signed in');

  await page.addScriptTag({ content: axe.source });
  expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);

  // An administrator hands each on to somebody else.
  await filterCard.getByLabel('Hand to').selectOption({ label: 'Ana Soursop' });
  await filterCard.getByRole('button', { name: 'Change owner' }).click();
  await expect(page.getByRole('status')).toContainText('Filter owner changed.');
  await dashboardCard.getByLabel('Hand to').selectOption({ label: 'Ana Soursop' });
  await dashboardCard.getByRole('button', { name: 'Change owner' }).click();
  await expect(page.getByRole('status')).toContainText('Dashboard owner changed.');
  await expect(page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: filterName, exact: true }) })).toContainText('Ana Soursop');

  // And removes them, though they now belong to somebody else.
  const owned = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: filterName, exact: true }) });
  await owned.locator('details.metadata-delete > summary').click();
  await owned.getByRole('button', { name: 'Delete filter' }).click();
  await expect(page.getByRole('status')).toContainText('no longer see it');
  await expect(page.getByRole('heading', { name: filterName, exact: true })).toHaveCount(0);

  const ownedDashboard = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: dashboardName, exact: true }) });
  await ownedDashboard.locator('details.metadata-delete > summary').click();
  await ownedDashboard.getByRole('button', { name: 'Delete dashboard' }).click();
  await expect(page.getByRole('status')).toContainText('no longer see it');
  await expect(page.getByRole('heading', { name: dashboardName, exact: true })).toHaveCount(0);

  // What the API says, after the page said it.
  const search = await (await page.request.get(`/rest/api/3/filter/search?filterName=${encodeURIComponent(filterName)}`, { headers })).json();
  expect(search.values).toEqual([]);

  await assertMemberRefused(browser);
});

async function assertMemberRefused(browser: Browser) {
  const context = await browser.newContext();
  try {
    const member = await context.newPage();
    await login(member, 'ana@zzira.dev', 'ana12345');
    expect((await member.goto('/settings/shared-items'))?.status()).toBe(403);
  } finally {
    await context.close();
  }
}
