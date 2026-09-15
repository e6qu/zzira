import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

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

  // Each sees a named caret where the other is working: Demo at the start
  // of the text and Ana at its end.
  await caretAt(demo, 'start');
  await caretAt(ana, 'end');
  const demoCaret = ana.locator('.wiki-remote-caret').filter({ hasText: /demo/i });
  const anaCaret = demo.locator('.wiki-remote-caret').filter({ hasText: /ana/i });
  await expect(demoCaret).toHaveCount(1, { timeout: 10_000 });
  await expect(anaCaret).toHaveCount(1, { timeout: 10_000 });
  const anaBox = (await anaEditor.boundingBox())!;
  await expect.poll(async () => (await demoCaret.boundingBox())!.x - anaBox.x, { timeout: 10_000 }).toBeLessThan(60);
  const demoBox = (await demoEditor.boundingBox())!;
  await expect.poll(async () => (await anaCaret.boundingBox())!.x - demoBox.x, { timeout: 10_000 }).toBeGreaterThan(100);
  await expect(ana.locator('.wiki-remote-carets')).toHaveAttribute('aria-hidden', 'true');
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

test('live edits made offline stay on the device and merge when back online', async ({ browser }) => {
  test.setTimeout(120_000);
  const context = await browser.newContext();
  const demo = await context.newPage();
  await login(demo, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  const title = `Offline ${stamp}`;
  await demo.goto('/wiki');
  await demo.locator('.wiki-create-space > summary').click();
  await demo.getByLabel('Space name').fill(`Offline ${stamp}`);
  await demo.getByLabel('Space key').fill(`O${stamp}`);
  await demo.getByLabel('Description', { exact: true }).fill('Offline live editing journey');
  await demo.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(demo).toHaveURL(/\/wiki\/spaces\/\d+$/);
  await demo.getByRole('link', { name: 'Create page', exact: true }).click();
  await demo.getByLabel('Page title').fill(title);
  await demo.getByRole('textbox', { name: 'Page content' }).fill('Plan.');
  await demo.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(demo.getByRole('heading', { name: title, level: 1 })).toBeVisible();
  const editURL = `${demo.url()}/edit`;

  // The edit page is loaded once under the service worker, so it opens offline.
  await demo.goto(editURL);
  await demo.waitForFunction(() => Boolean(navigator.serviceWorker && navigator.serviceWorker.controller));
  await demo.reload();
  const demoEditor = demo.getByRole('textbox', { name: 'Page content' });
  const demoStatus = demo.locator('[data-wiki-live-sync]');
  await expect(demoStatus).toContainText('Live editing is on');

  await context.setOffline(true);
  await caretAt(demo, 'end');
  await demo.keyboard.type(' Offline note.', { delay: 20 });
  await expect(demoStatus).toContainText('kept on this device', { timeout: 10_000 });
  await demo.reload();
  // The reopened editor starts from the typing kept on the device; offline,
  // its first exchange fails and the status says the changes are kept.
  await expect(demoEditor).toHaveText('Plan. Offline note.');
  await expect(demoStatus).toContainText('kept on this device');

  // Someone else edits the page while Demo is away.
  const ana = await browser.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await ana.goto(editURL);
  const anaEditor = ana.getByRole('textbox', { name: 'Page content' });
  await expect(ana.locator('[data-wiki-live-sync]')).toContainText('Live editing is on');
  await caretAt(ana, 'start');
  await ana.keyboard.type('Draft: ', { delay: 20 });
  await ana.waitForTimeout(1500);

  await context.setOffline(false);
  await expect(demoEditor).toHaveText('Draft: Plan. Offline note.', { timeout: 20_000 });
  await expect(anaEditor).toHaveText('Draft: Plan. Offline note.', { timeout: 20_000 });
  await expect(demoStatus).toContainText('Live editing is on');
  // Once shared, nothing is left waiting on the device.
  await expect.poll(async () => demo.evaluate(() => Object.keys(localStorage).filter((key) => key.startsWith('zzira-live:')).length), { timeout: 10_000 }).toBe(0);
  await context.close();
});

test('people editing the same blog post keep each other\'s changes as they type', async ({ browser, request }) => {
  test.setTimeout(120_000);
  const demo = await browser.newPage();
  await login(demo, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36).toUpperCase();
  await demo.goto('/wiki');
  await demo.locator('.wiki-create-space > summary').click();
  await demo.getByLabel('Space name').fill(`News ${stamp}`);
  await demo.getByLabel('Space key').fill(`N${stamp}`);
  await demo.getByLabel('Description', { exact: true }).fill('Live blog journey');
  await demo.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(demo).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceID = new URL(demo.url()).pathname.split('/').pop()!;
  const created = await request.post('/wiki/api/v2/blogposts', {
    headers: { Authorization: apiAuthHeader(), 'X-Atlassian-Token': 'no-check' },
    data: { spaceId: spaceID, status: 'current', title: `Weekly ${stamp}`, body: { representation: 'storage', value: '<p>Notes.</p>' } },
  });
  expect(created.status(), await created.text()).toBe(200);
  const postURL = `/wiki/spaces/${spaceID}/blogposts/${(await created.json()).id}`;

  const ana = await browser.newPage();
  await login(ana, 'ana@zzira.dev', 'ana12345');
  await demo.goto(`${postURL}?edit=true`);
  await ana.goto(`${postURL}?edit=true`);
  const demoBody = demo.getByLabel('Content', { exact: true });
  const anaBody = ana.getByLabel('Content', { exact: true });
  await expect(demo.locator('[data-wiki-live-sync]')).toContainText('Live editing is on');
  await expect(ana.locator('[data-wiki-live-sync]')).toContainText('Live editing is on');

  // One writes inside the paragraph's start and the other before its end.
  await demoBody.evaluate((field: HTMLTextAreaElement) => { field.focus(); field.setSelectionRange(3, 3); });
  await anaBody.evaluate((field: HTMLTextAreaElement) => { field.focus(); field.setSelectionRange(field.value.length - 4, field.value.length - 4); });
  await Promise.all([
    demo.keyboard.type('Draft: ', { delay: 40 }),
    ana.keyboard.type(' Approved.', { delay: 40 }),
  ]);
  await expect(demoBody).toHaveValue('<p>Draft: Notes. Approved.</p>', { timeout: 20_000 });
  await expect(anaBody).toHaveValue('<p>Draft: Notes. Approved.</p>', { timeout: 20_000 });
  // In source mode too, each sees where the other's caret is.
  const anaCaret = demo.locator('.wiki-remote-caret').filter({ hasText: /ana/i });
  await expect(anaCaret).toHaveCount(1, { timeout: 10_000 });
  const demoFieldBox = (await demoBody.boundingBox())!;
  const anaCaretBox = (await anaCaret.boundingBox())!;
  expect(anaCaretBox.x).toBeGreaterThan(demoFieldBox.x);
  expect(anaCaretBox.x).toBeLessThan(demoFieldBox.x + demoFieldBox.width);
  await expect(ana.locator('.wiki-remote-caret').filter({ hasText: /demo/i })).toHaveCount(1, { timeout: 10_000 });
  await accessible(ana);

  await demo.getByRole('button', { name: 'Save blog post', exact: true }).click();
  await expect(demo).toHaveURL(new RegExp(`${postURL}$`));
  await expect(demo.locator('main')).toContainText('Draft: Notes. Approved.');
});

