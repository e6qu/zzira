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

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

test('wiki pages show who else is here and bring new comments to readers', async ({ browser }) => {
  test.setTimeout(120_000);
  const demo = await browser.newPage();
  await login(demo, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  const title = `Live plan ${stamp}`;
  await demo.goto('/wiki');
  await demo.locator('.wiki-create-space > summary').click();
  await demo.getByLabel('Space name').fill(`Live ${stamp}`);
  await demo.getByLabel('Space key').fill(`L${stamp}`);
  await demo.getByLabel('Description', { exact: true }).fill('Live journey');
  await demo.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(demo).toHaveURL(/\/wiki\/spaces\/\d+$/);
  await demo.getByRole('link', { name: 'Create page', exact: true }).click();
  await demo.getByLabel('Page title').fill(title);
  await demo.getByRole('textbox', { name: 'Page content' }).fill('Plan.');
  await demo.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(demo.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  const pageURL = demo.url();

  // Someone editing is seen by someone reading.
  await demo.getByRole('link', { name: 'Edit page', exact: true }).click();
  await expect(demo.getByRole('textbox', { name: 'Page content' })).toBeVisible();
  const ana = await browser.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await ana.goto(pageURL);
  const live = ana.locator('[data-wiki-live]');
  await expect(live).toContainText('Also here: Demo User (editing)', { timeout: 20_000 });
  await checkAccessibility(ana);

  // A comment added elsewhere is announced to the reader.
  await demo.goto(pageURL);
  const comment = demo.locator('#wiki-new-comment');
  await comment.fill('<p>Looks good.</p>');
  await comment.locator('xpath=ancestor::form').getByRole('button', { name: 'Comment', exact: true }).click();
  await expect(live).toContainText('New comments have been added.', { timeout: 30_000 });
});
