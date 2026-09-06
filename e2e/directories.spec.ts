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
  await expect(page.getByText('Draft changes', { exact: true })).toBeVisible();
  await expect(page.getByText('Draft changes are not active')).toBeVisible();
  await page.getByRole('button', { name: 'Publish workflow' }).click();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();
  await expect(page.getByText('Version 2')).toBeVisible();

  await page.selectOption('#workflow-project', 'prj_default');
  await page.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.locator('.assigned-projects')).toContainText('ZZIRA Demo');

  await page.goto('/projects/ZZ');
  await expect(page.getByRole('heading', { name: 'Workflow' }).locator('xpath=..')).toContainText(workflowName);
});

test('status administrators can create, classify, edit, inspect, and safely delete a status', async ({ page }) => {
  await login(page);
  await page.getByRole('link', { name: 'Statuses', exact: true }).click();
  await expect(page).toHaveURL('/settings/statuses');
  await expect(page.getByRole('heading', { name: 'Statuses', level: 1 })).toBeVisible();

  const builtIn = page.locator('.status-directory-item').filter({ has: page.getByRole('heading', { name: 'To Do', exact: true }) });
  await expect(builtIn).toContainText('Built in');
  await expect(builtIn).toContainText('Built-in statuses are available to every project');

  const originalName = `Review queue ${Date.now()}`;
  await page.fill('#status-name', originalName);
  await page.selectOption('#status-category', 'indeterminate');
  await page.fill('#status-description', 'Waiting for a peer review.');
  await page.getByRole('button', { name: 'Add status' }).click();
  await expect(page.getByRole('status')).toContainText(`${originalName} created`);

  const row = page.locator('.status-directory-item').filter({ has: page.getByRole('heading', { name: originalName, exact: true }) });
  await expect(row).toContainText('Waiting for a peer review.');
  await expect(row.locator('.status-impact')).toContainText('Work items');
  await row.locator('summary', { hasText: 'Edit status' }).click();
  const updatedName = `${originalName} ready`;
  await row.getByLabel('Name').fill(updatedName);
  await row.getByLabel('Category').selectOption('done');
  await row.getByLabel('Description').fill('Review has completed.');
  await row.getByRole('button', { name: 'Save status' }).click();
  await expect(page.getByRole('status')).toContainText(`${updatedName} updated`);

  const updated = page.locator('.status-directory-item').filter({ has: page.getByRole('heading', { name: updatedName, exact: true }) });
  await expect(updated).toContainText('Review has completed.');
  await expect(updated).toContainText('Done');
  await updated.locator('summary', { hasText: 'Edit status' }).click();
  await updated.getByRole('button', { name: 'Delete status' }).click();
  await expect(page.getByRole('status')).toContainText('Status deleted');
  await expect(page.getByRole('heading', { name: updatedName, exact: true })).toHaveCount(0);
});

test('workflow scheme drafts publish only after a safe project impact preview', async ({ page }) => {
  await login(page);
  await page.getByRole('link', { name: 'Workflow schemes', exact: true }).click();
  await expect(page).toHaveURL('/settings/workflow-schemes');
  await expect(page.getByRole('heading', { name: 'Workflow schemes', level: 1 })).toBeVisible();

  const schemeName = `Delivery scheme ${Date.now()}`;
  await page.fill('#scheme-name', schemeName);
  await page.fill('#scheme-description', 'Routes delivery work by type.');
  await page.selectOption('#scheme-default', 'wf_default');
  await page.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page).toHaveURL(/\/settings\/workflow-schemes\/scheme_/);
  await expect(page.getByRole('heading', { name: schemeName, level: 1 })).toBeVisible();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();

  await page.fill('#scheme-edit-description', 'Routes every task through the published default.');
  await page.selectOption('#mapping-it_task', 'wf_default');
  await page.getByRole('button', { name: 'Save draft' }).click();
  await expect(page.getByText('Draft changes', { exact: true })).toBeVisible();
  await expect(page.getByText('Draft mappings are not active')).toBeVisible();
  await page.getByRole('button', { name: 'Publish scheme' }).click();
  await expect(page.getByText('Published', { exact: true })).toBeVisible();
  await expect(page.getByText('Version 2')).toBeVisible();

  await page.selectOption('#scheme-project', 'prj_default');
  await page.getByRole('button', { name: 'Preview assignment' }).click();
  await expect(page.getByText('All current work item statuses exist in the target workflows.')).toBeVisible();
  await page.getByRole('button', { name: 'Assign scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Project assigned');
  await expect(page.locator('.assigned-projects')).toContainText('ZZIRA Demo');
});
