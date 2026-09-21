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

test('a project administrator configures their own project workflow', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const key = `PW${stamp.toUpperCase().slice(-8)}`;
  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: { Authorization: apiAuthHeader() },
    data: { key, name: `Project workflow ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);

  // A site administrator's own workflow, shared by every company-managed
  // project, stays a site administrator's to change.
  await page.goto('/settings/workflows');
  const globalName = `Shared workflow ${stamp}`;
  const createWorkflow = page.locator('form.workflow-create');
  await createWorkflow.getByLabel('Create a workflow from the default').fill(globalName);
  await createWorkflow.locator('select[name="project"]').selectOption('');
  await createWorkflow.getByRole('button', { name: 'Create workflow' }).click();
  await expect(page.getByRole('heading', { name: globalName, level: 1 })).toBeVisible();
  const globalWorkflowID = new URL(page.url()).pathname.split('/').pop()!;

  const collaborator = await browser.newContext();
  const ana = await collaborator.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');

  // A member who does not administer the project cannot configure it.
  await ana.goto(`/projects/${key}/settings`);
  await expect(ana.locator('body')).toContainText('forbidden');
  await ana.goto('/settings/workflows');
  await expect(ana.locator('form.workflow-create option').filter({ hasText: key })).toHaveCount(0);

  await page.goto(`/projects/${key}/settings/roles`);
  const administrators = page.locator('.project-role-assignment-card[data-role-id="10000"]');
  await administrators.getByLabel('Add user').selectOption({ label: 'Ana Soursop' });
  await administrators.getByRole('button', { name: 'Add user' }).click();
  await expect(page.getByRole('status')).toContainText('Role actor added.');

  // Now the same person configures the project, without the site-level
  // actions the page also hosts for administrators.
  await ana.goto(`/projects/${key}/settings`);
  await expect(ana.getByRole('heading', { name: 'Details', level: 2 })).toBeVisible();
  await expect(ana.getByRole('heading', { name: 'Project templates' })).toHaveCount(0);
  await expect(ana.getByRole('heading', { name: 'Archive or move to trash' })).toHaveCount(0);
  await expect(ana.getByRole('heading', { name: 'Features and integration data' })).toBeVisible();
  await ana.goto('/settings/workflows');
  await expect(ana.locator('form.workflow-create option').filter({ hasText: key })).toHaveCount(1);
  await expect(ana.locator('form.workflow-create option').filter({ hasText: 'Global' })).toHaveCount(0);

  // The shared workflow is read-only for them, in the browser and over HTTP.
  await ana.goto(`/settings/workflows/${globalWorkflowID}`);
  await expect(ana.getByRole('heading', { name: globalName, level: 1 })).toBeVisible();
  await expect(ana.getByRole('heading', { name: 'Add transition' })).toHaveCount(0);
  const refused = await ana.request.post(`/settings/workflows/${globalWorkflowID}/transitions`, {
    headers: { Origin: new URL(ana.url()).origin, 'Content-Type': 'application/x-www-form-urlencoded' },
    form: { name: 'Sneak', from: 'any', to: 'status_done' },
  });
  expect(refused.status()).toBe(403);

  // A workflow of the project's own is theirs to edit and publish.
  await ana.goto(`/projects/${key}/settings`);
  const startWorkflow = ana.locator('form.project-workflow-create');
  await expect(startWorkflow).toBeVisible();
  await startWorkflow.getByLabel('Workflow name').fill(`Project workflow ${stamp}`);
  await startWorkflow.getByRole('button', { name: 'Create project workflow' }).click();
  await expect(ana.getByRole('heading', { name: `Project workflow ${stamp}`, level: 1 })).toBeVisible();
  const workflowID = new URL(ana.url()).pathname.split('/').pop()!;

  const addTransition = ana.locator('form.workflow-form').filter({ has: ana.getByRole('heading', { name: 'Add transition' }) });
  await addTransition.getByLabel('Name').fill('Needs evidence');
  await addTransition.getByLabel('From status').selectOption({ index: 0 });
  await addTransition.getByLabel('To status').selectOption({ index: 1 });
  await addTransition.getByRole('button', { name: 'Add transition' }).click();
  await expect(ana.locator('#main-content')).toContainText('Needs evidence');
  await ana.getByRole('button', { name: 'Publish workflow' }).click();
  await expect(ana.getByRole('status')).toContainText('Workflow published');

  // The project now routes through its own workflow, so the page stops
  // offering to start one.
  await ana.goto(`/projects/${key}/settings`);
  await expect(ana.locator('form.project-workflow-create')).toHaveCount(0);
  await expect(ana.getByRole('link', { name: 'Manage workflow' })).toHaveAttribute('href', `/settings/workflows/${workflowID}`);

  await ana.setViewportSize({ width: 320, height: 740 });
  expect(await ana.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await collaborator.close();

  expect((await page.request.delete(`/rest/api/3/project/${key}?enableUndo=false`, { headers: { Authorization: apiAuthHeader() } })).status()).toBe(204);
});
