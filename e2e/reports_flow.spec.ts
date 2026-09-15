import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// An agile coach reads how work flows through a board: the cumulative flow
// diagram and the control chart pick up work that moved through the columns.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('cumulative flow and control chart follow work through the board', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const created = await request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary: `Flowing work ${Date.now()}`, issuetype: { name: 'Task' } } } });
  expect(created.status(), await created.text()).toBe(201);
  const key = (await created.json()).key as string;
  const moveTo = async (category: string) => {
    const transitions = (await (await request.get(`/rest/api/3/issue/${key}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
    const transition = transitions.find(candidate => candidate.to.statusCategory.key === category);
    expect(transition, `a transition to ${category}`).toBeTruthy();
    const moved = await request.post(`/rest/api/3/issue/${key}/transitions`, { headers, data: { transition: { id: transition!.id } } });
    expect(moved.status(), await moved.text()).toBe(204);
  };
  await moveTo('indeterminate');
  await moveTo('done');

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/projects/ZZ');
  await page.locator('.nav-reports').click();

  await page.getByRole('link', { name: 'Open Cumulative flow diagram' }).click();
  await expect(page.getByRole('heading', { name: 'Cumulative flow diagram', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: 'ZZ board' });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByLabel('Time window').selectOption('14');
  await page.getByRole('button', { name: 'Show window' }).click();
  await expect(page).toHaveURL(/days=14/);
  await expect(page.locator('.flow-band')).not.toHaveCount(0);
  await page.getByText('View daily counts', { exact: true }).click();
  const counts = page.getByRole('table', { name: 'Daily column counts' });
  await expect(counts.locator('tbody tr')).toHaveCount(14);
  await expect(counts.locator('thead')).toContainText('Done');
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open Control chart' }).click();
  await expect(page.getByRole('heading', { name: 'Control chart', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: 'ZZ board' });
  await page.getByRole('button', { name: 'Show board' }).click();
  await expect(page.getByRole('table', { name: 'Completed work cycle times' })).toContainText(key);
  await expect(page.getByRole('region', { name: 'Cycle time summary' })).toContainText('Average cycle time');
  await expect(page.locator('.control-point').first()).toBeAttached();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await page.goto('/projects/ZZ/reports/control-chart?days=7'))?.status()).toBe(400);
  await page.setViewportSize({ width: 1280, height: 720 });

  // The same work shows in the issue analysis reports.
  const resolvedBean = await (await request.get(`/rest/api/3/issue/${key}`, { headers })).json();
  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open Created vs. resolved' }).click();
  await expect(page.getByRole('heading', { name: 'Created vs. resolved', level: 1 })).toBeVisible();
  await page.getByLabel('Time window').selectOption('7');
  await page.getByLabel('Running totals').check();
  await page.getByRole('button', { name: 'Update' }).click();
  await expect(page).toHaveURL(/days=7/);
  await expect(page).toHaveURL(/cumulative=true/);
  const totals = page.getByRole('region', { name: 'Created and resolved totals' });
  await expect(totals).toContainText('Created');
  expect(Number(await totals.locator('article').first().locator('strong').textContent())).toBeGreaterThanOrEqual(1);
  if (resolvedBean.fields.resolutiondate) {
    expect(Number(await totals.locator('article').nth(1).locator('strong').textContent())).toBeGreaterThanOrEqual(1);
  }
  await page.getByText('View daily counts', { exact: true }).click();
  await expect(page.getByRole('table', { name: 'Created and resolved by day' }).locator('tbody tr')).toHaveCount(7);
  await accessible(page);

  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open Resolution time' }).click();
  await expect(page.getByRole('heading', { name: 'Resolution time', level: 1 })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Resolution summary' })).toContainText('Average resolution time');
  await page.getByText('View daily resolution time', { exact: true }).click();
  await expect(page.getByRole('table', { name: 'Resolution time by day' }).locator('tbody tr')).toHaveCount(30);
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await page.goto('/projects/ZZ/reports/resolution-time?days=14'))?.status()).toBe(400);
});
