import { expect, test, Page } from '@playwright/test';

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page).toHaveURL('/');
}

test('revoked access purges the local replica, private cache, and offline queue', async ({ browser }) => {
  const adminContext = await browser.newContext();
  const admin = await adminContext.newPage();
  const memberContext = await browser.newContext();
  const member = await memberContext.newPage();
  try {
    await login(admin, 'demo@zzira.dev', 'demo1234');
    await admin.goto('/admin');
    let memberRow = admin.getByRole('row').filter({ hasText: 'ana@zzira.dev' });
    if (await memberRow.getByRole('button', { name: 'Restore' }).count()) {
      await memberRow.getByRole('button', { name: 'Restore' }).click();
      await expect(admin).toHaveURL(/saved=User\+restored/);
    }

    await login(member, 'ana@zzira.dev', 'ana12345');
    await member.goto('/issues/ZZ');
    await member.locator('.issue-list .key-cell a').first().click();
    await expect.poll(async () => (await member.locator('#sync-banner').textContent()) ?? '', { timeout: 20_000 }).toContain('synced');
    await expect.poll(() => member.evaluate(() => sessionStorage.getItem('zzira-replica-id'))).not.toBeNull();

    await memberContext.setOffline(true);
    await member.getByRole('button', { name: 'Edit', exact: true }).click();
    await member.locator('input[name=summary]').fill(`must never replay ${Date.now()}`);
    await member.locator('.modal button[type=submit]').click();
    await expect(member.locator('#sync-banner')).toContainText('queued');

    await admin.goto('/admin');
    memberRow = admin.getByRole('row').filter({ hasText: 'ana@zzira.dev' });
    await memberRow.getByRole('button', { name: 'Suspend' }).click();
    await expect(admin).toHaveURL(/saved=User\+suspended/);

    await memberContext.setOffline(false);
    await expect(member).toHaveURL('/signed-out', { timeout: 20_000 });
    expect(await member.evaluate(() => sessionStorage.getItem('zzira-replica-id'))).toBeNull();
    await expect.poll(async () => member.evaluate(async () => {
      const names = (await caches.keys()).filter((name) => name.startsWith('zzira-pages-'));
      const entries = await Promise.all(names.map(async (name) => (await caches.open(name)).keys()));
      return entries.reduce((total, current) => total + current.length, 0);
    })).toBe(0);
  } finally {
    await admin.goto('/admin');
    const memberRow = admin.getByRole('row').filter({ hasText: 'ana@zzira.dev' });
    if (await memberRow.getByRole('button', { name: 'Restore' }).count()) {
      await memberRow.getByRole('button', { name: 'Restore' }).click();
    }
    await memberContext.close();
    await adminContext.close();
  }
});
