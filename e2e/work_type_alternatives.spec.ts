import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';

test('an administrator replaces a work type only with a compatible type', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `ALT${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', { headers, data: { key, name: `Replacement project ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  const kinds: Record<string, string> = {};
  let typeSchemeID = '', configurationSchemeID = '';
  const configs: string[] = [];
  try {
    for (const name of ['Source', 'Compatible', 'Different']) {
      const type = await page.request.post('/rest/api/3/issuetype', { headers, data: { name: `${name} ${stamp}`, type: 'standard' } });
      expect(type.status()).toBe(201);
      kinds[name] = String((await type.json()).id);
    }
    const typeScheme = await page.request.post('/rest/api/3/issuetypescheme', { headers, data: { name: `Replacement types ${stamp}`, defaultIssueTypeId: kinds.Source, issueTypeIds: Object.values(kinds) } });
    expect(typeScheme.status()).toBe(201);
    typeSchemeID = String((await typeScheme.json()).issueTypeSchemeId);
    expect((await page.request.put('/rest/api/3/issuetypescheme/project', { headers, data: { issueTypeSchemeId: typeSchemeID, projectId: projectID } })).status()).toBe(204);
    for (const name of ['Shared', 'Different']) {
      const configuration = await page.request.post('/rest/api/3/fieldconfiguration', { headers, data: { name: `${name} replacement fields ${stamp}` } });
      expect(configuration.status()).toBe(201);
      configs.push(String((await configuration.json()).id));
    }
    const scheme = await page.request.post('/rest/api/3/fieldconfigurationscheme', { headers, data: { name: `Replacement fields ${stamp}` } });
    expect(scheme.status()).toBe(201);
    configurationSchemeID = String((await scheme.json()).id);
    expect((await page.request.put(`/rest/api/3/fieldconfigurationscheme/${configurationSchemeID}/mapping`, { headers, data: { mappings: [{ issueTypeId: 'default', fieldConfigurationId: configs[0] }, { issueTypeId: kinds.Compatible, fieldConfigurationId: configs[0] }, { issueTypeId: kinds.Different, fieldConfigurationId: configs[1] }] } })).status()).toBe(204);
    expect((await page.request.put('/rest/api/3/fieldconfigurationscheme/project', { headers, data: { projectId: projectID, fieldConfigurationSchemeId: configurationSchemeID } })).status()).toBe(204);
    const issue = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, issuetype: { id: kinds.Source }, summary: 'Work needing a compatible replacement' } } });
    expect(issue.status()).toBe(201);
    const issueKey = String((await issue.json()).key);
    const alternatives = await page.request.get(`/rest/api/3/issuetype/${kinds.Source}/alternatives`, { headers });
    expect(alternatives.status()).toBe(200);
    expect((await alternatives.json()).map((type: any) => type.id)).toEqual([kinds.Compatible]);
    const refused = await page.request.delete(`/rest/api/3/issuetype/${kinds.Source}?alternativeIssueTypeId=${kinds.Different}`, { headers });
    expect(refused.status()).toBe(400);
    expect((await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=issuetype`, { headers })).json()).fields.issuetype.id).toBe(kinds.Source);
    await page.goto('/settings/work-types');
    const card = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: `Source ${stamp}`, exact: true }) });
    await card.getByText(`Delete Source ${stamp}`, { exact: true }).click();
    const replacement = card.getByLabel('Move work items to', { exact: true });
    await expect(replacement.locator('option')).toHaveText(['Delete only if unused', `Compatible ${stamp}`]);
    await replacement.selectOption({ label: `Compatible ${stamp}` });
    await card.getByRole('button', { name: 'Delete work type', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Work type deleted.');
    await page.goto(`/browse/${issueKey}`);
    await expect(page.getByRole('complementary', { name: 'Issue details', exact: true })).toContainText(`Compatible ${stamp}`);
    await page.reload();
    expect((await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=issuetype`, { headers })).json()).fields.issuetype.id).toBe(kinds.Compatible);
  } finally {
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
    if (typeSchemeID) await page.request.delete(`/rest/api/3/issuetypescheme/${typeSchemeID}`, { headers, timeout: 10_000 });
    if (configurationSchemeID) await page.request.delete(`/rest/api/3/fieldconfigurationscheme/${configurationSchemeID}`, { headers, timeout: 10_000 });
    for (const id of configs) await page.request.delete(`/rest/api/3/fieldconfiguration/${id}`, { headers, timeout: 10_000 });
    for (const id of Object.values(kinds)) await page.request.delete(`/rest/api/3/issuetype/${id}`, { headers, timeout: 10_000 });
  }
});
