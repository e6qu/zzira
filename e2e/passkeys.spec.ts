import { expect, test, Page, BrowserContext } from '@playwright/test';
import axe from 'axe-core';

// A security key answers a sign-in instead of a code. The key here is
// Chrome's own virtual authenticator, driven through CDP, so the ceremony is
// the browser's rather than a stand-in for it.

async function submitLogin(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

// virtualKey gives a context a security key that signs what this site asks
// it to, and answers with its id.
async function virtualKey(page: Page): Promise<string> {
  const session = await page.context().newCDPSession(page);
  await session.send('WebAuthn.enable');
  const { authenticatorId } = await session.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  return authenticatorId;
}

test('a person registers a security key and signs in with it', async ({ page, browser }) => {
  const marker = Date.now();
  const email = `passkey-${marker}@zzira.dev`;
  const name = `Passkey Person ${marker}`;
  const password = 'first-password';

  // An account with a password of its own.
  await submitLogin(page, 'demo@zzira.dev', 'demo1234');
  await page.goto('/admin');
  const invitation = page.locator('form.admin-user-invite');
  await invitation.getByLabel('Display name').fill(name);
  await invitation.getByLabel('Email').fill(email);
  await invitation.getByRole('button', { name: 'Invite user' }).click();
  const row = page.getByRole('row').filter({ hasText: email });
  await row.getByRole('button', { name: 'Sign-in link' }).click();
  const link = (await page.locator('.admin-sign-in-link code').textContent()) ?? '';

  const own: BrowserContext = await browser.newContext();
  const theirs = await own.newPage();
  await virtualKey(theirs);
  await theirs.goto(link);
  await theirs.fill('#password-link-new', password);
  await theirs.fill('#password-link-confirm', password);
  await theirs.getByRole('button', { name: 'Set password' }).click();
  await submitLogin(theirs, email, password);
  await expect(theirs).not.toHaveURL(/\/login/);

  // The key is registered from the profile, and named.
  await theirs.goto('/profile');
  const keys = theirs.locator('#security-keys');
  await expect(keys).toContainText('No security key is registered');
  await accessible(theirs);
  await keys.getByLabel('Name this key').fill('Work laptop');
  await keys.getByRole('button', { name: 'Register a security key' }).click();
  await expect(theirs.locator('#security-keys')).toContainText('Work laptop');
  await expect(theirs.locator('#security-keys')).toContainText('checks who is using it');
  await accessible(theirs);

  // A password alone no longer signs them in: the sign-in waits for the key.
  await theirs.context().clearCookies();
  await submitLogin(theirs, email, password);
  await expect(theirs).toHaveURL(/\/login\/verify/);
  await expect(theirs.getByRole('button', { name: 'Use a security key' })).toBeVisible();
  await accessible(theirs);
  await theirs.getByRole('button', { name: 'Use a security key' }).click();
  await expect(theirs).toHaveURL(/\/$/);

  // A browser with no key of its own gets as far as being asked for one, and
  // no further: an answer that is not the key's is refused by the site.
  const stranger = await browser.newContext();
  const elsewhere = await stranger.newPage();
  await submitLogin(elsewhere, email, password);
  await expect(elsewhere).toHaveURL(/\/login\/verify/);
  await expect(elsewhere.getByRole('button', { name: 'Use a security key' })).toBeVisible();
  const forged = await elsewhere.request.post('/login/verify/passkey', {
    data: {
      rawId: Buffer.from('not-a-registered-key').toString('base64url'),
      response: {
        clientDataJSON: Buffer.from(JSON.stringify({ type: 'webauthn.get', challenge: 'AAAA', origin: 'http://localhost:8080' })).toString('base64url'),
        authenticatorData: Buffer.from('nonsense').toString('base64url'),
        signature: Buffer.from('nonsense').toString('base64url'),
      },
    },
  });
  expect(forged.status()).toBeGreaterThanOrEqual(400);
  await expect(elsewhere).toHaveURL(/\/login\/verify/);
  await stranger.close();

  // Removing the key leaves the account as it was before.
  await theirs.goto('/profile');
  await theirs.locator('#security-keys').getByRole('button', { name: 'Remove Work laptop' }).click();
  await expect(theirs.locator('#security-keys')).toContainText('No security key is registered');
  await theirs.context().clearCookies();
  await submitLogin(theirs, email, password);
  await expect(theirs).not.toHaveURL(/\/login\/verify/);
  await own.close();
});
