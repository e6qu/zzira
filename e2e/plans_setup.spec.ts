import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  return 'Basic ' + Buffer.from(`${DEMO.email}:${tokens[DEMO.email]}`).toString('base64');
}

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// Setting a plan up was REST-only: a plan had to exist before the browser
// could do anything with it. A manager now creates one, says what it reads,
// what it leaves out and who may see it.
test('a manager creates a plan, gives it sources, exclusions and access', async ({ page, browser, request }) => {
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const key = `PL${stamp.toString(36).toUpperCase().slice(-8)}`;
  const me = await (await request.get('/rest/api/3/myself', { headers })).json();
  const project = await request.post('/rest/api/3/project', {
    headers, data: { key, name: `Plan setup ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(project.status()).toBe(201);
  const fields = await (await request.get('/rest/api/3/field', { headers })).json() as Array<{ id: string; name: string }>;
  const targetStart = fields.find(field => field.name === 'Target start')!;
  const targetEnd = fields.find(field => field.name === 'Target end')!;
  const createIssue = async (summary: string, type: string) => {
    const response = await request.post('/rest/api/3/issue', {
      headers,
      data: { fields: { project: { key }, summary, issuetype: { name: type }, [targetStart.id]: '2026-11-02', [targetEnd.id]: '2027-01-29' } },
    });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const epic = await createIssue(`Plan epic ${stamp}`, 'Epic');
  const bug = await createIssue(`Plan bug ${stamp}`, 'Bug');

  await login(page, DEMO.email, DEMO.password);
  await page.goto('/plans');
  const create = page.locator('form[action="/plans"]');
  await create.getByLabel('Plan name').fill(`Setup plan ${stamp}`);
  await create.getByRole('checkbox', { name: `Projects: Plan setup ${stamp} (${key})` }).check();
  await accessible(page);
  await create.getByRole('button', { name: 'Create plan' }).click();
  await expect(page).toHaveURL(/\/plans\/\d+$/);
  const planID = page.url().split('/').pop()!;
  await expect(page.getByRole('heading', { name: `Setup plan ${stamp}`, level: 1 })).toBeVisible();
  // The plan reads the project it was given, so its work is on the timeline.
  await expect(page.locator('#main-content')).toContainText(epic);
  await expect(page.locator('#main-content')).toContainText(bug);

  // A plan with nothing to read is refused, and says so.
  await page.goto('/plans');
  await page.locator('form[action="/plans"]').getByLabel('Plan name').fill(`Empty plan ${stamp}`);
  await page.locator('form[action="/plans"]').getByRole('button', { name: 'Create plan' }).click();
  await expect(page.getByRole('alert')).toContainText('at least one board, project or filter');

  await page.goto(`/plans/${planID}`);
  await page.getByRole('link', { name: 'Plan settings' }).click();
  await expect(page).toHaveURL(`/plans/${planID}/settings`);

  // What it leaves out.
  const exclusions = page.locator('form').filter({ has: page.getByRole('button', { name: 'Save exclusion rules' }) });
  await exclusions.getByRole('checkbox', { name: 'Bug', exact: true }).check();
  await exclusions.getByLabel('Show completed work for').fill('14');
  await exclusions.getByRole('button', { name: 'Save exclusion rules' }).click();
  await expect(page.getByRole('status')).toContainText('Exclusion rules saved.');
  await page.goto(`/plans/${planID}`);
  await expect(page.locator('#main-content')).toContainText(epic);
  await expect(page.locator('#main-content')).not.toContainText(bug);

  // Who may see it: a colleague cannot until they are given access.
  const colleague = await browser.newContext();
  const ana = await colleague.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await ana.goto('/plans');
  await expect(ana.getByRole('link', { name: `Setup plan ${stamp}` })).toHaveCount(0);
  await ana.goto(`/plans/${planID}`);
  await expect(ana.locator('body')).toContainText('404');

  await page.goto(`/plans/${planID}/settings`);
  const grant = page.locator('form').filter({ has: page.getByRole('button', { name: 'Give access' }) });
  await grant.getByLabel('Person', { exact: true }).selectOption({ label: 'Ana Soursop' });
  await grant.getByLabel('Access').selectOption('View');
  await grant.getByRole('button', { name: 'Give access' }).click();
  await expect(page.getByRole('status')).toContainText('Plan access granted.');
  await accessible(page);

  await ana.goto('/plans');
  await expect(ana.getByRole('link', { name: `Setup plan ${stamp}` })).toBeVisible();
  await ana.goto(`/plans/${planID}`);
  await expect(ana.getByRole('heading', { name: `Setup plan ${stamp}`, level: 1 })).toBeVisible();
  // A viewer is not an editor: the settings page is not theirs.
  await expect(ana.getByRole('link', { name: 'Plan settings' })).toHaveCount(0);
  const refused = await ana.request.post(`/plans/${planID}/settings`, {
    headers: { Origin: new URL(ana.url()).origin, 'Content-Type': 'application/x-www-form-urlencoded' },
    form: { action: 'details', name: 'Hijacked plan' },
  });
  expect(refused.status()).toBe(403);
  await colleague.close();

  // Renaming and re-sourcing the plan.
  await page.goto(`/plans/${planID}/settings`);
  const details = page.locator('form').filter({ has: page.getByRole('button', { name: 'Save details' }) });
  await details.getByLabel('Name').fill(`Renamed plan ${stamp}`);
  await details.getByRole('button', { name: 'Save details' }).click();
  await expect(page.getByRole('status')).toContainText('Plan details saved.');
  const sources = page.locator('form').filter({ has: page.getByRole('button', { name: 'Save issue sources' }) });
  await sources.getByRole('checkbox', { name: `Projects: Plan setup ${stamp} (${key})` }).uncheck();
  await sources.getByRole('button', { name: 'Save issue sources' }).click();
  await expect(page.getByRole('alert')).toContainText('at least one board, project or filter');
  await expect(page.getByRole('heading', { name: 'Plan settings', level: 1 })).toBeVisible();

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  expect((await request.put(`/rest/api/3/plans/plan/${planID}/trash`, { headers })).status()).toBeLessThan(400);
  expect((await request.delete(`/rest/api/3/project/${key}?enableUndo=false`, { headers })).status()).toBe(204);
});
