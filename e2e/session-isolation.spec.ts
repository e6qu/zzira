import { expect, test, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

async function privatePageCacheEntries(page: Page): Promise<number> {
  return page.evaluate(async () => {
    const names = (await caches.keys()).filter((name) => name.startsWith('zzira-pages-'));
    const entries = await Promise.all(names.map(async (name) => (await caches.open(name)).keys()));
    return entries.reduce((total, cacheEntries) => total + cacheEntries.length, 0);
  });
}

test('sign-out clears authenticated page caches and rotates the local replica', async ({ page }) => {
  await login(page);
  await page.goto('/issues/ZZ');
  await page.locator('.issue-list .key-cell a').first().click();
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null)).toBe(true);
  await expect.poll(() => privatePageCacheEntries(page)).toBeGreaterThan(0);
  await expect.poll(() => page.evaluate(() =>
    sessionStorage.getItem('zzira-replica-id'),
  )).not.toBeNull();

  await page.locator('.user-menu summary').click();
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL('/signed-out');
  expect(await page.evaluate(() => sessionStorage.getItem('zzira-replica-id'))).toBeNull();
  await expect.poll(() => privatePageCacheEntries(page)).toBe(0);
});

test('the service worker does not intercept a cross-origin redirect into the Shauth logout bridge', async ({ page, baseURL }) => {
  await login(page);
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null)).toBe(true);

  const seenPaths: string[] = [];
  page.on('request', (request) => {
    if (request.isNavigationRequest()) {
      try {
        seenPaths.push(new URL(request.url()).pathname);
      } catch {
        // ignore
      }
    }
  });

  // Mirrors how the browser actually arrives at the bridge in production:
  // Shauth's Hydra instance (a different origin) 303s the top-level
  // navigation here after finishing RP-initiated logout, so this must be a
  // genuine cross-origin landing -- not a same-origin page.goto() -- to
  // trigger the process-swap conditions the worker-interception bug depends
  // on. fake-hydra.mjs on :8100 stands in for that redirecting IdP hop.
  const bridgeURL = new URL('/auth/shauth/logout/complete', baseURL!).toString();
  await page.goto(`http://127.0.0.1:8100/simulated-logout?to=${encodeURIComponent(bridgeURL)}`);
  await expect(page).toHaveURL('/signed-out');

  // A service worker that intercepts this navigation and answers with
  // fetch(req) resolves the redirect internally, so only the final URL is
  // ever visible to the page's own navigation events. Seeing the bridge
  // path itself proves the browser (not the worker) followed the redirect.
  expect(seenPaths).toContain('/auth/shauth/logout/complete');
});

test('rapid navigation serializes OPFS access handles for one replica', async ({ page }) => {
  await login(page);
  await page.goto('/issues/ZZ');
  const issueHref = await page.locator('.issue-list .key-cell a').first().getAttribute('href');
  expect(issueHref).toMatch(/^\/browse\/ZZ-/);

  for (let pass = 0; pass < 3; pass += 1) {
    await page.goto(issueHref!);
    await expect.poll(() => page.locator('[data-sync-label]').textContent(), { timeout: 30_000 }).toBe('Synced');
    await expect(page.locator('[data-sync-label]')).not.toHaveText('Sync needs attention');
    expect(await page.evaluate(() => (window as any).__bannerLog || [])).not.toEqual(
      expect.arrayContaining([expect.stringContaining('createSyncAccessHandle')]),
    );

    await page.goto('/board/brd_default');
    await expect.poll(() => page.locator('[data-sync-label]').textContent(), { timeout: 30_000 }).toBe('Synced');
    await expect(page.locator('[data-sync-label]')).not.toHaveText('Sync needs attention');
  }
});
