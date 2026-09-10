import { expect, test } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

// The journey creates and deletes its own screen so the shared workspace keeps
// the same shape for every other spec.
test('site administrators compose a screen from tabs and ordered fields', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const screenName = `Release readiness ${Date.now().toString(36)}`;

  await page.goto('/settings/screens');
  await expect(page.getByRole('heading', { name: 'Screens', level: 1 })).toBeVisible();
  await expect(page.locator('.nav-screens')).toHaveAttribute('aria-current', 'page');
  // The workspace is provisioned with a default screen.
  await expect(page.getByRole('heading', { name: 'Default Screen', level: 2 })).toBeVisible();

  const create = page.getByRole('region', { name: 'Create a screen' });
  await create.getByLabel('Screen name').fill(screenName);
  await create.getByLabel('Description').fill('Fields reviewed before a release goes out');
  await create.getByRole('button', { name: 'Create screen' }).click();
  await expect(page.getByRole('status')).toContainText('Screen created.');

  let card = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: screenName, exact: true }) });
  const screenID = await card.getAttribute('data-screen-id');
  expect(screenID).toMatch(/^\d+$/);
  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);

  // A new screen starts with one tab, so fields have somewhere to land.
  await expect(card.locator('.screen-tab-card')).toHaveCount(1);
  const firstTab = card.locator('.screen-tab-card').first();
  const firstTabID = await firstTab.getAttribute('data-tab-id');

  for (const field of ['Summary', 'Priority']) {
    await page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card[data-tab-id="${firstTabID}"]`)
      .getByLabel('Add a field').selectOption({ label: field });
    await page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card[data-tab-id="${firstTabID}"]`)
      .getByRole('button', { name: 'Add field' }).click();
    await expect(page.getByRole('status')).toContainText('Field added to the tab.');
  }

  const fieldIDs = async () => page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card[data-tab-id="${firstTabID}"] .screen-field-row small`).allTextContents();
  expect(await fieldIDs()).toEqual(['summary', 'priority']);

  // Moving Priority up reverses the pair.
  await page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card[data-tab-id="${firstTabID}"] .screen-field-row`)
    .filter({ hasText: 'priority' }).getByRole('button', { name: /Move up/ }).click();
  await expect(page.getByRole('status')).toContainText('Field moved.');
  expect(await fieldIDs()).toEqual(['priority', 'summary']);

  // A second tab groups the remaining fields.
  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  await card.getByLabel('Tab name').first().fill('Release checks');
  await card.getByRole('button', { name: 'Add tab' }).click();
  await expect(page.getByRole('status')).toContainText('Tab added.');
  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  await expect(card.locator('.screen-tab-card')).toHaveCount(2);

  const secondTab = card.locator('.screen-tab-card').nth(1);
  const secondTabID = await secondTab.getAttribute('data-tab-id');
  await secondTab.getByLabel('Add a field').selectOption({ label: 'Labels' });
  await secondTab.getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added to the tab.');

  // Moving the second tab earlier reorders the screen.
  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  await card.locator('.screen-tab-card').nth(1).getByRole('button', { name: /Move tab earlier/ }).click();
  await expect(page.getByRole('status')).toContainText('Tab moved.');
  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  expect(await card.locator('.screen-tab-card').first().getAttribute('data-tab-id')).toBe(secondTabID);

  // The REST contract agrees with what the browser shows.
  const tabs = await (await page.request.get(`/rest/api/3/screens/${screenID}/tabs`, { headers: { Authorization: apiAuthHeader() } })).json();
  expect(tabs.map((tab: { id: number }) => String(tab.id))).toEqual([secondTabID, firstTabID]);
  const restFields = await (await page.request.get(`/rest/api/3/screens/${screenID}/tabs/${firstTabID}/fields`, { headers: { Authorization: apiAuthHeader() } })).json();
  expect(restFields.map((field: { id: string }) => field.id)).toEqual(['priority', 'summary']);
  // A field already on the screen is no longer offered.
  const available = await (await page.request.get(`/rest/api/3/screens/${screenID}/availableFields`, { headers: { Authorization: apiAuthHeader() } })).json();
  expect(available.map((field: { id: string }) => field.id)).not.toContain('summary');

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Removing a field and a tab leaves the screen consistent.
  await page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card[data-tab-id="${firstTabID}"] .screen-field-row`)
    .filter({ hasText: 'summary' }).getByRole('button', { name: /Remove/ }).click();
  await expect(page.getByRole('status')).toContainText('Field removed from the tab.');
  expect(await fieldIDs()).toEqual(['priority']);

  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  const removable = card.locator(`.screen-tab-card[data-tab-id="${secondTabID}"]`);
  await removable.locator('summary').filter({ hasText: 'Rename or remove tab' }).click();
  await removable.getByRole('button', { name: 'Remove tab' }).click();
  await expect(page.getByRole('status')).toContainText('Tab removed.');
  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  await expect(card.locator('.screen-tab-card')).toHaveCount(1);

  // The last tab and the default screen are both protected.
  const lastTab = card.locator('.screen-tab-card').first();
  await lastTab.locator('summary').filter({ hasText: 'Rename or remove tab' }).click();
  await lastTab.getByRole('button', { name: 'Remove tab' }).click();
  await expect(page.getByRole('alert')).toContainText('at least one tab');

  card = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  await card.locator('summary').filter({ hasText: 'Delete screen' }).click();
  await card.getByRole('button', { name: 'Delete screen' }).click();
  await expect(page.getByRole('status')).toContainText('Screen deleted.');
  await expect(page.locator(`.screen-card[data-screen-id="${screenID}"]`)).toHaveCount(0);
});
