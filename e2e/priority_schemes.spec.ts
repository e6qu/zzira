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

test('an administrator runs a priority scheme from the browser', async ({ page }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();

  // A project of its own, so the scheme decides what it offers.
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `PS${stamp}`;
  await page.getByLabel('Name', { exact: true }).fill('Priority schemes');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);

  // A scheme holding two priorities, created in the browser.
  await page.goto('/settings/priorities');
  const schemeName = `Support ${stamp}`;
  const create = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create priority scheme' }) });
  await create.getByLabel('Scheme name').fill(schemeName);
  await create.getByLabel('Description').fill('What support work may be');
  await create.getByLabel('High', { exact: true }).check();
  await create.getByLabel('Medium', { exact: true }).check();
  await create.getByLabel('Default priority').selectOption({ label: 'Medium' });
  await create.getByRole('button', { name: 'Create priority scheme' }).click();
  await expect(page.getByRole('status')).toContainText(`${schemeName} created.`);

  let card = page.locator('.metadata-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  await expect(card.getByText('2 priorities · 0 projects')).toBeVisible();

  // The project takes the scheme, and offers only its priorities.
  const assign = card.locator('form').filter({ has: page.getByRole('button', { name: 'Assign project' }) });
  await assign.getByLabel('Assign to project').selectOption({ label: `Priority schemes (${key})` });
  await assign.getByRole('button', { name: 'Assign project' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned to the priority scheme.');

  await page.goto(`/projects/${key}`);
  await page.getByRole('button', { name: 'Create', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await dialog.locator('details.create-more > summary').click();
  await expect(dialog.getByLabel('Priority').locator('option')).toHaveText(['None', 'High', 'Medium']);
  await expect(dialog.getByLabel('Priority').locator('option:checked')).toHaveText('Medium');
  await dialog.getByLabel('Summary').fill(`Scheme work ${stamp}`);
  await dialog.getByLabel('Priority').selectOption({ label: 'High' });
  await dialog.getByRole('button', { name: 'Create issue' }).click();
  await expect(dialog).toBeHidden();

  // Dropping High says where its work items go; the work item follows.
  await page.goto('/settings/priorities');
  card = page.locator('.metadata-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const edit = card.locator('form').filter({ has: page.getByRole('button', { name: 'Save priority scheme' }) });
  await edit.getByLabel('High', { exact: true }).uncheck();
  await edit.locator('summary').click();
  await edit.getByLabel('Work items using High', { exact: true }).selectOption({ label: 'Medium' });
  await edit.getByRole('button', { name: 'Save priority scheme' }).click();
  await expect(page.getByRole('status')).toContainText(`${schemeName} saved.`);
  card = page.locator('.metadata-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  await expect(card.getByText('1 priorities · 1 projects')).toBeVisible();
  await expect.poll(async () => {
    const response = await page.request.get(`/rest/api/3/search/jql?jql=project%20%3D%20${key}&fields=priority`, {
      headers: { Authorization: apiAuthHeader() },
    });
    const body = await response.json();
    return body.issues?.[0]?.fields?.priority?.name ?? '';
  }, { timeout: 15_000 }).toBe('Medium');

  // A scheme with a project cannot be deleted; removing the project frees it.
  await card.getByRole('button', { name: `Delete scheme ${schemeName}` }).click();
  await expect(page.getByRole('alert')).toContainText('projects must be removed');
  card = page.locator('.metadata-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  await card.getByRole('button', { name: `Remove Priority schemes from ${schemeName}` }).click();
  await expect(page.getByRole('status')).toContainText('uses the default scheme again');
  card = page.locator('.metadata-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  await card.getByRole('button', { name: `Delete scheme ${schemeName}` }).click();
  await expect(page.getByRole('status')).toContainText('Priority scheme deleted.');
  await expect(page.getByRole('heading', { name: schemeName, exact: true })).toHaveCount(0);
});
