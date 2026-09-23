import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

// A site administrator points the site at a SAML identity provider, and
// somebody signs in through it. The provider is fake-saml-idp.mjs, which
// signs its assertions the way a real one does.

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };
const IDP = 'http://127.0.0.1:8200';

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a site administrator sets up SAML and somebody signs in through it', async ({ page, request, browser }) => {
  const certificate = await (await request.get(`${IDP}/certificate`)).text();
  expect(certificate.length).toBeGreaterThan(100);
  const entityID = (await (await request.get(`${IDP}/metadata`)).text()).trim();

  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await page.goto('/admin');
  const saml = page.locator('#admin-saml');
  await expect(saml.getByRole('heading', { name: 'SAML single sign-on' })).toBeVisible();
  await saml.locator('.admin-saml-registration summary').click();
  const key = `idp${Date.now().toString(36).slice(-6)}`;
  await saml.getByLabel('Provider key').fill(key);
  await saml.getByLabel('Display name').fill('Company SAML');
  await saml.getByLabel('Identity provider entity ID').fill(entityID);
  await saml.getByLabel('Sign-in address').fill(`${IDP}/sso`);
  await saml.getByLabel('Signing certificate').fill(certificate);
  await saml.getByRole('button', { name: 'Add SAML provider' }).click();
  await expect(page.getByRole('status')).toContainText('Company SAML saved');
  const configured = page.locator('#admin-saml');
  await expect(configured).toContainText(entityID);
  await expect(configured).toContainText('Enabled');
  await accessible(page);

  // The addresses an administrator gives the provider, and the metadata this
  // site publishes.
  await configured.locator('summary').filter({ hasText: 'Addresses for Company SAML' }).click();
  await expect(configured).toContainText(`/saml/${key}/acs`);
  const metadata = await request.get(`/saml/${key}/metadata`);
  expect(metadata.status()).toBe(200);
  const metadataBody = await metadata.text();
  expect(metadataBody).toContain('AssertionConsumerService');
  expect(metadataBody).toContain(`/saml/${key}/acs`);

  // Somebody who has never signed in here goes through the provider and
  // arrives signed in.
  const visitor = await browser.newPage();
  await visitor.goto('/login');
  await expect(visitor.getByRole('link', { name: 'Continue with Company SAML' })).toBeVisible();
  await visitor.getByRole('link', { name: 'Continue with Company SAML' }).click();
  await expect(visitor).toHaveURL(/\/$/);
  await visitor.goto('/profile');
  await expect(visitor.locator('main')).toContainText('saml.person@zzira.dev');
  await visitor.close();

  // An answer nobody asked for is refused: the site reads only the answers to
  // sign-ins it began.
  const unsolicited = await request.post(`/saml/${key}/acs`, {
    form: { SAMLResponse: Buffer.from('<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"></samlp:Response>').toString('base64') },
  });
  expect(unsolicited.status()).toBe(400);

  // Turning the provider off takes it off the sign-in page.
  await page.goto('/admin');
  await page.locator('#admin-saml').getByRole('button', { name: 'Disable Company SAML' }).click();
  await expect(page.locator('#admin-saml')).toContainText('Disabled');
  const signedOut = await browser.newPage();
  await signedOut.goto('/login');
  await expect(signedOut.getByRole('link', { name: 'Continue with Company SAML' })).toHaveCount(0);
  await signedOut.close();

  await page.goto('/admin');
  await page.locator('#admin-saml').getByRole('button', { name: 'Delete Company SAML' }).click();
  await expect(page.locator('#admin-saml')).not.toContainText('Company SAML');
});
