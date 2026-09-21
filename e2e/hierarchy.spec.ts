import { test, expect, Page } from '@playwright/test';
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
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('an administrator adds a level above Epic and parents an epic under it', async ({ page, request }) => {
  const stamp = Date.now().toString(36);
  const levelName = `Initiative ${stamp}`;
  const workTypeName = `Initiative type ${stamp}`;
  await login(page);

  // A work type to live on the new level.
  const createdType = await request.post('/rest/api/3/issuetype', {
    headers: { Authorization: apiAuthHeader() },
    data: { name: workTypeName, description: 'A body of epics.', type: 'standard' },
  });
  expect(createdType.status()).toBe(201);

  await page.goto('/settings/hierarchy');
  await expect(page.getByRole('heading', { name: 'Work type hierarchy', level: 1 })).toBeVisible();
  await expect(page.locator('.hierarchy-level-card')).toHaveCount(3);

  // Add the level, then move the work type onto it.
  await page.getByLabel('Level name').fill(levelName);
  await page.getByRole('button', { name: 'Add level' }).click();
  await expect(page.getByRole('status')).toContainText(`${levelName} added`);
  const card = page.locator('.hierarchy-level-card[data-level="2"]');
  await expect(card.getByRole('heading', { name: levelName })).toBeVisible();
  await card.getByLabel(`Move a work type to ${levelName}`).selectOption({ label: workTypeName });
  await card.getByRole('button', { name: 'Move here' }).click();
  await expect(page.getByRole('status')).toContainText(`${workTypeName} moved.`);
  await expect(page.locator('.hierarchy-level-card[data-level="2"] .hierarchy-work-types')).toContainText(workTypeName);

  // The REST hierarchy reports the level for the project.
  const hierarchy = await request.get('/rest/api/3/project/10000/hierarchy', {
    headers: { Authorization: apiAuthHeader() },
  });
  expect(hierarchy.status()).toBe(200);
  const body = await hierarchy.json();
  const levels = body.hierarchy.map((entry: { name: string; level: number }) => entry.name);
  expect(levels).toContain(levelName);
  expect(body.hierarchy[0].level).toBe(2);

  // Work of the new type takes epics as children.
  const initiative = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `Grow the platform ${stamp}`, issuetype: { name: workTypeName } } },
  });
  expect(initiative.status()).toBe(201);
  const initiativeKey = (await initiative.json()).key as string;
  const epic = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      fields: {
        project: { key: 'ZZ' }, summary: `Search ${stamp}`, issuetype: { name: 'Epic' },
        parent: { key: initiativeKey },
      },
    },
  });
  expect(epic.status()).toBe(201);
  const epicKey = (await epic.json()).key as string;

  // A task cannot skip the epic level.
  const skipped = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      fields: {
        project: { key: 'ZZ' }, summary: `Skips a level ${stamp}`, issuetype: { name: 'Task' },
        parent: { key: initiativeKey },
      },
    },
  });
  expect(skipped.status()).toBe(400);

  // The epic's page shows its parent, and the initiative lists the epic.
  await page.goto(`/browse/${epicKey}`);
  await expect(page.getByRole('link', { name: new RegExp(initiativeKey) }).first()).toBeVisible();
  await page.goto(`/browse/${initiativeKey}`);
  await expect(page.getByRole('link', { name: new RegExp(epicKey) }).first()).toBeVisible();

  // The roadmap rolls up through the new level: the initiative carries the
  // epic, and the epic still carries its own work.
  const task = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `Index ${stamp}`, issuetype: { name: 'Task' }, parent: { key: epicKey } } },
  });
  expect(task.status(), await task.text()).toBe(201);
  const taskKey = (await task.json()).key as string;

  await page.goto('/projects/ZZ/timeline');
  await expect(page.getByRole('heading', { name: 'Timeline', level: 1 })).toBeVisible();
  const indentOf = async (key: string) => {
    const work = page.locator('tr').filter({ has: page.locator(`a[href="/browse/${key}"]`) }).locator('.timeline-work');
    await expect(work).toHaveCount(1);
    return work.evaluate(element => (element as HTMLElement).style.paddingLeft);
  };
  expect(await indentOf(initiativeKey)).toBe('0px');
  expect(await indentOf(epicKey)).toBe('20px');
  expect(await indentOf(taskKey)).toBe('40px');
});
