import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// A program manager opens a plan built from a project and follows its
// scheduled work and teams.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a plan schedules the work its sources give, with its teams', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const fields = await (await request.get('/rest/api/3/field', { headers })).json() as Array<{ id: string; name: string }>;
  const targetStart = fields.find(field => field.name === 'Target start');
  const targetEnd = fields.find(field => field.name === 'Target end');
  expect(targetStart).toBeTruthy();
  expect(targetEnd).toBeTruthy();
  const createIssue = async (issueFields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields: issueFields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const epic = await createIssue({ project: { key: 'ZZ' }, summary: `Plan epic ${stamp}`, issuetype: { name: 'Epic' }, [targetStart!.id]: '2026-11-02', [targetEnd!.id]: '2027-01-29' });
  const story = await createIssue({ project: { key: 'ZZ' }, summary: `Plan story ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic }, [targetStart!.id]: '2026-11-09', [targetEnd!.id]: '2026-12-04' });

  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const created = await request.post('/rest/api/3/plans/plan', {
    headers,
    data: { name: `Delivery plan ${stamp}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(project.id) }], exclusionRules: { numberOfDaysToShowCompletedIssues: 30 } },
  });
  expect(created.status(), await created.text()).toBe(201);
  const planID = await created.json();
  const team = await request.post(`/rest/api/3/plans/plan/${planID}/team/planonly`, { headers, data: { name: `Delivery team ${stamp}`, planningStyle: 'Scrum', capacity: 30, sprintLength: 2 } });
  expect([201, 204], await team.text()).toContain(team.status());

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.locator('.nav-plans').click();
  await expect(page).toHaveURL('/plans');
  await expect(page.getByRole('heading', { name: 'Plans', level: 1 })).toBeVisible();
  await page.getByRole('link', { name: `Delivery plan ${stamp}` }).click();
  await expect(page).toHaveURL(`/plans/${planID}`);
  await expect(page.getByRole('heading', { name: `Delivery plan ${stamp}`, level: 1 })).toBeVisible();
  await expect(page.locator('main header')).toContainText('Target start');

  const epicRow = page.locator('.timeline-epic').filter({ hasText: epic });
  await expect(epicRow.locator('.timeline-bar')).toHaveCount(1);
  const storyRow = page.locator('.timeline-child').filter({ hasText: story });
  await expect(storyRow.locator('.timeline-bar:not(.timeline-bar-open)')).toHaveCount(1);
  const teams = page.getByRole('table', { name: 'Plan teams' });
  await expect(teams).toContainText(`Delivery team ${stamp}`);
  await expect(teams).toContainText('30');
  await expect(teams).toContainText('2 weeks');

  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  expect((await page.goto('/plans/999999999'))?.status()).toBe(404);
  expect((await request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers })).status()).toBe(204);
  expect((await page.goto(`/plans/${planID}`))?.status()).toBe(404);
});

