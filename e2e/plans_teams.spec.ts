import { expect, test, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('a team is created in the directory and planned in a plan', async ({ page }) => {
  await login(page);
  const suffix = Date.now().toString(36);
  const headers = { Authorization: apiAuthHeader() };

  await page.goto('/people');
  await page.getByRole('link', { name: 'Teams', exact: true }).click();
  await expect(page).toHaveURL('/teams');
  await expect(page.getByRole('heading', { name: 'Teams', level: 1 })).toBeVisible();
  await page.getByLabel('Team name').fill(`Platform ${suffix}`);
  await page.getByLabel('Description').fill('Builds the delivery platform');
  await page.getByRole('button', { name: 'Create team' }).click();
  await expect(page).toHaveURL(/\/teams\/[0-9a-f-]{36}\?saved=/);
  await expect(page.getByRole('status')).toContainText('Team created');
  await expect(page.getByRole('heading', { name: `Platform ${suffix}`, level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: /Members/ })).toContainText('1');
  const teamID = (await page.locator('main header code').textContent())!.trim();

  const project = await page.request.get('/rest/api/3/project/ZZ', { headers });
  expect(project.status()).toBe(200);
  const projectID = Number((await project.json()).id);
  const created = await page.request.post('/rest/api/3/plans/plan', {
    headers,
    data: { name: `Roadmap ${suffix}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: projectID }] },
  });
  expect(created.status()).toBe(201);
  const planID = await created.json();
  const added = await page.request.post(`/rest/api/3/plans/plan/${planID}/team/atlassian`, { headers, data: { id: teamID, planningStyle: 'Kanban', capacity: 20 } });
  expect(added.status()).toBe(204);
  const teams = await page.request.get(`/rest/api/3/plans/plan/${planID}/team`, { headers });
  expect(teams.status()).toBe(200);
  expect((await teams.json()).values).toEqual([{ id: teamID, type: 'Atlassian' }]);

  // Deleting the team removes it from the plan.
  await page.getByRole('button', { name: 'Delete team' }).click();
  await expect(page).toHaveURL('/teams');
  await expect(page.getByRole('link', { name: new RegExp(`Platform ${suffix}`) })).toHaveCount(0);
  const after = await page.request.get(`/rest/api/3/plans/plan/${planID}/team`, { headers });
  expect((await after.json()).total).toBe(0);
  expect((await page.request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers })).status()).toBe(204);
});

test('site administrators maintain the service registry', async ({ page }) => {
  await login(page);
  const name = `Payments ${Date.now().toString(36)}`;
  await page.goto('/service-registry');
  await expect(page.getByRole('heading', { name: 'Services', level: 1 })).toBeVisible();
  await page.getByLabel('Service name').fill(name);
  await page.getByLabel('Description').fill('Takes card payments');
  await page.getByLabel('Tier').selectOption('1');
  await page.getByRole('button', { name: 'Add service' }).click();
  await expect(page).toHaveURL(/saved=/);
  await expect(page.getByRole('status')).toContainText('Service created');
  const row = page.getByRole('row', { name: new RegExp(name) });
  await expect(row).toContainText('Tier 1');
  await expect(row.locator('code')).toHaveText(/[0-9a-f-]{36}/);
  await row.getByRole('button', { name: `Delete ${name}` }).click();
  await expect(page.getByRole('status')).toContainText('Service deleted');
  await expect(page.getByRole('row', { name: new RegExp(name) })).toHaveCount(0);
});
