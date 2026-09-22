import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const email = 'demo@zzira.dev';
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[email];
  return 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64');
}

// A manager puts reports on an operating dashboard: created vs. resolved work
// for a project, and the velocity and sprint burndown of a scrum board.

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('report gadgets draw project and board reports on a dashboard', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/dashboards');
  await page.getByLabel('Dashboard name', { exact: true }).fill(`Reports ${Date.now()}`);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);
  const dashboardURL = page.url().split('?')[0];

  await page.getByRole('button', { name: 'Add Created vs. resolved chart', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Configure Created vs. resolved chart' })).toBeVisible();
  await expect(page.locator('.dashboard-gadget')).toContainText('Configure this gadget to choose a project.');
  await page.getByLabel('Project', { exact: true }).selectOption('ZZ');
  await page.getByLabel('Time window').selectOption('7');
  await page.getByLabel('Running totals').check();
  await accessible(page);
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  await expect(page).toHaveURL(dashboardURL);
  const trend = page.locator('.dashboard-gadget').filter({ hasText: 'Created vs. resolved chart' });
  await expect(trend).toContainText('in ZZ, last 7 days, running totals');
  await trend.getByText('View daily counts').click();
  await expect(trend.getByRole('table', { name: 'Created and resolved by day in ZZ' }).locator('tbody tr')).toHaveCount(7);

  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Velocity chart', exact: true }).click();
  await page.getByLabel('Scrum board').selectOption({ label: 'ZZ board (ZZ)' });
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  const velocity = page.locator('.dashboard-gadget').filter({ hasText: 'Velocity chart' });
  await expect(velocity).toContainText(/completed per sprint|No completed sprints on ZZ board yet\./);

  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Sprint burndown', exact: true }).click();
  await page.getByLabel('Scrum board').selectOption({ label: 'ZZ board (ZZ)' });
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  const burndown = page.locator('.dashboard-gadget').filter({ hasText: 'Sprint burndown' });
  await expect(burndown).toContainText(/on ZZ board|No active sprint on ZZ board\./);

  // Refreshing redraws the reports in place.
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(trend).toContainText('running totals');
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});


test('project and sprint report gadgets show recent work, age and sprint progress', async ({ page }) => {
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  // Own the work and the active sprint the gadgets report on.
  expect((await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary: `Report gadget work ${stamp}`, issuetype: { name: 'Task' } } } })).status()).toBe(201);
  const boards = await (await page.request.get('/rest/agile/1.0/board?projectKeyOrId=ZZ&type=scrum', { headers })).json();
  const boardID = boards.values[0].id;
  const active = await (await page.request.get(`/rest/agile/1.0/board/${boardID}/sprint?state=active`, { headers })).json();
  if (active.values.length === 0) {
    const created = await page.request.post('/rest/agile/1.0/sprint', { headers, data: { name: `Report sprint ${stamp}`, originBoardId: boardID } });
    expect(created.status(), await created.text()).toBe(201);
    const start = new Date(Date.now() - 2 * 86_400_000).toISOString();
    const end = new Date(Date.now() + 8 * 86_400_000).toISOString();
    const started = await page.request.post(`/rest/agile/1.0/sprint/${(await created.json()).id}`, { headers, data: { state: 'active', startDate: start, endDate: end } });
    expect(started.status(), await started.text()).toBe(200);
  }

  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/dashboards');
  await page.getByLabel('Dashboard name', { exact: true }).fill(`Sprint reports ${stamp}`);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);
  const dashboardURL = page.url().split('?')[0];
  const add = async (title: string, configure: () => Promise<void>) => {
    if (!page.url().includes('add=1')) await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
    await page.getByRole('button', { name: `Add ${title}`, exact: true }).click();
    await configure();
    await page.getByRole('button', { name: 'Save gadget report' }).click();
    await expect(page).toHaveURL(dashboardURL);
    return page.locator('.dashboard-gadget').filter({ has: page.getByRole('heading', { name: title, exact: true }) });
  };
  const project = (days: string) => async () => {
    await page.getByLabel('Project', { exact: true }).selectOption('ZZ');
    await page.getByLabel('Time window').selectOption(days);
  };
  const board = async () => { await page.getByLabel('Scrum board').selectOption({ label: 'ZZ board (ZZ)' }); };

  const recent = await add('Recently created chart', project('7'));
  await expect(recent).toContainText(/[1-9]\d* created · \d+ since resolved in ZZ, last 7 days/);
  await recent.getByText('View daily counts').click();
  await expect(recent.getByRole('table', { name: 'Recently created work by day in ZZ' }).locator('tbody tr')).toHaveCount(7);

  const age = await add('Average age chart', project('30'));
  await expect(age).toContainText(/average age of [1-9]\d* unresolved in ZZ today/);
  await age.getByText('View daily average age').click();
  await expect(age.getByRole('table', { name: 'Average age by day in ZZ' }).locator('tbody tr')).toHaveCount(30);

  const since = await add('Time since chart', async () => {
    await project('7')();
    await expect(page.getByLabel('Date field', { exact: true })).toHaveValue('created');
    await page.getByLabel('Date field', { exact: true }).selectOption('updated');
  });
  await expect(since).toContainText(/[1-9]\d* updated in ZZ, last 7 days/);
  await since.getByText('View daily counts').click();
  await expect(since.getByRole('table', { name: 'Work updated by day in ZZ' }).getByRole('columnheader')).toHaveText(['Date', 'Updated']);

  const remaining = await add('Days remaining in sprint', board);
  await expect(remaining).toContainText(/\d+ days? (remaining in .+ on ZZ board, ending|overdue)|on ZZ board has no end date/);
  const health = await add('Sprint health', board);
  for (const term of ['Time elapsed', 'Work complete', 'Scope change']) await expect(health.getByRole('term').filter({ hasText: term })).toBeVisible();
  await expect(health.getByRole('progressbar', { name: 'Work complete', exact: true })).toBeVisible();

  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

