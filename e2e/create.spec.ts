import { apiAuthHeader } from './auth';
import { APIRequestContext, expect, test, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

// useSecurityLevel creates a scheme with one level only the account can see,
// associates it with the project through Jira's mapping task, and returns the
// level's id.
async function useSecurityLevel(request: APIRequestContext, auth: { headers: { Authorization: string } }, projectID: string, name: string, accountID: string): Promise<string> {
  const created = await request.post('/rest/api/3/issuesecurityschemes', {
    ...auth,
    data: { name, levels: [{ name: 'Private', members: [{ type: 'user', parameter: accountID }] }] },
  });
  expect(created.status()).toBe(201);
  const schemeID = String((await created.json()).id);
  const scheme = await (await request.get(`/rest/api/3/issuesecurityschemes/${schemeID}`, auth)).json();
  const levelID = String(scheme.levels[0].id);
  const current = await (await request.get(`/rest/api/3/issuesecurityschemes/project?projectId=${projectID}`, auth)).json();
  const mappings: { oldLevelId: string; newLevelId: string }[] = [];
  for (const association of current.values ?? []) {
    const previous = await (await request.get(`/rest/api/3/issuesecurityschemes/${association.issueSecuritySchemeId}`, auth)).json();
    for (const level of previous.levels ?? []) mappings.push({ oldLevelId: String(level.id), newLevelId: levelID });
  }
  const assigned = await request.put('/rest/api/3/issuesecurityschemes/project', {
    ...auth,
    maxRedirects: 0,
    data: { projectId: projectID, schemeId: schemeID, oldToNewSecurityLevelMappings: mappings },
  });
  expect(assigned.status()).toBe(303);
  await expect.poll(async () => String((await (await request.get(`/rest/api/3/issuesecurityschemes/project?projectId=${projectID}`, auth)).json()).values?.[0]?.issueSecuritySchemeId)).toBe(schemeID);
  return levelID;
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
  const reporterID = (await (await request.get('/rest/api/3/myself', { headers: { Authorization: apiAuthHeader('ana@zzira.dev') } })).json()).accountId;
  const unique = Date.now().toString();

	const fieldResponse = await request.post('/rest/api/3/field', {
    ...auth,
    data: { name: `Create score ${unique}`, type: 'number', description: 'Relative delivery effort.' },
  });
  expect(fieldResponse.status()).toBe(201);
  const customFieldID = (await fieldResponse.json()).id;

  const projectID = String((await (await request.get('/rest/api/3/project/ZZ', auth)).json()).id);
  const levelID = await useSecurityLevel(request, auth, projectID, `Create journey ${unique}`, demoID);

  const issueTypes = await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes', auth);
  expect(issueTypes.status()).toBe(200);
  const issueTypeBody = await issueTypes.json();
	expect(issueTypeBody.issueTypes).toEqual(expect.arrayContaining([
		expect.objectContaining({ id: '10002', name: 'Task', subtask: false }),
		expect.objectContaining({ id: '10003', name: 'Sub-task', subtask: true }),
	]));

	let fieldStart = 0;
	let fieldMetaBody: any = { fields: [], total: 0 };
	do {
		const fieldMeta = await request.get(`/rest/api/3/issue/createmeta/ZZ/issuetypes/10002?startAt=${fieldStart}&maxResults=100`, auth);
		expect(fieldMeta.status()).toBe(200);
		const pageBody = await fieldMeta.json();
		fieldMetaBody.fields.push(...pageBody.fields);
		fieldMetaBody.total = pageBody.total;
		fieldStart += pageBody.fields.length;
	} while (fieldStart < fieldMetaBody.total);
  expect(fieldMetaBody.fields.map((field: any) => field.fieldId)).toEqual(expect.arrayContaining([
		'project', 'issuetype', 'summary', 'description', 'assignee', 'reporter', 'priority', 'labels', 'parent', 'security', customFieldID,
  ]));
  expect(fieldMetaBody.fields.find((field: any) => field.fieldId === 'reporter')).toMatchObject({ required: false, defaultValue: { accountId: demoID }, allowedValues: expect.arrayContaining([expect.objectContaining({ accountId: reporterID })]) });
	const customMeta = fieldMetaBody.fields.find((field: any) => field.fieldId === customFieldID);
  expect(customMeta.schema).toMatchObject({ type: 'number', customId: Number(customFieldID.replace('customfield_', '')) });
	const subtaskMeta = await (await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes/10003?maxResults=100', auth)).json();
	expect(subtaskMeta.fields.find((field: any) => field.fieldId === 'parent')).toMatchObject({ required: true, schema: { system: 'parent' } });

  const legacyMeta = await request.get('/rest/api/3/issue/createmeta?projectKeys=ZZ&issuetypeIds=10002&expand=projects.issuetypes.fields', auth);
  expect(legacyMeta.status()).toBe(200);
  const legacyBody = await legacyMeta.json();
  expect(legacyBody.projects).toHaveLength(1);
  expect(legacyBody.projects[0].issuetypes[0].fields).toHaveProperty(customFieldID);
  expect((await request.get('/rest/api/3/issue/createmeta/ZZ/issuetypes/not-a-type', auth)).status()).toBe(400);

  const apiSummary = `Metadata API create ${unique}`;
  const apiCreated = await request.post('/rest/api/3/issue', {
    ...auth,
    data: { fields: {
      project: { id: projectID }, summary: apiSummary, issuetype: { id: '10002' },
      assignee: { accountId: demoID }, reporter: { accountId: reporterID }, priority: { id: '3' }, labels: ['api-create'],
      security: { id: levelID }, [customFieldID]: 5,
    } },
  });
  expect(apiCreated.status()).toBe(201);
  const apiIssue = await (await request.get(`/rest/api/3/issue/${(await apiCreated.json()).key}`, auth)).json();
  expect(apiIssue.fields).toMatchObject({ labels: ['api-create'], reporter: { accountId: reporterID }, security: { id: levelID }, [customFieldID]: 5 });

  await login(page);
  await page.goto('/projects/ZZ');
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await page.fill('#create-summary', `Metadata UI create ${unique}`);
  await page.fill('#create-description', 'Created from the shared field schema.');
  await page.locator('.create-more summary').click();
  await expect(page.locator('#create-reporter')).toHaveValue(demoID);
  await page.selectOption('#create-reporter', reporterID);
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
  expect(uiIssue.fields).toMatchObject({ labels: ['create-parity', 'ui'], reporter: { accountId: reporterID }, security: { id: levelID }, [customFieldID]: 8 });

  const retrySummary = `Validation retry ${unique}`;
  await page.fill('#create-summary', retrySummary);
  await page.locator('.create-more summary').click();
  await expect(page.locator('#create-reporter')).toHaveValue(demoID);
  await page.selectOption('#create-reporter', reporterID);
  await page.fill('#create-labels', 'two words');
  await dialog.getByRole('button', { name: 'Create issue' }).click();
  await expect(page.getByRole('alert')).toContainText('labels must be');
  await expect(page.locator('#create-summary')).toHaveValue(retrySummary);
  await expect(page.locator('#create-labels')).toHaveValue('two words');
  await expect(page.locator('#create-reporter')).toHaveValue(reporterID);

  await page.fill('#create-labels', 'validated');
  await page.getByRole('checkbox', { name: 'Create another' }).uncheck();
  await dialog.getByRole('button', { name: 'Create issue' }).click();
  // Landing on the new work item is a whole page load, which boots the
  // offline worker; on a machine running the rest of this suite that can
  // take longer than the five seconds an expectation waits by default.
  await page.waitForURL(/\/browse\/ZZ-\d+$/, { timeout: 30_000 });
  await expect(page.locator('.issue-summary')).toHaveText(retrySummary);

  // A visible dialog must accept field changes before HTMX's deferred settle.
  await page.locator('#global-create-issue').evaluate(trigger => trigger.setAttribute('hx-swap', 'innerHTML settle:5s'));
  await page.locator('#global-create-issue').click();
  const subtaskDialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(subtaskDialog).toBeVisible();
  await expect(subtaskDialog).toHaveAttribute('data-ready', '1');
  const [metadataResponse] = await Promise.all([
    page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === 'GET' && url.pathname === '/issues/new' && url.searchParams.get('issuetype') === 'it_subtask';
    }, { timeout: 10_000 }),
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
    issuetype: { id: '10003', subtask: true },
  });
  await page.goto(`/browse/${createdKey}`);
  await expect(page.getByRole('heading', { name: 'Sub-tasks' })).toBeVisible();
  await expect(page.getByRole('link', { name: new RegExp(`${childKey} Hierarchy child`) })).toBeVisible();
});

