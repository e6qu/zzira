import { expect, test } from '@playwright/test';

async function login(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
}

test('site administrators configure project permissions and delegate project administration', async ({ page, browser }) => {
  await login(page, 'demo@zzira.dev', 'demo1234');
  const stamp = Date.now().toString(36);
  const schemeName = `Governed delivery ${stamp}`;

  await page.goto('/settings/permission-schemes');
  await expect(page.getByRole('heading', { name: 'Permission schemes', level: 1 })).toBeVisible();
  const create = page.getByRole('region', { name: 'Create a scheme' });
  await create.getByLabel('Scheme name').fill(schemeName);
  await create.getByLabel('Description').fill('Restricts delivery administration to named owners');
  await create.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Permission scheme created.');

  let card = page.locator('.permission-scheme-card').filter({
    has: page.getByRole('heading', { name: schemeName, exact: true }),
  });
  const schemeID = await card.getAttribute('data-scheme-id');
  expect(schemeID).toMatch(/^\d+$/);
  await card.getByLabel('Description', { exact: true }).fill('Release work is visible to members and administered by Ana');
  await card.getByRole('button', { name: 'Save details' }).click();
  await expect(page.getByRole('status')).toContainText('Permission scheme updated.');

  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  const roleGrant = card.locator('form').filter({ has: page.getByRole('button', { name: 'Grant to role' }) });
  await roleGrant.getByLabel('Grant permission').selectOption('BROWSE_PROJECTS');
  await roleGrant.getByLabel('To project role').selectOption('10001');
  await roleGrant.getByRole('button', { name: 'Grant to role' }).click();
  await expect(page.getByRole('status')).toContainText('Permission grant added.');

  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  const personGrant = card.locator('form').filter({ has: page.getByRole('button', { name: 'Grant to person' }) });
  await personGrant.getByLabel('Grant permission').selectOption('ADMINISTER_PROJECTS');
  await personGrant.getByLabel('To person').selectOption({ label: 'Ana Soursop' });
  await personGrant.getByRole('button', { name: 'Grant to person' }).click();
  await expect(page.getByRole('status')).toContainText('Permission grant added.');

  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  await card.getByLabel('Assign a project').selectOption({ label: 'ZZIRA Demo (ZZ)' });
  await card.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Project permission scheme assigned.');
  await expect(page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`)).toContainText('ZZIRA Demo');

  const projectScheme = await page.request.get('/rest/api/3/project/ZZ/permissionscheme?expand=permissions');
  expect(projectScheme.status()).toBe(200);
  expect((await projectScheme.json()).name).toBe(schemeName);
  const myPermissions = await page.request.get('/rest/api/3/mypermissions?projectKey=ZZ&permissions=BROWSE_PROJECTS,ADMINISTER_PROJECTS');
  expect(myPermissions.status()).toBe(200);
  expect((await myPermissions.json()).permissions.ADMINISTER_PROJECTS.havePermission).toBe(true);

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  const member = await browser.newContext();
  const memberPage = await member.newPage();
  await login(memberPage, 'ana@zzira.dev', 'ana12345');
  await memberPage.goto('/projects/ZZ/settings/permissions');
  await expect(memberPage.getByRole('heading', { name: 'Permissions', level: 1 })).toBeVisible();
  await expect(memberPage.getByRole('heading', { name: schemeName, level: 2 })).toBeVisible();
  await expect(memberPage.locator('.nav-project-permissions')).toHaveAttribute('aria-current', 'page');
  await memberPage.setViewportSize({ width: 320, height: 740 });
  expect(await memberPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await member.close();

  await page.goto('/settings/permission-schemes');
  const defaultCard = page.locator('.permission-scheme-card[data-scheme-id="10000"]');
  await defaultCard.getByLabel('Assign a project').selectOption({ label: 'ZZIRA Demo (ZZ)' });
  await defaultCard.getByRole('button', { name: 'Assign', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Project permission scheme assigned.');
  card = page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`);
  await card.locator('summary').filter({ hasText: 'Delete scheme' }).click();
  await card.getByRole('button', { name: 'Delete scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Permission scheme deleted.');
  await expect(page.locator(`.permission-scheme-card[data-scheme-id="${schemeID}"]`)).toHaveCount(0);
});
