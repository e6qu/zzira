import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkAccessibility(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

test('knowledge collaborator models records with typed columns and saved views', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.goto('/wiki');

  await page.locator('.wiki-create-space > summary').click();
  const suffix = Date.now().toString(36).toUpperCase();
  await page.getByLabel('Space name').fill(`Release knowledge ${suffix}`);
  await page.getByLabel('Space key').fill(`D${suffix}`);
  await page.getByLabel('Description', { exact: true }).fill('Structured release decisions');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();

  const databases = page.getByRole('region', { name: 'Databases' });
  await databases.locator('summary').filter({ hasText: 'Create database' }).click();
  await databases.getByLabel('Database name').fill('Release register');
  await databases.getByRole('button', { name: 'Create database', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+\/databases\/\d+$/);
  await expect(page.getByRole('heading', { name: 'Release register', level: 1 })).toBeVisible();

  async function addColumn(name: string, key: string, type: string, options = '') {
    await page.locator('summary').filter({ hasText: 'Add column' }).click();
    await page.getByLabel('Column name').fill(name);
    await page.getByLabel('Column key').fill(key);
    await page.getByLabel('Type', { exact: true }).selectOption(type);
    if (options) await page.getByLabel('Select options').fill(options);
    await page.getByRole('button', { name: 'Add column', exact: true }).click();
  }

  await addColumn('Release', 'release', 'text');
  await addColumn('Phase', 'phase', 'select', 'Planned, Released');
  await addColumn('Risk score', 'risk', 'number');
  await addColumn('Target date', 'target', 'date');
  await addColumn('Approved', 'approved', 'checkbox');
  await expect(page.getByRole('region', { name: 'Columns' })).toContainText('Planned, Released');

  async function addRecord(release: string, phase: string, risk: string, approved: string) {
    await page.locator('summary').filter({ hasText: 'Add record' }).click();
    const form = page.locator('details[open]').filter({ hasText: 'Add record' }).locator('form');
    await form.locator('[name="value_release"]').fill(release);
    await form.locator('[name="value_phase"]').selectOption(phase);
    await form.locator('[name="value_risk"]').fill(risk);
    await form.locator('[name="value_target"]').fill('2030-06-15');
    await form.locator('[name="value_approved"]').selectOption(approved);
    await form.getByRole('button', { name: 'Add record', exact: true }).click();
  }

  await addRecord('ZZIRA 2.0', 'Released', '10', 'true');
  await addRecord('ZZIRA 2.1', 'Planned', '2', 'false');
  await expect(page.getByText('2 records', { exact: true })).toBeVisible();

  await page.locator('summary').filter({ hasText: 'Create saved view' }).click();
  await page.getByLabel('View name').fill('Released by risk');
  await page.getByLabel('Sort by').selectOption('risk');
  await page.getByLabel('Direction').selectOption('desc');
  await page.getByLabel('Filter column').selectOption('phase');
  await page.getByLabel('Contains').fill('Released');
  await page.getByRole('button', { name: 'Save view', exact: true }).click();
  await page.getByRole('link', { name: 'Released by risk', exact: true }).click();
  await expect(page.locator('.wiki-database-record')).toHaveCount(1);
  await expect(page.locator('.wiki-database-record [name="value_release"]')).toHaveValue('ZZIRA 2.0');

  const record = page.locator('.wiki-database-record').first();
  await record.locator('[name="value_risk"]').fill('8');
  await record.getByRole('button', { name: 'Save record', exact: true }).click();
  await expect(page.locator('.wiki-database-record').filter({ has: page.locator('[name="value_release"][value="ZZIRA 2.0"]') }).locator('[name="value_risk"]')).toHaveValue('8');

  await checkAccessibility(page);
  await page.locator('[data-theme-toggle]').click();
  await checkAccessibility(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});
