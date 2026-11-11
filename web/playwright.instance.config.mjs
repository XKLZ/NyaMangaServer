import { defineConfig } from "@playwright/test";
import path from "node:path";
process.env.PLAYWRIGHT_BROWSERS_PATH ??= path.resolve(
  import.meta.dirname,
  "../.runtime/cache/playwright",
);
export default defineConfig({
  testDir: "./tests/e2e",
  testMatch: [
    "instance.spec.mjs",
    "instance-errors.spec.mjs",
    "instance-cancel.spec.mjs",
    "instance-auth.spec.mjs",
  ],
  fullyParallel: false,
  workers: 1,
  retries: 0,
  outputDir: "../.runtime/tests/results/playwright-instance",
  reporter: "list",
  use: {
    browserName: "chromium",
    actionTimeout: 10000,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "desktop",
      use: {
        baseURL: "http://127.0.0.1:18081",
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: "mobile",
      use: {
        baseURL: "http://127.0.0.1:18082",
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
        colorScheme: "dark",
      },
    },
  ],
});
