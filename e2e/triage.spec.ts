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

test('issue triage journey: inline fields, labels API, watchers, votes, links, activity, and management actions', async ({ page, request }) => {
  page.on('dialog', (dialog) => dialog.accept());
  const marker = Date.now();
  const key = await createIssue(request, `Triage ${marker}`, ['api-label']);
  const linkedKey = await createIssue(request, `Linked ${marker}`);
  const auth = { Authorization: apiAuthHeader() };

  const initialBean = await request.get(`/rest/api/3/issue/${key}`, { headers: auth });
  expect((await initialBean.json()).fields.labels).toEqual(['api-label']);

  await login(page);
  await page.goto(`/browse/${key}`);

  await expect(page.getByRole('button', { name: /^Comments/ })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('button', { name: /^All/ })).toHaveAttribute('aria-pressed', 'false');

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

  const due = page.locator('#field-duedate');
  await due.fill('2026-12-01');
  await due.locator('xpath=..').getByRole('button', { name: 'Save due date' }).click();
  await expect(page.locator('#field-duedate')).toHaveValue('2026-12-01');
  await expect.poll(async () => {
    const updatedBean = await request.get(`/rest/api/3/issue/${key}`, { headers: auth });
    return (await updatedBean.json()).fields.duedate;
  }).toBe('2026-12-01');

  // Creating the work item autowatched it for its creator.
  const watchButton = page.locator(`form[action="/issues/${key}/watch"] .watch-button`);
  await expect(watchButton).toHaveAttribute('aria-pressed', 'true');
  await page.locator('.issue-summary').click();
  await page.keyboard.press('w');
  await expect(watchButton).toHaveAttribute('aria-pressed', 'false');
  await page.locator('.issue-summary').click();
  await page.keyboard.press('w');
  await expect(watchButton).toHaveAttribute('aria-pressed', 'true');
  const watchers = await request.get(`/rest/api/3/issue/${key}/watchers`, { headers: auth });
  const watcherBody = await watchers.json();
  expect(watcherBody.isWatching).toBe(true);
  expect(watcherBody.watchCount).toBe(1);

  const voteButton = page.locator(`form[action="/issues/${key}/vote"] .watch-button`);
  await voteButton.click();
  await expect(voteButton).toHaveAttribute('aria-pressed', 'true');
  await expect(voteButton).toContainText('Voted 1');
  const votes = await request.get(`/rest/api/3/issue/${key}/votes`, { headers: auth });
  expect(votes.status()).toBe(200);
  const voteBody = await votes.json();
  expect(voteBody.hasVoted).toBe(true);
  expect(voteBody.votes).toBe(1);
  expect(voteBody.voters[0].emailAddress).toBe(DEMO.email);

  await page.getByText('Link work item', { exact: true }).click();
  await page.fill('#link-issue', linkedKey);
  const linkedWork = page.locator('.linked-work').filter({ has: page.getByRole('heading', { name: /Linked work/ }) });
  await linkedWork.getByRole('button', { name: 'Link', exact: true }).click();
  await expect(linkedWork.locator('.linked-work-list')).toContainText(linkedKey);

  // The link is a change the replica syncs, and the view re-renders when it
  // lands. Write the comment into the view that change leaves behind, rather
  // than into one that is about to be replaced.
  await expect
    .poll(async () => (await page.locator('#sync-banner').textContent()) ?? '', { timeout: 20_000 })
    .toContain('synced');
  await page.fill('.rich-editor', 'Unified activity comment');
  await page.locator('.comment-form button[type=submit]').click();
  await expect(page.locator('[data-activity-kind=comment]', { hasText: 'Unified activity comment' })).toBeVisible();

  await page.fill('#worklog-time', '1h');
  await page.fill('#worklog-comment', 'Triage verification');
  await page.locator('.worklog-form button[type=submit]').click();
  await expect(page.locator('[data-activity-kind=worklog]', { hasText: 'logged 1h' })).toBeHidden();
  await expect(page.getByRole('button', { name: /^Comments/ })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: /^All/ }).click();
  await expect(page.locator('[data-activity-kind=comment]')).toBeVisible();
  await expect(page.locator('[data-activity-kind=worklog]', { hasText: 'logged 1h' })).toBeVisible();
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

  await page.getByRole('button', { name: /^All/ }).click();
  await page.getByRole('button', { name: /^Delete work log/ }).click();
  await expect(page.locator('[data-activity-kind=worklog]')).toHaveCount(0);
  await page.getByRole('button', { name: `Remove link to ${linkedKey}` }).click();
  await expect(page.locator('.linked-work-list')).toHaveCount(0);

  const me = await (await request.get('/rest/api/3/myself', { headers: auth })).json();
  const unwatch = await request.delete(`/rest/api/3/issue/${key}/watchers?accountId=${me.accountId}`, { headers: auth });
  expect(unwatch.status()).toBe(204);
  const unvote = await request.delete(`/rest/api/3/issue/${key}/votes`, { headers: auth });
  expect(unvote.status()).toBe(204);
});

