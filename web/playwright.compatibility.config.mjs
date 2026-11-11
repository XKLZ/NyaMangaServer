import { defineConfig } from "@playwright/test";
import base from "./playwright.instance.config.mjs";
export default defineConfig({
  ...base,
  testMatch: ["compatibility.spec.mjs"],
  outputDir: "../.runtime/tests/results/playwright-compatibility",
});
