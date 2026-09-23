import { expect, test, Page, BrowserContext } from '@playwright/test';
import { createHmac } from 'node:crypto';
import axe from 'axe-core';

// The spec stands in for the authenticator app: the same secret, the same
// thirty-second step, the same six digits.
function authenticatorCode(secret: string, at = Date.now()): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const character of secret.replace(/=+$/, '').toUpperCase()) {
    bits += alphabet.indexOf(character).toString(2).padStart(5, '0');
  }
  const bytes = Buffer.from((bits.match(/.{8}/g) ?? []).map((group) => parseInt(group, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const mac = createHmac('sha1', bytes).update(counter).digest();
  const offset = mac[mac.length - 1] & 0x0f;
  const value = mac.readUInt32BE(offset) & 0x7fffffff;
  return String(value % 1_000_000).padStart(6, '0');
}

async function submitLogin(page: Page, email: string, password: string) {
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

test('a person protects their account with a second step and signs in with it', async ({ page, browser }) => {
  const marker = Date.now();
  const email = `two-step-${marker}@zzira.dev`;
  const name = `Two Step ${marker}`;
  const password = 'first-password';

  // A person with a password of their own, made the way a new account is.
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
  await theirs.goto(link);
  await theirs.fill('#password-link-new', password);
  await theirs.fill('#password-link-confirm', password);
  await theirs.getByRole('button', { name: 'Set password' }).click();
  await submitLogin(theirs, email, password);
  await expect(theirs).not.toHaveURL(/\/login/);

  // Turning it on hands over a key, and asks for a code to prove the app has
  // it before anything changes.
  await theirs.goto('/profile');
  const section = theirs.locator('#two-step');
  await expect(section).toContainText('Off');
  await section.getByRole('button', { name: 'Set up two-step verification' }).click();
  const secret = ((await theirs.locator('#two-step dd code').first().textContent()) ?? '').trim();
  expect(secret.length).toBeGreaterThan(15);
  // The key is also offered as a code to scan, rather than only to type.
  const symbol = theirs.locator('#two-step .two-step-qr');
  await expect(symbol).toHaveAttribute('aria-label', /QR code/);
  expect(((await symbol.locator('path').getAttribute('d')) ?? '').length).toBeGreaterThan(200);
  await accessible(theirs);
  await theirs.fill('#two-step-confirm-code', '000000');
  await theirs.getByRole('button', { name: 'Confirm' }).click();
  await expect(theirs.getByRole('alert')).toContainText('That code is not right');

  // A wrong code leaves the account as it was, so the key is still the one to
  // confirm with.
  await theirs.goto('/profile');
  await expect(theirs.locator('#two-step')).toContainText('Off');
  await theirs.locator('#two-step').getByRole('button', { name: 'Start again' }).click();
  const confirmed = ((await theirs.locator('#two-step dd code').first().textContent()) ?? '').trim();
  await theirs.fill('#two-step-confirm-code', authenticatorCode(confirmed));
  await theirs.getByRole('button', { name: 'Confirm' }).click();
  const codes = await theirs.locator('.profile-recovery-codes code').allTextContents();
  expect(codes).toHaveLength(10);
  await accessible(theirs);
  await theirs.goto('/profile');
  await expect(theirs.locator('#two-step')).toContainText('On');
  await expect(theirs.locator('#two-step')).toContainText('10 recovery codes left');

  // From now on the password is half the answer.
  const second = await browser.newContext();
  const elsewhere = await second.newPage();
  await submitLogin(elsewhere, email, password);
  await expect(elsewhere).toHaveURL('/login/verify');
  await expect(elsewhere.getByRole('heading', { name: 'Enter your code' })).toBeVisible();
  await accessible(elsewhere);
  await elsewhere.fill('#two-step-code', '000000');
  await elsewhere.getByRole('button', { name: 'Verify' }).click();
  await expect(elsewhere.getByRole('alert')).toContainText('That code is not right');
  await elsewhere.fill('#two-step-code', authenticatorCode(confirmed));
  await elsewhere.getByRole('button', { name: 'Verify' }).click();
  await expect(elsewhere).not.toHaveURL(/\/login/);

  // A recovery code stands in for the app, once.
  const third = await browser.newContext();
  const recovering = await third.newPage();
  await submitLogin(recovering, email, password);
  await expect(recovering).toHaveURL('/login/verify');
  await recovering.fill('#two-step-code', codes[0]);
  await recovering.getByRole('button', { name: 'Verify' }).click();
  await expect(recovering).not.toHaveURL(/\/login/);
  await recovering.goto('/profile');
  await expect(recovering.locator('#two-step')).toContainText('9 recovery codes left');

  const fourth = await browser.newContext();
  const again = await fourth.newPage();
  await submitLogin(again, email, password);
  await again.fill('#two-step-code', codes[0]);
  await again.getByRole('button', { name: 'Verify' }).click();
  await expect(again.getByRole('alert')).toContainText('That code is not right');

  // Turning it off asks for the password, and then the password is the whole
  // answer again.
  await theirs.goto('/profile');
  await theirs.fill('#two-step-disable-password', 'not-the-password');
  await theirs.locator('#two-step').getByRole('button', { name: 'Turn off two-step verification' }).click();
  await expect(theirs.getByRole('alert')).toContainText('not your current password');
  await theirs.fill('#two-step-disable-password', password);
  await theirs.locator('#two-step').getByRole('button', { name: 'Turn off two-step verification' }).click();
  await expect(theirs.getByRole('status')).toContainText('Two-step verification turned off');

  const fifth = await browser.newContext();
  const plain = await fifth.newPage();
  await submitLogin(plain, email, password);
  await expect(plain).not.toHaveURL(/\/login/);

  // An authentication policy can ask for it instead of leaving it to each
  // person. Someone it covers is not refused -- they are sent to set it up.
  const policyName = `Two step required ${marker}`;
  await page.goto('/admin');
  const create = page.locator('#admin-authentication-policy-create');
  await create.getByLabel('Policy name').fill(policyName);
  await create.getByLabel('Two-step verification required').check();
  await create.getByLabel('Enable immediately').check();
  await create.getByRole('button', { name: 'Create authentication policy' }).click();
  let policy = page.locator('.admin-authentication-policy').filter({ hasText: policyName });
  await policy.getByLabel('Add a person').selectOption({ label: name });
  await policy.getByRole('button', { name: 'Add member' }).click();

  const sixth = await browser.newContext();
  const required = await sixth.newPage();
  await submitLogin(required, email, password);
  await expect(required).toHaveURL('/login/enrol');
  await expect(required.getByRole('heading', { name: 'Set up two-step verification' })).toBeVisible();
  await expect(required.locator('.two-step-qr')).toHaveAttribute('aria-label', /QR code/);
  const enrolKey = ((await required.locator('.two-step-key dd code').first().textContent()) ?? '').trim();
  await accessible(required);
  // The key survives a reload, so an app that already holds it still works.
  await required.reload();
  expect(((await required.locator('.two-step-key dd code').first().textContent()) ?? '').trim()).toBe(enrolKey);
  await required.fill('#two-step-enrol-code', '000000');
  await required.getByRole('button', { name: 'Confirm and sign in' }).click();
  await expect(required.getByRole('alert')).toContainText('That code is not right');
  await required.fill('#two-step-enrol-code', authenticatorCode(enrolKey));
  await required.getByRole('button', { name: 'Confirm and sign in' }).click();
  await expect(required.getByRole('heading', { name: 'Keep your recovery codes' })).toBeVisible();
  expect(await required.locator('.two-step-recovery-codes code').allTextContents()).toHaveLength(10);
  await accessible(required);
  await required.getByRole('link', { name: 'Continue' }).click();
  await expect(required).not.toHaveURL(/\/login/);

  // An administrator resets a second step when the app and the codes are both
  // gone, and it is in the audit log.
  await page.goto('/admin');
  const person = page.getByRole('row').filter({ hasText: email });
  await person.getByRole('button', { name: 'Reset two-step' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Two-step\+verification\+reset$/);
  await expect(page.locator('.admin-audit')).toContainText('user.two-step.reset');

  // The policy still asks for one, so the next sign-in sets it up again.
  const seventh = await browser.newContext();
  const afterReset = await seventh.newPage();
  await submitLogin(afterReset, email, password);
  await expect(afterReset).toHaveURL('/login/enrol');

  // Deleting the policy leaves the account on its password alone.
  await page.goto('/admin');
  policy = page.locator('.admin-authentication-policy').filter({ hasText: policyName });
  await policy.getByRole('button', { name: 'Delete policy' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+deleted$/);
  const eighth = await browser.newContext();
  const unpoliced = await eighth.newPage();
  await submitLogin(unpoliced, email, password);
  await expect(unpoliced).not.toHaveURL(/\/login/);

  for (const context of [own, second, third, fourth, fifth, sixth, seventh, eighth]) {
    await context.close();
  }
});
