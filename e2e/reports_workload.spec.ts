import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// A delivery manager asks who is holding the work, how much time it is
// estimated to still take, and how the project's work divides.

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

async function reflows(page: Page) {
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
}

test('workload and time tracking reports say who holds the work and what it will cost', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const versionResponse = await request.post('/rest/api/3/version', { headers, data: { name: `Workload ${stamp}`, projectId: Number(project.id) } });
  expect(versionResponse.status(), await versionResponse.text()).toBe(201);
  const version = await versionResponse.json();
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();

  const createIssue = async (fields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  // Two work items in the version, one of them mine with time logged
  // against it, and one outside the version so the version narrows.
  const mine = await createIssue({
    project: { key: 'ZZ' }, summary: `Workload mine ${stamp}`, issuetype: { name: 'Task' },
    assignee: { accountId: me.accountId }, fixVersions: [{ id: version.id }],
    timetracking: { originalEstimate: '4h', remainingEstimate: '3h' },
  });
  const unassigned = await createIssue({
    project: { key: 'ZZ' }, summary: `Workload unassigned ${stamp}`, issuetype: { name: 'Bug' },
    fixVersions: [{ id: version.id }], timetracking: { originalEstimate: '2h', remainingEstimate: '2h' },
  });
  const elsewhere = await createIssue({
    project: { key: 'ZZ' }, summary: `Workload elsewhere ${stamp}`, issuetype: { name: 'Task' },
    timetracking: { originalEstimate: '1h', remainingEstimate: '1h' },
  });
  const logged = await request.post(`/rest/api/3/issue/${mine}/worklog`, {
    headers,
    data: { timeSpent: '1h', comment: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'probe' }] }] } },
  });
  expect(logged.status(), await logged.text()).toBe(201);

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');

  // User workload: the whole project, by the person holding the work.
  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open User workload' }).click();
  await expect(page.getByRole('heading', { name: 'User workload', level: 1 })).toBeVisible();
  const people = page.getByRole('table', { name: 'Unresolved work by assignee' });
  await expect(people).toContainText('Demo User');
  // Two of the three are unassigned: the one in the version and the one
  // outside it.
  await expect(people.getByRole('row').filter({ hasText: 'Unassigned' })).toContainText('3h');
  await accessible(page);
  await reflows(page);
  const workloadCSV = await downloadCSV(page);
  expect(workloadCSV.name).toMatch(/^ZZ-user-workload-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(workloadCSV.lines[0]).toBe('Assignee,Unresolved work items,Remaining estimate');
  expect(workloadCSV.lines.find((line) => line.startsWith('Unassigned,'))).toBeTruthy();

  // Version workload: the same question of one version, by person and by
  // work type, and the work outside the version is not in it.
  await page.goto('/projects/ZZ/reports/version-workload');
  await page.getByLabel('Version').selectOption(version.id);
  await page.getByRole('button', { name: 'Show version' }).click();
  await expect(page.getByRole('region', { name: 'Workload summary' })).toContainText('2');
  await expect(page.getByRole('table', { name: 'Unresolved work by work type' })).toContainText('Bug');
  const versionCSV = await downloadCSV(page);
  expect(versionCSV.lines.filter((line) => line.startsWith('Work type,')).length).toBe(2);

  // Time tracking: estimates against what the work has cost.
  await page.goto('/projects/ZZ/reports/time-tracking');
  await expect(page.getByRole('heading', { name: 'Time tracking', level: 1 })).toBeVisible();
  const tracking = page.getByRole('table', { name: 'Estimates and time spent for each unresolved work item' });
  await expect(tracking).toContainText(mine);
  await expect(tracking).toContainText(elsewhere);
  // The totals cover whatever else the project holds, so the hour logged
  // here is read on its own row.
  await expect(tracking.getByRole('row').filter({ hasText: mine })).toContainText('1h');
  await accessible(page);
  await page.getByLabel('Version').selectOption(version.id);
  await page.getByRole('button', { name: 'Show', exact: true }).click();
  await expect(tracking).toContainText(mine);
  await expect(tracking).not.toContainText(elsewhere);
  const trackingCSV = await downloadCSV(page);
  expect(trackingCSV.lines[0]).toBe('Key,Summary,Original estimate,Remaining estimate,Time spent,Accuracy');
  expect(trackingCSV.lines.find((line) => line.startsWith(`${mine},`))).toBeTruthy();

  // Work by field: how the project's work divides, by the field chosen.
  await page.goto('/projects/ZZ/reports/group-by');
  await expect(page.getByRole('heading', { name: 'Work by field', level: 1 })).toBeVisible();
  const grouped = page.getByRole('table', { name: 'Work items counted by the chosen field' });
  await expect(grouped).toContainText('Demo User');
  await page.getByLabel('Group by').selectOption('issuetype');
  await page.getByRole('button', { name: 'Group' }).click();
  await expect(grouped).toContainText('Bug');
  await expect(grouped).toContainText('%');
  await accessible(page);
  await reflows(page);
  const groupedCSV = await downloadCSV(page);
  expect(groupedCSV.lines[0]).toBe('Work type,Work items,Share');
  expect(groupedCSV.lines.find((line) => line.startsWith('Bug,'))).toBeTruthy();

  // Resolved work is not workload: finishing one takes it out.
  const transitions = (await (await request.get(`/rest/api/3/issue/${unassigned}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
  const done = transitions.find((candidate) => candidate.to.statusCategory.key === 'done');
  expect(done).toBeTruthy();
  expect((await request.post(`/rest/api/3/issue/${unassigned}/transitions`, { headers, data: { transition: { id: done!.id } } })).status()).toBe(204);
  await page.goto('/projects/ZZ/reports/user-workload');
  await expect(page.getByRole('table', { name: 'Unresolved work by assignee' }).getByRole('row').filter({ hasText: 'Unassigned' })).toContainText('1h');
});
