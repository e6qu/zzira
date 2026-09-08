import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('saved filter owner manages access, columns, favorites, and ownership', async ({ page, request }) => {
  const marker = Date.now();
  const name = `Release gate ${marker}`;
  const response = await request.post('/rest/api/3/filter', {
    headers: { Authorization: apiAuthHeader() },
    data: {
      name,
      description: 'Ready for the release review',
      jql: 'project = ZZ AND status != Done',
      sharePermissions: [],
      editPermissions: [],
    },
  });
  expect(response.status()).toBe(200);
  const filterID = (await response.json()).id;

  await login(page);
  await page.goto('/filters');
  let card = page.locator(`#filter-${filterID}`);
  await expect(card.getByRole('heading', { name })).toBeVisible();
  await expect(card.getByText('Owner only')).toHaveCount(2);
  await expect(card.locator('.filter-query-strip')).toContainText('project = ZZ AND status != Done');

  await card.getByRole('button', { name: `Add ${name} to favorites` }).click();
  await expect(page.getByRole('status')).toContainText('Filter added to favorites.');
  card = page.locator(`#filter-${filterID}`);
  await expect(card.getByRole('button', { name: `Remove ${name} from favorites` })).toHaveAttribute('aria-pressed', 'true');

  await card.getByText('Edit details', { exact: true }).click();
  await card.getByLabel('Description').fill('Release review and evidence');
  await card.getByRole('button', { name: 'Save filter' }).click();
  await expect(page.getByRole('status')).toContainText('Filter updated.');
  card = page.locator(`#filter-${filterID}`);
  await expect(card).toContainText('Release review and evidence');

  await card.getByText('Columns', { exact: true }).click();
  await card.getByLabel('Key', { exact: true }).check();
  await card.getByLabel('Summary', { exact: true }).check();
  await card.getByLabel('Status', { exact: true }).check();
  await card.getByRole('button', { name: 'Save columns' }).click();
  await expect(page.getByRole('status')).toContainText('Filter columns updated.');
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Columns', { exact: true }).click();
  await expect(card.getByLabel('Summary', { exact: true })).toBeChecked();

  await card.getByText('Manage access', { exact: true }).click();
  await card.getByRole('button', { name: 'Share with everyone signed in' }).click();
  await expect(page.getByRole('status')).toContainText('Filter access added.');
  card = page.locator(`#filter-${filterID}`);
  await expect(card.locator('.filter-access-lane.view')).toContainText('Everyone signed in');

  await card.getByText('Transfer ownership', { exact: true }).click();
  await card.getByLabel('New owner').selectOption({ label: 'Ana Soursop' });
  await card.getByRole('button', { name: 'Transfer filter' }).click();
  await expect(page.getByRole('status')).toContainText('Filter owner changed.');
  card = page.locator(`#filter-${filterID}`);
  await expect(card.locator('.filter-owner-line')).toContainText('Ana Soursop');
  await expect(card.getByText('Delete', { exact: true })).toHaveCount(0);

  await card.getByText('Transfer ownership', { exact: true }).click();
  await card.getByLabel('New owner').selectOption({ label: 'Demo User' });
  await card.getByRole('button', { name: 'Transfer filter' }).click();
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Delete', { exact: true }).click();
  await card.getByRole('button', { name: 'Delete filter' }).click();
  await expect(page.getByRole('status')).toContainText('Filter deleted.');
  await expect(page.locator(`#filter-${filterID}`)).toHaveCount(0);
});