// A live update landing while somebody is writing must not take what they
// wrote. The view holds its re-render back; the comment is sent as written,
// and the newer view arrives after it.
test('a live update while a comment is being written does not empty it', async ({ page, request }) => {
  const marker = Date.now();
  const key = await createIssue(request, `Live comment ${marker}`);
  await login(page);
  await page.goto(`/browse/${key}`);
  const banner = page.locator('#sync-banner');
  await expect.poll(async () => (await banner.textContent()) ?? '', { timeout: 20_000 }).toContain('synced');
  const seqOf = async () => Number(((await banner.textContent()) ?? '').replace(/^.*seq /, '')) || 0;
  const before = await seqOf();

  const text = `Written while syncing ${marker}`;
  await page.fill('.rich-editor', text);

  // Somebody else changes the work item, so the page is told to re-render
  // while the comment is still being written.
  const renamed = `Live comment ${marker} renamed`;
  const changed = await request.put(`/rest/api/3/issue/${key}`, {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { summary: renamed } },
  });
  expect(changed.status()).toBe(204);
  // The replica says it has the change; the page is holding the render back.
  await expect.poll(seqOf, { timeout: 30_000 }).toBeGreaterThan(before);

  await page.locator('.comment-form button[type=submit]').click();
  await expect(page.locator('[data-activity-kind=comment]', { hasText: text })).toBeVisible();
  // The held render lands once the comment is sent.
  await expect.poll(async () => await page.locator('#issue-root h1').innerText(), { timeout: 20_000 }).toContain(renamed);
});

// Moving from one field to the next is not the same as being finished. A
// render held back while somebody typed must not land in the moment between
// the two, on a machine slow enough for that moment to be wide.
test('a held live update does not land between one field and the next', async ({ page, request }) => {
  const marker = Date.now();
  const key = await createIssue(request, `Held render ${marker}`);
  await login(page);
  await page.goto(`/browse/${key}`);
  const banner = page.locator('#sync-banner');
  await expect.poll(async () => (await banner.textContent()) ?? '', { timeout: 20_000 }).toContain('synced');
  const seqOf = async () => Number(((await banner.textContent()) ?? '').replace(/^.*seq /, '')) || 0;
  const before = await seqOf();

  // Something is typed, so the view holds its next render back.
  await page.getByText('Link work item', { exact: true }).click();
  await page.fill('#link-issue', `${key}`);
  const renamed = `Held render ${marker} renamed`;
  const changed = await request.put(`/rest/api/3/issue/${key}`, {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { summary: renamed } },
  });
  expect(changed.status()).toBe(204);
  await expect.poll(seqOf, { timeout: 30_000 }).toBeGreaterThan(before);

  // A busy machine takes its time landing the focus in the next field.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: 20 });
  const text = `Typed on the way to the comment ${marker}`;
  await page.fill('.rich-editor', text);
  await page.locator('.comment-form button[type=submit]').click();
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: 1 });
  await expect(page.locator('[data-activity-kind=comment]', { hasText: text })).toBeVisible({ timeout: 20_000 });
});
