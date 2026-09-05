import { expect, test, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('create journey and createmeta share every supported field', async ({ page, request }) => {
  const auth = { headers: { Authorization: apiAuthHeader() } };
  const demoID = (await (await request.get('/rest/api/3/myself', auth)).json()).accountId;
  const unique = Date.now().toString();

  const fieldResponse = await request.post('/rest/api/3/field', {
    ...auth,
    data: { name: `Create score ${unique}`, type: 'number', description: 'Relative delivery effort.' },
  });
  expect(fieldResponse.status()).toBe(201);
  const customFieldID = (await fieldResponse.json()).id;

  const schemeID = `create_scheme_${unique}`;
  const levelID = `create_private_${unique}`;
  expect((await request.post('/rest/api/3/issuesecurityschemes', {
    ...auth,
    data: { id: schemeID, name: 'Create journey scheme', levels: [{ id: levelID, name: 'Private', members: [demoID] }] },
  })).status()).toBe(201);
  expect((await request.put('/rest/api/3/issuesecurityschemes/project/ZZ', {
    ...auth, data: { id: schemeID },
  })).status()).toBe(204);

  const issueTypes = await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes', auth);
  expect(issueTypes.status()).toBe(200);
  const issueTypeBody = await issueTypes.json();
  expect(issueTypeBody.issueTypes).toEqual(expect.arrayContaining([expect.objectContaining({ id: 'it_task', name: 'Task' })]));

  const fieldMeta = await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes/it_task?maxResults=100', auth);
  expect(fieldMeta.status()).toBe(200);
  const fieldMetaBody = await fieldMeta.json();
  expect(fieldMetaBody.fields.map((field: any) => field.fieldId)).toEqual(expect.arrayContaining([
    'project', 'issuetype', 'summary', 'description', 'assignee', 'priority', 'labels', 'security', customFieldID,
  ]));
  const customMeta = fieldMetaBody.fields.find((field: any) => field.fieldId === customFieldID);
  expect(customMeta.schema).toMatchObject({ type: 'number', customId: Number(customFieldID.replace('customfield_', '')) });

  const legacyMeta = await request.get('/rest/api/3/issue/createmeta?projectKeys=ZZ&issuetypeIds=it_task&expand=projects.issuetypes.fields', auth);
  expect(legacyMeta.status()).toBe(200);
  const legacyBody = await legacyMeta.json();
  expect(legacyBody.projects).toHaveLength(1);
  expect(legacyBody.projects[0].issuetypes[0].fields).toHaveProperty(customFieldID);
  expect((await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes/not-a-type', auth)).status()).toBe(400);

  const apiSummary = `Metadata API create ${unique}`;
  const apiCreated = await request.post('/rest/api/3/issue', {
    ...auth,
    data: { fields: {
      project: { id: 'prj_default' }, summary: apiSummary, issuetype: { id: 'it_task' },
      assignee: { accountId: demoID }, priority: { id: 'pr_medium' }, labels: ['api-create'],
      security: { id: levelID }, [customFieldID]: 5,
    } },
  });
  expect(apiCreated.status()).toBe(201);
  const apiIssue = await (await request.get(`/rest/api/3/issue/${(await apiCreated.json()).key}`, auth)).json();
  expect(apiIssue.fields).toMatchObject({ labels: ['api-create'], security: { id: levelID }, [customFieldID]: 5 });

  await login(page);
  await page.goto('/projects/ZZ');
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await page.fill('#create-summary', `Metadata UI create ${unique}`);
  await page.fill('#create-description', 'Created from the shared field schema.');
  await page.locator('.create-more summary').click();
  await page.selectOption('#create-assignee', demoID);
  await page.selectOption('#create-priority', 'pr_medium');
  await page.fill('#create-labels', 'create-parity, ui');
  await page.selectOption('#create-security', levelID);
  await page.fill(`#create-${customFieldID}`, '8');
  await page.getByRole('checkbox', { name: 'Create another' }).check();
  await dialog.getByRole('button', { name: 'Create issue' }).click();

  await expect(page.locator('.create-success strong')).toContainText('created');
  const createdKey = (await page.locator('.create-success strong').textContent())!.split(' ')[0];
  await expect(page.locator('#create-summary')).toBeFocused();
  await expect(page.locator('#create-summary')).toHaveValue('');
  await expect(page.getByRole('checkbox', { name: 'Create another' })).toBeChecked();
  const uiIssue = await (await request.get(`/rest/api/3/issue/${createdKey}`, auth)).json();
  expect(uiIssue.fields).toMatchObject({ labels: ['create-parity', 'ui'], security: { id: levelID }, [customFieldID]: 8 });

  const retrySummary = `Validation retry ${unique}`;
  await page.fill('#create-summary', retrySummary);
  await page.locator('.create-more summary').click();
  await page.fill('#create-labels', 'two words');
  await dialog.getByRole('button', { name: 'Create issue' }).click();
  await expect(page.getByRole('alert')).toContainText('labels must be');
  await expect(page.locator('#create-summary')).toHaveValue(retrySummary);
  await expect(page.locator('#create-labels')).toHaveValue('two words');

  await page.fill('#create-labels', 'validated');
  await page.getByRole('checkbox', { name: 'Create another' }).uncheck();
  await dialog.getByRole('button', { name: 'Create issue' }).click();
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  await expect(page.locator('.issue-summary')).toHaveText(retrySummary);
});