// Delivery belongs on a dashboard, not only on a report page: the four DORA
// metrics and the deployments behind them.
test('delivery gadgets draw DORA metrics and deployment frequency', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');

  const stamp = Date.now();
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  // Work, a commit on it, and a production deployment that shipped it: what
  // the four metrics are read from.
  const created = await page.request.post('/rest/api/3/issue', {
    headers, data: { fields: { project: { key: 'ZZ' }, summary: `Delivery gadget work ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const key = (await created.json()).key as string;
  const shipped = new Date(Date.now() - 2 * 24 * 3600 * 1000).toISOString();
  expect((await page.request.post('/rest/devinfo/0.10/bulk', {
    headers,
    data: {
      repositories: [{
        id: `repo-${stamp}`, name: 'payments', url: 'https://git.example/payments', updateSequenceId: 1,
        commits: [{
          id: `c${stamp}`, displayId: `c${stamp}`, message: `Ship ${key}`, url: 'https://git.example/payments/commit',
          updateSequenceId: 1, issueKeys: [key],
          authorTimestamp: new Date(Date.now() - 3 * 24 * 3600 * 1000).toISOString(),
        }],
      }],
    },
  })).status()).toBe(202);
  expect((await page.request.post('/rest/deployments/0.1/bulk', {
    headers,
    data: {
      deployments: [{
        deploymentSequenceNumber: stamp, updateSequenceNumber: 1, issueKeys: [key],
        displayName: `Release ${stamp}`, url: 'https://git.example/payments/deploy', description: 'Shipped',
        lastUpdated: shipped, state: 'successful',
        pipeline: { id: 'release', displayName: 'Release', url: 'https://git.example/payments/pipeline' },
        environment: { id: 'prod', displayName: 'Production', type: 'production' },
      }],
    },
  })).status()).toBe(202);

  await page.goto('/dashboards');
  await page.getByLabel('Dashboard name', { exact: true }).fill(`Delivery ${stamp}`);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);

  await page.getByRole('button', { name: 'Add DORA metrics', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Configure DORA metrics' })).toBeVisible();
  await page.getByLabel('Project', { exact: true }).selectOption('ZZ');
  await page.getByLabel('Time window').selectOption('30');
  await accessible(page);
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  const dora = page.getByRole('region', { name: 'DORA metrics' });
  await expect(dora).toContainText('production deployment');
  await expect(dora).toContainText('Deployment frequency');
  await expect(dora).toContainText('Lead time for changes');
  await expect(dora).toContainText('Change failure rate');
  await expect(dora).toContainText('Time to restore service');
  // The gadget says what it counted through rather than implying everything.
  await expect(dora).toContainText(/Counting \d+ pipelines? deploying to production/);

  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Deployment frequency', exact: true }).click();
  await page.getByLabel('Project', { exact: true }).selectOption('ZZ');
  await page.getByLabel('Time window').selectOption('7');
  await page.getByRole('button', { name: 'Save gadget report' }).click();
  // "Deployment frequency" is also one of the DORA gadget's metrics, so the
  // gadget is taken by its own region rather than by the words in it.
  const frequency = page.getByRole('region', { name: 'Deployment frequency' });
  // Other specs ship to this site too, so what matters is that this
  // deployment is counted, not that it is the only one.
  await expect(frequency).toContainText(/[1-9]\d* successful/);
  await frequency.getByText('View daily counts').click();
  const days = frequency.getByRole('table', { name: 'Production deployments by day in ZZ' }).locator('tbody tr');
  expect(await days.count()).toBeGreaterThanOrEqual(7);
  await expect(days.filter({ hasText: /[1-9]/ }).first()).toBeVisible();
  await accessible(page);
});
