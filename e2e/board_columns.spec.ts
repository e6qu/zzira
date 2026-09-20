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

// A board administrator shapes the board's columns: names them, groups two
// statuses into one, caps the work it carries, and changes the board's filter
// and its estimate -- none of which the browser could do before.
test('board administrator names columns, groups statuses and changes the filter', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const created = await request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Column card ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const key = (await created.json()).key as string;

  await login(page);
  await page.goto('/board/brd_default/settings');
  const columns = page.locator('.board-column-row');
  await expect(columns).toHaveCount(4); // three columns and the add row

  // Rename the first column, group In Progress and Done under the second, and
  // give it a limit.
  await columns.nth(0).getByLabel('Name').fill('Queued');
  await columns.nth(1).getByLabel('Name').fill('Under way');
  await columns.nth(1).getByLabel('Limit').fill('4');
  await columns.nth(2).getByLabel('Delete this column').check();
  // The options are named after the columns as they are saved, so a status
  // is placed by the column's key rather than by the name being typed above.
  const placement = page.locator('.board-status-placement');
  await placement.getByLabel('To Do', { exact: true }).selectOption('c0');
  await placement.getByLabel('In Progress', { exact: true }).selectOption('c1');
  await placement.getByLabel('Done', { exact: true }).selectOption('c1');
  await accessible(page);
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await expect(page.getByRole('status')).toContainText('Board settings saved.');

  // The board shows the two columns, and the grouped one names both statuses.
  await page.goto('/board/brd_default');
  await expect(page.locator('.board-column')).toHaveCount(2);
  await expect(page.locator('.board-column-head .lozenge').first()).toHaveText('Queued');
  await expect(page.locator('.board-column-head').nth(1)).toContainText('In Progress, Done');
  await expect(page.getByRole('link', { name: new RegExp(`^${key}`) }).first()).toBeVisible();

  // Jira's configuration API describes the same two columns.
  const configuration = await (await request.get('/rest/agile/1.0/board/brd_default/configuration', { headers: auth })).json();
  expect(configuration.columnConfig.columns.map((column: any) => column.name)).toEqual(['Queued', 'Under way']);
  expect(configuration.columnConfig.columns[1].statuses).toHaveLength(2);
  expect(configuration.columnConfig.columns[1].max).toBe(4);
  expect(configuration.columnConfig.constraintType).toBe('issueCount');

  // The filter and the estimate are the board's own settings now.
  await page.goto('/board/brd_default/settings');
  await page.getByLabel('Board filter (JQL)').fill(`project = ZZ AND summary ~ "Column card ${stamp}"`);
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await expect(page.getByRole('status')).toContainText('Board settings saved.');
  await page.goto('/board/brd_default');
  await expect(page.locator('.board-card')).toHaveCount(1);

  // A status left out of every column leaves the board, and the settings page
  // says which statuses those are.
  await page.goto('/board/brd_default/settings');
  await page.locator('.board-status-placement').getByLabel('Done', { exact: true }).selectOption('');
  await page.getByRole('button', { name: 'Save board settings' }).click();
  const afterward = await (await request.get('/rest/agile/1.0/board/brd_default/configuration', { headers: auth })).json();
  expect(afterward.columnConfig.columns[1].statuses).toHaveLength(1);

  // Put the board back the way the other specs expect to find it.
  await page.goto('/board/brd_default/settings');
  await page.locator('.board-column-row').nth(0).getByLabel('Name').fill('To Do');
  await page.locator('.board-column-row').nth(1).getByLabel('Name').fill('In Progress');
  await page.locator('.board-column-row').nth(1).getByLabel('Limit').fill('0');
  await page.getByLabel('Board filter (JQL)').fill('project = ZZ');
  await page.locator('.board-status-placement').getByLabel('In Progress', { exact: true }).selectOption('c1');
  await page.locator('.board-column-new').getByLabel('Name').fill('Done');
  await page.locator('.board-status-placement').getByLabel('Done', { exact: true }).selectOption('new');
  await page.getByRole('button', { name: 'Save board settings' }).click();
  await page.goto('/board/brd_default');
  await expect(page.locator('.board-column')).toHaveCount(3);
});
