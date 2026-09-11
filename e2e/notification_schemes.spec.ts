import { expect, test } from '@playwright/test';

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

test('site administrators configure delivery and recipients receive a work item notification', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const schemeName = `Release notifications ${Date.now().toString(36)}`;

  await page.goto('/settings/notification-schemes');
  await expect(page.getByRole('heading', { name: 'Notification schemes', level: 1 })).toBeVisible();
  const create = page.getByRole('region', { name: 'Create a scheme' });
  await create.getByLabel('Scheme name').fill(schemeName);
  await create.getByLabel('Description').fill('Notify the release owner when delivery work is created');
  await create.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Notification scheme created.');

  let card = page.locator('.permission-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const schemeID = await card.getAttribute('data-scheme-id');
  expect(schemeID).toMatch(/^\d+$/);
  const anaID = await page.locator('#notification-recipient-values option').filter({ hasText: 'Ana Soursop' }).getAttribute('value');
  expect(anaID).toBeTruthy();
  const rule = card.locator('.notification-rule-create');
  await rule.getByLabel('Work item event').selectOption('1');
  await rule.getByLabel('Recipient', { exact: true }).selectOption('User');
  await rule.getByLabel(/Recipient value/).fill(anaID!);
  await rule.getByRole('button', { name: 'Add recipient' }).click();
  await expect(page.getByRole('status')).toContainText('Event recipient added.');

  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  await expect(card).toContainText('Ana Soursop');
  await card.getByLabel('Assign a project').selectOption({ label: 'ZZIRA Demo (ZZ)' });
  await card.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Project notification scheme assigned.');

  const projectScheme = await page.request.get('/rest/api/3/project/ZZ/notificationscheme?expand=all');
  expect(projectScheme.status()).toBe(200);
  expect((await projectScheme.json()).name).toBe(schemeName);
  await page.goto('/projects/ZZ/settings/notifications');
  await expect(page.getByRole('heading', { name: 'Notifications', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: schemeName, level: 2 })).toBeVisible();
  await expect(page.locator('.nav-project-notifications')).toHaveAttribute('aria-current', 'page');

  // The header button opens the dialog already scoped to the current project, so
  // re-selecting it would trigger a metadata re-render that discards the summary.
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('#create-summary')).toBeFocused();
  await expect(dialog.getByLabel('Project')).toHaveValue('ZZ');
  await dialog.getByLabel('Summary', { exact: false }).fill(`Notification journey ${Date.now()}`);
  await dialog.getByRole('button', { name: 'Create issue', exact: true }).click();
  await expect(page).toHaveURL(/\/browse\/ZZ-\d+$/);
  const issueKey = page.url().split('/').pop()!;

  const member = await browser.newContext();
  const memberPage = await member.newPage();
  await login(memberPage, 'ana@zzira.dev', 'ana12345');
  await memberPage.goto('/notifications');
  await expect(memberPage.locator('.notification-inbox-item').filter({ hasText: `created ${issueKey}` })).toBeVisible();
  await member.close();

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto('/settings/notification-schemes');
  const defaultCard = page.locator('.permission-scheme-card').filter({ hasText: 'Default Notification Scheme' });
  await defaultCard.getByLabel('Assign a project').selectOption({ label: 'ZZIRA Demo (ZZ)' });
  await defaultCard.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Project notification scheme assigned.');
  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  await card.locator('summary').filter({ hasText: 'Delete scheme' }).click();
  await card.getByRole('button', { name: 'Delete scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Notification scheme deleted.');
});
