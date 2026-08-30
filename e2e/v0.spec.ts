import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}


// Serial suite: one shared browser context boots the wasm worker ONCE for the
// whole file (the expensive part). Tests build on the shared logged-in state.
test.describe.configure({ mode: 'serial' });

let browser: any;
let context: any;
let page: Page;

test.beforeAll(async ({ browser: b }) => {
  browser = b;
  context = await browser.newContext();
  page = await context.newPage();
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
});

test.afterAll(async () => {
  await context?.close();
});

test('V0 station 4: Jira API contract smoke (serverInfo + create via REST)', async ({ request }) => {
  const info = await request.get('/rest/api/3/serverInfo');
  expect(info.ok()).toBeTruthy();
  expect((await info.json()).product).toBe('ZZIRA');

  const created = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `E2E API issue ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const body = await created.json();
  expect(body.key).toMatch(/^ZZ-\d+$/);
});

test('V0 station 5: login → create via UI → lands on issue view', async () => {
  await page.click('text=Create');
  await expect(page.locator('.modal input[name=summary]')).toBeVisible({ timeout: 15_000 });
  const summary = `E2E UI issue ${Date.now()}`;
  await page.fill('input[name=summary]', summary);
  await page.click('.modal button[type=submit]');
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  await expect(page.locator('.issue-summary')).toHaveText(summary);
});

test('V0 station 6: wasm worker boots and syncs (banner)', async () => {
  await page.goto('/');
  const banner = page.locator('#sync-banner');
  await expect
    .poll(async () => (await banner.textContent()) ?? '', { timeout: 45_000 })
    .toContain('local sync ready');
});

test('V0 done-when: offline reload still renders the issue from local SQLite', async () => {
  await page.goto('/issues/ZZ');
  await page.click('tbody a >> nth=0');
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  const summary = await page.locator('.issue-summary').textContent();
  expect(summary).toBeTruthy();

  // wait for one sync cycle, then cut the network and reload
  const banner = page.locator('#sync-banner');
  await expect
    .poll(async () => (await banner.textContent()) ?? '', { timeout: 20_000 })
    .toContain('synced');
  await context.setOffline(true);
  await page.reload();
  await expect(page.locator('.issue-summary')).toHaveText(summary!);
  await context.setOffline(false);
});

test('V0 done-when: two browsers converge through the action log', async ({ browser: b }) => {
  const ctxA = await b.newContext();
  const ctxB = await b.newContext();
  const pa = await ctxA.newPage();
  const pb = await ctxB.newPage();

  for (const p of [pa, pb]) {
    await p.goto('/login');
    await p.fill('input[name=email]', DEMO.email);
    await p.fill('input[name=password]', DEMO.password);
    await p.click('button[type=submit]');
    await expect(p).toHaveURL('/');
  }
  await pb.goto('/issues/ZZ');

  // A creates an issue through the REST contract edge.
  const created = await pa.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `Converge ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  const { key } = await created.json();

  // B sees it after a sync cycle: the replica replays the tail, a reload
  // re-renders the navigator from the fresh server list.
  await expect
    .poll(async () => {
      await pb.reload();
      return pb.locator(`text=${key}`).count();
    }, { timeout: 30_000 })
    .toBeGreaterThan(0);

  await ctxA.close();
  await ctxB.close();
});