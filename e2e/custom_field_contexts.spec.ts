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

// The journey owns two projects and one custom field. A custom field cannot be
// deleted through the API, so the field is scoped away from the shared demo
// project at the end rather than left applying to it.
test('site administrators scope a custom field with a context and the create form follows', async ({ page }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const auth = { Authorization: apiAuthHeader() };
  const keyA = `CTA${Date.now().toString().slice(-6)}`;
  const keyB = `CTB${Date.now().toString().slice(-6)}`;
  const nameA = `Context alpha ${stamp}`;
  const nameB = `Context beta ${stamp}`;

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const makeProject = async (key: string, name: string) => {
    const created = await page.request.post('/rest/api/3/project', {
      headers: auth,
      data: { key, name, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
    });
    expect(created.status()).toBe(201);
    return String((await created.json()).id);
  };
  const projectA = await makeProject(keyA, nameA);
  await makeProject(keyB, nameB);

  const field = await page.request.post('/rest/api/3/field', {
    headers: auth, data: { name: `Release note ${stamp}`, type: 'text' },
  });
  expect(field.status()).toBe(201);
  const fieldID = (await field.json()).id;

  const createFormHasField = async (projectKey: string) => {
    await page.goto(`/projects/${projectKey}`);
    await page.locator('#global-create-issue').click();
    const dialog = page.getByRole('dialog', { name: 'Create issue' });
    await expect(dialog).toBeVisible();
    await expect(page.locator('#create-summary')).toBeFocused();
    await dialog.locator('.create-more summary').click();
    const present = await dialog.locator(`#create-${fieldID}`).count();
    await page.keyboard.press('Escape');
    return present > 0;
  };

  // A new custom field is provisioned with a global context, so it reaches both.
  expect(await createFormHasField(keyA)).toBe(true);
  expect(await createFormHasField(keyB)).toBe(true);

  await page.goto('/settings/custom-fields');
  await expect(page.getByRole('heading', { name: 'Custom fields', level: 1 })).toBeVisible();
  await expect(page.locator('.nav-custom-fields')).toHaveAttribute('aria-current', 'page');

  const card = page.locator(`.custom-field-card[data-field-id="${fieldID}"]`);
  await expect(card.locator('.custom-field-context')).toHaveCount(1);
  const context = card.locator('.custom-field-context').first();
  const contextID = await context.getAttribute('data-context-id');
  await expect(context).toContainText('Every project');

  // Narrowing the context to project A removes the field from project B.
  await context.locator('.context-project-row select').selectOption({ label: `${nameA} (${keyA})` });
  await context.locator('.context-project-row').getByRole('button', { name: 'Add project' }).click();
  await expect(page.getByRole('status')).toContainText('Project added to the context.');
  expect(await createFormHasField(keyA)).toBe(true);
  expect(await createFormHasField(keyB)).toBe(false);

  // A default value pre-fills the create form where the context governs.
  await page.goto('/settings/custom-fields');
  const scoped = page.locator(`.custom-field-card[data-field-id="${fieldID}"] .custom-field-context[data-context-id="${contextID}"]`);
  await scoped.locator('.context-default-row input[name="defaultValue"]').fill('Ship it');
  await scoped.locator('.context-default-row').getByRole('button', { name: 'Save default' }).click();
  await expect(page.getByRole('status')).toContainText('Default value saved.');

  await page.goto(`/projects/${keyA}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await dialog.locator('.create-more summary').click();
  await expect(dialog.locator(`#create-${fieldID}`)).toHaveValue('Ship it');
  await page.keyboard.press('Escape');

  // The REST contract agrees with what the browser shows.
  const meta = await (await page.request.get(`/rest/api/3/issue/createmeta/${keyA}/issuetypes/it_task?maxResults=60`, { headers: auth })).json();
  const wire = meta.fields.find((entry: { fieldId: string }) => entry.fieldId === fieldID);
  expect(wire).toBeTruthy();
  expect(wire.defaultValue).toBe('Ship it');
  const mapping = await (await page.request.post(`/rest/api/3/field/${fieldID}/context/mapping`, {
    headers: auth, data: { mappings: [{ projectId: projectA, issueTypeId: 'it_task' }] },
  })).json();
  expect(mapping.total).toBe(1);

  await page.goto('/settings/custom-fields');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // The shared demo project must not keep this field, and a field always keeps
  // one context, so the scope stays narrowed rather than being deleted.
  expect(await createFormHasField('ZZ')).toBe(false);
});
