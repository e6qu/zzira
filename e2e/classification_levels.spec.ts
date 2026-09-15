import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
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

test('site admin defines a classification level and a page takes it', async ({ page }) => {
  await login(page);
  const stamp = Date.now().toString(36).toUpperCase();
  const levelName = `Board only ${stamp}`;

  // A new level is a draft until it is published.
  await page.goto('/admin#admin-classification-levels');
  const section = page.locator('#admin-classification-levels');
  await section.getByText('Create a level', { exact: true }).click();
  const create = section.locator('form[action="/admin/classification-levels"]');
  await create.getByLabel('New level name').fill(levelName);
  await create.getByLabel('New level description').fill('Only the board may read it');
  await create.getByLabel('New level handling guideline').fill('Never share outside the board.');
  await create.getByLabel('New level color').selectOption('PURPLE');
  await create.getByRole('button', { name: 'Create draft level', exact: true }).click();
  const level = section.locator('li', { hasText: levelName });
  await expect(level).toContainText('DRAFT');
  await accessible(page);
  await level.getByRole('button', { name: `Publish ${levelName}`, exact: true }).click();
  await expect(section.locator('li', { hasText: levelName })).toContainText('PUBLISHED');

  // A page can now be classified with it.
  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  await page.getByLabel('Space name').fill(`Classified ${stamp}`);
  await page.getByLabel('Space key').fill(`C${stamp}`);
  await page.getByLabel('Description', { exact: true }).fill('Classification journey');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await page.getByLabel('Page title').fill(`Board minutes ${stamp}`);
  await page.getByRole('textbox', { name: 'Page content' }).fill('Decisions.');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await page.getByLabel('Data classification').selectOption({ label: levelName });
  await page.getByRole('button', { name: 'Save page classification', exact: true }).click();
  await expect(page.getByLabel('Data classification')).toHaveValue(/.+/);
  await expect(page.locator('.page-header .eyebrow')).toContainText(levelName);

  // Archiving the level keeps it on the page but marks it archived.
  await page.goto('/admin#admin-classification-levels');
  await page.locator('#admin-classification-levels li', { hasText: levelName }).getByRole('button', { name: `Archive ${levelName}`, exact: true }).click();
  await expect(page.locator('#admin-classification-levels li', { hasText: levelName })).toContainText('ARCHIVED');
  await page.goBack();
  await page.goBack();
  await page.reload();
  await expect(page.getByLabel('Data classification').locator('option:checked')).toHaveText(`${levelName} (archived)`);
});
