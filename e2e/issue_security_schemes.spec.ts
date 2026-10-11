import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';
import axe from 'axe-core';

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

test('an administrator maps restricted work before switching its project scheme', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36);
  const key = `MAP${Date.now().toString().slice(-6)}`;
  const name = `Mapped delivery ${stamp}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers })).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers, data: { key, name, projectTypeKey: 'software', leadAccountId: me.accountId },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);
  const scheme = async (title: string, levels: string[]) => {
    const response = await page.request.post('/rest/api/3/issuesecurityschemes', {
      headers, data: { name: `${title} ${stamp}`, levels: levels.map(level => ({ name: level, members: [{ type: 'reporter' }] })) },
    });
    expect(response.status()).toBe(201);
    const id = (await response.json()).id as string;
    const detail = await (await page.request.get(`/rest/api/3/issuesecurityschemes/${id}`, { headers })).json();
    return { ...detail, id: String(detail.id), levels: detail.levels.map((level: { id: string | number; name: string }) => ({ ...level, id: String(level.id) })) };
  };
  const old = await scheme('Original boundary', ['Release leads', 'Private notes', 'Unused level']);
  const target = await scheme('Replacement boundary', ['Delivery leads']);
  const association = await page.request.put('/rest/api/3/issuesecurityschemes/project', { headers, maxRedirects: 0, data: { projectId: projectID, schemeId: old.id } });
  expect(association.status(), await association.text()).toBe(303);
  const assigned = async () => (await (await page.request.get(`/rest/api/3/issuesecurityschemes/project?projectId=${projectID}`, { headers })).json()).values[0]?.issueSecuritySchemeId;
  await expect.poll(assigned).toBe(old.id);
  const issues: string[] = [];
  for (const level of old.levels.slice(0, 2)) {
    const issue = await page.request.post('/rest/api/3/issue', {
      headers, data: { fields: { project: { key }, summary: `Mapped ${level.name} ${stamp}`, issuetype: { name: 'Task' }, security: { id: level.id } } },
    });
    expect(issue.status()).toBe(201);
    issues.push((await issue.json()).key);
  }
  const colleague = await browser.newContext();
  try {
    const ana = await colleague.newPage();
    await login(ana, 'ana@zzira.dev', 'ana12345');
    for (const issue of issues) expect((await ana.request.get(`/rest/api/3/issue/${issue}`)).status()).toBe(404);
    await page.goto('/settings/issue-security-schemes');
    const card = page.locator(`.security-scheme-card[data-scheme-id="${target.id}"]`);
    await card.getByLabel('Assign a project').selectOption({ label: `${name} (${key})` });
    await card.getByRole('button', { name: 'Assign scheme' }).click();
    await expect(page.getByRole('heading', { name: 'Map security levels', level: 1 })).toBeVisible();
    expect(await assigned()).toBe(old.id);
    const form = page.locator('form').filter({ has: page.getByRole('button', { name: 'Assign scheme with mappings' }) });
    await form.getByLabel('Replace Release leads (1 work item)').selectOption(target.levels[0].id);
    await form.getByLabel('Replace Private notes (1 work item)').selectOption('none');
    // A missing choice is refused by the server without discarding the others.
    await form.evaluate(element => { (element as HTMLFormElement).noValidate = true; });
    await form.getByRole('button', { name: 'Assign scheme with mappings' }).click();
    await expect(page.getByRole('alert')).toContainText('Choose a replacement for every security level.');
    await expect(form.getByLabel('Replace Release leads (1 work item)')).toHaveValue(target.levels[0].id);
    await expect(form.getByLabel('Replace Private notes (1 work item)')).toHaveValue('none');
    expect(await assigned()).toBe(old.id);
    for (const issue of issues) expect((await ana.request.get(`/rest/api/3/issue/${issue}`)).status()).toBe(404);
    await form.getByLabel('Replace Unused level (0 work items)').selectOption(target.levels[0].id);
    await page.addScriptTag({ content: axe.source });
    expect(await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations)).toEqual([]);
    await page.setViewportSize({ width: 320, height: 740 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.setViewportSize({ width: 1280, height: 720 });
    const refused = await ana.request.post(`/settings/issue-security-schemes/${target.id}`, {
      form: { action: 'assign-project', project: projectID, mapped: 'true', [`level.${old.levels[0].id}`]: 'none' },
    });
    expect(refused.status()).toBe(403);
    await form.getByRole('button', { name: 'Assign scheme with mappings' }).click();
    await expect(page.getByRole('status')).toContainText('Project assignment queued.');
    await expect.poll(assigned).toBe(target.id);
    const kept = await (await page.request.get(`/rest/api/3/issue/${issues[0]}?fields=security`, { headers })).json();
    expect(String(kept.fields.security.id)).toBe(target.levels[0].id);
    expect(kept.fields.security.name).toBe('Delivery leads');
    expect((await ana.request.get(`/rest/api/3/issue/${issues[0]}`)).status()).toBe(404);
    const cleared = await (await page.request.get(`/rest/api/3/issue/${issues[1]}`, { headers })).json();
    expect(cleared.fields.security).toBeNull();
    expect((await ana.request.get(`/rest/api/3/issue/${issues[1]}`)).status()).toBe(200);
  } finally {
    await colleague.close();
  }
});
