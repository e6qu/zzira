import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkAccessibility(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('wiki drafts of published pages, discarding them, and purging trashed pages', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const stamp = Date.now().toString(36).toUpperCase();
  const title = `Runbook ${stamp}`;

  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  await page.getByLabel('Space name').fill(`Drafts ${stamp}`);
  await page.getByLabel('Space key').fill(`R${stamp}`);
  await page.getByLabel('Description', { exact: true }).fill('Draft journey');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();

  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await page.getByLabel('Page title').fill(title);
  await page.getByRole('textbox', { name: 'Page content' }).fill('Published steps.');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  const pageURL = page.url();

  // Saving as a draft leaves the published page as readers see it.
  await page.getByRole('link', { name: 'Edit page', exact: true }).click();
  await page.getByRole('textbox', { name: 'Page content' }).fill('Draft steps, not yet published.');
  await page.getByRole('button', { name: 'Save as draft', exact: true }).click();
  await expect(page).toHaveURL(pageURL);
  await expect(page.getByRole('article', { name: 'Page content' })).toContainText('Published steps.');
  await expect(page.getByRole('status', { name: 'Unpublished draft' })).toContainText('There is an unpublished draft of this page.');
  await checkAccessibility(page);

  // Editing picks the draft up, and discarding it removes it.
  await page.getByRole('link', { name: 'Edit draft', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'You are editing the unpublished draft' })).toBeVisible();
  await expect(page.locator('#wiki-body')).toHaveValue(/Draft steps, not yet published\./);
  await page.goto(pageURL);
  await page.getByRole('button', { name: 'Discard draft', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Unpublished draft' })).toHaveCount(0);
  await expect(page.getByRole('article', { name: 'Page content' })).toContainText('Published steps.');

  // A trashed page can be purged by a space administrator.
  await page.getByText('Move to trash', { exact: true }).click();
  await page.getByRole('button', { name: 'Confirm move to trash', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+/);
  await page.goto(`${spaceURL}?status=trashed`);
  await page.getByRole('region', { name: 'Page tree' }).getByRole('link', { name: title, exact: true }).click();
  await page.locator('.wiki-purge > summary').click();
  await page.getByRole('button', { name: 'Confirm purge', exact: true }).click();
  await expect(page).toHaveURL(/status=trashed/);
  await expect(page.getByRole('region', { name: 'Page tree' }).getByRole('link', { name: title, exact: true })).toHaveCount(0);
});
