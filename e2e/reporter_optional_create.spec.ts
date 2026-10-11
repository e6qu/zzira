import { test, expect } from '@playwright/test';
import axe from 'axe-core';
import { apiAuthHeader, seedPassword } from './auth';

test('an optional Reporter can stay empty through create refusal and retry', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36).toUpperCase();
  const key = `RN${stamp}`.slice(0, 10);
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  const createdProject = await request.post('/rest/api/3/project', {
    headers, data: { key, name: `Optional Reporter ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId },
  });
  expect(createdProject.status(), await createdProject.text()).toBe(201);
  const project = await createdProject.json();
  try {
    await page.goto('/login');
    await page.fill('#login-email', 'demo@zzira.dev');
    await page.fill('#login-password', seedPassword());
    await page.click('button[type=submit]');
    await expect(page).toHaveURL('/');
    await page.goto(`/issues/${key}`);
    await page.locator('#global-create-issue').click();
    const dialog = page.getByRole('dialog', { name: 'Create issue' });
    await expect(dialog).toHaveAttribute('data-ready', '1');
    if (await dialog.locator('#create-project').inputValue() !== key) {
      await Promise.all([
        page.waitForResponse(response => {
          const url = new URL(response.url());
          return response.request().method() === 'GET' && url.pathname === '/issues/new' && url.searchParams.get('project') === key;
        }),
        dialog.locator('#create-project').selectOption(key),
      ]);
      await expect(dialog).toHaveAttribute('data-ready', '1');
    }
    await expect(dialog.locator('#create-project')).toHaveValue(key);
    await dialog.locator('details.create-more > summary').click();
    await expect(dialog.getByLabel('Reporter', { exact: true })).toHaveValue(me.accountId);
    await dialog.getByLabel('Reporter', { exact: true }).selectOption({ label: 'None' });
    const summary = `No Reporter ${stamp}`;
    await dialog.locator('#create-summary').fill(summary);
    await dialog.getByLabel('Labels', { exact: true }).fill('two words');
    await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
    await expect(dialog.getByRole('alert')).toContainText('labels must be');
    await expect(dialog.getByLabel('Reporter', { exact: true })).toHaveValue('');
    await expect(dialog.locator('#create-summary')).toHaveValue(summary);
    await dialog.getByLabel('Labels', { exact: true }).fill('validated');
    await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
    await page.waitForURL(new RegExp(`/browse/${key}-\\d+$`), { timeout: 30_000 });
    const issueKey = new URL(page.url()).pathname.split('/').pop();
    const issue = await (await request.get(`/rest/api/3/issue/${issueKey}?fields=reporter,creator`, { headers })).json();
    expect(issue.fields.reporter).toBeNull();
    expect(issue.fields.creator.accountId).toBe(me.accountId);
    await page.reload();
    await expect(page.locator('#field-reporter')).toHaveValue('');
    const search = await (await request.post('/rest/api/3/search/jql', { headers, data: { jql: `project = ${key} AND reporter IS EMPTY`, fields: ['reporter'] } })).json();
    expect(search.issues.map((item: any) => item.key)).toContain(issueKey);
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
