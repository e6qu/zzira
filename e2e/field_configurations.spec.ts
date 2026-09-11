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

// The journey owns its project, configuration, and scheme, and hands the project
// back to the workspace default before deleting them.
test('site administrators make a field required and the create form enforces it', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const configName = `Strict fields ${stamp}`;
  const schemeName = `Strict scheme ${stamp}`;
  const projectKey = `CFG${Date.now().toString().slice(-6)}`;
  const projectName = `Field rules ${stamp}`;

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: { Authorization: apiAuthHeader() },
    data: { key: projectKey, name: projectName, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);

  await page.goto('/settings/field-configurations');
  await expect(page.getByRole('heading', { name: 'Field configurations', level: 1 })).toBeVisible();
  await expect(page.locator('.nav-field-configurations')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: 'Default Field Configuration', level: 2, exact: true })).toBeVisible();

  const createConfig = page.getByRole('region', { name: 'Create a field configuration', exact: true });
  await createConfig.getByLabel('Configuration name').fill(configName);
  await createConfig.getByLabel('Description').fill('Priority is mandatory, labels are off');
  await createConfig.getByRole('button', { name: 'Create field configuration', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Field configuration created.');

  let configCard = page.locator('.field-config-card').filter({ has: page.getByRole('heading', { name: configName, exact: true }) });
  const configID = await configCard.getAttribute('data-field-config-id');
  expect(configID).toMatch(/^\d+$/);

  // Priority becomes required with its own help text; labels are hidden.
  configCard = page.locator(`.field-config-card[data-field-config-id="${configID}"]`);
  await configCard.getByLabel('Field', { exact: true }).selectOption({ label: 'Priority' });
  await configCard.getByLabel('Behaviour').selectOption('required');
  await configCard.getByLabel('Help text').fill('Pick the release priority');
  await configCard.getByRole('button', { name: 'Save field rule' }).click();
  await expect(page.getByRole('status')).toContainText('Field rule saved.');

  configCard = page.locator(`.field-config-card[data-field-config-id="${configID}"]`);
  await configCard.getByLabel('Field', { exact: true }).selectOption({ label: 'Labels' });
  await configCard.getByLabel('Behaviour').selectOption('hidden');
  await configCard.getByRole('button', { name: 'Save field rule' }).click();
  await expect(page.getByRole('status')).toContainText('Field rule saved.');

  configCard = page.locator(`.field-config-card[data-field-config-id="${configID}"]`);
  await expect(configCard.locator('.field-config-row[data-field-id="priority"]')).toContainText('Required');
  await expect(configCard.locator('.field-config-row[data-field-id="labels"]')).toContainText('Hidden');

  const createScheme = page.getByRole('region', { name: 'Create a field configuration scheme', exact: true });
  await createScheme.getByLabel('Scheme name').fill(schemeName);
  await createScheme.getByRole('button', { name: 'Create field configuration scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Field configuration scheme created.');

  let schemeCard = page.locator('.field-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const schemeID = await schemeCard.getAttribute('data-field-scheme-id');

  schemeCard = page.locator(`.field-scheme-card[data-field-scheme-id="${schemeID}"]`);
  await schemeCard.getByLabel('Map a work type').selectOption({ label: 'Task' });
  await schemeCard.getByLabel('Field configuration').selectOption({ label: configName });
  await schemeCard.getByRole('button', { name: 'Map work type' }).click();
  await expect(page.getByRole('status')).toContainText('Work type mapping saved.');

  schemeCard = page.locator(`.field-scheme-card[data-field-scheme-id="${schemeID}"]`);
  await schemeCard.getByLabel('Assign a project').selectOption({ label: `${projectName} (${projectKey})` });
  await schemeCard.getByRole('button', { name: 'Assign project' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned to the field configuration scheme.');

  // The create form now marks priority required, shows the help text, and no
  // longer offers labels.
  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await dialog.locator('.create-more summary').click();
  await expect(dialog.locator('#create-priority')).toHaveAttribute('required', '');
  await expect(dialog.locator('#create-help-priority')).toHaveText('Pick the release priority');
  await expect(dialog.locator('#create-labels')).toHaveCount(0);

  // The command path enforces the rule, not just the form.
  const rejected = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: projectKey }, summary: `No priority ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(rejected.status()).toBe(400);
  expect(JSON.stringify(await rejected.json())).toContain('priority');

  await dialog.getByLabel('Summary', { exact: false }).fill(`Release work ${stamp}`);
  await dialog.getByLabel('Priority').selectOption({ label: 'Medium' });
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/browse/${projectKey}-\\d+$`));

  await page.goto('/settings/field-configurations');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Handing the project back to the workspace default restores the full form.
  const defaultScheme = page.locator('.field-scheme-card').filter({ has: page.getByRole('heading', { name: 'Default Field Configuration Scheme', exact: true }) });
  await defaultScheme.getByLabel('Assign a project').selectOption({ label: `${projectName} (${projectKey})` });
  await defaultScheme.getByRole('button', { name: 'Assign project' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned to the field configuration scheme.');

  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  await expect(page.getByRole('dialog', { name: 'Create issue' })).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await page.locator('.create-more summary').click();
  await expect(page.locator('#create-labels')).toBeVisible();
  await expect(page.locator('#create-priority')).not.toHaveAttribute('required', '');
  await page.keyboard.press('Escape');

  // Clean up in dependency order: scheme, then configuration.
  await page.goto('/settings/field-configurations');
  schemeCard = page.locator(`.field-scheme-card[data-field-scheme-id="${schemeID}"]`);
  await schemeCard.locator('summary').filter({ hasText: 'Delete field configuration scheme' }).click();
  await schemeCard.getByRole('button', { name: 'Delete field configuration scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Field configuration scheme deleted.');

  configCard = page.locator(`.field-config-card[data-field-config-id="${configID}"]`);
  await configCard.locator('summary').filter({ hasText: /^Delete field configuration$/ }).click();
  await configCard.getByRole('button', { name: 'Delete field configuration', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Field configuration deleted.');
});
