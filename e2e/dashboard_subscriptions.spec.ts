import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

// A manager has a shared dashboard emailed to a teammate every week, sees the
// schedule it set, and removes it.

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a dashboard is emailed on a schedule to people who can view it', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/dashboards');
  await page.getByLabel('Dashboard name', { exact: true }).fill(`Weekly ${Date.now()}`);
  await page.getByRole('group', { name: 'Who can view?', exact: true }).getByLabel('Everyone in this workspace', { exact: true }).check();
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);
  const dashboardURL = page.url().split('?')[0];
  await page.goto(dashboardURL);

  const panel = page.locator('.dashboard-subscribe');
  await panel.locator('summary').click();
  await panel.getByLabel('Delivery').selectOption('0 8 * * 1');
  const recipients = panel.getByRole('group', { name: 'Recipients' });
  await expect(recipients.getByRole('checkbox').first()).toBeVisible();
  const ana = recipients.getByLabel(/Ana/);
  if (await ana.count()) await ana.first().check();
  await accessible(page);
  await panel.getByRole('button', { name: 'Schedule email' }).click();
  await expect(page).toHaveURL(dashboardURL);

  await page.locator('.dashboard-subscribe summary').click();
  // Saved schedules are the forms that remove one; the form that adds one
  // offers the same wording among its choices.
  const saved = page.locator('.dashboard-subscribe-panel form').filter({ has: page.getByRole('button', { name: 'Remove email' }) });
  const scheduled = saved.filter({ hasText: 'Every Monday at 08:00 UTC' });
  await expect(scheduled).toHaveCount(1);
  await expect(scheduled).toContainText('Next');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  await scheduled.getByRole('button', { name: 'Remove email' }).click();
  await expect(page).toHaveURL(dashboardURL);
  await page.locator('.dashboard-subscribe summary').click();
  await expect(saved).toHaveCount(0);
});
