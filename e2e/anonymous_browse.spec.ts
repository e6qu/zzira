import { expect, test } from '@playwright/test';
import { apiAuthHeader } from './auth';
import axe from 'axe-core';

test('a signed-out reader sees only public work and loses access after revocation', async ({ page, browser, baseURL }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PUB${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const created = await page.request.post('/rest/api/3/project', { headers, data: { key, name: `Public work ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);
  const guest = await browser.newContext({ baseURL, storageState: { cookies: [], origins: [] }, colorScheme: 'dark', viewport: { width: 320, height: 740 } });
  try {
    const issue = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: `Public journey ${stamp}`, description: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'A public description' }] }] } } } });
    expect(issue.status()).toBe(201);
    const issueKey = String((await issue.json()).key);
    expect((await page.request.post(`/rest/api/3/issue/${issueKey}/comment`, { headers, data: { body: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'A public comment' }] }] } } })).status()).toBe(201);
    const reader = await guest.newPage();
    expect((await reader.goto(`/browse/${issueKey}`))?.status()).toBe(404);
    const scheme = await page.request.post('/rest/api/3/permissionscheme', { headers, data: { name: `Public grant ${stamp}`, permissions: [{ permission: 'BROWSE_PROJECTS', holder: { type: 'anyone' } }] } });
    expect(scheme.status()).toBe(201);
    const schemeID = String((await scheme.json()).id);
    expect((await page.request.put(`/rest/api/3/project/${projectID}/permissionscheme`, { headers, data: { id: Number(schemeID) } })).status()).toBe(200);
    const response = await reader.goto(`/browse/${issueKey}`);
    expect(response?.status()).toBe(200);
    expect(response?.headers()['cache-control']).toBe('private, no-store');
    await expect(reader.getByRole('heading', { name: `Public journey ${stamp}` })).toBeVisible();
    await expect(reader.getByText('A public description', { exact: true })).toBeVisible();
    await expect(reader.getByText('A public comment', { exact: true })).toBeVisible();
    await expect(reader.getByRole('link', { name: 'Log in', exact: true })).toHaveCount(2);
    await expect(reader.locator('form')).toHaveCount(0);
    await expect(reader.locator('html')).toHaveAttribute('data-theme', 'dark');
    expect(await reader.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await reader.addScriptTag({ content: axe.source });
    expect(await reader.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
    await reader.evaluate(async () => {
      await navigator.serviceWorker.register('/static/sw.js');
      await navigator.serviceWorker.ready;
    });
    expect((await reader.reload())?.status()).toBe(200);
    await expect.poll(() => reader.evaluate(() => Boolean(navigator.serviceWorker.controller))).toBe(true);
    const cachedPublicPages = await reader.evaluate(async () => {
      const cacheNames = (await caches.keys()).filter(name => name.startsWith('zzira-pages-'));
      const requests = (await Promise.all(cacheNames.map(async name => (await caches.open(name)).keys()))).flat();
      return requests.filter(request => new URL(request.url).pathname.startsWith('/browse/')).length;
    });
    expect(cachedPublicPages).toBe(0);
    await guest.setOffline(true);
    expect((await reader.reload())?.status()).toBe(503);
    await expect(reader.getByRole('heading', { name: 'ZZIRA is offline' })).toBeVisible();
    await guest.setOffline(false);
    const grants = await (await page.request.get(`/rest/api/3/permissionscheme/${schemeID}/permission`, { headers })).json();
    const grant = grants.permissions.find((item: any) => item.permission === 'BROWSE_PROJECTS');
    expect(grant).toBeTruthy();
    expect((await page.request.delete(`/rest/api/3/permissionscheme/${schemeID}/permission/${grant.id}`, { headers })).status()).toBe(204);
    expect((await reader.reload())?.status()).toBe(404);
    await expect(reader.getByText(`Public journey ${stamp}`, { exact: true })).toHaveCount(0);
    await guest.addCookies([{ name: 'zzira_session', value: 'invalid-session', url: baseURL! }]);
    await reader.goto(`/browse/${issueKey}`);
    await expect(reader).toHaveURL(/\/login/);
  } finally {
    await guest.close();
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
