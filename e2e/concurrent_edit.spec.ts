import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';

test.use({ actionTimeout: 15_000 });

// Two people editing one work item is where a lost update actually happens:
// the dialog is composed from the work item as it was opened, and saving it
// minutes later would write every field back over whatever changed in the
// meantime. Jira refuses that save, and so does this.
test('an edit dialog opened before somebody else saved refuses to overwrite them', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `CNC${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', {
    headers,
    data: { key, name: `Concurrent edits ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  try {
    const created = await page.request.post('/rest/api/3/issue', {
      headers,
      data: { fields: { project: { key }, summary: 'Original summary', issuetype: { name: 'Task' } } },
    });
    expect(created.status()).toBe(201);
    const issueKey = (await created.json()).key;

    await page.goto(`/browse/${issueKey}`);
    await page.locator('#edit-issue-button').click();
    const edit = page.getByRole('dialog', { name: 'Edit issue' });
    await expect(edit).toBeVisible();
    await edit.getByLabel('Summary', { exact: true }).fill('My careful rewrite');

    // Somebody else changes the work item while this dialog is open.
    const theirs = await page.request.put(`/rest/api/3/issue/${issueKey}`, {
      headers, data: { fields: { summary: 'Their urgent correction' } },
    });
    expect(theirs.status()).toBe(204);

    // Saving the stale dialog is refused, and says what happened.
    const refused = await Promise.all([
      page.waitForResponse(response => response.url().includes(`/issues/${issueKey}/edit`) && response.request().method() === 'POST'),
      edit.getByRole('button', { name: 'Save changes' }).click(),
    ]);
    expect(refused[0].status()).toBe(409);
    expect(await refused[0].text()).toContain('updated by somebody else');

    // Their change stands: the refused save wrote nothing.
    const after = await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=summary`, { headers })).json();
    expect(after.fields.summary).toBe('Their urgent correction');

    // Reopening the work item picks up their change, and editing then works.
    await page.reload();
    await expect(page.locator('h1')).toContainText('Their urgent correction');
    await page.locator('#edit-issue-button').click();
    await expect(edit).toBeVisible();
    await edit.getByLabel('Summary', { exact: true }).fill('Agreed wording');
    await Promise.all([
      page.waitForResponse(response => response.url().includes(`/issues/${issueKey}/edit`) && response.request().method() === 'POST' && response.status() === 200),
      edit.getByRole('button', { name: 'Save changes' }).click(),
    ]);
    const settled = await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=summary`, { headers })).json();
    expect(settled.fields.summary).toBe('Agreed wording');

    // The API publishes the same refusal as a conflict. An edit that names the
    // work item it was composed from is refused once the work item moves on.
    const conflict = await page.request.post(`/issues/${issueKey}/edit`, {
      headers: { Origin: new URL(page.url()).origin },
      form: { summary: 'Composed against an older work item', version: 'a version this work item never had' },
    });
    expect(conflict.status()).toBe(409);
  } finally {
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
