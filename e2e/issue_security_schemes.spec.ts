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

// The journey owns its project so that assignment starts from a project with no
// restricted work: associating a scheme requires a mapping for every level a
// project already uses, and the shared demo project accumulates levels from the
// other specs.
test('site and project administrators govern restricted work from scheme to visibility', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const schemeName = `Release confidentiality ${stamp}`;
  const projectKey = `SEC${Date.now().toString().slice(-6)}`;
  const projectName = `Restricted delivery ${stamp}`;

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const createdProject = await page.request.post('/rest/api/3/project', {
    headers: { Authorization: apiAuthHeader() },
    data: { key: projectKey, name: projectName, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(createdProject.status()).toBe(201);
  const projectID = String((await createdProject.json()).id);

  await page.goto('/settings/issue-security-schemes');
  await expect(page.getByRole('heading', { name: 'Issue security schemes', level: 1 })).toBeVisible();
  const create = page.getByRole('region', { name: 'Create an access boundary' });
  await create.getByLabel('Scheme name').fill(schemeName);
  await create.getByLabel('Description').fill('Limit release work to its reporter and selected reviewers');
  await create.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Issue security scheme created.');

  let card = page.locator('.security-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const schemeID = await card.getAttribute('data-scheme-id');
  expect(schemeID).toMatch(/^\d+$/);
  await card.getByLabel('Level name').fill('Release leads');
  await card.getByLabel('Description', { exact: true }).last().fill('Visible to the reporter and approved release leads');
  await card.getByLabel('Use by default for new work').check();
  await card.getByRole('button', { name: 'Add level' }).click();
  await expect(page.getByRole('status')).toContainText('Security level added.');

  card = page.locator(`.security-scheme-card[data-scheme-id="${schemeID}"]`);
  const level = card.locator('.security-level-card').filter({ has: page.getByRole('heading', { name: 'Release leads', exact: true }) });
  const levelID = await level.getAttribute('data-level-id');
  expect(levelID).toMatch(/^\d+$/);
  await level.getByLabel('Add a contextual rule').selectOption('reporter');
  await level.getByRole('button', { name: 'Add rule' }).click();
  await expect(page.getByRole('status')).toContainText('Level member added.');

  card = page.locator(`.security-scheme-card[data-scheme-id="${schemeID}"]`);
  await card.getByLabel('Assign a project').selectOption({ label: `${projectName} (${projectKey})` });
  await card.getByRole('button', { name: 'Assign scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Project assignment queued.');
  await expect.poll(async () => (await page.request.get(`/rest/api/3/project/${projectKey}/issuesecuritylevelscheme`)).status()).toBe(200);

  await page.goto(`/projects/${projectKey}/settings/issue-security`);
  await expect(page.getByRole('heading', { name: 'Issue security', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: schemeName, level: 2 })).toBeVisible();
  await expect(page.getByText('Reporter', { exact: true })).toBeVisible();
  await expect(page.locator('.nav-project-issue-security')).toHaveAttribute('aria-current', 'page');

  // The header button opens the dialog already scoped to the current project, so
  // re-selecting it would trigger a metadata re-render that discards the summary.
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await expect(dialog.getByLabel('Project')).toHaveValue(projectKey);
  await dialog.getByLabel('Summary', { exact: false }).fill(`Restricted release ${Date.now()}`);
  await dialog.locator('.create-more summary').click();
  await dialog.getByLabel('Restrict to').selectOption(levelID!);
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/browse/${projectKey}-\\d+$`));
  const issueKey = page.url().split('/').pop()!;

  const ana = await browser.newContext();
  const anaPage = await ana.newPage();
  await login(anaPage, 'ana@zzira.dev', 'ana12345');
  expect((await anaPage.request.get(`/rest/api/3/issue/${issueKey}`)).status()).toBe(404);

  await page.goto('/settings/issue-security-schemes');
  card = page.locator(`.security-scheme-card[data-scheme-id="${schemeID}"]`);
  const editableLevel = card.locator(`.security-level-card[data-level-id="${levelID}"]`);
  await editableLevel.getByLabel('Add a person').selectOption({ label: 'Ana Soursop' });
  await editableLevel.getByRole('button', { name: 'Add person' }).click();
  await expect(page.getByRole('status')).toContainText('Level member added.');
  expect((await anaPage.request.get(`/rest/api/3/issue/${issueKey}`)).status()).toBe(200);
  await ana.close();

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  const cleared = await page.request.put('/rest/api/3/issuesecurityschemes/project', {
    maxRedirects: 0,
    headers: { Authorization: apiAuthHeader() },
    data: {
      projectId: projectID,
      schemeId: null,
      oldToNewSecurityLevelMappings: [{ oldLevelId: levelID, newLevelId: '' }],
    },
  });
  expect(cleared.status()).toBe(303);
  await expect.poll(async () => (await page.request.get(`/rest/api/3/issuesecurityschemes/project?projectId=${projectID}`)).json().then(body => body.total)).toBe(0);
  await page.goto('/settings/issue-security-schemes');
  card = page.locator(`.security-scheme-card[data-scheme-id="${schemeID}"]`);
  await card.locator('summary').filter({ hasText: 'Delete scheme' }).click();
  await card.getByRole('button', { name: 'Delete scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Issue security scheme deleted.');
});
