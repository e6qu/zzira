import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('user chooses Atlassian sign-in and admin can inspect provider status', async ({ page }) => {
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: 'Log in to your workspace' })).toBeVisible();
  const atlassian = page.getByRole('link', { name: 'Continue with Atlassian' });
  await expect(atlassian).toHaveAttribute('href', '/auth/atlassian');
  await expect(page.getByLabel('Email')).toBeVisible();
  await expect(page.getByLabel('Password')).toBeVisible();

  await page.addScriptTag({ content: axe.source });
  expect((await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations))).toEqual([]);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

  await page.setViewportSize({ width: 1280, height: 800 });

  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  await page.getByRole('link', { name: 'Administration', exact: true }).click();
  const providerRow = page.getByRole('row').filter({ hasText: 'Atlassian' });
  await expect(providerRow).toContainText('OAuth 2.0 (3LO)');
  await expect(providerRow).toContainText('https://auth.atlassian.com');
  await expect(providerRow).toContainText('Enabled');
});
