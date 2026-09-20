import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// Someone makes a token for a script they are writing, uses it against the
// API as themselves, and revokes it when the script is done. The secret is
// shown once, at creation.
test('a person creates an API token, calls the API with it, and revokes it', async ({ page, request }) => {
  await login(page);
  await page.goto('/profile');
  const tokens = page.getByRole('region', { name: 'API tokens' });
  await tokens.locator('summary').filter({ hasText: 'Create API token' }).click();
  const label = `Release script ${Date.now()}`;
  await tokens.getByLabel('What is it for?').fill(label);
  await accessible(page);
  await tokens.getByRole('button', { name: 'Create API token', exact: true }).click();

  const secret = await page.locator('[data-new-api-token]').innerText();
  expect(secret).toMatch(/^zzira_/);
  await expect(page.getByText('Copy this token now.')).toBeVisible();
  await expect(page.getByRole('region', { name: 'API tokens' })).toContainText(label);

  // The token signs in as its owner over HTTP Basic, which is what it is for.
  const authorization = 'Basic ' + Buffer.from(`${DEMO.email}:${secret}`).toString('base64');
  const myself = await request.get('/rest/api/3/myself', { headers: { Authorization: authorization } });
  expect(myself.status()).toBe(200);
  expect((await myself.json()).emailAddress).toBe(DEMO.email);

  // Reloading never shows the secret again: only its fingerprint was kept.
  await page.reload();
  await expect(page.locator('[data-new-api-token]')).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'API tokens' })).toContainText(label);

  // An unlabelled token is refused, with the reason on the page.
  const refused = page.getByRole('region', { name: 'API tokens' });
  await refused.locator('summary').filter({ hasText: 'Create API token' }).click();
  await refused.getByLabel('What is it for?').fill(' ');
  await refused.getByLabel('What is it for?').evaluate((node: HTMLInputElement) => node.removeAttribute('required'));
  await refused.getByRole('button', { name: 'Create API token', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('label');

  // Revoked, and the token stops working the moment it is.
  const row = page.getByRole('region', { name: 'API tokens' }).getByRole('listitem').filter({ hasText: label });
  await row.getByRole('button', { name: new RegExp(`^Revoke`) }).click();
  await expect(page.getByRole('region', { name: 'API tokens' })).not.toContainText(label);
  const after = await request.get('/rest/api/3/myself', { headers: { Authorization: authorization } });
  expect(after.status()).toBe(401);
});
