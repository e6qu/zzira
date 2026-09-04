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
