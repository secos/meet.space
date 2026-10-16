import { defineConfig, devices } from '@playwright/test';

// Every spec runs twice: on the platform host and on the seeded group's
// custom domain (hikers.localhost), against the server `make e2e` starts.
const port = process.env.E2E_PORT ?? '8090';

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: true,
  retries: 0,
  reporter: [['list']],
  outputDir: './test-results',
  use: {
    // Map every *.localhost name to the loopback server, so nothing is
    // ever resolved through DNS.
    launchOptions: { args: [`--host-resolver-rules=MAP localhost 127.0.0.1, MAP *.localhost 127.0.0.1`] },
    trace: 'retain-on-failure',
  },
  // OPEN(Q22): which browser engines `make e2e` must run is an open owner
  // question; Chromium only until it is answered.
  projects: [
    { name: 'platform', use: { ...devices['Desktop Chrome'], baseURL: `http://localhost:${port}` } },
    { name: 'custom-domain', use: { ...devices['Desktop Chrome'], baseURL: `http://hikers.localhost:${port}` } },
  ],
});
