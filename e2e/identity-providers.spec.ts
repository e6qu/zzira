import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('user chooses Atlassian sign-in and admin controls provider availability', async ({ page, browser }) => {
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
  await page.goto('/profile');
  await expect(page.getByRole('heading', { name: 'Connected sign-in accounts' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Connect Atlassian' })).toHaveAttribute('href', '/auth/atlassian/link');
  await page.getByRole('link', { name: 'Administration', exact: true }).click();
  const providerRow = page.getByRole('row').filter({ hasText: 'Atlassian' });
  await expect(providerRow).toContainText('OAuth 2.0 (3LO)');
  await expect(providerRow).toContainText('https://auth.atlassian.com');
  await expect(providerRow).toContainText('Enabled');
  await providerRow.getByRole('button', { name: 'Disable Atlassian' }).click();
  await expect(page).toHaveURL(/saved=Atlassian\+disabled/);
  await expect(page.getByRole('row').filter({ hasText: 'Atlassian' })).toContainText('Disabled');

  const signedOutContext = await browser.newContext();
  const signedOutPage = await signedOutContext.newPage();
  await signedOutPage.goto('/login');
  await expect(signedOutPage.getByRole('link', { name: 'Continue with Atlassian' })).toHaveCount(0);
  await signedOutContext.close();

  await page.getByRole('row').filter({ hasText: 'Atlassian' }).getByRole('button', { name: 'Enable Atlassian' }).click();
  await expect(page).toHaveURL(/saved=Atlassian\+enabled/);
  const restoredContext = await browser.newContext();
  const restoredPage = await restoredContext.newPage();
  await restoredPage.goto('/login');
  await expect(restoredPage.getByRole('link', { name: 'Continue with Atlassian' })).toBeVisible();
  await restoredContext.close();
});

test('admin registers, rotates, and removes an encrypted OIDC provider', async ({ page, browser }) => {
  const suffix = Date.now().toString(36);
  const key = `test-${suffix}`;
  const name = `Test SSO ${suffix}`;
  const firstSecret = `first-${suffix}`;
  const secondSecret = `second-${suffix}`;

  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
  await page.goto('/admin');

  await page.getByText('Add OpenID Connect provider').click();
  const registration = page.locator('.admin-provider-registration');
  await registration.getByLabel('Provider key').fill(key);
  await registration.getByLabel('Display name').fill(name);
  await registration.getByLabel('Issuer URL').fill('http://127.0.0.1:8100');
  await registration.getByLabel('Client ID').fill(`client-${suffix}`);
  await registration.getByLabel('Client secret').fill(firstSecret);
  await registration.getByRole('button', { name: 'Add provider' }).click();
  await expect(page.getByRole('status')).toContainText(`${name} registered`);

  let row = page.getByRole('row').filter({ hasText: name });
  await expect(row).toContainText('Encrypted database');
  await expect(row).toContainText('Enabled');
  await expect(page.locator('body')).not.toContainText(firstSecret);

  const loginContext = await browser.newContext();
  const loginPage = await loginContext.newPage();
  await loginPage.goto('/login');
  await expect(loginPage.getByRole('link', { name: `Continue with ${name}` })).toBeVisible();
  await loginContext.close();

  await row.getByText('Rotate secret').click();
  await row.getByLabel('New client secret').fill(secondSecret);
  await row.getByRole('button', { name: 'Save new secret' }).click();
  await expect(page).toHaveURL(/credentials\+rotated/);
  await expect(page.locator('body')).not.toContainText(secondSecret);

  row = page.getByRole('row').filter({ hasText: name });
  await row.getByRole('button', { name: `Delete ${name}` }).click();
  await expect(page).toHaveURL(/deleted/);
  await expect(page.getByRole('row').filter({ hasText: name })).toHaveCount(0);

  const removedContext = await browser.newContext();
  const removedPage = await removedContext.newPage();
  await removedPage.goto('/login');
  await expect(removedPage.getByRole('link', { name: `Continue with ${name}` })).toHaveCount(0);
  await removedContext.close();
});
