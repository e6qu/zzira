import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('plan a release, assign scope, publish notes, archive and delete', async ({ page, context }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.goto('/projects/ZZ');
  await page.locator('.nav-releases').click();
  await expect(page.getByRole('heading', { name: 'Releases', exact: true })).toBeVisible();
  const name = `September ${Date.now()}`;
  await page.getByLabel('Version name').fill(name);
  await page.getByLabel('Description', { exact: true }).fill('Release planning and delivery');
  await page.getByLabel('Start date', { exact: true }).fill('2026-09-01');
  await page.getByLabel('Release date', { exact: true }).fill('2026-09-30');
  await accessible(page);
  await page.getByRole('button', { name: 'Create version', exact: true }).click();
  await expect(page).toHaveURL(/\/projects\/ZZ\/releases\/\d+$/);
  const releaseURL = page.url();
  const id = releaseURL.split('/').pop()!;
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(name);
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await dialog.getByLabel('Summary', { exact: false }).fill(`Release scope ${name}`);
  await page.locator('.create-more summary').click();
  await page.selectOption('#create-fixVersions', [id]);
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  const issue = { key: page.url().split('/').pop()! };
  await expect(page.getByRole('link', { name: `Fix version: ${name}`, exact: true })).toBeVisible();
  await page.goto(releaseURL);
  await page.getByRole('button', { name: `Remove ${issue.key} from release`, exact: true }).click();
  await page.getByLabel('Add work item by key').fill(issue.key);
  await page.getByRole('button', { name: 'Add to release' }).click();
  await expect(page.locator('.release-issues')).toContainText(issue.key);
  await expect(page.locator('.release-progress')).toContainText('0 of 1 done');
  await expect(page.locator('.release-notes')).toContainText(issue.key);
  await page.getByRole('link', { name: 'Open in search' }).click();
  await expect(page.locator('.issue-list')).toContainText(issue.key);
  await page.goto(`/browse/${issue.key}`);
  await expect(page.getByRole('link', { name: `Fix version: ${name}`, exact: true })).toBeVisible();
  await page.goto(releaseURL);
  await page.getByRole('link', { name: 'Edit version', exact: true }).click();
  await page.getByLabel('Version name').fill(`${name} final`);
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(`${name} final`);
  await page.goto(`/browse/${issue.key}`);
  const versionLink = page.getByRole('link', { name: `Fix version: ${name} final`, exact: true });
  await expect(versionLink).toBeVisible();
  await expect.poll(async () => (await page.locator('#sync-banner').textContent()) ?? '', { timeout: 20000 }).toContain('synced');
  await context.setOffline(true);
  try {
    await page.reload();
    await expect(versionLink).toBeVisible();
  } finally {
    await context.setOffline(false);
  }
  await page.goto(releaseURL);
  await page.getByRole('button', { name: 'Release version', exact: true }).click();
  await expect(page.locator('.page-header .eyebrow')).toHaveText('Released');
  const versionResponse = await page.request.get(`/rest/api/3/version/${id}`);
  expect(await versionResponse.json()).toMatchObject({ released: true, releaseDate: '2026-09-30', name: `${name} final` });
  await page.getByRole('button', { name: 'Archive version', exact: true }).click();
  await expect(page.locator('.page-header .eyebrow')).toHaveText('Archived');
  await page.getByRole('button', { name: 'Unarchive version', exact: true }).click();
  await page.getByRole('button', { name: 'Mark unreleased', exact: true }).click();
  await expect(page.locator('.page-header .eyebrow')).toHaveText('Unreleased');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);
  await page.getByRole('button', { name: `Remove ${issue.key} from release`, exact: true }).click();
  await expect(page.locator('.release-progress')).toContainText('0 of 0 done');
  await page.locator('.release-delete > summary').click();
  await page.getByRole('button', { name: 'Delete version permanently', exact: true }).click();
  await expect(page).toHaveURL('/projects/ZZ/releases');
  expect((await page.request.get(`/rest/api/3/version/${id}`)).status()).toBe(404);
  expect((await page.request.get(`/rest/api/3/issue/${issue.key}`)).status()).toBe(200);
});
