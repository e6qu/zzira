import { apiAuthHeader } from './auth';
import { test, expect, Page } from '@playwright/test';
import axe from 'axe-core';
import { scopeFixtureField } from './field_contexts';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('a transition collects the fields of the screen it names, and follows that screen', async ({ page }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();

  // A screen of its own, with one field on it.
  await page.goto('/settings/screens');
  const createScreen = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create screen' }) }).first();
  await createScreen.getByLabel('Screen name').fill(`Finish screen ${stamp}`);
  await createScreen.getByRole('button', { name: 'Create screen' }).click();
  const screenCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: `Finish screen ${stamp}` }) });
  await expect(screenCard).toBeVisible();
  const headers = { Authorization: apiAuthHeader() };
  const screens = await (await page.request.get(`/rest/api/3/screens?queryString=${encodeURIComponent(`Finish screen ${stamp}`)}`, { headers })).json();
  const screenID = String(screens.values.find((screen: { name: string }) => screen.name === `Finish screen ${stamp}`).id);
  const tabCard = screenCard.locator('.screen-tab-card').first();
  await tabCard.getByLabel(/Add a field/).selectOption('resolution');
  await tabCard.getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  // A project and workflow of its own, whose Done transition uses that screen.
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `TS${stamp}`;
  await page.getByLabel('Name', { exact: true }).fill('Transition screen project');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  const projectResponse = await page.request.get(`/rest/api/3/project/${key}`, { headers: { Authorization: apiAuthHeader() } });
  const projectID = (await projectResponse.json()).id as string;

  await page.goto('/settings/workflows');
  await page.fill('#workflow-name', `Screen-backed workflow ${stamp}`);
  await page.selectOption('#workflow-project-scope', projectID);
  await page.getByRole('button', { name: 'Create workflow' }).click();
  await expect(page).toHaveURL(/\/settings\/workflows\/workflow_/);

  await page.fill('#transition-name', 'Finish');
  await page.selectOption('#transition-from', 'st_todo');
  await page.selectOption('#transition-to', 'st_done');
  await page.selectOption('#transition-screen-id', { label: `Finish screen ${stamp}` });
  await page.getByRole('button', { name: 'Add transition' }).click();
  await expect(page.locator('.workflow-node').getByText('Finish', { exact: true })).toBeVisible();
  // Two actions may share a screen; labels and panels must still be unique.
  await page.fill('#transition-name', 'Finish later');
  await page.selectOption('#transition-from', 'st_todo');
  await page.selectOption('#transition-to', 'st_inprogress');
  await page.selectOption('#transition-screen-id', { label: `Finish screen ${stamp}` });
  await page.getByRole('button', { name: 'Add transition' }).click();
  await expect(page.locator('.workflow-node').getByText('Finish later', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Publish workflow' }).click();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();

  const fieldIDs: Record<string, string> = {};
  let optionIDs: string[] = [];
  for (const type of ['number', 'multiselect']) {
    const response = await page.request.post('/rest/api/3/field', { headers, data: { name: `${type} transition ${stamp}`, type } });
    expect(response.status()).toBe(201);
    const fieldID = String((await response.json()).id);
    fieldIDs[type] = fieldID;
    const contexts = await (await page.request.get(`/rest/api/3/field/${fieldID}/context`, { headers })).json();
    const contextID = await scopeFixtureField(page.request, fieldID, String(contexts.values[0].id), projectID, headers);
    if (type === 'multiselect') {
      const options = await page.request.post(`/rest/api/3/field/${fieldID}/context/${contextID}/option`, { headers, data: { options: [{ value: 'One' }, { value: 'Two' }] } });
      expect(options.status()).toBe(200);
      optionIDs = (await options.json()).options.map((option: { id: string }) => String(option.id));
    }
  }
  const rich = { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Retained rich transition', marks: [{ type: 'strong' }] }] }] };
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const created = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { project: { key }, summary: `Screen-backed work ${stamp}`, issuetype: { name: 'Task' }, assignee: { accountId: me.accountId }, description: rich, environment: rich, [fieldIDs.number]: 2, [fieldIDs.multiselect]: [optionIDs[0]] } },
  });
  expect(created.status()).toBe(201);
  const issueKey = (await created.json()).key as string;
  const initial = (await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=assignee,priority`, { headers })).json()).fields;

  // The transition asks for the screen's field, and not for one it does not hold.
  await page.goto(`/browse/${issueKey}`);
  const dialog = page.locator('details.transition-screen').filter({ has: page.locator('summary').filter({ hasText: /^Finish$/ }) });
  await dialog.locator('summary').click();
  await expect(dialog.getByLabel('Resolution')).toBeVisible();
  await expect(dialog.getByLabel('Labels')).toHaveCount(0);
  await expect(dialog.getByRole('tablist')).toHaveCount(0);

  // Adding a field to the screen changes the transition, with no workflow edit.
  await page.goto('/settings/screens');
  const card = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: `Finish screen ${stamp}` }) });
  await card.locator('.screen-tab-card').first().getByLabel(/Add a field/).selectOption('labels');
  await card.locator('.screen-tab-card').first().getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  const textTab = await page.request.post(`/rest/api/3/screens/${screenID}/tabs`, { headers, data: { name: 'Text' } });
  expect(textTab.status()).toBe(200);
  const textID = String((await textTab.json()).id);
  for (const fieldId of ['summary', 'assignee', 'priority', 'description', 'environment', fieldIDs.number, fieldIDs.multiselect]) {
    expect((await page.request.post(`/rest/api/3/screens/${screenID}/tabs/${textID}/fields`, { headers, data: { fieldId } })).status()).toBe(200);
  }
  expect((await page.request.post(`/rest/api/3/screens/${screenID}/tabs`, { headers, data: { name: 'Unused' } })).status()).toBe(200);

  const configuration = await page.request.post('/rest/api/3/fieldconfiguration', { headers, data: { name: `Required transition environment ${stamp}` } });
  expect(configuration.status()).toBe(201);
  const configurationID = String((await configuration.json()).id);
  const requireEnvironment = async (isRequired: boolean) => {
    expect((await page.request.put(`/rest/api/3/fieldconfiguration/${configurationID}/fields`, { headers, data: { fieldConfigurationItems: [{ id: 'environment', isRequired }] } })).status()).toBe(204);
  };
  await requireEnvironment(true);
  const configurationScheme = await page.request.post('/rest/api/3/fieldconfigurationscheme', { headers, data: { name: `Required transition types ${stamp}` } });
  expect(configurationScheme.status()).toBe(201);
  const configurationSchemeID = String((await configurationScheme.json()).id);
  expect((await page.request.put(`/rest/api/3/fieldconfigurationscheme/${configurationSchemeID}/mapping`, { headers, data: { mappings: [{ issueTypeId: 'default', fieldConfigurationId: configurationID }] } })).status()).toBe(204);
  expect((await page.request.put('/rest/api/3/fieldconfigurationscheme/project', { headers, data: { fieldConfigurationSchemeId: configurationSchemeID, projectId: projectID } })).status()).toBe(204);

  await page.goto(`/browse/${issueKey}`);
  const again = page.locator('details.transition-screen').filter({ has: page.locator('summary').filter({ hasText: /^Finish$/ }) });
  await again.locator('summary').click();
  await expect(again.getByLabel('Labels')).toBeVisible();

  const details = again.getByRole('tab', { name: 'Field Tab', exact: true });
  const text = again.getByRole('tab', { name: 'Text', exact: true });
  await expect(again.getByRole('tab')).toHaveCount(2);
  await expect(again.getByLabel('Summary', { exact: true })).not.toBeVisible();
  const ids = await page.locator('.transition-screen [id]').evaluateAll((elements) => elements.map((element) => element.id));
  expect(new Set(ids).size).toBe(ids.length);

  await again.getByLabel('Resolution').selectOption({ label: "Won't Do" });
  await again.getByLabel('Labels').fill('screened');
  await details.focus();
  await page.keyboard.press('End');
  await expect(text).toBeFocused();
  await expect(again.getByLabel('Assignee')).toHaveValue(initial.assignee.accountId);
  await expect(again.getByLabel('Priority').locator('option:checked')).toHaveText(initial.priority.name);
  await again.getByLabel(`number transition ${stamp}`, { exact: true }).fill('12.5');
  await again.getByLabel(`multiselect transition ${stamp}`, { exact: true }).selectOption(optionIDs);
  await expect(again.getByLabel('Environment', { exact: true })).toHaveAttribute('required', '');
  await expect(again.getByLabel('Labels')).not.toHaveAttribute('required', '');
  await again.getByLabel('Environment', { exact: true }).fill('');
  await details.click();
  await again.getByRole('button', { name: 'Complete Finish', exact: true }).click();
  await expect(again.getByLabel('Environment', { exact: true })).toBeFocused();
  await expect(text).toHaveAttribute('aria-selected', 'true');
  await expect(again.getByLabel('Labels')).toHaveValue('screened');
  expect((await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=status,environment`, { headers })).json()).fields).toMatchObject({ status: { name: 'To Do' }, environment: rich });
  await again.getByLabel('Environment', { exact: true }).fill('Retained rich transition');
  await again.getByLabel('Summary', { exact: true }).fill('');
  await text.focus();
  await page.keyboard.press('Home');
  await expect(details).toBeFocused();
  await page.keyboard.press('ArrowLeft');
  await expect(text).toBeFocused();
  await details.click();
  await again.getByRole('button', { name: 'Complete Finish', exact: true }).click();
  await expect(again.getByLabel('Summary', { exact: true })).toBeFocused();
  await expect(again.getByLabel('Labels')).toHaveValue('screened');
  await again.getByLabel('Summary', { exact: true }).fill('   ');
  await details.click();
  const refused = page.waitForResponse((response) => response.url().endsWith(`/issues/${issueKey}/transition`) && response.request().method() === 'POST');
  await again.getByRole('button', { name: 'Complete Finish', exact: true }).click();
  expect((await refused).status()).toBe(400);
  await expect(again.getByRole('alert')).toContainText('summary is required');
  await expect(again.getByLabel('Labels')).toHaveValue('screened');
  await text.click();
  await again.getByLabel('Summary', { exact: true }).fill('Saved tabbed transition');

  // Two dirty actions sharing one URL must retain their own hidden identity.
  const later = page.locator('details.transition-screen').filter({ has: page.locator('summary').filter({ hasText: /^Finish later$/ }) });
  await later.locator('summary').click();
  await later.getByRole('tab', { name: 'Text', exact: true }).click();
  await later.getByLabel(`multiselect transition ${stamp}`, { exact: true }).selectOption([]);
  const finishID = await again.locator('input[name="transition"]').inputValue();
  const laterID = await later.locator('input[name="transition"]').inputValue();
  expect(finishID).not.toBe(laterID);

  // Another action refreshes the issue without losing this action's draft/tab.
  const watch = page.locator('.watch-button[aria-keyshortcuts="W"]');
  const watching = await watch.getAttribute('aria-pressed');
  await watch.click();
  await expect(watch).toHaveAttribute('aria-pressed', watching === 'true' ? 'false' : 'true');
  await expect(text).toHaveAttribute('aria-selected', 'true');
  await expect(again.getByLabel('Summary', { exact: true })).toHaveValue('Saved tabbed transition');
  await expect(again.getByLabel('Labels')).toHaveValue('screened');

  await expect(again.locator('input[name="transition"]')).toHaveValue(finishID);
  await expect(later.locator('input[name="transition"]')).toHaveValue(laterID);
  await expect(again.getByLabel(`multiselect transition ${stamp}`, { exact: true })).toHaveValues(optionIDs);
  await expect(later.getByLabel(`multiselect transition ${stamp}`, { exact: true })).toHaveValues([]);
  await expect(again.getByLabel(`number transition ${stamp}`, { exact: true })).toHaveValue('12.5');
  await later.locator('summary').click();

  await page.locator('[data-theme-toggle]').click();
  await page.setViewportSize({ width: 320, height: 740 });
  await page.addScriptTag({ content: axe.source });
  for (const tab of [details, text]) {
    await tab.click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const formBounds = await again.locator('form').boundingBox();
    expect(formBounds).not.toBeNull();
    expect(formBounds!.x).toBeGreaterThanOrEqual(0);
    expect(formBounds!.x + formBounds!.width).toBeLessThanOrEqual(320);
    expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
  }

  // Completing it persists the draft while preserving untouched choice fields.
  await again.getByRole('button', { name: 'Complete Finish', exact: true }).click();
  await expect.poll(async () => {
    const response = await page.request.get(`/rest/api/3/issue/${issueKey}?fields=summary,resolution,labels,status,assignee,priority`, { headers });
    const fields = (await response.json()).fields;
    return {
      summary: fields.summary, status: fields.status?.name, resolution: fields.resolution?.name,
      labels: fields.labels, assignee: fields.assignee?.accountId, priority: fields.priority?.id,
    };
  }, { timeout: 15_000 }).toEqual({ summary: 'Saved tabbed transition', status: 'Done', resolution: "Won't Do", labels: ['screened'], assignee: initial.assignee.accountId, priority: initial.priority.id });
  await page.reload();
  await expect(page.locator('.issue-summary')).toHaveText('Saved tabbed transition');
  const saved = (await (await page.request.get(`/rest/api/3/issue/${issueKey}?fields=description,environment,${fieldIDs.number},${fieldIDs.multiselect}`, { headers })).json()).fields;
  expect(saved.description).toEqual(rich);
  expect(saved.environment).toEqual(rich);
  expect(saved[fieldIDs.number]).toBe(12.5);
  expect(saved[fieldIDs.multiselect]).toEqual(optionIDs);

  await requireEnvironment(false);
  // Deselecting all options is a clear; omitted fields were covered at the web edge.
  const clearIssue = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, summary: 'Clear transition values', issuetype: { name: 'Task' }, description: rich, environment: rich, [fieldIDs.number]: 2, [fieldIDs.multiselect]: optionIDs } } });
  expect(clearIssue.status()).toBe(201);
  const clearKey = String((await clearIssue.json()).key);
  await page.goto(`/browse/${clearKey}`);
  const clear = page.locator('details.transition-screen').filter({ has: page.locator('summary').filter({ hasText: /^Finish$/ }) });
  await clear.locator('summary').click();
  await clear.getByRole('tab', { name: 'Text', exact: true }).click();
  await clear.getByLabel(`multiselect transition ${stamp}`, { exact: true }).selectOption([]);
  await clear.getByLabel(`number transition ${stamp}`, { exact: true }).fill('');
  await clear.getByLabel('Description', { exact: true }).fill('');
  await clear.getByLabel('Environment', { exact: true }).fill('');
  await clear.getByRole('button', { name: 'Complete Finish', exact: true }).click();
  await expect.poll(async () => (await (await page.request.get(`/rest/api/3/issue/${clearKey}?fields=status`, { headers })).json()).fields.status.name).toBe('Done');
  await page.reload();
  const cleared = (await (await page.request.get(`/rest/api/3/issue/${clearKey}?fields=description,environment,${fieldIDs.number},${fieldIDs.multiselect}`, { headers })).json()).fields;
  expect(cleared.environment).toBeNull();
  expect(cleared[fieldIDs.number] ?? null).toBeNull();
  expect(cleared[fieldIDs.multiselect] ?? null).toBeNull();
  expect(JSON.stringify(cleared.description)).not.toContain('Retained rich transition');
});
