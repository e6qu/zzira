import { apiAuthHeader } from './auth';
import { scopeFixtureField } from './field_contexts';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('a plan reads its timeline dates from the custom fields chosen in settings', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PDT${stamp.toString().slice(-7)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name: `Plan dates ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);
  const fieldIDs: string[] = [];
  let planID = '';
  try {
    for (const name of ['Planned start', 'Planned finish']) {
      const createdField = await page.request.post('/rest/api/3/field', {
        headers, data: { name: `${name} ${stamp}`, type: 'com.atlassian.jira.plugin.system.customfieldtypes:datepicker' },
      });
      expect(createdField.status()).toBe(201);
      const id = String((await createdField.json()).id);
      fieldIDs.push(id);
      const contexts = await (await page.request.get(`/rest/api/3/field/${id}/context`, { headers })).json();
      await scopeFixtureField(page.request, id, String(contexts.values[0].id), projectID, headers);
    }
    const issue = await page.request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: 'Use custom plan dates', [fieldIDs[0]]: '2026-11-03', [fieldIDs[1]]: '2026-11-18' } },
    });
    expect(issue.status()).toBe(201);
    const issueKey = String((await issue.json()).key);
    const plan = await page.request.post('/rest/api/3/plans/plan', {
      headers, data: { name: `Custom dates ${stamp}`, leadAccountId: me.accountId, scheduling: { estimation: 'Days' }, issueSources: [{ type: 'Project', value: Number(projectID) }] },
    });
    expect(plan.status()).toBe(201);
    planID = String(await plan.json());
    await page.goto(`/plans/${planID}/settings`);
    const scheduling = page.locator('form').filter({ has: page.getByRole('button', { name: 'Save scheduling' }) });
    const choices = fieldIDs.map(id => `DateCustomField:${id.replace('customfield_', '')}`);
    await scheduling.getByLabel('Start date field').selectOption(choices[0]);
    await scheduling.getByLabel('End date field').selectOption(choices[1]);
    await scheduling.getByRole('button', { name: 'Save scheduling' }).click();
    await expect(page.getByRole('status')).toContainText('Scheduling saved.');
    await page.reload();
    await expect(scheduling.getByLabel('Start date field')).toHaveValue(choices[0]);
    await expect(scheduling.getByLabel('End date field')).toHaveValue(choices[1]);
    const persisted = await (await page.request.get(`/rest/api/3/plans/plan/${planID}`, { headers })).json();
    expect(persisted.scheduling.startDate).toEqual({ type: 'DateCustomField', dateCustomFieldId: Number(fieldIDs[0].replace('customfield_', '')) });
    expect(persisted.scheduling.endDate).toEqual({ type: 'DateCustomField', dateCustomFieldId: Number(fieldIDs[1].replace('customfield_', '')) });
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
    expect(violations).toEqual([]);
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto(`/plans/${planID}`);
    await expect(page.locator(`#plan-start-${issueKey}`)).toHaveValue('2026-11-03');
    await expect(page.locator(`#plan-end-${issueKey}`)).toHaveValue('2026-11-18');
  } finally {
    if (planID) await page.request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers, timeout: 10_000 });
    for (const id of fieldIDs) {
      await page.request.post(`/rest/api/3/field/${id}/trash`, { headers, timeout: 10_000 });
      await page.request.delete(`/rest/api/3/field/${id}`, { headers, timeout: 10_000 });
    }
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
