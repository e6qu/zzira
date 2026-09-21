import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  return 'Basic ' + Buffer.from(`${DEMO.email}:${tokens[DEMO.email]}`).toString('base64');
}

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.fill('#login-email', email);
  await page.fill('#login-password', password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// An engineering manager decides which deployments their delivery metrics
// count. Production on every pipeline is only the default.
test('a project chooses which environments and pipelines its DORA metrics count', async ({ page, browser }) => {
  const auth = { Authorization: apiAuthHeader(), 'Content-Type': 'application/json' };
  const stamp = Date.now();
  const key = `DM${stamp.toString(36).toUpperCase().slice(-8)}`;
  const me = await (await page.request.get('/rest/api/3/myself', { headers: auth })).json();
  expect((await page.request.post('/rest/api/3/project', {
    headers: auth,
    data: { key, name: `Delivery mapping ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  })).status()).toBe(201);
  const issue = await (await page.request.post('/rest/api/3/issue', {
    headers: auth, data: { fields: { project: { key }, summary: `Mapping ${stamp}`, issuetype: { name: 'Task' } } },
  })).json();

  // One deployment, to staging, from one pipeline.
  const pipelineID = `staging-${stamp}`;
  const lastUpdated = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
  expect((await page.request.post('/rest/deployments/0.1/bulk', {
    headers: auth,
    data: {
      properties: { accountId: 'dora-mapping-e2e' },
      deployments: [{
        deploymentSequenceNumber: stamp, updateSequenceNumber: stamp, displayName: 'Staging rollout',
        url: 'https://deploy.example/staging', description: 'Staging deployment', lastUpdated,
        state: 'successful', issueKeys: [issue.key],
        pipeline: { id: pipelineID, displayName: 'Staging pipeline', url: 'https://ci.example/staging' },
        environment: { id: 'staging', displayName: 'Staging', type: 'staging' },
      }],
    },
  })).status()).toBe(202);

  await login(page, DEMO.email, DEMO.password);
  await page.goto(`/projects/${key}/reports/dora`);
  const mapping = page.locator('#dora-mapping');
  await expect(mapping).toContainText('production');
  await expect(mapping).toContainText('That is the default');
  const deployments = page.getByRole('region', { name: 'DORA summary' }).locator('.dora-metric').first().locator('strong');
  await expect(deployments).toHaveText('0');
  await expect(page.locator('.dora-detail-grid')).not.toContainText('Staging rollout');

  // Counting staging brings the deployment in.
  await mapping.locator('input[name="environment"][value="staging"]').check();
  await mapping.getByRole('button', { name: 'Save mapping' }).click();
  await expect(page.getByRole('status')).toContainText('Delivery mapping saved');
  await expect(page.locator('#dora-mapping')).toContainText('staging');
  await expect(page.getByRole('region', { name: 'DORA summary' }).locator('.dora-metric').first().locator('strong')).toHaveText('1');
  await expect(page.locator('.dora-detail-grid')).toContainText('Staging rollout');
  await accessible(page);

  // Naming the pipeline that deployed keeps it; the mapping offers only the
  // pipelines that have deployed work in this project.
  const pipelines = page.locator('#dora-mapping input[name="pipeline"]');
  await expect(pipelines).toHaveCount(1);
  await pipelines.check();
  await page.locator('#dora-mapping').getByRole('button', { name: 'Save mapping' }).click();
  await expect(page.getByRole('status')).toContainText('Delivery mapping saved');
  await expect(page.locator('#dora-mapping')).toContainText('1 chosen pipeline');
  await expect(page.getByRole('region', { name: 'DORA summary' }).locator('.dora-metric').first().locator('strong')).toHaveText('1');

  // A mapping that counts nothing is refused, and says so.
  for (const box of await page.locator('#dora-mapping input[name="environment"]').all()) {
    if (await box.isChecked()) {
      await box.uncheck();
    }
  }
  await page.locator('#dora-mapping').getByRole('button', { name: 'Save mapping' }).click();
  await expect(page.getByRole('alert')).toContainText('choose at least one environment');
  await expect(page.getByRole('region', { name: 'DORA summary' }).locator('.dora-metric').first().locator('strong')).toHaveText('1');

  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // A reader who does not administer the project sees what counts, and no
  // control to change it.
  const reader = await browser.newContext();
  const readerPage = await reader.newPage();
  await login(readerPage, 'ana@zzira.dev', 'ana12345');
  await readerPage.goto(`/projects/${key}/reports/dora`);
  await expect(readerPage.locator('#dora-mapping')).toContainText('A project administrator chooses what counts');
  await expect(readerPage.locator('#dora-mapping form')).toHaveCount(0);
  expect((await readerPage.request.post(`/projects/${key}/reports/dora/mapping`, {
    headers: { Origin: new URL(readerPage.url()).origin, 'Content-Type': 'application/x-www-form-urlencoded' },
    form: { environment: 'production' },
  })).status()).toBe(403);
  await reader.close();

  expect((await page.request.delete(`/rest/api/3/project/${key}?enableUndo=false`, { headers: auth })).status()).toBe(204);
});
