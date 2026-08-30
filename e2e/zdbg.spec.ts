import { test, expect } from '@playwright/test';
const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };
test('banner dump', async ({ page }) => {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
  await page.waitForTimeout(20000);
  console.log('LOG:', JSON.stringify(await page.evaluate(() => (window as any).__bannerLog ?? [])));
  console.log('WORKERS:', page.workers().length);
});
