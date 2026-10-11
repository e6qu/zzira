import { test, expect } from '@playwright/test';
import * as axe from 'axe-core';
import { apiAuthHeader, seedPassword } from './auth';

test('Reporter edits retain refused choices and persist through a transition screen', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36).toUpperCase();
  const key = `RE${stamp}`.slice(0, 10);
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  const colleague = await (await request.get('/rest/api/3/myself', { headers: { Authorization: apiAuthHeader('ana@zzira.dev') } })).json();
  const createdProject = await request.post('/rest/api/3/project', {
    headers, data: { key, name: `Reporter editing ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId },
  });
  expect(createdProject.status(), await createdProject.text()).toBe(201);
  const project = await createdProject.json();
  try {
    await page.goto('/login');
    await page.fill('#login-email', 'demo@zzira.dev');
    await page.fill('#login-password', seedPassword());
    await page.click('button[type=submit]');
    await expect(page).toHaveURL('/');
    await page.goto('/settings/workflows');
    await page.fill('#workflow-name', `Reporter workflow ${stamp}`);
    await page.selectOption('#workflow-project-scope', String(project.id));
    await page.getByRole('button', { name: 'Create workflow' }).click();
    await expect(page).toHaveURL(/\/settings\/workflows\/workflow_/);
    await page.fill('#transition-name', 'Start and report');
    await page.selectOption('#transition-from', 'st_todo');
    await page.selectOption('#transition-to', 'st_inprogress');
    await page.locator('fieldset').filter({ hasText: 'Transition screen fields' }).getByLabel('Reporter', { exact: true }).check();
    await page.getByRole('button', { name: 'Add transition' }).click();
    await page.getByRole('button', { name: 'Publish workflow' }).click();
    await expect(page.getByText('Published', { exact: true })).toBeVisible();
    const createdIssue = await request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: `Reporter subject ${stamp}` } },
    });
    expect(createdIssue.status(), await createdIssue.text()).toBe(201);
    const issue = await createdIssue.json();
    const reporter = async () => (await (await request.get(`/rest/api/3/issue/${issue.key}?fields=reporter,creator`, { headers })).json()).fields;
    await page.goto(`/browse/${issue.key}`);
    await expect(page.locator('#field-reporter')).toHaveValue(me.accountId);
    await page.locator('#edit-issue-button').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByLabel('Reporter', { exact: true })).toHaveValue(me.accountId);
    await dialog.getByLabel('Reporter', { exact: true }).selectOption(colleague.accountId);
    await dialog.getByLabel('Summary', { exact: true }).fill('   ');
    await dialog.getByRole('button', { name: 'Save changes' }).click();
    await expect(dialog.getByRole('alert')).toContainText('summary is required');
    await expect(dialog.getByLabel('Reporter', { exact: true })).toHaveValue(colleague.accountId);
    expect((await reporter()).reporter.accountId).toBe(me.accountId);
    await dialog.getByLabel('Summary', { exact: true }).fill(`Reporter subject ${stamp}`);
    await dialog.getByRole('button', { name: 'Save changes' }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(async () => (await reporter()).reporter.accountId).toBe(colleague.accountId);
    const transition = page.locator('details.transition-screen').filter({ hasText: 'Start and report' });
    await transition.locator('summary').click();
    await expect(transition.getByLabel('Reporter', { exact: true })).toHaveValue(colleague.accountId);
    await transition.getByLabel('Reporter', { exact: true }).selectOption(me.accountId);
    await transition.getByRole('button', { name: 'Complete Start and report' }).click();
    await expect.poll(async () => (await reporter()).reporter.accountId).toBe(me.accountId);
    expect((await reporter()).creator.accountId).toBe(me.accountId);
    await page.reload();
    await expect(page.locator('#field-reporter')).toHaveValue(me.accountId);
    await page.locator('#field-reporter').selectOption(colleague.accountId);
    await page.getByRole('button', { name: 'Save reporter', exact: true }).click();
    await expect.poll(async () => (await reporter()).reporter.accountId).toBe(colleague.accountId);
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
