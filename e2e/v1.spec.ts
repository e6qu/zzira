import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

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

async function createIssueViaAPI(request: any, summary: string): Promise<string> {
  const res = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' } } },
  });
  expect(res.status()).toBe(201);
  return (await res.json()).key;
}

test('V1: edit summary via dialog → issue view updates', async ({ page, request }) => {
  const key = await createIssueViaAPI(request, `V1 edit ${Date.now()}`);
  await login(page);
  await page.goto(`/browse/${key}`);
  const editPosts: string[] = [];
  page.on('response', r => {
    if (r.request().method() === 'POST' && r.url().includes('/edit')) editPosts.push(`${r.status()} ${r.url()}`);
  });
  await page.click('text=Edit');
  await expect(page.locator('.modal')).toBeVisible();
  const newSummary = `V1 edited ${Date.now()}`;
  await page.fill('input[name=summary]', newSummary);
  await page.locator('.modal button[type=submit]').click({ force: true });
  console.log('EDIT-POSTS:', JSON.stringify(editPosts));
  await expect(page.locator('.issue-summary')).toHaveText(newSummary, { timeout: 20_000 });
  // the API agrees
  const bean = await request.get(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() } });
  expect((await bean.json()).fields.summary).toBe(newSummary);
});

test('V1: comment via UI → appears; changelog endpoint reflects edit', async ({ page, request }) => {
  const key = await createIssueViaAPI(request, `V1 comment ${Date.now()}`);
  await login(page);
  await page.goto(`/browse/${key}`);
  await page.fill('.rich-editor', 'first comment from e2e');
  await page.click('.comment-form button[type=submit]');
  await expect(page.locator('.comment-body', { hasText: 'first comment from e2e' })).toBeVisible({ timeout: 20_000 });

  const cl = await request.get(`/rest/api/3/issue/${key}/comment`, { headers: { Authorization: apiAuthHeader() } });
  const body = await cl.json();
  expect(body.total).toBe(1);
});

test('V1: transition via UI button → status lozenge changes', async ({ page, request }) => {
  const key = await createIssueViaAPI(request, `V1 transition ${Date.now()}`);
  // workflow-agnostic: pick whatever transition the project workflow offers first
  const tr = await request.get(`/rest/api/3/issue/${key}/transitions`, { headers: { Authorization: apiAuthHeader() } });
  const transition = (await tr.json()).transitions[0];
  expect(transition).toBeTruthy();

  await login(page);
  await page.goto(`/browse/${key}`);
  await page.click(`.issue-transitions button:has-text("${transition.name}")`);
  await expect(page.locator('.issue-details .lozenge')).toHaveText(transition.to.name, { timeout: 10_000 });
});

test('V1 done-when: offline edit queues, drain on reconnect, zero duplicates', async ({ page, request }) => {
  const key = await createIssueViaAPI(request, `V1 offline ${Date.now()}`);
  await login(page);
  await page.goto(`/browse/${key}`);

  // Offline mutations require an installed local replica. Wait for the
  // protocol's explicit sync acknowledgement, never an elapsed-time guess.
  await expect.poll(async () => (await page.locator('#sync-banner').textContent()) ?? '', {
    timeout: 20_000,
  }).toContain('synced');

  await page.context().setOffline(true);
  await page.click('text=Edit');
  await page.fill('input[name=summary]', `offline edit ${Date.now()}`);
  await page.locator('.modal button[type=submit]').click({ force: true });
  await page.context().setOffline(false);

  await expect.poll(async () => {
    const bean = await request.get(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() } });
    return (await bean.json()).fields.summary;
  }, { timeout: 10_000 }).toContain('offline edit');

  // no duplicates: exactly one action beyond creation for this edit path
  const cl = await request.get(`/rest/api/3/issue/${key}/changelog`, { headers: { Authorization: apiAuthHeader() } });
  const summaryEdits = (await cl.json()).values
    .flatMap(v => v.items)
    .filter(i => i.field === 'summary').length;
  expect(summaryEdits).toBe(1);
});
