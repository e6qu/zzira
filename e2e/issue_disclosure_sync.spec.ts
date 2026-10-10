import { expect, test, Page } from '@playwright/test';
import { apiAuthHeader } from './auth';

async function openIssue(page: Page, request: any) {
  const created = await request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, summary: `Panel recovery ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const key = (await created.json()).key;
  // Control when a replica render arrives, without racing the real worker.
  await page.addInitScript(() => {
    (window as any).Worker = class {
      constructor() { (window as any).issueWorker = this; }
      postMessage() {}
      terminate() {}
    };
  });
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password', { exact: true }).fill('demo1234');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page).toHaveURL('/');
  await page.goto(`/browse/${key}`);
  return key;
}

test('replica refresh preserves panel identity, closed state and keyboard focus', async ({ page, request }) => {
  const key = await openIssue(page, request);
  // The incoming view has the server defaults, before anybody opens a panel.
  const html = await page.locator('#issue-root').evaluate(root => {
    const next = root.cloneNode(true) as HTMLElement;
    next.dataset.panelRefresh = 'complete';
    return next.outerHTML;
  });
  await page.route(`**/browse/${key}`, route => route.fulfill({ contentType: 'text/html', body: html }));
  await page.getByText('Add an update', { exact: true }).click();
  const linkSummary = page.getByText('Link work item', { exact: true });
  await linkSummary.click();
  await expect(linkSummary).toBeFocused();
  await page.evaluate(() => {
    const worker = (window as any).issueWorker;
    worker.onmessage({ data: { type: 'synced', seq: 1 } });
    worker.onmessage({ data: { type: 'html', issueId: document.body.dataset.currentIssue } });
  });
  await expect(page.locator('#issue-root')).toHaveAttribute('data-panel-refresh', 'complete');
  await expect(page.locator('.linked-work details')).toHaveAttribute('open', '');
  // These two panels share a CSS class but must retain separate states.
  await expect(page.locator('.issue-forms details')).not.toHaveAttribute('open');
  await expect(page.locator('.activity-composer')).not.toHaveAttribute('open');
  await expect(linkSummary).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.locator('#link-type')).toBeFocused();
  await page.locator('#link-issue').fill(key);
});

test('saving another field preserves open panels and their unsent values', async ({ page, request }) => {
  const key = await openIssue(page, request);
  await page.getByText('Add an update', { exact: true }).click();
  await page.getByText('Link work item', { exact: true }).click();
  await page.locator('#link-issue').fill(key);
  await page.locator('.issue-forms summary').click();
  await page.locator('#form-template').fill('onboarding');
  const vote = page.locator(`form[action="/issues/${key}/vote"] button`);
  await vote.click();
  await expect(vote).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#link-issue')).toBeVisible();
  await expect(page.locator('#link-issue')).toHaveValue(key);
  await expect(page.locator('#form-template')).toBeVisible();
  await expect(page.locator('#form-template')).toHaveValue('onboarding');
  await expect(page.locator('.activity-composer')).not.toHaveAttribute('open');
});
