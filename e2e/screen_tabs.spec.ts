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

test('a create form shows the tabs of the screen it uses', async ({ page }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();

  // A screen with two tabs: the everyday fields, and the ones behind a tab.
  await page.goto('/settings/screens');
  const screenName = `Two tabs ${stamp}`;
  const createScreen = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create screen' }) }).first();
  await createScreen.getByLabel('Screen name').fill(screenName);
  await createScreen.getByRole('button', { name: 'Create screen' }).click();
  let screenCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: screenName, exact: true }) });
  const screenID = await screenCard.getAttribute('data-screen-id');
  await screenCard.locator('.screen-tab-card').first().getByLabel(/Add a field/).selectOption('priority');
  await screenCard.locator('.screen-tab-card').first().getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  screenCard = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  const addTab = screenCard.locator('form').filter({ has: page.getByRole('button', { name: 'Add tab' }) }).first();
  await addTab.getByLabel('Tab name').fill('Planning');
  await addTab.getByRole('button', { name: 'Add tab' }).click();
  await expect(page.getByRole('status')).toContainText('Tab added');
  screenCard = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  const planningTab = screenCard.locator('.screen-tab-card').filter({ hasText: 'Planning' });
  await planningTab.getByLabel(/Add a field/).selectOption('duedate');
  await planningTab.getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  // A project whose create screen is that screen.
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `TB${stamp}`;
  await page.getByLabel('Name', { exact: true }).fill('Tabbed screens');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);

  await page.goto('/settings/screen-schemes');
  const schemeName = `Tabbed screens ${stamp}`;
  const createScheme = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create screen scheme' }) }).first();
  await createScheme.getByLabel('Scheme name').fill(schemeName);
  await createScheme.getByRole('button', { name: 'Create screen scheme' }).click();
  const schemeCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const defaultRow = schemeCard.locator('form').filter({ hasText: 'default' }).first();
  await defaultRow.getByLabel(/Screen/).selectOption({ label: screenName });
  await defaultRow.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('status')).toContainText('Screen scheme mapping saved.');

  const typeSchemeName = `Tabbed work types ${stamp}`;
  const createTypeScheme = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create work type screen scheme' }) }).first();
  await createTypeScheme.getByLabel('Scheme name').fill(typeSchemeName);
  await createTypeScheme.getByRole('button', { name: 'Create work type screen scheme' }).click();
  const typeSchemeCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: typeSchemeName, exact: true }) });
  const mapRow = typeSchemeCard.locator('form').filter({ has: page.getByRole('button', { name: 'Map work type' }) });
  await mapRow.getByLabel('Map a work type').selectOption({ label: 'Task' });
  await mapRow.getByLabel('Screen scheme').selectOption({ label: schemeName });
  await mapRow.getByRole('button', { name: 'Map work type' }).click();
  await expect(page.getByRole('status')).toContainText('Work type mapping saved.');
  const assignRow = typeSchemeCard.locator('form').filter({ has: page.getByRole('button', { name: 'Assign project' }) });
  await assignRow.getByLabel('Project').selectOption({ label: `Tabbed screens (${key})` });
  await assignRow.getByRole('button', { name: 'Assign project' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned');

  // The create form shows both tabs, one panel at a time.
  await page.goto(`/projects/${key}`);
  await page.getByRole('button', { name: 'Create', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await dialog.locator('details.create-more > summary').click();
  const tabs = dialog.getByRole('tab');
  await expect(tabs).toHaveCount(2);
  await expect(dialog.getByLabel('Priority')).toBeVisible();
  await expect(dialog.getByLabel('Due date')).toBeHidden();

  await tabs.nth(1).click();
  await expect(dialog.getByLabel('Due date')).toBeVisible();
  await expect(dialog.getByLabel('Priority')).toBeHidden();

  // What a tab collects is on the work item.
  await dialog.getByLabel('Summary').fill(`Tabbed work ${stamp}`);
  await dialog.getByLabel('Due date').fill('2030-03-04');
  await dialog.getByRole('button', { name: 'Create issue' }).click();
  await expect.poll(async () => {
    const response = await page.request.get(`/rest/api/3/search/jql?jql=project%20%3D%20${key}&fields=duedate`, {
      headers: { Authorization: apiAuthHeader() },
    });
    const body = await response.json();
    return body.issues?.[0]?.fields?.duedate ?? '';
  }, { timeout: 15_000 }).toBe('2030-03-04');
});
