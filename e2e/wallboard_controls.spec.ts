import { expect, test, Page } from '@playwright/test';
import { apiAuthHeader } from './auth';
import axe from 'axe-core';

let issueKey: string;
let dashboards: string[];
const auth = () => ({ Authorization: apiAuthHeader() });

test.beforeAll(async ({ request }) => {
  const response = await request.post('/rest/api/3/issue', { headers: auth(), data: {
    fields: { project: { key: 'ZZ' }, summary: `Wallboard control fixture ${Date.now()}`, issuetype: { name: 'Task' } },
  } });
  expect(response.status()).toBe(201);
  issueKey = (await response.json()).key;
});
test.afterAll(async ({ request }) => {
  if (issueKey) expect((await request.delete(`/rest/api/3/issue/${issueKey}`, { headers: auth() })).status()).toBe(204);
});
test.beforeEach(async ({ page }) => {
  dashboards = [];
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
});
test.afterEach(async ({ request }) => {
  for (const id of dashboards) expect((await request.delete(`/rest/api/3/dashboard/${id}`, { headers: auth() })).status()).toBe(204);
});

async function openWallboard(page: Page, refresh = false, rotates = true) {
  const response = await page.request.post('/rest/api/3/dashboard', { headers: auth(), data: {
    name: `Wallboard controls ${Date.now()}`, sharePermissions: [], editPermissions: [],
  } });
  expect(response.status()).toBe(200);
  const id = (await response.json()).id;
  dashboards.push(id);
  for (const [row, title] of ['First chart', 'Second chart'].entries()) {
    const gadget = await page.request.post(`/rest/api/3/dashboard/${id}/gadget`, { headers: auth(), data: {
      moduleKey: 'com.zzira:bubble-chart', title, color: rotates || row === 0 ? 'blue' : 'red', position: { column: 0, row },
    } });
    expect(gadget.status()).toBe(200);
    const gid = (await gadget.json()).id;
    expect((await page.request.put(`/rest/api/3/dashboard/${id}/items/${gid}/properties/zzira.config`, {
      headers: auth(), data: { jql: `key = ${issueKey}`, limit: 5 },
    })).status()).toBe(201);
  }
  if (refresh) {
    await page.goto(`/dashboards/${id}?edit=1`);
    await page.getByLabel('Automatic refresh', { exact: true }).selectOption('60000');
    await page.getByRole('button', { name: 'Save layout', exact: true }).click();
  }
  await page.clock.install();
  await page.goto(`/dashboards/${id}/wallboard`);
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  expect(await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations)).toEqual([]);
}

for (const interaction of ['focus', 'expanded table']) {
  test(`rotation preserves a gadget with ${interaction} until the interaction ends`, async ({ page }) => {
    await openWallboard(page);
    const first = page.locator('[data-wallboard-gadget]').filter({ has: page.getByRole('heading', { name: 'First chart', exact: true }) });
    const summary = first.getByText('View work items', { exact: true });
    if (interaction === 'focus') await summary.focus();
    else {
      await summary.click();
      await page.getByRole('link', { name: 'Exit wallboard', exact: true }).focus();
    }
    await page.clock.fastForward(30001);
    await expect(page.getByRole('heading', { name: 'First chart', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Second chart', exact: true })).toBeHidden();
    await expect(page.locator('#wallboard-status')).toHaveText('Updates paused while you use this wallboard.');
    if (interaction === 'focus') await expect(summary).toBeFocused();
    else {
      await expect(first.locator('details')).toHaveAttribute('open', '');
      await summary.click();
    }
    await page.getByRole('link', { name: 'Exit wallboard', exact: true }).focus();
    await page.clock.fastForward(30001);
    await expect(page.getByRole('heading', { name: 'Second chart', exact: true })).toBeVisible();
  });
}

test('a refresh-only wallboard can pause and resume page reloads', async ({ page }) => {
  await openWallboard(page, true, false);
  const main = await page.locator('[data-wallboard]').elementHandle();
  await page.getByRole('button', { name: 'Pause refresh', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Resume refresh', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#wallboard-status')).toHaveText('Refresh paused.');
  await page.clock.fastForward(90001);
  expect(await main!.evaluate(node => node.isConnected)).toBe(true);
  await accessible(page);
  await page.getByRole('button', { name: 'Resume refresh', exact: true }).click();
  const loaded = page.waitForEvent('load');
  await page.clock.fastForward(30001);
  await loaded;
  await expect(page.getByRole('button', { name: 'Pause refresh', exact: true })).toBeVisible();
});

test('reduced motion starts paused and an explicit resume permits rotation', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await openWallboard(page);
  await expect(page.getByRole('button', { name: 'Resume rotation', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#wallboard-status')).toContainText('Reduced motion is preferred');
  await page.clock.fastForward(60001);
  await expect(page.getByRole('heading', { name: 'First chart', exact: true })).toBeVisible();
  await accessible(page);
  await page.getByRole('button', { name: 'Resume rotation', exact: true }).click();
  await page.clock.fastForward(30001);
  await expect(page.getByRole('heading', { name: 'Second chart', exact: true })).toBeVisible();
});

test('turning on reduced motion pauses rotation until the viewer resumes it', async ({ page }) => {
  await openWallboard(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(page.getByRole('button', { name: 'Resume rotation', exact: true })).toBeVisible();
  await page.clock.fastForward(30001);
  await expect(page.getByRole('heading', { name: 'First chart', exact: true })).toBeVisible();
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await expect(page.getByRole('button', { name: 'Resume rotation', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Resume rotation', exact: true }).click();
  await page.clock.fastForward(30001);
  await expect(page.getByRole('heading', { name: 'Second chart', exact: true })).toBeVisible();
});
