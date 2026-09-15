import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// An agile coach reads how work flows through a board: the cumulative flow
// diagram and the control chart pick up work that moved through the columns.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function downloadCSV(page: Page): Promise<{ name: string; lines: string[] }> {
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Download CSV' }).click()]);
  return { name: download.suggestedFilename(), lines: fs.readFileSync((await download.path())!, 'utf8').trim().split('\n') };
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('cumulative flow and control chart follow work through the board', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  // A project of its own keeps the default workflow, whatever other journeys
  // do to the seeded project's.
  const stamp = Date.now();
  const projectKey = `FL${stamp.toString(36).slice(-6).toUpperCase()}`;
  const boardName = `Flow board ${stamp}`;
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  const project = await request.post('/rest/api/3/project', { headers, data: { key: projectKey, name: `Flow ${projectKey}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
  expect(project.status(), await project.text()).toBe(201);
  const filter = await request.post('/rest/api/3/filter', { headers, data: { name: `Flow filter ${stamp}`, jql: `project = ${projectKey}` } });
  expect(filter.status(), await filter.text()).toBe(200);
  const board = await request.post('/rest/agile/1.0/board', { headers, data: { name: boardName, type: 'kanban', filterId: Number((await filter.json()).id), location: { type: 'project', projectKeyOrId: projectKey } } });
  expect(board.status(), await board.text()).toBe(201);
  const created = await request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: projectKey }, summary: `Flowing work ${stamp}`, issuetype: { name: 'Task' } } } });
  expect(created.status(), await created.text()).toBe(201);
  const key = (await created.json()).key as string;
  const moveTo = async (category: string) => {
    const transitions = (await (await request.get(`/rest/api/3/issue/${key}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
    const transition = transitions.find(candidate => candidate.to.statusCategory.key === category);
    expect(transition, `a transition to ${category}`).toBeTruthy();
    const moved = await request.post(`/rest/api/3/issue/${key}/transitions`, { headers, data: { transition: { id: transition!.id } } });
    expect(moved.status(), await moved.text()).toBe(204);
  };
  await moveTo('indeterminate');
  await moveTo('done');

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto(`/projects/${projectKey}`);
  await page.locator('.nav-reports').click();

  await page.getByRole('link', { name: 'Open Cumulative flow diagram' }).click();
  await expect(page.getByRole('heading', { name: 'Cumulative flow diagram', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: boardName });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByLabel('Time window').selectOption('14');
  await page.getByRole('button', { name: 'Show window' }).click();
  await expect(page).toHaveURL(/days=14/);
  await expect(page.locator('.flow-band')).not.toHaveCount(0);
  await page.getByText('View daily counts', { exact: true }).click();
  const counts = page.getByRole('table', { name: 'Daily column counts' });
  await expect(counts.locator('tbody tr')).toHaveCount(14);
  await expect(counts.locator('thead')).toContainText('Done');
  const flowCSV = await downloadCSV(page);
  expect(flowCSV.name).toMatch(new RegExp(`^${projectKey}-cumulative-flow-\\d{4}-\\d{2}-\\d{2}\\.csv$`));
  expect(flowCSV.lines[0]).toMatch(/^Date,.*Done/);
  expect(flowCSV.lines).toHaveLength(15);
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.goto(`/projects/${projectKey}/reports`);
  await page.getByRole('link', { name: 'Open Control chart' }).click();
  await expect(page.getByRole('heading', { name: 'Control chart', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: boardName });
  await page.getByRole('button', { name: 'Show board' }).click();
  await expect(page.getByRole('table', { name: 'Completed work cycle times' })).toContainText(key);
  const controlCSV = await downloadCSV(page);
  expect(controlCSV.lines[0]).toBe('Work item,Summary,Completed,Cycle time (hours)');
  expect(controlCSV.lines.some((line) => line.startsWith(`${key},`))).toBe(true);
  await page.getByLabel('Compare with previous period').check();
  await page.getByRole('button', { name: 'Show window' }).click();
  await expect(page).toHaveURL(/compare=previous/);
  await expect(page.getByRole('region', { name: 'Cycle time summary' }).locator('.report-change')).toHaveCount(3);
  expect((await downloadCSV(page)).lines.some((line) => line.startsWith(`Current period,${key},`))).toBe(true);
  await expect(page.getByRole('region', { name: 'Cycle time summary' })).toContainText('Average cycle time');
  await expect(page.locator('.control-point').first()).toBeAttached();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await page.goto(`/projects/${projectKey}/reports/control-chart?days=7`))?.status()).toBe(400);
  await page.setViewportSize({ width: 1280, height: 720 });

  // The same work shows in the issue analysis reports.
  const resolvedBean = await (await request.get(`/rest/api/3/issue/${key}`, { headers })).json();
  await page.goto(`/projects/${projectKey}/reports`);
  await page.getByRole('link', { name: 'Open Created vs. resolved' }).click();
  await expect(page.getByRole('heading', { name: 'Created vs. resolved', level: 1 })).toBeVisible();
  await page.getByLabel('Time window').selectOption('7');
  await page.getByLabel('Running totals').check();
  await page.getByRole('button', { name: 'Update' }).click();
  await expect(page).toHaveURL(/days=7/);
  await expect(page).toHaveURL(/cumulative=true/);
  const totals = page.getByRole('region', { name: 'Created and resolved totals' });
  await expect(totals).toContainText('Created');
  expect(Number(await totals.locator('article').first().locator('strong').textContent())).toBeGreaterThanOrEqual(1);
  if (resolvedBean.fields.resolutiondate) {
    expect(Number(await totals.locator('article').nth(1).locator('strong').textContent())).toBeGreaterThanOrEqual(1);
  }
  await page.getByText('View daily counts', { exact: true }).click();
  await expect(page.getByRole('table', { name: 'Created and resolved by day' }).locator('tbody tr')).toHaveCount(7);
  const createdCSV = await downloadCSV(page);
  expect(createdCSV.name).toMatch(new RegExp(`^${projectKey}-created-vs-resolved-`));
  expect(createdCSV.lines[0]).toBe('Date,Created,Resolved,Created in total,Resolved in total');
  expect(createdCSV.lines).toHaveLength(8);
  expect(Number(createdCSV.lines[7].split(',')[3])).toBeGreaterThanOrEqual(1);
  await page.getByLabel('Compare with previous period').check();
  await page.getByRole('button', { name: 'Update' }).click();
  await expect(page).toHaveURL(/compare=previous/);
  await expect(page).toHaveURL(/cumulative=true/);
  await expect(totals.locator('.report-change')).toHaveCount(2);
  await expect(totals.locator('.report-change').first()).toContainText('previous 7 days');
  const comparedCSV = await downloadCSV(page);
  expect(comparedCSV.lines[0]).toBe('Period,Date,Created,Resolved,Created in total,Resolved in total');
  expect(comparedCSV.lines).toHaveLength(15);
  expect(comparedCSV.lines[1]).toMatch(/^Previous period,/);
  expect(comparedCSV.lines[14]).toMatch(/^Current period,/);
  await accessible(page);

  await page.goto(`/projects/${projectKey}/reports`);
  await page.getByRole('link', { name: 'Open Resolution time' }).click();
  await expect(page.getByRole('heading', { name: 'Resolution time', level: 1 })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Resolution summary' })).toContainText('Average resolution time');
  await page.getByText('View daily resolution time', { exact: true }).click();
  await expect(page.getByRole('table', { name: 'Resolution time by day' }).locator('tbody tr')).toHaveCount(30);
  const resolutionCSV = await downloadCSV(page);
  expect(resolutionCSV.lines[0]).toBe('Date,Resolved,Average resolution time (hours)');
  expect(resolutionCSV.lines).toHaveLength(31);
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await page.goto(`/projects/${projectKey}/reports/resolution-time?days=14`))?.status()).toBe(400);
});
