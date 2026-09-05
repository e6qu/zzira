import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email = 'demo@zzira.dev', password = 'demo1234') {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
}

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

test('site admin manages a directory group and its audited membership', async ({ page, browser }) => {
  await login(page);
  await page.getByRole('link', { name: 'Administration', exact: true }).click();
  await expect(page).toHaveURL('/admin');
  await expect(page.getByRole('heading', { name: 'ZZIRA', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Products', level: 2 })).toBeVisible();
  await expect(page.getByRole('row', { name: /Jira Software/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /Jira Service Management/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /Confluence/ })).toBeVisible();

  const groupName = `delivery-managers-${Date.now()}`;
  await page.getByLabel('Group name').fill(groupName);
  await page.getByLabel('Description').fill('Coordinates plans, releases, and delivery evidence.');
  await page.getByRole('button', { name: 'Create group' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Group\+created$/);
  const group = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await expect(group).toContainText('0 members');

  const ana = group.getByRole('listitem').filter({ hasText: 'Ana Soursop' });
  await ana.getByRole('button', { name: 'Add' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Member\+added$/);
  const updatedGroup = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await expect(updatedGroup).toContainText('1 member');
  await expect(page.locator('.admin-audit')).toContainText('group.member.added');
  await accessible(page);

  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);

  const memberContext = await browser.newContext();
  const member = await memberContext.newPage();
  try {
    await login(member, 'ana@zzira.dev', 'ana12345');
    await expect(member.getByRole('link', { name: 'Administration', exact: true })).toHaveCount(0);
    const denied = await member.goto('/admin');
    expect(denied?.status()).toBe(403);
  } finally {
    await memberContext.close();
  }

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/admin');
  const finalGroup = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await finalGroup.getByRole('listitem').filter({ hasText: 'Ana Soursop' }).getByRole('button', { name: 'Remove' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Member\+removed$/);
  await expect(page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) })).toContainText('0 members');
  await expect(page.locator('.admin-audit')).toContainText('group.member.removed');
});
