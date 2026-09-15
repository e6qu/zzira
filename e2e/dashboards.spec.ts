import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const email = 'demo@zzira.dev';
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[email];
  return 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64');
}

// Own the work the gadgets count instead of relying on specs that happened to
// run earlier against the same database.
test.beforeAll(async ({ request }) => {
  const created = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `Dashboard fixture ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
});

async function login(page: Page, email = 'demo@zzira.dev', password = 'demo1234') {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
}
async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('create, configure, share, refresh, copy and delete a dashboard', async ({ page, browser }) => {
  await login(page);
  // A new dashboard starts with its owner's default sharing.
  const shareAuth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  expect((await page.request.put('/rest/api/3/filter/defaultShareScope', { headers: shareAuth, data: { scope: 'AUTHENTICATED' } })).status()).toBe(200);
  await page.locator('.nav-dashboards').click();
  const everyone = page.getByRole('group', { name: 'Who can view?', exact: true }).getByLabel('Everyone in this workspace', { exact: true });
  await expect(everyone).toBeChecked();
  expect((await page.request.put('/rest/api/3/filter/defaultShareScope', { headers: shareAuth, data: { scope: 'PRIVATE' } })).status()).toBe(200);
  await page.reload();
  await expect(everyone).not.toBeChecked();
  await page.locator('.nav-dashboards').click();
  const name = `Delivery ${Date.now()}`;
  await page.getByLabel('Dashboard name', { exact: true }).fill(name);
  await page.getByLabel('Description', { exact: true }).fill('Team delivery and priorities');
  await accessible(page);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboards\/\d+\?add=1$/);
  const dashboardURL = page.url().split('?')[0];
  const id = dashboardURL.split('/').pop()!;
  await page.getByRole('button', { name: 'Add Pie chart', exact: true }).click();
  await page.getByLabel('JQL query').fill('project = ZZ');
  await page.getByLabel('Group charts by').selectOption('status');
  await page.getByLabel('Maximum list results').fill('1');
  await accessible(page);
  await page.getByRole('button', { name: 'Save gadget query' }).click();
  await expect(page.locator('.gadget-counts')).toBeVisible();
  await expect(page.locator('.gadget-pie')).toBeVisible();
  await page.getByRole('link', { name: 'Add gadget', exact: true }).click();
  await page.getByRole('button', { name: 'Add Filter results', exact: true }).click();
  await page.getByLabel('JQL query').fill('project = ZZ');
  await page.getByLabel('Maximum list results').fill('5');
  await page.getByRole('button', { name: 'Save gadget query' }).click();
  await expect(page.locator('.gadget-issues li').first()).toBeVisible();
  await page.getByRole('link', { name: 'Configure Filter results', exact: true }).click();
  await page.getByLabel('Gadget title', { exact: true }).fill('Delivery queue');
  await page.getByLabel('Column (from 0)').selectOption('1');
  await page.getByLabel('Accent colour').selectOption('purple');
  await page.getByRole('button', { name: 'Save appearance and position' }).click();
  await expect(page.locator('.dashboard-column').nth(1)).toContainText('Delivery queue');
  await page.getByRole('link', { name: 'Edit dashboard', exact: true }).click();
  await page.getByLabel('Layout', { exact: true }).selectOption('AB');
  await page.getByLabel('Automatic refresh', { exact: true }).selectOption('60000');
  await page.getByRole('button', { name: 'Save layout' }).click();
  await expect(page.locator('#dashboard-grid')).toHaveClass(/layout-AB/);
  const viewerContext = await browser.newContext();
  const viewer = await viewerContext.newPage();
  try {
    await login(viewer, 'ana@zzira.dev', 'ana12345');
    expect((await viewer.request.get(`/rest/api/3/dashboard/${id}`)).status()).toBe(404);
    await page.getByRole('link', { name: 'Edit dashboard', exact: true }).click();
    await page.getByRole('group', { name: 'Who can view?', exact: true }).getByLabel('Everyone in this workspace', { exact: true }).check();
    await page.getByRole('button', { name: 'Save dashboard', exact: true }).click();
    await viewer.goto(dashboardURL);
    await expect(viewer.getByRole('heading', { level: 1 })).toHaveText(name);
    await expect(viewer.getByRole('link', { name: 'Edit dashboard', exact: true })).toHaveCount(0);
    await expect(viewer.locator('.dashboard-gadget')).toHaveCount(2);
    await viewer.getByRole('button', { name: 'Add favourite', exact: true }).click();
    await expect(viewer.getByRole('button', { name: 'Remove favourite', exact: true })).toBeVisible();
    await accessible(viewer);
    await viewer.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(viewer.getByRole('status').filter({ hasText: 'Updated just now.' })).toBeVisible();
    await page.getByRole('link', { name: 'Edit dashboard', exact: true }).click();
    await page.getByRole('group', { name: 'Who can view?', exact: true }).getByLabel('Everyone in this workspace', { exact: true }).uncheck();
    await page.getByRole('button', { name: 'Save dashboard', exact: true }).click();
    await viewer.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(viewer.locator('#dashboard-refresh-status')).toContainText('no longer available');
    await expect(viewer.locator('.dashboard-gadget')).toHaveCount(0);
  } finally { await viewerContext.close(); }
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Copy dashboard' }).click();
  await page.getByLabel('Copy name', { exact: true }).fill(`${name} copy`);
  await page.getByRole('button', { name: 'Create copy', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(`${name} copy`);
  await expect(page.locator('.dashboard-gadget')).toHaveCount(2);
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Delete dashboard' }).click();
  await page.getByRole('button', { name: 'Delete dashboard permanently' }).click();
  await expect(page).toHaveURL('/dashboards');
  await page.goto(dashboardURL);
  // Online-only dashboards must never be retained in the service-worker page cache.
  const cached = await page.evaluate(async () => {
    const paths: string[] = [];
    for (const key of await caches.keys()) for (const request of await (await caches.open(key)).keys()) paths.push(new URL(request.url).pathname);
    return paths.filter(path => path === '/dashboards' || path.startsWith('/dashboards/'));
  });
  expect(cached).toEqual([]);
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Delete dashboard' }).click();
  await page.getByRole('button', { name: 'Delete dashboard permanently' }).click();
  expect((await page.request.get(`/rest/api/3/dashboard/${id}`)).status()).toBe(404);
});

test('a team shows dashboards as a wallboard and a slide show', async ({ page }) => {
  await login(page);
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const dashboard = async (name: string, gadgets: { title: string; color: string }[]) => {
    const created = await page.request.post('/rest/api/3/dashboard', { headers: auth, data: { name, sharePermissions: [], editPermissions: [] } });
    expect(created.status()).toBe(200);
    const id = (await created.json()).id as string;
    for (const [row, gadget] of gadgets.entries()) {
      const added = await page.request.post(`/rest/api/3/dashboard/${id}/gadget`, { headers: auth, data: { moduleKey: 'com.zzira:assigned-to-me', title: gadget.title, color: gadget.color, position: { column: 0, row } } });
      expect(added.status()).toBe(200);
    }
    return id;
  };
  const first = await dashboard(`Wall A ${stamp}`, [{ title: 'Blue first', color: 'blue' }, { title: 'Blue second', color: 'blue' }, { title: 'Red alone', color: 'red' }]);
  const second = await dashboard(`Wall B ${stamp}`, [{ title: 'Second board gadget', color: 'green' }]);
  await page.clock.install();
  await page.goto(`/dashboards/${first}`);
  await page.getByRole('link', { name: 'View as wallboard', exact: true }).click();
  await expect(page).toHaveURL(`/dashboards/${first}/wallboard`);
  // The wallboard hides navigation; gadgets sharing a colour take turns.
  await expect(page.locator('.nav-dashboards')).toHaveCount(0);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(`Wall A ${stamp}`);
  await expect(page.getByRole('heading', { name: 'Blue first', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Blue second', exact: true })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Red alone', exact: true })).toBeVisible();
  await expect(page.locator('.wallboard-position').first()).toHaveText('1 of 2');
  await accessible(page);
  await page.clock.runFor(30_000);
  await expect(page.getByRole('heading', { name: 'Blue first', exact: true })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Blue second', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Red alone', exact: true })).toBeVisible();
  const pause = page.getByRole('button', { name: 'Pause rotation', exact: true });
  await pause.click();
  await expect(page.getByRole('button', { name: 'Resume rotation', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#wallboard-status')).toHaveText('Rotation paused.');
  await page.clock.runFor(60_000);
  await expect(page.getByRole('heading', { name: 'Blue second', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Resume rotation', exact: true }).click();
  await page.clock.runFor(30_000);
  await expect(page.getByRole('heading', { name: 'Blue first', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.getByRole('link', { name: 'Exit wallboard', exact: true }).click();
  await expect(page).toHaveURL(`/dashboards/${first}`);

  // Someone who can edit a dashboard configures the site's slide show from it.
  await page.locator('.dashboard-footer summary').filter({ hasText: 'Configure wallboard slide show' }).click();
  const chosen = page.getByRole('group', { name: 'Dashboards in the slide show', exact: true });
  await page.getByLabel('Seconds per dashboard', { exact: true }).fill('10');
  await page.getByRole('button', { name: 'Save slide show', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('choose between 1 and 50 dashboards');
  await expect(page.getByLabel('Seconds per dashboard', { exact: true })).toHaveValue('10');
  await chosen.getByLabel(`Wall A ${stamp}`, { exact: true }).check();
  await chosen.getByLabel(`Wall B ${stamp}`, { exact: true }).check();
  await accessible(page);
  await page.getByRole('button', { name: 'Save slide show', exact: true }).click();
  await expect(page).toHaveURL(`/dashboards/${first}`);
  await page.getByRole('link', { name: 'View wallboard slide show', exact: true }).click();
  await expect(page).toHaveURL('/dashboards/slideshow');
  await expect(page.getByRole('heading', { level: 2, name: `Wall A ${stamp}`, exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { level: 2, name: `Wall B ${stamp}`, exact: true })).toBeHidden();
  await accessible(page);
  await page.clock.runFor(10_000);
  await expect(page.getByRole('heading', { level: 2, name: `Wall A ${stamp}`, exact: true })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Second board gadget', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Exit slide show', exact: true }).click();
  await expect(page).toHaveURL('/dashboards');
  for (const id of [first, second]) expect((await page.request.delete(`/rest/api/3/dashboard/${id}`, { headers: auth })).status()).toBe(204);
});

test('chart gadgets count work across two groupings, by weight and for each viewer', async ({ page }) => {
  await login(page);
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const [wide, narrow] = [`chart-wide-${stamp}`, `chart-narrow-${stamp}`];
  const keys: string[] = [];
  for (const labels of [[wide, narrow], [wide]]) {
    const created = await page.request.post('/rest/api/3/issue', { headers: auth, data: { fields: { project: { key: 'ZZ' }, summary: `Chart ${labels.join(' ')}`, issuetype: { name: 'Task' }, labels } } });
    expect(created.status()).toBe(201);
    keys.push((await created.json()).key);
  }
  const me = await (await page.request.get('/rest/api/3/myself', { headers: auth })).json();
  const board = await page.request.post('/rest/api/3/dashboard', { headers: auth, data: { name: `Charts ${stamp}`, sharePermissions: [], editPermissions: [] } });
  expect(board.status()).toBe(200);
  const id = (await board.json()).id as string;
  const jql = `labels in (${wide}, ${narrow})`;
  const gadget = async (moduleKey: string, column: number, config: object) => {
    const added = await page.request.post(`/rest/api/3/dashboard/${id}/gadget`, { headers: auth, data: { moduleKey, position: { column, row: 0 } } });
    expect(added.status()).toBe(200);
    const gid = (await added.json()).id;
    expect((await page.request.put(`/rest/api/3/dashboard/${id}/items/${gid}/properties/zzira.config`, { headers: auth, data: config })).status()).toBe(201);
  };
  await gadget('com.zzira:two-dimensional-statistics', 0, { jql, groupBy: 'labels', yGroupBy: 'issuetype', limit: 5 });
  await gadget('com.zzira:heat-map', 1, { jql, groupBy: 'labels', limit: 5 });
  await gadget('com.zzira:watched-issues', 1, { jql, limit: 5 });
  // Only the first work item is watched by the viewer.
  for (const key of keys) await page.request.delete(`/rest/api/3/issue/${key}/watchers?accountId=${me.accountId}`, { headers: auth });
  expect((await page.request.post(`/rest/api/3/issue/${keys[0]}/watchers`, { headers: auth, data: JSON.stringify(me.accountId) })).status()).toBe(204);

  await page.goto(`/dashboards/${id}`);
  const grid = page.getByRole('region', { name: 'Two dimensional filter statistics table', exact: true });
  await expect(grid.getByRole('columnheader')).toHaveText(['Work type', wide, narrow, 'Total']);
  await expect(grid.getByRole('row', { name: /^Task/ })).toHaveText(/Task\s*2\s*1\s*3/);
  const heat = page.getByRole('list', { name: 'Work items by Labels', exact: true });
  await expect(heat.getByRole('listitem')).toHaveText([new RegExp(`${wide}\\s*2 · 100%`), new RegExp(`${narrow}\\s*1 · 50%`)]);
  await expect(heat.locator('.heat-5')).toContainText(wide);
  const watched = page.getByRole('region', { name: 'Watched work items', exact: true });
  await expect(watched.locator('.gadget-issues li')).toHaveCount(1);
  await expect(watched).toContainText(keys[0]);
  await accessible(page);

  await page.getByRole('link', { name: 'Configure Two dimensional filter statistics', exact: true }).click();
  await expect(page.getByLabel('Columns', { exact: true })).toHaveValue('labels');
  await page.getByLabel('Rows', { exact: true }).selectOption('priority');
  await page.getByRole('button', { name: 'Save gadget query' }).click();
  await expect(grid.getByRole('columnheader').first()).toHaveText('Priority');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await page.request.delete(`/rest/api/3/dashboard/${id}`, { headers: auth })).status()).toBe(204);
});

test('activity stream, calendar and road map gadgets follow recent work, due dates and releases', async ({ page }) => {
  await login(page);
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const today = new Date().toISOString().slice(0, 10);
  const project = await (await page.request.get('/rest/api/3/project/ZZ', { headers: auth })).json();
  const version = await page.request.post('/rest/api/3/version', { headers: auth, data: { name: `Stream release ${stamp}`, projectId: Number(project.id), releaseDate: today } });
  expect(version.status(), await version.text()).toBe(201);
  const versionID = (await version.json()).id;
  const created = await page.request.post('/rest/api/3/issue', { headers: auth, data: { fields: { project: { key: 'ZZ' }, summary: `Stream work ${stamp}`, issuetype: { name: 'Task' }, duedate: today, fixVersions: [{ id: versionID }] } } });
  expect(created.status(), await created.text()).toBe(201);
  const key = (await created.json()).key as string;
  const comment = await page.request.post(`/rest/api/3/issue/${key}/comment`, { headers: auth, data: { body: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: `Stream note ${stamp}` }] }] } } });
  expect(comment.status(), await comment.text()).toBe(201);

  const board = await page.request.post('/rest/api/3/dashboard', { headers: auth, data: { name: `Streams ${stamp}`, sharePermissions: [], editPermissions: [] } });
  const id = (await board.json()).id as string;
  const gadget = async (moduleKey: string, column: number, config: object) => {
    const added = await page.request.post(`/rest/api/3/dashboard/${id}/gadget`, { headers: auth, data: { moduleKey, position: { column, row: 0 } } });
    expect(added.status()).toBe(200);
    const gid = (await added.json()).id;
    expect((await page.request.put(`/rest/api/3/dashboard/${id}/items/${gid}/properties/zzira.config`, { headers: auth, data: config })).status()).toBe(201);
  };
  await gadget('com.zzira:activity-stream', 0, { jql: `key = ${key}`, limit: 5 });
  await gadget('com.zzira:calendar', 1, { jql: `key = ${key}` });
  await gadget('com.zzira:road-map', 1, { projectKey: 'ZZ', days: 30 });

  await page.goto(`/dashboards/${id}`);
  const activity = page.getByRole('region', { name: 'Activity stream', exact: true });
  await expect(activity.getByRole('listitem').first()).toContainText(`commented on ${key}`);
  await expect(activity.getByRole('listitem').first()).toContainText(`Stream note ${stamp}`);
  await expect(activity).toContainText(`created ${key}`);
  const calendar = page.getByRole('region', { name: /^Calendar, / });
  const todayCell = calendar.locator('td[aria-current="date"]');
  await expect(todayCell.getByRole('link', { name: new RegExp(`^${key}`) })).toBeVisible();
  await expect(todayCell).toContainText(`ZZ Stream release ${stamp} release`);
  const roadMap = page.locator('.dashboard-gadget').filter({ has: page.getByRole('heading', { name: 'Road map', exact: true }) });
  const release = roadMap.getByRole('listitem').filter({ hasText: `Stream release ${stamp}` });
  await expect(release).toContainText('0 of 1 work items done');
  await expect(release.getByRole('progressbar', { name: `Stream release ${stamp} work done`, exact: true })).toBeVisible();
  await accessible(page);

  await page.getByRole('link', { name: 'Configure Activity stream', exact: true }).click();
  await expect(page.getByLabel('Maximum events', { exact: true })).toHaveValue('5');
  await expect(page.getByLabel('Group charts by', { exact: true })).toHaveCount(0);
  await page.goto(`/dashboards/${id}`);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await page.request.delete(`/rest/api/3/dashboard/${id}`, { headers: auth })).status()).toBe(204);
});
