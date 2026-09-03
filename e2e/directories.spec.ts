import { expect, test, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('project and people directories provide Jira-style navigation journeys', async ({ page }) => {
  await login(page);

  await page.getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page).toHaveURL('/projects');
  await expect(page.getByRole('heading', { name: 'Projects', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'ZZIRA Demo' })).toBeVisible();
  await page.getByRole('link', { name: 'ZZIRA Demo' }).click();
  await expect(page).toHaveURL('/projects/ZZ');
  await expect(page.getByRole('navigation', { name: 'ZZIRA Demo project' })).toContainText('Overview');
  await expect(page.getByRole('navigation', { name: 'ZZIRA Demo project' })).toContainText('Board');
  await expect(page.getByRole('navigation', { name: 'ZZIRA Demo project' })).toContainText('Work items');

  await page.getByRole('link', { name: 'People', exact: true }).click();
  await expect(page).toHaveURL('/people');
  await expect(page.getByRole('heading', { name: 'People', level: 1 })).toBeVisible();
  await page.getByRole('link', { name: /Demo User/ }).click();
  await expect(page).toHaveURL(/\/people\/usr_/);
  await expect(page.getByRole('heading', { name: 'Demo User', level: 1 })).toBeVisible();

  await page.locator('.user-menu summary').click();
  await page.getByRole('link', { name: 'Profile', exact: true }).click();
  await expect(page).toHaveURL(/\/people\/usr_/);
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toContainText('Your profile');
});

test('workflow directory, editor, transition changes, and project assignment work', async ({ page }) => {
  await login(page);
  await page.getByRole('link', { name: 'Workflows', exact: true }).click();
  await expect(page).toHaveURL('/settings/workflows');
  await expect(page.getByRole('heading', { name: 'Workflows', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Default', exact: true })).toBeVisible();

  const workflowName = `Delivery ${Date.now()}`;
  await page.fill('#workflow-name', workflowName);
  await page.getByRole('button', { name: 'Create workflow' }).click();
  await expect(page).toHaveURL(/\/settings\/workflows\/workflow_/);
  await expect(page.getByRole('heading', { name: workflowName, level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Add transition' })).toBeVisible();

  await page.fill('#transition-name', 'Ready for review');
  await page.selectOption('#transition-from', 'st_inprogress');
  await page.selectOption('#transition-to', 'st_done');
  await page.getByRole('button', { name: 'Add transition' }).click();
  await expect(page.getByText('Ready for review', { exact: true })).toBeVisible();

  await page.selectOption('#workflow-project', 'prj_default');
  await page.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.locator('.assigned-projects')).toContainText('ZZIRA Demo');

  await page.goto('/projects/ZZ');
  await expect(page.getByRole('heading', { name: 'Workflow' }).locator('xpath=..')).toContainText(workflowName);
});
