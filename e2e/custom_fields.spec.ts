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

test('an administrator creates a custom field, trashes it, restores it and deletes it', async ({ page }) => {
  const stamp = Date.now().toString(36);
  const name = `Impact ${stamp}`;
  await login(page);

  await page.goto('/settings/custom-fields');
  await expect(page.getByRole('heading', { name: 'Custom fields', level: 1 })).toBeVisible();
  const create = page.getByRole('region', { name: 'Create custom field' });
  await create.getByLabel('Name').fill(name);
  await create.getByLabel('Type').selectOption('number');
  await create.getByLabel('Description').fill('Orders affected per minute.');
  await create.getByRole('button', { name: 'Create custom field' }).click();
  await expect(page.getByRole('status')).toContainText(`${name} created.`);

  let card = page.locator('.custom-field-card').filter({ has: page.getByRole('heading', { name, exact: true }) });
  const fieldID = await card.getAttribute('data-field-id');
  expect(fieldID).toMatch(/^customfield_\d+$/);

  // The field is a real Jira field: clients see it with its type.
  const fields = await page.request.get('/rest/api/3/field', { headers: { Authorization: apiAuthHeader() } });
  const described = (await fields.json()).find((field: { id: string }) => field.id === fieldID);
  expect(described.name).toBe(name);
  expect(described.schema.type).toBe('number');

  // Renaming it.
  card = page.locator(`.custom-field-card[data-field-id="${fieldID}"]`);
  await card.locator('form.custom-field-details').getByLabel('Name').fill(`${name} score`);
  await card.locator('form.custom-field-details').getByRole('button', { name: 'Save field' }).click();
  await expect(page.getByRole('status')).toContainText(`${name} score saved.`);

  // Trashing takes it off the forms but keeps it.
  card = page.locator(`.custom-field-card[data-field-id="${fieldID}"]`);
  await card.getByRole('button', { name: `Move to trash ${name} score` }).click();
  await expect(page.getByRole('status')).toContainText('Field moved to the trash');
  await expect(page.locator(`.custom-field-card[data-field-id="${fieldID}"]`)).toHaveCount(0);
  const trashed = page.locator(`[data-trashed-field-id="${fieldID}"]`);
  await expect(trashed).toBeVisible();

  // Restoring puts it back, and deleting removes it for good.
  await trashed.getByRole('button', { name: `Restore ${name} score` }).click();
  await expect(page.getByRole('status')).toContainText('Field restored.');
  await expect(page.locator(`.custom-field-card[data-field-id="${fieldID}"]`)).toBeVisible();
  await page.locator(`.custom-field-card[data-field-id="${fieldID}"]`).getByRole('button', { name: `Move to trash ${name} score` }).click();
  await page.locator(`[data-trashed-field-id="${fieldID}"]`).getByRole('button', { name: `Delete ${name} score` }).click();
  await expect(page.getByRole('status')).toContainText('Field deleted');
  await expect(page.locator(`[data-trashed-field-id="${fieldID}"]`)).toHaveCount(0);
  const after = await page.request.get('/rest/api/3/field', { headers: { Authorization: apiAuthHeader() } });
  expect((await after.json()).some((field: { id: string }) => field.id === fieldID)).toBe(false);
});
