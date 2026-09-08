import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkAccessibility(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

test('knowledge collaborator builds and edits a connected whiteboard', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  const suffix = Date.now().toString(36).toUpperCase();
  await page.getByLabel('Space name').fill(`Visual planning ${suffix}`);
  await page.getByLabel('Space key').fill(`V${suffix}`);
  await page.getByLabel('Description', { exact: true }).fill('Connected delivery maps');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  const whiteboards = page.getByRole('region', { name: 'Whiteboards' });
  await whiteboards.locator('summary').filter({ hasText: 'Create whiteboard' }).click();
  await whiteboards.getByLabel('Whiteboard name').fill('Release flow');
  await whiteboards.getByLabel('Template', { exact: true }).selectOption('flow-chart');
  await whiteboards.getByLabel('Template language', { exact: true }).selectOption('en-US');
  await whiteboards.getByRole('button', { name: 'Create whiteboard', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+\/whiteboards\/\d+$/);

  async function addObject(title: string, type: string, color: string, x: string) {
    await page.locator('summary').filter({ hasText: 'Add object' }).click();
    const form = page.locator('details[open]').filter({ hasText: 'Add object' }).locator('form');
    await form.getByLabel('Type', { exact: true }).selectOption(type);
    await form.getByLabel('Title', { exact: true }).fill(title);
    await form.getByLabel('Body', { exact: true }).fill(`${title} details`);
    await form.getByLabel('Color', { exact: true }).selectOption(color);
    await form.locator('[name="x"]').fill(x);
    await form.getByRole('button', { name: 'Add object', exact: true }).click();
  }

  await addObject('Build', 'sticky', 'yellow', '100');
  await addObject('Deploy', 'shape', 'green', '520');
  await expect(page.getByRole('img', { name: 'Release flow visual map' })).toBeVisible();
  await expect(page.locator('.wiki-canvas-object')).toHaveCount(2);

  await page.locator('summary').filter({ hasText: 'Add connector' }).click();
  await page.getByLabel('From', { exact: true }).selectOption({ label: 'Build' });
  await page.getByLabel('To', { exact: true }).selectOption({ label: 'Deploy' });
  await page.getByLabel('Label', { exact: true }).fill('ships');
  await page.getByLabel('Line style', { exact: true }).selectOption('dashed');
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  await expect(page.locator('.wiki-connector-dashed')).toHaveCount(1);
  await expect(page.getByRole('region', { name: 'Connectors' })).toContainText('ships');

  const build = page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Build"]') });
  await build.locator('[name="x"]').fill('180');
  await build.getByRole('button', { name: 'Save Build', exact: true }).click();
  await expect(page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Build"]') }).locator('[name="x"]')).toHaveValue('180');

  await checkAccessibility(page);
  await page.locator('[data-theme-toggle]').click();
  await checkAccessibility(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});
