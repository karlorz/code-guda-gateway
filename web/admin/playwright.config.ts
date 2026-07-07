import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  use: {
    baseURL: process.env.ADMIN_E2E_BASE_URL ?? 'http://127.0.0.1:8080',
    trace: 'on-first-retry',
  },
  webServer: process.env.ADMIN_E2E_BASE_URL
    ? undefined
    : {
        command: '../../guda-gateway',
        cwd: '../..',
        url: 'http://127.0.0.1:8080/healthz',
        reuseExistingServer: true,
        timeout: 30_000,
      },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
