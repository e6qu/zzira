import { expect, test, Page } from '@playwright/test';
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
  expect(sprintBean).toMatchObject({ state: 'active', originBoardId: 'brd_default' });
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
});
