import { expect, test } from '@playwright/test';
import { apiAuthHeader, seedPassword } from './auth';

test('restricted relationships stay out of issue pages and parent choices', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const owner = await (await request.get('/rest/api/3/myself', { headers })).json();
  const stamp = Date.now().toString(36).toUpperCase();
  const projectKey = `RV${stamp}`;
  const projectResponse = await request.post('/rest/api/3/project', { headers, data: {
    key: projectKey, name: `Relationship visibility ${stamp}`, projectTypeKey: 'software', leadAccountId: owner.accountId,
  } });
  expect(projectResponse.status()).toBe(201);
  const project = await projectResponse.json();
  const create = async (summary: string, type: string, parent?: string) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields: {
      project: { key: projectKey }, summary, issuetype: { name: type }, ...(parent ? { parent: { key: parent } } : {}),
    } } });
    expect(response.status()).toBe(201);
    return (await response.json()).key as string;
  };
  const publicEpic = await create('Visible parent choice', 'Epic');
  const privateEpic = await create('Confidential parent choice', 'Epic');
  const story = await create('Public story with a restricted parent', 'Story', privateEpic);
  const task = await create('Public task with mixed children', 'Task', publicEpic);
  const publicChild = await create('Visible child work', 'Sub-task', task);
  const privateChild = await create('Confidential child work', 'Sub-task', task);
  const schemeResponse = await request.post('/rest/api/3/issuesecurityschemes', { headers, data: {
    name: `Private relationships ${stamp}`, levels: [{ name: 'Owner only', members: [{ type: 'user', parameter: owner.accountId }] }],
  } });
  expect(schemeResponse.status()).toBe(201);
  const scheme = await schemeResponse.json();
  const detail = await (await request.get(`/rest/api/3/issuesecurityschemes/${scheme.id}`, { headers })).json();
  const levelID = detail.levels[0].id;
  const assigned = await request.put('/rest/api/3/issuesecurityschemes/project', { headers, maxRedirects: 0, data: {
    projectId: String(project.id), schemeId: String(scheme.id), oldToNewSecurityLevelMappings: [],
  } });
  expect(assigned.status()).toBe(303);
  await expect.poll(async () => String((await (await request.get(`/rest/api/3/project/${projectKey}/issuesecuritylevelscheme`, { headers })).json()).id)).toBe(String(scheme.id));
  for (const key of [privateEpic, privateChild]) {
    expect((await request.put(`/rest/api/3/issue/${key}`, { headers, data: { fields: { security: { id: levelID } } } })).status()).toBe(204);
  }

  await page.goto('/login');
  await page.locator('#login-email').fill('ana@zzira.dev');
  await page.locator('#login-password').fill(seedPassword('ana@zzira.dev'));
  await page.locator('button[type=submit]').click();
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto(`/browse/${story}`);
  await expect(page.locator('.issue-summary')).toHaveText('Public story with a restricted parent');
  await expect(page.locator('body')).not.toContainText('Confidential parent choice');
  await expect(page.locator('#field-parent')).not.toContainText('Confidential parent choice');
  await expect(page.locator('#field-parent')).toContainText('Visible parent choice');
  await page.goto(`/browse/${task}`);
  await expect(page.locator('body')).toContainText('Visible child work');
  await expect(page.locator('body')).not.toContainText('Confidential child work');
  await expect(page.locator('body')).not.toContainText(privateChild);
  const childRead = await page.request.get(`/rest/api/3/issue/${task}?fields=subtasks`);
  const children = (await childRead.json()).fields.subtasks;
  expect(children.map((child: any) => child.key)).toEqual([publicChild]);
  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog).not.toContainText('Confidential parent choice');
  await expect(dialog).not.toContainText('Confidential child work');
});
