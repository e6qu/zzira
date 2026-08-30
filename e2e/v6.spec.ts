import { test, expect, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('V6: dashboard renders status counts and recent activity', async ({ page }) => {
  await login(page);
  await page.goto('/dashboard');
  const cards = await page.locator('.dash-card').count();
  expect(cards).toBeGreaterThanOrEqual(3); // To Do / In Progress / Done / My open
  await expect(page.locator('h2', { hasText: 'Recent activity' })).toBeVisible();
});
