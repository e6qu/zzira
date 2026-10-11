import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

// The journey owns and removes its two projects and custom field.
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
  const projectB = await makeProject(keyB, nameB);

  const field = await page.request.post('/rest/api/3/field', {
    headers: auth, data: { name: `Release note ${stamp}`, type: 'text' },
  });
  expect(field.status()).toBe(201);
  const fieldID = (await field.json()).id;

  try {
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

  // Keep the global fallback while giving Alpha its own default.
  await context.locator('.context-default-row input[name="defaultValue"]').fill('Global fallback');
  await context.locator('.context-default-row').getByRole('button', { name: 'Save default' }).click();
  const addContext = card.locator('form.screen-level-create');
  await addContext.locator('input[name="name"]').fill('Alpha override');
  await addContext.locator('textarea[name="description"]').fill('Only Alpha');
  await addContext.locator('select[name="project"]').selectOption(projectA);
  await addContext.getByRole('button', { name: 'Add context' }).click();
  await expect(page.getByRole('status')).toContainText('Custom field context created.');
  await expect(card.locator('.custom-field-context')).toHaveCount(2);
  const override = card.locator('.custom-field-context').filter({ has: page.getByRole('heading', { name: 'Alpha override', exact: true }) });
  await override.locator('.context-default-row input[name="defaultValue"]').fill('Alpha default');
  await override.locator('.context-default-row').getByRole('button', { name: 'Save default' }).click();
  for (const [key, value] of [[keyA, 'Alpha default'], [keyB, 'Global fallback']]) {
    await page.goto(`/projects/${key}`);
    await page.locator('#global-create-issue').click();
    const form = page.getByRole('dialog', { name: 'Create issue' });
    await expect(page.locator('#create-summary')).toBeFocused();
    await form.locator('.create-more summary').click();
    await expect(form.locator(`#create-${fieldID}`)).toHaveValue(value);
    await page.keyboard.press('Escape');
  }
  await page.goto('/settings/custom-fields');
  await addContext.locator('input[name="name"]').fill('Conflicting Alpha');
  await addContext.locator('textarea[name="description"]').fill('Retain my description');
  await addContext.locator('select[name="project"]').selectOption(projectA);
  await addContext.getByRole('button', { name: 'Add context' }).click();
  await expect(addContext.locator('input[name="name"]')).toHaveValue('Conflicting Alpha');
  await expect(addContext.locator('textarea[name="description"]')).toHaveValue('Retain my description');
  await expect(addContext.locator('select[name="project"]')).toHaveValues([projectA]);
  await expect(card.locator('.custom-field-context')).toHaveCount(2);
  await override.getByText('Narrow or remove this context', { exact: true }).click();
  await override.getByRole('button', { name: 'Delete context', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Custom field context deleted.');
  const fallback = await (await page.request.get(`/rest/api/3/issue/createmeta/${keyA}/issuetypes/10002?maxResults=60`, { headers: auth })).json();
  expect(fallback.fields.find((entry: { fieldId: string }) => entry.fieldId === fieldID).defaultValue).toBe('Global fallback');

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
  const meta = await (await page.request.get(`/rest/api/3/issue/createmeta/${keyA}/issuetypes/10002?maxResults=60`, { headers: auth })).json();
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

  expect(await createFormHasField('ZZ')).toBe(false);
  } finally {
    await page.request.post(`/rest/api/3/field/${fieldID}/trash`, { headers: auth, timeout: 10_000 });
    await page.request.delete(`/rest/api/3/field/${fieldID}`, { headers: auth, timeout: 10_000 });
    for (const project of [projectA, projectB]) {
      await page.request.delete(`/rest/api/3/project/${project}`, { headers: auth, timeout: 10_000 });
    }
  }
});
