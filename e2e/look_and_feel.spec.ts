import { expect, test, Page, Browser } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test("an administrator sets the site's look and feel", async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const title = `Northwind ${Date.now().toString(36).toUpperCase()}`;
  await page.goto('/settings/look-and-feel');

  const form = page.locator('form').filter({ has: page.getByRole('button', { name: 'Save look and feel' }) });
  await form.getByLabel('Site title', { exact: true }).fill(title);
  await form.getByLabel('Navigation background', { exact: true }).fill('#0b1b33');
  await form.getByLabel('Primary button colour', { exact: true }).fill('#0052cc');
  await form.getByLabel('Day/Month/Year format', { exact: true }).fill('dd/MMM/yy');
  await form.getByLabel('Show the site title beside the logo').check();
  await form.getByRole('button', { name: 'Save look and feel' }).click();
  await expect(page.getByRole('status')).toContainText('Look and feel saved.');

  // The title a person reads comes from what was saved.
  await page.goto('/');
  await expect(page).toHaveTitle(new RegExp(title));

  // A colour the pages cannot measure, and a date pattern they cannot read,
  // are refused rather than stored and ignored.
  await page.goto('/settings/look-and-feel');
  await form.getByLabel('Navigation background', { exact: true }).fill('notacolour');
  await form.getByRole('button', { name: 'Save look and feel' }).click();
  await expect(page.getByRole('alert')).toContainText('must be a hex colour');
  await page.goto('/settings/look-and-feel');
  await form.getByLabel('Day/Month/Year format', { exact: true }).fill('qqq');
  await form.getByRole('button', { name: 'Save look and feel' }).click();
  await expect(page.getByRole('alert')).toContainText('not a date pattern');

  // White label text has to stay readable on the primary button.
  await page.goto('/settings/look-and-feel');
  await form.getByLabel('Primary button colour', { exact: true }).fill('#ffff00');
  await form.getByRole('button', { name: 'Save look and feel' }).click();
  await expect(page.getByRole('alert')).toContainText('would not be readable');

  // What was saved survives the refusals.
  await page.goto('/settings/look-and-feel');
  await expect(form.getByLabel('Site title', { exact: true })).toHaveValue(title);
  await expect(form.getByLabel('Navigation background', { exact: true })).toHaveValue('#0b1b33');

  await page.addScriptTag({ content: axe.source });
  expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);

  // Restoring the defaults clears the site's own branding.
  await page.getByRole('button', { name: 'Restore the defaults' }).click();
  await expect(page.getByRole('status')).toContainText('restored to the defaults');
  await expect(form.getByLabel('Site title', { exact: true })).toHaveValue('');
  await page.goto('/');
  await expect(page).toHaveTitle(/ZZIRA/);

  await assertMemberRefused(browser);
});

async function assertMemberRefused(browser: Browser) {
  const context = await browser.newContext();
  try {
    const member = await context.newPage();
    await login(member, 'ana@zzira.dev', 'ana12345');
    expect((await member.goto('/settings/look-and-feel'))?.status()).toBe(403);
  } finally {
    await context.close();
  }
}
