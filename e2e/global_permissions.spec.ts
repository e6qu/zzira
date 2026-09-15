import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function basicAuth(email: string): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  return 'Basic ' + Buffer.from(`${email}:${tokens[email]}`).toString('base64');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('site admin removes and grants a global permission that decides what members may do', async ({ page }) => {
  const memberHeaders = { Authorization: basicAuth('ana@zzira.dev') };
  const memberHasBulkChange = async () => {
    const response = await page.request.get('/rest/api/3/mypermissions?permissions=BULK_CHANGE', { headers: memberHeaders });
    expect(response.status()).toBe(200);
    return (await response.json()).permissions.BULK_CHANGE.havePermission as boolean;
  };
  expect(await memberHasBulkChange()).toBe(true);

  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto('/admin#admin-global-permissions');
  const section = page.locator('#admin-global-permissions');
  await expect(section.getByRole('heading', { name: 'Global permissions' })).toBeVisible();
  const bulkChange = section.locator('.admin-global-permission').filter({ has: page.getByText('Bulk change', { exact: true }) });
  await expect(bulkChange).toContainText('Everyone with Jira access');
  await accessible(page);

  // Removing the grant takes bulk change away from members.
  await bulkChange.getByRole('button', { name: 'Remove this BULK_CHANGE grant' }).click();
  await expect(page.locator('#admin-global-permissions .admin-global-permission').filter({ has: page.getByText('Bulk change', { exact: true }) })).toContainText('Not granted');
  expect(await memberHasBulkChange()).toBe(false);

  // Granting it to everyone with Jira access gives it back.
  await page.locator('#admin-global-permissions').getByLabel('Permission', { exact: true }).selectOption('BULK_CHANGE');
  await page.locator('#admin-global-permissions').getByLabel('Granted to', { exact: true }).selectOption('product:jira-software');
  await page.locator('#admin-global-permissions').getByRole('button', { name: 'Grant permission', exact: true }).click();
  await expect(page.locator('#admin-global-permissions .admin-global-permission').filter({ has: page.getByText('Bulk change', { exact: true }) })).toContainText('Everyone with Jira access');
  expect(await memberHasBulkChange()).toBe(true);
});
