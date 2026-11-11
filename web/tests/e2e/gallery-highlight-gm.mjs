// An explicit GM protocol/DOM simulator. This does NOT emulate extension isolation.
export async function installGM(page, source, service, options = {}) {
  await page.evaluate(
    ({ source, service, options }) => {
      const menus = new Map(),
        records = [],
        pending = [];
      let stored =
          options.stored === false
            ? null
            : typeof options.stored === "object"
              ? options.stored
              : {
                  protocol_version: 2,
                  service_url: service,
                  token: "ngh1_" + "a".repeat(43),
                },
        active = 0,
        maxActive = 0,
        aborts = 0,
        storageReads = 0,
        storageWrites = 0,
        releaseRead;
      const mode = {
        status: 200,
        lookup: 200,
        states: { 1: "collected", 2: "needs_review", 5: "needs_review" },
        hold: false,
        headers: "",
        bad: null,
        ...options,
      };
      const info = {
        scriptHandler: "Tampermonkey",
        sandboxMode: options.sandboxMode || "dom",
      };
      const alerts = [];
      const GM = {
        registerMenuCommand: (name, fn) => menus.set(name, fn),
        getValue: async () => {
          storageReads++;
          const value = stored && structuredClone(stored);
          if (options.holdRead)
            await new Promise((resolve) => {
              releaseRead = resolve;
            });
          return value;
        },
        setValue: async (_key, value) => {
          storageWrites++;
          stored = structuredClone(value);
        },
        deleteValue: async () => {
          storageWrites++;
          stored = null;
        },
        xmlHttpRequest: (input) => {
          if (
            input.anonymous !== true ||
            input.redirect !== "error" ||
            input.headers.Cookie ||
            input.headers.cookie ||
            !input.headers.Authorization?.startsWith("Bearer ngh1_") ||
            !input.url.startsWith(service + "/api/v1/gallery-highlight/")
          )
            throw Error("unsafe mock request");
          const ids = input.data ? JSON.parse(input.data).gallery_ids : null;
          records.push({
            url: input.url,
            method: input.method,
            ids,
            body: input.data ? JSON.parse(input.data) : null,
            anonymous: input.anonymous,
            redirect: input.redirect,
            cookie: false,
            time: Date.now(),
          });
          active++;
          maxActive = Math.max(maxActive, active);
          const item = { input, ids, ended: false };
          const captured = { ...mode, states: { ...mode.states } };
          function end() {
            if (!item.ended) {
              active--;
              item.ended = true;
            }
          }
          item.deliver = () => {
            end();
            const status = ids ? captured.lookup : captured.status;
            let response = ids
              ? {
                  protocol_version: 2,
                  provider: "ehentai",
                  query_version: "same-version",
                  items: ids.map((id) => ({
                    gallery_id: id,
                    state: captured.allCollected
                      ? "collected"
                      : captured.states[id] || "none",
                  })),
                }
              : {
                  protocol_version: 2,
                  provider: "ehentai",
                  max_batch_size: 200,
                  connection_expires_at: Object.hasOwn(captured, "expiry")
                    ? captured.expiry
                    : null,
                };
            if (captured.bad === "missing" && ids) response.items.pop();
            if (
              captured.bad === "duplicate" &&
              ids &&
              response.items.length > 1
            )
              response.items[1] = response.items[0];
            if (captured.bad === "version") response.protocol_version = 1;
            if (!ids && captured.bad === "expiry-missing")
              delete response.connection_expires_at;
            input.onload({
              status,
              response,
              finalUrl: captured.redirect
                ? "https://foreign.example.test/"
                : input.url,
              responseHeaders: captured.headers,
            });
          };
          if (mode.hold && ids) pending.push(item);
          else queueMicrotask(item.deliver);
          return {
            abort() {
              end();
              aborts++;
              input.onabort?.();
            },
          };
        },
      };
      window.fetch = () => {
        throw Error("page fetch forbidden in GM fixture");
      };
      window.gmHarness = {
        menu: (name) => menus.get(name)?.(),
        names: () => [...menus.keys()],
        update: (value) => Object.assign(mode, value),
        late: () => pending.splice(0).forEach((item) => item.deliver()),
        releaseRead: () => releaseRead?.(),
        stats: () => ({
          records: structuredClone(records),
          active,
          maxActive,
          aborts,
          storageReads,
          storageWrites,
          configured: !!stored,
          storedProtocol: stored?.protocol_version,
        }),
        hidden: (value) => {
          Object.defineProperty(document, "hidden", {
            configurable: true,
            get: () => value,
          });
          document.dispatchEvent(new Event("visibilitychange"));
        },
        diagnostics: () => alerts.at(-1),
      };
      // Passing the API as function arguments models GM callbacks only. The actual
      // DOM isolated world/manager grant requires a separate manual validation.
      new Function("GM", "GM_info", "alert", source)(GM, info, (text) =>
        alerts.push(text),
      );
    },
    { source, service, options },
  );
}
export function galleryFixture(count = 3, theme = "light") {
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>body{font:14px/1.5 system-ui;background:${theme === "dark" ? "#1f1f1f" : "#faf7ee"};color:${theme === "dark" ? "#ddd" : "#333"};padding:20px}table{width:100%;border-spacing:8px}td{padding:12px}.itg.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:12px}.gl1t,.gl1e{padding:12px}a{color:inherit}.glink{font-weight:600}h1{font-size:20px}</style></head><body><h1>匿名画廊列表</h1><nav><a href="https://e-hentai.org/g/99/navigation"><span class="glink">非画廊容器链接</span></a></nav><table class="itg gltc"><tbody>${Array.from({ length: count }, (_, i) => `<tr data-card="${i + 1}"><td><a href="https://e-hentai.org/g/${i + 1}/private${i + 1}/"><span class="glink">匿名作品 ${i + 1} 日本語とLong title</span></a></td><td>本页示例</td></tr>`).join("")}</tbody></table><div class="itg tiles"><div class="gl1t" data-card="duplicate"><a href="https://exhentai.org/g/1/private1/"><span class="glink">重复画廊 · 缩略图布局</span></a></div><div class="gl1e" data-card="extended"><a href="https://e-hentai.org/g/5/private5/"><span class="glink">扩展布局 · 无图片</span></a></div></div></body></html>`;
}
