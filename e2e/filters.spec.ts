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
  // The column choices also offer a Description column, so name the field.
  await card.locator(`#filter-description-${filterID}`).fill('Release review and evidence');
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

  await card.getByText('Email results', { exact: true }).click();
  await card.locator(`#filter-schedule-${filterID}`).selectOption('0 8 * * *');
  await card.getByRole('button', { name: 'Schedule email' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email scheduled.');
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await expect(card).toContainText('Every day at 08:00 in UTC');
  await card.getByRole('button', { name: 'Remove schedule' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email schedule removed.');

  // A schedule of the owner's own, read in the zone they name.
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await card.locator(`#filter-cron-${filterID}`).fill('0 0 9 ? * MON-FRI');
  await card.locator(`#filter-zone-${filterID}`).fill('Europe/Bucharest');
  await card.getByRole('button', { name: 'Schedule email' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email scheduled.');
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await expect(card).toContainText('0 0 9 ? * MON-FRI in Europe/Bucharest');
  await card.getByRole('button', { name: 'Remove schedule' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email schedule removed.');

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

// Jira lets anyone who can see a filter subscribe to it, sends a group's
// members the result when the subscription names one, and leaves an empty
// result unsent unless it was asked for.
test('a viewer subscribes to a filter, and an administrator subscribes a group', async ({ page, browser, request }) => {
  const marker = Date.now();
  const name = `Shared review ${marker}`;
  const created = await request.post('/rest/api/3/filter', {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { name, jql: 'project = ZZ ORDER BY created DESC', sharePermissions: [{ type: 'authenticated' }] },
  });
  expect(created.status(), await created.text()).toBe(200);
  const filterID = (await created.json()).id as string;

  // A colleague who does not own the filter can still be emailed it.
  const colleague = await browser.newContext();
  const ana = await colleague.newPage();
  await ana.goto('/login');
  await ana.fill('input[name=email]', 'ana@zzira.dev');
  await ana.fill('input[name=password]', 'ana12345');
  await ana.click('button[type=submit]');
  await ana.goto('/filters');
  let card = ana.locator(`#filter-${filterID}`);
  await expect(card).toContainText(name);
  await card.getByText('Email results', { exact: true }).click();
  await card.locator(`#filter-schedule-${filterID}`).selectOption('0 8 * * *');
  await card.getByRole('button', { name: 'Schedule email' }).click();
  await expect(ana.getByRole('status')).toContainText('Filter email scheduled.');
  // A subscriber who owns nothing can still stop their own email.
  card = ana.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await expect(card).toContainText('Every day at 08:00 in UTC');
  // A group is a site administrator's to subscribe, so the viewer is not
  // offered one.
  await expect(card.locator(`#filter-group-${filterID}`)).toHaveCount(0);
  await card.getByRole('button', { name: 'Remove schedule' }).click();
  await expect(ana.getByRole('status')).toContainText('Filter email schedule removed.');
  await colleague.close();

  // The owner, who administers the site, can send it to a group instead.
  const groupName = `filter-watchers-${marker}`;
  const group = await request.post('/rest/api/3/group', {
    headers: { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' },
    data: { name: groupName },
  });
  expect(group.status(), await group.text()).toBe(201);
  await login(page);
  await page.goto('/filters');
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await card.locator(`#filter-schedule-${filterID}`).selectOption('0 8 * * *');
  await card.locator(`#filter-group-${filterID}`).selectOption({ label: groupName });
  await card.getByLabel('Email even when nothing matches').check();
  await card.getByRole('button', { name: 'Schedule email' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email scheduled.');
  card = page.locator(`#filter-${filterID}`);
  await card.getByText('Email results', { exact: true }).click();
  await expect(card).toContainText('Every day at 08:00 in UTC');

  await card.getByRole('button', { name: 'Remove schedule' }).click();
  await expect(page.getByRole('status')).toContainText('Filter email schedule removed.');
  expect((await request.delete(`/rest/api/3/filter/${filterID}`, { headers: { Authorization: apiAuthHeader() } })).status()).toBe(204);
  expect((await request.delete(`/rest/api/3/group?groupId=${(await group.json()).groupId}`, { headers: { Authorization: apiAuthHeader() } })).status()).toBeLessThan(400);
});
