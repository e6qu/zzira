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

// Changing many work items at once from the navigator: a field across the
// selection, and watching or unwatching them. Both were API-only.
test('navigator edits, watches and unwatches a selection', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const keys: string[] = [];
  for (const summary of [`Bulk ${stamp} one`, `Bulk ${stamp} two`]) {
    const created = await request.post('/rest/api/3/issue', {
      headers: auth,
      data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' } } },
    });
    expect(created.status()).toBe(201);
    keys.push((await created.json()).key as string);
  }

  await login(page);
  await page.goto(`/issues/ZZ?text=Bulk+${stamp}`);
  for (const key of keys) {
    await page.locator('tr').filter({ hasText: key }).getByRole('checkbox').check();
  }

  // One field, across the selection.
  await page.locator('summary').filter({ hasText: 'Edit selected' }).click();
  const editor = page.locator('.bulk-edit-picker');
  await editor.locator('select[name="field"]').selectOption('labels');
  await editor.locator('input[name="valueLabels"]').fill(`bulk-${stamp}`);
  await accessible(page);
  page.once('dialog', (dialog) => dialog.accept());
  await editor.getByRole('button', { name: 'Start edit' }).click();
  await expect(page.getByRole('heading', { name: /Bulk edit issues/ })).toBeVisible();
  await expect(page.locator('.page-header .lozenge')).toContainText(/COMPLETE|RUNNING|ENQUEUED/);

  // The label is on both work items once the task has run.
  await expect(async () => {
    for (const key of keys) {
      const issue = await (await request.get(`/rest/api/3/issue/${key}`, { headers: auth })).json();
      expect(issue.fields.labels).toContain(`bulk-${stamp}`);
    }
  }).toPass({ timeout: 20_000 });

  // Watching the selection, then not.
  await page.goto(`/issues/ZZ?text=Bulk+${stamp}`);
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

  await page.goto(`/issues/ZZ?text=Bulk+${stamp}`);
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
});
