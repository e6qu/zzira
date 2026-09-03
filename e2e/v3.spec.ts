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

async function createIssue(page: Page, request: any, summary: string): Promise<string> {
  const res = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' } } },
  });
  expect(res.status()).toBe(201);
  return (await res.json()).key;
}

test('V3: attachment upload via UI → listed → content round-trips', async ({ page, request }) => {
  const key = await createIssue(page, request, `V3 attachment ${Date.now()}`);
  await login(page);
  await page.goto(`/browse/${key}`);

  const payload = `attachment content ${Date.now()}`;
  await page.setInputFiles('.upload-form input[type=file]', {
    name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from(payload),
  });
  await page.click('.upload-form button[type=submit]');
  await expect(page.locator('.attachment', { hasText: 'notes.txt' })).toBeVisible({ timeout: 10_000 });

  // download through the content endpoint and verify the bytes
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.click('.attachment a'),
  ]);
  const downloaded = await download.path();
  expect(fs.readFileSync(downloaded!, 'utf8')).toBe(payload);

  const contentPath = await page.locator('.attachment a').getAttribute('href');
  const attachmentId = contentPath!.split('/').pop()!;
  const deleted = await request.delete(`/rest/api/3/attachment/${attachmentId}`, {
    headers: { Authorization: apiAuthHeader() },
  });
  expect(deleted.status()).toBe(204);
  expect((await request.get(contentPath!, {
    headers: { Authorization: apiAuthHeader() },
  })).status()).toBe(404);
});

test('V3: worklog via UI → listed with rendered time', async ({ page, request }) => {
  const key = await createIssue(page, request, `V3 worklog ${Date.now()}`);
  await login(page);
  await page.goto(`/browse/${key}`);
  await page.fill('.worklog-seconds', '3600');
  await page.fill('.worklog-comment', 'e2e worklog body');
  await page.click('.worklog-form button[type=submit]');
  await expect(page.locator('.worklog', { hasText: 'e2e worklog body' })).toBeHidden({ timeout: 10_000 });
  await page.getByRole('button', { name: /^Work log/ }).click();
  await expect(page.locator('.worklog', { hasText: 'e2e worklog body' })).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('.worklog .lozenge')).toHaveText('1h');
});
