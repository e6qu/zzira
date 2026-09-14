import { expect, test } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

// The journey owns its project and select field, and narrows the field's context
// to that project so the shared demo project keeps its own form.
test('site administrators give a select field options and the create form offers them', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const auth = { Authorization: apiAuthHeader() };
  const projectKey = `OPT${Date.now().toString().slice(-6)}`;
  const projectName = `Release rings ${stamp}`;
  const fieldName = `Release ring ${stamp}`;

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: auth,
    data: { key: projectKey, name: projectName, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);

  const field = await page.request.post('/rest/api/3/field', {
    headers: auth, data: { name: fieldName, type: 'select' },
  });
  expect(field.status()).toBe(201);
  const fieldID = (await field.json()).id;

  // Keep the field to this project so other specs' forms are untouched.
  const contexts = await (await page.request.get(`/rest/api/3/field/${fieldID}/context`, { headers: auth })).json();
  const contextID = String(contexts.values[0].id);
  const scoped = await page.request.put(`/rest/api/3/field/${fieldID}/context/${contextID}/project`, {
    headers: auth, data: { projectIds: [projectID] },
  });
  expect(scoped.status()).toBe(204);

  await page.goto('/settings/custom-fields');
  const card = page.locator(`.custom-field-card[data-field-id="${fieldID}"]`);
  const context = card.locator(`.custom-field-context[data-context-id="${contextID}"]`);
  await expect(context.locator('.context-option-ledger')).toContainText('No options');

  for (const value of ['Canary', 'Broad']) {
    await context.locator('.context-option-create input[name="value"]').fill(value);
    await context.locator('.context-option-create').getByRole('button', { name: 'Add option' }).click();
    await expect(page.getByRole('status')).toContainText('Option added.');
  }

  const optionValues = async () => page.locator(`.custom-field-card[data-field-id="${fieldID}"] .context-option-row strong`).allTextContents();
  expect(await optionValues()).toEqual(['Canary', 'Broad']);

  // Reordering is reflected in the admin list.
  await page.locator(`.custom-field-card[data-field-id="${fieldID}"] .context-option-row`)
    .filter({ hasText: 'Broad' }).getByRole('button', { name: /Move first/ }).click();
  await expect(page.getByRole('status')).toContainText('Option moved.');
  expect(await optionValues()).toEqual(['Broad', 'Canary']);

  // The create form offers exactly those options, in that order.
  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await dialog.locator('.create-more summary').click();
  const select = dialog.locator(`#create-${fieldID}`);
  await expect(select).toBeVisible();
  expect(await select.locator('option').allTextContents()).toEqual(['None', 'Broad', 'Canary']);

  await dialog.getByLabel('Summary', { exact: false }).fill(`Ringed work ${stamp}`);
  await select.selectOption({ label: 'Canary' });
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/browse/${projectKey}-\\d+$`));
  const issueKey = page.url().split('/').pop()!;

  // A disabled option stays on the work item but leaves the form.
  await page.goto('/settings/custom-fields');
  await page.locator(`.custom-field-card[data-field-id="${fieldID}"] .context-option-row`)
    .filter({ hasText: 'Canary' }).getByRole('button', { name: /Disable/ }).click();
  await expect(page.getByRole('status')).toContainText('Option updated.');

  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  await expect(page.getByRole('dialog', { name: 'Create issue' })).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await page.locator('.create-more summary').click();
  expect(await page.locator(`#create-${fieldID} option`).allTextContents()).toEqual(['None', 'Broad']);
  await page.keyboard.press('Escape');

  const stored = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers: auth })).json();
  expect(stored.fields[fieldID]).toBeTruthy();

  await page.goto('/settings/custom-fields');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
});

// A multi-select field offers its options as a multiple choice and keeps every
// chosen option on the work item.
test('a multi-select field keeps every chosen option', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const auth = { Authorization: apiAuthHeader() };
  const projectKey = `MSE${Date.now().toString().slice(-6)}`;
  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: auth,
    data: { key: projectKey, name: `Platforms ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);

  const field = await page.request.post('/rest/api/3/field', {
    headers: auth, data: { name: `Platforms ${stamp}`, type: 'com.atlassian.jira.plugin.system.customfieldtypes:multiselect' },
  });
  expect(field.status()).toBe(201);
  const fieldID = (await field.json()).id;
  const contexts = await (await page.request.get(`/rest/api/3/field/${fieldID}/context`, { headers: auth })).json();
  const contextID = String(contexts.values[0].id);
  expect((await page.request.put(`/rest/api/3/field/${fieldID}/context/${contextID}/project`, { headers: auth, data: { projectIds: [projectID] } })).status()).toBe(204);
  const options = await page.request.post(`/rest/api/3/field/${fieldID}/context/${contextID}/option`, {
    headers: auth, data: { options: [{ value: 'iOS' }, { value: 'Android' }, { value: 'Web' }] },
  });
  expect(options.status()).toBe(200);
  const ids = Object.fromEntries((await options.json()).options.map((option: { id: string; value: string }) => [option.value, String(option.id)]));

  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await dialog.locator('.create-more summary').click();
  const select = dialog.locator(`#create-${fieldID}`);
  await expect(select).toHaveAttribute('multiple', '');
  expect(await select.locator('option').allTextContents()).toEqual(['iOS', 'Android', 'Web']);
  await dialog.getByLabel('Summary', { exact: false }).fill(`Ship everywhere ${stamp}`);
  await select.selectOption([{ label: 'iOS' }, { label: 'Web' }]);
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/browse/${projectKey}-\\d+$`));
  const issueKey = page.url().split('/').pop()!;

  const stored = await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=${fieldID}`, { headers: auth })).json();
  expect(stored.fields[fieldID].map((option: { id: string; value: string }) => [option.id, option.value])).toEqual([[ids.iOS, 'iOS'], [ids.Web, 'Web']]);
  const found = await (await page.request.get(`/rest/api/3/search/jql?fields=summary&jql=${encodeURIComponent(`${fieldID} = ${ids.Web} AND project = ${projectKey}`)}`, { headers: auth })).json();
  expect(found.issues.map((issue: { key: string }) => issue.key)).toEqual([issueKey]);
});

