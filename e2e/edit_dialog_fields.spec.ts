import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test.use({ actionTimeout: 15_000 });

test('fields on only the edit screen can be changed in the issue dialog', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `EDF${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', { headers, data: { key, name: `Editing fields ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  const screens: string[] = [];
  let screenSchemeID = '', typeSchemeID = '';
  try {
    const createIssue = async (type: string, summary: string) => {
      const response = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, issuetype: { name: type }, summary } } });
      expect(response.status()).toBe(201);
      return String((await response.json()).key);
    };
    const parentKey = await createIssue('Epic', 'Parent epic');
    const issueKey = await createIssue('Task', 'Editable work');
    const editOrder = ['labels', 'parent', 'summary', 'timetracking', 'resolution', 'duedate', 'priority'];
    for (const [name, fields] of [['Editing', editOrder], ['Reading', ['summary']]] as const) {
      const response = await page.request.post('/rest/api/3/screens', { headers, data: { name: `${name} fields ${stamp}` } });
      expect(response.status()).toBe(201);
      const id = String((await response.json()).id);
      screens.push(id);
      const tabs = await (await page.request.get(`/rest/api/3/screens/${id}/tabs`, { headers })).json();
      for (const fieldId of fields) expect((await page.request.post(`/rest/api/3/screens/${id}/tabs/${tabs[0].id}/fields`, { headers, data: { fieldId } })).status()).toBe(200);
    }
    const scheme = await page.request.post('/rest/api/3/screenscheme', { headers, data: { name: `Edit-only screens ${stamp}`, screens: { default: screens[0], edit: screens[0], view: screens[1] } } });
    expect(scheme.status()).toBe(201);
    screenSchemeID = String((await scheme.json()).id);
    const types = await page.request.post('/rest/api/3/issuetypescreenscheme', { headers, data: { name: `Edit-only types ${stamp}`, issueTypeMappings: [{ issueTypeId: 'default', screenSchemeId: screenSchemeID }] } });
    expect(types.status()).toBe(201);
    typeSchemeID = String((await types.json()).id);
    expect((await page.request.put('/rest/api/3/issuetypescreenscheme/project', { headers, data: { projectId: projectID, issueTypeScreenSchemeId: typeSchemeID } })).status()).toBe(204);
    await page.goto(`/browse/${issueKey}`);
    await expect(page.locator('#field-priority, #field-labels, #field-duedate, #field-parent, #field-resolution')).toHaveCount(0);
    await page.locator('#edit-issue-button').click();
    const dialog = page.getByRole('dialog', { name: 'Edit issue', exact: true });
    await expect(dialog.locator('.edit-form-body > label')).toHaveText(['Labels', 'Parent', 'Summary', 'Original estimate', 'Remaining estimate', 'Resolution', 'Due date', 'Priority']);
    await dialog.getByLabel('Priority', { exact: true }).selectOption({ label: 'High' });
    await dialog.getByLabel('Labels', { exact: true }).fill('release, verified');
    await dialog.getByLabel('Due date', { exact: true }).fill('2026-12-01');
    await dialog.getByLabel('Parent', { exact: true }).selectOption(parentKey);
    await dialog.getByLabel('Resolution', { exact: true }).selectOption({ label: 'Done' });
    await dialog.getByLabel('Original estimate', { exact: true }).fill('not a duration');
    await dialog.getByLabel('Remaining estimate', { exact: true }).fill('1h');
    const refused = page.waitForResponse(response => response.url().endsWith(`/issues/${issueKey}/edit`) && response.request().method() === 'POST');
    await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
    expect((await refused).status()).toBe(400);
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel('Labels', { exact: true })).toHaveValue('release, verified');
    await expect(dialog.getByLabel('Original estimate', { exact: true })).toHaveValue('not a duration');
    await dialog.getByLabel('Original estimate', { exact: true }).fill('3h');
    await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(dialog).not.toBeVisible();
    await page.reload();
    const fields = (await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=priority,labels,duedate,parent,resolution,timetracking`, { headers })).json()).fields;
    expect(fields).toMatchObject({ priority: { name: 'High' }, labels: ['release', 'verified'], duedate: '2026-12-01', parent: { key: parentKey }, resolution: { name: 'Done' }, timetracking: { originalEstimateSeconds: 10800, remainingEstimateSeconds: 3600 } });
    await page.locator('#edit-issue-button').click();
    await expect(dialog.getByLabel('Priority', { exact: true }).locator('option:checked')).toHaveText('High');
    await expect(dialog.getByLabel('Labels', { exact: true })).toHaveValue('release, verified');
    await expect(dialog.getByLabel('Parent', { exact: true }).locator('option:checked')).toContainText(parentKey);
    await dialog.getByLabel('Original estimate', { exact: true }).fill('');
    await dialog.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(dialog).not.toBeVisible();
    const afterClear = (await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=parent,timetracking`, { headers })).json()).fields;
    expect(afterClear.parent.key).toBe(parentKey);
    expect(afterClear.timetracking.originalEstimateSeconds).toBeUndefined();
    expect(afterClear.timetracking.remainingEstimateSeconds).toBe(3600);
    await page.locator('[data-theme-toggle]').click();
    await page.setViewportSize({ width: 320, height: 740 });
    await page.locator('#edit-issue-button').click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
  } finally {
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
    if (typeSchemeID) await page.request.delete(`/rest/api/3/issuetypescreenscheme/${typeSchemeID}`, { headers, timeout: 10_000 });
    if (screenSchemeID) await page.request.delete(`/rest/api/3/screenscheme/${screenSchemeID}`, { headers, timeout: 10_000 });
    for (const id of screens) await page.request.delete(`/rest/api/3/screens/${id}`, { headers, timeout: 10_000 });
  }
});
