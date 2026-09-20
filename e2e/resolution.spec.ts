import { test, expect, Page } from '@playwright/test';
import axe from 'axe-core';
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
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a transition screen asks how work was resolved, and the field is editable', async ({ page, browser }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();

  // A project and workflow of its own, so other specs keep theirs.
  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `RS${stamp}`;
  await page.getByLabel('Name', { exact: true }).fill('Resolution screens');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  const projectResponse = await page.request.get(`/rest/api/3/project/${key}`, { headers: { Authorization: apiAuthHeader() } });
  const projectID = (await projectResponse.json()).id as string;

  await page.goto('/settings/workflows');
  await page.fill('#workflow-name', `Resolution workflow ${stamp}`);
  await page.selectOption('#workflow-project-scope', projectID);
  await page.getByRole('button', { name: 'Create workflow' }).click();
  await expect(page).toHaveURL(/\/settings\/workflows\/workflow_/);

  // A Done transition that asks for the resolution.
  const screenFields = page.locator('fieldset').filter({ hasText: 'Transition screen fields' });
  await page.fill('#transition-name', 'Finish');
  await page.selectOption('#transition-from', 'st_todo');
  await page.selectOption('#transition-to', 'st_done');
  await screenFields.getByLabel('Resolution').check();
  await page.getByRole('button', { name: 'Add transition' }).click();
  await expect(page.locator('.workflow-node').getByText('Finish', { exact: true })).toBeVisible();
  await page.fill('#transition-name', 'Reopen');
  await page.selectOption('#transition-from', 'st_done');
  await page.selectOption('#transition-to', 'st_todo');
  await page.getByRole('button', { name: 'Add transition' }).click();
  await page.getByRole('button', { name: 'Publish workflow' }).click();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();

  const created = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { project: { key }, summary: `Resolution subject ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const issueKey = (await created.json()).key as string;

  const resolutionOf = async () => {
    const response = await page.request.get(`/rest/api/3/issue/${issueKey}?fields=resolution`, { headers: { Authorization: apiAuthHeader() } });
    return (await response.json()).fields?.resolution?.name ?? null;
  };

  // The screen records the resolution the person picked, not the default.
  await page.goto(`/browse/${issueKey}`);
  const screen = page.locator('details.transition-screen').filter({ hasText: 'Finish' });
  await screen.locator('summary').click();
  await screen.getByLabel('Resolution').selectOption({ label: 'Duplicate' });
  await screen.getByRole('button', { name: 'Complete Finish' }).click();
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBe('Duplicate');

  // Reopening clears it.
  await page.goto(`/browse/${issueKey}`);
  await page.getByRole('button', { name: 'Reopen' }).click();
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBeNull();

  // The screen is reachable and readable on its own terms.
  await page.goto(`/browse/${issueKey}`);
  const finishAgain = page.locator('details.transition-screen').filter({ hasText: 'Finish' });
  await finishAgain.locator('summary').click();
  await expect(finishAgain.getByLabel('Resolution')).toBeVisible();
  await accessible(page);

  // The work item page edits the field on its own.
  await page.goto(`/browse/${issueKey}`);
  const field = page.locator('form.inline-field').filter({ has: page.locator('#field-resolution') });
  await field.getByLabel('Resolution').selectOption({ label: "Won't Do" });
  await field.getByRole('button', { name: 'Save resolution' }).click();
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBe("Won't Do");

  // The API sets and clears it as Jira does.
  const set = await page.request.put(`/rest/api/3/issue/${issueKey}`, {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { resolution: { name: 'Done' } } },
  });
  expect(set.status()).toBe(204);
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBe('Done');
  const cleared = await page.request.put(`/rest/api/3/issue/${issueKey}`, {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { fields: { resolution: null } },
  });
  expect(cleared.status()).toBe(204);
  await expect.poll(resolutionOf, { timeout: 15_000 }).toBeNull();

  // Someone who may read the work but not move it is not offered the screen,
  // and is refused if they ask for the transition anyway.
  const schemeName = `Read only delivery ${stamp}`;
  await page.goto('/settings/permission-schemes');
  const create = page.getByRole('region', { name: 'Create a scheme' });
  await create.getByLabel('Scheme name').fill(schemeName);
  await create.getByLabel('Description').fill('Members read the work; only administrators move it');
  await create.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Permission scheme created.');
  let card = page.locator('.permission-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const schemeID = await card.getAttribute('data-scheme-id');
  for (const permission of ['BROWSE_PROJECTS', 'EDIT_ISSUES']) {
    card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
    const roleGrant = card.locator('form').filter({ has: page.getByRole('button', { name: 'Grant to role' }) });
    await roleGrant.getByLabel('Grant permission').selectOption(permission);
    await roleGrant.getByLabel('To project role').selectOption('10001');
    await roleGrant.getByRole('button', { name: 'Grant to role' }).click();
    await expect(page.getByRole('status')).toContainText('Permission grant added.');
  }
  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  await card.getByLabel('Assign a project').selectOption({ label: `Resolution screens (${key})` });
  await card.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Project permission scheme assigned.');

  const denied = await browser.newContext();
  const deniedPage = await denied.newPage();
  await deniedPage.goto('/login');
  await deniedPage.fill('input[name=email]', 'ana@zzira.dev');
  await deniedPage.fill('input[name=password]', 'ana12345');
  await deniedPage.click('button[type=submit]');
  await deniedPage.goto(`/browse/${issueKey}`);
  await expect(deniedPage.getByRole('heading', { name: new RegExp(`Resolution subject ${stamp}`) })).toBeVisible();
  await expect(deniedPage.locator('details.transition-screen')).toHaveCount(0);
  await expect(deniedPage.getByRole('button', { name: 'Reopen' })).toHaveCount(0);
  const transitions = await deniedPage.request.get(`/rest/api/3/issue/${issueKey}/transitions`);
  expect((await transitions.json()).transitions).toEqual([]);
  // The transition exists -- it is offered to someone who may move the work
  // -- so refusing it is the permission speaking, not a bad id.
  const offered = await page.request.get(`/rest/api/3/issue/${issueKey}/transitions`, { headers: { Authorization: apiAuthHeader() } });
  const finishID = (await offered.json()).transitions.find((transition: { name: string }) => transition.name === 'Finish').id as string;
  // The session's own request, same-origin as the page it came from, so
  // what answers is the permission rather than the cross-origin guard.
  const refused = await deniedPage.request.post(`/rest/api/3/issue/${issueKey}/transitions`, {
    headers: { Origin: new URL(deniedPage.url()).origin, 'Content-Type': 'application/json' },
    data: { transition: { id: finishID } },
  });
  expect(refused.status()).toBe(400);
  expect(await refused.text()).toContain('permission to transition');
  await accessible(deniedPage);
  await denied.close();
});
