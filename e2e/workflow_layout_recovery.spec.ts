import { expect, test, Page, Locator } from '@playwright/test';

async function openEditor(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await page.goto('/settings/workflows');
  await page.locator('#workflow-name').fill(`Layout recovery ${Date.now()}`);
  await page.getByRole('button', { name: 'Create workflow', exact: true }).click();
  await expect(page.locator('.workflow-map')).toHaveAttribute('data-editable', 'true');
  await expect(page.locator('.workflow-node')).toHaveCount(3);
  return `**${new URL(page.url()).pathname}/layout`;
}

function node(page: Page, id = 'st_todo') {
  return page.locator(`.workflow-node[data-status-id="${id}"]`);
}

async function position(target: Locator) {
  return target.evaluate(element => ({
    x: parseFloat((element as HTMLElement).style.left),
    y: parseFloat((element as HTMLElement).style.top),
  }));
}

async function moveWithKeyboard(page: Page, id = 'st_todo') {
  await node(page, id).focus();
  await page.keyboard.press('ArrowDown');
}

test('rapid workflow moves preserve every status and publish the saved layout', async ({ page }) => {
  const pattern = await openEditor(page);
  await page.clock.install();
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const saved: string[] = [];
  await page.route(pattern, async route => {
    saved.push(new URLSearchParams(route.request().postData()!).get('status')!);
    if (saved.length === 1) await gate;
    await route.continue();
  });
  const positions = new Map<string, { x: number; y: number }>();
  try {
    for (const id of ['st_todo', 'st_inprogress', 'st_done']) {
      const before = await position(node(page, id));
      await moveWithKeyboard(page, id);
      await page.clock.runFor(251);
      const moved = await position(node(page, id));
      expect(moved.y).toBeGreaterThan(before.y);
      positions.set(id, moved);
    }
    // Coalesce a second edit to one queued status without losing its neighbour.
    await moveWithKeyboard(page, 'st_inprogress');
    await page.clock.runFor(251);
    positions.set('st_inprogress', await position(node(page, 'st_inprogress')));
  } finally {
    release();
  }
  await expect.poll(() => saved.length).toBe(3);
  await expect(page.locator('.workflow-map')).toHaveAttribute('aria-busy', 'false');
  expect(new Set(saved)).toEqual(new Set(positions.keys()));
  await page.unroute(pattern);
  await page.reload();
  for (const [id, moved] of positions) expect(await position(node(page, id))).toEqual(moved);
  await page.getByRole('button', { name: 'Publish workflow', exact: true }).click();
  for (const [id, moved] of positions) expect(await position(node(page, id))).toEqual(moved);
  await expect(page.locator('.workflow-editor-header')).toContainText('Published');
});

test('an earlier refused workflow save does not erase a newer queued position', async ({ page }) => {
  const pattern = await openEditor(page);
  await page.clock.install();
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let saves = 0;
  await page.route(pattern, async route => {
    saves++;
    if (saves === 1) {
      await gate;
      await route.fulfill({ status: 403, body: 'Refused first move' });
    } else await route.continue();
  });
  let latest: { x: number; y: number };
  try {
    await moveWithKeyboard(page);
    await page.clock.runFor(251);
    await expect.poll(() => saves).toBe(1);
    await moveWithKeyboard(page);
    await page.clock.runFor(251);
    latest = await position(node(page));
  } finally {
    release();
  }
  await expect(page.locator('.workflow-map')).toHaveAttribute('aria-busy', 'false');
  expect(saves).toBe(2);
  expect(await position(node(page))).toEqual(latest!);
  await page.reload();
  expect(await position(node(page))).toEqual(latest!);
});

test('saving another workflow status keeps a refused position visible until retry', async ({ page }) => {
  const pattern = await openEditor(page);
  await page.clock.install();
  const before = await position(node(page));
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let saves = 0;
  await page.route(pattern, async route => {
    saves++;
    if (saves === 1) {
      await gate;
      await route.fulfill({ status: 403, body: 'Refused first status' });
    } else await route.continue();
  });
  try {
    await moveWithKeyboard(page);
    await page.clock.runFor(251);
    await expect.poll(() => saves).toBe(1);
    await moveWithKeyboard(page, 'st_inprogress');
    await page.clock.runFor(251);
  } finally {
    release();
  }
  await expect(page.locator('.workflow-map')).toHaveAttribute('aria-busy', 'false');
  await expect(page.locator('.workflow-save-state')).toContainText('Unsaved statuses: To Do');
  expect(await position(node(page))).toEqual(before);
  await page.unroute(pattern);
  await moveWithKeyboard(page);
  await page.clock.runFor(251);
  await expect(page.locator('.workflow-save-state')).toHaveText('Position saved to draft');
});

