import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

// A program manager opens a plan built from a project and follows its
// scheduled work and teams.

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

test('a plan schedules the work its sources give, with its teams', async ({ page, request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const fields = await (await request.get('/rest/api/3/field', { headers })).json() as Array<{ id: string; name: string }>;
  const targetStart = fields.find(field => field.name === 'Target start');
  const targetEnd = fields.find(field => field.name === 'Target end');
  expect(targetStart).toBeTruthy();
  expect(targetEnd).toBeTruthy();
  const createIssue = async (issueFields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields: issueFields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const epic = await createIssue({ project: { key: 'ZZ' }, summary: `Plan epic ${stamp}`, issuetype: { name: 'Epic' }, [targetStart!.id]: '2026-11-02', [targetEnd!.id]: '2027-01-29' });
  const story = await createIssue({ project: { key: 'ZZ' }, summary: `Plan story ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic }, [targetStart!.id]: '2026-11-09', [targetEnd!.id]: '2026-12-04' });

  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const created = await request.post('/rest/api/3/plans/plan', {
    headers,
    data: { name: `Delivery plan ${stamp}`, scheduling: { estimation: 'StoryPoints' }, issueSources: [{ type: 'Project', value: Number(project.id) }], exclusionRules: { numberOfDaysToShowCompletedIssues: 30 } },
  });
  expect(created.status(), await created.text()).toBe(201);
  const planID = await created.json();
  const team = await request.post(`/rest/api/3/plans/plan/${planID}/team/planonly`, { headers, data: { name: `Delivery team ${stamp}`, planningStyle: 'Scrum', capacity: 30, sprintLength: 2 } });
  expect([201, 204], await team.text()).toContain(team.status());

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.locator('.nav-plans').click();
  await expect(page).toHaveURL('/plans');
  await expect(page.getByRole('heading', { name: 'Plans', level: 1 })).toBeVisible();
  await page.getByRole('link', { name: `Delivery plan ${stamp}` }).click();
  await expect(page).toHaveURL(`/plans/${planID}`);
  await expect(page.getByRole('heading', { name: `Delivery plan ${stamp}`, level: 1 })).toBeVisible();
  await expect(page.locator('main header')).toContainText('Target start');

  const epicRow = page.locator('.timeline-epic').filter({ hasText: epic });
  await expect(epicRow.locator('.timeline-bar')).toHaveCount(1);
  const storyRow = page.locator('.timeline-child').filter({ hasText: story });
  await expect(storyRow.locator('.timeline-bar:not(.timeline-bar-open)')).toHaveCount(1);
  const teams = page.getByRole('table', { name: 'Plan teams' });
  await expect(teams).toContainText(`Delivery team ${stamp}`);
  await expect(teams).toContainText('30');
  await expect(teams).toContainText('2 weeks');

  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  expect((await page.goto('/plans/999999999'))?.status()).toBe(404);
  expect((await request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers })).status()).toBe(204);
  expect((await page.goto(`/plans/${planID}`))?.status()).toBe(404);
});
