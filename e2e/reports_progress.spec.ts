import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// A product owner follows an epic and a version to completion through the
// epic report and the version report.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function downloadCSV(page: Page): Promise<{ name: string; lines: string[] }> {
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Download CSV' }).click()]);
  return { name: download.suggestedFilename(), lines: fs.readFileSync((await download.path())!, 'utf8').trim().split('\n') };
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('epic and version reports follow work to completion', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const version = await request.post('/rest/api/3/version', { headers, data: { name: `Progress ${stamp}`, projectId: Number(project.id) } });
  expect(version.status(), await version.text()).toBe(201);
  const versionBean = await version.json();
  const createIssue = async (fields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const epic = await createIssue({ project: { key: 'ZZ' }, summary: `Progress epic ${stamp}`, issuetype: { name: 'Epic' } });
  const finished = await createIssue({ project: { key: 'ZZ' }, summary: `Finished ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic }, fixVersions: [{ id: versionBean.id }] });
  const open = await createIssue({ project: { key: 'ZZ' }, summary: `Open ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic }, fixVersions: [{ id: versionBean.id }] });
  const transitions = (await (await request.get(`/rest/api/3/issue/${finished}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
  const done = transitions.find(candidate => candidate.to.statusCategory.key === 'done');
  expect(done).toBeTruthy();
  expect((await request.post(`/rest/api/3/issue/${finished}/transitions`, { headers, data: { transition: { id: done!.id } } })).status()).toBe(204);

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open Epic report' }).click();
  await expect(page.getByRole('heading', { name: 'Epic report', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: 'ZZ board' });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByRole('combobox', { name: /^Epic/ }).selectOption(epic);
  await page.getByRole('button', { name: 'Show epic' }).click();
  await expect(page.getByRole('region', { name: 'Progress summary' })).toContainText('1 of 2 work items done');
  await expect(page.getByRole('table', { name: 'Completed work items' })).toContainText(finished);
  await expect(page.getByRole('table', { name: 'Incomplete work items' })).toContainText(open);
  const epicCSV = await downloadCSV(page);
  expect(epicCSV.name).toMatch(new RegExp(`^ZZ-epic-report-${epic}-\\d{4}-\\d{2}-\\d{2}\\.csv$`));
  expect(epicCSV.lines.find((line) => line.includes(`,${finished},`))).toMatch(/^Completed/);
  expect(epicCSV.lines.find((line) => line.includes(`,${open},`))).toMatch(/^Incomplete/);
  await expect(page.locator('.agile-chart .chart-completed-line')).toHaveCount(1);
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.goto('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open Version report' }).click();
  await expect(page.getByRole('heading', { name: 'Version report', level: 1 })).toBeVisible();
  await page.getByLabel('Board', { exact: true }).selectOption({ label: 'ZZ board' });
  await page.getByRole('button', { name: 'Show board' }).click();
  await page.getByLabel('Version', { exact: true }).selectOption(versionBean.id);
  await page.getByRole('button', { name: 'Show version' }).click();
  await expect(page.getByRole('region', { name: 'Progress summary' })).toContainText('50%');
  await expect(page.getByRole('table', { name: 'Completed work items' })).toContainText(finished);
  await page.getByText('View daily progress', { exact: true }).click();
  await expect(page.getByRole('table', { name: 'Daily progress' })).toBeVisible();
  const versionCSV = await downloadCSV(page);
  expect(versionCSV.lines.some((line) => line.includes(`,${finished},`))).toBe(true);
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  expect((await page.goto('/projects/ZZ/reports/epic?epic=ZZ-999999'))?.status()).toBe(404);
});
