import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkAccessibility(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

async function createSpace(page: Page, name: string, key: string) {
  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  await page.getByLabel('Space name').fill(name);
  await page.getByLabel('Space key').fill(key);
  await page.getByLabel('Description', { exact: true }).fill(`${name} pages`);
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  return page.url();
}

async function createPage(page: Page, from: string, link: string, title: string) {
  await page.goto(from);
  await page.getByRole('link', { name: link, exact: true }).click();
  await page.getByLabel('Page title').fill(title);
  await page.getByRole('textbox', { name: 'Page content' }).fill(`${title} content.`);
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  return page.url();
}

test('wiki pages archive, restore and move to another space', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const stamp = Date.now().toString(36).toUpperCase();
  const guide = `Guide ${stamp}`;
  const step = `Step ${stamp}`;
  const landing = `Landing ${stamp}`;

  const sourceURL = await createSpace(page, `Archive source ${stamp}`, `A${stamp}`);
  const guideURL = await createPage(page, sourceURL, 'Create page', guide);
  const stepURL = await createPage(page, guideURL, 'Create child page', step);
  const destinationURL = await createSpace(page, `Move destination ${stamp}`, `B${stamp}`);
  await createPage(page, destinationURL, 'Create page', landing);

  // Archiving the guide on its own leaves the step in the tree, a level up.
  await page.goto(guideURL);
  await page.locator('.wiki-archive > summary').click();
  await expect(page.getByLabel('Archive child pages too')).toBeVisible();
  await page.getByRole('button', { name: 'Confirm archive', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Archived page' })).toContainText('This page is archived.');
  await expect(page.getByRole('link', { name: 'Edit page', exact: true })).toHaveCount(0);
  await checkAccessibility(page);
  await page.goto(sourceURL);
  const tree = page.getByRole('region', { name: 'Page tree' });
  await expect(tree.getByRole('link', { name: step, exact: true })).toBeVisible();
  await expect(tree.getByRole('link', { name: guide, exact: true })).toHaveCount(0);
  await page.getByRole('link', { name: 'Archived', exact: true }).click();
  await expect(page).toHaveURL(/status=archived/);
  await expect(page.getByRole('region', { name: 'Page tree' }).getByRole('link', { name: guide, exact: true })).toBeVisible();

  // Restoring brings it back and makes it editable again.
  await page.getByRole('region', { name: 'Page tree' }).getByRole('link', { name: guide, exact: true }).click();
  await page.locator('.wiki-restore > summary').click();
  await page.getByRole('button', { name: 'Confirm restore from archive', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Archived page' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Edit page', exact: true })).toBeVisible();

  // Moving the step to another space follows it there.
  await page.goto(stepURL);
  await page.locator('.wiki-move > summary').click();
  await page.getByLabel('Target page').selectOption({ label: landing });
  await page.getByLabel('Position').selectOption('append');
  await page.locator('.wiki-move').getByRole('button', { name: 'Move page', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`^${destinationURL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/pages/\\d+$`));
  await expect(page.getByRole('heading', { name: step, level: 1 })).toBeVisible();
  await page.goto(destinationURL);
  await expect(page.getByRole('region', { name: 'Page tree' }).getByRole('link', { name: step, exact: true })).toBeVisible();
  await page.goto(sourceURL);
  await expect(page.getByRole('region', { name: 'Page tree' }).getByRole('link', { name: step, exact: true })).toHaveCount(0);
});
