import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

// Writing a page with the editor's own controls: headings, a quote, a code
// block, a link and a table, and reading them back on the page and in the
// storage the site keeps.

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a writer formats a page with the editor rather than by typing storage', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const stamp = Date.now().toString(36).toUpperCase();

  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  await page.getByLabel('Space name').fill(`Editor ${stamp}`);
  await page.getByLabel('Space key').fill(`E${stamp}`);
  await page.getByLabel('Description', { exact: true }).fill('Editor journey');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();

  await page.goto(`${spaceURL}/pages/new`);
  await page.getByLabel('Page title').fill(`Runbook ${stamp}`);
  const editor = page.locator('[data-wiki-editor]');
  await expect(editor).toBeVisible();
  const toolbar = page.locator('[data-wiki-toolbar]');
  await accessible(page);

  // A heading, then a paragraph, then a quote, then a code block: each is the
  // block the caret is in, changed to another.
  await editor.click();
  await page.keyboard.type('Restarting the queue');
  await toolbar.getByLabel('Style').selectOption('h2');
  await page.keyboard.press('Enter');
  await page.keyboard.type('Drain traffic first.');
  await page.keyboard.press('Enter');
  await page.keyboard.type('Nobody restarts on a Friday.');
  await toolbar.getByLabel('Style').selectOption('blockquote');
  await editor.click();
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  await page.keyboard.type('systemctl restart queue');
  await toolbar.getByLabel('Style').selectOption('pre');

  // A link, from the address in the toolbar.
  await editor.click();
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  await toolbar.getByLabel('Link address').fill('https://runbooks.example/queue');
  await toolbar.getByRole('button', { name: 'Add link' }).click();

  // A table arrives with a header row and a row to type into.
  await editor.click();
  await page.keyboard.press('End');
  await toolbar.getByRole('button', { name: 'Table' }).click();
  await expect(editor.locator('table th')).toHaveCount(2);

  await page.getByRole('button', { name: 'Save page' }).click();
  await expect(page.getByRole('heading', { name: `Runbook ${stamp}`, level: 1 })).toBeVisible();
  const article = page.getByRole('article', { name: 'Page content' });
  await expect(article.locator('h2')).toContainText('Restarting the queue');
  await expect(article.locator('blockquote')).toContainText('Nobody restarts on a Friday');
  await expect(article.locator('pre')).toContainText('systemctl restart queue');
  await expect(article.getByRole('link', { name: 'https://runbooks.example/queue' })).toBeVisible();
  await expect(article.locator('table th')).toHaveCount(2);
  await accessible(page);

  // The site kept it as storage, which is what the API answers with.
  const pageID = page.url().split('/').pop();
  // The page's own session answers for the API call, as it does for the page.
  const stored = await page.request.get(`/wiki/api/v2/pages/${pageID}?body-format=storage`);
  expect(stored.status()).toBe(200);
  const body = (await stored.json()).body.storage.value as string;
  expect(body).toContain('<h2>');
  expect(body).toContain('<blockquote>');
  expect(body).toContain('<pre>');
  expect(body).toContain('<table>');
  expect(body).toContain('href="https://runbooks.example/queue"');
});
