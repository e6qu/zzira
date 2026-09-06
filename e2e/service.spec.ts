import { expect, test } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

async function accessible(page: import('@playwright/test').Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('admin creates a service project with Jira Service Management request types', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const key = `S${String(Date.now()).slice(-7)}`;
  await page.goto('/projects/new');
  await page.getByLabel('Name').fill(`Service desk ${key}`);
  await page.getByLabel('Key').fill(key);
  await page.getByLabel('Template').selectOption('com.atlassian.servicedesk:simplified-it-service-management');
  await page.getByLabel('Description').fill('Customer requests and production incidents');
  await page.getByRole('button', { name: 'Create project' }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  await expect(page.getByRole('heading', { name: `Service desk ${key}`, level: 1 })).toBeVisible();

  const auth = { Authorization: apiAuthHeader() };
  const projectResponse = await page.request.get(`/rest/api/3/project/${key}`, { headers: auth });
  expect(projectResponse.status()).toBe(200);
  expect(await projectResponse.json()).toMatchObject({ key, projectTypeKey: 'service_desk' });
  const desksResponse = await page.request.get('/rest/servicedeskapi/servicedesk', { headers: auth });
  expect(desksResponse.status()).toBe(200);
  const desks = await desksResponse.json();
  const desk = desks.values.find((candidate: any) => candidate.projectKey === key);
  expect(desk).toBeTruthy();
  const requestTypesResponse = await page.request.get(`/rest/servicedeskapi/servicedesk/${desk.id}/requesttype`, { headers: auth });
  expect(requestTypesResponse.status()).toBe(200);
  const requestTypes = await requestTypesResponse.json();
  expect(requestTypes.values.map((requestType: any) => requestType.name)).toEqual(expect.arrayContaining(['Get IT help', 'Report an incident']));
  const fieldsResponse = await page.request.get(`/rest/servicedeskapi/servicedesk/${desk.id}/requesttype/${requestTypes.values[0].id}/field`, { headers: auth });
  expect(fieldsResponse.status()).toBe(200);
  expect((await fieldsResponse.json()).requestTypeFields).toEqual(expect.arrayContaining([expect.objectContaining({ fieldId: 'summary', required: true })]));

  await page.goto('/service');
  await expect(page.getByRole('heading', { name: 'How can we help?', level: 1 })).toBeVisible();
  await accessible(page);
  await page.getByRole('link', { name: `Service desk ${key}` }).click();
  await expect(page.getByRole('heading', { name: `Service desk ${key}`, level: 1 })).toBeVisible();
  await page.getByRole('link', { name: /Report an incident/ }).click();
  await expect(page.getByRole('heading', { name: 'Report an incident', level: 1 })).toBeVisible();
  const requestSummary = `Checkout incident ${Date.now()}`;
  await page.getByLabel('Summary').fill(requestSummary);
  await page.getByLabel('Description').fill('Customers receive an error when completing checkout.');
  await page.getByRole('button', { name: 'Send request' }).click();
  await expect(page).toHaveURL(new RegExp(`/service/requests/${key}-\\d+$`));
  await expect(page.getByRole('heading', { name: requestSummary, level: 1 })).toBeVisible();
  await expect(page.locator('.service-current-status')).toHaveText('To Do');
  await expect(page.getByText('Customers receive an error when completing checkout.')).toBeVisible();
  await page.getByLabel('Add to the conversation').fill('The impact is increasing across regions.');
  await page.getByRole('button', { name: 'Add comment' }).click();
  await expect(page.locator('.service-comments')).toContainText('The impact is increasing across regions.');
  await page.getByRole('button', { name: /In Progress/ }).click();
  await expect(page.locator('.service-current-status')).toHaveText('In Progress');
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.goto('/service');
  await expect(page.locator('.service-request-list')).toContainText(requestSummary);
});
