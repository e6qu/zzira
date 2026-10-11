import { apiAuthHeader } from './auth';
import { scopeFixtureField } from './field_contexts';
import { expect, test } from '@playwright/test';

test('built-in and custom field details follow separate screens and hidden rules', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `CFS${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', { headers, data: { key, name: `Field screens ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  const fields: Record<string, string> = {};
  const screens: string[] = [];
  let screenSchemeID = '', typeSchemeID = '', configurationID = '', configurationSchemeID = '';
  try {
    for (const name of ['Shared', 'View only', 'Edit only', 'Hidden']) {
      const created = await page.request.post('/rest/api/3/field', { headers, data: { name: `${name} ${stamp}`, type: 'com.atlassian.jira.plugin.system.customfieldtypes:textfield' } });
      expect(created.status()).toBe(201);
      const id = String((await created.json()).id);
      fields[name] = id;
      const contexts = await (await page.request.get(`/rest/api/3/field/${id}/context`, { headers })).json();
      await scopeFixtureField(page.request, id, String(contexts.values[0].id), projectID, headers);
    }
    const description = { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'View-only rich description', marks: [{ type: 'strong' }] }] }] };
    const created = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: 'Field screen journey', description, labels: ['view-only-label'], ...Object.fromEntries(Object.entries(fields).map(([name, id]) => [id, `${name} answer`])) } } });
    expect(created.status()).toBe(201);
    const issueKey = String((await created.json()).key);
    const createScreen = async (name: string, selected: string[]) => {
      const screen = await page.request.post('/rest/api/3/screens', { headers, data: { name: `${name} ${stamp}` } });
      expect(screen.status()).toBe(201);
      const id = String((await screen.json()).id);
      screens.push(id);
      const tabs = await (await page.request.get(`/rest/api/3/screens/${id}/tabs`, { headers })).json();
      for (const fieldId of ['summary', ...selected]) expect((await page.request.post(`/rest/api/3/screens/${id}/tabs/${tabs[0].id}/fields`, { headers, data: { fieldId } })).status()).toBe(200);
      return id;
    };
    const editScreen = await createScreen('Editing', [fields.Shared, fields['Edit only'], fields.Hidden, 'priority', 'assignee', 'duedate']);
    const viewScreen = await createScreen('Reading', [fields.Shared, fields['View only'], fields.Hidden, 'priority', 'assignee', 'description', 'labels']);
    const scheme = await page.request.post('/rest/api/3/screenscheme', { headers, data: { name: `Field screens ${stamp}`, screens: { default: editScreen, edit: editScreen, view: viewScreen } } });
    expect(scheme.status()).toBe(201);
    screenSchemeID = String((await scheme.json()).id);
    const types = await page.request.post('/rest/api/3/issuetypescreenscheme', { headers, data: { name: `Field types ${stamp}`, issueTypeMappings: [{ issueTypeId: 'default', screenSchemeId: screenSchemeID }] } });
    expect(types.status()).toBe(201);
    typeSchemeID = String((await types.json()).id);
    expect((await page.request.put('/rest/api/3/issuetypescreenscheme/project', { headers, data: { issueTypeScreenSchemeId: typeSchemeID, projectId: projectID } })).status()).toBe(204);
    const configuration = await page.request.post('/rest/api/3/fieldconfiguration', { headers, data: { name: `Field rules ${stamp}` } });
    expect(configuration.status()).toBe(201);
    configurationID = String((await configuration.json()).id);
    expect((await page.request.put(`/rest/api/3/fieldconfiguration/${configurationID}/fields`, { headers, data: { fieldConfigurationItems: [{ id: fields.Hidden, isHidden: true }, { id: 'priority', isHidden: true }, { id: 'assignee', isHidden: true }] } })).status()).toBe(204);
    const configScheme = await page.request.post('/rest/api/3/fieldconfigurationscheme', { headers, data: { name: `Field rule scheme ${stamp}` } });
    expect(configScheme.status()).toBe(201);
    configurationSchemeID = String((await configScheme.json()).id);
    expect((await page.request.put(`/rest/api/3/fieldconfigurationscheme/${configurationSchemeID}/mapping`, { headers, data: { mappings: [{ issueTypeId: 'default', fieldConfigurationId: configurationID }] } })).status()).toBe(204);
    expect((await page.request.put('/rest/api/3/fieldconfigurationscheme/project', { headers, data: { projectId: projectID, fieldConfigurationSchemeId: configurationSchemeID } })).status()).toBe(204);
    await page.goto(`/browse/${issueKey}`);
    await expect(page.locator('.issue-description strong')).toHaveText('View-only rich description');
    await expect(page.locator('#field-labels, #field-assignee, #field-priority, #field-duedate, #field-reporter')).toHaveCount(0);
    const details = page.locator('details.more-fields');
    await details.locator('summary').click();
    await expect(details).toContainText('View only answer');
    await expect(details).not.toContainText('Edit only answer');
    await expect(details).not.toContainText('Hidden answer');
    await expect(details.locator(`#detail-field-${fields['View only']}`)).toHaveCount(0);
    await expect(details.locator(`#detail-field-${fields.Shared}`)).toHaveValue('Shared answer');
    await page.locator('#edit-issue-button').click();
    const edit = page.getByRole('dialog', { name: 'Edit issue' });
    await expect(edit.locator('#edit-description, #edit-assignee')).toHaveCount(0);
    await expect(edit.getByLabel(`View only ${stamp}`, { exact: true })).toHaveCount(0);
    await expect(edit.getByLabel(`Hidden ${stamp}`, { exact: true })).toHaveCount(0);
    await edit.getByLabel(`Edit only ${stamp}`, { exact: true }).fill('Updated edit answer');
    await edit.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(edit).toHaveCount(0);
    await page.reload();
    await expect(page.locator('details.more-fields')).not.toContainText('Updated edit answer');
    const issue = await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=description,labels,${Object.values(fields).join(',')}`, { headers })).json();
    expect(issue.fields.description).toEqual(description);
    expect(issue.fields.labels).toEqual(['view-only-label']);
    expect(issue.fields[fields['Edit only']]).toBe('Updated edit answer');
    expect(issue.fields[fields['View only']]).toBe('View only answer');
    const refused = await page.request.put(`/rest/api/3/issue/${issueKey}`, { headers, data: { fields: { [fields['View only']]: 'Forged answer' } } });
    expect(refused.status()).toBe(400);
    expect((await page.request.put(`/rest/api/3/issue/${issueKey}`, { headers, data: { fields: { labels: ['forged'] } } })).status()).toBe(400);
  } finally {
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
    for (const [kind, id] of [['issuetypescreenscheme', typeSchemeID], ['screenscheme', screenSchemeID], ['fieldconfigurationscheme', configurationSchemeID], ['fieldconfiguration', configurationID]] as const) if (id) await page.request.delete(`/rest/api/3/${kind}/${id}`, { headers, timeout: 10_000 });
    for (const id of screens) await page.request.delete(`/rest/api/3/screens/${id}`, { headers, timeout: 10_000 });
    for (const id of Object.values(fields)) {
      await page.request.post(`/rest/api/3/field/${id}/trash`, { headers, timeout: 10_000 });
      await page.request.delete(`/rest/api/3/field/${id}`, { headers, timeout: 10_000 });
    }
  }
});
