import { expect, test, Page } from '@playwright/test';
import { apiAuthHeader } from './auth';

const headers = () => ({ Authorization: apiAuthHeader() });
test.beforeEach(async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
});

async function open(page: Page, surface: string, offline = false) {
  if (surface === 'issue comment') {
    const created = await page.request.post('/rest/api/3/issue', { headers: headers(), data: {
      fields: { project: { key: 'ZZ' }, summary: `Composed mention ${Date.now()}`, issuetype: { name: 'Task' } },
    } });
    expect(created.status()).toBe(201);
    const issue = await created.json();
    await page.goto(`/browse/${issue.key}`);
    return page.getByRole('textbox', { name: 'Add a comment', exact: true });
  }
  const stamp = Date.now().toString(36).toUpperCase();
  const createdSpace = await page.request.post('/wiki/api/v2/spaces', { headers: headers(), data: {
    key: `MC${stamp}`, name: `Composed mentions ${stamp}`, createPrivateSpace: false,
  } });
  expect(createdSpace.status()).toBe(201);
  const space = await createdSpace.json();
  const createdPage = await page.request.post('/wiki/api/v2/pages', { headers: headers(), data: {
    spaceId: space.id, title: `Composed mention ${surface}`, status: 'current',
    body: { representation: 'storage', value: surface === 'wiki source' ? '<p>Due <time datetime="2030-01-02" /></p>' : '<p>Plan.</p>' },
  } });
  expect(createdPage.status()).toBe(200);
  const document = await createdPage.json();
  if (offline) await page.route(`**/wiki/spaces/${space.id}/pages/${document.id}/live`, route => route.abort());
  await page.goto(`/wiki/spaces/${space.id}/pages/${document.id}/edit`);
  return page.getByRole('textbox', { name: 'Page content', exact: true });
}

for (const surface of ['issue comment', 'wiki rich', 'wiki source']) {
  test(`${surface} mention suggestions wait for input composition to finish`, async ({ page }) => {
    const editor = await open(page, surface);
    await editor.fill('Check @Ana');
    const picker = page.getByRole('listbox', { name: 'People to mention', exact: true });
    await expect(picker.getByRole('option', { name: 'Ana Soursop', exact: true })).toBeVisible();
    // Browser keyboard automation cannot run an OS IME. Dispatch its event
    // sequence and verify that the picker leaves confirmation keys alone.
    expect(await editor.evaluate(element => element.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Enter', isComposing: true, bubbles: true, cancelable: true,
    })))).toBe(true);
    await editor.dispatchEvent('compositionstart');
    await expect(picker).toBeHidden();
    await editor.dispatchEvent('input', { isComposing: true });
    await expect(picker).toBeHidden();
    for (const key of ['ArrowDown', 'Enter', 'Escape', 'Tab']) {
      expect(await editor.evaluate((element, key) => element.dispatchEvent(new KeyboardEvent('keydown', {
        key, bubbles: true, cancelable: true,
      })), key), key).toBe(true);
    }
    if (surface === 'wiki source') await expect(editor).toHaveValue('Check @Ana');
    else await expect(editor).toHaveText('Check @Ana');
    await editor.dispatchEvent('compositionend');
    await expect(picker.getByRole('option', { name: 'Ana Soursop', exact: true })).toBeVisible();
    await editor.press('Enter');
    await expect(picker).toBeHidden();
    if (surface === 'wiki source') await expect(editor).toHaveValue(/ri:user ri:account-id=/);
    else await expect(editor).toContainText('@Ana Soursop');
  });
}

for (const surface of ['wiki rich', 'wiki source']) {
  test(`${surface} keeps an inserted mention immediately before any sync timer runs`, async ({ page }) => {
    await page.clock.install();
    const editor = await open(page, surface, true);
    await expect(page.locator('[data-wiki-live-sync]')).toContainText('kept on this device');
    await page.clock.pauseAt(new Date());
    await editor.fill('Check @Ana');
    const picker = page.getByRole('listbox', { name: 'People to mention', exact: true });
    await expect(picker.getByRole('option', { name: 'Ana Soursop', exact: true })).toBeVisible();
    await editor.press('Enter');
    expect(await page.evaluate(() => Object.values(localStorage).some(value => {
      try { return JSON.parse(value).text?.includes('<ri:user ri:account-id='); } catch { return false; }
    }))).toBe(true);
    await page.reload();
    if (surface === 'wiki source') await expect(editor).toHaveValue(/ri:user ri:account-id=/);
    else await expect(editor).toContainText('@Ana Soursop');
  });
}
