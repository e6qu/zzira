import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

function apiAuthHeader(): string {
  const email = 'demo@zzira.dev';
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[email];
  return 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64');
}

async function login(page: Page, email = 'demo@zzira.dev', password = 'demo1234') {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
  await expect(page).not.toHaveURL(/\/login/);
}

async function accessible(page: Page) {
  // Axe counts controls under the sticky header as covered, so pages are
  // checked from the top rather than wherever an anchor scrolled them.
  await page.evaluate(() => window.scrollTo(0, 0));
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
  // The page carries a second create form for authentication policies, so an
  // access policy is filled in through its own.
  const accessPolicyForm = page.locator('#admin-access-policy-create');
  await accessPolicyForm.getByLabel('Policy name').fill(policyName);
  await accessPolicyForm.getByLabel('Policy type').selectOption('ip-allowlist');
  // The allowlist keeps the loopback addresses the browser runs from.
  await accessPolicyForm.getByLabel('Rule values').fill('192.0.2.0/24, 127.0.0.1, ::1');
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

  // The application title brands every page.
  const titleAuth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  expect((await page.request.put('/rest/api/3/application-properties/jira.title', { headers: titleAuth, data: { id: 'jira.title', value: 'Acme Delivery' } })).status()).toBe(200);
  await page.reload();
  await expect(page).toHaveTitle(/Acme Delivery/);
  // As in Jira, the logo link names the application and the title shows beside
  // the logo only once "Show application title" is on; the hero button colour
  // brands primary buttons.
  await expect(page.getByRole('link', { name: 'Acme Delivery home' })).toBeVisible();
  await expect(page.locator('.global-header .logo')).not.toContainText('Acme Delivery');
  const setProperty = async (id: string, value: string) => expect((await page.request.put(`/rest/api/3/application-properties/${id}`, { headers: titleAuth, data: { id, value } })).status()).toBe(200);
  await setProperty('jira.lf.logo.show.application.title', 'true');
  await setProperty('jira.lf.hero.button.base.bg.colour', '#206B4E');
  await page.reload();
  await expect(page.locator('.global-header .logo')).toContainText('Acme Delivery');
  const primaryButton = page.locator('.btn-primary').first();
  if (await primaryButton.count()) await expect(primaryButton).toHaveCSS('background-color', 'rgb(32, 107, 78)');
  await setProperty('jira.lf.logo.show.application.title', 'false');
  await setProperty('jira.lf.hero.button.base.bg.colour', '#3b7fc4');
  expect((await page.request.put('/rest/api/3/application-properties/jira.title', { headers: titleAuth, data: { id: 'jira.title', value: 'ZZIRA' } })).status()).toBe(200);
  await page.reload();
  await expect(page).toHaveTitle(/ZZIRA/);

  // Products run on plans; free plans cap their users.
  await page.getByLabel('Confluence plan').selectOption('premium');
  await page.getByRole('button', { name: 'Save plan for Confluence' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Product\+plan\+updated$/);
  await expect(page.getByLabel('Confluence plan')).toHaveValue('premium');
  await page.getByLabel('Confluence plan').selectOption('standard');
  await page.getByRole('button', { name: 'Save plan for Confluence' }).click();
  await expect(page.getByLabel('Confluence plan')).toHaveValue('standard');

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
  await resetTitleProperty.getByRole('textbox').fill('ZZIRA');
  await resetTitleProperty.getByRole('button', { name: 'Save' }).click();
});

