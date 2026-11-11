import { mergeConfig } from "vite";
import base from "./vite.config.mjs";
// Dedicated anonymous preview; never proxies to the daily API.
export default mergeConfig(base, {
  cacheDir: "../.runtime/cache/vite-instance-ui",
  server: {
    port: 5174,
    strictPort: true,
    proxy: {
      "/api": { target: "http://127.0.0.1:18081", changeOrigin: false },
    },
  },
});
