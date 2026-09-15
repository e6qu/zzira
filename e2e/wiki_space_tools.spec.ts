import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('space manager keeps templates, starts pages from them, reads analytics and archives the space', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.getByRole('link', { name: 'Wiki', exact: true }).click();
  await page.locator('.wiki-create-space > summary').click();
  const key = `T${Date.now().toString(36).toUpperCase()}`;
  await page.getByLabel('Space name').fill(`Operations ${key}`);
  await page.getByLabel('Space key').fill(key);
  await page.getByLabel('Description', { exact: true }).fill('Runbooks and incident notes');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();

  // The space keeps its own template.
  await page.getByRole('link', { name: 'Templates', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Templates', level: 1 })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Blueprints' })).toContainText('Meeting notes');
  await expect(page.getByRole('region', { name: 'Space templates' })).toContainText('This space has no templates of its own yet.');
  await page.locator('summary').filter({ hasText: 'Create template' }).click();
  const templateForm = page.locator('form[action$="/templates"]').filter({ has: page.getByLabel('Template name') });
  await templateForm.getByLabel('Template name').fill('Runbook');
  await templateForm.getByLabel('Template description').fill('Steps to operate a service');
  await templateForm.getByLabel('Template body').fill('<h2>Steps</h2><p>Check the dashboard.</p>');
  await templateForm.getByLabel('Template labels').fill('runbook, operations');
  await accessible(page);
  await templateForm.getByRole('button', { name: 'Create template', exact: true }).click();
  const spaceTemplates = page.getByRole('region', { name: 'Space templates' });
  await expect(spaceTemplates).toContainText('Runbook');
  await expect(spaceTemplates).toContainText('runbook, operations');

  // A page starts from the template.
  await spaceTemplates.getByRole('link', { name: 'Create page from Runbook', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText('Check the dashboard.');
  await page.getByLabel('Page title').fill(`Restart the service ${key}`);
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: `Restart the service ${key}`, level: 1 })).toBeVisible();
  await expect(page.getByRole('article', { name: 'Page content' })).toContainText('Check the dashboard.');
  await page.getByText('Attach a file', { exact: true }).click();
  await page.getByLabel('File', { exact: true }).setInputFiles({ name: 'restart-checklist.txt', mimeType: 'text/plain', buffer: Buffer.from('Drain traffic, then restart.') });
  await page.getByRole('button', { name: 'Upload file', exact: true }).click();
  await expect(page.getByRole('link', { name: 'restart-checklist.txt', exact: true })).toBeVisible();

  // Viewing the page shows in the space's analytics.
  await page.goto(spaceURL);
  await page.getByRole('link', { name: 'Analytics', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Analytics', level: 1 })).toBeVisible();
  const row = page.getByRole('row').filter({ hasText: `Restart the service ${key}` });
  await expect(row.getByRole('cell').nth(0)).toHaveText('Page');
  await expect(row.getByRole('cell').nth(1)).toHaveText(/^[1-9]\d*$/);
  await expect(row.getByRole('cell').nth(2)).toHaveText('1');
  await page.getByRole('link', { name: '7 days', exact: true }).click();
  await expect(page.getByRole('link', { name: '7 days', exact: true })).toHaveAttribute('aria-current', 'page');
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Editing and deleting the space's template.
  await page.goto(spaceURL);
  await page.getByRole('link', { name: 'Templates', exact: true }).click();
  await page.getByRole('region', { name: 'Space templates' }).getByRole('link', { name: 'Edit Runbook', exact: true }).click();
  await expect(page.getByLabel('Template name')).toHaveValue('Runbook');
  await page.getByLabel('Template name').fill('Service runbook');
  await page.getByRole('button', { name: 'Save template', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Space templates' })).toContainText('Service runbook');
  await page.getByRole('region', { name: 'Space templates' }).getByRole('button', { name: 'Delete Service runbook', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Space templates' })).toContainText('This space has no templates of its own yet.');

  // The space is exported to HTML for its administrator.
  await page.goto(spaceURL);
  await page.getByRole('button', { name: 'Export to HTML', exact: true }).click();
  const exportsRegion = page.getByRole('region', { name: 'Export space' });
  await expect.poll(async () => {
    await page.reload();
    return exportsRegion.getByRole('link', { name: /^Download export / }).count();
  }, { timeout: 15_000 }).toBeGreaterThan(0);
  await accessible(page);
  const href = await exportsRegion.getByRole('link', { name: /^Download export / }).first().getAttribute('href');
  const archive = await page.request.get(href!);
  expect(archive.status()).toBe(200);
  expect(archive.headers()['content-type']).toBe('application/zip');
  const zipBytes = await archive.body();
  expect(zipBytes.subarray(0, 2).toString()).toBe('PK');
  expect(zipBytes.includes(Buffer.from('index.html'))).toBe(true);
  expect(zipBytes.includes(Buffer.from('pages/'))).toBe(true);
  // The page's attachment travels with it.
  expect(zipBytes.includes(Buffer.from('restart-checklist.txt'))).toBe(true);
  expect(zipBytes.includes(Buffer.from('attachments/'))).toBe(true);

  // The space is archived and restored.
  await page.goto(spaceURL);
  await page.getByRole('button', { name: 'Archive space', exact: true }).click();
  await expect(page.getByText('Archived space', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Restore space', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Archive space', exact: true })).toBeVisible();
  await expect(page.getByText('Archived space', { exact: true })).toHaveCount(0);
});
