import { defineConfig } from "@playwright/test";
import path from "node:path";
process.env.PLAYWRIGHT_BROWSERS_PATH ??= path.resolve(
  import.meta.dirname,
  "..",
  ".runtime",
  "cache",
  "playwright",
);
export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  outputDir: "../.runtime/tests/results/playwright",
  reporter: "list",
  use: {
    actionTimeout: 10000,
    baseURL: "http://localhost:18081",
    browserName: "chromium",
    viewport: { width: 1280, height: 900 },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "desktop",
      use: {
        baseURL: "http://localhost:18081",
        viewport: { width: 1280, height: 900 },
      },
    },
    {
      name: "mobile",
      use: {
        baseURL: "http://localhost:18082",
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
        colorScheme: "dark",
      },
    },
  ],
  webServer: [18081, 18082].map((port) => ({
    command: `node ../scripts/test/e2e-server.mjs ${port}`,
    url: `http://localhost:${port}/api/v1/health/ready`,
    reuseExistingServer: false,
    timeout: 20000,
    gracefulShutdown: { signal: "SIGTERM", timeout: 10000 },
  })),
});
