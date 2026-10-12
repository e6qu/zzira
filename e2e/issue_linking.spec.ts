import { apiAuthHeader } from './auth';
import { expect, test, Page, Browser } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test("an administrator runs the site's link types from the browser", async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  const name = `Supersedes ${stamp}`;
  await page.goto('/settings/issue-linking');

  // The site's built-in relationships are listed.
  await expect(page.getByRole('heading', { name: 'Blocks', exact: true })).toBeVisible();

  const create = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create link type' }) });
  await create.getByLabel('Name', { exact: true }).fill(name);
  await create.getByLabel('Outward description', { exact: true }).fill('supersedes');
  await create.getByLabel('Inward description', { exact: true }).fill('is superseded by');
  await create.getByRole('button', { name: 'Create link type' }).click();
  await expect(page.getByRole('status')).toContainText(`${name} created.`);

  // A relationship the work item view can use: the new type is offered there.
  const headers = { Authorization: apiAuthHeader() };
  const types = await (await page.request.get('/rest/api/3/issueLinkType', { headers })).json();
  expect(types.issueLinkTypes.map((t: { name: string }) => t.name)).toContain(name);

  // One name per site, and the refusal says why.
  await create.getByLabel('Name', { exact: true }).fill('Blocks');
  await create.getByLabel('Outward description', { exact: true }).fill('blocks');
  await create.getByLabel('Inward description', { exact: true }).fill('is blocked by');
  await create.getByRole('button', { name: 'Create link type' }).click();
  await expect(page.getByRole('alert')).toContainText('name is in use');

  // Renaming it carries both directions.
  let card = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name, exact: true }) });
  const renamed = `Replaces ${stamp}`;
  await card.getByLabel('Name', { exact: true }).fill(renamed);
  await card.getByLabel('Outward description', { exact: true }).fill('replaces');
  await card.getByLabel('Inward description', { exact: true }).fill('is replaced by');
  await card.getByRole('button', { name: 'Save link type' }).click();
  await expect(page.getByRole('status')).toContainText(`${renamed} saved.`);
  card = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: renamed, exact: true }) });
  await expect(card).toContainText('replaces · is replaced by');

  await page.addScriptTag({ content: axe.source });
  expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);

  // Deleting it takes the relationship away from the site.
  await card.locator('details.metadata-delete > summary').click();
  await card.getByRole('button', { name: 'Delete link type' }).click();
  await expect(page.getByRole('status')).toContainText('no longer related');
  await expect(page.getByRole('heading', { name: renamed, exact: true })).toHaveCount(0);
  const after = await (await page.request.get('/rest/api/3/issueLinkType', { headers })).json();
  expect(after.issueLinkTypes.map((t: { name: string }) => t.name)).not.toContain(renamed);

  // Link types are site administration: a member is refused the page itself.
  await assertMemberRefused(browser);
});

async function assertMemberRefused(browser: Browser) {
  const context = await browser.newContext();
  try {
    const member = await context.newPage();
    await login(member, 'ana@zzira.dev', 'ana12345');
    expect((await member.goto('/settings/issue-linking'))?.status()).toBe(403);
  } finally {
    await context.close();
  }
}
