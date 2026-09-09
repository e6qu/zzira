import { expect, test } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${tokens['demo@zzira.dev']}`).toString('base64');
}

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

test('site and project administrators manage reusable roles and project access', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');

  const stamp = Date.now().toString(36);
  const roleName = `Delivery steward ${stamp}`;
  await page.goto('/settings/project-roles');
  await expect(page.getByRole('heading', { name: 'Project roles', level: 1 })).toBeVisible();
  const createRole = page.getByRole('region', { name: 'Create role' });
  await createRole.getByLabel('Role name').fill(roleName);
  await createRole.getByLabel('Description').fill('Coordinates release readiness');
  await createRole.getByRole('button', { name: 'Create role' }).click();
  await expect(page.getByRole('status')).toContainText('Project role created.');

  let roleCard = page.locator('.project-role-admin-card').filter({
    has: page.getByRole('heading', { name: roleName, exact: true }),
  });
  const roleID = await roleCard.getAttribute('data-role-id');
  expect(roleID).toMatch(/^\d+$/);
  await roleCard.getByLabel('Description', { exact: true }).fill('Coordinates release readiness and evidence');
  await roleCard.getByRole('button', { name: 'Save role' }).click();
  await expect(page.getByRole('status')).toContainText('Project role updated.');

  roleCard = page.locator(`.project-role-admin-card[data-role-id="${roleID}"]`);
  await roleCard.getByLabel('Add default user').selectOption({ label: 'Ana Soursop' });
  await roleCard.getByRole('button', { name: 'Add user' }).click();
  await expect(page.getByRole('status')).toContainText('Default actor added.');
  await expect(page.locator(`.project-role-admin-card[data-role-id="${roleID}"]`)).toContainText('Ana Soursop');

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  const filterResponse = await page.request.post('/rest/api/3/filter', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      name: `Role filter ${stamp}`,
      description: 'Exercises the custom project-role selector',
      jql: 'project = ZZ',
      sharePermissions: [],
      editPermissions: [],
    },
  });
  expect(filterResponse.status()).toBe(200);
  const filterID = (await filterResponse.json()).id;
  await page.goto('/filters');
  await expect(page.locator('select[name="projectRoleId"]').first()).toContainText(roleName);

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const key = `RA${stamp.toUpperCase().slice(-8)}`;
  const create = await page.request.post('/rest/api/3/project', {
    headers: { Authorization: apiAuthHeader() },
    data: { key, name: `Role access ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(create.status()).toBe(201);

  await page.goto(`/projects/${key}/settings/roles`);
  await expect(page.getByRole('heading', { name: 'People and access', level: 1 })).toBeVisible();
  await expect(page.locator(`.project-role-assignment-card[data-role-id="${roleID}"]`)).toContainText('Ana Soursop');
  const adminRole = page.locator('.project-role-assignment-card[data-role-id="10000"]');
  await adminRole.getByLabel('Add user').selectOption({ label: 'Ana Soursop' });
  await adminRole.getByRole('button', { name: 'Add user' }).click();
  await expect(page.getByRole('status')).toContainText('Role actor added.');

  const member = await browser.newContext();
  const memberPage = await member.newPage();
  await login(memberPage, 'ana@zzira.dev', 'ana12345');
  await memberPage.goto(`/projects/${key}/settings/roles`);
  await expect(memberPage.getByRole('heading', { name: 'People and access', level: 1 })).toBeVisible();
  await expect(memberPage.locator('.nav-project-roles')).toHaveAttribute('aria-current', 'page');
  await memberPage.setViewportSize({ width: 320, height: 740 });
  expect(await memberPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await member.close();

  await page.goto('/settings/project-roles');
  roleCard = page.locator(`.project-role-admin-card[data-role-id="${roleID}"]`);
  await roleCard.locator('summary').filter({ hasText: 'Delete role' }).click();
  await roleCard.getByLabel('Replacement role').selectOption('10001');
  await roleCard.getByRole('button', { name: 'Delete role' }).click();
  await expect(page.getByRole('status')).toContainText('Project role deleted.');
  await expect(page.locator(`.project-role-admin-card[data-role-id="${roleID}"]`)).toHaveCount(0);

  const memberRole = await page.request.get(`/rest/api/3/project/${key}/role/10001`);
  expect(memberRole.status()).toBe(200);
  expect(JSON.stringify(await memberRole.json())).toContain('Ana Soursop');
  expect((await page.request.delete(`/rest/api/3/filter/${filterID}`, { headers: { Authorization: apiAuthHeader() } })).status()).toBe(204);
  expect((await page.request.delete(`/rest/api/3/project/${key}?enableUndo=false`, { headers: { Authorization: apiAuthHeader() } })).status()).toBe(204);
});
