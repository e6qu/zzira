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

test('V2: JQL search via navigator finds a fresh issue', async ({ page, request }) => {
  const marker = `jqlmarker${Date.now()}`;
  const res = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `issue with ${marker}`, issuetype: { name: 'Task' } } },
  });
  expect(res.status()).toBe(201);
  const key = (await res.json()).key;

  await login(page);
  await page.goto(`/issues/ZZ?jql=${encodeURIComponent(`summary ~ "${marker}" ORDER BY key DESC`)}`);
  await expect(page.locator('.jql-meta')).toContainText('1 result');
  await expect(page.locator(`a:has-text("${key}")`)).toBeVisible();
});

test('V2: malformed JQL surfaces the server error inline', async ({ page }) => {
  await login(page);
  await page.goto(`/issues/ZZ?jql=${encodeURIComponent('bogus = 1')}`);
  await expect(page.locator('.jql-meta')).toContainText('field does not exist');
});

test('V2: custom field created via API is fillable in the UI and filterable', async ({ page, request }) => {
  const cfName = `E2E Points ${Date.now()}`;
  const created = await request.post('/rest/api/3/field', {
    headers: { Authorization: apiAuthHeader() },
    data: { name: cfName, type: 'text', description: 'e2e' },
  });
  expect(created.status()).toBe(201);
  const cf = (await created.json()).id;

  const key = await (async () => {
    const res = await request.post('/rest/api/3/issue', {
      headers: { Authorization: apiAuthHeader() },
      data: { fields: { project: { key: 'ZZ' }, summary: `CF issue ${Date.now()}`, issuetype: { name: 'Task' } } },
    });
    return (await res.json()).key;
  })();

  await login(page);
  await page.goto(`/browse/${key}`);
  await page.click('text=Edit');
  await expect(page.locator(`input[name="${cf}"]`)).toBeVisible();
  await page.fill(`input[name="${cf}"]`, '42');
  await page.click('.modal button[type=submit]');
  await expect(page.locator('.issue-view')).toBeVisible();

  // filterable via JQL
  await expect.poll(async () => {
    const r = await request.get('/rest/api/3/search', {
      headers: { Authorization: apiAuthHeader() },
      params: { jql: `${cf} = "42"` },
    });
    return (await r.json()).total;
  }, { timeout: 10_000 }).toBe(1);
});
