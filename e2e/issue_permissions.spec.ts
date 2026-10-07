import { expect, test } from '@playwright/test';
import axe from 'axe-core';
import { apiAuthHeader, seedPassword } from './auth';

test('readers, commenters and editors get independent controls and private activity', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader() };
  const owner = await (await request.get('/rest/api/3/myself', { headers: auth })).json();
  const reader = await (await request.get('/rest/api/3/myself', { headers: { Authorization: apiAuthHeader('ana@zzira.dev') } })).json();
  const projectKey = `IP${Date.now().toString(36).toUpperCase()}`;
  expect((await request.post('/rest/api/3/project', { headers: auth, data: {
    key: projectKey, name: 'Independent issue permissions', projectTypeKey: 'software', leadAccountId: owner.accountId,
  } })).status()).toBe(201);
  const created = await request.post('/rest/api/3/issue', { headers: auth, data: { fields: {
    project: { key: projectKey }, summary: 'A reader can follow this work', issuetype: { name: 'Task' }, assignee: { accountId: owner.accountId },
  } } });
  expect(created.status()).toBe(201);
  const issueKey = (await created.json()).key;
  const roleResponse = await request.post('/rest/api/3/role', { headers: auth, data: { name: `Private activity ${projectKey}` } });
  expect(roleResponse.status()).toBe(200);
  const role = await roleResponse.json();
  expect((await request.post(`/rest/api/3/project/${projectKey}/role/${role.id}`, { headers: auth, data: { user: [owner.accountId] } })).status()).toBe(200);
  const privateComment = 'Private release decision';
  expect((await request.post(`/rest/api/3/issue/${issueKey}/comment`, { headers: auth, data: {
    body: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: privateComment }] }] },
    visibility: { type: 'role', identifier: String(role.id) },
  } })).status()).toBe(201);
  const schemeResponse = await request.post('/rest/api/3/permissionscheme', { headers: auth, data: {
    name: `Issue journeys ${projectKey}`, permissions: [{ permission: 'BROWSE_PROJECTS', holder: { type: 'anyone' } }],
  } });
  expect(schemeResponse.status()).toBe(201);
  const scheme = await schemeResponse.json();
  expect((await request.put(`/rest/api/3/project/${projectKey}/permissionscheme`, { headers: auth, data: { id: scheme.id } })).status()).toBe(200);

  await page.goto('/login');
  await page.locator('#login-email').fill('ana@zzira.dev');
  await page.locator('#login-password').fill(seedPassword('ana@zzira.dev'));
  await page.locator('button[type=submit]').click();
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto(`/browse/${issueKey}`);
  await expect(page.locator('.issue-summary')).toHaveText('A reader can follow this work');
  await expect(page.locator('#edit-issue-button, .inline-field, .comment-form, .worklog-form, .upload-form')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Delete issue', exact: true })).toHaveCount(0);
  await expect(page.locator('.activity-ledger')).not.toContainText(privateComment);
  await page.getByRole('button', { name: /^Watch / }).click();
  await expect(page.getByRole('button', { name: /^Watching / })).toHaveAttribute('aria-pressed', 'true');
  expect((await page.request.post(`/issues/${issueKey}/comments`, { form: { body: 'Unauthorized comment' } })).status()).toBe(403);
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);

  const grant = async (permission: string) => {
    expect((await request.post(`/rest/api/3/permissionscheme/${scheme.id}/permission`, { headers: auth, data: {
      permission, holder: { type: 'user', value: reader.accountId },
    } })).status()).toBe(201);
    await page.reload();
  };
  await grant('ADD_COMMENTS');
  await expect(page.locator('.comment-form')).toBeVisible();
  await expect(page.locator('#edit-issue-button')).toHaveCount(0);
  await page.locator('#comment-editor').fill('A permitted progress update');
  await page.getByRole('button', { name: 'Add comment', exact: true }).click();
  await expect(page.locator('.activity-ledger')).toContainText('A permitted progress update');
  await expect(page.locator('.activity-ledger')).not.toContainText(privateComment);
  await grant('EDIT_ISSUES');
  await expect(page.locator('#field-priority')).toBeVisible();
  await expect(page.locator('#field-assignee, #field-duedate')).toHaveCount(0);
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.locator('#edit-assignee')).toHaveCount(0);
  await page.locator('#edit-summary').fill('Edited without reassigning');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('.issue-summary')).toHaveText('Edited without reassigning');
  const saved = await (await request.get(`/rest/api/3/issue/${issueKey}`, { headers: auth })).json();
  expect(saved.fields.assignee.accountId).toBe(owner.accountId);
});
