import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

async function expectNoWCAGViolations(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => {
    const result = await (window as any).axe.run(document, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'] },
    });
    return result.violations.map((violation: any) => ({
      id: violation.id,
      help: violation.help,
      nodes: violation.nodes.map((node: any) => node.target),
    }));
  });
  expect(violations).toEqual([]);
}

test('accessible login, navigation, dialog, and dark theme', async ({ page }) => {
  await page.goto('/login');
  await expectNoWCAGViolations(page);

  await login(page);
  await expectNoWCAGViolations(page);

  const toggle = page.locator('[data-theme-toggle]');
  await expect(toggle).toHaveAttribute('aria-label', 'Switch to dark mode');
  await toggle.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(toggle).toHaveAttribute('aria-label', 'Switch to light mode');
  await expectNoWCAGViolations(page);

  await page.click('.header-create');
  const dialog = page.locator('[role=dialog]');
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await expectNoWCAGViolations(page);

  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('.header-create')).toBeFocused();
});
