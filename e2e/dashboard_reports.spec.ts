import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

// A manager puts reports on an operating dashboard: created vs. resolved work
// for a project, and the velocity and sprint burndown of a scrum board.

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('report gadgets draw project and board reports on a dashboard', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/dashboards');
  await page.getByLabel('Dashboard name', { exact: true }).fill(`Reports ${Date.now()}`);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);
  const dashboardURL = page.url().split('?')[0];

  await page.getByRole('button', { name: 'Add Created vs. resolved chart', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Configure Created vs. resolved chart' })).toBeVisible();
  await expect(page.locator('.dashboard-gadget')).toContainText('Configure this gadget to choose a project.');
  await page.getByLabel('Project', { exact: true }).selectOption('ZZ');
  await page.getByLabel('Time window').selectOption('7');
  await page.getByLabel('Running totals').check();
  await accessible(page);
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  await expect(page).toHaveURL(dashboardURL);
  const trend = page.locator('.dashboard-gadget').filter({ hasText: 'Created vs. resolved chart' });
  await expect(trend).toContainText('in ZZ, last 7 days, running totals');
  await trend.getByText('View daily counts').click();
  await expect(trend.getByRole('table', { name: 'Created and resolved by day in ZZ' }).locator('tbody tr')).toHaveCount(7);

  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Velocity chart', exact: true }).click();
  await page.getByLabel('Scrum board').selectOption({ label: 'ZZ board (ZZ)' });
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  const velocity = page.locator('.dashboard-gadget').filter({ hasText: 'Velocity chart' });
  await expect(velocity).toContainText(/completed per sprint|No completed sprints on ZZ board yet\./);

  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Sprint burndown', exact: true }).click();
  await page.getByLabel('Scrum board').selectOption({ label: 'ZZ board (ZZ)' });
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  const burndown = page.locator('.dashboard-gadget').filter({ hasText: 'Sprint burndown' });
  await expect(burndown).toContainText(/on ZZ board|No active sprint on ZZ board\./);

  // Refreshing redraws the reports in place.
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(trend).toContainText('running totals');
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
