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

async function login(page: Page, email = 'demo@zzira.dev', password = 'demo1234') {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

test('admin creates, runs, audits, disables and deletes scheduled automation', async ({ page, browser }) => {
  await login(page);
  // Own the work item the rule runs on instead of relying on earlier specs.
  const fixture = await page.request.post('/rest/api/3/issue', { headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' }, data: { fields: { project: { key: 'ZZ' }, summary: `Scheduled rule work ${Date.now()}`, issuetype: { name: 'Task' } } } });
  expect(fixture.status()).toBe(201);
  const fixtureKey = (await fixture.json()).key as string;
  await page.getByRole('link', { name: 'Automation', exact: true }).click();
  await expect(page).toHaveURL('/settings/automation');
  await expect(page.getByRole('heading', { name: 'Automation rules', exact: true })).toBeVisible();
  await accessible(page);

  await page.getByRole('link', { name: 'Create rule', exact: true }).click();
  const name = `E2E schedule ${Date.now()}`;
  const label = `scheduled-${Date.now()}`;
  await page.getByLabel('Rule name').fill(name);
  await page.getByLabel('Description').fill('Marks a demo work item through the durable runner');
  await page.getByLabel('Run every').fill('60');
  await page.getByLabel('Timezone').fill('Europe/Bucharest');
  await page.getByLabel('JQL query').fill(`key = ${fixtureKey}`);
  await page.getByRole('combobox', { name: 'Action', exact: true }).selectOption('jira.issue.add-label');
  await page.getByRole('combobox', { name: 'Value', exact: true }).first().fill(label);
  await accessible(page);
  await page.getByRole('button', { name: 'Create rule' }).click();
  await expect(page).toHaveURL(/\/settings\/automation\/[0-9a-f-]+$/);
  const ruleURL = page.url();
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();

  await page.getByRole('button', { name: 'Run now' }).click();
  await expect.poll(async () => {
    await page.goto(ruleURL);
    return await page.locator('.automation-audit tbody').innerText();
  }, { timeout: 15_000 }).toContain('SUCCESS');
  await expect(page.locator('.automation-audit tbody')).toContainText('1');
  const issueResponse = await page.request.get(`/rest/api/3/issue/${fixtureKey}`);
  expect(issueResponse.ok()).toBe(true);
  const issue = await issueResponse.json();
  expect(issue.fields.labels).toContain(label);

  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);

  const memberContext = await browser.newContext();
  const member = await memberContext.newPage();
  try {
    await login(member, 'ana@zzira.dev', 'ana12345');
    await expect(member.getByRole('link', { name: 'Automation', exact: true })).toHaveCount(0);
    const denied = await member.goto('/settings/automation');
    expect(denied?.status()).toBe(403);
  } finally {
    await memberContext.close();
  }

  await page.goto(ruleURL);
  await page.getByRole('button', { name: 'Disable' }).click();
  await expect(page.getByText('DISABLED', { exact: true }).first()).toBeVisible();
  await page.locator('.automation-danger').getByText('Delete rule', { exact: true }).click();
  await page.getByRole('button', { name: 'Delete rule permanently' }).click();
  await expect(page).toHaveURL('/settings/automation');
  await expect(page.getByRole('link', { name, exact: true })).toHaveCount(0);
});


test('admin builds an event rule with a condition and smart values that runs when work is commented', async ({ page }) => {
  await login(page);
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const created = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary: `Event rule work ${stamp}`, issuetype: { name: 'Task' } } } });
  expect(created.status()).toBe(201);
  const key = (await created.json()).key as string;

  await page.goto('/settings/automation/new');
  const name = `E2E event ${stamp}`;
  await page.getByLabel('Rule name').fill(name);
  await page.getByRole('combobox', { name: 'Trigger', exact: true }).selectOption('jira.issue.event.trigger:commented');
  await page.getByLabel('JQL query').fill(`key = ${key}`);
  await page.getByRole('combobox', { name: 'Condition field', exact: true }).selectOption('status');
  await page.getByRole('combobox', { name: 'Comparison', exact: true }).selectOption('IS_NOT_EMPTY');
  await page.getByRole('combobox', { name: 'Action', exact: true }).selectOption('jira.issue.add-label');
  await page.getByRole('combobox', { name: 'Value', exact: true }).first().fill('commented-{{issue.key}}');
  await page.getByRole('combobox', { name: 'Additional action', exact: true }).selectOption('jira.issue.comment');
  await page.getByRole('combobox', { name: 'Value', exact: true }).nth(1).fill('Thanks {{initiator.displayName}}, {{issue.key}} is {{issue.status.name}}');
  await accessible(page);
  await page.getByRole('button', { name: 'Create rule' }).click();
  await expect(page).toHaveURL(/\/settings\/automation\/[0-9a-f-]+$/);
  const ruleURL = page.url();
  // The editor shows the saved trigger, condition and actions.
  await expect(page.getByRole('combobox', { name: 'Trigger', exact: true })).toHaveValue('jira.issue.event.trigger:commented');
  await expect(page.getByRole('combobox', { name: 'Condition field', exact: true }).first()).toHaveValue('status');
  await expect(page.getByRole('combobox', { name: 'Comparison', exact: true }).first()).toHaveValue('IS_NOT_EMPTY');
  await expect(page.getByRole('combobox', { name: 'Action', exact: true }).nth(1)).toHaveValue('jira.issue.comment');
  await page.goto('/settings/automation');
  await expect(page.getByRole('article').filter({ hasText: name })).toContainText('Work item commented');

  const comment = await page.request.post(`/rest/api/3/issue/${key}/comment`, { headers, data: { body: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Please take a look' }] }] } } });
  expect(comment.status()).toBe(201);
  await expect.poll(async () => (await (await page.request.get(`/rest/api/3/issue/${key}`, { headers })).json()).fields.labels, { timeout: 15_000 }).toContain(`commented-${key}`);
  const comments = await (await page.request.get(`/rest/api/3/issue/${key}/comment`, { headers })).json();
  const texts = comments.comments.map((item: any) => JSON.stringify(item.body));
  // The rule's own comment does not start it again.
  expect(texts.filter((text: string) => text.includes(`Thanks Demo User, ${key} is To Do`))).toHaveLength(1);
  await page.goto(ruleURL);
  await expect(page.locator('.automation-audit tbody')).toContainText('SUCCESS');
  await page.getByRole('button', { name: 'Disable' }).click();
  await page.locator('.automation-danger').getByText('Delete rule', { exact: true }).click();
  await page.getByRole('button', { name: 'Delete rule permanently' }).click();
  await expect(page).toHaveURL('/settings/automation');
});

test('admin schedules a rule with a Quartz cron expression', async ({ page }) => {
  await login(page);
  const name = `E2E cron ${Date.now()}`;
  const fill = async (expression: string) => {
    await page.goto('/settings/automation/new');
    await page.getByLabel('Rule name').fill(name);
    await page.getByLabel('Timezone').fill('Europe/Bucharest');
    await page.getByLabel('Cron expression').fill(expression);
    await page.getByRole('combobox', { name: 'Action', exact: true }).selectOption('jira.issue.add-label');
    await page.getByRole('combobox', { name: 'Value', exact: true }).first().fill('weekday-morning');
  };
  // Quartz features the scheduler does not run are refused.
  await fill('0 0 9 ? * MON#2');
  await page.getByRole('button', { name: 'Create rule' }).click();
  await expect(page.locator('body')).toContainText('L, W and # are not supported');

  await fill('0 0 9 ? * MON-FRI');
  await accessible(page);
  await page.getByRole('button', { name: 'Create rule' }).click();
  await expect(page).toHaveURL(/\/settings\/automation\/[0-9a-f-]+$/);
  const ruleURL = page.url();
  await expect(page.getByLabel('Cron expression')).toHaveValue('0 0 9 ? * MON-FRI');
  await expect(page.getByRole('combobox', { name: 'Trigger', exact: true })).toHaveValue('jira.jql.scheduled');
  await page.goto('/settings/automation');
  const card = page.getByRole('article').filter({ hasText: name });
  await expect(card).toContainText('Scheduled · Cron 0 0 9 ? * MON-FRI in Europe/Bucharest · Next run');

  await page.goto(ruleURL);
  await page.getByRole('button', { name: 'Disable' }).click();
  await page.locator('.automation-danger').getByText('Delete rule', { exact: true }).click();
  await page.getByRole('button', { name: 'Delete rule permanently' }).click();
  await expect(page).toHaveURL('/settings/automation');
});

test('the editor keeps a branched rule it cannot show by turning saving off', async ({ page }) => {
  await login(page);
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const tenant = await (await page.request.get('/_edge/tenant_info')).json();
  const base = `/gateway/api/automation/public/jira/${tenant.cloudId}/rest/v1/rule`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const name = `E2E branch ${Date.now()}`;
  const created = await page.request.post(base, { headers, data: { rule: {
    name, state: 'ENABLED', actor: { type: 'ACCOUNT_ID', actor: me.accountId },
    trigger: { component: 'TRIGGER', type: 'jira.issue.event.trigger:created', schemaVersion: 1, value: { jql: 'project = ZZ' } },
    components: [{ component: 'BRANCH', type: 'jira.issue.related', schemaVersion: 1, value: { relatedType: 'sub-tasks' },
      // A condition inside a branch is something the editor does not show.
      children: [{ component: 'CONDITION', type: 'jira.jql.condition', schemaVersion: 1, value: { jql: 'status != Done' } },
        { component: 'ACTION', type: 'jira.issue.add-label', schemaVersion: 1, value: { label: 'from-{{triggerIssue.key}}' } }] }],
  }, connections: [] } });
  expect(created.status(), await created.text()).toBe(201);
  const uuid = (await created.json()).ruleUuid as string;

  await page.goto(`/settings/automation/${uuid}`);
  await expect(page.getByRole('note')).toContainText('The editor does not show its branches, so saving here is turned off');
  await expect(page.getByRole('button', { name: 'Save rule', exact: true })).toBeDisabled();
  await expect(page.getByRole('combobox', { name: 'Trigger', exact: true })).toHaveValue('jira.issue.event.trigger:created');
  await accessible(page);
  // Saving is refused by the server too, so the branch survives.
  // A form submitted from this page carries its origin, which session posts require.
  const refused = await page.request.post(`/settings/automation/${uuid}`, { headers: { Origin: new URL(page.url()).origin }, form: { operation: 'save', name, trigger_type: 'jira.issue.event.trigger:created', action_type: 'jira.issue.add-label', action_value: 'flattened' } });
  expect(refused.status()).toBe(400);
  expect(await refused.text()).toContain('the editor does not show its branches');
  const rule = await (await page.request.get(`${base}/${uuid}`, { headers })).json();
  expect(rule.rule.components[0].component).toBe('BRANCH');

  await page.getByRole('button', { name: 'Disable' }).click();
  await page.locator('.automation-danger').getByText('Delete rule', { exact: true }).click();
  await page.getByRole('button', { name: 'Delete rule permanently' }).click();
  await expect(page).toHaveURL('/settings/automation');
});

test('admin builds a rule with a JQL condition and a branch for linked work in the editor', async ({ page }) => {
  await login(page);
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const issue = async (summary: string) => {
    const created = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' } } } });
    expect(created.status()).toBe(201);
    return (await created.json()).key as string;
  };
  const blocker = await issue(`Blocker ${stamp}`);
  const blocked = await issue(`Blocked ${stamp}`);
  const link = await page.request.post('/rest/api/3/issueLink', { headers, data: { type: { name: 'Blocks' }, inwardIssue: { key: blocker }, outwardIssue: { key: blocked } } });
  expect(link.status(), await link.text()).toBe(201);

  await page.goto('/settings/automation/new');
  const name = `E2E branch editor ${stamp}`;
  await page.getByLabel('Rule name').fill(name);
  await page.getByLabel('JQL query').fill(`key = ${blocker}`);
  await page.getByRole('combobox', { name: 'Condition field', exact: true }).selectOption('jql');
  await page.getByLabel('Compared with').fill('status != Done');
  await page.getByRole('combobox', { name: 'Action', exact: true }).selectOption('jira.issue.add-label');
  await page.getByRole('combobox', { name: 'Value', exact: true }).first().fill(`blocker-${stamp}`);
  await page.getByRole('combobox', { name: 'Related work items', exact: true }).selectOption('linked');
  await page.getByLabel('Link types').fill('Blocks');
  await page.getByRole('combobox', { name: 'Additional branch action', exact: true }).selectOption('jira.issue.add-label');
  await page.getByRole('combobox', { name: 'Branch value', exact: true }).first().fill('blocked-by-{{triggerIssue.key}}');
  await accessible(page);
  await page.getByRole('button', { name: 'Create rule' }).click();
  await expect(page).toHaveURL(/\/settings\/automation\/[0-9a-f-]+$/);
  const ruleURL = page.url();
  // The editor shows the whole rule, so saving stays available.
  await expect(page.getByRole('note')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Save rule', exact: true })).toBeEnabled();
  await expect(page.getByRole('combobox', { name: 'Condition field', exact: true }).first()).toHaveValue('jql');
  await expect(page.getByRole('combobox', { name: 'Related work items', exact: true })).toHaveValue('linked');
  await expect(page.getByLabel('Link types')).toHaveValue('Blocks');
  await expect(page.getByRole('combobox', { name: 'Branch action', exact: true })).toHaveValue('jira.issue.add-label');

  await page.getByRole('button', { name: 'Run now' }).click();
  await expect.poll(async () => (await (await page.request.get(`/rest/api/3/issue/${blocked}`, { headers })).json()).fields.labels, { timeout: 15_000 }).toContain(`blocked-by-${blocker}`);
  expect((await (await page.request.get(`/rest/api/3/issue/${blocker}`, { headers })).json()).fields.labels).toContain(`blocker-${stamp}`);

  // Saving again from the editor keeps the branch.
  await page.goto(ruleURL);
  await page.getByRole('button', { name: 'Save rule', exact: true }).click();
  await expect(page).toHaveURL(ruleURL);
  await expect(page.getByRole('combobox', { name: 'Related work items', exact: true })).toHaveValue('linked');
  await page.getByRole('button', { name: 'Disable' }).click();
  await page.locator('.automation-danger').getByText('Delete rule', { exact: true }).click();
  await page.getByRole('button', { name: 'Delete rule permanently' }).click();
  await expect(page).toHaveURL('/settings/automation');
});
