import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

test('an invited person sets their own password and later replaces it', async ({ page, browser }) => {
  const marker = Date.now();
  const email = `newcomer-${marker}@zzira.dev`;
  const name = `Newcomer ${marker}`;

  // An invitation provisions the account, and nobody -- not even the
  // administrator -- knows a password for it.
  await login(page, 'demo@zzira.dev', 'demo1234');
  await page.goto('/admin');
  const invitation = page.locator('form.admin-user-invite');
  await invitation.getByLabel('Display name').fill(name);
  await invitation.getByLabel('Email').fill(email);
  await invitation.getByRole('button', { name: 'Invite user' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=/);
  const row = page.getByRole('row').filter({ hasText: email });
  await expect(row).toContainText('Active');

  // The sign-in link is what makes that account usable. It is shown once, on
  // the answer to the request that made it.
  await row.getByRole('button', { name: 'Sign-in link' }).click();
  const notice = page.locator('.admin-sign-in-link');
  await expect(notice).toContainText(name);
  const link = (await notice.locator('code').textContent()) ?? '';
  expect(link).toContain('/password/set?token=');
  await accessible(page);

  // The person opens it and sets a password. The rule is on the page before
  // they type one, and a password under it is refused.
  const newcomer = await browser.newContext();
  const theirPage = await newcomer.newPage();
  await theirPage.goto(link);
  await expect(theirPage.getByRole('heading', { name: 'Set your password' })).toBeVisible();
  await expect(theirPage.locator('body')).toContainText('at least 8 characters');
  await accessible(theirPage);
  await theirPage.fill('#password-link-new', 'short');
  await theirPage.fill('#password-link-confirm', 'short');
  await theirPage.evaluate(() => {
    document.querySelectorAll('input[minlength]').forEach((input) => input.removeAttribute('minlength'));
  });
  await theirPage.getByRole('button', { name: 'Set password' }).click();
  await expect(theirPage.getByRole('alert')).toContainText('at least 8 characters');

  await theirPage.fill('#password-link-new', 'first-password');
  await theirPage.fill('#password-link-confirm', 'first-password');
  await theirPage.getByRole('button', { name: 'Set password' }).click();
  await expect(theirPage).toHaveURL('/login?saved=password');
  await expect(theirPage.getByRole('status')).toContainText('Your password is set');

  // A link is spent when it is used: opening it again offers no form.
  await theirPage.goto(link);
  await expect(theirPage.getByRole('alert')).toContainText('has been used or has expired');
  await expect(theirPage.locator('#password-link-new')).toHaveCount(0);

  // The password they set is the one that signs them in.
  await login(theirPage, email, 'first-password');
  await expect(theirPage).not.toHaveURL(/\/login/);

  // They replace it from their own profile, which ends the session they left
  // open elsewhere and leaves this one alone.
  const otherDevice = await browser.newContext();
  const otherPage = await otherDevice.newPage();
  await login(otherPage, email, 'first-password');
  await expect(otherPage).not.toHaveURL(/\/login/);

  await theirPage.goto('/profile');
  await expect(theirPage.getByRole('heading', { name: 'Password', level: 2 })).toBeVisible();
  await theirPage.fill('#profile-current-password', 'wrong-password');
  await theirPage.fill('#profile-new-password', 'second-password');
  await theirPage.fill('#profile-confirm-password', 'second-password');
  await theirPage.getByRole('button', { name: 'Change password' }).click();
  await expect(theirPage.getByRole('alert')).toContainText('not your current password');

  await theirPage.fill('#profile-current-password', 'first-password');
  await theirPage.fill('#profile-new-password', 'second-password');
  await theirPage.fill('#profile-confirm-password', 'second-password');
  await theirPage.getByRole('button', { name: 'Change password' }).click();
  await expect(theirPage.getByRole('status')).toContainText('Password changed');
  await accessible(theirPage);

  // The session it was changed from carries on.
  await theirPage.goto('/profile');
  await expect(theirPage.getByRole('heading', { name: 'Password', level: 2 })).toBeVisible();
  // The one left open elsewhere does not.
  await otherPage.goto('/profile');
  await expect(otherPage).toHaveURL(/\/login/);
  // And the password it was changed from no longer signs in.
  await login(otherPage, email, 'first-password');
  await expect(otherPage).toHaveURL(/\/login/);
  await login(otherPage, email, 'second-password');
  await expect(otherPage).not.toHaveURL(/\/login/);

  await newcomer.close();
  await otherDevice.close();
});
