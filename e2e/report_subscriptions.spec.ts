import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

// A team lead has a report's data emailed every week with the window they
// chose, sees the schedule on that report, and removes it.

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a report is emailed on a schedule with the choices it was scheduled from', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/projects/ZZ/reports/created-vs-resolved?days=7');
  await expect(page.getByRole('heading', { name: 'Created vs. resolved', level: 1 })).toBeVisible();
  const reportURL = page.url();

  const panel = page.locator('.report-subscribe');
  await panel.locator('summary').click();
  await panel.getByLabel('Delivery').selectOption('0 8 * * 1');
  const recipients = panel.getByRole('group', { name: 'Recipients' });
  await expect(recipients.getByRole('checkbox', { checked: true })).toHaveCount(1);
  await accessible(page);
  await panel.getByRole('button', { name: 'Schedule email' }).click();
  await expect(page).toHaveURL(reportURL);

  // Saved schedules are the forms that remove one; the form that adds one
  // offers the same wording among its choices.
  await page.locator('.report-subscribe summary').click();
  const saved = page.locator('.report-subscribe .dashboard-subscribe-panel form').filter({ has: page.getByRole('button', { name: 'Remove email' }) });
  const scheduled = saved.filter({ hasText: 'Every Monday at 08:00 UTC' });
  await expect(scheduled).toHaveCount(1);
  await expect(scheduled).toContainText('Next');
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // The same report over another window is a different email.
  await page.goto('/projects/ZZ/reports/created-vs-resolved?days=30');
  await page.locator('.report-subscribe summary').click();
  await expect(saved).toHaveCount(0);

  await page.goto(reportURL);
  await page.locator('.report-subscribe summary').click();
  await scheduled.getByRole('button', { name: 'Remove email' }).click();
  await expect(page).toHaveURL(reportURL);
  await page.locator('.report-subscribe summary').click();
  await expect(saved).toHaveCount(0);

  // Removing an email that is already gone says so on the report.
  const stale = await page.request.post('/reports/email', { form: { action: 'unsubscribe', report: '/projects/ZZ/reports/created-vs-resolved?days=7', subscriptionId: '999999999' }, headers: { Origin: new URL(reportURL).origin }, maxRedirects: 0 });
  expect(stale.status()).toBe(303);
  await page.goto(stale.headers()['location']);
  await expect(page.getByRole('alert')).toHaveText('That report email no longer exists.');
});
