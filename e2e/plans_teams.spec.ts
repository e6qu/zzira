import { apiAuthHeader } from './auth';
import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

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
  await page.goto(`/plans/${planID}`);
  await page.getByText('Add an existing team', { exact: true }).click();
  await page.getByLabel('Team', { exact: true }).selectOption(teamID);
  await page.getByRole('button', { name: 'Add team to plan' }).click();
  await expect(page.getByRole('status')).toContainText('Team added to the plan.');
  const row = page.getByRole('row').filter({ has: page.getByRole('rowheader', { name: `Platform ${suffix}`, exact: true }) });
  await row.locator('summary').filter({ hasText: /^Settings/ }).click();
  await row.getByLabel('Planning style').selectOption('Kanban');
  await row.getByLabel('Capacity', { exact: true }).fill('20');
  await row.getByRole('button', { name: 'Save team settings' }).click();
  await expect(page.getByRole('status')).toContainText('Team settings saved.');
  await expect(row).toContainText('Kanban');
  const teams = await page.request.get(`/rest/api/3/plans/plan/${planID}/team`, { headers });
  expect(teams.status()).toBe(200);
  expect((await teams.json()).values).toEqual([{ id: teamID, type: 'Atlassian' }]);

  // Removing it from a plan leaves the directory team and its members intact.
  await row.locator('summary').filter({ hasText: 'Remove' }).click();
  await row.getByRole('button', { name: 'Remove team from plan' }).click();
  await expect(page.getByRole('status')).toContainText('Team removed from the plan.');
  await expect(row).toHaveCount(0);
  await page.goto(`/teams/${teamID}`);
  await expect(page.getByRole('heading', { name: `Platform ${suffix}`, level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: /Members/ })).toContainText('1');
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

test('a planner creates a plan-only team and retries a refused save without losing answers', async ({ page, browser, request }) => {
  await login(page);
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36);
  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const created = await request.post('/rest/api/3/plans/plan', {
    headers, data: { name: `Team setup ${stamp}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(project.id) }] },
  });
  expect(created.status()).toBe(201);
  const planID = await created.json();
  const teamName = `Mobile ${stamp}`;
  const colleague = await browser.newContext();
  try {
    await page.goto(`/plans/${planID}`);
    await page.getByText('Create a plan-only team', { exact: true }).click();
    const form = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create team in plan' }) });
    await form.getByLabel('Team name', { exact: true }).fill(teamName);
    await form.getByLabel('Planning style').selectOption('Kanban');
    await form.getByRole('checkbox', { name: 'Ana Soursop', exact: true }).check();
    await form.getByLabel('Capacity', { exact: true }).fill('-1');
    // Exercise a server refusal rather than relying on browser validation.
    await form.evaluate(element => { (element as HTMLFormElement).noValidate = true; });
    await form.getByRole('button', { name: 'Create team in plan' }).click();
    await expect(page.getByRole('alert')).toContainText('finite, non-negative');
    await expect(form.getByLabel('Team name', { exact: true })).toHaveValue(teamName);
    await expect(form.getByLabel('Planning style')).toHaveValue('Kanban');
    await expect(form.getByRole('checkbox', { name: 'Ana Soursop', exact: true })).toBeChecked();
    await expect(form.getByLabel('Capacity', { exact: true })).toHaveValue('-1');
    await form.getByLabel('Capacity', { exact: true }).fill('12');
    await form.getByRole('button', { name: 'Create team in plan' }).click();
    await expect(page.getByRole('status')).toContainText('Team added to the plan.');
    await page.reload();
    const row = page.getByRole('row').filter({ has: page.getByRole('rowheader', { name: teamName, exact: true }) });
    await expect(row).toContainText('Kanban');
    await expect(row.getByRole('cell').nth(2)).toHaveText('12');
    await expect(row.getByRole('cell').nth(3)).toHaveText('1');
    const teams = await (await request.get(`/rest/api/3/plans/plan/${planID}/team`, { headers })).json();
    expect(teams.values).toHaveLength(1);
    const teamID = teams.values[0].id;
    const saved = await (await request.get(`/rest/api/3/plans/plan/${planID}/team/planonly/${teamID}`, { headers })).json();
    expect(saved.capacity).toBe(12);
    expect(saved.memberAccountIds).toHaveLength(1);

    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
    expect(violations).toEqual([]);
    await page.setViewportSize({ width: 320, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
    await page.setViewportSize({ width: 1280, height: 720 });

    await page.goto(`/plans/${planID}/settings`);
    const grant = page.locator('form').filter({ has: page.getByRole('button', { name: 'Give access' }) });
    await grant.getByLabel('Person', { exact: true }).selectOption({ label: 'Ana Soursop' });
    await grant.getByLabel('Access').selectOption('View');
    await grant.getByRole('button', { name: 'Give access' }).click();
    const viewer = await colleague.newPage();
    await viewer.goto('/login');
    await viewer.fill('#login-email', 'ana@zzira.dev');
    await viewer.fill('#login-password', 'ana12345');
    await viewer.click('button[type=submit]');
    await viewer.goto(`/plans/${planID}`);
    await expect(viewer.getByRole('rowheader', { name: teamName, exact: true })).toBeVisible();
    await expect(viewer.getByText('Create a plan-only team', { exact: true })).toHaveCount(0);
    for (const action of ['add-planonly', 'remove']) {
      const refused = await viewer.request.post(`/plans/${planID}/teams/membership`, { form: { action, name: 'Forbidden', planningStyle: 'Kanban', team: teamID } });
      expect(refused.status()).toBe(403);
    }

    await page.goto(`/plans/${planID}`);
    await row.locator('summary').filter({ hasText: 'Remove' }).click();
    await row.getByRole('button', { name: 'Remove team from plan' }).click();
    await expect(page.getByRole('status')).toContainText('Team removed from the plan.');
    await expect(row).toHaveCount(0);
    expect((await (await request.get(`/rest/api/3/plans/plan/${planID}/team`, { headers })).json()).total).toBe(0);
  } finally {
    await colleague.close();
    await request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers });
  }
});
