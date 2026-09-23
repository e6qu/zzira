import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

// A customer raising a request in the portal says who it is for: themselves,
// or one of the organizations they belong to. That choice is what their
// colleagues read, what the agent can change afterwards, and what the
// Organizations search answers -- not who the customer happens to work with.
test('a customer shares a request with their organization, and the agent changes it', async ({ page, browser }) => {
  test.setTimeout(180_000);
  const auth = { Authorization: apiAuthHeader() };
  await login(page, 'demo@zzira.dev', 'demo1234');
  const key = `SW${String(Date.now()).slice(-6)}`;
  await page.goto('/projects/new');
  await page.getByLabel('Name').fill(`Sharing desk ${key}`);
  await page.getByLabel('Key').fill(key);
  await page.getByLabel('Template').selectOption('com.atlassian.servicedesk:simplified-it-service-management');
  await page.getByRole('button', { name: 'Create project' }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  const desks = await (await page.request.get('/rest/servicedeskapi/servicedesk?limit=100', { headers: auth })).json();
  const desk = desks.values.find((candidate: any) => candidate.projectKey === key);
  expect(desk).toBeTruthy();
  const requestTypes = await (await page.request.get(`/rest/servicedeskapi/servicedesk/${desk.id}/requesttype`, { headers: auth })).json();
  const requestType = requestTypes.values.find((candidate: any) => candidate.name === 'Get IT help');
  expect(requestType).toBeTruthy();

  // A customer of this desk is somebody who has asked it for something.
  const customer = await browser.newPage();
  await login(customer, 'ana@zzira.dev', 'ana12345');
  await customer.goto(`/service/portals/${desk.id}/request/${requestType.id}`);
  await expect(customer.getByLabel('Share with')).toHaveCount(0);
  await customer.getByLabel('Summary').fill(`The lobby printer jams ${key}`);
  await customer.getByRole('button', { name: 'Send request' }).click();
  await expect(customer.getByRole('heading', { name: `The lobby printer jams ${key}`, level: 1 })).toBeVisible();
  await expect(customer.locator('#shared-with')).toContainText('Only you and the service team');

  // Her organization is a customer of this portal.
  const organizationName = `Riverbank Foods ${key}`;
  await page.goto(`/service/agent/${desk.id}`);
  const organizationSettings = page.locator('#organizations');
  await organizationSettings.getByPlaceholder('Organization name').fill(organizationName);
  await organizationSettings.getByRole('button', { name: 'Create organization' }).click();
  const organizationCard = page.locator('#organizations article').filter({ hasText: organizationName });
  await organizationCard.getByPlaceholder('Customer email or account ID').fill('ana@zzira.dev');
  await organizationCard.getByRole('button', { name: 'Add customer' }).click();
  await expect(page.locator('#organizations article').filter({ hasText: organizationName })).toContainText('Ana Soursop');
  await page.locator('#organizations article').filter({ hasText: organizationName }).getByRole('button', { name: 'Link portal' }).click();
  await expect(page.locator('#organizations article').filter({ hasText: organizationName }).getByRole('button', { name: 'Unlink portal' })).toBeVisible();

  // Now the portal asks who the next request is for.
  await customer.goto(`/service/portals/${desk.id}/request/${requestType.id}`);
  await customer.getByLabel('Summary').fill(`The card reader by the loading bay is dead ${key}`);
  await accessible(customer);
  await customer.getByLabel('Share with').selectOption({ label: organizationName });
  await customer.getByRole('button', { name: 'Send request' }).click();
  await expect(customer.getByRole('heading', { name: `The card reader by the loading bay is dead ${key}`, level: 1 })).toBeVisible();
  await expect(customer.locator('#shared-with')).toContainText(organizationName);
  const sharedKey = (await customer.locator('.service-request-header p').innerText()).split(' ')[0];
  await accessible(customer);

  // The organization a request was shared with is what Organizations searches,
  // so the request she kept to herself is not among them.
  await page.goto(`/issues/${key}?mode=advanced&jql=${encodeURIComponent(`Organizations = "${organizationName}"`)}`);
  await expect(page.locator('.issue-list')).toContainText(`The card reader by the loading bay is dead ${key}`);
  await expect(page.locator('.issue-list')).not.toContainText(`The lobby printer jams ${key}`);

  // An agent changes who a request is for.
  await page.goto(`/service/requests/${sharedKey}`);
  await expect(page.locator('#shared-with')).toContainText(organizationName);
  await page.locator('#shared-with').getByLabel('Share this request with').selectOption({ label: 'Private request' });
  await page.locator('#shared-with').getByRole('button', { name: 'Save sharing' }).click();
  await expect(page.locator('#shared-with')).toContainText('Only you and the service team');
  await page.goto(`/issues/${key}?mode=advanced&jql=${encodeURIComponent(`Organizations = "${organizationName}"`)}`);
  await expect(page.locator('.issue-list')).not.toContainText(`The card reader by the loading bay is dead ${key}`);
  await customer.close();
});
