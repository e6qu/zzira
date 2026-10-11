import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('an administrator archives, trashes and restores a plan without losing its scenario', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PLC${stamp.toString().slice(-7)}`;
  const name = `Lifecycle plan ${stamp}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name: `Lifecycle sources ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  let planID = '';
  try {
    const issue = await page.request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: 'Committed lifecycle work' } },
    });
    expect(issue.status()).toBe(201);
    const issueKey = String((await issue.json()).key);
    const created = await page.request.post('/rest/api/3/plans/plan', {
      headers, data: { name, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(projectID) }] },
    });
    expect(created.status()).toBe(201);
    planID = String(await created.json());
    await page.goto(`/plans/${planID}`);
    await page.locator('.plan-manage > summary').click();
    const createScenario = page.locator('.plan-manage form').filter({ has: page.getByRole('heading', { name: 'Create scenario' }) });
    await createScenario.getByLabel('Scenario name', { exact: true }).fill('Retained draft');
    await createScenario.getByLabel('Scenario color', { exact: true }).selectOption('purple');
    await createScenario.getByRole('button', { name: 'Create scenario', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Scenario created.');
    const scenarioID = await page.locator('#plan-scenario').inputValue();
    const work = page.getByRole('table', { name: 'Planned work with dates, teams, sprints and estimates' }).getByRole('row').filter({ has: page.getByRole('link', { name: issueKey, exact: true }) });
    await work.getByText(`Edit ${issueKey} in the plan`, { exact: true }).click();
    await work.locator(`#plan-summary-${issueKey}`).fill('Retained planning draft');
    await work.getByRole('button', { name: 'Save in scenario', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText(`${issueKey} changed in Retained draft.`);
    // Walk all pages: other journeys may have already created over 100 plans.
    const showPlan = async (status = 'Active') => {
      await page.goto(`/plans?status=${status}`);
      for (let n = 0; n < 100; n++) {
        if (await planRow().count()) return;
        const next = page.getByRole('link', { name: 'Next plans', exact: true });
        await expect(next).toBeVisible();
        await next.click();
      }
      throw new Error('Lifecycle plan was not found in lifecycle pages');
    };
    const planRow = () => page.getByRole('table', { name: 'Plans you can view' }).getByRole('row').filter({ hasText: name });
    await showPlan();
    await planRow().getByText(`Manage ${name}`, { exact: true }).click();
    await planRow().getByRole('button', { name: `Archive ${name}`, exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Plan archived.');
    await showPlan('Archived');
    await expect(page.getByText('Archived plans can be restored at any time.', { exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Archived', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(planRow().getByRole('link', { name, exact: true })).toHaveCount(0);
    await planRow().getByRole('button', { name: `Restore ${name}`, exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Plan restored.');
    await showPlan();
    await planRow().getByText(`Manage ${name}`, { exact: true }).click();
    await planRow().getByRole('button', { name: `Move to trash ${name}`, exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Plan moved to trash.');
    await showPlan('Trashed');
    await expect(page.getByText('Plans can be restored for 60 days after moving to trash.', { exact: false })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Trash', exact: true })).toHaveAttribute('aria-current', 'page');
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
    expect(violations).toEqual([]);
    await planRow().getByRole('button', { name: `Restore ${name}`, exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Plan restored.');
    await page.goto(`/plans/${planID}?scenario=${scenarioID}`);
    await expect(page.locator('#plan-scenario option:checked')).toHaveText('Retained draft, 1 unsaved');
    await expect(work.locator(`#plan-summary-${issueKey}`)).toHaveValue('Retained planning draft');
    const committed = await (await page.request.get(`/rest/api/3/issue/${issueKey}`, { headers })).json();
    expect(committed.fields.summary).toBe('Committed lifecycle work');
  } finally {
    if (planID) await page.request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers, timeout: 10_000 });
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