test('a planner plans a team sprint in a scenario and saves the changes to Jira', async ({ page, request }) => {
  test.setTimeout(120_000);
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const fields = await (await request.get('/rest/api/3/field', { headers })).json() as Array<{ id: string; name: string; schema?: { type?: string } }>;
  const teamField = fields.find(field => field.name === 'Team' && field.schema?.type === 'team');
  const points = fields.find(field => field.name === 'Story point estimate');
  const targetStart = fields.find(field => field.name === 'Target start');
  const targetEnd = fields.find(field => field.name === 'Target end');
  expect(teamField && points && targetStart && targetEnd).toBeTruthy();

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/teams');
  await page.getByLabel('Team name').fill(`Delivery squad ${stamp}`);
  await page.getByLabel('Description').fill('Plans sprints');
  await page.getByRole('button', { name: 'Create team' }).click();
  await expect(page).toHaveURL(/\/teams\/[0-9a-f-]{36}\?saved=/);
  const teamID = (await page.locator('main header code').textContent())!.trim();

  const boards = await (await request.get('/rest/agile/1.0/board?projectKeyOrId=ZZ&type=scrum', { headers })).json();
  const boardID = boards.values[0].id;
  const sprint = await request.post('/rest/agile/1.0/sprint', { headers, data: { name: `Plan sprint ${stamp}`, originBoardId: boardID } });
  expect(sprint.status(), await sprint.text()).toBe(201);
  const sprintID = (await sprint.json()).id;
  const createIssue = async (summary: string, extra: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Story' }, [teamField!.id]: { id: teamID }, ...extra } } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const blocker = await createIssue(`Schema ${stamp}`, { [points!.id]: 3, [targetStart!.id]: '2026-11-02', [targetEnd!.id]: '2026-11-06' });
  const story = await createIssue(`Checkout ${stamp}`, { [points!.id]: 13, [targetStart!.id]: '2026-11-09', [targetEnd!.id]: '2026-11-20' });
  expect((await request.post(`/rest/agile/1.0/sprint/${sprintID}/issue`, { headers, data: { issues: [blocker, story] } })).status()).toBe(204);
  const link = await request.post('/rest/api/3/issueLink', { headers, data: { type: { name: 'Blocks' }, inwardIssue: { key: blocker }, outwardIssue: { key: story } } });
  expect(link.status(), await link.text()).toBe(201);

  const created = await request.post('/rest/api/3/plans/plan', {
    headers,
    data: { name: `Sprint plan ${stamp}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Board', value: boardID }], exclusionRules: { numberOfDaysToShowCompletedIssues: 30 } },
  });
  expect(created.status(), await created.text()).toBe(201);
  const planID = await created.json();
  expect((await request.post(`/rest/api/3/plans/plan/${planID}/team/atlassian`, { headers, data: { id: teamID, planningStyle: 'Scrum', capacity: 10, sprintLength: 2 } })).status()).toBe(204);

  await page.goto(`/plans/${planID}`);
  const teamName = `Delivery squad ${stamp}`;
  const teamsTable = page.getByRole('table', { name: 'Plan teams' });
  await teamsTable.getByText(`Settings for ${teamName}`).click();
  await teamsTable.getByLabel('Issue source').selectOption({ label: 'Board: ZZ board' });
  await teamsTable.getByRole('button', { name: 'Save team settings' }).click();
  await expect(page.getByRole('status')).toHaveText('Team settings saved.');

  // The team's sprint holds more estimate than its capacity, and the blocking
  // work shares the sprint of the work it blocks.
  const capacity = page.getByRole('table', { name: `${teamName} capacity by iteration in story points` });
  const sprintRow = capacity.getByRole('row').filter({ hasText: `Plan sprint ${stamp}` });
  await expect(sprintRow).toContainText('16');
  await expect(sprintRow).toContainText('Over capacity');
  const dependencies = page.getByRole('table', { name: 'Blocking work in the plan' });
  const dependencyRow = dependencies.getByRole('row').filter({ hasText: story });
  await expect(dependencyRow).toContainText(blocker);
  await expect(dependencyRow).toContainText('Off track');
  // A work item's row in the planned work is the one headed by its own key;
  // the blocking work's row, and the dependencies table, also name it.
  const plannedWork = page.getByRole('table', { name: 'Planned work with dates, teams, sprints and estimates' });
  const rowOf = (key: string) => plannedWork.getByRole('row').filter({ has: page.getByRole('rowheader').getByRole('link', { name: key, exact: true }) });
  await expect(rowOf(story)).toContainText(`Blocked by ${blocker}`);
  await expect(rowOf(blocker)).toContainText(`Blocks ${story}`);
  await accessible(page);

  // A new scenario takes the changes; the default is untouched.
  await page.locator('.plan-manage > summary').click();
  // The panel holds the form that creates a scenario and the one that edits
  // the current scenario, with the same field names.
  const createScenario = page.locator('.plan-manage form').filter({ has: page.getByRole('heading', { name: 'Create scenario' }) });
  await createScenario.getByLabel('Scenario name', { exact: true }).fill('Stretch');
  await createScenario.getByLabel('Scenario color', { exact: true }).selectOption('orange');
  await createScenario.getByRole('button', { name: 'Create scenario' }).click();
  await expect(page.getByRole('status')).toHaveText('Scenario created.');
  // The new scenario is the one shown, and it holds no changes yet.
  await expect(page.locator('#plan-scenario option:checked')).toHaveText('Stretch, 0 unsaved');
  const storyRow = rowOf(story);
  await storyRow.getByText(`Edit ${story} in the plan`).click();
  await storyRow.getByLabel('Estimate (story points)').fill('5');
  await storyRow.getByLabel('End date').fill('2026-11-27');
  await storyRow.getByRole('button', { name: 'Save in scenario' }).click();
  await expect(page.getByRole('status')).toHaveText(`${story} changed in Stretch.`);
  await expect(rowOf(story).locator('.plan-flag')).toHaveCount(2);
  await expect(capacity.getByRole('row').filter({ hasText: `Plan sprint ${stamp}` })).toContainText('Within capacity');
  await expect(dependencies.getByRole('row').filter({ hasText: story })).toContainText('Off track');
  await page.getByLabel('Dependent work items can be scheduled to the same iteration').check();
  await page.getByRole('button', { name: 'Save setting' }).click();
  await expect(page.getByRole('table', { name: 'Blocking work in the plan' }).getByRole('row').filter({ hasText: story })).toContainText('On track');
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Review changes saves the estimate to Jira and discards the new end date.
  await page.getByRole('link', { name: /Review changes \(2\)/ }).click();
  await expect(page.getByRole('heading', { name: 'Review changes', level: 1 })).toBeVisible();
  const changes = page.getByRole('table', { name: 'Unsaved changes in the Stretch scenario' });
  await expect(changes).toContainText('Estimate: 13 story points → 5 story points');
  await expect(changes).toContainText('End date: 2026-11-20 → 2026-11-27');
  await accessible(page);
  await page.getByLabel(`Select End date of ${story}`).uncheck();
  await page.getByLabel('Send update notifications').uncheck();
  await page.getByRole('button', { name: 'Save selected changes' }).click();
  await expect(page.getByRole('status')).toHaveText('1 changes saved to Jira.');
  const saved = await (await request.get(`/rest/api/3/issue/${story}?fields=${points!.id},${targetEnd!.id}`, { headers })).json();
  expect(saved.fields[points!.id]).toBe(5);
  expect(saved.fields[targetEnd!.id]).toBe('2026-11-20');
  await page.getByRole('button', { name: 'Discard selected changes' }).click();
  await expect(page.getByRole('status')).toHaveText('1 changes discarded.');
  await expect(page.getByText('No unsaved changes')).toBeVisible();
  expect((await request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers })).status()).toBe(204);
});

test('a planner groups and filters a plan, and keeps the view', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const createIssue = async (issueFields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields: issueFields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const first = await createIssue({ project: { key: 'ZZ' }, summary: `Grouped epic ${stamp}`, issuetype: { name: 'Epic' } });
  const second = await createIssue({ project: { key: 'ZZ' }, summary: `Other epic ${stamp}`, issuetype: { name: 'Epic' } });
  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const created = await request.post('/rest/api/3/plans/plan', {
    headers,
    data: { name: `Grouping plan ${stamp}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(project.id) }], exclusionRules: { numberOfDaysToShowCompletedIssues: 30 } },
  });
  expect(created.status(), await created.text()).toBe(201);
  const planID = await created.json();

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await page.goto(`/plans/${planID}`);
  await expect(page.getByRole('heading', { name: `Grouping plan ${stamp}`, level: 1 })).toBeVisible();
  const views = page.locator('.plan-views');
  await expect(page.locator('.plan-group-row')).toHaveCount(0);

  // Grouped, the work sits under a heading per value, with how many are
  // under it.
  await views.getByLabel('Group by').selectOption('status');
  await views.getByRole('button', { name: 'Show' }).click();
  await expect(page.locator('.plan-group-heading').first()).toBeVisible();
  await expect(page.locator('.timeline-table')).toContainText(first);
  await expect(page.locator('.timeline-table')).toContainText(second);

  // Filtered, only the work that matches is left.
  await views.getByLabel('Filter').fill(first);
  await views.getByRole('button', { name: 'Show' }).click();
  await expect(page.locator('.timeline-table')).toContainText(first);
  await expect(page.locator('.timeline-table')).not.toContainText(second);
  await accessible(page);

  // Rolled up, a parent reads as the work under it: the epic carries the
  // story's dates and estimate, while the form still edits its own.
  await views.getByLabel('Filter').fill('');
  await views.getByLabel(/Add the work under a parent/).check();
  await views.getByRole('button', { name: 'Show' }).click();
  await expect(page.locator('.plan-table')).toContainText(first);

  // Kept under a name, the same reading comes back from its link.
  await views.getByLabel('Keep this view as').fill(`Mine ${stamp}`);
  await views.getByRole('button', { name: 'Save view' }).click();
  await expect(page.getByRole('status')).toContainText(`Saved the view Mine ${stamp}`);
  await expect(page.locator('.plan-view-list')).toContainText(`grouped by status`);
  await expect(page.locator('.plan-view-list')).toContainText('rolled up');
  await page.goto(`/plans/${planID}`);
  await expect(page.locator('.timeline-table')).toContainText(second);
  await page.locator('.plan-view-list').getByRole('link', { name: `Mine ${stamp}` }).click();
  await expect(page.locator('.timeline-table')).toContainText(first);
  await expect(page.getByLabel(/Add the work under a parent/)).toBeChecked();

  // A filter that matches nothing says so rather than showing an empty table.
  await page.goto(`/plans/${planID}?q=nothing-matches-${stamp}`);
  await expect(page.locator('.plan-no-matches')).toBeVisible();

  await page.locator('.plan-view-list li').filter({ hasText: `Mine ${stamp}` }).getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('status')).toContainText('The view was deleted.');
  await expect(page.locator('.plan-view-list')).toHaveCount(0);
});
