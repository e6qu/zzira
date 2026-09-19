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

  // A retrying assertion, not a snapshot: every move and delete reloads the
  // settings page, and reading the list once returns whatever the page held
  // at that instant -- empty, on a page carrying a site's worth of fields.
  const optionValues = page.locator(`.custom-field-card[data-field-id="${fieldID}"] .context-option-row strong`);
  // A row names the other options too, in the replacement list its delete
  // form offers, so a row is found by its own first cell rather than by any
  // text it contains.
  const optionRow = (value: string) => page.locator(`.custom-field-card[data-field-id="${fieldID}"] .context-option-row`)
    .filter({ has: page.locator('span[role="cell"] > strong', { hasText: value }) });
  await expect(optionValues).toHaveText(['Canary', 'Broad']);

  // Reordering is reflected in the admin list.
  await optionRow('Broad').getByRole('button', { name: /Move first/ }).click();
  await expect(page.getByRole('status')).toContainText('Option moved.');
  await expect(optionValues).toHaveText(['Broad', 'Canary']);

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
  await optionRow('Canary').getByRole('button', { name: /Disable/ }).click();
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

  // Reordering runs both ways, not just "move first", and an option the field
  // no longer needs is deleted here rather than through the API. The work
  // item holds Broad, so deleting Broad has to move it to another option.
  await optionRow('Broad').getByRole('button', { name: /Move down/ }).click();
  await expect(optionValues).toHaveText(['Canary', 'Broad']);
  await optionRow('Broad').getByRole('button', { name: /Move up/ }).click();
  await expect(optionValues).toHaveText(['Broad', 'Canary']);

  await optionRow('Broad').locator('summary').click();
  const removeBroad = optionRow('Broad').locator('form').filter({ has: page.getByRole('button', { name: 'Delete option', exact: true }) });
  await removeBroad.getByLabel('Replace it on work items with').selectOption({ label: 'Canary' });
  await removeBroad.getByRole('button', { name: 'Delete option', exact: true }).click();
  await expect(optionValues).toHaveText(['Canary']);
  const moved = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers: auth })).json();
  expect(moved.fields[fieldID].value).toBe('Canary');

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

// The issue page edits pickers with real choices and shows names, not ids.
test('the issue page edits custom field pickers', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const auth = { Authorization: apiAuthHeader() };
  const projectKey = `PK${Date.now().toString().slice(-6)}`;
  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: auth,
    data: { key: projectKey, name: `Pickers ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);
  for (const name of ['1.0', '2.0']) {
    expect((await page.request.post('/rest/api/3/version', { headers: auth, data: { project: projectKey, name } })).status()).toBe(201);
  }
  const field = async (name: string, type: string) => {
    const response = await page.request.post('/rest/api/3/field', {
      headers: auth, data: { name: `${name} ${stamp}`, type: `com.atlassian.jira.plugin.system.customfieldtypes:${type}` },
    });
    expect(response.status()).toBe(201);
    const id = (await response.json()).id;
    const contexts = await (await page.request.get(`/rest/api/3/field/${id}/context`, { headers: auth })).json();
    const contextID = String(contexts.values[0].id);
    expect((await page.request.put(`/rest/api/3/field/${id}/context/${contextID}/project`, { headers: auth, data: { projectIds: [projectID] } })).status()).toBe(204);
    return { id, contextID, name: `${name} ${stamp}` };
  };
  const owner = await field('Owner', 'userpicker');
  const shipped = await field('Shipped in', 'multiversion');
  const region = await field('Region', 'cascadingselect');
  const parents = await (await page.request.post(`/rest/api/3/field/${region.id}/context/${region.contextID}/option`, {
    headers: auth, data: { options: [{ value: 'Europe' }] },
  })).json();
  const europe = String(parents.options[0].id);
  await page.request.post(`/rest/api/3/field/${region.id}/context/${region.contextID}/option`, { headers: auth, data: { options: [{ value: 'Berlin', optionId: europe }] } });

  const issue = await page.request.post('/rest/api/3/issue', {
    headers: auth, data: { fields: { project: { key: projectKey }, summary: `Picked work ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(issue.status()).toBe(201);
  const issueKey = (await issue.json()).key;

  await page.goto(`/browse/${issueKey}`);
  // Saving a field re-renders the issue; the next field is used only once that
  // render has replaced the page the save was made from.
  const save = async (name: string) => {
    const root = await page.locator('#issue-root').elementHandle();
    await Promise.all([
      page.waitForResponse((response) => response.url().endsWith(`/issues/${issueKey}/fields`) && response.request().method() === 'POST'),
      page.getByRole('button', { name: `Save ${name}` }).click(),
    ]);
    await expect.poll(() => root!.evaluate((element) => !element.isConnected)).toBe(true);
  };
  // Saving a field re-renders the page, which may keep the section open.
  const openMoreFields = async () => {
    await expect(async () => {
      const section = page.locator('.more-fields');
      if (!(await section.evaluate((element) => (element as HTMLDetailsElement).open))) {
        await section.locator('summary').click();
      }
      await expect(section).toHaveJSProperty('open', true, { timeout: 1000 });
    }).toPass();
  };
  await openMoreFields();
  const ownerControl = page.locator(`#detail-field-${owner.id}`);
  await expect(ownerControl).toHaveJSProperty('tagName', 'SELECT');
  await ownerControl.selectOption(me.accountId);
  await save(owner.name);
  await expect(page.locator(`#detail-field-${owner.id}`)).toHaveValue(me.accountId);

  await openMoreFields();
  const regionControl = page.locator(`#detail-field-${region.id}`);
  expect(await regionControl.locator('option').allTextContents()).toEqual(['None', 'Europe', 'Europe › Berlin']);
  await regionControl.selectOption({ label: 'Europe › Berlin' });
  await save(region.name);
  await expect(page.locator(`#detail-field-${region.id} option:checked`)).toHaveText('Europe › Berlin');

  await openMoreFields();
  const shippedControl = page.locator(`#detail-field-${shipped.id}`);
  await expect(shippedControl).toHaveAttribute('multiple', '');
  await shippedControl.selectOption([{ label: '1.0' }, { label: '2.0' }]);
  await save(shipped.name);
  await expect(page.locator(`#detail-field-${shipped.id} option:checked`)).toHaveText(['1.0', '2.0']);

  const stored = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers: auth })).json();
  expect(stored.fields[owner.id].accountId).toBe(me.accountId);
  expect(stored.fields[region.id]).toMatchObject({ id: europe, value: 'Europe', child: { value: 'Berlin' } });
  expect(stored.fields[shipped.id].map((version: { name: string }) => version.name)).toEqual(['1.0', '2.0']);
});
