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

test('board controls, settings, preview, and Agile configuration APIs are coherent', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const configuration = await request.get('/rest/agile/1.0/board/brd_default/configuration', { headers });
  expect(configuration.status()).toBe(200);
  const configurationBody = await configuration.json();
  expect(configurationBody.location.projectKey).toBe('ZZ');
  expect(configurationBody.columnConfig.columns).toHaveLength(3);
  expect(configurationBody.ranking.rankCustomFieldId).toBe('rank');

  const quickFilters = await request.get('/rest/agile/1.0/board/brd_default/quickfilter?startAt=0&maxResults=1', { headers });
  expect(quickFilters.status()).toBe(200);
  const quickFilterBody = await quickFilters.json();
  expect(quickFilterBody.total).toBeGreaterThanOrEqual(2);
  expect(quickFilterBody.values).toHaveLength(1);
  expect(quickFilterBody.isLast).toBe(false);
  const quickFilter = await request.get(`/rest/agile/1.0/board/brd_default/quickfilter/${quickFilterBody.values[0].id}`, { headers });
  expect(quickFilter.status()).toBe(200);

  await login(page);
  await page.goto('/board/brd_default');
  const firstCard = page.locator('.board-card').first();
  await expect(firstCard).toBeVisible();
  await firstCard.locator('.board-card-summary').click();
  await expect(page.locator('#board-preview .issue-preview-card')).toBeVisible();

  const mine = page.getByRole('link', { name: 'Only my work' });
  await mine.click();
  await expect(page).toHaveURL(/qf=qf_my/);
  await expect(mine).toHaveAttribute('aria-current', 'true');

  await page.goto('/board/brd_default/settings');
  await expect(page.getByRole('heading', { name: 'Configure ZZ board' })).toBeVisible();
  const rows = page.locator('[data-quick-filter-row]');
  const initialRows = await rows.count();
  await page.getByRole('button', { name: 'Add quick filter' }).click();
  await expect(rows).toHaveCount(initialRows + 1);
  await expect(rows.last().locator('input[name="quickFilterName"]')).toBeFocused();
  await rows.last().getByRole('button', { name: 'Remove' }).click();
  await expect(rows).toHaveCount(initialRows);
});

test('board controls remain available when Web Workers are unsupported', async ({ page }) => {
  await page.addInitScript(() => {
    delete (window as any).Worker;
  });
  await login(page);
  await page.goto('/board/brd_default');

  await expect(page.evaluate(() => typeof (window as any).zzira?.loadIssuePreview)).resolves.toBe('function');
  await page.locator('.board-card-summary').first().click();
  await expect(page.locator('#board-preview .issue-preview-card')).toBeVisible();
});

test('parsed controls queue actions until the deferred UI controller is ready', async ({ browser }) => {
  const context = await browser.newContext({
    baseURL: process.env.ZZIRA_URL || 'http://localhost:8080',
    serviceWorkers: 'block',
  });
  const page = await context.newPage();
  let releaseApp!: () => void;
  let appReleased = false;
  let holdApp = false;
  const appGate = new Promise<void>((resolve) => { releaseApp = resolve; });
  const release = () => {
    if (!appReleased) {
      appReleased = true;
      releaseApp();
    }
  };

  try {
    await page.route('**/static/app.js*', async (route) => {
      if (holdApp) await appGate;
      await route.continue();
    });
    await login(page);

    holdApp = true;
    const navigation = page.goto('/board/brd_default/backlog');
    const summary = page.locator('.backlog-summary').first();
    await summary.waitFor({ state: 'attached' });
    await expect(page.evaluate(() => Array.isArray((window as any).zzira?._pending))).resolves.toBe(true);
    await summary.evaluate((button: HTMLButtonElement) => button.click());
    await expect(page.evaluate(() => (window as any).zzira._pending.length)).resolves.toBe(1);

    release();
    await navigation;
    await expect(page.locator('#backlog-preview .issue-preview-card')).toBeVisible();
  } finally {
    release();
    await context.close();
  }
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
