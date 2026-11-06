import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: 'dashboard_live.spec.js',
  timeout: 30000,
  workers: 1,
  use: {
    headless: true,
    channel: 'chrome',
    viewport: { width: 1100, height: 560 },
  },
});
