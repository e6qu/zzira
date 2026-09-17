import { test, expect, Page } from '@playwright/test';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('an administrator manages work types, priorities and resolutions', async ({ page }) => {
  const stamp = Date.now().toString(36);
  await login(page);

  // Work types: create one, rename it, put it in a new scheme, then delete it.
  await page.goto('/settings/work-types');
  await expect(page.getByRole('heading', { name: 'Work types', level: 1 })).toBeVisible();
  const createType = page.getByRole('region', { name: 'Create work type' });
  await createType.getByLabel('Name').fill(`Spike ${stamp}`);
  await createType.getByLabel('Description').fill('A time-boxed investigation.');
  await createType.getByLabel('Kind').selectOption('standard');
  await createType.getByRole('button', { name: 'Create work type' }).click();
  await expect(page.getByRole('status')).toContainText(`Spike ${stamp} created.`);

  let card = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: `Spike ${stamp}`, exact: true }) });
  const typeID = await card.getAttribute('data-work-type-id');
  card = page.locator(`.metadata-card[data-work-type-id="${typeID}"]`);
  await card.getByLabel('Name', { exact: true }).fill(`Research ${stamp}`);
  await card.getByRole('button', { name: 'Save work type' }).click();
  await expect(page.getByRole('status')).toContainText(`Research ${stamp} saved.`);

  const scheme = page.getByRole('region', { name: 'Work type schemes' });
  await scheme.getByLabel('Scheme name').fill(`Delivery types ${stamp}`);
  await scheme.getByRole('checkbox', { name: `Research ${stamp}` }).check();
  await scheme.getByRole('checkbox', { name: 'Task', exact: true }).check();
  await scheme.getByLabel('Default work type').selectOption({ label: 'Task' });
  await scheme.getByRole('button', { name: 'Create scheme' }).click();
  await expect(page.getByRole('status')).toContainText('Work type scheme created.');
  await expect(page.locator('.metadata-scheme-card').filter({ hasText: `Delivery types ${stamp}` })).toBeVisible();

  card = page.locator(`.metadata-card[data-work-type-id="${typeID}"]`);
  await card.locator('summary', { hasText: `Delete Research ${stamp}` }).click();
  await card.getByRole('button', { name: 'Delete work type' }).click();
  await expect(page.getByRole('status')).toContainText('Work type deleted.');
  await expect(page.locator(`.metadata-card[data-work-type-id="${typeID}"]`)).toHaveCount(0);

  // Priorities: create one, make it the default, and move it to the top.
  await page.goto('/settings/priorities');
  await expect(page.getByRole('heading', { name: 'Priorities', level: 1 })).toBeVisible();
  const createPriority = page.getByRole('region', { name: 'Create priority' });
  await createPriority.getByLabel('Name').fill(`Now ${stamp}`);
  await createPriority.getByLabel('Status colour').fill('#ff5630');
  await createPriority.getByRole('button', { name: 'Create priority' }).click();
  await expect(page.getByRole('status')).toContainText(`Now ${stamp} created.`);

  const priorityCard = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: `Now ${stamp}`, exact: true }) });
  await priorityCard.getByRole('button', { name: `Move to top Now ${stamp}` }).click();
  await expect(page.getByRole('status')).toContainText('Priority moved.');
  await expect(page.locator('.metadata-card').first().getByRole('heading', { level: 2 })).toHaveText(`Now ${stamp}`);
  await page.locator('.metadata-card').first().getByRole('button', { name: `Make default Now ${stamp}` }).click();
  await expect(page.getByRole('status')).toContainText('Default priority set.');
  await expect(page.locator('.metadata-card').first().getByText('Default', { exact: true })).toBeVisible();

  // Resolutions: create one and delete it, moving its work to another.
  await page.goto('/settings/resolutions');
  await expect(page.getByRole('heading', { name: 'Resolutions', level: 1 })).toBeVisible();
  const createResolution = page.getByRole('region', { name: 'Create resolution' });
  await createResolution.getByLabel('Name').fill(`Superseded ${stamp}`);
  await createResolution.getByLabel('Description').fill('Replaced by other work.');
  await createResolution.getByRole('button', { name: 'Create resolution' }).click();
  await expect(page.getByRole('status')).toContainText(`Superseded ${stamp} created.`);

  const resolutionCard = page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: `Superseded ${stamp}`, exact: true }) });
  await resolutionCard.locator('summary', { hasText: `Delete Superseded ${stamp}` }).click();
  await resolutionCard.getByRole('button', { name: 'Delete resolution' }).click();
  await expect(page.getByRole('status')).toContainText('Resolution deletion started');
});
