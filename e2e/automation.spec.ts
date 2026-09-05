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

test('admin creates, runs, audits, disables and deletes scheduled automation', async ({ page, browser }) => {
  await login(page);
  await page.getByRole('link', { name: 'Automation', exact: true }).click();
  await expect(page).toHaveURL('/settings/automation');
  await expect(page.getByRole('heading', { name: 'Automation rules', exact: true })).toBeVisible();
  await accessible(page);

  await page.getByRole('link', { name: 'Create rule', exact: true }).click();
  const name = `E2E schedule ${Date.now()}`;
  const label = `scheduled-${Date.now()}`;
  await page.getByLabel('Rule name').fill(name);
  await page.getByLabel('Description').fill('Marks the first demo work item through the durable runner');
  await page.getByLabel('Run every').fill('60');
  await page.getByLabel('Timezone').fill('Europe/Bucharest');
  await page.getByLabel('JQL query').fill('key = ZZ-1');
  await page.getByRole('combobox', { name: 'Action', exact: true }).selectOption('jira.issue.add-label');
  await page.getByRole('combobox', { name: 'Value', exact: true }).first().fill(label);
  await accessible(page);
  await page.getByRole('button', { name: 'Create rule' }).click();
  await expect(page).toHaveURL(/\/settings\/automation\/[0-9a-f-]+$/);
  const ruleURL = page.url();
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();

  await page.getByRole('button', { name: 'Run now' }).click();
  await expect.poll(async () => {
    await page.goto(ruleURL);
    return await page.locator('.automation-audit tbody').innerText();
  }, { timeout: 15_000 }).toContain('SUCCESS');
  await expect(page.locator('.automation-audit tbody')).toContainText('1');
  const issueResponse = await page.request.get('/rest/api/3/issue/ZZ-1');
  expect(issueResponse.ok()).toBe(true);
  const issue = await issueResponse.json();
  expect(issue.fields.labels).toContain(label);

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
    await expect(member.getByRole('link', { name: 'Automation', exact: true })).toHaveCount(0);
    const denied = await member.goto('/settings/automation');
    expect(denied?.status()).toBe(403);
  } finally {
    await memberContext.close();
  }

  await page.goto(ruleURL);
  await page.getByRole('button', { name: 'Disable' }).click();
  await expect(page.getByText('DISABLED', { exact: true }).first()).toBeVisible();
  await page.locator('.automation-danger').getByText('Delete rule', { exact: true }).click();
  await page.getByRole('button', { name: 'Delete rule permanently' }).click();
  await expect(page).toHaveURL('/settings/automation');
  await expect(page.getByRole('link', { name, exact: true })).toHaveCount(0);
});
