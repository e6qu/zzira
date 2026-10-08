import { expect, test } from '@playwright/test';
import { apiAuthHeader } from './auth';
import axe from 'axe-core';

test('portal cascading details follow their parent and survive a refused draft', async ({ page, request }) => {
  test.setTimeout(60_000);
  const headers = { Authorization: apiAuthHeader() };
  const owner = await (await request.get('/rest/api/3/myself', { headers })).json();
  const stamp = Date.now().toString(36).toUpperCase();
  const key = `CF${stamp}`;
  const createdProject = await request.post('/rest/api/3/project', { headers, data: {
    key, name: `Portal recovery ${stamp}`, projectTypeKey: 'service_desk', leadAccountId: owner.accountId,
    projectTemplateKey: 'com.atlassian.servicedesk:simplified-it-service-management',
  } });
  expect(createdProject.status()).toBe(201);
  const project = await createdProject.json();
  const desks = await (await request.get('/rest/servicedeskapi/servicedesk?limit=100', { headers })).json();
  const desk = desks.values.find((candidate: any) => candidate.projectKey === key);
  expect(desk).toBeTruthy();
  const types = await (await request.get(`/rest/servicedeskapi/servicedesk/${desk.id}/requesttype`, { headers })).json();
  const type = types.values.find((candidate: any) => candidate.name === 'Get IT help');
  expect(type).toBeTruthy();
  const field = async (name: string, kind: string, choices: string[]) => {
    const created = await request.post('/rest/api/3/field', { headers, data: { name, type: kind } });
    expect(created.status()).toBe(201);
    const id = (await created.json()).id as string;
    const contexts = await (await request.get(`/rest/api/3/field/${id}/context`, { headers })).json();
    const context = contexts.values[0].id;
    expect((await request.put(`/rest/api/3/field/${id}/context/${context}/project`, { headers, data: { projectIds: [String(project.id)] } })).status()).toBe(204);
    const options = await request.post(`/rest/api/3/field/${id}/context/${context}/option`, { headers, data: { options: choices.map(value => ({ value })) } });
    expect(options.status()).toBe(200);
    return { id, context, options: (await options.json()).options.map((option: { id: string | number }) => ({ id: String(option.id) })) };
  };
  const category = await field(`Category ${stamp}`, 'select', ['Device', 'General']);
  const platform = await field(`Platform ${stamp}`, 'cascadingselect', ['Web', 'Mobile', 'Other']);
  const children = await request.post(`/rest/api/3/field/${platform.id}/context/${platform.context}/option`, { headers, data: { options: [
    { value: 'Chrome', optionId: platform.options[0].id }, { value: 'iOS', optionId: platform.options[1].id },
  ] } });
  expect(children.status()).toBe(200);
  const [chrome, ios] = (await children.json()).options.map((option: { id: string | number }) => ({ id: String(option.id) }));
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
  const settings = new URLSearchParams();
  for (const id of ['summary', 'description', category.id, platform.id]) settings.append('fieldId', id);
  for (const id of ['summary', platform.id]) settings.append('requiredFieldId', id);
  settings.set(`condition_${platform.id}`, category.id);
  settings.set(`condition_options_${platform.id}`, category.options[0].id);
  const configured = await page.request.post(`/service/agent/${desk.id}/request-types/${type.id}/fields`, {
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', Origin: new URL(page.url()).origin }, data: settings.toString(),
  });
  expect(configured.ok(), await configured.text()).toBe(true);
  const formURL = `/service/portals/${desk.id}/request/${type.id}`;
  await page.goto(formURL);
  const parent = page.locator(`#request-field-${platform.id}`);
  const detail = page.getByLabel(`Platform ${stamp} detail`, { exact: true });
  await expect(parent).toBeHidden();
  await page.getByLabel(`Category ${stamp}`).selectOption(category.options[0].id);
  await expect(parent).toBeVisible();
  await expect(detail).toBeDisabled();
  await parent.selectOption(platform.options[1].id);
  await expect(detail).toBeEnabled();
  await expect(detail.locator(`option[value="${chrome.id}"]`)).toBeDisabled();
  await detail.selectOption(ios.id);
  await page.getByLabel('Summary').fill('   ');
  await page.getByLabel('Description', { exact: true }).fill('Keep the cascading draft.');
  await page.getByRole('button', { name: 'Send request' }).click();
  await expect(page.getByRole('alert')).toContainText('Summary is required.');
  await expect(parent).toHaveValue(String(platform.options[1].id));
  await expect(detail).toHaveValue(String(ios.id));
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
  await page.setViewportSize({ width: 1280, height: 720 });
  await parent.selectOption(platform.options[0].id);
  await expect(detail).toHaveValue('');
  await expect(detail.locator(`option[value="${ios.id}"]`)).toBeDisabled();
  await detail.selectOption(chrome.id);
  await parent.selectOption(platform.options[2].id);
  await expect(detail).toHaveValue('');
  await expect(detail).toBeDisabled();
  await parent.selectOption(platform.options[1].id);
  await detail.selectOption(ios.id);
  await page.getByLabel(`Category ${stamp}`).selectOption(category.options[1].id);
  await expect(parent).toBeDisabled();
  await expect(detail).toBeDisabled();
  await page.getByLabel(`Category ${stamp}`).selectOption(category.options[0].id);
  await expect(detail).toHaveValue(String(ios.id));
  const summary = `Portal cascading recovery ${stamp}`;
  await page.getByLabel('Summary').fill(summary);
  await page.getByRole('button', { name: 'Send request' }).click();
  await expect(page.getByRole('heading', { name: summary, level: 1 })).toBeVisible();
  await expect(page.locator('.service-request-fields')).toContainText('Mobile - iOS');
  await expect(page.locator('.service-request-fields')).not.toContainText('Chrome');

  // Cascading choices work on forms that have no conditional fields, too.
  settings.delete(`condition_${platform.id}`);
  settings.delete(`condition_options_${platform.id}`);
  const unconditional = await page.request.post(`/service/agent/${desk.id}/request-types/${type.id}/fields`, {
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', Origin: new URL(page.url()).origin }, data: settings.toString(),
  });
  expect(unconditional.ok(), await unconditional.text()).toBe(true);
  await page.goto(formURL);
  await expect(parent).toBeVisible();
  await expect(detail).toBeDisabled();
  await parent.selectOption(platform.options[0].id);
  await expect(detail.locator(`option[value="${ios.id}"]`)).toBeDisabled();
  await detail.selectOption(chrome.id);
  await parent.selectOption(platform.options[1].id);
  await expect(detail).toHaveValue('');
  await expect(detail.locator(`option[value="${chrome.id}"]`)).toBeDisabled();
});
