import { expect, test } from '@playwright/test';
import axe from 'axe-core';

async function accessible(page: import('@playwright/test').Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

test('admin installs and manages a scoped host-rendered app', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');

  const suffix = String(Date.now());
  const appKey = `journey.${suffix}`;
  const appName = `Journey app ${suffix}`;
  const moduleTitle = `Release companion ${suffix}`;
  const issuePanelTitle = `Issue risk ${suffix}`;
  const gadgetTitle = `App health ${suffix}`;
  const bylineTitle = `Page review ${suffix}`;
  const descriptor = {
    key: appKey,
    name: appName,
    baseUrl: `https://apps.example.test/${suffix}`,
    version: '1.0.0',
    scopes: ['read:jira-work', 'read:confluence-content', 'read:app-storage', 'write:app-storage', 'manage:webhooks'],
    modules: [
      { key: 'release-companion', type: 'jira:globalPage', location: 'jira.navigation', title: moduleTitle, body: 'Release readiness and incident context from the installed app.' },
      { key: 'issue-risk', type: 'jira:issuePanel', location: 'jira.issue.view', title: issuePanelTitle, body: 'No cross-service release risk detected.' },
      { key: 'app-health', type: 'jira:dashboardGadget', location: 'jira.dashboard', title: gadgetTitle, body: 'All app checks are healthy.' },
      { key: 'page-review', type: 'confluence:contentBylineItem', location: 'confluence.content.byline', title: bylineTitle, body: 'Reviewed by the installed app.' },
    ],
    lifecycle: { installed: '/lifecycle/installed', disabled: '/lifecycle/disabled', enabled: '/lifecycle/enabled', uninstalled: '/lifecycle/uninstalled' },
    webhooks: [{ key: 'issue-events', url: '/webhooks/issues', events: ['jira:issue_created', 'jira:issue_updated'], jql: 'project = ZZ' }],
    scheduledTriggers: [{ key: 'hourly-sync', url: '/scheduled/hourly', interval: 'hour' }],
  };

  await page.goto('/admin#admin-apps');
  await page.locator('#admin-apps summary', { hasText: 'Install app' }).click();
  await page.getByLabel('App descriptor').fill(JSON.stringify(descriptor, null, 2));
  await page.getByLabel('Shared signing secret').fill('browser-test-shared-secret-123456');
  await page.getByRole('button', { name: 'Install app' }).click();

  let app = page.locator('.admin-app', { hasText: appName });
  await expect(app).toContainText('active');
  await expect(app).toContainText('read:jira-work');
  await expect(app).toContainText('app_principal_');
  await expect(app).toContainText('/lifecycle/installed');
  await expect(app).toContainText('issue-events');
  await expect(app).toContainText('hourly-sync');
  await accessible(page);
  await expect(page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle })).toBeVisible();
  await page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle }).click();
  await expect(page.getByRole('heading', { name: moduleTitle, level: 1 })).toBeVisible();
  await expect(page.getByText('Release readiness and incident context from the installed app.')).toBeVisible();
  await accessible(page);

  await page.goto('/browse/ZZ-1');
  const issuePanel = page.locator('.app-context-module', { has: page.getByRole('heading', { name: issuePanelTitle, level: 2 }) });
  await expect(issuePanel).toBeVisible();
  await expect(issuePanel).toContainText('No cross-service release risk detected.');

  await page.goto('/dashboards');
  await page.getByLabel('Dashboard name', { exact: true }).fill(`App dashboard ${suffix}`);
  await page.getByRole('button', { name: 'Create dashboard', exact: true }).click();
  await page.getByRole('button', { name: `Add ${gadgetTitle}`, exact: true }).click();
  await expect(page.locator('.app-dashboard-module')).toContainText('All app checks are healthy.');

  await page.goto('/wiki');
  await page.getByRole('link', { name: 'Browse pages', exact: true }).first().click();
  const wikiPage = page.locator('a[href*="/wiki/spaces/"][href*="/pages/"]:not([href$="/new"])').first();
  await expect(wikiPage).toBeVisible();
  await wikiPage.click();
  const byline = page.locator('.app-byline > span', { hasText: bylineTitle });
  await expect(byline).toBeVisible();
  await expect(byline).toContainText('Reviewed by the installed app.');
  await accessible(page);

  await page.goto('/admin#admin-apps');
  app = page.locator('.admin-app', { hasText: appName });
  await app.getByRole('button', { name: `Suspend ${appName}` }).click();
  app = page.locator('.admin-app', { hasText: appName });
  await expect(app).toContainText('suspended');
  await expect(page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle })).toHaveCount(0);

  await app.getByRole('button', { name: `Resume ${appName}` }).click();
  app = page.locator('.admin-app', { hasText: appName });
  await expect(app).toContainText('active');
  await expect(page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle })).toBeVisible();

  await app.getByRole('button', { name: `Uninstall ${appName}` }).click();
  app = page.locator('.admin-app', { hasText: appName });
  await expect(app).toContainText('uninstalled');
  await expect(page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle })).toHaveCount(0);
});
