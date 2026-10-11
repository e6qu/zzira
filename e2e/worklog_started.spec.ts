import { test, expect } from '@playwright/test';
import * as axe from 'axe-core';
import { apiAuthHeader, seedPassword } from './auth';

test('logging earlier work keeps refused answers and shows the recorded start time', async ({ page, request }) => {
  test.setTimeout(60_000);
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36).toUpperCase();
  const projectKey = `WS${stamp}`.slice(0, 10);
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  const projectResponse = await request.post('/rest/api/3/project', {
    headers, data: { key: projectKey, name: `Work start ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId },
  });
  expect(projectResponse.status(), await projectResponse.text()).toBe(201);
  const project = await projectResponse.json();
  try {
    const created = await request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key: projectKey }, issuetype: { name: 'Task' }, summary: `Earlier work ${stamp}` } },
    });
    expect(created.status(), await created.text()).toBe(201);
    const issue = await created.json();
    await page.goto('/login');
    await page.fill('#login-email', 'demo@zzira.dev');
    await page.fill('#login-password', seedPassword());
    await page.click('button[type=submit]');
    await expect(page).toHaveURL('/');
    await page.goto(`/browse/${issue.key}`);
    const form = page.locator('.worklog-form');
    await form.getByLabel('Time spent', { exact: true }).fill('invalid duration');
    await form.getByLabel('Work started (UTC)').fill('2026-10-01T08:34:56');
    await form.getByLabel('Work description').fill(`Investigation ${stamp}`);
    await form.getByLabel('Set to', { exact: true }).check();
    await form.getByLabel('New remaining estimate').fill('3h');
    await form.getByRole('button', { name: 'Log time' }).click();
    await expect(form.getByRole('alert')).toContainText('Enter the time spent');
    await expect(form.getByLabel('Time spent', { exact: true })).toHaveValue('invalid duration');
    await expect(form.getByLabel('Work started (UTC)')).toHaveValue('2026-10-01T08:34:56');
    await expect(form.getByLabel('Work description')).toHaveValue(`Investigation ${stamp}`);
    await expect(form.getByLabel('Set to', { exact: true })).toBeChecked();
    await expect(form.getByLabel('New remaining estimate')).toHaveValue('3h');
    const empty = await (await request.get(`/rest/api/3/issue/${issue.key}/worklog`, { headers })).json();
    expect(empty.total).toBe(0);
    await form.getByLabel('Time spent', { exact: true }).fill('1h 30m');
    await form.getByRole('button', { name: 'Log time' }).click();
    await expect(form.getByRole('alert')).toHaveCount(0);
    await page.getByRole('button', { name: /^Work log/ }).click();
    const entry = page.locator('.worklog', { hasText: `Investigation ${stamp}` });
    await expect(entry).toBeVisible();
    await expect(entry).toContainText('1h 30m');
    await expect(entry).toContainText('Work started 2026-10-01T08:34:56.000Z');
    const listed = await (await request.get(`/rest/api/3/issue/${issue.key}/worklog`, { headers })).json();
    expect(listed.total).toBe(1);
    const logged = listed.worklogs[0];
    expect(logged.started).toBe('2026-10-01T08:34:56.000+0000');
    expect(logged.author.accountId).toBe(me.accountId);
    expect(logged.updateAuthor.accountId).toBe(me.accountId);
    expect(logged.issueId).toBe(issue.id);
    expect((await request.get(new URL(logged.self).pathname, { headers, timeout: 10_000 })).status()).toBe(200);
    await page.reload();
    await page.getByRole('button', { name: /^Work log/ }).click();
    await expect(entry).toContainText('Work started 2026-10-01T08:34:56.000Z');
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
    })).violations);
    expect(violations).toEqual([]);
  } finally {
    expect((await request.delete(`/rest/api/3/project/${project.id}`, { headers, timeout: 10_000 })).status()).toBe(204);
  }
});
