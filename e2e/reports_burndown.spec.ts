import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// A delivery lead watches an epic and a release burn down sprint by sprint.

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
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('epic and release burndowns follow the work through sprints', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const versionResponse = await request.post('/rest/api/3/version', { headers, data: { name: `Burndown ${stamp}`, projectId: Number(project.id) } });
  expect(versionResponse.status(), await versionResponse.text()).toBe(201);
  const version = await versionResponse.json();

  const createIssue = async (fields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json());
  };
  const epic = await createIssue({ project: { key: 'ZZ' }, summary: `Burndown epic ${stamp}`, issuetype: { name: 'Epic' } });
  const finished = await createIssue({ project: { key: 'ZZ' }, summary: `Burndown finished ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic.key }, fixVersions: [{ id: version.id }] });
  const open = await createIssue({ project: { key: 'ZZ' }, summary: `Burndown open ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic.key }, fixVersions: [{ id: version.id }] });

  // A sprint the work runs through, so the chart has a bar to draw.
  const boards = await (await request.get('/rest/agile/1.0/board?projectKeyOrId=ZZ', { headers })).json();
  const board = boards.values.find((candidate: { type: string }) => candidate.type === 'scrum') ?? boards.values[0];
  const sprintResponse = await request.post('/rest/agile/1.0/sprint', { headers, data: { name: `Burndown sprint ${stamp}`, originBoardId: board.id } });
  expect(sprintResponse.status(), await sprintResponse.text()).toBe(201);
  const sprint = await sprintResponse.json();
  expect((await request.post(`/rest/agile/1.0/sprint/${sprint.id}/issue`, { headers, data: { issues: [finished.key, open.key] } })).status()).toBe(204);
  const started = await request.post(`/rest/agile/1.0/sprint/${sprint.id}`, { headers, data: { state: 'active', startDate: new Date(Date.now() - 86_400_000).toISOString(), endDate: new Date(Date.now() + 86_400_000).toISOString() } });
  expect(started.status(), await started.text()).toBe(200);
  const transitions = (await (await request.get(`/rest/api/3/issue/${finished.key}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
  const done = transitions.find((candidate) => candidate.to.statusCategory.key === 'done');
  expect(done).toBeTruthy();
  expect((await request.post(`/rest/api/3/issue/${finished.key}/transitions`, { headers, data: { transition: { id: done!.id } } })).status()).toBe(204);

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');

  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open Epic burndown' }).click();
  await expect(page.getByRole('heading', { name: 'Epic burndown', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: board.name });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByLabel('Epic', { exact: true }).selectOption(epic.key);
  await page.getByRole('button', { name: 'Show epic' }).click();
  const sprints = page.getByRole('table', { name: 'Work completed, added and remaining in each sprint' });
  await page.getByText('View sprint by sprint').click();
  await expect(sprints.getByRole('row').filter({ hasText: `Burndown sprint ${stamp}` })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Burndown summary' })).toContainText('1');
  await expect(page.locator('.agile-chart .chart-remaining')).toHaveCount(1);
  await accessible(page);
  const epicCSV = await downloadCSV(page);
  expect(epicCSV.lines[0]).toBe('Sprint,Completed,Added,Remaining');
  expect(epicCSV.lines.find((line) => line.startsWith(`Burndown sprint ${stamp},`))).toBeTruthy();

  await page.goto('/projects/ZZ/reports/release-burndown');
  await expect(page.getByRole('heading', { name: 'Release burndown', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: board.name });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByLabel('Version', { exact: true }).selectOption(version.id);
  await page.getByRole('button', { name: 'Show version' }).click();
  await page.getByText('View sprint by sprint').click();
  await expect(page.getByRole('table', { name: 'Work completed, added and remaining in each sprint' })).toContainText(`Burndown sprint ${stamp}`);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
});
