import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('DIAG: worker boot pipeline', async ({ page }) => {
  const errors: string[] = [];
  const workers: string[] = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('worker', w => { workers.push(w.url()); w.on('close', () => errors.push('WORKER CLOSED: ' + w.url())); });
  page.on('response', r => {
    const u = r.url();
    if (u.includes('.wasm') || u.includes('worker.js') || u.includes('sqlite3'))
      if (r.status() >= 400) errors.push(`HTTP ${r.status()}: ${u}`);
  });

  await login(page);
  await expect
    .poll(async () => {
      const log = await page.evaluate(() => (window as any).__bannerLog ?? []);
      return log.join('|');
    }, { timeout: 45_000, intervals: [500, 1000, 2500, 5000] })
    .toContain('local sync ready');

  console.log('WORKERS:', JSON.stringify(workers));
  console.log('PAGE-ERRORS:', JSON.stringify(errors));
  expect(errors.filter(e => e.startsWith('HTTP 4') || e.startsWith('pageerror'))).toEqual([]);
});

test('DIAG: create dialog opens', async ({ page }) => {
  await login(page);
  const errors: string[] = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.click('text=Create');
  await page.waitForTimeout(3000);
  console.log('MODAL-HTML:', (await page.locator('#modal-root').innerHTML()).slice(0, 200));
  console.log('PAGE-ERRORS:', JSON.stringify(errors));
  await expect(page.locator('.modal input[name=summary]')).toBeVisible();
});
