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

test('watching a wiki blog post brings its updates and comments to the watcher', async ({ browser }) => {
  const demo = await browser.newPage();
  await login(demo, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  const title = `Weekly ${stamp}`;

  await demo.goto('/wiki');
  await demo.locator('.wiki-create-space > summary').click();
  await demo.getByLabel('Space name').fill(`Watches ${stamp}`);
  await demo.getByLabel('Space key').fill(`W${stamp}`);
  await demo.getByLabel('Description', { exact: true }).fill('Watch journey');
  await demo.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(demo).toHaveURL(/\/wiki\/spaces\/\d+$/);
  await demo.goto(`${demo.url()}/blogposts/new`);
  await demo.getByLabel('Title').fill(title);
  await demo.getByLabel('Content').fill('<p>First update</p>');
  await demo.getByRole('button', { name: 'Create blog post', exact: true }).click();
  await expect(demo.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  const postURL = demo.url().split('?')[0];

  // Someone else watches the post.
  const ana = await browser.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await ana.goto(postURL);
  await ana.getByRole('button', { name: 'Watch blog post', exact: true }).click();
  await expect(ana.getByRole('button', { name: 'Stop watching blog post', exact: true })).toBeVisible();
  await checkAccessibility(ana);

  // The author's update reaches the watcher, and leads back to the post.
  await demo.goto(`${postURL}?edit=true`);
  await demo.getByLabel('Content').fill('<p>Second update</p>');
  await demo.getByRole('button', { name: 'Save blog post', exact: true }).click();
  await expect(demo.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  await ana.goto('/notifications');
  const update = ana.locator('.notification-inbox-item', { hasText: `Updated blog post "${title}".` });
  await expect(update).toContainText('Watched work');
  await update.locator('.notification-open').click();
  await expect(ana).toHaveURL(postURL);

  // Stopping watching stops the notifications.
  await ana.getByRole('button', { name: 'Stop watching blog post', exact: true }).click();
  await expect(ana.getByRole('button', { name: 'Watch blog post', exact: true })).toBeVisible();
});
