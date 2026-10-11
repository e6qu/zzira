import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';

test('new replicas leave room for journals and retain earlier offline copies', async ({ page, context }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const summary = `Retained replica ${Date.now()}`;
  const latestSummary = `${summary} updated`;
  const created = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, issuetype: { name: 'Task' }, summary } } });
  expect(created.status()).toBe(201);
  const key = String((await created.json()).key);
  let firstReplica = '';
  try {
    // The default SQLite pool has six slots. Each new replica must have
    // capacity for its database and journal without clearing older files.
    for (let i = 0; i < 8; i++) {
      const replica = await page.evaluate(() => {
        const id = crypto.randomUUID();
        sessionStorage.setItem('zzira-replica-id', id);
        return id;
      });
      if (i === 0) firstReplica = replica;
      await page.goto(`/browse/${key}`);
      await expect(page.locator('#sync-banner')).toContainText('synced', { timeout: 20_000 });
      await expect(page.locator('.issue-summary')).toHaveText(i === 0 ? summary : latestSummary);
      if (i === 0) {
        expect((await page.request.put(`/rest/api/3/issue/${key}`, { headers, data: { fields: { summary: latestSummary } } })).status()).toBe(204);
      }
    }
    await page.evaluate(id => sessionStorage.setItem('zzira-replica-id', id), firstReplica);
    await context.setOffline(true);
    await page.reload();
    await expect(page.locator('#sync-banner')).toContainText('offline', { timeout: 20_000 });
    // A cached page shell has the latest server summary. Its offline edit
    // dialog must instead read the earlier replica's retained SQLite row.
    await page.locator('#edit-issue-button').click();
    await expect(page.locator('#edit-summary')).toHaveValue(summary);
  } finally {
    await context.setOffline(false);
    await page.request.delete(`/rest/api/3/issue/${key}`, { headers, timeout: 10_000 });
  }
});
