import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('an administrator duplicates scenario drafts and edits the copy independently', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PDC${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name: `Plan copy ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  const plans: string[] = [];
  try {
    const issue = await page.request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: 'Original duplication work' } },
    });
    expect(issue.status()).toBe(201);
    const issueKey = String((await issue.json()).key);
    const created = await page.request.post('/rest/api/3/plans/plan', {
      headers, data: { name: `Source plan ${stamp}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(projectID) }] },
    });
    expect(created.status()).toBe(201);
    const sourceID = String(await created.json());
    plans.push(sourceID);
    const team = await page.request.post(`/rest/api/3/plans/plan/${sourceID}/team/planonly`, {
      headers, data: { name: 'Copy team', planningStyle: 'Kanban', capacity: 12 },
    });
    expect(team.status()).toBe(201);
    const sourceTeam = String(await team.json());
    await page.goto(`/plans/${sourceID}`);
    await page.locator('.plan-manage > summary').click();
    const createScenario = page.locator('.plan-manage form').filter({ has: page.getByRole('heading', { name: 'Create scenario' }) });
    await createScenario.getByLabel('Scenario name', { exact: true }).fill('Alternative');
    await createScenario.getByLabel('Scenario color', { exact: true }).selectOption('purple');
    await createScenario.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Scenario created.');
    const sourceScenario = await page.locator('#plan-scenario').inputValue();
    const row = page.getByRole('table', { name: 'Planned work with dates, teams, sprints and estimates' }).getByRole('row').filter({ has: page.getByRole('link', { name: issueKey, exact: true }) });
    await row.getByText(`Edit ${issueKey} in the plan`, { exact: true }).click();
    await row.locator(`#plan-summary-${issueKey}`).fill('Alternative draft');
    await row.locator(`#plan-team-${issueKey}`).selectOption(sourceTeam);
    await row.getByRole('button', { name: 'Save in scenario', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText(`${issueKey} changed in Alternative.`);
    await page.goto(`/plans/${sourceID}/settings`);
    const duplicate = page.getByRole('region', { name: 'Duplicate plan', exact: true });
    await duplicate.getByLabel('Copy name').fill('   ');
    await duplicate.getByRole('button', { name: 'Duplicate plan', exact: true }).click();
    await expect(duplicate.getByLabel('Copy name')).toHaveValue('   ');
    await duplicate.getByLabel('Copy name').fill(`Independent copy ${stamp}`);
    await duplicate.getByRole('button', { name: 'Duplicate plan', exact: true }).click();
    await expect(page.getByRole('heading', { name: `Independent copy ${stamp}`, level: 1 })).toBeVisible();
    const copiedID = new URL(page.url()).pathname.split('/').pop()!;
    plans.push(copiedID);
    const copiedScenario = await page.locator('#plan-scenario option').filter({ hasText: /^Alternative,/ }).getAttribute('value');
    expect(copiedScenario).toBeTruthy();
    expect(copiedScenario).not.toBe(sourceScenario);
    const teams = await (await page.request.get(`/rest/api/3/plans/plan/${copiedID}/team`, { headers })).json();
    const copiedTeam = String(teams.values.find((candidate: { name?: string }) => candidate.name === 'Copy team').id);
    expect(copiedTeam).not.toBe(sourceTeam);
    await page.goto(`/plans/${copiedID}?scenario=${copiedScenario}`);
    await expect(row.locator(`#plan-summary-${issueKey}`)).toHaveValue('Alternative draft');
    await expect(row.locator(`#plan-team-${issueKey}`)).toHaveValue(copiedTeam);
    await row.getByText(`Edit ${issueKey} in the plan`, { exact: true }).click();
    await row.locator(`#plan-summary-${issueKey}`).fill('Copy-only draft');
    await row.getByRole('button', { name: 'Save in scenario', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText(`${issueKey} changed in Alternative.`);
    await page.goto(`/plans/${sourceID}?scenario=${sourceScenario}`);
    await expect(row.locator(`#plan-summary-${issueKey}`)).toHaveValue('Alternative draft');
    await expect(row.locator(`#plan-team-${issueKey}`)).toHaveValue(sourceTeam);
    const committed = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers })).json();
    expect(committed.fields.summary).toBe('Original duplication work');
    await page.goto(`/plans/${copiedID}/settings`);
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
    expect(violations).toEqual([]);
  } finally {
    for (const id of plans) await page.request.put(`/rest/api/3/plans/plan/${id}/trash`, { headers, timeout: 10_000 });
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
