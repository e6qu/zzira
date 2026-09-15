import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// Keyboard shortcuts for the start and end of a document differ by platform,
// so the caret is placed on the text directly.
async function caretAt(page: Page, where: 'start' | 'end') {
  await page.getByRole('textbox', { name: 'Page content' }).evaluate((editor, edge) => {
    (editor as HTMLElement).focus();
    const walker = document.createTreeWalker(editor, NodeFilter.SHOW_TEXT);
    const texts: Text[] = [];
    for (let node = walker.nextNode(); node; node = walker.nextNode()) texts.push(node as Text);
    const range = document.createRange();
    if (!texts.length) range.selectNodeContents(editor);
    else if (edge === 'start') range.setStart(texts[0], 0);
    else range.setStart(texts[texts.length - 1], texts[texts.length - 1].length);
    range.collapse(true);
    const selection = document.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
  }, where);
}

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

test('people editing the same page keep each other\'s changes as they type', async ({ browser }) => {
  test.setTimeout(120_000);
  const demo = await browser.newPage();
  await login(demo, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  const title = `Together ${stamp}`;
  await demo.goto('/wiki');
  await demo.locator('.wiki-create-space > summary').click();
  await demo.getByLabel('Space name').fill(`Together ${stamp}`);
  await demo.getByLabel('Space key').fill(`T${stamp}`);
  await demo.getByLabel('Description', { exact: true }).fill('Live editing journey');
  await demo.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(demo).toHaveURL(/\/wiki\/spaces\/\d+$/);
  await demo.getByRole('link', { name: 'Create page', exact: true }).click();
  await demo.getByLabel('Page title').fill(title);
  await demo.getByRole('textbox', { name: 'Page content' }).fill('Plan.');
  await demo.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(demo.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  const editURL = `${demo.url()}/edit`;

  const ana = await browser.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await demo.goto(editURL);
  await ana.goto(editURL);
  const demoEditor = demo.getByRole('textbox', { name: 'Page content' });
  const anaEditor = ana.getByRole('textbox', { name: 'Page content' });
  await expect(demo.locator('[data-wiki-live-sync]')).toContainText('Live editing is on');
  await expect(ana.locator('[data-wiki-live-sync]')).toContainText('Live editing is on');

  // Both type at once, one at the start and one at the end.
  await caretAt(demo, 'start');
  await caretAt(ana, 'end');
  await Promise.all([
    demo.keyboard.type('Draft: ', { delay: 40 }),
    ana.keyboard.type(' Approved.', { delay: 40 }),
  ]);
  await expect(demoEditor).toHaveText('Draft: Plan. Approved.', { timeout: 20_000 });
  await expect(anaEditor).toHaveText('Draft: Plan. Approved.', { timeout: 20_000 });
  await expect(ana.locator('[data-wiki-live]')).toContainText('also editing. Your changes merge as you type.', { timeout: 20_000 });
  await accessible(ana);
  await ana.setViewportSize({ width: 320, height: 740 });
  expect(await ana.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await ana.setViewportSize({ width: 1280, height: 720 });

  // Demo publishes; Ana keeps editing the restarted document and publishes too.
  await demo.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(demo.locator('.wiki-page-body, main')).toContainText('Draft: Plan. Approved.');
  await expect(anaEditor).toHaveText('Draft: Plan. Approved.');
  await caretAt(ana, 'end');
  await ana.keyboard.type(' Next.', { delay: 20 });
  await expect(anaEditor).toHaveText('Draft: Plan. Approved. Next.');
  await ana.waitForTimeout(1500);
  await ana.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(ana.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  await expect(ana.locator('main')).toContainText('Draft: Plan. Approved. Next.');
});
