import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
const authFor = (email: string) => ({ Authorization: 'Basic ' + Buffer.from(`${email}:${tokens[email]}`).toString('base64') });

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so the page is
  // checked from the top rather than wherever it was scrolled.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('an administrator checks who is notified with the notification helper', async ({ page }) => {
  const demo = authFor('demo@zzira.dev');
  const ana = await (await page.request.get('/rest/api/3/myself', { headers: authFor('ana@zzira.dev') })).json();
  const created = await page.request.post('/rest/api/3/issue', { headers: demo, data: { fields: { project: { key: 'ZZ' }, summary: `Helper ${Date.now()}`, issuetype: { name: 'Task' }, assignee: { accountId: ana.accountId } } } });
  expect(created.status()).toBe(201);
  const { key } = await created.json();

  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', tokens['demo@zzira.dev.password']);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto('/admin#admin-issue-events');
  await page.getByRole('link', { name: 'Use the notification helper' }).click();
  await expect(page.getByRole('heading', { name: 'Notification helper', level: 1 })).toBeVisible();

  const check = async (eventName: string) => {
    await page.getByRole('combobox', { name: 'Person' }).selectOption({ label: ana.displayName });
    await page.getByRole('textbox', { name: 'Work item key' }).fill(key);
    await page.getByRole('combobox', { name: 'Event' }).selectOption({ label: eventName });
    await page.getByRole('button', { name: 'Check notification' }).click();
  };

  // The default scheme notifies the assignee when work is assigned.
  await check('Issue assigned');
  const result = page.getByRole('region', { name: `${ana.displayName} will be notified` });
  await expect(result).toContainText(`Issue assigned on ${key}, under Default Notification Scheme.`);
  await expect(result.getByRole('listitem').first()).toContainText(/assignee/i);
  await expect(result).toContainText('Can browse the project');
  await accessible(page);

  // No default rule covers an edited comment.
  await check('Issue comment edited');
  await expect(page.getByRole('region', { name: `${ana.displayName} will not be notified` })).toContainText(`No rule for this event names ${ana.displayName}.`);

  await page.getByRole('textbox', { name: 'Work item key' }).fill('NOPE-404');
  await page.getByRole('button', { name: 'Check notification' }).click();
  await expect(page.getByRole('alert')).toHaveText('No work item has the key NOPE-404.');
});
