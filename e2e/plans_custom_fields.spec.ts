import { apiAuthHeader } from './auth';
import { scopeFixtureField } from './field_contexts';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('a planner selects custom columns and filters persisted work by labels', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const key = `PCF${stamp.toString().slice(-7)}`;
  const fieldName = `Planning labels ${stamp}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const project = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name: `Custom columns ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const projectID = String((await project.json()).id);
  let fieldID = '', planID = '';
  try {
    const field = await page.request.post('/rest/api/3/field', {
      headers, data: { name: fieldName, type: 'com.atlassian.jira.plugin.system.customfieldtypes:labels' },
    });
    expect(field.status()).toBe(201);
    fieldID = String((await field.json()).id);
    const contexts = await (await page.request.get(`/rest/api/3/field/${fieldID}/context`, { headers })).json();
    await scopeFixtureField(page.request, fieldID, String(contexts.values[0].id), projectID, headers);
    for (const color of ['blue', 'red']) {
      const issue = await page.request.post('/rest/api/3/issue', {
        headers, data: { fields: { project: { key }, issuetype: { name: 'Task' }, summary: `${color} planning work`, [fieldID]: [color] } },
      });
      expect(issue.status()).toBe(201);
    }
    const created = await page.request.post('/rest/api/3/plans/plan', {
      headers, data: { name: `Custom columns ${stamp}`, leadAccountId: me.accountId, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(projectID) }] },
    });
    expect(created.status()).toBe(201);
    planID = String(await created.json());
    await page.goto(`/plans/${planID}/settings`);
    const fields = page.getByRole('region', { name: 'Custom fields', exact: true });
    // Refused filters keep their answer so the planner can select the field.
    await fields.getByLabel(`Enable ${fieldName} filter`, { exact: true }).check();
    await fields.getByRole('button', { name: 'Save custom fields', exact: true }).click();
    await expect(page.getByRole('alert')).toHaveText('Add a custom field to the plan before enabling its filter.');
    await expect(fields.getByLabel(`Enable ${fieldName} filter`, { exact: true })).toBeChecked();
    await fields.getByLabel(fieldName, { exact: true }).check();
    await fields.getByRole('button', { name: 'Save custom fields', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Custom fields saved.');
    await page.reload();
    await expect(fields.getByLabel(fieldName, { exact: true })).toBeChecked();
    const stored = await (await page.request.get(`/rest/api/3/plans/plan/${planID}`, { headers })).json();
    expect(stored.customFields).toEqual([{ customFieldId: Number(fieldID.replace('customfield_', '')), filter: true }]);
    await page.goto(`/plans/${planID}`);
    const work = page.getByRole('table', { name: 'Planned work with dates, teams, sprints and estimates' });
    await expect(work.getByRole('columnheader', { name: fieldName, exact: true })).toBeVisible();
    await expect(work.getByRole('row').filter({ hasText: 'blue planning work' }).getByRole('cell', { name: 'blue', exact: true })).toBeVisible();
    await page.getByLabel(fieldName, { exact: true }).selectOption({ label: 'blue' });
    await page.getByRole('button', { name: 'Show', exact: true }).click();
    await expect(work.getByRole('row').filter({ hasText: 'blue planning work' })).toHaveCount(1);
    await expect(work.getByRole('row').filter({ hasText: 'red planning work' })).toHaveCount(0);
    await page.reload();
    await expect(page.getByLabel(fieldName, { exact: true })).toHaveValue('"blue"');
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
    expect(violations).toEqual([]);
    await page.goto(`/plans/${planID}/settings`);
    await fields.getByLabel(`Enable ${fieldName} filter`, { exact: true }).uncheck();
    await fields.getByLabel(fieldName, { exact: true }).uncheck();
    await fields.getByRole('button', { name: 'Save custom fields', exact: true }).click();
    await expect(page.getByRole('status')).toHaveText('Custom fields saved.');
    await page.goto(`/plans/${planID}`);
    await expect(work.getByRole('columnheader', { name: fieldName, exact: true })).toHaveCount(0);
    await expect(page.getByLabel(fieldName, { exact: true })).toHaveCount(0);
    await expect(work.getByRole('row').filter({ hasText: 'red planning work' })).toHaveCount(1);
  } finally {
    if (planID) await page.request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers, timeout: 10_000 });
    if (fieldID) {
      await page.request.post(`/rest/api/3/field/${fieldID}/trash`, { headers, timeout: 10_000 });
      await page.request.delete(`/rest/api/3/field/${fieldID}`, { headers, timeout: 10_000 });
    }
    await page.request.delete(`/rest/api/3/project/${projectID}`, { headers, timeout: 10_000 });
  }
});
