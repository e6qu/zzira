import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// Changing many work items at once from the navigator: several fields across
// the selection in one task, and watching or unwatching them. All were
// API-only.
test('navigator edits, watches and unwatches a selection', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  // Its own project, so a component and a version can be counted on and no
  // other spec's work is edited.
  const projectKey = `BK${stamp.toString(36).toUpperCase().slice(-8)}`;
  const me = await (await request.get('/rest/api/3/myself', { headers: auth })).json();
  const project = await request.post('/rest/api/3/project', {
    headers: auth,
    data: { key: projectKey, name: `Bulk edit ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const componentName = `Runtime ${stamp}`;
  expect((await request.post('/rest/api/3/component', { headers: auth, data: { name: componentName, project: projectKey } })).status()).toBe(201);
  const versionName = `Release ${stamp}`;
  expect((await request.post('/rest/api/3/version', { headers: auth, data: { name: versionName, project: projectKey } })).status()).toBe(201);

  const keys: string[] = [];
  for (const summary of [`Bulk ${stamp} one`, `Bulk ${stamp} two`]) {
    const created = await request.post('/rest/api/3/issue', {
      headers: auth,
      data: { fields: { project: { key: projectKey }, summary, issuetype: { name: 'Task' } } },
    });
    expect(created.status()).toBe(201);
    keys.push((await created.json()).key as string);
  }

  await login(page);
  await page.goto(`/issues/${projectKey}?text=Bulk+${stamp}`);
  for (const key of keys) {
    await page.locator('tr').filter({ hasText: key }).getByRole('checkbox').check();
  }

  // Six fields, across the selection, in one task.
  await page.locator('summary').filter({ hasText: 'Edit selected' }).click();
  const editor = page.locator('.bulk-edit-picker');
  for (const field of ['assignee', 'priority', 'duedate', 'labels', 'components', 'fixVersions']) {
    await editor.locator(`input[name="field"][value="${field}"]`).check();
  }
  // The project lead is assignable in their own project; the point is that
  // the assignee changes at all, which it never did through this form.
  await editor.locator('select[name="valueAssignee"]').selectOption({ label: me.displayName });
  const priority = editor.locator('select[name="valuePriority"]');
  await priority.selectOption({ index: 1 });
  const priorityName = (await priority.locator('option').nth(1).textContent())!.trim();
  await editor.locator('input[name="valueDueDate"]').fill('2027-02-03');
  await editor.locator('input[name="valueLabels"]').fill(`bulk-${stamp}`);
  await editor.locator('select[name="valueComponents"]').selectOption({ label: componentName });
  await editor.locator('select[name="valueFixVersions"]').selectOption({ label: versionName });
  await accessible(page);
  page.once('dialog', (dialog) => dialog.accept());
  await editor.getByRole('button', { name: 'Start edit' }).click();
  await expect(page.getByRole('heading', { name: /Bulk edit issues/ })).toBeVisible();
  await expect(page.locator('.page-header .lozenge')).toContainText(/COMPLETE|RUNNING|ENQUEUED/);

  // Every field the editor was asked to change is on both work items once the
  // task has run.
  await expect(async () => {
    for (const key of keys) {
      const issue = await (await request.get(`/rest/api/3/issue/${key}`, { headers: auth })).json();
      expect(issue.fields.labels).toContain(`bulk-${stamp}`);
      expect(issue.fields.assignee?.displayName).toBe(me.displayName);
      expect(issue.fields.priority?.name).toBe(priorityName);
      expect(issue.fields.duedate).toBe('2027-02-03');
      expect((issue.fields.components ?? []).map((c: any) => c.name)).toContain(componentName);
      expect((issue.fields.fixVersions ?? []).map((v: any) => v.name)).toContain(versionName);
    }
  }).toPass({ timeout: 20_000 });

  // Watching the selection, then not.
  await page.goto(`/issues/${projectKey}?text=Bulk+${stamp}`);
  for (const key of keys) {
    await page.locator('tr').filter({ hasText: key }).getByRole('checkbox').check();
  }
  await page.getByRole('button', { name: 'Watch selected', exact: true }).click();
  await expect(page.getByRole('heading', { name: /Bulk watch issues/ })).toBeVisible();
  await expect(async () => {
    for (const key of keys) {
      const watchers = await (await request.get(`/rest/api/3/issue/${key}/watchers`, { headers: auth })).json();
      expect(watchers.isWatching).toBe(true);
    }
  }).toPass({ timeout: 20_000 });

  await page.goto(`/issues/${projectKey}?text=Bulk+${stamp}`);
  for (const key of keys) {
    await page.locator('tr').filter({ hasText: key }).getByRole('checkbox').check();
  }
  await page.getByRole('button', { name: 'Unwatch selected' }).click();
  await expect(page.getByRole('heading', { name: /Bulk unwatch issues/ })).toBeVisible();
  await expect(async () => {
    for (const key of keys) {
      const watchers = await (await request.get(`/rest/api/3/issue/${key}/watchers`, { headers: auth })).json();
      expect(watchers.isWatching).toBe(false);
    }
  }).toPass({ timeout: 20_000 });

  expect((await request.delete(`/rest/api/3/project/${projectKey}?enableUndo=false`, { headers: auth })).status()).toBe(204);
});
