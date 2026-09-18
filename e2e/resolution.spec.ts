import { test, expect, Page } from '@playwright/test';
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
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('a transition screen asks how work was resolved, and the field is editable', async ({ page }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();

  // A project and workflow of its own, so other specs keep theirs.
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `RS${stamp}`;
  await page.getByLabel('Name', { exact: true }).fill('Resolution screens');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  const projectResponse = await page.request.get(`/rest/api/3/project/${key}`, { headers: { Authorization: apiAuthHeader() } });
  const projectID = (await projectResponse.json()).id as string;

  await page.goto('/settings/workflows');
  await page.fill('#workflow-name', `Resolution workflow ${stamp}`);
  await page.selectOption('#workflow-project-scope', projectID);
  await page.getByRole('button', { name: 'Create workflow' }).click();
  await expect(page).toHaveURL(/\/settings\/workflows\/workflow_/);

  // A Done transition that asks for the resolution.
  const screenFields = page.locator('fieldset').filter({ hasText: 'Transition screen fields' });
  await page.fill('#transition-name', 'Finish');
  await page.selectOption('#transition-from', 'st_todo');
  await page.selectOption('#transition-to', 'st_done');
  await screenFields.getByLabel('Resolution').check();
  await page.getByRole('button', { name: 'Add transition' }).click();
  await expect(page.locator('.workflow-node').getByText('Finish', { exact: true })).toBeVisible();
  await page.fill('#transition-name', 'Reopen');
  await page.selectOption('#transition-from', 'st_done');
  await page.selectOption('#transition-to', 'st_todo');
  await page.getByRole('button', { name: 'Add transition' }).click();
  await page.getByRole('button', { name: 'Publish workflow' }).click();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();

  const created = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { project: { key }, summary: `Resolution subject ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const issueKey = (await created.json()).key as string;

  const resolutionOf = async () => {
    const response = await page.request.get(`/rest/api/3/issue/${issueKey}?fields=resolution`, { headers: { Authorization: apiAuthHeader() } });
    return (await response.json()).fields?.resolution?.name ?? null;
  };

  // The screen records the resolution the person picked, not the default.
  await page.goto(`/browse/${issueKey}`);
  const screen = page.locator('details.transition-screen').filter({ hasText: 'Finish' });
  await screen.locator('summary').click();
  await screen.getByLabel('Resolution').selectOption({ label: 'Duplicate' });
  await screen.getByRole('button', { name: 'Complete Finish' }).click();
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBe('Duplicate');

  // Reopening clears it.
  await page.goto(`/browse/${issueKey}`);
  await page.getByRole('button', { name: 'Reopen' }).click();
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBeNull();

  // The work item page edits the field on its own.
  await page.goto(`/browse/${issueKey}`);
  const field = page.locator('form.inline-field').filter({ has: page.locator('#field-resolution') });
  await field.getByLabel('Resolution').selectOption({ label: "Won't Do" });
  await field.getByRole('button', { name: 'Save resolution' }).click();
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBe("Won't Do");

  // The API sets and clears it as Jira does.
  const set = await page.request.put(`/rest/api/3/issue/${issueKey}`, {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { resolution: { name: 'Done' } } },
  });
  expect(set.status()).toBe(204);
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBe('Done');
  const cleared = await page.request.put(`/rest/api/3/issue/${issueKey}`, {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { resolution: null } },
  });
  expect(cleared.status()).toBe(204);
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBeNull();
});
