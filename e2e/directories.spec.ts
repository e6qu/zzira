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

test('project switcher keeps the shell and generic pages in the current project', async ({ page }) => {
  await login(page);
  await page.goto('/projects/ZZ');

  const switcher = page.locator('.project-switcher');
  const summary = switcher.locator('summary');
  await expect(summary).toHaveAttribute('aria-label', 'Switch project. Current project: ZZIRA Demo');
  await expect(page.locator('.global-search')).toHaveAttribute('action', '/issues/ZZ');
  await expect(page.locator('#global-create-issue')).toHaveAttribute('hx-get', '/issues/new?project=ZZ');
  await expect(page.locator('.nav-project-overview')).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.nav-backlog')).toHaveAttribute('href', '/board/brd_default/backlog');
  await expect(page.locator('.nav-board')).toHaveAttribute('href', '/board/brd_default');
  await expect(page.locator('.nav-issues')).toHaveAttribute('href', '/issues/ZZ');

  await summary.click();
  await expect(switcher).toHaveAttribute('open', '');
  await expect(switcher.getByRole('navigation', { name: 'Projects' }).getByRole('link', { name: /ZZIRA Demo/ }))
    .toHaveAttribute('aria-current', 'true');
  await page.getByRole('heading', { name: 'ZZIRA Demo', level: 1 }).click();
  await expect(switcher).not.toHaveAttribute('open', '');

  await summary.click();
  await page.keyboard.press('Escape');
  await expect(switcher).not.toHaveAttribute('open', '');
  await expect(summary).toBeFocused();

  await page.getByRole('link', { name: 'Your work' }).click();
  await expect(page).toHaveURL('/dashboard');
  await expect(page.getByRole('link', { name: 'View all work' })).toHaveAttribute('href', '/issues/ZZ');
  await expect(page.getByRole('link', { name: /Open board/ })).toHaveAttribute('href', '/board/brd_default');
  await expect(page.locator('.global-search')).toHaveAttribute('action', '/issues/ZZ');

  await page.locator('#global-create-issue').click();
  await expect(page.locator('#create-project')).toHaveValue('ZZ');
  await page.keyboard.press('Escape');
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
