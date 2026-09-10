import { expect, test } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens['demo@zzira.dev'];
  return 'Basic ' + Buffer.from(`demo@zzira.dev:${token}`).toString('base64');
}

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

async function moreFields(dialog: import('@playwright/test').Locator) {
  await dialog.locator('.create-more summary').click();
}

// The journey owns its project, screen, and both schemes, and hands the project
// back to the workspace default before deleting them, so the shared workspace
// keeps the same shape for every other spec.
test('site administrators point a work type at a screen and the create form follows', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const screenName = `Lean create ${stamp}`;
  const screenSchemeName = `Lean scheme ${stamp}`;
  const typeSchemeName = `Lean forms ${stamp}`;
  const projectKey = `FRM${Date.now().toString().slice(-6)}`;
  const projectName = `Form layout ${stamp}`;

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: { Authorization: apiAuthHeader() },
    data: { key: projectKey, name: projectName, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);

  // A project starts on the workspace default screen, which shows priority.
  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  let dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await moreFields(dialog);
  await expect(dialog.getByLabel('Priority')).toBeVisible();
  await page.keyboard.press('Escape');

  // Build a screen that shows only summary and labels.
  await page.goto('/settings/screens');
  const createScreen = page.getByRole('region', { name: 'Create a screen' });
  await createScreen.getByLabel('Screen name').fill(screenName);
  await createScreen.getByRole('button', { name: 'Create screen' }).click();
  await expect(page.getByRole('status')).toContainText('Screen created.');
  let screenCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: screenName, exact: true }) });
  const screenID = await screenCard.getAttribute('data-screen-id');
  for (const field of ['Summary', 'Labels']) {
    await page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card`).first().getByLabel('Add a field').selectOption({ label: field });
    await page.locator(`.screen-card[data-screen-id="${screenID}"] .screen-tab-card`).first().getByRole('button', { name: 'Add field' }).click();
    await expect(page.getByRole('status')).toContainText('Field added to the tab.');
  }

  await page.goto('/settings/screen-schemes');
  await expect(page.getByRole('heading', { name: 'Screen schemes', level: 1 })).toBeVisible();
  await expect(page.locator('.nav-screen-schemes')).toHaveAttribute('aria-current', 'page');

  const createScheme = page.getByRole('region', { name: 'Create a screen scheme' });
  await createScheme.getByLabel('Scheme name').fill(screenSchemeName);
  await createScheme.getByLabel('Default screen').selectOption({ label: screenName });
  await createScheme.getByRole('button', { name: 'Create screen scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Screen scheme created.');
  const schemeCard = page.locator('.screen-scheme-card').filter({ has: page.getByRole('heading', { name: screenSchemeName, exact: true }) });
  const screenSchemeID = await schemeCard.getAttribute('data-screen-scheme-id');
  expect(screenSchemeID).toMatch(/^\d+$/);

  const createTypeScheme = page.getByRole('region', { name: 'Create a work type screen scheme' });
  await createTypeScheme.getByLabel('Scheme name').fill(typeSchemeName);
  await createTypeScheme.getByLabel('Default screen scheme').selectOption({ label: screenSchemeName });
  await createTypeScheme.getByRole('button', { name: 'Create work type screen scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Work type screen scheme created.');
  let typeCard = page.locator('.issue-type-scheme-card').filter({ has: page.getByRole('heading', { name: typeSchemeName, exact: true }) });
  const typeSchemeID = await typeCard.getAttribute('data-issue-type-scheme-id');

  typeCard = page.locator(`.issue-type-scheme-card[data-issue-type-scheme-id="${typeSchemeID}"]`);
  await typeCard.getByLabel('Assign a project').selectOption({ label: `${projectName} (${projectKey})` });
  await typeCard.getByRole('button', { name: 'Assign project' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned to the work type screen scheme.');

  // The create form now follows the lean screen.
  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await moreFields(dialog);
  await expect(dialog.getByLabel('Labels')).toBeVisible();
  await expect(dialog.getByLabel('Priority')).toHaveCount(0);
  // Context fields survive any screen, so the work item can still be created.
  await expect(dialog.getByLabel('Project')).toBeVisible();
  await dialog.getByLabel('Summary', { exact: false }).fill(`Lean form work ${stamp}`);
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/browse/${projectKey}-\\d+$`));
  const issueKey = page.url().split('/').pop()!;

  // The edit form follows the same screen through the default fallback.
  const editMeta = await (await page.request.get(`/rest/api/3/issue/${issueKey}/editmeta`, { headers: { Authorization: apiAuthHeader() } })).json();
  expect(Object.keys(editMeta.fields)).toContain('labels');
  expect(Object.keys(editMeta.fields)).not.toContain('priority');

  await page.goto('/settings/screen-schemes');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Handing the project back to the workspace default restores the full form.
  const defaultCard = page.locator('.issue-type-scheme-card').filter({ has: page.getByRole('heading', { name: 'Default Issue Type Screen Scheme', exact: true }) });
  await defaultCard.getByLabel('Assign a project').selectOption({ label: `${projectName} (${projectKey})` });
  await defaultCard.getByRole('button', { name: 'Assign project' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned to the work type screen scheme.');

  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await moreFields(dialog);
  await expect(dialog.getByLabel('Priority')).toBeVisible();
  await page.keyboard.press('Escape');

  // Clean up in dependency order: scheme, screen scheme, then screen.
  await page.goto('/settings/screen-schemes');
  typeCard = page.locator(`.issue-type-scheme-card[data-issue-type-scheme-id="${typeSchemeID}"]`);
  await typeCard.locator('summary').filter({ hasText: 'Delete work type screen scheme' }).click();
  await typeCard.getByRole('button', { name: 'Delete work type screen scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Work type screen scheme deleted.');

  const remaining = page.locator(`.screen-scheme-card[data-screen-scheme-id="${screenSchemeID}"]`);
  await remaining.locator('summary').filter({ hasText: 'Delete screen scheme' }).click();
  await remaining.getByRole('button', { name: 'Delete screen scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Screen scheme deleted.');

  await page.goto('/settings/screens');
  screenCard = page.locator(`.screen-card[data-screen-id="${screenID}"]`);
  await screenCard.locator('summary').filter({ hasText: 'Delete screen' }).click();
  await screenCard.getByRole('button', { name: 'Delete screen' }).click();
  await expect(page.getByRole('status')).toContainText('Screen deleted.');
});
