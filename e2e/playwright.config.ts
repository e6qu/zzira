import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 120_000,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  use: {
    baseURL: process.env.ZZIRA_URL || 'http://localhost:8080',
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
  webServer: {
    command: 'node fake-hydra.mjs',
    url: 'http://127.0.0.1:8100/healthz',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
});
