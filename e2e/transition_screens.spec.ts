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

test('a transition collects the fields of the screen it names, and follows that screen', async ({ page }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();

  // A screen of its own, with one field on it.
  await page.goto('/settings/screens');
  const createScreen = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create screen' }) }).first();
  await createScreen.getByLabel('Screen name').fill(`Finish screen ${stamp}`);
  await createScreen.getByRole('button', { name: 'Create screen' }).click();
  const screenCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: `Finish screen ${stamp}` }) });
  await expect(screenCard).toBeVisible();
  const tabCard = screenCard.locator('.screen-tab-card').first();
  await tabCard.getByLabel(/Add a field/).selectOption('resolution');
  await tabCard.getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  // A project and workflow of its own, whose Done transition uses that screen.
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `TS${stamp}`;
  await page.getByLabel('Name', { exact: true }).fill('Transition screen project');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  const projectResponse = await page.request.get(`/rest/api/3/project/${key}`, { headers: { Authorization: apiAuthHeader() } });
  const projectID = (await projectResponse.json()).id as string;

  await page.goto('/settings/workflows');
  await page.fill('#workflow-name', `Screen-backed workflow ${stamp}`);
  await page.selectOption('#workflow-project-scope', projectID);
  await page.getByRole('button', { name: 'Create workflow' }).click();
  await expect(page).toHaveURL(/\/settings\/workflows\/workflow_/);

  await page.fill('#transition-name', 'Finish');
  await page.selectOption('#transition-from', 'st_todo');
  await page.selectOption('#transition-to', 'st_done');
  await page.selectOption('#transition-screen-id', { label: `Finish screen ${stamp}` });
  await page.getByRole('button', { name: 'Add transition' }).click();
  await expect(page.locator('.workflow-node').getByText('Finish', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Publish workflow' }).click();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();

  const created = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { project: { key }, summary: `Screen-backed work ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const issueKey = (await created.json()).key as string;

  // The transition asks for the screen's field, and not for one it does not hold.
  await page.goto(`/browse/${issueKey}`);
  const dialog = page.locator('details.transition-screen').filter({ hasText: 'Finish' });
  await dialog.locator('summary').click();
  await expect(dialog.getByLabel('Resolution')).toBeVisible();
  await expect(dialog.getByLabel('Labels')).toHaveCount(0);

  // Adding a field to the screen changes the transition, with no workflow edit.
  await page.goto('/settings/screens');
  const card = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: `Finish screen ${stamp}` }) });
  await card.locator('.screen-tab-card').first().getByLabel(/Add a field/).selectOption('labels');
  await card.locator('.screen-tab-card').first().getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  await page.goto(`/browse/${issueKey}`);
  const again = page.locator('details.transition-screen').filter({ hasText: 'Finish' });
  await again.locator('summary').click();
  await expect(again.getByLabel('Labels')).toBeVisible();

  // Completing it records what the screen collected.
  await again.getByLabel('Resolution').selectOption({ label: "Won't Do" });
  await again.getByLabel('Labels').fill('screened');
  await again.getByRole('button', { name: 'Complete Finish' }).click();
  await expect.poll(async () => {
    const response = await page.request.get(`/rest/api/3/issue/${issueKey}?fields=resolution,labels,status`, {
      headers: { Authorization: apiAuthHeader() },
    });
    const body = await response.json();
    return `${body.fields?.status?.name}:${body.fields?.resolution?.name}:${(body.fields?.labels ?? []).join(',')}`;
  }, { timeout: 15_000 }).toBe("Done:Won't Do:screened");
});
