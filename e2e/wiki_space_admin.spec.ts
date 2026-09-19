import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// A space administrator governs a space entirely from the browser: its
// details, the custom roles that gather permissions, and the direct grants
// that answer a single case. All three used to be API-only.
test('space administrator renames a space, keeps its custom roles and grants a permission directly', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');
  await page.getByRole('link', { name: 'Wiki', exact: true }).click();
  await page.locator('.wiki-create-space > summary').click();
  const key = `A${Date.now().toString(36).toUpperCase()}`;
  await page.getByLabel('Space name').fill(`Support ${key}`);
  await page.getByLabel('Space key').fill(key);
  await page.getByLabel('Description', { exact: true }).fill('Customer questions');
  await page.getByRole('button', { name: 'Create space', exact: true }).click();
  await expect(page).toHaveURL(/\/wiki\/spaces\/\d+$/);
  const spaceURL = page.url();

  // A page to point the space at, so the home page choice has an answer.
  await page.getByRole('link', { name: 'Create page', exact: true }).click();
  await page.getByLabel('Page title').fill(`Welcome ${key}`);
  await page.getByRole('textbox', { name: 'Page content' }).fill('<p>Start here.</p>');
  await page.getByRole('button', { name: 'Save page', exact: true }).click();
  await expect(page.getByRole('heading', { name: `Welcome ${key}`, level: 1 })).toBeVisible();

  // Space details: rename, describe, and open on that page.
  await page.goto(spaceURL);
  const details = page.getByRole('region', { name: 'Space details' });
  await details.locator('summary').filter({ hasText: 'Edit space details' }).click();
  await accessible(page);
  await details.getByLabel('Space name').fill(`Customer support ${key}`);
  await details.getByLabel('Description', { exact: true }).fill('Questions customers ask and the answers we give');
  await details.getByLabel('Home page').selectOption({ label: `Welcome ${key}` });
  await details.getByRole('button', { name: 'Save space details', exact: true }).click();
  await expect(page.getByRole('heading', { name: `Customer support ${key}`, level: 1 })).toBeVisible();
  await expect(page.locator('.page-header')).toContainText('Questions customers ask and the answers we give');

  // A custom role is created, edited and deleted without leaving the page.
  const roles = page.getByRole('region', { name: 'Space roles' });
  // A closed <details> keeps its contents out of the accessibility tree, so
  // each card is opened by its summary and only then addressed by its button.
  await roles.locator('summary').filter({ hasText: 'Create custom role' }).click();
  const createRole = roles.locator('form').filter({ has: page.getByRole('button', { name: 'Create custom role', exact: true }) });
  await createRole.getByLabel('Role name', { exact: true }).fill(`Reviewers ${key}`);
  await createRole.getByLabel('Description', { exact: true }).fill('Read and comment on what the team writes');
  await createRole.getByRole('checkbox', { name: 'Update pages' }).check();
  await createRole.getByRole('button', { name: 'Create custom role', exact: true }).click();
  await expect(roles).toContainText(`Reviewers ${key}`);

  await roles.locator('summary').filter({ hasText: `Edit Reviewers ${key}` }).click();
  const editRole = roles.locator('form').filter({ has: page.getByRole('button', { name: 'Save role', exact: true }) });
  await editRole.getByLabel('Role name', { exact: true }).fill(`Reviewers and editors ${key}`);
  await editRole.getByRole('checkbox', { name: 'Delete pages' }).check();
  await accessible(page);
  await editRole.getByRole('button', { name: 'Save role', exact: true }).click();
  await expect(roles).toContainText(`Reviewers and editors ${key}`);

  // A direct grant: one person, one permission, listed and then removed.
  const grants = page.getByRole('region', { name: 'Direct grants' });
  await expect(grants).toContainText('No direct grants');
  await grants.locator('summary').filter({ hasText: 'Grant to a person' }).click();
  const grantForm = grants.locator('form').filter({ has: page.getByRole('button', { name: 'Grant to person', exact: true }) });
  await grantForm.getByLabel('Permission').selectOption('read/space');
  await grantForm.getByRole('button', { name: 'Grant to person', exact: true }).click();
  // The space's access was implicit until now, so the grant that ends it also
  // records the administrator writing it -- otherwise they would lose the
  // page they granted it from.
  const granted = grants.getByRole('listitem');
  await expect(granted).toHaveCount(2);
  await expect(granted.filter({ hasText: 'read/space' })).toHaveCount(1);
  await expect(granted.filter({ hasText: 'administer/space' })).toHaveCount(1);
  await accessible(page);
  await grants.getByRole('button', { name: /^Remove grant of read\/space/ }).click();
  await expect(page.getByRole('region', { name: 'Direct grants' }).getByRole('listitem')).toHaveCount(1);
  await expect(page.getByRole('region', { name: 'Direct grants' }).getByRole('listitem')).toContainText('administer/space');

  // Deleting the custom role leaves the system roles alone.
  const rolesAfter = page.getByRole('region', { name: 'Space roles' });
  await rolesAfter.locator('summary').filter({ hasText: `Delete Reviewers and editors ${key}` }).click();
  await rolesAfter.locator('form').filter({ has: page.getByRole('button', { name: 'Confirm delete', exact: true }) })
    .getByRole('button', { name: 'Confirm delete', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Space roles' })).not.toContainText(`Reviewers and editors ${key}`);
  await expect(page.getByRole('region', { name: 'Space roles' })).toContainText('Space administrators');
});
