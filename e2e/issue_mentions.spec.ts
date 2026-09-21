import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
const authFor = (email: string) => ({ Authorization: 'Basic ' + Buffer.from(`${email}:${tokens[email]}`).toString('base64') });

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so the page is
  // checked from the top rather than wherever it was scrolled.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('mentioning someone in a comment notifies them', async ({ page }) => {
  const demo = authFor('demo@zzira.dev');
  const ana = authFor('ana@zzira.dev');
  const anaAccount = (await (await page.request.get('/rest/api/3/myself', { headers: ana })).json());
  const created = await page.request.post('/rest/api/3/issue', { headers: demo, data: { fields: { project: { key: 'ZZ' }, summary: `Mention ${Date.now()}`, issuetype: { name: 'Task' } } } });
  expect(created.status()).toBe(201);
  const { key } = await created.json();

  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', tokens['demo@zzira.dev.password']);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto(`/browse/${key}`);

  const editor = page.getByRole('textbox', { name: 'Add a comment' });
  await editor.click();
  await page.keyboard.type(`Could you check this, @${anaAccount.displayName.slice(0, 3)}`);
  const people = page.getByRole('listbox', { name: 'People to mention' });
  await expect(people.getByRole('option', { name: anaAccount.displayName })).toHaveAttribute('aria-selected', 'true');
  await accessible(page);
  await page.keyboard.press('Enter');
  await expect(people).toBeHidden();
  await expect(editor.locator('.mention')).toHaveText(`@${anaAccount.displayName}`);
  await page.keyboard.type('thanks');
  await page.getByRole('button', { name: 'Add comment' }).click();
  await expect(page.locator('.comment-body .mention', { hasText: `@${anaAccount.displayName}` })).toBeVisible();

  const comments = await (await page.request.get(`/rest/api/3/issue/${key}/comment`, { headers: demo })).json();
  const paragraph = comments.comments.at(-1).body.content[0].content;
  expect(paragraph).toContainEqual({ type: 'mention', attrs: { id: anaAccount.accountId, text: `@${anaAccount.displayName}` } });

  const inbox = await (await page.request.get('/rest/zzira/1/notifications?limit=200', { headers: ana })).json();
  expect(inbox.notifications.find((item: any) => item.kind === 'issue_mentioned' && item.message === `mentioned you in a comment on ${key}`)).toBeTruthy();
});
