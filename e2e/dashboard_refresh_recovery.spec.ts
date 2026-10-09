import { expect, test } from '@playwright/test';
import { apiAuthHeader } from './auth';

let issueKey: string;
const auth = () => ({ Authorization: apiAuthHeader() });

test.beforeAll(async ({ request }) => {
  const created = await request.post('/rest/api/3/issue', { headers: auth(), data: {
    fields: { project: { key: 'ZZ' }, summary: `Refresh fixture ${Date.now()}`, issuetype: { name: 'Task' } },
  } });
  expect(created.status()).toBe(201);
  issueKey = (await created.json()).key;
});

test.afterAll(async ({ request }) => {
  if (issueKey) expect((await request.delete(`/rest/api/3/issue/${issueKey}`, { headers: auth() })).status()).toBe(204);
});

test.beforeEach(async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const created = await page.request.post('/rest/api/3/dashboard', { headers: auth(), data: {
    name: `Refresh recovery ${Date.now()}`, sharePermissions: [], editPermissions: [],
  } });
  expect(created.status()).toBe(200);
  const id = (await created.json()).id;
  const gadget = await page.request.post(`/rest/api/3/dashboard/${id}/gadget`, { headers: auth(), data: {
    moduleKey: 'com.zzira:bubble-chart', position: { column: 0, row: 0 },
  } });
  expect(gadget.status()).toBe(200);
  const gid = (await gadget.json()).id;
  expect((await page.request.put(`/rest/api/3/dashboard/${id}/items/${gid}/properties/zzira.config`, {
    headers: auth(), data: { jql: `key = ${issueKey}`, limit: 5 },
  })).status()).toBe(201);
  await page.goto(`/dashboards/${id}?edit=1`);
  await page.getByLabel('Automatic refresh', { exact: true }).selectOption('60000');
  await page.getByRole('button', { name: 'Save layout', exact: true }).click();
  await page.clock.install();
  await page.goto(`/dashboards/${id}`);
  await expect(page.locator('#dashboard-grid')).toHaveAttribute('data-refresh', '60000');
});

test.afterEach(async ({ page }) => {
  const id = new URL(page.url()).pathname.split('/')[2];
  if (id) expect((await page.request.delete(`/rest/api/3/dashboard/${id}`, { headers: auth() })).status()).toBe(204);
});

for (const fault of ['timeout', 'server error', 'HTML', 'redirect']) {
  test(`manual refresh recovers from ${fault} and retries with the previous results intact`, async ({ page }) => {
    const url = `${page.url()}/content`;
    const grid = page.locator('#dashboard-grid');
    const before = await grid.innerHTML();
    await page.route(url, route => fault === 'timeout' ? undefined : route.fulfill(
      fault === 'redirect' ? { status: 303, headers: { location: '/login' } }
      : { status: fault === 'server error' ? 503 : 200, contentType: 'text/html', body: '<html>Unavailable</html>' }));
    const pending = page.waitForRequest(url);
    await page.getByRole('button', { name: 'Refresh', exact: true }).click();
    await pending;
    if (fault === 'timeout') {
      await expect(grid).toHaveAttribute('aria-busy', 'true');
      await expect(page.locator('#dashboard-refresh-status')).toHaveText('Refreshing dashboard…');
      await page.clock.fastForward(15001);
    }
    await expect(page.locator('#dashboard-refresh-status')).toContainText('Showing the last loaded results');
    await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toBeEnabled();
    await expect(grid).not.toHaveAttribute('aria-busy', 'true');
    expect(await grid.innerHTML()).toBe(before);
    await page.unroute(url);
    await page.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(page.locator('#dashboard-refresh-status')).toHaveText('Updated just now.');
    await expect(grid).toContainText(issueKey);
  });
}

for (const interaction of ['focus', 'expanded table']) {
  test(`automatic refresh preserves ${interaction} started while a request is pending`, async ({ page }) => {
    const url = `${page.url()}/content`;
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    await page.route(url, async route => {
      const response = await route.fetch();
      await held;
      await route.fulfill({ response });
    });
    const grid = await page.locator('#dashboard-grid').elementHandle();
    const pending = page.waitForRequest(url);
    await page.clock.fastForward(60001);
    await pending;
    await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toBeDisabled();
    const link = page.getByRole('link', { name: 'Configure Bubble chart', exact: true });
    if (interaction === 'focus') await link.focus();
    else {
      await page.getByText('View work items', { exact: true }).click();
      await page.locator('#main-content').focus();
    }
    release();
    await expect(page.locator('#dashboard-refresh-status')).toContainText('Automatic refresh paused');
    expect(await grid!.evaluate(node => node.isConnected)).toBe(true);
    if (interaction === 'focus') await expect(link).toBeFocused();
    else await expect(page.locator('#dashboard-grid details')).toHaveAttribute('open', '');
    // Once the interaction ends, the next scheduled refresh can apply.
    await page.locator('#main-content').focus();
    await page.locator('#dashboard-grid details').evaluateAll(nodes => nodes.forEach(node => node.removeAttribute('open')));
    await page.unroute(url);
    await page.clock.fastForward(60001);
    await expect(page.locator('#dashboard-refresh-status')).toHaveText('Updated just now.');
    expect(await grid!.evaluate(node => node.isConnected)).toBe(false);
  });
}

test('revoked access clears results even when interaction starts during refresh', async ({ page }) => {
  const url = `${page.url()}/content`;
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route(url, async route => { await held; await route.fulfill({ status: 403 }); });
  const pending = page.waitForRequest(url);
  await page.clock.fastForward(60001);
  await pending;
  await page.getByRole('link', { name: 'Configure Bubble chart', exact: true }).focus();
  release();
  await expect(page.locator('#dashboard-refresh-status')).toContainText('no longer available');
  await expect(page.locator('.dashboard-gadget')).toHaveCount(0);
  await expect(page.locator('#dashboard-grid')).toHaveAttribute('data-refresh', '0');
  await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toBeEnabled();
  await expect(page.locator('#dashboard-grid')).not.toHaveAttribute('aria-busy', 'true');
  let requests = 0;
  page.on('request', request => { if (request.url() === url) requests++; });
  await page.clock.fastForward(120001);
  expect(requests).toBe(0);
});


test('automatic refresh waits for an expanded table, while manual refresh remains available', async ({ page }) => {
  const url = `${page.url()}/content`;
  let requests = 0;
  page.on('request', request => { if (request.url() === url) requests++; });
  await page.getByText('View work items', { exact: true }).click();
  await page.locator('#main-content').focus();
  await page.clock.fastForward(60001);
  await expect(page.locator('#dashboard-refresh-status')).toContainText('Automatic refresh paused');
  expect(requests).toBe(0);
  await expect(page.locator('#dashboard-grid details')).toHaveAttribute('open', '');
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.locator('#dashboard-refresh-status')).toHaveText('Updated just now.');
  expect(requests).toBe(1);
});

test('automatic refresh retries on the next interval after a timeout', async ({ page }) => {
  const url = `${page.url()}/content`;
  await page.route(url, () => {});
  const pending = page.waitForRequest(url);
  await page.clock.fastForward(60001);
  await pending;
  await page.clock.fastForward(15001);
  await expect(page.locator('#dashboard-refresh-status')).toContainText('Refresh took too long');
  await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toBeEnabled();
  await expect(page.locator('#dashboard-grid')).toContainText(issueKey);
  await page.unroute(url);
  await page.clock.fastForward(60001);
  await expect(page.locator('#dashboard-refresh-status')).toHaveText('Updated just now.');
});
