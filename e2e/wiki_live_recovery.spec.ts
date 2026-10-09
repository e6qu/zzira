import { expect, test } from '@playwright/test';
import { apiAuthHeader } from './auth';

// The first exchange stalls either before headers or while reading JSON.
// Later exchanges use the real server to prove that typing reconnects and saves.
for (const phase of ['headers', 'body']) {
  test(`live editing reconnects after stalled ${phase} without losing typing`, async ({ page, request }) => {
    const headers = { Authorization: apiAuthHeader() };
    const stamp = Date.now().toString(36).toUpperCase();
    const createdSpace = await request.post('/wiki/api/v2/spaces', { headers, data: {
      key: `LR${stamp}`, name: `Live recovery ${stamp}`, createPrivateSpace: false,
    } });
    expect(createdSpace.status()).toBe(201);
    const space = await createdSpace.json();
    const createdPage = await request.post('/wiki/api/v2/pages', { headers, data: {
      spaceId: space.id, title: `Live recovery ${phase}`, status: 'current',
      body: { representation: 'storage', value: '<p>Plan.</p>' },
    } });
    expect(createdPage.status()).toBe(200);
    const document = await createdPage.json();
    await page.goto('/login');
    await page.getByLabel('Email').fill('demo@zzira.dev');
    await page.getByLabel('Password', { exact: true }).fill('demo1234');
    await page.getByRole('button', { name: 'Log in', exact: true }).click();
    await expect(page).toHaveURL('/');
    await page.clock.install();
    await page.addInitScript(({ phase }) => {
      const original = window.fetch.bind(window);
      let first = true;
      (window as any).liveRequests = 0;
      window.fetch = async (input, options) => {
        if (String(input).endsWith('/live')) {
          (window as any).liveRequests++;
          if (first) {
            first = false;
            const stalled = () => new Promise<never>((_, reject) => {
              options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
            });
            if (phase === 'headers') return stalled();
            return { ok: true, status: 200, json: stalled } as Response;
          }
        }
        return original(input, options);
      };
    }, { phase });
    await page.goto(`/wiki/spaces/${space.id}/pages/${document.id}/edit`);
    await expect.poll(() => page.evaluate(() => (window as any).liveRequests)).toBe(1);
    const editor = page.getByRole('textbox', { name: 'Page content' });
    const draft = 'Plan. Typed during the stalled exchange.';
    await editor.fill(draft);
    await page.clock.fastForward(14000);
    expect(await page.evaluate(() => (window as any).liveRequests)).toBe(1);
    await page.clock.fastForward(1001);
    await expect(page.locator('[data-wiki-live-sync]')).toContainText('reconnecting');
    await expect(editor).toHaveText(draft);
    await expect.poll(() => page.evaluate(() => Object.values(localStorage).some(value => value.includes('Typed during the stalled exchange.')))).toBe(true);
    await page.clock.fastForward(1001);
    await expect(page.locator('[data-wiki-live-sync]')).toContainText('Live editing is on');
    await page.clock.fastForward(1001);
    await expect.poll(() => page.evaluate(() => (window as any).liveRequests)).toBeGreaterThan(2);
    await page.getByRole('button', { name: 'Save page', exact: true }).click();
    await expect(page.getByRole('article', { name: 'Page content' })).toHaveText(draft);
  });
}
