import { expect, test } from '@playwright/test';
import { apiAuthHeader } from './auth';
import axe from 'axe-core';

test('a signed-out reader discovers public projects and pages only public work', async ({ page, browser, baseURL }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PC${stamp.toString().slice(-7)}`;
  const privateKey = `PV${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const projects: string[] = [];
  const guest = await browser.newContext({ baseURL, storageState: { cookies: [], origins: [] }, colorScheme: 'dark', viewport: { width: 320, height: 740 } });
  try {
    for (const projectKey of [key, privateKey]) {
      const project = await page.request.post('/rest/api/3/project', { headers, data: { key: projectKey, name: `Collection ${projectKey}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
      expect(project.status(), await project.text()).toBe(201);
      projects.push(String((await project.json()).id));
    }
    const scheme = await page.request.post('/rest/api/3/permissionscheme', { headers, data: { name: `Public collection ${stamp}`, permissions: [{ permission: 'BROWSE_PROJECTS', holder: { type: 'anyone' } }] } });
    expect(scheme.status(), await scheme.text()).toBe(201);
    const schemeID = String((await scheme.json()).id);
    expect((await page.request.put(`/rest/api/3/project/${projects[0]}/permissionscheme`, { headers, data: { id: Number(schemeID) } })).status()).toBe(200);
    const keys: string[] = [];
    for (let n = 0; n < 51; n++) {
      const issue = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: `Public row ${n}` } } });
      expect(issue.status(), await issue.text()).toBe(201);
      keys.push(String((await issue.json()).key));
    }
    const reader = await guest.newPage();
    const directory = await reader.goto('/projects');
    expect(directory?.status()).toBe(200);
    expect(directory?.headers()['cache-control']).toBe('private, no-store');
    await expect(reader.getByRole('link', { name: `${key} · Collection ${key}`, exact: true })).toBeVisible();
    await expect(reader.getByText(`Collection ${privateKey}`, { exact: true })).toHaveCount(0);
    await reader.getByRole('link', { name: `${key} · Collection ${key}`, exact: true }).click();
    const workLinks = () => reader.locator('.public-collection-list a');
    await expect(workLinks()).toHaveCount(50);
    const first = await workLinks().evaluateAll(links => links.map(link => new URL((link as HTMLAnchorElement).href).pathname));
    await reader.getByRole('link', { name: 'Next page', exact: true }).click();
    await expect(workLinks()).toHaveCount(1);
    const last = await workLinks().getAttribute('href');
    expect([...first, last].sort()).toEqual(keys.map(key => `/browse/${key}`).sort());
    await expect(reader.getByRole('link', { name: 'Next page', exact: true })).toHaveCount(0);
    await reader.getByRole('link', { name: 'First page', exact: true }).click();
    await expect(workLinks()).toHaveCount(50);
    await expect(reader.locator('form')).toHaveCount(0);
    await expect(reader.locator('html')).toHaveAttribute('data-theme', 'dark');
    expect(await reader.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await reader.addScriptTag({ content: axe.source });
    expect(await reader.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
    await reader.evaluate(async () => {
      await navigator.serviceWorker.register('/static/sw.js');
      await navigator.serviceWorker.ready;
    });
    expect((await reader.reload())?.headers()['cache-control']).toBe('private, no-store');
    await expect.poll(() => reader.evaluate(() => Boolean(navigator.serviceWorker.controller))).toBe(true);
    const cachedCollections = await reader.evaluate(async () => {
      const names = (await caches.keys()).filter(name => name.startsWith('zzira-pages-'));
      const requests = (await Promise.all(names.map(async name => (await caches.open(name)).keys()))).flat();
      return requests.filter(request => /^\/(projects|issues)(\/|$)/.test(new URL(request.url).pathname)).length;
    });
    expect(cachedCollections).toBe(0);
    await guest.setOffline(true);
    expect((await reader.reload())?.status()).toBe(503);
    await guest.setOffline(false);
    expect((await reader.goto(`/issues/${key}`))?.status()).toBe(200);
    await workLinks().first().click();
    await expect(reader.getByRole('link', { name: `Collection ${key}`, exact: true })).toBeVisible();
    await reader.getByRole('link', { name: `Collection ${key}`, exact: true }).click();
    const grants = await (await page.request.get(`/rest/api/3/permissionscheme/${schemeID}/permission`, { headers })).json();
    const grant = grants.permissions.find((item: any) => item.permission === 'BROWSE_PROJECTS');
    expect((await page.request.delete(`/rest/api/3/permissionscheme/${schemeID}/permission/${grant.id}`, { headers })).status()).toBe(204);
    expect((await reader.reload())?.status()).toBe(404);
    expect((await reader.goto('/projects'))?.status()).toBe(200);
    await expect(reader.getByRole('link', { name: `${key} · Collection ${key}`, exact: true })).toHaveCount(0);
    await guest.addCookies([{ name: 'zzira_session', value: 'invalid-session', url: baseURL! }]);
    await reader.goto('/projects');
    await expect(reader).toHaveURL(/\/login/);
  } finally {
    await guest.close();
    for (const projectID of projects) await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
