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
	expect(issueTypeBody.issueTypes).toEqual(expect.arrayContaining([
		expect.objectContaining({ id: 'it_task', name: 'Task', subtask: false }),
		expect.objectContaining({ id: 'it_subtask', name: 'Sub-task', subtask: true }),
	]));

	let fieldStart = 0;
	let fieldMetaBody: any = { fields: [], total: 0 };
	do {
		const fieldMeta = await request.get(`/rest/api/3/issue/createmeta/ZZ/issuetypes/it_task?startAt=${fieldStart}&maxResults=100`, auth);
		expect(fieldMeta.status()).toBe(200);
		const pageBody = await fieldMeta.json();
		fieldMetaBody.fields.push(...pageBody.fields);
		fieldMetaBody.total = pageBody.total;
		fieldStart += pageBody.fields.length;
	} while (fieldStart < fieldMetaBody.total);
  expect(fieldMetaBody.fields.map((field: any) => field.fieldId)).toEqual(expect.arrayContaining([
		'project', 'issuetype', 'summary', 'description', 'assignee', 'priority', 'labels', 'parent', 'security', customFieldID,
  ]));
	const customMeta = fieldMetaBody.fields.find((field: any) => field.fieldId === customFieldID);
  expect(customMeta.schema).toMatchObject({ type: 'number', customId: Number(customFieldID.replace('customfield_', '')) });
	const subtaskMeta = await (await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes/it_subtask?maxResults=100', auth)).json();
	expect(subtaskMeta.fields.find((field: any) => field.fieldId === 'parent')).toMatchObject({ required: true, schema: { system: 'parent' } });

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

  await page.locator('#global-create-issue').click();
  const subtaskDialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(subtaskDialog).toBeVisible();
  await expect(subtaskDialog).toHaveAttribute('data-ready', '1');
  const [metadataResponse] = await Promise.all([
    page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === 'GET' && url.pathname === '/issues/new' && url.searchParams.get('issuetype') === 'it_subtask';
    }),
    page.selectOption('#create-issuetype', 'it_subtask'),
  ]);
  expect(metadataResponse.ok()).toBe(true);
  await expect(page.locator('#create-issuetype')).toHaveValue('it_subtask');
  await expect(page.locator('#create-parent')).toHaveAttribute('required', '');
  await page.fill('#create-summary', `Hierarchy child ${unique}`);
  await page.locator('.create-more summary').click();
  const parentOption = page.locator('#create-parent option').filter({ hasText: createdKey });
  await page.selectOption('#create-parent', await parentOption.getAttribute('value') as string);
  const previousURL = page.url();
  await Promise.all([
    page.waitForURL(url => url.href !== previousURL && /\/browse\/ZZ-\d+$/.test(url.pathname)),
    subtaskDialog.getByRole('button', { name: 'Create issue', exact: true }).click(),
  ]);
  await expect(page.getByLabel('Parent')).toContainText(createdKey);
  const childKey = page.url().split('/').pop()!;
  const childIssue = await (await request.get(`/rest/api/3/issue/${childKey}`, auth)).json();
  expect(childIssue.fields).toMatchObject({
    parent: { key: createdKey, fields: { summary: `Metadata UI create ${unique}` } },
    issuetype: { id: 'it_subtask', subtask: true },
  });
  await page.goto(`/browse/${createdKey}`);
  await expect(page.getByRole('heading', { name: 'Sub-tasks' })).toBeVisible();
  await expect(page.getByRole('link', { name: new RegExp(`${childKey} Hierarchy child`) })).toBeVisible();
});
