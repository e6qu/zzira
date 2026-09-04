import { expect, Page, test } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };
const ANA = { email: 'ana@zzira.dev', password: 'ana12345' };

function authFor(email: string): { headers: { Authorization: string } } {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN && email === DEMO.email ? process.env.ZZIRA_API_TOKEN : tokens[email];
  return { headers: { Authorization: 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64') } };
}

async function login(page: Page, account = ANA) {
  await page.goto('/login');
  await page.fill('input[name=email]', account.email);
  await page.fill('input[name=password]', account.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('notifications API and inbox keep private read state in sync', async ({ page, request }) => {
  const demo = authFor(DEMO.email);
  const ana = authFor(ANA.email);
  const users = await (await request.get('/rest/api/3/user/search?query=ana', demo)).json();
  const anaID = users[0].accountId;

  const created = await request.post('/rest/api/3/issue', {
    ...demo,
    data: {
      fields: {
        project: { key: 'ZZ' },
        summary: `Notification inbox ${Date.now()}`,
        issuetype: { name: 'Task' },
      },
    },
  });
  expect(created.status()).toBe(201);
  const issue = await created.json();
  const assigned = await request.put(`/rest/api/3/issue/${issue.key}`, {
    ...demo,
    data: { fields: { assignee: { accountId: anaID } } },
  });
  expect(assigned.status()).toBe(204);

  const response = await request.get('/rest/zzira/1/notifications?limit=200', ana);
  expect(response.status()).toBe(200);
  const payload = await response.json();
  const notification = payload.notifications.find((item: any) => item.entityId === issue.id);
  expect(notification).toMatchObject({ read: false, kind: 'assigned', entityType: 'issue' });
  expect(notification.created).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  expect(payload.unreadCount).toBeGreaterThan(0);
  expect(payload).toMatchObject({ startAt: 0, maxResults: 200 });
  expect(payload.total).toBeGreaterThanOrEqual(payload.notifications.length);

  const unreadPage = await (await request.get('/rest/zzira/1/notifications?startAt=0&maxResults=1&unreadOnly=true', ana)).json();
  expect(unreadPage.notifications).toHaveLength(1);
  expect(unreadPage.total).toBe(payload.unreadCount);
  expect((await request.get('/rest/zzira/1/notifications?maxResults=0', ana)).status()).toBe(400);
  expect((await request.get('/rest/zzira/1/notifications?startAt=-1', ana)).status()).toBe(400);
  expect((await request.get('/rest/zzira/1/notifications?unreadOnly=sometimes', ana)).status()).toBe(400);

  const forbidden = await request.put(`/rest/zzira/1/notifications/${notification.id}`, {
    ...demo,
    data: { read: true },
  });
  expect(forbidden.status()).toBe(404);

  await login(page);
  await page.goto('/notifications');
  const item = page.locator('.notification-inbox-item', { hasText: issue.key });
  await expect(item).toHaveClass(/is-unread/);
  await expect(page.locator('.header-notifications')).toHaveAttribute('aria-label', /unread/);

  await item.getByRole('button', { name: 'Mark as read' }).click();
  await expect(page.locator('.notification-inbox-item', { hasText: issue.key })).not.toHaveClass(/is-unread/);
  await page.locator('.notification-inbox-item', { hasText: issue.key }).getByRole('button', { name: 'Mark as unread' }).click();

  await page.getByRole('link', { name: /Unread/ }).click();
  const unreadItem = page.locator('.notification-inbox-item', { hasText: issue.key });
  await expect(unreadItem).toBeVisible();
  await unreadItem.locator('.notification-open').click();
  await expect(page.locator('#issue-root')).toHaveAttribute('data-issue-id', issue.id);

  const afterOpen = await (await request.get('/rest/zzira/1/notifications?limit=200', ana)).json();
  expect(afterOpen.notifications.find((item: any) => item.id === notification.id).read).toBe(true);

  const marked = await request.post('/rest/zzira/1/notifications/read-all', ana);
  expect(marked.status()).toBe(200);
  expect(await marked.json()).toMatchObject({ unreadCount: 0 });
});
