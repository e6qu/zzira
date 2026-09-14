import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function checkAccessibility(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('wiki live docs, stars, owners, Smart Link cards and archived whiteboards', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const stamp = Date.now().toString(36).toUpperCase();
  const live = `Live notes ${stamp}`;
  const link = `Status board ${stamp}`;
  const board = `Retro board ${stamp}`;

  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  await page.getByLabel('Space name').fill(`Details ${stamp}`);
  await page.getByLabel('Space key').fill(`D${stamp}`);
  await page.getByLabel('Description', { exact: true }).fill('Page details journey');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();

  // A live doc is created from the editor and says what it is.
  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await page.getByLabel('Page title').fill(live);
  await page.getByRole('textbox', { name: 'Page content' }).fill('Everything here is live.');
  await page.getByLabel('Live doc — always published, and every edit is live').check();
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: live, level: 1 })).toBeVisible();
  await expect(page.locator('.page-header .eyebrow')).toContainText('Live doc');
  const liveURL = page.url();

  // Starring keeps the page in the wiki's Starred list.
  await page.getByRole('button', { name: 'Star', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Starred', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await page.goto('/wiki');
  await expect(page.getByRole('region', { name: 'Starred' }).getByRole('link', { name: live, exact: true })).toBeVisible();

  // Ownership moves to another member and the page says who owns it.
  await page.goto(liveURL);
  const owner = page.getByLabel('Page owner');
  const choices = await owner.locator('option').evaluateAll((options) => options.map((option) => ({ value: (option as HTMLOptionElement).value, text: option.textContent ?? '', selected: (option as HTMLOptionElement).selected })));
  const next = choices.find((choice) => !choice.selected);
  expect(next).toBeTruthy();
  await owner.selectOption(next!.value);
  await page.getByRole('button', { name: 'Change owner', exact: true }).click();
  await expect(page.locator('.page-header')).toContainText(`Owned by ${next!.text}`);
  await checkAccessibility(page);

  // A Smart Link opens as a card with its destination.
  await page.goto(spaceURL);
  const links = page.getByRole('region', { name: 'Smart Links' });
  await links.locator('summary').filter({ hasText: 'Add Smart Link' }).click();
  await links.getByLabel('Link title').fill(link);
  await links.getByLabel('HTTP or HTTPS URL').fill('https://status.example.test/board');
  await links.getByRole('button', { name: 'Add Smart Link', exact: true }).click();
  await page.getByRole('link', { name: `Smart Link card for ${link}`, exact: true }).click();
  await expect(page.getByRole('heading', { name: link, level: 1 })).toBeVisible();
  await expect(page.locator('.page-header')).toContainText('status.example.test');
  await expect(page.getByRole('link', { name: 'Open link', exact: true })).toHaveAttribute('href', 'https://status.example.test/board');
  await expect(page.locator(`iframe[title="Preview of ${link}"]`)).toHaveAttribute('src', 'https://status.example.test/board');

  // An archived whiteboard still opens, read-only, and can be restored there.
  await page.goto(spaceURL);
  const whiteboards = page.getByRole('region', { name: 'Whiteboards' });
  await whiteboards.locator('summary').filter({ hasText: 'Create whiteboard' }).click();
  await whiteboards.getByLabel('Whiteboard name').fill(board);
  await whiteboards.getByRole('button', { name: 'Create whiteboard', exact: true }).click();
  await page.goto(spaceURL);
  const tree = page.getByRole('region', { name: 'Page tree' });
  const row = (title: string) => tree.locator(`xpath=.//li[div[normalize-space((.//a|.//strong)[1])="${title}"]]`);
  await row(board).locator('summary').click();
  await row(board).getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(page).toHaveURL(/status=archived/);
  await row(board).getByRole('link', { name: board, exact: true }).click();
  await expect(page.getByRole('status', { name: 'Archived whiteboard' })).toContainText('This whiteboard is archived.');
  await checkAccessibility(page);
  await page.getByRole('button', { name: 'Restore whiteboard', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`${spaceURL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(#.*)?$`));
  await expect(row(board)).not.toContainText('archived');
});
