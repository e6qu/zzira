import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test.use({ actionTimeout: 15_000, colorScheme: 'light' });

test('mobile account menu switches theme and retains the same desktop control', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  await page.locator('.user-menu > summary').click();
  const toggle = page.locator('[data-theme-toggle]');
  await expect(toggle).toBeVisible();
  await expect(page.locator('.user-menu [data-theme-toggle]')).toHaveCount(1);
  for (let i = 0; i < 4; i++) await page.keyboard.press('Tab');
  await expect(toggle).toBeFocused();
  await page.keyboard.press('Space');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(toggle).toHaveAttribute('aria-pressed', 'true');
  await expect(toggle).toHaveAttribute('aria-label', 'Switch to light mode');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.addScriptTag({ content: axe.source });
  expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.locator('.user-menu > summary').click();
  await expect(toggle).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 720 });
  await expect(page.locator('.global-header > [data-theme-toggle]')).toBeVisible();
  await expect(toggle).toHaveCount(1);
  await page.keyboard.press('Escape');
  await toggle.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.setViewportSize({ width: 320, height: 740 });
  await page.locator('.user-menu > summary').click();
  await expect(toggle).toBeVisible();
  await expect(toggle).toHaveAttribute('aria-label', 'Switch to dark mode');
});
