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

test('admin installs a standard Connect descriptor and opens its signed remote page', async ({ page }) => {
  await page.route('https://connect.example.test/**', async route => {
    const target = new URL(route.request().url());
    expect(target.searchParams.get('jwt')).toBeTruthy();
    expect(target.searchParams.get('xdm_e')).toBe('http://localhost:8080');
    expect(target.searchParams.get('xdm_c')).toMatch(/^zzira-/);
    if (target.pathname.endsWith('/remote-panel')) {
      expect(target.searchParams.get('issue.key')).toBe('ZZ-1');
      expect(target.searchParams.get('selected')).toBe('ZZ-1');
      await route.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body><main><h1>Remote issue risk</h1><p>Issue context received.</p></main></body></html>' });
      return;
    }
    if (target.pathname.endsWith('/remote-review')) {
      expect(target.searchParams.get('content.id')).toBeTruthy();
      expect(target.searchParams.get('content')).toBe(target.searchParams.get('content.id'));
      await route.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body><main><h1>Remote page review</h1><p>Content context received.</p></main></body></html>' });
      return;
    }
    if (target.pathname.endsWith('/remote-shortcut')) {
      await route.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body><main><h1>Remote team shortcut</h1><p>Web item context received.</p></main></body></html>' });
      return;
    }
    expect(target.pathname).toContain('/connect/base/remote-page');
    await route.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body><main><h1>Remote release intelligence</h1><p>Signed Connect context received.</p></main></body></html>' });
  });
  await page.goto('/login');
  await page.fill('#login-email', 'demo@zzira.dev');
  await page.fill('#login-password', 'demo1234');
  await page.click('button[type=submit]');

  const suffix = String(Date.now());
  const appName = `Connect journey ${suffix}`;
  const moduleTitle = `Remote releases ${suffix}`;
  const panelTitle = `Remote risk ${suffix}`;
  const bylineTitle = `Remote review ${suffix}`;
  const fieldName = `Remote risk score ${suffix}`;
  const shortcutTitle = `Remote shortcut ${suffix}`;
  const descriptor = {
    key: `connect.journey.${suffix}`,
    name: appName,
    baseUrl: 'https://connect.example.test/connect/base',
    authentication: { type: 'jwt' },
    scopes: ['READ'],
    modules: {
      generalPages: [{ key: 'remote-releases', url: '/remote-page?view=releases', name: { value: moduleTitle } }],
      webPanels: [{ key: 'remote-risk', url: '/remote-panel?selected={issue.key}', location: 'atl.jira.view.issue.right.context', name: { value: panelTitle } }],
      contentBylineItems: [{ key: 'remote-review', url: '/remote-review?content={content.id}', name: { value: bylineTitle } }],
      jiraIssueFields: [{ key: 'remote-risk-score', name: { value: fieldName }, description: { value: 'Risk supplied by the Connect app' }, type: 'number' }],
      webItems: [{ key: 'remote-shortcut', url: '/remote-shortcut', location: 'system.top.navigation.bar', name: { value: shortcutTitle } }],
    },
  };
  await page.goto('/admin#admin-apps');
  await page.locator('#admin-apps summary', { hasText: 'Install app' }).click();
  await page.getByLabel('App descriptor').fill(JSON.stringify(descriptor, null, 2));
  await page.getByLabel('Shared signing secret').fill('connect-browser-shared-secret-123456');
  await page.getByRole('button', { name: 'Install app' }).click();

  let app = page.locator('.admin-app', { hasText: appName });
  await expect(app).toContainText('connect descriptor');
  await expect(app).toContainText('/remote-page?view=releases');
  await expect(app).toContainText(fieldName);
  await expect(app).toContainText(`${descriptor.key}__remote-risk-score`);
  await page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle }).click();
  await expect(page.getByRole('heading', { name: moduleTitle, level: 1 })).toBeVisible();
  const remote = page.frameLocator('iframe.app-module-frame');
  await expect(remote.getByRole('heading', { name: 'Remote release intelligence' })).toBeVisible();
  await expect(remote.getByText('Signed Connect context received.')).toBeVisible();
  await accessible(page);
  await page.locator('#workspace-navigation').getByRole('link', { name: shortcutTitle }).click();
  await expect(page.frameLocator('iframe.app-module-frame').getByRole('heading', { name: 'Remote team shortcut' })).toBeVisible();

  await page.goto('/browse/ZZ-1');
  const issuePanel = page.locator('.app-context-module', { has: page.getByRole('heading', { name: panelTitle, level: 2 }) });
  await expect(issuePanel).toBeVisible();
  await expect(issuePanel.frameLocator('iframe').getByRole('heading', { name: 'Remote issue risk' })).toBeVisible();
  await page.locator('details.more-fields').click();
  const issueField = page.getByLabel(fieldName);
  await expect(issueField).toBeVisible();
  await issueField.fill('7');
  await page.getByRole('button', { name: `Save ${fieldName}` }).click();
  await expect(page.getByLabel(fieldName)).toHaveValue('7');

  await page.goto('/wiki');
  await page.getByRole('link', { name: 'Browse pages', exact: true }).first().click();
  const wikiPage = page.locator('a[href*="/wiki/spaces/"][href*="/pages/"]:not([href$="/new"])').first();
  await wikiPage.click();
  await page.getByRole('link', { name: bylineTitle }).click();
  await expect(page.getByRole('heading', { name: bylineTitle, level: 1 })).toBeVisible();
  await expect(page.frameLocator('iframe.app-module-frame').getByRole('heading', { name: 'Remote page review' })).toBeVisible();
  await accessible(page);

  await page.goto('/admin#admin-apps');
  app = page.locator('.admin-app', { hasText: appName });
  await app.getByRole('button', { name: `Uninstall ${appName}` }).click();
  await expect(page.locator('#workspace-navigation').getByRole('link', { name: moduleTitle })).toHaveCount(0);
});
