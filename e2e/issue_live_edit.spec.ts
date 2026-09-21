import { expect, test, Page } from '@playwright/test';
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
  await expect(page).not.toHaveURL(/\/login/);
}

// Somebody else's change arriving while you are typing is held back until you
// are done. What it must not do is land afterwards: by then it is a picture of
// the work item taken before your own edit, and applying it puts the field you
// just filled in back to empty.
test('a change held back while you type does not undo the edit you make', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const created = await request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Live edit ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const key = (await created.json()).key as string;

  await login(page);
  await page.goto(`/browse/${key}`);
  await expect(page.locator('.issue-summary')).toHaveText(`Live edit ${stamp}`);

  // Typing into a field is what holds the other change back.
  const due = page.locator('#field-duedate');
  await due.click();
  await due.fill('2027-05-06');

  // Somebody else changes the work item while the box is still being filled.
  expect((await request.put(`/rest/api/3/issue/${key}`, {
    headers: auth, data: { fields: { summary: `Live edit ${stamp} from elsewhere` } },
  })).status()).toBe(204);
  await page.waitForTimeout(3000);
  // It is held back, so what was typed is still there.
  await expect(due).toHaveValue('2027-05-06');

  // Saving is where the held-back picture would land, and it must not put the
  // field back to what it was before.
  await due.locator('xpath=..').getByRole('button', { name: 'Save due date' }).click();
  await expect(page.locator('#field-duedate')).toHaveValue('2027-05-06');
  await page.waitForTimeout(3000);
  await expect(page.locator('#field-duedate')).toHaveValue('2027-05-06');
  // The other person's change is there too: holding it back is not losing it.
  await expect(page.locator('.issue-summary')).toHaveText(`Live edit ${stamp} from elsewhere`);

  const bean = await (await request.get(`/rest/api/3/issue/${key}`, { headers: auth })).json();
  expect(bean.fields.duedate).toBe('2027-05-06');
});

// Two fields saved one after the other, without waiting for the first answer:
// both were sent, and both must be on the page afterwards. The first answer
// used to take the work item view off the page with it, and the second was
// swapped into the view that had gone -- so a field was saved and the page
// went on showing it empty until a reload.
test('a second field saved before the first answers is shown, not swallowed', async ({ page, request }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const created = await request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Quick saves ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const key = (await created.json()).key as string;

  await login(page);
  await page.goto(`/browse/${key}`);
  const labels = page.locator('#field-labels');
  await labels.fill(`quick-${stamp}`);
  await labels.locator('xpath=..').getByRole('button', { name: 'Save labels' }).click();
  // No wait: the due date is saved while the labels answer is still coming.
  const due = page.locator('#field-duedate');
  await due.fill('2027-04-05');
  await due.locator('xpath=..').getByRole('button', { name: 'Save due date' }).click();

  await expect(page.locator('#field-duedate')).toHaveValue('2027-04-05');
  await expect(page.locator('#field-labels')).toHaveValue(`quick-${stamp}`);
  // And it stays: a late answer does not put either of them back.
  await page.waitForTimeout(2000);
  await expect(page.locator('#field-duedate')).toHaveValue('2027-04-05');
  await expect(page.locator('#field-labels')).toHaveValue(`quick-${stamp}`);

  const bean = await (await request.get(`/rest/api/3/issue/${key}`, { headers: auth })).json();
  expect(bean.fields.duedate).toBe('2027-04-05');
  expect(bean.fields.labels).toEqual([`quick-${stamp}`]);
});
