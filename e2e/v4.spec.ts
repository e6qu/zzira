import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('V4: board renders columns and rank-ordered cards', async ({ page, request }) => {
  await login(page);
  await page.goto('/board/brd_default');
  await expect(page.locator('.board-column')).toHaveCount(3);
  await expect(page.locator('.board-column-head .lozenge').first()).toHaveText('To Do');
  await expect(page.locator('.board-card').first()).toBeVisible();
});

test('V4 done-when: rank via API in browser A converges in browser B via poke', async ({ browser, request }) => {
  const pa = await browser.newContext();
  const pb = await browser.newContext();
  const pageA = await pa.newPage();
  const pageB = await pb.newPage();

  await login(pageA);
  await login(pageB);
  await pageA.goto('/board/brd_default');
  await pageB.goto('/board/brd_default');
  await expect(pageB.locator('.board-card').first()).toBeVisible();
  // Live convergence can only be asserted once B's local-first replica has
  // established its checkpoint; this is a protocol acknowledgement, not an
  // elapsed-time guess.
  await expect.poll(async () => (await pageB.locator('#sync-banner').textContent()) ?? '', {
    timeout: 20_000,
  }).toContain('synced');

  const firstCard = await pageA.locator('.board-column[data-status="st_todo"] .board-card').first();
  const topKey = await firstCard.getAttribute('data-key');
  // Move the last card rather than the adjacent card. That guarantees this
  // request changes the ordering even if a previous test leaves the first two
  // cards already adjacent in the requested order.
  const cards = pageA.locator('.board-column[data-status="st_todo"] .board-card');
  const lastKey = await cards.last().getAttribute('data-key');

  // Jira's rankBeforeIssue places the issue ahead of the referenced card.
  // Using rankAfterIssue here would deliberately preserve the existing order.
  const ranked = await request.post('/rest/agile/1.0/issue/rank', {
    headers: { Authorization: apiAuthHeader() },
    data: { issues: [lastKey], rankBeforeIssue: topKey },
  });
  expect(ranked.status()).toBe(204);

  // Browser B receives the poke and re-renders with the new order.
  await expect
    .poll(async () => {
      const keys = await pageB.locator('.board-column[data-status="st_todo"] .board-card').evaluateAll(
        cards => cards.map(c => c.getAttribute('data-key')),
      );
      return keys[0] === lastKey && keys.includes(topKey);
    }, { timeout: 30_000 })
    .toBe(true);

  await pa.close();
  await pb.close();
});
