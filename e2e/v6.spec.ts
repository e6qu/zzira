import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

// The dashboard shows a card for each status holding work, so the spec raises
// work in two statuses rather than relying on another spec having run.
test.beforeAll(async ({ request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const raise = async (summary: string) => {
    const created = await request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary: `${summary} ${Date.now()}`, issuetype: { name: 'Task' } } } });
    expect(created.status()).toBe(201);
    return (await created.json()).key as string;
  };
  await raise('Dashboard open work');
  const finished = await raise('Dashboard finished work');
  const transitions = (await (await request.get(`/rest/api/3/issue/${finished}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
  const done = transitions.find(candidate => candidate.to.statusCategory.key === 'done');
  expect(done).toBeTruthy();
  expect((await request.post(`/rest/api/3/issue/${finished}/transitions`, { headers, data: { transition: { id: done!.id } } })).status()).toBe(204);
});

test('V6: dashboard renders status counts and recent activity', async ({ page }) => {
  await login(page);
  await page.goto('/dashboard');
  const cards = await page.locator('.dash-card').count();
  expect(cards).toBeGreaterThanOrEqual(3); // To Do / In Progress / Done / My open
  await expect(page.locator('h2', { hasText: 'Recent activity' })).toBeVisible();
});
