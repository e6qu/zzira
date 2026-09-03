import { expect, test, Page } from '@playwright/test';
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
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

async function createIssue(request: any, summary: string, labels: string[] = []) {
  const response = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary, labels, issuetype: { name: 'Task' } } },
  });
  expect(response.status()).toBe(201);
  return (await response.json()).key as string;
}

test('issue triage journey: inline fields, labels API, watchers, links, activity, and management actions', async ({ page, request }) => {
  page.on('dialog', (dialog) => dialog.accept());
  const marker = Date.now();
  const key = await createIssue(request, `Triage ${marker}`, ['api-label']);
  const linkedKey = await createIssue(request, `Linked ${marker}`);
  const auth = { Authorization: apiAuthHeader() };

  const initialBean = await request.get(`/rest/api/3/issue/${key}`, { headers: auth });
  expect((await initialBean.json()).fields.labels).toEqual(['api-label']);

  await login(page);
  await page.goto(`/browse/${key}`);

  await page.locator('.inline-summary-editor > summary').click();
  const summary = `Triage updated ${marker}`;
  await page.fill('#issue-summary-input', summary);
  await page.locator('.inline-summary-editor button[type=submit]').click();
  await expect(page.locator('.issue-summary')).toHaveText(summary);

  const labels = page.locator('#field-labels');
  await expect(labels).toHaveValue('api-label');
  await labels.fill('frontend, parity');
  await labels.locator('xpath=..').getByRole('button', { name: 'Save labels' }).click();
  await expect(page.locator('#field-labels')).toHaveValue('frontend, parity');
  await expect.poll(async () => {
    const updatedBean = await request.get(`/rest/api/3/issue/${key}`, { headers: auth });
    return (await updatedBean.json()).fields.labels;
  }).toEqual(['frontend', 'parity']);

  const watchButton = page.locator('.watch-button');
  await page.locator('.issue-summary').click();
  await page.keyboard.press('w');
  await expect(watchButton).toHaveAttribute('aria-pressed', 'true');
  const watchers = await request.get(`/rest/api/3/issue/${key}/watchers`, { headers: auth });
  const watcherBody = await watchers.json();
  expect(watcherBody.isWatching).toBe(true);
  expect(watcherBody.watchCount).toBe(1);

  await page.getByText('Link work item', { exact: true }).click();
  await page.fill('#link-issue', linkedKey);
  await page.locator('.link-create button[type=submit]').click();
  await expect(page.locator('.linked-work-list')).toContainText(linkedKey);

  await page.fill('.rich-editor', 'Unified activity comment');
  await page.locator('.comment-form button[type=submit]').click();
  await expect(page.locator('[data-activity-kind=comment]', { hasText: 'Unified activity comment' })).toBeVisible();

  await page.fill('#worklog-seconds', '3600');
  await page.fill('#worklog-comment', 'Triage verification');
  await page.locator('.worklog-form button[type=submit]').click();
  await expect(page.locator('[data-activity-kind=worklog]', { hasText: 'logged 1h' })).toBeVisible();

  await page.getByRole('button', { name: /^Comments/ }).click();
  await expect(page.locator('[data-activity-kind=comment]')).toBeVisible();
  await expect(page.locator('[data-activity-kind=worklog]')).toBeHidden();
  await page.getByRole('button', { name: /^All/ }).click();
  const sort = page.locator('[data-activity-sort]');
  await sort.click();
  await expect(sort).toHaveAttribute('aria-pressed', 'true');

  await page.setInputFiles('#attachment-file', {
    name: 'triage-note.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('triage attachment'),
  });
  await Promise.all([
    page.waitForURL(`/browse/${key}`),
    page.locator('.upload-form button[type=submit]').click(),
  ]);
  await expect(page.getByRole('link', { name: 'triage-note.txt' })).toBeVisible();
  await page.getByRole('button', { name: 'Delete attachment triage-note.txt' }).click();
  await expect(page.getByRole('link', { name: 'triage-note.txt' })).toHaveCount(0);

  await page.getByRole('button', { name: /^Delete work log/ }).click();
  await expect(page.locator('[data-activity-kind=worklog]')).toHaveCount(0);
  await page.getByRole('button', { name: `Remove link to ${linkedKey}` }).click();
  await expect(page.locator('.linked-work-list')).toHaveCount(0);

  const unwatch = await request.delete(`/rest/api/3/issue/${key}/watchers`, { headers: auth });
  expect(unwatch.status()).toBe(204);
});
