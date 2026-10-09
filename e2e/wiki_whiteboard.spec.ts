import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

function object(page: Page, title = 'Build') {
  return page.locator('[data-whiteboard-object]').filter({ has: page.locator('title', { hasText: title }) }).first();
}

async function dragObject(page: Page, title = 'Build') {
  const target = object(page, title);
  await target.scrollIntoViewIfNeeded();
  const box = (await target.boundingBox())!;
  await page.mouse.move(box.x + 30, box.y + 30);
  await page.mouse.down();
  await page.mouse.move(box.x + 90, box.y + 60, { steps: 5 });
  await page.mouse.up();
}

async function checkAccessibility(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

async function prepareWhiteboard(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.goto('/wiki');
  await page.locator('.wiki-create-space > summary').click();
  const suffix = Date.now().toString(36).toUpperCase();
  await page.getByLabel('Space name').fill(`Visual planning ${suffix}`);
  await page.getByLabel('Space key').fill(`V${suffix}`);
  await page.getByLabel('Description', { exact: true }).fill('Connected delivery maps');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  const whiteboards = page.getByRole('region', { name: 'Whiteboards' });
  await whiteboards.locator('summary').filter({ hasText: 'Create whiteboard' }).click();
  await whiteboards.getByLabel('Whiteboard name').fill('Release flow');
  await whiteboards.getByLabel('Template', { exact: true }).selectOption('flow-chart');
  await whiteboards.getByLabel('Template language', { exact: true }).selectOption('en-US');
  await whiteboards.getByRole('button', { name: 'Create whiteboard', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+\/whiteboards\/\d+$/);

  async function addObject(title: string, type: string, color: string, x: string) {
    await page.locator('summary').filter({ hasText: 'Add object' }).click();
    const form = page.locator('details[open]').filter({ hasText: 'Add object' }).locator('form');
    await form.getByLabel('Type', { exact: true }).selectOption(type);
    await form.getByLabel('Title', { exact: true }).fill(title);
    await form.getByLabel('Body', { exact: true }).fill(`${title} details`);
    await form.getByLabel('Color', { exact: true }).selectOption(color);
    await form.locator('[name="x"]').fill(x);
    await form.getByRole('button', { name: 'Add object', exact: true }).click();
  }

  await addObject('Build', 'sticky', 'yellow', '100');
  await addObject('Deploy', 'shape', 'green', '520');
  await expect(page.getByRole('img', { name: 'Release flow visual map' })).toBeVisible();
  await expect(page.locator('.wiki-canvas-object')).toHaveCount(2);

  await page.locator('summary').filter({ hasText: 'Add connector' }).click();
  await page.getByLabel('From', { exact: true }).selectOption({ label: 'Build' });
  await page.getByLabel('To', { exact: true }).selectOption({ label: 'Deploy' });
  await page.getByLabel('Label', { exact: true }).fill('ships');
  await page.getByLabel('Line style', { exact: true }).selectOption('dashed');
  await page.getByRole('button', { name: 'Add connector', exact: true }).click();
  await expect(page.locator('.wiki-connector-dashed')).toHaveCount(1);
  await expect(page.getByRole('region', { name: 'Connectors' })).toContainText('ships');

}

test('knowledge collaborator builds and edits a connected whiteboard', async ({ page }) => {
  await prepareWhiteboard(page);
  const build = page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Build"]') });
  await build.locator('[name="x"]').fill('180');
  await build.getByRole('button', { name: 'Save Build', exact: true }).click();
  await expect(page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Build"]') }).locator('[name="x"]')).toHaveValue('180');

  // An object is dragged where it belongs, and the move is saved as it is
  // let go: the form below the canvas reads the new place.
  const dragged = page.locator('[data-whiteboard-object]').filter({ has: page.locator('title', { hasText: 'Deploy' }) }).first();
  const before = await dragged.getAttribute('data-x');
  const box = (await dragged.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 - 120, box.y + box.height / 2 + 40, { steps: 8 });
  await page.mouse.up();
  await expect(page.locator('[data-whiteboard-status]')).toContainText('Deploy moved to');
  const after = await page.locator('[data-whiteboard-object]').filter({ has: page.locator('title', { hasText: 'Deploy' }) }).first().getAttribute('data-x');
  expect(Number(after)).toBeLessThan(Number(before));
  const movedForm = page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Deploy"]') });
  await expect(movedForm.locator('[name="x"]')).toHaveValue(String(after));
  await movedForm.getByRole('textbox', { name: 'Body', exact: true }).fill('Deployment details after moving');
  await movedForm.getByRole('button', { name: 'Save Deploy', exact: true }).click();
  await page.reload();
  const deploy = page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Deploy"]') });
  await expect(deploy.locator('[name="x"]')).toHaveValue(String(after));

  await checkAccessibility(page);
  await page.locator('[data-theme-toggle]').click();
  await checkAccessibility(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

for (const failure of ['network', 'permission', 'sign-in redirect', 'unexpected page']) {
  test(`a whiteboard move recovers from ${failure} and can be retried`, async ({ page }) => {
    await prepareWhiteboard(page);
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    const target = object(page);
    const beforeX = (await target.getAttribute('data-x'))!;
    const beforeY = (await target.getAttribute('data-y'))!;
    const connector = page.locator('.wiki-connector-dashed');
    const beforeLineX = await connector.getAttribute('x1');
    const beforeLineY = await connector.getAttribute('y1');
    const path = await page.locator('[data-whiteboard-canvas]').getAttribute('data-save');
    const pattern = `**${path}/${await target.getAttribute('data-whiteboard-object')}`;
    await page.route(pattern, async route => {
      if (failure === 'network') await route.abort('connectionfailed');
      else if (failure === 'permission') await route.fulfill({ status: 403, body: 'Not allowed' });
      else if (failure === 'sign-in redirect') await route.fulfill({ status: 303, headers: { Location: '/login' } });
      else await route.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Log in</h1>' });
    });
    await dragObject(page);
    await expect(page.locator('[data-whiteboard-status]')).toContainText('could not be saved');
    await expect(target).toHaveAttribute('data-x', beforeX);
    await expect(target).toHaveAttribute('data-y', beforeY);
    await expect(connector).toHaveAttribute('x1', beforeLineX!);
    await expect(connector).toHaveAttribute('y1', beforeLineY!);
    expect(errors).toEqual([]);
    await page.unroute(pattern);
    await dragObject(page);
    await expect(page.locator('[data-whiteboard-status]')).toContainText('Build moved to');
    const afterX = (await target.getAttribute('data-x'))!;
    expect(Number(afterX)).toBeGreaterThan(Number(beforeX));
    await page.reload();
    await expect(object(page)).toHaveAttribute('data-x', afterX);
  });
}

test('a canceled whiteboard drag restores its position without saving', async ({ page }) => {
  await prepareWhiteboard(page);
  const target = object(page);
  const beforeX = (await target.getAttribute('data-x'))!;
  const beforeY = (await target.getAttribute('data-y'))!;
  let saves = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && /\/objects\//.test(request.url())) saves++;
  });
  await target.scrollIntoViewIfNeeded();
  await page.locator('[data-whiteboard-canvas]').evaluate(canvas => {
    canvas.addEventListener('pointerdown', event => {
      (canvas as HTMLElement).dataset.testPointer = String((event as PointerEvent).pointerId);
    }, { once: true });
  });
  const box = (await target.boundingBox())!;
  await page.mouse.move(box.x + 30, box.y + 30);
  await page.mouse.down();
  await page.mouse.move(box.x + 90, box.y + 60, { steps: 5 });
  expect(await target.getAttribute('data-x')).not.toBe(beforeX);
  await page.locator('[data-whiteboard-canvas]').evaluate(canvas => {
    canvas.dispatchEvent(new PointerEvent('pointercancel', {
      pointerId: Number((canvas as HTMLElement).dataset.testPointer), bubbles: true,
    }));
  });
  await page.mouse.up();
  await expect(page.locator('[data-whiteboard-status]')).toHaveText('Move canceled.');
  await expect(target).toHaveAttribute('data-x', beforeX);
  await expect(target).toHaveAttribute('data-y', beforeY);
  expect(saves).toBe(0);
});

test('an unfinished whiteboard save cannot race another drag', async ({ page }) => {
  await prepareWhiteboard(page);
  const target = object(page);
  const deploy = object(page, 'Deploy');
  const deployX = (await deploy.getAttribute('data-x'))!;
  const deployY = (await deploy.getAttribute('data-y'))!;
  const path = await page.locator('[data-whiteboard-canvas]').getAttribute('data-save');
  const pattern = `**${path}/*`;
  let unblock!: () => void;
  const gate = new Promise<void>(resolve => { unblock = resolve; });
  let saves = 0;
  await page.route(pattern, async route => {
    saves++;
    await gate;
    await route.continue();
  });
  try {
    await dragObject(page);
    await expect(page.locator('[data-whiteboard-status]')).toContainText('Saving Build');
    await expect(page.getByRole('button', { name: 'Save Build', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Delete Build', exact: true })).toBeDisabled();
    await dragObject(page, 'Deploy');
    await expect(deploy).toHaveAttribute('data-x', deployX);
    await expect(deploy).toHaveAttribute('data-y', deployY);
    expect(saves).toBe(1);
  } finally {
    unblock();
  }
  await expect(page.locator('[data-whiteboard-status]')).toContainText('Build moved to');
  await expect(page.getByRole('button', { name: 'Save Build', exact: true })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Delete Build', exact: true })).toBeEnabled();
  const afterX = (await target.getAttribute('data-x'))!;
  await page.unroute(pattern);
  await page.reload();
  await expect(object(page)).toHaveAttribute('data-x', afterX);
  await dragObject(page, 'Deploy');
  await expect(page.locator('[data-whiteboard-status]')).toContainText('Deploy moved to');
});

test('a stalled whiteboard save times out and unlocks editing', async ({ page }) => {
  await prepareWhiteboard(page);
  await page.clock.install();
  const target = object(page);
  const beforeX = (await target.getAttribute('data-x'))!;
  const path = await page.locator('[data-whiteboard-canvas]').getAttribute('data-save');
  let unblock!: () => void;
  const gate = new Promise<void>(resolve => { unblock = resolve; });
  await page.route(`**${path}/*`, async route => {
    await gate;
    await route.abort().catch(() => {}); // The client may have already aborted it.
  });
  try {
    await dragObject(page);
    await expect(page.locator('[data-whiteboard-status]')).toContainText('Saving Build');
    await page.clock.runFor(15001);
    await expect(page.locator('[data-whiteboard-status]')).toContainText('could not be saved');
    await expect(target).toHaveAttribute('data-x', beforeX);
    await expect(page.getByRole('button', { name: 'Save Build', exact: true })).toBeEnabled();
    await expect(page.locator('[data-whiteboard-canvas]')).not.toHaveAttribute('aria-busy');
  } finally {
    unblock();
  }
});

test('dragging a tall whiteboard object never creates negative coordinates', async ({ page }) => {
  await prepareWhiteboard(page);
  const form = page.locator('.wiki-whiteboard-object-list article').filter({ has: page.locator('input[name="title"][value="Build"]') });
  await form.getByRole('spinbutton', { name: 'Height', exact: true }).fill('1200');
  await form.getByRole('button', { name: 'Save Build', exact: true }).click();
  await dragObject(page);
  await expect(page.locator('[data-whiteboard-status]')).toContainText('Build moved to');
  const y = (await object(page).getAttribute('data-y'))!;
  expect(Number(y)).toBeGreaterThanOrEqual(0);
  await page.reload();
  await expect(object(page)).toHaveAttribute('data-y', y);
});
