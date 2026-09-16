import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const EMAIL = 'ana@zzira.dev';
const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
const auth = { Authorization: 'Basic ' + Buffer.from(`${EMAIL}:${tokens[EMAIL]}`).toString('base64') };

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('a member chooses autowatch and own-change notifications from their profile', async ({ page }) => {
  const createAndCheckWatching = async (summary: string) => {
    const created = await page.request.post('/rest/api/3/issue', { headers: auth, data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' } } } });
    expect(created.status()).toBe(201);
    const { key } = await created.json();
    const watchers = await page.request.get(`/rest/api/3/issue/${key}/watchers`, { headers: auth });
    return (await watchers.json()).isWatching as boolean;
  };
  const preference = async (key: string) => {
    const response = await page.request.get(`/rest/api/3/mypreferences?key=${key}`, { headers: auth });
    return response.status() === 200 ? await response.json() : null;
  };

  await page.goto('/login');
  await page.fill('#login-email', EMAIL);
  await page.fill('#login-password', tokens[`${EMAIL}.password`]);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
  await page.goto('/profile');
  const section = page.getByRole('region', { name: 'Email notifications' });
  await expect(section.getByRole('combobox', { name: 'My changes' })).toHaveValue('false');
  await expect(section.getByRole('combobox', { name: 'Autowatch' })).toHaveValue('enabled');
  await accessible(page);
  expect(await createAndCheckWatching(`Autowatched ${Date.now()}`)).toBe(true);

  // Turning autowatch off stops new work from being watched.
  await section.getByRole('combobox', { name: 'My changes' }).selectOption('true');
  await section.getByRole('combobox', { name: 'Autowatch' }).selectOption('disabled');
  await section.getByRole('button', { name: 'Save notification preferences' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Notification preferences saved' })).toBeVisible();
  await expect(page.getByRole('combobox', { name: 'My changes' })).toHaveValue('true');
  await expect(page.getByRole('combobox', { name: 'Autowatch' })).toHaveValue('disabled');
  expect(await preference('user.notify.own.changes')).toBe('true');
  expect(await preference('user.autowatch.disabled')).toBe('true');
  expect(await createAndCheckWatching(`Not autowatched ${Date.now()}`)).toBe(false);

  // Restore the defaults for other journeys.
  await page.getByRole('combobox', { name: 'My changes' }).selectOption('false');
  await page.getByRole('combobox', { name: 'Autowatch' }).selectOption('enabled');
  await page.getByRole('button', { name: 'Save notification preferences' }).click();
  await expect(page.getByRole('combobox', { name: 'Autowatch' })).toHaveValue('enabled');
  expect(await createAndCheckWatching(`Autowatched again ${Date.now()}`)).toBe(true);
});
