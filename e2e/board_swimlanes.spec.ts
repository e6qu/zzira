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

// A board groups its work the way the team reads it: by the epic above each
// work item, and by named queries. Both were assignee-or-nothing before.
test('board administrator groups the board by epic and by named queries', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();

  const epic = await request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Lane epic ${stamp}`, issuetype: { name: 'Epic' } } },
  });
  expect(epic.status()).toBe(201);
  const epicKey = (await epic.json()).key as string;

  const child = await request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Under the epic ${stamp}`, issuetype: { name: 'Task' }, parent: { key: epicKey }, priority: { name: 'High' } } },
  });
  expect(child.status()).toBe(201);
  const childKey = (await child.json()).key as string;

  const orphan = await request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Under no epic ${stamp}`, issuetype: { name: 'Task' }, priority: { name: 'Low' } } },
  });
  expect(orphan.status()).toBe(201);
  const orphanKey = (await orphan.json()).key as string;

  await login(page);
  await page.goto('/board/brd_default/settings');
  await page.getByLabel('Swimlanes').selectOption('epic');
  await accessible(page);
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await expect(page.getByRole('status')).toContainText('Board settings saved.');

  // The epic is a lane, and what sits under no epic has its own.
  await page.goto('/board/brd_default');
  const epicLane = page.getByRole('region', { name: new RegExp(`^${epicKey}`) });
  await expect(epicLane).toBeVisible();
  await expect(epicLane.getByRole('link', { name: new RegExp(`^${childKey}`) })).toBeVisible();
  const noEpic = page.getByRole('region', { name: /Work under no epic/ });
  await expect(noEpic.getByRole('link', { name: new RegExp(`^${orphanKey}`) })).toBeVisible();

  // Named queries: the first lane whose query matches takes the work item,
  // and the rest stand in the lane at the bottom.
  await page.goto('/board/brd_default/settings');
  await page.getByLabel('Swimlanes').selectOption('query');
  const lanes = page.locator('.board-swimlane-row');
  await lanes.last().getByLabel('Name').fill('Urgent');
  await lanes.last().getByLabel('JQL').fill('priority = High');
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await expect(page.getByRole('status')).toContainText('Board settings saved.');

  await page.goto('/board/brd_default');
  const urgent = page.getByRole('region', { name: /^Urgent/ });
  await expect(urgent.getByRole('link', { name: new RegExp(`^${childKey}`) })).toBeVisible();
  const everythingElse = page.getByRole('region', { name: /Everything else/ });
  await expect(everythingElse.getByRole('link', { name: new RegExp(`^${orphanKey}`) })).toBeVisible();

  // Grouping by query needs a query, and the page says so rather than saving
  // a board with no lanes.
  await page.goto('/board/brd_default/settings');
  await page.locator('.board-swimlane-row').first().getByLabel('Delete this lane').check();
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await expect(page.getByRole('alert')).toContainText('needs at least one swimlane query');

  // Put the board back the way the other specs expect it.
  await page.getByLabel('Swimlanes').selectOption('none');
  await page.locator('.board-swimlane-row').first().getByLabel('Delete this lane').check();
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await expect(page.getByRole('status')).toContainText('Board settings saved.');
  await page.goto('/board/brd_default');
  await expect(page.getByRole('region', { name: 'All work swimlane', exact: true })).toBeVisible();
});
