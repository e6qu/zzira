import { expect, test } from '@playwright/test';
import { apiAuthHeader } from './auth';
import axe from 'axe-core';

let key: string;
test.beforeAll(async ({ request }) => {
  const created = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() }, data: {
      fields: { project: { key: 'ZZ' }, summary: `Editor loading ${Date.now()}`, issuetype: { name: 'Task' } },
    },
  });
  expect(created.status()).toBe(201);
  key = (await created.json()).key;
});
test.afterAll(async ({ request }) => {
  if (key) expect((await request.delete(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() } })).status()).toBe(204);
});
test.beforeEach(async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
  await page.clock.install();
  await page.goto(`/browse/${key}`);
});

for (const fault of ['timeout', 'stalled body', 'server error', 'HTML', 'wrong issue', 'redirect']) {
  test(`loading an editor recovers from ${fault} and can retry`, async ({ page }) => {
    const url = `**/issues/${key}/edit`;
    await page.route(url, route => fault === 'timeout' ? undefined : route.fulfill(
      fault === 'redirect' ? { status: 303, headers: { location: '/login' } }
      : { status: fault === 'server error' ? 503 : 200, contentType: 'text/html',
        body: fault === 'wrong issue'
          ? '<div class="modal" role="dialog"><form hx-post="/issues/ZZ-999999/edit"></form></div>'
          : '<html><h1>Sign in</h1></html>' }));
    if (fault === 'stalled body') await page.evaluate(() => {
      const original = window.fetch.bind(window);
      (window as any).restoreEditorFetch = () => { window.fetch = original; };
      window.fetch = async (input, options) => String(input).endsWith('/edit')
        ? { ok: true, text: () => new Promise<never>((_, reject) => {
            options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
          }) } as Response
        : original(input, options);
    });
    const pending = fault === 'stalled body' ? Promise.resolve() : page.waitForRequest(url);
    await page.getByRole('button', { name: 'Edit', exact: true }).click();
    await pending;
    if (fault === 'timeout' || fault === 'stalled body') {
      const loading = page.getByRole('dialog', { name: 'Loading issue editor', exact: true });
      await expect(loading.getByRole('status')).toHaveText('Loading the issue fields…');
      await expect(loading.getByRole('button', { name: 'Close dialog', exact: true })).toBeFocused();
      await expect(page.locator('.app-shell')).toHaveAttribute('inert', '');
      await page.clock.fastForward(15001);
    }
    const failed = page.getByRole('dialog', { name: 'Could not open issue editor', exact: true });
    await expect(failed.getByRole('alert')).toContainText(/retry|Try again/);
    await expect(failed.getByRole('alert')).toBeFocused();
    await expect(page).toHaveURL(`/browse/${key}`);
    if (fault === 'timeout') {
      await page.setViewportSize({ width: 320, height: 740 });
      await page.addScriptTag({ content: axe.source });
      expect(await page.evaluate(async () => (await (window as any).axe.run(document, {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
      })).violations)).toEqual([]);
    }
    await page.unroute(url);
    if (fault === 'stalled body') await page.evaluate(() => (window as any).restoreEditorFetch());
    await failed.getByRole('button', { name: 'Retry', exact: true }).click();
    const editor = page.getByRole('dialog', { name: 'Edit issue', exact: true });
    await expect(editor.getByLabel('Summary', { exact: true })).toBeFocused();
    await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeFocused();
    await expect(page.locator('.app-shell')).not.toHaveAttribute('inert');
  });
}

for (const action of ['Cancel', 'Close dialog', 'Escape']) {
  test(`${action} cancels a loading editor and ignores a late response`, async ({ page }) => {
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    await page.route(`**/issues/${key}/edit`, async route => {
      const response = await route.fetch();
      await held;
      await route.fulfill({ response }).catch(() => {});
    });
    const pending = page.waitForRequest(`**/issues/${key}/edit`);
    await page.getByRole('button', { name: 'Edit', exact: true }).click();
    await pending;
    const loading = page.getByRole('dialog', { name: 'Loading issue editor', exact: true });
    await expect(loading).toBeVisible();
    if (action === 'Escape') await page.keyboard.press('Escape');
    else await loading.getByRole('button', { name: action, exact: true }).click();
    release();
    await page.unrouteAll({ behavior: 'wait' });
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeFocused();
    await expect(page.locator('.app-shell')).not.toHaveAttribute('inert');
  });
}