// The create form offers people, groups, dates, links and a cascading select's
// first-level options, and issue responses describe the chosen values.
test('the create form offers Jira field types and keeps their values', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const auth = { Authorization: apiAuthHeader() };
  const projectKey = `FT${Date.now().toString().slice(-6)}`;
  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: auth,
    data: { key: projectKey, name: `Field types ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);
  const group = await page.request.post('/rest/api/3/group', { headers: auth, data: { name: `on-call-${stamp}` } });
  expect(group.status()).toBe(201);
  const groupID = (await group.json()).groupId;

  const field = async (name: string, type: string) => {
    const response = await page.request.post('/rest/api/3/field', {
      headers: auth, data: { name: `${name} ${stamp}`, type: `com.atlassian.jira.plugin.system.customfieldtypes:${type}` },
    });
    expect(response.status()).toBe(201);
    const id = (await response.json()).id;
    const contexts = await (await page.request.get(`/rest/api/3/field/${id}/context`, { headers: auth })).json();
    const contextID = String(contexts.values[0].id);
    expect((await page.request.put(`/rest/api/3/field/${id}/context/${contextID}/project`, { headers: auth, data: { projectIds: [projectID] } })).status()).toBe(204);
    return { id, contextID };
  };
  const owner = await field('Owner', 'userpicker');
  const rota = await field('Rota', 'grouppicker');
  const launch = await field('Launch', 'datepicker');
  const runbook = await field('Runbook', 'url');
  const region = await field('Region', 'cascadingselect');
  const options = await page.request.post(`/rest/api/3/field/${region.id}/context/${region.contextID}/option`, {
    headers: auth, data: { options: [{ value: 'Europe' }, { value: 'Asia' }] },
  });
  expect(options.status()).toBe(200);
  const europe = String((await options.json()).options[0].id);
  expect((await page.request.post(`/rest/api/3/field/${region.id}/context/${region.contextID}/option`, {
    headers: auth, data: { options: [{ value: 'Berlin', optionId: europe }] },
  })).status()).toBe(200);

  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await dialog.locator('.create-more summary').click();
  await dialog.getByLabel('Summary', { exact: false }).fill(`Launch readiness ${stamp}`);
  await dialog.locator(`#create-${owner.id}`).selectOption(me.accountId);
  await dialog.locator(`#create-${rota.id}`).selectOption(groupID);
  await expect(dialog.locator(`#create-${launch.id}`)).toHaveAttribute('type', 'date');
  await dialog.locator(`#create-${launch.id}`).fill('2026-11-02');
  await expect(dialog.locator(`#create-${runbook.id}`)).toHaveAttribute('type', 'url');
  await dialog.locator(`#create-${runbook.id}`).fill('https://runbooks.example.test/launch');
  expect(await dialog.locator(`#create-${region.id} option`).allTextContents()).toEqual(['None', 'Europe', 'Asia']);
  await dialog.locator(`#create-${region.id}`).selectOption({ label: 'Europe' });
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/browse/${projectKey}-\\d+$`));
  const issueKey = page.url().split('/').pop()!;

  const stored = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers: auth })).json();
  expect(stored.fields[owner.id].accountId).toBe(me.accountId);
  expect(stored.fields[rota.id]).toMatchObject({ groupId: groupID, name: `on-call-${stamp}` });
  expect(stored.fields[launch.id]).toBe('2026-11-02');
  expect(stored.fields[runbook.id]).toBe('https://runbooks.example.test/launch');
  expect(stored.fields[region.id]).toMatchObject({ id: europe, value: 'Europe' });
  const search = await (await page.request.get(`/rest/api/3/search/jql?fields=summary&jql=${encodeURIComponent(`${owner.id} = currentUser() AND ${region.id} = Europe AND project = ${projectKey}`)}`, { headers: auth })).json();
  expect(search.issues.map((issue: { key: string }) => issue.key)).toEqual([issueKey]);
});
