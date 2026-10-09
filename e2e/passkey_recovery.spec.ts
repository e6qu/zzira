import { expect, test } from '@playwright/test';

// Fault injection isolates browser recovery; passkeys.spec.ts covers real
// registration, server verification and sign-in with a virtual authenticator.
test.beforeEach(async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.goto('/profile');
  await page.locator('[data-passkey-label]').fill('Travel key');
});

for (const fault of ['HTML', 'missing acknowledgement', 'redirect', 'denied']) {
  test(`registration recovers from ${fault} without losing the name`, async ({ page }) => {
    await page.evaluate(() => {
      Object.defineProperty(navigator.credentials, 'create', { configurable: true, value: async () => ({
        rawId: new ArrayBuffer(1), response: { clientDataJSON: new ArrayBuffer(1), attestationObject: new ArrayBuffer(1) },
      }) });
    });
    await page.route('**/profile/passkeys', route => route.fulfill(fault === 'redirect'
      ? { status: 303, headers: { location: '/login' } }
      : fault === 'denied' ? { status: 401, contentType: 'text/plain', body: 'sign in first' }
      : { status: fault === 'HTML' ? 200 : 201, contentType: fault === 'HTML' ? 'text/html' : 'application/json', body: fault === 'HTML' ? '<html>Sign in</html>' : '{}' }));
    const profileURL = page.url();
    await page.locator('[data-passkey-register]').click();
    await expect(page.locator('[data-passkey-status]')).toHaveClass('form-error');
    await expect(page.locator('[data-passkey-register]')).toBeEnabled();
    await expect(page.locator('[data-passkey-label]')).toBeEnabled();
    await expect(page.locator('[data-passkey-label]')).toHaveValue('Travel key');
    await expect(page).toHaveURL(profileURL);
    await expect(page.locator('#security-keys')).toContainText('No security key is registered');
  });
}

for (const result of ['canceled', 'empty']) {
  test(`registration explains ${result === 'empty' ? 'an' : 'a'} ${result} key prompt and allows retry`, async ({ page }) => {
    await page.evaluate(result => {
      Object.defineProperty(navigator.credentials, 'create', { configurable: true, value: async () => {
        if (result === 'empty') return null;
        throw new DOMException('Technical browser message', 'NotAllowedError');
      } });
    }, result);
    await page.locator('[data-passkey-register]').click();
    await expect(page.locator('[data-passkey-status]')).toContainText('canceled or timed out');
    await expect(page.locator('[data-passkey-register]')).toBeEnabled();
    await expect(page.locator('[data-passkey-label]')).toHaveValue('Travel key');
    await page.route('**/profile/passkeys/options', route => route.fulfill({ status: 503, contentType: 'text/plain', body: 'Try again later' }));
    await page.locator('[data-passkey-register]').click();
    await expect(page.locator('[data-passkey-status]')).toHaveText('Try again later');
    await expect(page.locator('[data-passkey-register]')).toBeEnabled();
  });
}

for (const phase of ['options', 'completion']) {
  test(`registration releases controls when ${phase} times out`, async ({ page }) => {
    await page.clock.install();
    if (phase === 'completion') await page.evaluate(() => {
      Object.defineProperty(navigator.credentials, 'create', { configurable: true, value: async () => ({
        rawId: new ArrayBuffer(1), response: { clientDataJSON: new ArrayBuffer(1), attestationObject: new ArrayBuffer(1) },
      }) });
    });
    const endpoint = phase === 'options' ? '**/profile/passkeys/options' : '**/profile/passkeys';
    await page.route(endpoint, () => {});
    const pending = page.waitForRequest(phase === 'options' ? '**/profile/passkeys/options' : '**/profile/passkeys');
    await page.locator('[data-passkey-register]').click();
    await pending;
    await expect(page.locator('[data-passkey-register]')).toBeDisabled();
    await expect(page.locator('[data-passkey-label]')).toBeDisabled();
    await page.clock.fastForward(15001);
    await expect(page.locator('[data-passkey-status]')).toContainText('took too long');
    await expect(page.locator('[data-passkey-register]')).toBeEnabled();
    await expect(page.locator('[data-passkey-label]')).toHaveValue('Travel key');
  });
}
