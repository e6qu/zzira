import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkWikiAccessibility(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}


test('wiki space, rich page, stale edits, child pages, history, trash and restore', async ({ page, context }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.getByRole('link', { name: 'Wiki', exact: true }).click();
  await page.locator('.wiki-create-space > summary').click();
  const key = `W${Date.now().toString(36).toUpperCase()}`;
  await page.getByLabel('Space name').fill('Engineering handbook');
  await page.getByLabel('Space key').fill(key);
  await page.getByLabel('Description', { exact: true }).fill('How we build and release');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();
  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await checkWikiAccessibility(page);
  await page.getByLabel('Page title').fill('Release checklist');
  await page.getByRole('textbox', { name: 'Page content' }).fill('Review changes and publish release notes.');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Release checklist', level: 1 })).toBeVisible();
  await expect(page.getByRole('article', { name: 'Page content' })).toContainText('Review changes');
  await checkWikiAccessibility(page);
  await page.locator('[data-theme-toggle]').click();
  await checkWikiAccessibility(page);
  await page.locator('[data-theme-toggle]').click();
  const pageURL = page.url();
  const stale = await context.newPage();
  await stale.goto(pageURL + '/edit');
  await page.getByRole('link', { name: 'Edit page', exact: true }).click();
  await page.getByRole('textbox', { name: 'Page content' }).fill('Run checks before publishing.');
  await page.getByLabel('What changed?').fill('Added release checks');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await stale.getByRole('textbox', { name: 'Page content' }).fill('My unsaved changes');
  await stale.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(stale.getByRole('alert')).toContainText('page changed');
  await expect(stale.getByRole('textbox', { name: 'Page content' })).toHaveText('My unsaved changes');
  await stale.close();
  await page.getByText('Page history', { exact: true }).click();
  await expect(page.locator('.wiki-history')).toContainText('Added release checks');
  await page.getByRole('link', { name: 'Create child page', exact: true }).click();
  await page.getByLabel('Page title').fill('Rollback steps');
  await page.getByRole('textbox', { name: 'Page content' }).fill('Restore the previous release.');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await page.getByText('Move to trash', { exact: true }).click();
  await page.getByRole('button', { name: 'Confirm move to trash' }).click();
  await page.getByRole('link', { name: 'Trash', exact: true }).click();
  await page.getByRole('link', { name: 'Rollback steps', exact: true }).click();
  await page.getByText('Restore page', { exact: true }).click();
  await page.getByRole('button', { name: 'Confirm restore' }).click();
  await expect(page.getByRole('link', { name: 'Rollback steps', exact: true })).toBeVisible();
  await page.goto(spaceURL);
  await page.getByLabel('Find a page').fill('Release');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page.getByRole('link', { name: 'Release checklist', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Rollback steps', exact: true })).toHaveCount(0);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
