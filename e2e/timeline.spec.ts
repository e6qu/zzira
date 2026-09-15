import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// A project manager plans an epic's work on the software project timeline,
// and the Roadmap feature governs whether the timeline exists.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('schedule an epic and its work on the project timeline', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const createIssue = async (fields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const fields = await (await request.get('/rest/api/3/field', { headers })).json();
  const startField = fields.find((field: { name: string }) => field.name === 'Start date');
  expect(startField).toBeTruthy();
  const epic = await createIssue({ project: { key: 'ZZ' }, summary: `Timeline launch ${stamp}`, issuetype: { name: 'Epic' }, duedate: '2026-12-18', [startField.id]: '2026-11-02' });
  const story = await createIssue({ project: { key: 'ZZ' }, summary: `Timeline sign-up ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic } });

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.goto('/projects/ZZ');
  await page.locator('.nav-timeline').click();
  await expect(page).toHaveURL('/projects/ZZ/timeline');
  await expect(page.getByRole('heading', { name: 'Timeline', level: 1 })).toBeVisible();

  const epicRow = page.locator('.timeline-epic').filter({ hasText: epic });
  await expect(epicRow).toBeVisible();
  await expect(epicRow.locator('.timeline-bar')).toHaveCount(1);
  const storyRow = page.locator('.timeline-child').filter({ hasText: story });
  await expect(storyRow.locator('.timeline-bar')).toHaveCount(0);

  await storyRow.locator('.timeline-schedule > summary').click();
  await storyRow.getByLabel('Start date').fill('2026-11-09');
  await storyRow.getByLabel('Due date').fill('2026-11-03');
  await storyRow.getByRole('button', { name: 'Save dates' }).click();
  await expect(page.getByRole('alert')).toContainText('The due date cannot be before the start date.');

  const retry = page.locator('.timeline-child').filter({ hasText: story });
  await retry.locator('.timeline-schedule > summary').click();
  await retry.getByLabel('Start date').fill('2026-11-09');
  await retry.getByLabel('Due date').fill('2026-11-20');
  await retry.getByRole('button', { name: 'Save dates' }).click();
  await expect(page.getByRole('status')).toContainText(`${story} scheduled.`);
  const scheduled = page.locator('.timeline-child').filter({ hasText: story });
  await expect(scheduled.locator('.timeline-bar:not(.timeline-bar-open)')).toHaveCount(1);
  const bean = await (await request.get(`/rest/api/3/issue/${story}`, { headers })).json();
  expect(bean.fields.duedate).toBe('2026-11-20');
  expect(bean.fields[startField.id]).toBe('2026-11-09');

  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Turning Roadmap off removes the timeline.
  const key = `TL${stamp.toString(36).slice(-6).toUpperCase()}`;
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  const project = await request.post('/rest/api/3/project', { headers, data: { key, name: `Timeline ${key}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' } });
  expect(project.status(), await project.text()).toBe(201);
  await page.goto(`/projects/${key}/timeline`);
  await expect(page.getByText('No epics yet')).toBeVisible();
  const disabled = await request.put(`/rest/api/3/project/${key}/features/jsw.classic.roadmap`, { headers, data: { state: 'DISABLED' } });
  expect(disabled.status(), await disabled.text()).toBe(200);
  expect((await page.goto(`/projects/${key}/timeline`))?.status()).toBe(404);
  await page.goto(`/projects/${key}`);
  await expect(page.locator('.nav-timeline')).toHaveCount(0);
});
