import { expect, test } from '@playwright/test';

test('create a project, use its board, and update settings through UI and API', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');

  const categoryName = `Delivery ${Date.now().toString(36)}`;
  await page.goto('/admin#admin-project-categories');
  const categoryForm = page.locator('.admin-category-create');
  await categoryForm.getByLabel('Category name', { exact: true }).fill(categoryName);
  await categoryForm.getByLabel('Category description', { exact: true }).fill('Projects with a managed release cadence');
  await categoryForm.getByRole('button', { name: 'Create category' }).click();
  await expect(page.getByRole('status')).toContainText('Project category created');
  expect(await page.locator('.admin-category-list input[name=name]').evaluateAll((inputs, value) => inputs.some(input => (input as HTMLInputElement).value === value), categoryName)).toBe(true);

  await page.goto('/projects');
  await page.getByRole('link', { name: 'Create project', exact: true }).click();
  const key = `P${Date.now().toString(36).toUpperCase()}`;
  await page.getByLabel('Name', { exact: true }).fill('Platform delivery');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByLabel('Description', { exact: true }).fill('Delivery planning and releases');
  await page.getByLabel('Category').selectOption({ label: categoryName });
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page).toHaveURL(`/projects/${key}`);
  await expect(page.getByRole('heading', { name: 'Platform delivery', level: 1 })).toBeVisible();
  await page.locator('.nav-board').click();
  await expect(page).toHaveURL(/\/board\/brd_/);
  await page.locator('.nav-project-settings').click();
  await page.locator('#project-name').fill('Platform engineering');
  await page.getByLabel('Project URL').fill('https://example.test/platform');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Project details saved.' })).toBeVisible();
  const response = await page.request.get(`/rest/api/3/project/${key}`);
  expect(response.status()).toBe(200);
  const project = await response.json();
  expect(project).toMatchObject({ key, name: 'Platform engineering', url: 'https://example.test/platform', description: 'Delivery planning and releases', projectCategory: { name: categoryName } });

  await page.getByLabel('Sender email').fill('delivery@example.test');
  await page.getByRole('button', { name: 'Save sender' }).click();
  await expect(page.getByRole('status')).toContainText('Sender email saved.');
  const sender = await page.request.get(`/rest/api/3/project/${project.id}/email`);
  expect(sender.status()).toBe(200);
  await expect(sender.json()).resolves.toMatchObject({ emailAddress: 'delivery@example.test' });

  const reportsFeature = page.locator('.project-feature-list form').filter({ hasText: 'Reports' });
  await reportsFeature.getByRole('button', { name: 'Disable' }).click();
  await expect(page.getByRole('status')).toContainText('Project feature saved.');
  await expect(page.locator('.project-feature-list form').filter({ hasText: 'Reports' })).toContainText('DISABLED');
  await expect(page.locator('.nav-reports')).toHaveCount(0);

  const propertyForm = page.locator('.project-property-create');
  await propertyForm.getByLabel('Property key').fill('release.cadence');
  await propertyForm.getByLabel('JSON value').fill('{"frequency":"weekly"}');
  await propertyForm.getByRole('button', { name: 'Add property' }).click();
  await expect(page.getByRole('status')).toContainText('Project property saved.');
  const property = await page.request.get(`/rest/api/3/project/${key}/properties/release.cadence`);
  expect(property.status()).toBe(200);
  await expect(property.json()).resolves.toMatchObject({ key: 'release.cadence', value: { frequency: 'weekly' } });

  const createComponent = page.locator('.project-component-create');
  await createComponent.getByLabel('Name', { exact: true }).fill('Runtime');
  await createComponent.getByLabel('Description', { exact: true }).fill('Runtime services and ownership');
  await createComponent.getByLabel('Lead', { exact: true }).selectOption({ index: 1 });
  await createComponent.getByLabel('Default assignee').selectOption('COMPONENT_LEAD');
  await createComponent.getByRole('button', { name: 'Create component' }).click();
  await expect(page.getByRole('status')).toContainText('Component created.');
  let component = page.locator('.project-component-list details').filter({ hasText: 'Runtime' });
  await component.locator('summary').click();
  await component.getByLabel('Description', { exact: true }).fill('Runtime systems and services');
  await component.getByRole('button', { name: 'Save component' }).click();
  await expect(page.getByRole('status')).toContainText('Component updated.');
  const components = await page.request.get(`/rest/api/3/project/${key}/components`);
  expect(components.status()).toBe(200);
  expect(await components.json()).toEqual(expect.arrayContaining([expect.objectContaining({ name: 'Runtime', description: 'Runtime systems and services', assigneeType: 'COMPONENT_LEAD' })]));
  component = page.locator('.project-component-list details').filter({ hasText: 'Runtime' });
  await component.locator('summary').click();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await expect(page.locator('.project-components-layout')).toHaveCSS('grid-template-columns', /^\d+(?:\.\d+)?px$/);
  await expect(page.locator('.project-governance-grid')).toHaveCSS('grid-template-columns', /^\d+(?:\.\d+)?px$/);
  await expect(component.locator('.project-component-delete')).toHaveCSS('flex-direction', 'column');
  await expect(component.getByRole('button', { name: 'Delete component' })).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 720 });
  await component.getByRole('button', { name: 'Delete component' }).click();
  await expect(page.getByRole('status')).toContainText('Component deleted.');
  await expect(page.locator('.project-component-list')).toContainText('No components yet');

  await page.goto('/projects/new');
  await page.getByLabel('Name', { exact: true }).fill('Duplicate project');
  await page.getByLabel('Key', { exact: true }).fill(key);
  await page.getByRole('button', { name: 'Create project', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('A project with this key already exists');
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Duplicate project');
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
