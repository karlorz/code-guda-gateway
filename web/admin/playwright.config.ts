import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  use: {
    baseURL: process.env.ADMIN_E2E_BASE_URL ?? 'http://127.0.0.1:18081',
    trace: 'on-first-retry',
  },
  webServer: process.env.ADMIN_E2E_BASE_URL
    ? undefined
    : {
        command:
          'ADDR=127.0.0.1:18081 DB_PATH=/tmp/code-guda-gateway-admin-e2e.db GUDA_MASTER_KEY_PATH=/tmp/code-guda-gateway-admin-e2e-master.key GUDA_ADMIN_COOKIE_SECURE=false ./guda-gateway',
        cwd: '../..',
        url: 'http://127.0.0.1:18081/healthz',
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
