import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',
  timeout: 60000, // Increase test timeout to 60s

  use: {
    baseURL: 'http://localhost:5556',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    // Slow down actions by 500ms when PWTEST_SLOW=1 (use yarn test:e2e:slow)
    ...(process.env.PWTEST_SLOW ? { slowMo: 500 } : {}),
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  webServer: {
    command: 'cd ../../.. && ./kcp ui --port 5556',
    url: 'http://localhost:5556',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000, // Give server 60s to start
  },
})
