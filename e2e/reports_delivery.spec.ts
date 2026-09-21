import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// An engineering manager asks two questions the DORA page answers only as
// single numbers: how often do we ship, and how long does work take once it
// starts.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  return 'Basic ' + Buffer.from(`${DEMO.email}:${tokens[DEMO.email]}`).toString('base64');
}

async function downloadCSV(page: Page): Promise<{ name: string; lines: string[] }> {
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Download CSV' }).click()]);
  return { name: download.suggestedFilename(), lines: fs.readFileSync((await download.path())!, 'utf8').trim().split('\n') };
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

async function reflows(page: Page) {
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
}

test('deployment frequency and cycle time each answer on a page of their own', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const key = `DC${stamp.toString(36).slice(-6).toUpperCase()}`;
  const boardName = `Delivery board ${stamp}`;
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  expect((await request.post('/rest/api/3/project', {
    headers, data: { key, name: `Delivery ${key}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  })).status()).toBe(201);
  const filter = await request.post('/rest/api/3/filter', { headers, data: { name: `Delivery filter ${stamp}`, jql: `project = ${key}` } });
  expect(filter.status()).toBe(200);
  const board = await request.post('/rest/agile/1.0/board', {
    headers, data: { name: boardName, type: 'kanban', filterId: Number((await filter.json()).id), location: { type: 'project', projectKeyOrId: key } },
  });
  expect(board.status(), await board.text()).toBe(201);

  const createIssue = async (summary: string, type: string) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, summary, issuetype: { name: type } } } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const moveTo = async (issueKey: string, category: string) => {
    const transitions = (await (await request.get(`/rest/api/3/issue/${issueKey}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
    const transition = transitions.find(candidate => candidate.to.statusCategory.key === category);
    expect(transition, `a transition to ${category}`).toBeTruthy();
    expect((await request.post(`/rest/api/3/issue/${issueKey}/transitions`, { headers, data: { transition: { id: transition!.id } } })).status()).toBe(204);
  };
  const task = await createIssue(`Delivery task ${stamp}`, 'Task');
  const bug = await createIssue(`Delivery bug ${stamp}`, 'Bug');
  for (const issueKey of [task, bug]) {
    await moveTo(issueKey, 'indeterminate');
    await moveTo(issueKey, 'done');
  }

  // Two production deployments, a day apart, carrying that work.
  for (const [index, issueKey] of [task, bug].entries()) {
    expect((await request.post('/rest/deployments/0.1/bulk', {
      headers,
      data: {
        properties: { accountId: 'delivery-reports-e2e' },
        deployments: [{
          deploymentSequenceNumber: stamp + index, updateSequenceNumber: stamp + index,
          displayName: `Rollout ${index + 1}`, url: 'https://deploy.example/' + index, description: 'Production rollout',
          lastUpdated: new Date(Date.now() - (index + 1) * 24 * 60 * 60 * 1000).toISOString(),
          state: 'successful', issueKeys: [issueKey],
          pipeline: { id: `delivery-${stamp}`, displayName: 'Delivery pipeline', url: 'https://ci.example/delivery' },
          environment: { id: 'production', displayName: 'Production', type: 'production' },
        }],
      },
    })).status()).toBe(202);
  }

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await page.goto(`/projects/${key}/reports`);

  // How often we ship.
  await page.getByRole('link', { name: 'Open Deployment frequency' }).click();
  await expect(page).toHaveURL(`/projects/${key}/reports/deployment-frequency`);
  await expect(page.getByRole('heading', { name: 'Deployment frequency', level: 1 })).toBeVisible();
  const summary = page.getByRole('region', { name: 'Deployment summary' });
  await expect(summary.locator('.dora-metric').first().locator('strong')).toHaveText('2');
  await expect(page.locator('#main-content')).toContainText('production deployments');
  await expect(page.locator('.report-table tbody tr')).toHaveCount(31);
  await accessible(page);
  const daily = await downloadCSV(page);
  expect(daily.name).toMatch(/^.*-deployment-frequency-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(daily.lines[0]).toBe('Start,Period,Successful,Failed or rolled back');
  expect(daily.lines.length).toBe(32);

  // The same deployments, by week.
  await page.getByLabel('Group by').selectOption('week');
  await page.getByRole('button', { name: 'Update' }).click();
  await expect(page).toHaveURL(/group=week/);
  await expect(page.locator('.report-table tbody tr').first()).toContainText('Week of');
  await expect(page.getByRole('region', { name: 'Deployment summary' }).locator('.dora-metric').first().locator('strong')).toHaveText('2');
  await reflows(page);

  // How long work takes once it starts.
  await page.goto(`/projects/${key}/reports`);
  await page.getByRole('link', { name: 'Open Cycle time' }).click();
  await expect(page).toHaveURL(`/projects/${key}/reports/cycle-time`);
  await expect(page.getByRole('heading', { name: 'Cycle time', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: boardName });
  await page.getByRole('button', { name: 'Show board' }).click();
  const percentiles = page.getByRole('region', { name: 'Cycle time summary' });
  await expect(percentiles.locator('.dora-metric')).toHaveCount(3);
  await expect(percentiles.locator('.dora-metric').first().locator('strong')).not.toHaveText('No data');
  // Both work types finished, so each gets its own line.
  const types = page.locator('table').filter({ has: page.getByRole('columnheader', { name: 'Work type' }) }).first();
  await expect(types).toContainText('Task');
  await expect(types).toContainText('Bug');
  await expect(page.locator('#main-content')).toContainText(task);
  await expect(page.locator('#main-content')).toContainText(bug);
  await accessible(page);
  const cycle = await downloadCSV(page);
  expect(cycle.name).toMatch(/^.*-cycle-time-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(cycle.lines[0]).toBe('Work item,Work type,Summary,Completed,Cycle time (hours)');
  expect(cycle.lines.length).toBe(3);
  await reflows(page);

  expect((await request.delete(`/rest/api/3/project/${key}?enableUndo=false`, { headers })).status()).toBe(204);
});
