import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';

async function login(page: Page, email = 'demo@zzira.dev', password = 'demo1234') {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
}

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] },
  })).violations);
  expect(violations).toEqual([]);
}

test('site admin manages a directory group and its audited membership', async ({ page, browser }) => {
  await login(page);
  const activity = await page.waitForResponse((response) => response.url().endsWith('/rest/zzira/1/product-activity'));
  expect(activity.status()).toBe(204);
  await page.getByRole('link', { name: 'Administration', exact: true }).click();
  await expect(page).toHaveURL('/admin');
  await expect(page.getByRole('heading', { name: 'ZZIRA', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Products', level: 2 })).toBeVisible();
  await expect(page.getByRole('row', { name: /Jira Software/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /Jira Service Management/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /Confluence/ })).toBeVisible();

  await expect(page.getByRole('heading', { name: 'Jira configuration', level: 2 })).toBeVisible();
  const announcement = `Release maintenance ${Date.now()}`;
  await page.getByLabel('Message', { exact: true }).fill(announcement);
  await page.getByLabel('Visibility').selectOption('private');
  await page.getByLabel('Show banner').check();
  await page.getByLabel('People can dismiss it').check();
  await page.getByRole('button', { name: 'Save announcement' }).click();
  await expect(page).toHaveURL(/saved=Announcement\+banner\+saved/);
  const banner = page.getByRole('complementary', { name: 'Site announcement' });
  await expect(banner).toContainText(announcement);
  const bannerAPI = await page.context().request.get('/rest/api/3/announcementBanner');
  expect(bannerAPI.status()).toBe(200);
  expect(await bannerAPI.json()).toMatchObject({ message: announcement, isEnabled: true, isDismissible: true, visibility: 'private' });
  await banner.getByRole('button', { name: 'Dismiss announcement' }).click();
  await expect(banner).toBeHidden();

  await page.getByLabel('Hours per day').fill('7.5');
  await page.getByLabel('Days per week').fill('4.5');
  await page.getByLabel('Display format', { exact: true }).selectOption('hours');
  await page.getByLabel('Default unit').selectOption('hour');
  await page.getByRole('button', { name: 'Save time tracking' }).click();
  await expect(page).toHaveURL(/saved=Time\+tracking\+settings\+saved/);
  const timeAPI = await page.context().request.get('/rest/api/3/configuration/timetracking/options');
  expect(await timeAPI.json()).toEqual({ defaultUnit: 'hour', timeFormat: 'hours', workingDaysPerWeek: 4.5, workingHoursPerDay: 7.5 });

  const navigator = page.locator('form[action="/admin/jira-configuration/columns"]');
  await navigator.locator('label').filter({ hasText: 'Priority' }).getByRole('checkbox').uncheck();
  await navigator.getByRole('button', { name: 'Save default columns' }).click();
  await expect(page).toHaveURL(/saved=Issue\+navigator\+columns\+saved/);
  const columnsAPI = await page.context().request.get('/rest/api/3/settings/columns');
  const columns = await columnsAPI.json();
  expect(columns.map((column: { value: string }) => column.value)).not.toContain('priority');

  await page.getByText('Advanced application properties', { exact: true }).click();
  const titleProperty = page.locator('form').filter({ hasText: 'jira.title' });
  await titleProperty.getByRole('textbox').fill('ZZIRA Cloud');
  await titleProperty.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/saved=Application\+property\+saved/);
  const propertyAPI = await page.context().request.get('/rest/api/3/application-properties?key=jira.title');
  expect(await propertyAPI.json()).toMatchObject({ id: 'jira.title', value: 'ZZIRA Cloud' });

  const domainName = `journey-${Date.now()}.example.invalid`;
  await page.getByLabel('Domain name').fill(domainName);
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Domain\+added$/);
  let domainRow = page.getByRole('row').filter({ hasText: domainName });
  await expect(domainRow).toContainText('unverified');
  await expect(domainRow).toContainText('zzira-domain-verification=');

  const policyName = `Office network ${Date.now()}`;
  await page.getByLabel('Policy name').fill(policyName);
  await page.getByLabel('Policy type').selectOption('ip-allowlist');
  await page.getByLabel('Rule values').fill('192.0.2.0/24');
  await page.getByRole('group', { name: 'Policy products' }).getByLabel('Jira Software').check();
  await page.getByRole('button', { name: 'Create policy' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+created$/);
  let policy = page.locator('.admin-policy').filter({ hasText: policyName });
  await expect(policy).toContainText('disabled');
  await expect(policy).toContainText('192.0.2.0/24');
  await policy.getByRole('button', { name: 'Enable' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+enabled$/);
  policy = page.locator('.admin-policy').filter({ hasText: policyName });
  await expect(policy).toContainText('enabled');

  const groupName = `delivery-managers-${Date.now()}`;
  await page.getByLabel('Group name').fill(groupName);
  await page.getByLabel('Description', { exact: true }).fill('Coordinates plans, releases, and delivery evidence.');
  await page.getByRole('button', { name: 'Create group' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Group\+created$/);
  const group = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await expect(group).toContainText('0 members');

  const inviteEmail = `journey-${Date.now()}@example.invalid`;
  const invitation = page.locator('form.admin-user-invite');
  await invitation.getByLabel('Display name').fill('Journey Invite');
  await invitation.getByLabel('Email').fill(inviteEmail);
  await invitation.getByRole('group', { name: 'Product access' }).getByLabel('Jira Service Management').check();
  await invitation.getByRole('group', { name: 'Group membership' }).getByLabel(groupName).check();
  await invitation.getByRole('button', { name: 'Invite user' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=User\+invited$/);
  let invitedRow = page.getByRole('row').filter({ hasText: inviteEmail });
  await expect(invitedRow).toContainText('Active');
  await expect(group).toContainText('1 member');
  await invitedRow.getByText('Edit profile', { exact: true }).click();
  await invitedRow.getByLabel('Job title').fill('Service analyst');
  await invitedRow.getByLabel('Department').fill('Customer operations');
  await invitedRow.getByLabel('Location').fill('Remote');
  await invitedRow.getByRole('button', { name: 'Save profile' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Profile\+updated$/);
  invitedRow = page.getByRole('row').filter({ hasText: inviteEmail });
  await expect(invitedRow).toContainText('Service analyst · Customer operations');
  await invitedRow.getByRole('button', { name: 'Suspend' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=User\+suspended$/);
  invitedRow = page.getByRole('row').filter({ hasText: inviteEmail });
  await expect(invitedRow).toContainText('Suspended');
  await invitedRow.getByRole('button', { name: 'Restore' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=User\+restored$/);
  invitedRow = page.getByRole('row').filter({ hasText: inviteEmail });
  await invitedRow.getByRole('button', { name: 'Remove' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=User\+removed$/);
  await expect(page.getByRole('row').filter({ hasText: inviteEmail })).toHaveCount(0);
  await expect(group).toContainText('0 members');

  const serviceAccess = group.locator('.admin-product-access > div').filter({ hasText: 'Jira Service Management' });
  await serviceAccess.getByRole('button', { name: 'Grant access' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Product\+access\+granted$/);
  const groupWithAccess = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await expect(groupWithAccess.locator('.admin-product-access > div').filter({ hasText: 'Jira Service Management' })).toContainText('Access granted');

  const ana = groupWithAccess.getByRole('listitem').filter({ hasText: 'Ana Soursop' });
  await ana.getByRole('button', { name: 'Add' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Member\+added$/);
  const updatedGroup = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await expect(updatedGroup).toContainText('1 member');
  await expect(page.locator('.admin-audit')).toContainText('group.member.added');
  await accessible(page);

  await page.locator('[data-theme-toggle]').click();
  await accessible(page);
  await page.locator('[data-theme-toggle]').click();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);

  const memberContext = await browser.newContext();
  const member = await memberContext.newPage();
  try {
    await login(member, 'ana@zzira.dev', 'ana12345');
    await expect(member.getByRole('link', { name: 'Administration', exact: true })).toHaveCount(0);
    const denied = await member.goto('/admin');
    expect(denied?.status()).toBe(403);
  } finally {
    await memberContext.close();
  }

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/admin');
  const finalGroup = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await finalGroup.locator('.admin-product-access > div').filter({ hasText: 'Jira Service Management' }).getByRole('button', { name: 'Revoke access' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Product\+access\+revoked$/);
  await expect(page.locator('.admin-audit')).toContainText('role.revoked');
  await page.goto('/admin');
  const groupAfterRevoke = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await groupAfterRevoke.getByRole('listitem').filter({ hasText: 'Ana Soursop' }).getByRole('button', { name: 'Remove' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Member\+removed$/);
  const emptyGroup = page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) });
  await expect(emptyGroup).toContainText('0 members');
  await expect(page.locator('.admin-audit')).toContainText('group.member.removed');
  await emptyGroup.getByRole('button', { name: 'Delete group' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Group\+deleted$/);
  await expect(page.locator('.admin-group').filter({ has: page.getByRole('heading', { name: groupName }) })).toHaveCount(0);
  await expect(page.locator('.admin-audit')).toContainText('group.deleted');
  await page.getByLabel('Search audit log').fill(groupName);
  await page.getByLabel('Action', { exact: true }).selectOption('group.deleted');
  await page.getByRole('button', { name: 'Filter events' }).click();
  await expect(page).toHaveURL(/auditAction=group.deleted/);
  await expect(page.locator('.admin-audit')).toContainText('group.deleted');
  await expect(page.locator('.admin-audit')).not.toContainText('group.created');
  await page.getByRole('link', { name: 'Clear filters' }).click();
  await expect(page).toHaveURL('/admin');
  domainRow = page.getByRole('row').filter({ hasText: domainName });
  await domainRow.getByRole('button', { name: 'Remove' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Domain\+removed$/);
  await expect(page.getByRole('row').filter({ hasText: domainName })).toHaveCount(0);
  policy = page.locator('.admin-policy').filter({ hasText: policyName });
  await policy.getByRole('button', { name: 'Delete policy' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+deleted$/);
  await expect(page.locator('.admin-policy').filter({ hasText: policyName })).toHaveCount(0);

  await page.getByLabel('Show banner').uncheck();
  await page.getByRole('button', { name: 'Save announcement' }).click();
  await expect(page).toHaveURL(/saved=Announcement\+banner\+saved/);
  await page.getByLabel('Hours per day').fill('8');
  await page.getByLabel('Days per week').fill('5');
  await page.getByLabel('Display format', { exact: true }).selectOption('pretty');
  await page.getByLabel('Default unit').selectOption('minute');
  await page.getByRole('button', { name: 'Save time tracking' }).click();
  await page.locator('form[action="/admin/jira-configuration/columns"]').locator('label').filter({ hasText: 'Priority' }).getByRole('checkbox').check();
  await page.getByRole('button', { name: 'Save default columns' }).click();
  await page.getByText('Advanced application properties', { exact: true }).click();
  const resetTitleProperty = page.locator('form').filter({ hasText: 'jira.title' });
  await resetTitleProperty.getByRole('textbox').fill('Jira');
  await resetTitleProperty.getByRole('button', { name: 'Save' }).click();
});