test('create metadata refresh pauses input and retains the draft after refusal', async ({ page, request }) => {
  await login(page);
  await page.goto('/projects/ZZ');
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue', exact: true });
  await expect(dialog).toHaveAttribute('data-ready', '1');
  await dialog.locator('form').evaluate(form => form.setAttribute('data-initial-refresh', 'true'));
  await page.selectOption('#create-issuetype', 'it_task');
  await expect(dialog.locator('form')).not.toHaveAttribute('data-initial-refresh', 'true');
  await expect(dialog).toHaveAttribute('data-ready', '1');
  const summary = `Refresh draft ${Date.now()}`;
  await page.locator('#create-summary').fill(summary);
  await page.locator('#create-description').fill('Retain this context.');
  let mode: 'hold' | 'fail' | 'pass' = 'hold';
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route('**/issues/new?**', async route => {
    if (mode === 'hold') {
      await held;
      const response = await route.fetch();
      await route.fulfill({ response });
    } else if (mode === 'fail') {
      await route.fulfill({ status: 503, contentType: 'text/plain', body: 'Metadata unavailable' });
    } else await route.continue();
  });
  let key = '';
  try {
    await page.selectOption('#create-issuetype', 'it_subtask');
    await expect(dialog.locator('form')).toHaveAttribute('aria-busy', 'true');
    await expect(page.locator('#create-summary')).toBeDisabled();
    await expect(page.locator('#create-description')).toBeDisabled();
    await expect(page.locator('#create-project')).toBeDisabled();
    await expect(dialog.getByRole('button', { name: 'Create issue', exact: true })).toBeDisabled();
    await expect(dialog.getByRole('status')).toHaveText('Updating fields…');
    release();
    await expect(page.locator('#create-parent')).toBeVisible();
    await expect(page.locator('#create-summary')).toBeEnabled();
    await expect(page.locator('#create-summary')).toHaveValue(summary);
    await expect(page.locator('#create-description')).toHaveValue('Retain this context.');
    mode = 'fail';
    await page.selectOption('#create-issuetype', 'it_task');
    await expect(dialog.locator('[data-request-error]')).toContainText('Could not complete this action');
    await expect(page.locator('#create-issuetype')).toHaveValue('it_subtask');
    await expect(page.locator('#create-summary')).toBeEnabled();
    await expect(page.locator('#create-summary')).toHaveValue(summary);
    await expect(page.locator('#create-description')).toHaveValue('Retain this context.');
    mode = 'pass';
    await dialog.locator('form').evaluate(form => form.setAttribute('data-retry-refresh', 'true'));
    await page.selectOption('#create-issuetype', 'it_task');
    await expect(dialog.locator('form')).not.toHaveAttribute('data-retry-refresh', 'true');
    await expect(page.locator('#create-summary')).toBeEnabled();
    await expect(page.locator('#create-summary')).toHaveValue(summary);
    await expect(page.locator('#create-description')).toHaveValue('Retain this context.');
    await Promise.all([
      page.waitForURL(/\/browse\/ZZ-\d+$/, { timeout: 30_000 }),
      dialog.getByRole('button', { name: 'Create issue', exact: true }).click(),
    ]);
    key = page.url().split('/').pop()!;
    const fetched = await request.get(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() } });
    expect(fetched.status()).toBe(200);
    expect((await fetched.json()).fields).toMatchObject({ summary, issuetype: { name: 'Task' }, description: { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Retain this context.' }] }] } });
  } finally {
    release();
    if (key) await request.delete(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() }, timeout: 10_000 });
  }
});
