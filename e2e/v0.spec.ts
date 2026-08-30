import { test, expect, Page } from '@playwright/test';

import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

// The REST contract authenticates with email + API token (never the password).
function apiAuthHeader(): string {
  const tokenFile = path.join(__dirname, '..', 'data', 'seed-tokens.json');
  const tokens = JSON.parse(fs.readFileSync(tokenFile, 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  if (!token) throw new Error('no API token: run `go run ./cmd/server -mode=seed`');
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}



test('V0 station 4: Jira API contract smoke (serverInfo + create via REST)', async ({ request }) => {
  const info = await request.get('/rest/api/3/serverInfo');
  expect(info.ok()).toBeTruthy();
  expect((await info.json()).product).toBe('ZZIRA');

  const created = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      fields: {
        project: { key: 'ZZ' },
        summary: `E2E API issue ${Date.now()}`,
        issuetype: { name: 'Task' },
      },
    },
  });
  expect(created.status()).toBe(201);
  const body = await created.json();
  expect(body.key).toMatch(/^ZZ-\d+$/);
});

test('V0 station 5: login → create via UI → lands on issue view', async ({ page }) => {
  await login(page);
  await page.click('text=Create');
  const summary = `E2E UI issue ${Date.now()}`;
  await page.fill('input[name=summary]', summary);
  await page.click('.modal button[type=submit]');
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  await expect(page.locator('.issue-summary')).toHaveText(summary);
});

test('V0 station 6: wasm worker boots and syncs (banner)', async ({ page }) => {
  await login(page);
  await page.goto('/');
  const banner = page.locator('#sync-banner');
  await expect
    .poll(async () => (await banner.textContent()) ?? '', { timeout: 15_000 })
    .toContain('local sync ready');
});

test('V0 done-when: offline reload still renders the issue from local SQLite', async ({ page }) => {
  await login(page);
  // Visit an issue so the service worker caches the shell and the replica has data.
  await page.goto('/issues/ZZ');
  await page.click('tbody a >> nth=0');
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  const summary = await page.locator('.issue-summary').textContent();
  expect(summary).toBeTruthy();

  // Give the worker one sync cycle, then cut the network and reload.
  await page.waitForTimeout(4_000);
  await page.context().setOffline(true);
  await page.reload();
  await expect(page.locator('.issue-summary')).toHaveText(summary!);
  await page.context().setOffline(false);
});

test('V0 done-when: two browsers converge through the action log', async ({ browser }) => {
  const a = await browser.newContext();
  const b = await browser.newContext();
  const pa = await a.newPage();
  const pb = await b.newPage();

  await login(pa);
  await login(pb);
  await pb.goto('/issues/ZZ');

  // A creates an issue through the REST contract edge.
  const created = await pa.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      fields: { project: { key: 'ZZ' }, summary: `Converge ${Date.now()}`, issuetype: { name: 'Task' } },
    },
  });
  const { key } = await created.json();

  // B sees it after a sync cycle: the replica replays the tail, a reload
  // re-renders the navigator from the fresh server list.
  await expect
    .poll(async () => {
      await pb.reload();
      return pb.locator(`text=${key}`).count();
    }, { timeout: 15_000 })
    .toBeGreaterThan(0);

  await a.close();
  await b.close();
});
