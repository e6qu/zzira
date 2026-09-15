import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
const authFor = (email: string) => ({ Authorization: 'Basic ' + Buffer.from(`${email}:${tokens[email]}`).toString('base64') });

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('an administrator checks why someone holds a project permission', async ({ page }) => {
  const anaAuth = authFor('ana@zzira.dev');
  const ana = await (await page.request.get('/rest/api/3/myself', { headers: anaAuth })).json();
  const created = await page.request.post('/rest/api/3/issue', { headers: authFor('demo@zzira.dev'), data: { fields: { project: { key: 'ZZ' }, summary: `Permission helper ${Date.now()}`, issuetype: { name: 'Task' } } } });
  expect(created.status()).toBe(201);
  const { key } = await created.json();
  // The helper must agree with what Ana's own permission check reports.
  const candidates = ['ADMINISTER_PROJECTS', 'DELETE_ALL_COMMENTS', 'EDIT_WORKFLOW', 'DELETE_ALL_ATTACHMENTS', 'MANAGE_SPRINTS_PERMISSION'];
  const mine = (await (await page.request.get(`/rest/api/3/mypermissions?issueKey=${key}&permissions=BROWSE_PROJECTS,${candidates.join(',')}`, { headers: anaAuth })).json()).permissions;
  expect(mine.BROWSE_PROJECTS.havePermission).toBe(true);
  const lacking = candidates.find((permission) => !mine[permission].havePermission);
  expect(lacking).toBeTruthy();

  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', tokens['demo@zzira.dev.password']);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto('/admin#admin-global-permissions');
  await page.getByRole('link', { name: 'Use the permission helper' }).click();
  await expect(page.getByRole('heading', { name: 'Permission helper', level: 1 })).toBeVisible();

  const check = async (person: string, permission: string) => {
    await page.getByRole('combobox', { name: 'Person' }).selectOption({ label: person });
    await page.getByRole('textbox', { name: 'Work item key' }).fill(key);
    await page.getByRole('combobox', { name: 'Permission' }).selectOption(permission);
    await page.getByRole('button', { name: 'Check permission' }).click();
  };

  await check(ana.displayName, 'BROWSE_PROJECTS');
  const granted = page.getByRole('region', { name: `${ana.displayName} has Browse projects` });
  await expect(granted).toContainText(`Checked on ${key}`);
  await expect(granted.getByRole('listitem').first()).toContainText('Granted to');
  await accessible(page);

  await check(ana.displayName, lacking!);
  const denied = page.getByRole('region', { name: new RegExp(`^${ana.displayName} does not have `) });
  await expect(denied).toContainText(`includes ${ana.displayName}.`);

  const demo = await (await page.request.get('/rest/api/3/myself', { headers: authFor('demo@zzira.dev') })).json();
  await check(demo.displayName, lacking!);
  await expect(page.getByRole('region', { name: new RegExp(`^${demo.displayName} has `) })).toContainText('site or organization administrator');
});
