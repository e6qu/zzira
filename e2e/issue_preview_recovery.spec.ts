import { expect, test, Page } from '@playwright/test';
import { apiAuthHeader } from './auth';
import axe from 'axe-core';

let keys: string[] = [];
const headers = () => ({ Authorization: apiAuthHeader() });
test.beforeAll(async ({ request }) => {
  for (const name of ['First', 'Second']) {
    const created = await request.post('/rest/api/3/issue', { headers: headers(), data: {
      fields: { project: { key: 'ZZ' }, summary: `${name} preview recovery ${Date.now()}`, issuetype: { name: 'Task' } },
    } });
    expect(created.status()).toBe(201);
    keys.push((await created.json()).key);
  }
});
test.afterAll(async ({ request }) => {
  for (const key of keys) expect((await request.delete(`/rest/api/3/issue/${key}`, { headers: headers() })).status()).toBe(204);
});
test.beforeEach(async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
  await page.clock.install();
});

async function open(page: Page, surface: string) {
  await page.goto(surface === 'navigator'
    ? `/issues/ZZ?jql=${encodeURIComponent(`key in (${keys.join(',')}) ORDER BY key ASC`)}`
    : `/board/brd_default${surface === 'backlog' ? '/backlog' : ''}`);
}
function trigger(page: Page, surface: string, key: string) {
  return surface === 'navigator' ? page.locator(`[data-navigator-row][data-key="${key}"] [data-preview-link]`)
    : page.locator(`[data-issue-preview="/browse/${key}/preview"]`);
}

for (const surface of ['navigator', 'board', 'backlog']) {
  for (const fault of ['timeout', 'stalled body', 'server error', 'wrong issue', 'denied', 'redirect']) {
    test(`${surface} preview recovers from ${fault} and allows retry`, async ({ page }) => {
      await open(page, surface);
      const preview = page.getByRole('complementary', { name: 'Issue preview', exact: true });
      await trigger(page, surface, keys[0]).click();
      await expect(preview.locator('.issue-preview-card')).toHaveAttribute('data-preview-key', keys[0]);
      const url = `**/browse/${keys[1]}/preview`;
      await page.route(url, route => fault === 'timeout' ? undefined : route.fulfill(
        fault === 'redirect' ? { status: 303, headers: { location: '/login' } }
        : { status: fault === 'denied' ? 403 : fault === 'server error' ? 503 : 200,
          contentType: 'text/html', body: `<article class="issue-preview-card" data-preview-key="${keys[0]}">Wrong issue</article>` }));
      if (fault === 'stalled body') await page.evaluate(key => {
        const original = window.fetch.bind(window);
        (window as any).restorePreviewFetch = () => { window.fetch = original; };
        window.fetch = async (input, options) => String(input) === `/browse/${key}/preview`
          ? { ok: true, status: 200, text: () => new Promise<never>((_, reject) => {
              options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
            }) } as Response
          : original(input, options);
      }, keys[1]);
      const pending = fault === 'stalled body' ? Promise.resolve() : page.waitForRequest(url);
      await trigger(page, surface, keys[1]).click();
      await pending;
      if (fault === 'timeout' || fault === 'stalled body') {
        await expect(preview).toHaveAttribute('aria-busy', 'true');
        await page.clock.fastForward(15001);
      }
      await expect(preview.getByRole('alert')).toContainText('Select the work item again to retry');
      await expect(preview).toHaveAttribute('aria-busy', 'false');
      if (fault === 'denied') await expect(preview.locator('.issue-preview-card')).toHaveCount(0);
      else await expect(preview.locator('.issue-preview-card')).toHaveAttribute('data-preview-key', keys[0]);
      if (fault === 'server error') {
        await page.setViewportSize({ width: 320, height: 740 });
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.addScriptTag({ content: axe.source });
        expect(await page.evaluate(async () => (await (window as any).axe.run(document, {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
        })).violations)).toEqual([]);
      }
      await page.unroute(url);
      if (fault === 'stalled body') await page.evaluate(() => (window as any).restorePreviewFetch());
      await trigger(page, surface, keys[1]).click();
      await expect(preview.locator('.issue-preview-card')).toHaveAttribute('data-preview-key', keys[1]);
      await expect(preview.getByRole('alert')).toHaveCount(0);
    });
  }
}

test('navigator keyboard selection cancels a pending click preview', async ({ page }) => {
  await open(page, 'navigator');
  const preview = page.getByRole('complementary', { name: 'Issue preview', exact: true });
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route(`**/browse/${keys[0]}/preview`, async route => {
    const response = await route.fetch();
    await held;
    await route.fulfill({ response }).catch(() => {}); // The newer selection aborts this request.
  });
  const pending = page.waitForRequest(`**/browse/${keys[0]}/preview`);
  await trigger(page, 'navigator', keys[0]).click();
  await pending;
  await page.locator('[data-navigator]').focus();
  await page.keyboard.press('ArrowDown');
  await expect(preview.locator('.issue-preview-card')).toHaveAttribute('data-preview-key', keys[1]);
  release();
  await page.unrouteAll({ behavior: 'wait' });
  await expect(preview.locator('.issue-preview-card')).toHaveAttribute('data-preview-key', keys[1]);
  await expect(preview).toHaveAttribute('aria-busy', 'false');
  await expect(trigger(page, 'navigator', keys[1])).toHaveAttribute('aria-current', 'true');
});
