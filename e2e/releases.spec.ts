import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

async function downloadCSV(page: Page): Promise<{ name: string; lines: string[] }> {
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Download CSV' }).click()]);
  return { name: download.suggestedFilename(), lines: fs.readFileSync((await download.path())!, 'utf8').trim().split('\n') };
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
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
  const deliverySequence = Date.now();
  const buildResponse = await page.request.post('/rest/builds/0.1/bulk', { headers: { Authorization: apiAuthHeader() }, data: {
    properties: { accountId: 'release-e2e' }, builds: [{ pipelineId: `release-${deliverySequence}`, buildNumber: 1,
      updateSequenceNumber: deliverySequence, displayName: 'Release candidate build', url: 'https://ci.example/release/build',
      state: 'successful', lastUpdated: '2026-09-06T12:00:00Z', issueKeys: [issue.key] }],
  } });
  expect(buildResponse.status()).toBe(202);
  const deploymentResponse = await page.request.post('/rest/deployments/0.1/bulk', { headers: { Authorization: apiAuthHeader() }, data: {
    properties: { accountId: 'release-e2e' }, deployments: [{ deploymentSequenceNumber: deliverySequence,
      updateSequenceNumber: deliverySequence, displayName: 'Production rollout', url: 'https://deploy.example/release',
      description: 'Release deployment', lastUpdated: '2026-09-06T13:00:00Z', state: 'successful', issueKeys: [issue.key],
      pipeline: { id: `release-${deliverySequence}`, displayName: 'Release pipeline', url: 'https://ci.example/release' },
      environment: { id: 'production', displayName: 'Production', type: 'production' } }],
  } });
  expect(deploymentResponse.status()).toBe(202);
  await page.reload();
  await expect(page.getByRole('link', { name: `Fix version: ${name}`, exact: true })).toBeVisible();
  await expect(page.locator('.issue-delivery')).toContainText('Release candidate build');
  await expect(page.locator('.issue-delivery')).toContainText('Production rollout');
  await page.goto(releaseURL);
  await page.getByRole('button', { name: `Remove ${issue.key} from release`, exact: true }).click();
  await page.getByLabel('Add work item by key').fill(issue.key);
  await page.getByRole('button', { name: 'Add to release' }).click();
  await expect(page.locator('.release-issues')).toContainText(issue.key);
  await expect(page.locator('.release-progress')).toContainText('0 of 1 done');
  await expect(page.locator('.release-notes')).toContainText(issue.key);
  await expect(page.locator('#release-delivery-list')).toContainText('Release candidate build');
  await expect(page.locator('#release-delivery-list')).toContainText('Production rollout');
  await page.getByRole('link', { name: 'Reports', exact: true }).click();
  await expect(page).toHaveURL('/projects/ZZ/reports');
  await page.getByRole('link', { name: 'Open DORA metrics' }).click();
  await expect(page).toHaveURL('/projects/ZZ/reports/dora');
  await expect(page.getByRole('heading', { name: 'DORA metrics', level: 1 })).toBeVisible();
  const doraSummary = page.getByRole('region', { name: 'DORA summary' });
  await expect(doraSummary.getByText('Deployment frequency', { exact: true })).toBeVisible();
  await expect(doraSummary.getByText('Lead time for changes', { exact: true })).toBeVisible();
  await expect(doraSummary.getByText('Change failure rate', { exact: true })).toBeVisible();
  await expect(doraSummary.getByText('Time to restore service', { exact: true })).toBeVisible();
  await expect(page.locator('.dora-chart')).toBeVisible();
  await expect(page.locator('.dora-detail-grid')).toContainText('Production rollout');
  await page.getByText('View daily data', { exact: true }).click();
  await expect(page.getByRole('table')).toBeVisible();
  const doraCSV = await downloadCSV(page);
  expect(doraCSV.name).toMatch(/^ZZ-DORA-metrics-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(doraCSV.lines[0]).toBe('Date,Successful deployments,Failed or rolled back');
  expect(doraCSV.lines.length).toBe(await page.getByRole('table').locator('tbody tr').count() + 1);
  await page.getByLabel('Compare with previous period').check();
  await page.getByRole('button', { name: 'Update' }).click();
  await expect(page).toHaveURL(/compare=previous/);
  await expect(doraSummary.locator('.report-change')).toHaveCount(4);
  await expect(doraSummary.locator('.report-change').first()).toContainText('previous 30 days');
  await page.getByText('View daily data', { exact: true }).click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.locator('[data-theme-toggle]').click();
  await page.goto(releaseURL);
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

test('a project administrator orders releases from the list', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');

  const stamp = Date.now();
  const create = async (name: string) => {
    await page.goto('/projects/ZZ/releases');
    await page.getByLabel('Version name').fill(name);
    await page.getByRole('button', { name: 'Create version', exact: true }).click();
    await expect(page).toHaveURL(/\/projects\/ZZ\/releases\/\d+$/);
  };
  const first = `Order A ${stamp}`;
  const second = `Order B ${stamp}`;
  await create(first);
  await create(second);

  await page.goto('/projects/ZZ/releases');
  const order = async () => await page.locator('.release-name').allTextContents();
  const before = await order();
  expect(before.indexOf(first)).toBeLessThan(before.indexOf(second));
  await accessible(page);

  // Moving one later swaps it past its neighbour in the project's order.
  await page.getByRole('button', { name: `Move ${first} later` }).click();
  await expect(page).toHaveURL(/\/projects\/ZZ\/releases$/);
  const after = await order();
  expect(after.indexOf(first)).toBeGreaterThan(after.indexOf(second));

  // The order is the project's, not the filtered view's, so a filtered list
  // offers no move at all.
  await page.goto('/projects/ZZ/releases?status=unreleased');
  await expect(page.getByRole('link', { name: second, exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /^Move / })).toHaveCount(0);
});

test('a project member is not offered the order, and is refused one sent anyway', async ({ page, browser }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');

  // Something to order, created by the administrator.
  await page.goto('/projects/ZZ/releases');
  await page.getByLabel('Version name').fill(`Gated ${Date.now()}`);
  await page.getByRole('button', { name: 'Create version', exact: true }).click();
  await expect(page).toHaveURL(/\/projects\/ZZ\/releases\/\d+$/);
  const id = page.url().split('/').pop()!;

  const memberContext = await browser.newContext();
  const member = await memberContext.newPage();
  try {
    await member.goto('/login');
    await member.fill('#login-email', 'ana@zzira.dev');
    await member.fill('#login-password', 'ana12345');
    await member.click('button[type=submit]');
    await member.goto('/projects/ZZ/releases');
    await expect(member.getByRole('heading', { name: 'Releases', exact: true })).toBeVisible();
    await expect(member.getByRole('button', { name: /^Move / })).toHaveCount(0);
    const refused = await member.request.post('/projects/ZZ/releases', { form: { action: 'move', version: id, position: 'Later' } });
    expect(refused.status()).toBe(403);
  } finally {
    await memberContext.close();
  }
});

test('releasing a version moves the work it did not finish', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  const headers = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();

  // Two versions: the one being shipped, and where unfinished work goes.
  const version = async (name: string) => {
    await page.goto('/projects/ZZ/releases');
    await page.getByLabel('Version name').fill(name);
    await page.getByRole('button', { name: 'Create version', exact: true }).click();
    await expect(page).toHaveURL(/\/projects\/ZZ\/releases\/\d+$/);
    return page.url().split('/').pop()!;
  };
  const shipping = await version(`Ship ${stamp}`);
  const nextName = `Next ${stamp}`;
  const next = await version(nextName);

  const issue = async (summary: string) => {
    const created = await page.request.post('/rest/api/3/issue', { headers, data: { fields: { project: { key: 'ZZ' }, summary, issuetype: { name: 'Task' }, fixVersions: [{ id: shipping }] } } });
    expect(created.status()).toBe(201);
    return (await created.json()).key as string;
  };
  const finished = await issue(`Finished ${stamp}`);
  const unfinished = await issue(`Unfinished ${stamp}`);

  // One of them is done, the way the reports journey finishes work.
  const transitions = (await (await page.request.get(`/rest/api/3/issue/${finished}/transitions`, { headers })).json()).transitions as Array<{ id: string; to: { statusCategory: { key: string } } }>;
  const done = transitions.find(candidate => candidate.to.statusCategory.key === 'done');
  expect(done).toBeTruthy();
  expect((await page.request.post(`/rest/api/3/issue/${finished}/transitions`, { headers, data: { transition: { id: done!.id } } })).status()).toBe(204);

  // Release it, sending what is not done to the next version.
  await page.goto(`/projects/ZZ/releases/${shipping}`);
  await page.getByLabel('Work that is not done').selectOption({ label: `Move it to ${nextName}` });
  await page.getByRole('button', { name: 'Release version' }).click();
  await expect(page.getByText('Released', { exact: true }).first()).toBeVisible();

  const fixVersions = async (key: string) => {
    const response = await page.request.get(`/rest/api/3/issue/${key}?fields=fixVersions`, { headers });
    return ((await response.json()).fields.fixVersions as Array<{ id: string }>).map(v => v.id);
  };
  // Work that is not done moved; work that shipped stayed with the release.
  expect(await fixVersions(unfinished)).toEqual([next]);
  expect(await fixVersions(finished)).toEqual([shipping]);
});
