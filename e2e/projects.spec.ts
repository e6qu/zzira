import { expect, test } from '@playwright/test';

test('create a project, use its board, and update settings through UI and API', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `P${Date.now().toString(36).toUpperCase()}`;
  await page.getByLabel('Name', { exact: true }).fill('Platform delivery');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByLabel('Description', { exact: true }).fill('Delivery planning and releases');
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  await expect(page.getByRole('heading', { name: 'Platform delivery', level: 1 })).toBeVisible();
  await page.locator('.nav-board').click();
  await expect(page).toHaveURL(/\/board\/brd_/);
  await page.locator('.nav-project-settings').click();
  await page.getByLabel('Name', { exact: true }).fill('Platform engineering');
  await page.getByLabel('Project URL').fill('https://example.test/platform');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Project details saved.' })).toBeVisible();
  const response = await page.request.get(`/rest/api/3/project/${key}`);
  expect(response.status()).toBe(200);
  expect(await response.json()).toMatchObject({ key, name: 'Platform engineering', url: 'https://example.test/platform', description: 'Delivery planning and releases' });

  await page.goto('/projects/new');
  await page.getByLabel('Name', { exact: true }).fill('Duplicate project');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('A project with this key already exists');
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Duplicate project');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
