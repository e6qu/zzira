import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkAccessibility(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('wiki content tree holds pages beneath folders, and folders move, rename, archive and restore', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const stamp = Date.now().toString(36).toUpperCase();
  const guide = `Guide ${stamp}`;
  const folder = `Runbooks ${stamp}`;
  const renamed = `Runbook archive ${stamp}`;
  const rollback = `Rollback ${stamp}`;

  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  await page.getByLabel('Space name').fill(`Tree ${stamp}`);
  await page.getByLabel('Space key').fill(`T${stamp}`);
  await page.getByLabel('Description', { exact: true }).fill('Content tree journey');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();

  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await page.getByLabel('Page title').fill(guide);
  await page.getByRole('textbox', { name: 'Page content' }).fill('The guide.');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: guide, level: 1 })).toBeVisible();

  await page.goto(spaceURL);
  const folders = page.locator('.wiki-folders');
  await folders.locator('summary').filter({ hasText: 'Create folder' }).click();
  await folders.getByLabel('Folder name').fill(folder);
  await folders.getByLabel('Parent content').selectOption({ label: guide });
  await folders.getByRole('button', { name: 'Create folder', exact: true }).click();
  await expect(page.locator('.wiki-folders li').filter({ hasText: folder })).toContainText(`Inside page ${guide}`);

  // A page can be filed beneath the folder.
  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await page.getByLabel('Page title').fill(rollback);
  await page.getByRole('textbox', { name: 'Page content' }).fill('Roll back the release.');
  await page.getByLabel('Parent', { exact: true }).selectOption({ label: `${folder} · Folder` });
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: rollback, level: 1 })).toBeVisible();

  await page.goto(spaceURL);
  const tree = page.getByRole('region', { name: 'Page tree' });
  const row = (title: string) => tree.locator(`xpath=.//li[div[normalize-space((.//a|.//strong)[1])="${title}"]]`);
  await expect(row(guide)).toHaveClass(/wiki-tree-depth-0/);
  await expect(row(folder)).toHaveClass(/wiki-tree-depth-1/);
  await expect(row(folder)).toContainText('Folder');
  await expect(row(rollback)).toHaveClass(/wiki-tree-depth-2/);
  await checkAccessibility(page);

  // Rename the folder, then move it to the top level of the space.
  await row(folder).locator('summary').click();
  await row(folder).getByLabel('Name').fill(renamed);
  await row(folder).getByRole('button', { name: 'Rename', exact: true }).click();
  await expect(row(renamed)).toHaveClass(/wiki-tree-depth-1/);
  await row(renamed).locator('summary').click();
  await row(renamed).getByLabel('Move to').selectOption({ label: 'Top level of this space' });
  await row(renamed).getByRole('button', { name: 'Move', exact: true }).click();
  await expect(row(renamed)).toHaveClass(/wiki-tree-depth-0/);
  await expect(row(rollback)).toHaveClass(/wiki-tree-depth-1/);

  // Archive the folder with what is inside it, find both in the Archived
  // list, and restore them together.
  await row(renamed).locator('summary').click();
  await row(renamed).getByLabel('Archive the content inside it too').check();
  await row(renamed).getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(page).toHaveURL(/status=archived/);
  await expect(row(renamed)).toContainText('archived');
  await expect(row(rollback)).toHaveClass(/wiki-tree-depth-1/);
  await page.goto(spaceURL);
  await expect(row(renamed)).toHaveCount(0);
  await expect(row(rollback)).toHaveCount(0);
  await page.goto(`${spaceURL}?status=archived`);
  await row(renamed).locator('summary').click();
  await row(renamed).getByLabel('Restore the archived content inside it too').check();
  await row(renamed).getByRole('button', { name: 'Restore', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`${spaceURL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(#.*)?$`));
  await expect(row(renamed)).toHaveClass(/wiki-tree-depth-0/);
  await expect(row(rollback)).toHaveClass(/wiki-tree-depth-1/);
});