test('a site administrator sees every filter email and stops one', async ({ page, request }) => {
  const marker = Date.now();
  const name = `Admin gate ${marker}`;
  const response = await request.post('/rest/api/3/filter', {
    headers: { Authorization: apiAuthHeader() },
    data: { name, description: 'Scheduled for the administration journey', jql: 'project = ZZ', sharePermissions: [], editPermissions: [] },
  });
  expect(response.status()).toBe(200);
  const filterID = (await response.json()).id;

  // The owner schedules the email their own way, on a cron in a named zone.
  await login(page);
  await page.goto('/filters');
  let card = page.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await card.locator(`#filter-cron-${filterID}`).fill('0 0 9 ? * MON-FRI');
  await card.locator(`#filter-zone-${filterID}`).fill('Europe/Bucharest');
  await card.getByRole('button', { name: 'Schedule email' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email scheduled.');

  // The administration page shows it, whoever scheduled it.
  await page.goto('/admin');
  const section = page.locator('#admin-filter-subscriptions');
  await expect(page.getByRole('heading', { name: 'Filter emails', level: 2 })).toBeVisible();
  await expect(section).toContainText(name);
  await expect(section).toContainText('0 0 9 ? * MON-FRI in Europe/Bucharest');
  await accessible(page);

  // Stopping it says so on the page, rather than looking like a dead button.
  await section.getByRole('button', { name: new RegExp(`^Stop the ${name} email`) }).click();
  await expect(page).toHaveURL(/\/admin\?saved=/);
  await expect(page.getByRole('status')).toContainText('Filter email stopped');
  await expect(page.locator('#admin-filter-subscriptions')).not.toContainText(name);

  // The filter itself is untouched; only its schedule went.
  await page.goto('/filters');
  card = page.locator(`#filter-${filterID}`);
  await expect(card.getByRole('heading', { name })).toBeVisible();
});

test('an administrator makes one person sign in through the identity provider', async ({ page, browser }) => {
  await login(page);
  await page.goto('/admin');
  await expect(page.getByRole('heading', { name: 'Authentication policies', level: 2 })).toBeVisible();

  // The policy is written first and covers nobody, so nothing changes yet.
  const policyName = `Contractors ${Date.now()}`;
  const create = page.locator('.admin-authentication-policy-create');
  await create.getByLabel('Policy name').fill(policyName);
  await create.getByLabel('Session duration in minutes').fill('30');
  await create.getByLabel('Single sign-on only').check();
  await create.getByLabel('Enable immediately').check();
  await create.getByRole('button', { name: 'Create authentication policy' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+created$/);
  let policy = page.locator('.admin-authentication-policy').filter({ hasText: policyName });
  await expect(policy).toContainText('enabled');
  await expect(policy).toContainText('0 members');
  await accessible(page);

  // Putting someone under it is what changes how they sign in.
  await policy.getByLabel('Add a person').selectOption({ label: 'Ana Soursop' });
  await policy.getByRole('button', { name: 'Add member' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+member\+added$/);
  policy = page.locator('.admin-authentication-policy').filter({ hasText: policyName });
  await expect(policy).toContainText('1 member');
  await expect(policy.locator('.admin-policy-members')).toContainText('Ana Soursop');

  // A policy with members still fits a narrow viewport.
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await accessible(page);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Her password is no longer a way in, and the page says why rather than
  // reading as a wrong password.
  const refused = await browser.newContext();
  const refusedPage = await refused.newPage();
  await refusedPage.goto('/login');
  await refusedPage.fill('#login-email', 'ana@zzira.dev');
  await refusedPage.fill('#login-password', 'ana12345');
  await refusedPage.click('button[type=submit]');
  await expect(refusedPage).toHaveURL(/\/login/);
  await expect(refusedPage.locator('body')).toContainText('Your organization signs this account in through its identity provider.');

  // The settings are editable in place, and the policy can be relaxed.
  await policy.getByRole('button', { name: 'Remove Ana Soursop' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+member\+removed$/);
  policy = page.locator('.admin-authentication-policy').filter({ hasText: policyName });
  await expect(policy).toContainText('0 members');
  const settings = policy.locator('.admin-policy-settings');
  await settings.getByLabel('Session duration in minutes').fill('120');
  await settings.getByLabel('Single sign-on only').uncheck();
  await settings.getByRole('button', { name: 'Save settings' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+settings\+saved$/);
  policy = page.locator('.admin-authentication-policy').filter({ hasText: policyName });
  await expect(policy.locator('.admin-policy-settings').getByLabel('Session duration in minutes')).toHaveValue('120');

  // Deleting it puts everyone back on the site's own sign-in.
  await policy.getByRole('button', { name: 'Delete policy' }).click();
  await expect(page).toHaveURL(/\/admin\?saved=Policy\+deleted$/);
  await expect(page.locator('.admin-authentication-policy').filter({ hasText: policyName })).toHaveCount(0);
  await refusedPage.goto('/login');
  await refusedPage.fill('#login-email', 'ana@zzira.dev');
  await refusedPage.fill('#login-password', 'ana12345');
  await refusedPage.click('button[type=submit]');
  await expect(refusedPage).not.toHaveURL(/\/login/);
  await refused.close();
});