for (const failure of ['network', 'permission', 'sign-in redirect', 'unexpected page']) {
  test(`workflow positions recover from ${failure} and can be retried`, async ({ page }) => {
    const pattern = await openEditor(page);
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    const before = await position(node(page));
    await page.route(pattern, async route => {
      if (failure === 'network') await route.abort('connectionfailed');
      else if (failure === 'permission') await route.fulfill({ status: 403, body: 'Not allowed' });
      else if (failure === 'sign-in redirect') await route.fulfill({ status: 303, headers: { Location: '/login' } });
      else await route.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Log in</h1>' });
    });
    await moveWithKeyboard(page);
    await expect(page.locator('.workflow-save-state')).toContainText('Position was not saved');
    expect(await position(node(page))).toEqual(before);
    await expect(page.locator('.workflow-editor-header')).toContainText('Published');
    expect(errors).toEqual([]);
    await page.unroute(pattern);
    await moveWithKeyboard(page);
    await expect(page.locator('.workflow-save-state')).toHaveText('Position saved to draft');
    const after = await position(node(page));
    expect(after.y).toBeGreaterThan(before.y);
    await page.reload();
    expect(await position(node(page))).toEqual(after);
  });
}

test('workflow child controls, clicks and canceled drags do not move or save statuses', async ({ page }) => {
  await openEditor(page);
  await page.clock.install();
  const before = await position(node(page));
  let saves = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && request.url().endsWith('/layout')) saves++;
  });
  await node(page).getByRole('button', { name: /Lock editing/ }).focus();
  await page.keyboard.press('ArrowDown');
  await page.clock.runFor(300);
  expect(await position(node(page))).toEqual(before);
  await node(page).locator('header').click();
  await page.clock.runFor(300);
  expect(saves).toBe(0);
  const handle = node(page).locator('header');
  await handle.evaluate(element => {
    element.addEventListener('pointerdown', event => {
      (element as HTMLElement).dataset.testPointer = String((event as PointerEvent).pointerId);
    }, { once: true });
  });
  const box = (await handle.boundingBox())!;
  await page.mouse.move(box.x + 30, box.y + 30);
  await page.mouse.down();
  await page.mouse.move(box.x + 90, box.y + 60, { steps: 4 });
  expect(await position(node(page))).not.toEqual(before);
  await handle.evaluate(element => element.dispatchEvent(new PointerEvent('pointercancel', {
    pointerId: Number((element as HTMLElement).dataset.testPointer), bubbles: true,
  })));
  await page.mouse.up();
  expect(await position(node(page))).toEqual(before);
  expect(saves).toBe(0);
});

test('a stalled workflow save restores the node and unlocks the editor', async ({ page }) => {
  const pattern = await openEditor(page);
  await page.clock.install();
  const before = await position(node(page));
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  await page.route(pattern, async route => {
    await gate;
    await route.abort().catch(() => {});
  });
  try {
    await moveWithKeyboard(page);
    await page.clock.runFor(251);
    await expect(page.locator('.workflow-save-state')).toHaveText('Saving position…');
    await expect(node(page).getByRole('button', { name: /Lock editing/ })).toBeDisabled();
    await expect(page.locator('.workflow-publish button[value="publish"]')).toBeDisabled();
    await page.clock.runFor(15001);
    await expect(page.locator('.workflow-save-state')).toContainText('Position was not saved');
    expect(await position(node(page))).toEqual(before);
    await expect(node(page).getByRole('button', { name: /Lock editing/ })).toBeEnabled();
    await expect(page.locator('.workflow-map')).toHaveAttribute('aria-busy', 'false');
  } finally {
    release();
  }
});
