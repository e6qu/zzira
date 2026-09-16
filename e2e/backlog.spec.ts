import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };
const sprintName = `Planning sprint ${Date.now()}`;
const updatedGoal = 'Ship the planning journey with API and browser parity';
let firstIssueKey = '';
let secondIssueKey = '';

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

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

function sprintSection(page: Page, name: string) {
  return page.locator('.sprint-section').filter({
    has: page.locator(':scope > summary strong', { hasText: name }),
  });
}

test.beforeAll(async ({ request }) => {
  const create = async (summary: string) => {
    const response = await request.post('/rest/api/3/issue', {
      headers: { Authorization: apiAuthHeader() },
      data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' } } },
    });
    expect(response.status()).toBe(201);
    return (await response.json()).key as string;
  };
  firstIssueKey = await create(`Backlog planning first ${Date.now()}`);
  secondIssueKey = await create(`Backlog planning second ${Date.now()}`);
});

test('backlog journey creates, plans, ranks, starts, updates, and completes a sprint', async ({ page, request }) => {
  await login(page);
  await page.goto('/board/brd_default/backlog');
  await expect(page).toHaveTitle('ZZIRA Demo backlog · ZZIRA');
  await expect(page.getByRole('heading', { name: 'Backlog', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Backlog', exact: true }).first()).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.backlog-item', { hasText: firstIssueKey })).toBeVisible();

  await page.locator('.create-sprint > summary').click();
  await page.fill('#new-sprint-name', sprintName);
  await page.fill('#new-sprint-goal', 'Make sprint planning usable');
  await Promise.all([
    page.waitForURL('/board/brd_default/backlog'),
    page.locator('.create-sprint form').getByRole('button', { name: 'Create sprint' }).click(),
  ]);
  const sprint = sprintSection(page, sprintName);
  await expect(sprint).toBeVisible();
  await expect(sprint).toContainText('This sprint is empty');

  for (const key of [firstIssueKey, secondIssueKey]) {
    const item = page.locator('.backlog-item', { hasText: key });
    await item.locator('.backlog-item-menu > summary').click();
    await item.locator('select[name=sprint]').selectOption({ label: sprintName });
    await Promise.all([
      page.waitForURL('/board/brd_default/backlog'),
      item.getByRole('button', { name: 'Move', exact: true }).click(),
    ]);
    await expect(sprint.locator('.backlog-item', { hasText: key })).toBeVisible();
  }

  const sprintItems = sprint.locator('.backlog-item');
  await expect(sprintItems).toHaveCount(2);
  await expect(sprintItems.nth(0)).toContainText(firstIssueKey);
  const secondItem = sprintItems.filter({ hasText: secondIssueKey });
  await secondItem.locator('.backlog-item-menu > summary').click();
  await Promise.all([
    page.waitForURL('/board/brd_default/backlog'),
    secondItem.getByRole('button', { name: 'Move up' }).click(),
  ]);
  await expect(sprint.locator('.backlog-item').nth(0)).toContainText(secondIssueKey);

  await sprint.locator('.backlog-summary').first().click();
  await expect(page.locator('#backlog-preview .issue-preview-card')).toBeVisible();

  await sprint.locator('.start-sprint > summary').click();
  await Promise.all([
    page.waitForURL('/board/brd_default/backlog'),
    sprint.locator('.start-sprint form').getByRole('button', { name: 'Start sprint' }).click(),
  ]);
  await expect(sprint).toContainText('active');

  const sprintList = await request.get('/rest/agile/1.0/board/brd_default/sprint', {
    headers: { Authorization: apiAuthHeader() },
  });
  expect(sprintList.status()).toBe(200);
  const sprintBean = (await sprintList.json()).values.find((value: any) => value.name === sprintName);
  const board = await (await request.get('/rest/agile/1.0/board/brd_default', { headers: { Authorization: apiAuthHeader() } })).json();
  expect(typeof board.id).toBe('number');
  expect(sprintBean).toMatchObject({ state: 'active', originBoardId: board.id });
  expect(sprintBean.startDate).toBeTruthy();
  expect(sprintBean.endDate).toBeTruthy();

  const updated = await request.put(`/rest/agile/1.0/sprint/${sprintBean.id}`, {
    headers: { Authorization: apiAuthHeader() },
    data: { goal: updatedGoal },
  });
  expect(updated.status()).toBe(200);
  expect((await updated.json()).goal).toBe(updatedGoal);
  await page.reload();
  await expect(sprint).toContainText(updatedGoal);

  const movedToBacklog = await request.post('/rest/agile/1.0/backlog/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { issues: [secondIssueKey] },
  });
  expect(movedToBacklog.status()).toBe(204);
  const movedBackToSprint = await request.post(`/rest/agile/1.0/sprint/${sprintBean.id}/issue`, {
    headers: { Authorization: apiAuthHeader() },
    data: { issues: [secondIssueKey] },
  });
  expect(movedBackToSprint.status()).toBe(204);

  await Promise.all([
    page.waitForURL('/board/brd_default/backlog'),
    sprint.getByRole('button', { name: 'Complete sprint' }).click(),
  ]);
  await expect(sprint).toHaveCount(0);
  await expect(page.locator('.backlog-section').last().locator('.backlog-item', { hasText: firstIssueKey })).toBeVisible();

  const backlogKeys: string[] = [];
  let startAt = 0;
  for (;;) {
    const response = await request.get(`/rest/agile/1.0/board/brd_default/backlog?startAt=${startAt}&maxResults=100`, {
      headers: { Authorization: apiAuthHeader() },
    });
    expect(response.status()).toBe(200);
    const body = await response.json();
    backlogKeys.push(...body.issues.map((issue: any) => issue.key));
    if (backlogKeys.includes(firstIssueKey) && backlogKeys.includes(secondIssueKey)) break;
    startAt += body.issues.length;
    if (startAt >= body.total || body.issues.length === 0) break;
  }
  expect(backlogKeys).toEqual(expect.arrayContaining([firstIssueKey, secondIssueKey]));

  // The completed sprint reads back in the sprint report and velocity chart.
  await page.goto('/board/brd_default/backlog');
  await page.locator('.nav-reports').click();
  await page.getByRole('link', { name: 'Open Sprint report' }).click();
  await expect(page.getByRole('heading', { name: 'Sprint report', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: 'ZZ board' });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByLabel('Sprint', { exact: true }).selectOption({ label: sprintName });
  await page.getByRole('button', { name: 'Show sprint' }).click();
  await expect(page.getByRole('heading', { name: sprintName, level: 2 })).toBeVisible();
  const incomplete = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Work items not completed' }) });
  await expect(incomplete).toContainText(firstIssueKey);
  await expect(incomplete).toContainText(secondIssueKey);
  await expect(page.getByRole('region', { name: 'Sprint summary' })).toContainText('Completed');
  await expect(page.locator('.agile-chart .chart-remaining')).toHaveCount(1);
  await expect(page.getByRole('heading', { name: 'Burnup', level: 2 })).toBeVisible();
  await expect(page.locator('.agile-chart .chart-scope')).toHaveCount(1);
  await expect(page.locator('.agile-chart .chart-completed-line')).toHaveCount(1);
  await page.getByText('View burndown changes', { exact: true }).click();
  const changes = page.getByRole('table', { name: 'Burndown changes' });
  await expect(changes).toContainText('Sprint start');
  await expect(changes).toContainText('Sprint completed');
  await expect(changes).toContainText(secondIssueKey);
  const sprintCSV = await downloadCSV(page);
  expect(sprintCSV.name).toMatch(/^ZZ-sprint-report-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(sprintCSV.lines[0]).toBe('Section,Work item,Summary,Work type,Status,Estimate at start,Estimate at end,Added after start');
  expect(sprintCSV.lines.find((line) => line.includes(`,${firstIssueKey},`))).toMatch(/^Work items not completed,/);
  expect(sprintCSV.lines.find((line) => line.includes(`,${secondIssueKey},`))).toMatch(/^Work items not completed,/);
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.goto('/board/brd_default/backlog');
  await page.locator('.nav-reports').click();
  await page.getByRole('link', { name: 'Open Velocity chart' }).click();
  await expect(page.getByRole('heading', { name: 'Velocity chart', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: 'ZZ board' });
  await page.getByRole('button', { name: 'Show board' }).click();
  await expect(page.getByRole('table', { name: 'Velocity data' })).toContainText(sprintName);
  const velocityCSV = await downloadCSV(page);
  expect(velocityCSV.lines[0]).toBe('Sprint,Commitment,Completed');
  expect(velocityCSV.lines.some((line) => line.startsWith(`${sprintName},`))).toBe(true);
  await expect(page.locator('.chart-commitment').first()).toBeAttached();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('parallel sprints stay off until the site switch turns them on', async ({ page }) => {
  const first = `Parallel first ${Date.now()}`;
  const second = `Parallel second ${Date.now()}`;
  const features = () => page.locator('form[action="/admin/jira-configuration/features"]');
  const parallelSwitch = () => features().locator('label').filter({ hasText: 'Parallel sprints' }).getByRole('checkbox');
  const setParallel = async (on: boolean) => {
    await page.goto('/admin');
    await (on ? parallelSwitch().check() : parallelSwitch().uncheck());
    await features().getByRole('button', { name: 'Save features' }).click();
    await page.goto('/admin');
    await expect(parallelSwitch()).toBeChecked({ checked: on });
  };

  await login(page);
  // Jira leaves parallel sprints off, and a board then runs one sprint at a time.
  await setParallel(false);
  await page.goto('/board/brd_default/backlog');
  for (const name of [first, second]) {
    await page.locator('.create-sprint > summary').click();
    await page.fill('#new-sprint-name', name);
    await Promise.all([
      page.waitForURL('/board/brd_default/backlog'),
      page.locator('.create-sprint form').getByRole('button', { name: 'Create sprint' }).click(),
    ]);
  }

  const start = async (name: string, expectation: 'started' | 'refused') => {
    const sprint = sprintSection(page, name);
    await sprint.locator('.start-sprint > summary').click();
    await Promise.all([
      page.waitForURL(/\/board\/brd_default\/backlog/),
      sprint.locator('.start-sprint form').getByRole('button', { name: 'Start sprint' }).click(),
    ]);
    if (expectation === 'started') {
      await expect(sprintSection(page, name)).toContainText('active');
    } else {
      await expect(page.locator('p.backlog-error')).toContainText('complete the active sprint before starting another');
    }
  };

  await start(first, 'started');
  await start(second, 'refused');

  // With the site switch on, the board runs both sprints at once.
  await setParallel(true);
  await page.goto('/board/brd_default/backlog');
  await start(second, 'started');
  await expect(sprintSection(page, first)).toContainText('active');

  for (const name of [first, second]) {
    const sprint = sprintSection(page, name);
    await Promise.all([
      page.waitForURL(/\/board\/brd_default\/backlog/),
      sprint.getByRole('button', { name: 'Complete sprint' }).click(),
    ]);
  }
  await setParallel(false);
});
