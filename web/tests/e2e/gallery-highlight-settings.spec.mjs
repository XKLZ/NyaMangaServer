import { test, expect, request as apiRequest } from "@playwright/test";
import { readFile, mkdir } from "node:fs/promises";
import path from "node:path";
import vm from "node:vm";
import { installViteSource } from "./vite-source.mjs";
import { enhancementFixture } from "./enhancement-fixture.mjs";
installViteSource(test);
test("协议2授权码、长期连接与独立脚本下载、撤销CAS", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(240000);
  const { get, write, headers } = await enhancementFixture(page, baseURL);
  expect((await get("/settings/service")).database_schema).toBe(22);
  const initial = await get("/settings/enhancements");
  expect(initial.features.local_gallery_highlight.enabled).toBe(false);
  const failures = [],
    reads = [],
    creates = [];
  page.on("pageerror", (e) => failures.push(e.message));
  page.on("request", (r) => {
    if (r.method() === "GET") reads.push(new URL(r.url()));
    else if (
      r.method() === "POST" &&
      r.url().endsWith("/settings/gallery-highlight/connections")
    )
      creates.push(r.postDataJSON());
  });
  const root = path.resolve(
    import.meta.dirname,
    "../../../.runtime/tests/gallery-highlight-v2-ui",
  );
  await mkdir(path.join(root, "screenshots"), { recursive: true });
  await mkdir(path.join(root, "screenshots", "final"), { recursive: true });
  await mkdir(path.join(root, "screenshots", "evidence"), { recursive: true });
  await mkdir(path.join(root, "downloads"), { recursive: true });
  async function screenshot(label, anchor) {
    for (const colorScheme of ["light", "dark"]) {
      await page.emulateMedia({ colorScheme });
      await expect(page.locator(".admin-shell")).toHaveClass(
        new RegExp(colorScheme),
      );
      for (const width of info.project.name === "mobile"
        ? [390]
        : [1280, 1440]) {
        await page.setViewportSize({
          width,
          height: info.project.name === "mobile" ? 844 : 900,
        });
        await expect
          .poll(() => page.evaluate(() => document.documentElement.scrollWidth))
          .toBe(width);
        if (anchor)
          await anchor.evaluate((element) =>
            window.scrollTo({
              top: Math.max(
                0,
                window.scrollY + element.getBoundingClientRect().top - 80,
              ),
            }),
          );
        else await page.evaluate(() => window.scrollTo({ top: 0 }));
        await page.screenshot({
          path: path.join(
            root,
            "screenshots",
            anchor ? "final" : "evidence",
            `${info.project.name}-${label}-${width}-${colorScheme}.png`,
          ),
          animations: "disabled",
          fullPage: !anchor,
          mask: [
            page.getByRole("textbox", { name: "一次性授权码", exact: true }),
          ],
        });
      }
    }
  }
  const settings = "**/api/v1/settings/enhancements",
    connections = "**/api/v1/settings/gallery-highlight/connections";
  await page.route(settings, async (route) => {
    const response = await route.fetch(),
      json = await response.json();
    delete json.features.local_gallery_highlight;
    await route.fulfill({ response, json });
  });
  await page.goto(
    "/admin/settings?section=enhancements&focus=local_gallery_highlight",
  );
  const row = page.getByRole("region", { name: "本地画廊高亮", exact: true }),
    toggle = row.getByRole("switch", { name: "本地画廊高亮开关", exact: true });
  await expect(toggle).toBeDisabled();
  expect(
    reads.some((u) =>
      u.pathname.includes("/settings/gallery-highlight/connections"),
    ),
  ).toBe(false);
  await page.unroute(settings);
  await page.getByRole("button", { name: "刷新增强设置", exact: true }).click();
  await expect(toggle).toBeEnabled();
  await expect(toggle).not.toBeChecked();
  const panel = row.getByRole("region", {
    name: "本地画廊高亮配置",
    exact: true,
  });
  await expect(panel).toBeVisible();
  await expect(panel.getByText("0条授权", { exact: true })).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "生成授权码", exact: true }),
  ).toBeDisabled();
  await screenshot("disabled");
  await expect(
    panel.getByRole("textbox", { name: "浏览器授权名称", exact: true }),
  ).toHaveCount(0);
  await expect(panel.getByRole("combobox")).toHaveCount(0);
  const serviceRoute = "**/api/v1/settings/service";
  await page.route(serviceRoute, async (route) => {
    const response = await route.fetch(),
      json = await response.json();
    json.database_schema = 21;
    await route.fulfill({ response, json });
  });
  await panel
    .getByRole("button", { name: "刷新授权列表", exact: true })
    .click();
  await expect(
    panel.getByText("当前服务尚未支持协议2，请升级服务后刷新。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "生成授权码", exact: true }),
  ).toBeDisabled();
  expect(creates).toHaveLength(0);
  await page.unroute(serviceRoute);
  await panel
    .getByRole("button", { name: "刷新授权列表", exact: true })
    .click();
  await expect(
    panel.getByText("当前服务尚未支持协议2，请升级服务后刷新。", {
      exact: true,
    }),
  ).toHaveCount(0);

  const address = panel.getByRole("textbox", {
    name: "NyaMangaServer地址",
    exact: true,
  });
  await expect(address).toHaveValue(baseURL);
  await address.fill("https://user:secret@nas.example.test/path");
  await panel.getByRole("button", { name: "确认地址", exact: true }).click();
  await expect(
    panel.getByText("仅支持无凭据、路径、查询或片段的HTTP(S)服务根地址", {
      exact: true,
    }),
  ).toBeVisible();
  await address.fill("http://192.168.1.3:19400");
  await panel.getByRole("button", { name: "确认地址", exact: true }).click();
  await expect(
    panel.getByText("远程HTTP会明文传输只读令牌，建议使用HTTPS。", {
      exact: true,
    }),
  ).toBeVisible();
  async function download(label) {
    const pending = page.waitForEvent("download");
    await panel
      .getByRole("button", { name: "下载浏览器脚本", exact: true })
      .click();
    const result = await pending;
    const file = path.join(
      root,
      "downloads",
      `${info.project.name}-${label}.user.js`,
    );
    await result.saveAs(file);
    const source = await readFile(file, "utf8");
    expect(() => new vm.Script(source)).not.toThrow();
    expect(source).not.toContain("__NYAMANGA");
    expect(source).not.toMatch(/@connect\s+\*/u);
    expect(source).not.toContain("@require");
    return source;
  }
  const nas = await download("nas");
  expect(nas).toMatch(/@connect\s+192\.168\.1\.3\n/u);
  expect(nas).toContain('"http://192.168.1.3:19400"');
  await address.fill(baseURL);
  await panel.getByRole("button", { name: "确认地址", exact: true }).click();
  await toggle.click();
  await expect(toggle).toBeChecked();
  await expect(toggle).toBeEnabled();
  expect(
    (await get("/settings/enhancements")).features.local_gallery_highlight
      .transition,
  ).toBeNull();
  await page.route(connections, (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 503,
          json: {
            error: {
              code: "gallery_highlight_unavailable",
              message: "anonymous failure",
            },
          },
        })
      : route.fallback(),
  );
  await panel.getByRole("button", { name: "生成授权码", exact: true }).click();
  await expect(panel.getByText(/生成结果未确认/)).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "生成授权码", exact: true }),
  ).toBeDisabled();
  expect(creates).toHaveLength(1);
  await page.unroute(connections);
  await panel
    .getByRole("button", { name: "已核对并撤销，允许重新生成", exact: true })
    .click();
  let releaseCreate, createStarted;
  const createGate = new Promise((resolve) => {
      releaseCreate = resolve;
    }),
    createBegin = new Promise((resolve) => {
      createStarted = resolve;
    });
  await page.route(connections, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    createStarted();
    await createGate;
    await route.continue();
  });
  await panel.getByRole("button", { name: "生成授权码", exact: true }).click();
  await createBegin;
  await expect(toggle).toBeDisabled();
  await expect(address).toBeDisabled();
  await expect(
    panel.getByRole("textbox", { name: "一次性授权码", exact: true }),
  ).toHaveCount(0);
  releaseCreate();
  const secret = panel.getByRole("textbox", {
    name: "一次性授权码",
    exact: true,
  });
  await expect(secret).toBeVisible();
  await expect(address).toBeEnabled();
  await page.unroute(connections);
  const token = await secret.inputValue();
  expect(token).toMatch(/^ngh1_[A-Za-z0-9_-]{43}$/u);
  const source = await download("local");
  expect(source).not.toContain(token);
  expect(source).toMatch(/@connect\s+localhost\n/u);
  expect(source).toMatch(/@sandbox\s+DOM\n/u);
  const bearer = await apiRequest.newContext();
  try {
    const status = await bearer.get(
      `${baseURL}/api/v1/gallery-highlight/status`,
      { headers: { Authorization: `Bearer ${token}` } },
    );
    expect(status.status()).toBe(200);
    const statusBody = await status.json();
    expect(statusBody.protocol_version).toBe(2);
    expect(statusBody.connection_expires_at).toBeNull();
    const lookup = await bearer.post(
      `${baseURL}/api/v1/gallery-highlight/lookup`,
      {
        headers: { Authorization: `Bearer ${token}` },
        data: { provider: "ehentai", gallery_ids: ["000987654", "987654"] },
      },
    );
    expect(lookup.status()).toBe(200);
    expect((await lookup.json()).items).toEqual([
      { gallery_id: "987654", state: "none" },
    ]);
    const management = await bearer.get(`${baseURL}/api/v1/settings/service`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(management.status()).toBe(401);
    await page.evaluate(() =>
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: {
          writeText: async () => {
            throw Error("anonymous denied");
          },
        },
      }),
    );
    await panel
      .getByRole("button", { name: "复制授权码", exact: true })
      .click();
    await expect(panel.getByRole("status")).toHaveText(
      "复制失败，请手动选择并复制下方授权码。",
    );
    expect(
      await page.evaluate(
        (t) =>
          JSON.stringify({ ...localStorage }).includes(t) ||
          location.href.includes(t) ||
          JSON.stringify(history.state).includes(t),
        token,
      ),
    ).toBe(false);
    await row
      .getByRole("button", { name: "收起本地画廊高亮配置", exact: true })
      .click();
    const close = page.getByRole("dialog", {
      name: "放弃尚未保存的高亮配置？",
      exact: true,
    });
    await expect(close).toBeVisible();
    await close.getByRole("button", { name: "继续配置", exact: true }).click();
    await expect(secret).toHaveValue(token);
    const before = page.url();
    if (info.project.name === "mobile") {
      await page
        .getByRole("combobox", { name: "设置分类", exact: true })
        .click();
      await page.getByRole("option", { name: "概览", exact: true }).click();
    } else
      await page
        .getByRole("navigation", { name: "设置分类" })
        .getByRole("button", { name: "概览", exact: true })
        .click();
    const guard = page.getByRole("dialog", {
      name: "有尚未保存的修改",
      exact: true,
    });
    await expect(guard).toBeVisible();
    await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
    expect(page.url()).toBe(before);
    await screenshot("one-time");
    await page.evaluate(
      (expected) =>
        Object.defineProperty(navigator, "clipboard", {
          configurable: true,
          value: {
            writeText: async (text) => {
              if (text !== expected) throw Error("anonymous incorrect copy");
            },
          },
        }),
      token,
    );
    await panel
      .getByRole("button", { name: "复制授权码", exact: true })
      .click();
    await expect(panel.getByRole("status")).toHaveText("授权码已复制");
    await panel
      .getByRole("button", { name: "已保存授权码，关闭", exact: true })
      .click();
    await expect(secret).toHaveCount(0);
    const current = (await get("/settings/gallery-highlight/connections"))
      .items[0];
    expect(current.name).toBe("授权-" + current.id.slice(0, 8));
    expect(current.expires_at).toBeNull();
    expect(current.state).toBe("active");
    expect(current.token).toBeUndefined();
    await write(
      `/settings/gallery-highlight/connections/${current.id}`,
      { expected_revision: current.revision },
      "DELETE",
    );
    await panel
      .getByRole("button", { name: `撤销授权${current.name}`, exact: true })
      .click();
    const revoke = page.getByRole("dialog", {
      name: `撤销授权${current.name}？`,
      exact: true,
    });
    await expect(revoke).toContainText("此授权将失效，不影响漫画资料。");
    await revoke
      .getByRole("button", { name: "撤销此授权", exact: true })
      .click();
    await expect(
      revoke.getByText("授权版本已变化，原选择保留，请明确重新读取。", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      revoke.getByRole("button", { name: "撤销此授权", exact: true }),
    ).toBeDisabled();
    await revoke
      .getByRole("button", { name: "重新读取授权状态", exact: true })
      .click();
    await expect(revoke).toContainText("当前：已撤销");
    await revoke
      .getByRole("button", { name: "按已读取版本撤销", exact: true })
      .click();
    await expect(revoke).not.toBeVisible();
    expect(
      (
        await bearer.get(`${baseURL}/api/v1/gallery-highlight/status`, {
          headers: { Authorization: `Bearer ${token}` },
        })
      ).status(),
    ).toBe(401);
    await page.route(connections, (route) =>
      route.request().method() === "POST"
        ? route.fulfill({
            status: 429,
            json: {
              error: {
                code: "gallery_connection_limit",
                message: "anonymous limit",
              },
            },
          })
        : route.fallback(),
    );
    await panel
      .getByRole("button", { name: "生成授权码", exact: true })
      .click();
    await expect(
      panel.getByText("已达20条有效授权，请先撤销不再使用的授权。", {
        exact: true,
      }),
    ).toBeVisible();
    await page.unroute(connections);
    let active;
    for (let i = 0; i < 11; i++)
      active = await write(
        "/settings/gallery-highlight/connections",
        {},
        "POST",
        201,
      );
    await panel
      .getByRole("button", { name: "刷新授权列表", exact: true })
      .click();
    await expect(panel.getByText("12条授权", { exact: true })).toBeVisible();
    await panel.locator(".ant-pagination-item-2").click();
    await expect(panel.locator(".gallery-connections > ul > li")).toHaveCount(
      2,
    );
    await expect(panel.getByText("已撤销", { exact: true })).toBeVisible();
    await screenshot("connections-limit-fault");
    await row
      .getByRole("button", { name: "收起本地画廊高亮配置", exact: true })
      .click();
    await row
      .getByRole("button", { name: "展开本地画廊高亮配置", exact: true })
      .click();
    await expect(panel.getByText("12条授权", { exact: true })).toBeVisible();
    await expect(
      panel.getByText("已达20条有效授权，请先撤销不再使用的授权。", {
        exact: true,
      }),
    ).toHaveCount(0);
    await screenshot("normal-setup", row);
    await screenshot(
      "normal-connections",
      panel.getByRole("region", { name: "浏览器授权列表", exact: true }),
    );
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await expect(
      panel.getByRole("button", { name: "生成授权码", exact: true }),
    ).toBeDisabled();
    await expect(
      panel.getByRole("button", { name: "下载浏览器脚本", exact: true }),
    ).toBeEnabled();
    expect(
      (
        await bearer.get(`${baseURL}/api/v1/gallery-highlight/status`, {
          headers: { Authorization: `Bearer ${active.token}` },
        })
      ).status(),
    ).toBe(403);
  } finally {
    await bearer.dispose();
  }
  expect(failures).toEqual([]);
  expect(creates.every((v) => v && Object.keys(v).length === 0)).toBe(true);
  expect(
    reads
      .filter((u) => u.pathname.includes("/settings/gallery-highlight"))
      .every(
        (u) => !["account_id", "library_id"].some((k) => u.searchParams.has(k)),
      ),
  ).toBe(true);
});
