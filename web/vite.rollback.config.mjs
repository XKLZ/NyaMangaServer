// Explicit recovery only; standard/pilot startup never loads this configuration.
import { readFileSync } from "node:fs";
import base from "./vite.config.mjs";
const record = JSON.parse(
  readFileSync(
    new URL(
      "../.runtime/maintenance/promote-pilot/state.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
if (record.phase !== "rolled_back")
  throw new Error(
    "Rollback Vite requires a completed explicit instance rollback.",
  );
export default {
  ...base,
  cacheDir: "../.runtime/cache/vite-recommendation-pilot",
  server: {
    host: "127.0.0.1",
    port: 5174,
    strictPort: true,
    proxy: {
      "/api": { target: "http://127.0.0.1:8081", changeOrigin: false },
    },
  },
};
