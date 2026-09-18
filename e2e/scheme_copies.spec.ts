import { test, expect, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('an administrator copies a scheme before changing it', async ({ page }) => {
  const stamp = Date.now().toString(36);
  await login(page);

  // A permission scheme of its own, with one grant, then a copy of it.
  await page.goto('/settings/permission-schemes');
  const schemeName = `Delivery ${stamp}`;
  const create = page.getByRole('region', { name: 'Create a scheme' });
  await create.getByLabel('Scheme name').fill(schemeName);
  await create.getByLabel('Description').fill('Who may do what while we try a change');
  await create.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Permission scheme created.');

  let card = page.locator('.permission-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  const roleGrant = card.locator('form').filter({ has: page.getByRole('button', { name: 'Grant to role' }) });
  await roleGrant.getByLabel('Grant permission').selectOption('BROWSE_PROJECTS');
  await roleGrant.getByLabel('To project role').selectOption('10001');
  await roleGrant.getByRole('button', { name: 'Grant to role' }).click();
  await expect(page.getByRole('status')).toContainText('Permission grant added.');

  card = page.locator('.permission-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  await card.getByRole('button', { name: `Copy scheme ${schemeName}` }).click();
  await expect(page.getByRole('status')).toContainText(`Copy of ${schemeName} created.`);

  // The copy carries the grant, and belongs to no project.
  const copy = page.locator('.permission-scheme-card').filter({
    has: page.getByRole('heading', { name: `Copy of ${schemeName}`, exact: true }),
  });
  await expect(copy).toContainText('Browse projects');
  await expect(copy).toContainText('0 assigned projects');

  // Copying again takes the next name.
  card = page.locator('.permission-scheme-card').filter({ has: page.getByRole('heading', { name: schemeName, exact: true }) });
  await card.getByRole('button', { name: `Copy scheme ${schemeName}` }).click();
  await expect(page.getByRole('status')).toContainText(`Copy 2 of ${schemeName} created.`);

  // A screen copies with its tabs and their fields.
  await page.goto('/settings/screens');
  const screenName = `Triage ${stamp}`;
  const createScreen = page.locator('form').filter({ has: page.getByRole('button', { name: 'Create screen' }) }).first();
  await createScreen.getByLabel('Screen name').fill(screenName);
  await createScreen.getByRole('button', { name: 'Create screen' }).click();
  let screenCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: screenName, exact: true }) });
  await screenCard.locator('.screen-tab-card').first().getByLabel(/Add a field/).selectOption('priority');
  await screenCard.locator('.screen-tab-card').first().getByRole('button', { name: 'Add field' }).click();
  await expect(page.getByRole('status')).toContainText('Field added');

  screenCard = page.locator('.screen-card').filter({ has: page.getByRole('heading', { name: screenName, exact: true }) });
  await screenCard.getByRole('button', { name: `Copy screen ${screenName}` }).click();
  await expect(page.getByRole('status')).toContainText(`Copy of ${screenName} created.`);
  const screenCopy = page.locator('.screen-card').filter({
    has: page.getByRole('heading', { name: `Copy of ${screenName}`, exact: true }),
  });
  await expect(screenCopy).toContainText('priority');
});
