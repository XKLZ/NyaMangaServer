import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";
// @rc-component/util 1.14.0 omits the CSP option for its temporary scrollbar
// measurement style. Patch that one call at build time, without modifying
// node_modules or weakening the server's style-src-elem policy. Fail closed
// when the locked source changes so dependency upgrades must review this shim.
function scrollbarNonce() {
  const version = JSON.parse(
    readFileSync(
      new URL(
        "./node_modules/@rc-component/util/package.json",
        import.meta.url,
      ),
      "utf8",
    ),
  ).version;
  if (version !== "1.14.0")
    throw new Error(
      "Review scrollbar CSP compatibility before upgrading @rc-component/util",
    );
  const portalVersion = JSON.parse(
    readFileSync(
      new URL(
        "./node_modules/@rc-component/portal/package.json",
        import.meta.url,
      ),
      "utf8",
    ),
  ).version;
  if (portalVersion !== "2.2.1")
    throw new Error(
      "Review portal CSP compatibility before upgrading @rc-component/portal",
    );
  return {
    name: "nyamanga-scrollbar-csp-nonce",
    enforce: "pre",
    transform(source, id) {
      const module = id.replaceAll("\\", "/");
      const scrollbar = module.endsWith(
        "/@rc-component/util/es/getScrollBarSize.js",
      );
      const portal = module.endsWith(
        "/@rc-component/portal/es/useScrollLocker.js",
      );
      if (!scrollbar && !portal) return null;
      const needle = scrollbar ? "}`, randomId);" : "}`, id);";
      if (source.split(needle).length !== 2)
        throw new Error(
          "Scrollbar CSP call changed; review compatibility transform",
        );
      return {
        code: source.replace(
          needle,
          `${scrollbar ? "}` , randomId" : "}` , id"}, { csp: { nonce: document.querySelector('meta[name="csp-nonce"]')?.content } });`,
        ),
        map: null,
      };
    },
  };
}
export default defineConfig({
  cacheDir: "../.runtime/cache/vite-dev",
  plugins: [scrollbarNonce(), react()],
  optimizeDeps: {
    exclude: ["@rc-component/util", "@rc-component/portal"],
    // Excluded ESM modules still import these CommonJS dependencies in dev mode.
    include: ["react-is", "is-mobile"],
  },
  build: { outDir: "../internal/webassets/dist", emptyOutDir: true },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: false,
      },
    },
  },
});
