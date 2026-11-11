export function installViteSource(test) {
  test.beforeEach(async ({ page, baseURL }) => routeViteSource(page, baseURL));
}
// Business requests always stay on the fresh anonymous API host.
export async function routeViteSource(page, baseURL) {
  const vite = process.env.NYAMANGA_E2E_VITE;
  if (!vite) return;
  if (!["http://127.0.0.1:5173", "http://127.0.0.1:5174"].includes(vite))
    throw new Error("unsupported Vite verification host");
  const origin = new URL(baseURL).origin;
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (url.origin !== origin || url.pathname.startsWith("/api/"))
      return route.continue();
    const response = await route.fetch({
      url: vite + url.pathname + url.search,
      maxRetries: 2,
      headers: Object.fromEntries(
        Object.entries(route.request().headers()).filter(
          ([name]) => name !== "cookie",
        ),
      ),
    });
    if (url.pathname === "/@vite/client") {
      // The anonymous API hosts do not provide Vite's websocket. Keep module
      // and stylesheet transforms, but do not forward their browser console.
      let body = await response.text();
      for (const statement of [
        "transport.connect(createHMRHandler(handleMessage));",
        "setupForwardConsoleHandler(transport, forwardConsole);",
      ]) {
        if (!body.includes(statement))
          throw new Error("Review Vite test adapter after dependency change");
        body = body.replace(statement, "");
      }
      await route.fulfill({ response, body });
    } else await route.fulfill({ response });
  });
}
