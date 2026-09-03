import { expect, test, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('sign-out clears authenticated page caches and rotates the local replica', async ({ page }) => {
  await login(page);
  await page.goto('/issues/ZZ');
  await page.locator('.issue-list .key-cell a').first().click();
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null)).toBe(true);
  await expect.poll(() => page.evaluate(async () =>
    (await (await caches.open('zzira-pages-v7')).keys()).length,
  )).toBeGreaterThan(0);
  await expect.poll(() => page.evaluate(() =>
    sessionStorage.getItem('zzira-replica-id'),
  )).not.toBeNull();

  await page.locator('.user-menu summary').click();
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL('/signed-out');
  expect(await page.evaluate(() => sessionStorage.getItem('zzira-replica-id'))).toBeNull();
  await expect.poll(() => page.evaluate(async () =>
    (await (await caches.open('zzira-pages-v7')).keys()).length,
  )).toBe(0);
});
