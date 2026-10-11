import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('a planner infers missing dates from releases and sprints without committing them', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PID${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name: `Inferred dates ${stamp}`, projectTypeKey: 'software', projectTemplateKey: 'com.pyxis.greenhopper.jira:gh-simplified-agility-scrum', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  let planID = '';
  try {
    const version = await page.request.post('/rest/api/3/version', { headers, data: { project: key, name: `Release ${stamp}`, startDate: '2026-11-02', releaseDate: '2026-11-20' } });
    expect(version.status()).toBe(201);
    const versionID = String((await version.json()).id);
    const issue = await page.request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key }, issuetype: { name: 'Story' }, summary: 'Work with empty dates', fixVersions: [{ id: versionID }] } },
    });
    expect(issue.status()).toBe(201);
    const issueKey = String((await issue.json()).key);
    const boards = await (await page.request.get(`/rest/agile/1.0/board?projectKeyOrId=${key}&type=scrum`, { headers })).json();
    const sprint = await page.request.post('/rest/agile/1.0/sprint', { headers, data: { name: `Sprint ${stamp}`, originBoardId: boards.values[0].id, startDate: '2026-12-01T09:00:00Z', endDate: '2026-12-11T17:00:00Z' } });
    expect(sprint.status()).toBe(201);
    const sprintID = String((await sprint.json()).id);
    expect((await page.request.post(`/rest/agile/1.0/sprint/${sprintID}/issue`, { headers, data: { issues: [issueKey] } })).status()).toBe(204);
    const created = await page.request.post('/rest/api/3/plans/plan', {
      headers, data: { name: `Inferred dates plan ${stamp}`, leadAccountId: me.accountId, scheduling: { estimation: 'StoryPoints', startDate: { type: 'TargetStartDate' }, endDate: { type: 'DueDate' } }, issueSources: [{ type: 'Project', value: Number(projectID) }] },
    });
    expect(created.status()).toBe(201);
    planID = String(await created.json());
    const setInference = async (mode: string) => {
      await page.goto(`/plans/${planID}/settings`);
      const form = page.locator('form').filter({ has: page.getByRole('button', { name: 'Save scheduling', exact: true }) });
      await form.getByLabel('Work with empty start or end dates', { exact: true }).selectOption(mode);
      await form.getByRole('button', { name: 'Save scheduling', exact: true }).click();
      await expect(page.getByRole('status')).toHaveText('Scheduling saved.');
      await page.reload();
      await expect(form.getByLabel('Work with empty start or end dates', { exact: true })).toHaveValue(mode);
      const persisted = await (await page.request.get(`/rest/api/3/plans/plan/${planID}`, { headers })).json();
      expect(persisted.scheduling.inferredDates).toBe(mode);
      await page.goto(`/plans/${planID}`);
    };
    const row = () => page.getByRole('table', { name: 'Planned work with dates, teams, sprints and estimates' }).getByRole('row').filter({ hasText: issueKey });
    await setInference('ReleaseDates');
    await expect(row().getByText('Release dates', { exact: true })).toHaveCount(2);
    await expect(row().locator('td').nth(0)).toContainText('02/Nov/26');
    await expect(row().locator('td').nth(1)).toContainText('20/Nov/26');
    await row().getByText(`Edit ${issueKey} in the plan`, { exact: true }).click();
    await expect(row().getByLabel('Start date', { exact: true })).toHaveValue('');
    await expect(row().getByLabel('End date', { exact: true })).toHaveValue('');
    await row().getByLabel('Summary', { exact: true }).fill('Unrelated planning draft');
    await row().getByRole('button', { name: 'Save in scenario', exact: true }).click();
    await expect(page.locator('#plan-scenario option:checked')).toContainText('1 unsaved');
    await setInference('SprintDates');
    await expect(row().getByText('Sprint dates', { exact: true })).toHaveCount(2);
    await expect(row().locator('td').nth(0)).toContainText('01/Dec/26');
    await expect(row().locator('td').nth(1)).toContainText('11/Dec/26');
    await row().getByText(`Edit ${issueKey} in the plan`, { exact: true }).click();
    await row().getByLabel('Start date', { exact: true }).fill('2026-12-03');
    await row().getByRole('button', { name: 'Save in scenario', exact: true }).click();
    await expect(row().getByText('Sprint dates', { exact: true })).toHaveCount(1);
    await expect(row().locator('td').nth(0)).toContainText('03/Dec/26');
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
    expect(violations).toEqual([]);
    await page.setViewportSize({ width: 1280, height: 720 });
    await setInference('None');
    await expect(row().getByText('Sprint dates', { exact: true })).toHaveCount(0);
    await expect(row().locator('td').nth(1)).toContainText('None');
    const committed = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers })).json();
    expect(committed.fields.summary).toBe('Work with empty dates');
    expect(committed.fields.duedate).toBeNull();
  } finally {
    if (planID) await page.request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers, timeout: 10_000 });
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
