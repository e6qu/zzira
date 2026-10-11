import { apiAuthHeader, seedPassword } from './auth';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('auto-scheduling uses live release dates and preserves active-sprint work', async ({ page }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `RAS${stamp.toString().slice(-7)}`;
  const day = (offset: number) => new Date(stamp + offset * 86400000).toISOString().slice(0, 10);
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const createdProject = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name: `Release scheduling ${stamp}`, projectTypeKey: 'software', projectTemplateKey: 'com.pyxis.greenhopper.jira:gh-simplified-agility-scrum', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(createdProject.status(), await createdProject.text()).toBe(201);
  const projectID = String((await createdProject.json()).id);
  let planID = '';
  try {
    const version = await page.request.post('/rest/api/3/version', { headers, data: { project: key, name: `Release ${stamp}`, startDate: day(20), releaseDate: day(34) } });
    expect(version.status(), await version.text()).toBe(201);
    const versionID = String((await version.json()).id);
    const work: string[] = [];
    for (const summary of ['Release-only work', 'Active sprint work']) {
      const issue = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key }, issuetype: { name: 'Story' }, summary, fixVersions: [{ id: versionID }] } } });
      expect(issue.status(), await issue.text()).toBe(201);
      work.push((await issue.json()).key);
    }
    const boards = await (await page.request.get(`/rest/agile/1.0/board?projectKeyOrId=${key}&type=scrum`, { headers })).json();
    const sprint = await page.request.post('/rest/agile/1.0/sprint', { headers, data: { name: `Active sprint ${stamp}`, originBoardId: boards.values[0].id, startDate: `${day(-5)}T09:00:00Z`, endDate: `${day(5)}T17:00:00Z` } });
    expect(sprint.status(), await sprint.text()).toBe(201);
    const sprintID = String((await sprint.json()).id);
    expect((await page.request.post(`/rest/agile/1.0/sprint/${sprintID}/issue`, { headers, data: { issues: [work[1]] } })).status()).toBe(204);
    const started = await page.request.put(`/rest/agile/1.0/sprint/${sprintID}`, { headers, data: { state: 'active' } });
    expect(started.status(), await started.text()).toBe(200);
    const plan = await page.request.post('/rest/api/3/plans/plan', {
      headers, data: { name: `Release scheduling ${stamp}`, leadAccountId: me.accountId, scheduling: { estimation: 'StoryPoints', startDate: { type: 'TargetStartDate' }, endDate: { type: 'DueDate' } }, issueSources: [{ type: 'Project', value: Number(projectID) }] },
    });
    expect(plan.status(), await plan.text()).toBe(201);
    planID = String(await plan.json());
    await page.goto('/login');
    await page.getByLabel('Email').fill('demo@zzira.dev');
    await page.getByLabel('Password').fill(seedPassword());
    await page.getByRole('button', { name: 'Log in' }).click();
    await page.goto(`/plans/${planID}`);
    await page.getByLabel('Schedule automatically', { exact: true }).selectOption('all');
    await page.locator('#plan-schedule-from').fill(day(0));
    await page.getByRole('button', { name: 'Schedule work', exact: true }).click();
    await expect(page.getByRole('status')).toContainText('Planned 1 work items');
    await expect(page.getByRole('status')).toContainText('active sprint');
    const row = page.getByRole('table', { name: 'Planned work with dates, teams, sprints and estimates', exact: true }).locator('tbody tr').filter({ has: page.getByRole('link', { name: work[0], exact: true }) });
    await row.getByText(`Edit ${work[0]} in the plan`, { exact: true }).click();
    await expect(row.getByLabel('Start date', { exact: true })).toHaveValue(day(20));
    await expect(row.getByLabel('End date', { exact: true })).toHaveValue(day(34));
    await page.reload();
    const committed = await (await page.request.get(`/rest/api/3/issue/${work[0]}?fields=duedate`, { headers })).json();
    expect(committed.fields.duedate).toBeNull();
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
  } finally {
    if (planID) await page.request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers, timeout: 10_000 });
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
