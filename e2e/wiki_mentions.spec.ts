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

test('wiki mentions from the editor and comments notify the people mentioned', async ({ browser }) => {
  const demo = await browser.newPage();
  await login(demo, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  const title = `Handover ${stamp}`;

  await demo.goto('/wiki');
  await demo.locator('.wiki-create-space > summary').click();
  await demo.getByLabel('Space name').fill(`Mentions ${stamp}`);
  await demo.getByLabel('Space key').fill(`M${stamp}`);
  await demo.getByLabel('Description', { exact: true }).fill('Mention journey');
  await demo.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(demo).toHaveURL(/\/wiki\/spaces\/\d+$/);

  // Typing @ in the editor offers people; choosing one writes a mention.
  await demo.getByRole('link', { name: 'Create page', exact: true }).click();
  await demo.getByLabel('Page title').fill(title);
  const editor = demo.getByRole('textbox', { name: 'Page content' });
  await editor.click();
  await demo.keyboard.type('Over to @Ana');
  const people = demo.getByRole('listbox', { name: 'People to mention' });
  await expect(people.getByRole('option', { name: 'Ana Soursop' })).toBeVisible();
  await checkAccessibility(demo);
  await demo.keyboard.press('Enter');
  await expect(people).toBeHidden();
  await demo.getByRole('button', { name: 'Save page', exact: true }).click();
  const content = demo.getByRole('article', { name: 'Page content' });
  await expect(content.getByRole('link', { name: '@Ana Soursop' })).toBeVisible();
  const pageURL = demo.url();

  // Comment boxes offer the same people and write storage mentions.
  const comment = demo.locator('#wiki-new-comment');
  await comment.click();
  await demo.keyboard.type('Thanks @Ana');
  await people.getByRole('option', { name: 'Ana Soursop' }).click();
  await expect(comment).toHaveValue(/ri:user ri:account-id=/);
  await comment.press('End');
  await demo.keyboard.type('for the review.');
  await comment.locator('xpath=ancestor::form').getByRole('button', { name: 'Comment', exact: true }).click();
  await expect(demo.locator('.wiki-comment-list').getByRole('link', { name: '@Ana Soursop' })).toBeVisible();

  // The person mentioned hears about it, and the notification leads back.
  const ana = await browser.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await ana.goto('/notifications');
  const mention = ana.locator('.notification-inbox-item', { hasText: title }).first();
  await expect(mention).toContainText('Mention');
  await checkAccessibility(ana);
  await mention.locator('.notification-open').click();
  await expect(ana).toHaveURL(pageURL);
});
