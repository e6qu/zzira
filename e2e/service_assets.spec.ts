import { expect, test } from '@playwright/test';
import axe from 'axe-core';

async function accessible(page: import('@playwright/test').Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

async function login(page: import('@playwright/test').Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
}

test('service manager models assets and an agent calculates request impact', async ({ page }) => {
  await login(page);
  const key = `A${String(Date.now()).slice(-9)}`;
  await page.goto('/projects/new');
  await page.getByLabel('Name').fill(`Asset desk ${key}`);
  await page.getByLabel('Key').fill(key);
  await page.getByLabel('Template').selectOption('com.atlassian.servicedesk:simplified-it-service-management');
  await page.getByLabel('Description').fill('Asset-backed service operations');
  await page.getByRole('button', { name: 'Create project' }).click();

  await page.goto('/service/agent');
  await page.getByRole('link', { name: `Asset desk ${key}` }).click();
  const deskURL = page.url();
  const deskID = deskURL.split('/').pop()!;
  await page.getByRole('link', { name: 'Assets', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Assets', level: 1 })).toBeVisible();

  const schemaCreate = page.locator('details').filter({ hasText: 'Create schema' });
  await schemaCreate.locator('summary').click();
  await schemaCreate.getByLabel('Schema name').fill('Business services');
  await schemaCreate.getByLabel('Schema key').fill('SERVICE');
  await schemaCreate.getByLabel('Description').fill('Customer-facing production services');
  await schemaCreate.getByLabel('Attributes').fill('tier | Service tier | select | required | Tier 1,Tier 2\ncapacity | Capacity | number | required\nactive | Active | boolean');
  await schemaCreate.getByRole('button', { name: 'Create schema' }).click();
  await expect(page.locator('#schemas')).toContainText('Business services');

  let objectCreate = page.locator('details').filter({ hasText: 'Add Business services object' }).last();
  await objectCreate.locator('summary').click();
  await objectCreate.getByLabel('Object key').fill('DATABASE');
  await objectCreate.getByLabel('Label').fill('Checkout database');
  await objectCreate.getByLabel('Service tier').selectOption('Tier 1');
  await objectCreate.getByLabel('Capacity').fill('1200');
  await objectCreate.getByLabel('Active').selectOption('true');
  await objectCreate.getByLabel('X position').fill('760');
  await objectCreate.getByLabel('Y position').fill('320');
  await objectCreate.getByRole('button', { name: 'Add object' }).click();
  await expect(page.locator('#objects')).toContainText('Checkout database');

  objectCreate = page.locator('details').filter({ hasText: 'Add Business services object' }).last();
  await objectCreate.locator('summary').click();
  await objectCreate.getByLabel('Object key').fill('STOREFRONT');
  await objectCreate.getByLabel('Label').fill('Customer storefront');
  await objectCreate.getByLabel('Service tier').selectOption('Tier 1');
  await objectCreate.getByLabel('Capacity').fill('5000');
  await objectCreate.getByLabel('X position').fill('340');
  await objectCreate.getByLabel('Y position').fill('320');
  await objectCreate.getByRole('button', { name: 'Add object' }).click();

  const databaseCard = page.locator('#objects article').filter({ hasText: 'Checkout database' });
  const databaseEdit = databaseCard.locator('details').filter({ hasText: 'Edit object' });
  await databaseEdit.locator('summary').click();
  await databaseEdit.getByLabel('Capacity').fill('1400');
  await databaseEdit.getByRole('button', { name: 'Save object' }).click();
  await expect(page.locator('#objects article').filter({ hasText: 'Checkout database' })).toContainText('1400');

  const relationshipCreate = page.locator('details').filter({ hasText: 'Connect two objects' });
  await relationshipCreate.locator('summary').click();
  await relationshipCreate.getByLabel('Object that depends').selectOption({ label: 'STOREFRONT · Customer storefront' });
  await relationshipCreate.getByLabel('Dependency').selectOption({ label: 'DATABASE · Checkout database' });
  await relationshipCreate.getByRole('button', { name: 'Add relationship' }).click();
  await expect(page.locator('#topology tbody')).toContainText('STOREFRONT · Customer storefront');
  await expect(page.locator('#topology svg line')).toHaveCount(1);
  await accessible(page);
  const toggle = page.locator('[data-theme-toggle]');
  if (await toggle.getAttribute('aria-pressed') === 'false') await toggle.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await accessible(page);

  await page.goto(`/service/portals/${deskID}`);
  await page.getByRole('link', { name: /Get IT help/ }).click();
  const summary = `Checkout outage ${Date.now()}`;
  await page.getByLabel('Summary').fill(summary);
  await page.getByRole('button', { name: 'Send request' }).click();
  await expect(page.getByRole('heading', { name: summary, level: 1 })).toBeVisible();
  const impact = page.locator('#asset-impact');
  await impact.locator('#request-asset-object').selectOption({ label: 'DATABASE · Checkout database' });
  await impact.locator('#request-asset-role').selectOption('affected');
  await impact.getByRole('button', { name: 'Connect asset' }).click();
  await expect(page.locator('#asset-impact')).toContainText('Affected directly');
  await expect(page.locator('#asset-impact')).toContainText('STOREFRONT · Customer storefront');
  await expect(page.locator('#asset-impact')).toContainText('Impacted through 1 relationship');
  await accessible(page);
});
