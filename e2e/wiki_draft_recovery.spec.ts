import { expect, test, Page } from '@playwright/test';
import { apiAuthHeader } from './auth';

async function login(page: Page, email: string, password: string) {
  if (await page.locator('.user-menu').count()) {
    await page.locator('.user-menu > summary').click();
    await page.getByRole('button', { name: 'Log out', exact: true }).click();
    await page.waitForURL(/\/(login|signed-out)/);
  }
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
}

test('early unsynced typing survives reload and stays with its writer', async ({ page, request }) => {
  test.setTimeout(60_000);
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36).toUpperCase();
  const createdSpace = await request.post('/wiki/api/v2/spaces', { headers, data: {
    key: `DR${stamp}`, name: `Draft recovery ${stamp}`, createPrivateSpace: false,
  } });
  expect(createdSpace.status()).toBe(201);
  const space = await createdSpace.json();
  const createdPage = await request.post('/wiki/api/v2/pages', { headers, data: {
    spaceId: space.id, title: `Early draft ${stamp}`, status: 'current', body: { representation: 'storage', value: '<p>Plan.</p>' },
  } });
  expect(createdPage.status()).toBe(200);
  const document = await createdPage.json();
  const editURL = `/wiki/spaces/${space.id}/pages/${document.id}/edit`;
  const liveURL = `**/wiki/spaces/${space.id}/pages/${document.id}/live`;
  await login(page, 'demo@zzira.dev', 'demo1234');
  await page.evaluate(({ key, text }) => localStorage.setItem(key, JSON.stringify({
    session: '', revision: -1, synced: '<p>Plan.</p>', text, at: Date.now(), closed: true,
  })), { key: `zzira-live:/wiki/spaces/${space.id}/pages/${document.id}/live#legacy`, text: '<p>Unattributed legacy draft.</p>' });
  // The first exchange never succeeds, so recovery must use the rendered body.
  await page.route(liveURL, route => route.abort());
  await page.goto(editURL);
  const editor = page.getByRole('textbox', { name: 'Page content' });
  const status = page.locator('[data-wiki-live-sync]');
  await expect(status).toContainText('kept on this device');
  await expect(editor).toHaveText('Plan.');
  const demoDraft = 'Plan. Unsent Demo note.';
  await editor.fill(demoDraft);
  await expect.poll(() => page.evaluate(() => Object.values(localStorage).some(value => value.includes('Unsent Demo note.')))).toBe(true);
  // Reload before the input debounce could perform a successful exchange.
  await page.reload();
  await expect(editor).toHaveText(demoDraft);

  // Switching accounts in this browser must not recover Demo's private typing.
  await login(page, 'ana@zzira.dev', 'ana12345');
  await page.goto(editURL);
  await expect(editor).toHaveText('Plan.');
  await editor.fill('Plan. Unsent Ana note.');
  await login(page, 'demo@zzira.dev', 'demo1234');
  await page.goto(editURL);
  await expect(editor).toHaveText(demoDraft);
  await expect(editor).not.toContainText('Unsent Ana note.');
  await page.unroute(liveURL);
  await expect(status).toContainText('Live editing is on');
  await expect(editor).toHaveText(demoDraft);
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('article', { name: 'Page content' })).toContainText(demoDraft);
});
