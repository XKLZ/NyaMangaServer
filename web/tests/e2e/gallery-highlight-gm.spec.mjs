import { test, expect } from "@playwright/test";
import { readFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { buildGalleryScript } from "../../src/shared/galleryHighlightFlow.ts";
import { installGM, galleryFixture } from "./gallery-highlight-gm.mjs";
const output = path.resolve(
    import.meta.dirname,
    "../../../.runtime/tests/gallery-highlight-v2-ui",
  ),
  template = await readFile(
    new URL("../../userscripts/gallery-highlight.user.js", import.meta.url),
    "utf8",
  );
const artifact = process.env.NYAMANGA_GM_SCRIPT_ARTIFACT;
if (
  artifact &&
  !path.resolve(artifact).startsWith(path.join(output, "downloads") + path.sep)
)
  throw Error("Anonymous script artifact required");
const source = artifact
  ? await readFile(artifact, "utf8")
  : buildGalleryScript(template, "https://nas.example.test:8443");
const service = JSON.parse(
  /const SERVICE_ORIGIN = ("[^\n]+?");/u.exec(source)[1],
);
async function open(page, html = galleryFixture()) {
  // All navigation and subresources, including E/EX URLs, are intercepted.
  await page.route("**/*", (route) =>
    route.fulfill({ contentType: "text/html; charset=utf-8", body: html }),
  );
  await page.goto("https://e-hentai.org/");
  await page.clock.install();
}
const tick = async (page, ms = 250) => {
  await page.clock.runFor(ms);
};
const stats = (page) => page.evaluate(() => window.gmHarness.stats());
const menu = (page, name) =>
  page.evaluate((n) => window.gmHarness.menu(n), name);
const update = (page, value) =>
  page.evaluate((v) => window.gmHarness.update(v), value);
async function configure(page, answers) {
  let count = 0;
  const respond = (dialog) => {
    if (dialog.type() !== "prompt") return dialog.dismiss();
    const answer = answers[count++];
    return answer === null ? dialog.dismiss() : dialog.accept(answer);
  };
  page.on("dialog", respond);
  try {
    await menu(page, "配置服务器");
  } finally {
    page.off("dialog", respond);
  }
  return count;
}

test("下载脚本的GM模拟：分布局、本页ID、更新与未知状态、交叉主题", async ({
  page,
}, info) => {
  test.setTimeout(120000);
  await open(page);
  await installGM(page, source, service);
  await tick(page);
  await expect(page.locator('[data-card="1"] .glink')).toHaveText(
    "匿名作品 1 日本語とLong title",
  );
  await expect(
    page.locator('[data-card="1"] [data-ngh-owned="badge"]'),
  ).toHaveText("已收录");
  await expect(
    page.locator('[data-card="2"] [data-ngh-owned="badge"]'),
  ).toHaveText("记录待检查");
  await expect(
    page.locator('[data-card="3"] [data-ngh-owned="badge"]'),
  ).toHaveCount(0);
  await expect(
    page.locator('[data-card="duplicate"] [data-ngh-owned="badge"]'),
  ).toHaveText("已收录");
  await expect(
    page.locator('[data-card="extended"] [data-ngh-owned="badge"]'),
  ).toHaveText("记录待检查");
  let state = await stats(page);
  expect(state.records[0].url).toBe(
    service + "/api/v1/gallery-highlight/status",
  );
  expect(state.records.filter((r) => !r.ids)).toHaveLength(1);
  expect(new Set(state.records.flatMap((r) => r.ids || []))).toEqual(
    new Set(["1", "2", "3", "5"]),
  );
  expect(JSON.stringify(state.records)).not.toContain("private");
  const initial = state.records.length;
  await tick(page, 1000);
  expect((await stats(page)).records.length).toBe(initial); // Own DOM changes do not loop.
  await update(page, {
    states: { 1: "none", 2: "collected", 3: "needs_review", 5: "none" },
  });
  await menu(page, "刷新本页");
  await tick(page);
  await expect(
    page.locator('[data-card="1"] [data-ngh-owned="badge"]'),
  ).toHaveCount(0);
  await expect(
    page.locator('[data-card="2"] [data-ngh-owned="badge"]'),
  ).toHaveText("已收录");
  await page.evaluate(() => {
    const row = document.createElement("tr");
    row.dataset.card = "6";
    row.innerHTML =
      '<td><a href="https://exhentai.org/g/6/private6/"><span class="glink">动态追加</span></a></td>';
    document.querySelector("tbody").append(row);
  });
  await update(page, {
    states: { 2: "collected", 3: "needs_review", 6: "collected" },
  });
  await tick(page);
  await expect(
    page.locator('[data-card="6"] [data-ngh-owned="badge"]'),
  ).toHaveText("已收录");
  const beforeBurst = (await stats(page)).records.length;
  await page.evaluate(() => {
    const row = document.createElement("tr");
    row.innerHTML =
      '<td><a href="https://e-hentai.org/g/7/new7/"><span class="glink">新增7</span></a></td>';
    document.querySelector("tbody").append(row);
  });
  await tick(page, 100);
  await page.evaluate(() => {
    const row = document.createElement("tr");
    row.innerHTML =
      '<td><a href="https://e-hentai.org/g/8/new8/"><span class="glink">新增8</span></a></td>';
    document.querySelector("tbody").append(row);
  });
  await tick(page, 110);
  expect((await stats(page)).records.length).toBe(beforeBurst + 1);
  // Opposite site and system themes: only the small badge owns a background.
  await mkdir(path.join(output, "screenshots", "final"), { recursive: true });
  for (const [site, system] of [
    ["light", "dark"],
    ["dark", "light"],
  ]) {
    await page.emulateMedia({ colorScheme: system });
    await page.evaluate((theme) => {
      document.body.style.backgroundColor =
        theme === "dark" ? "rgb(31, 31, 31)" : "rgb(250, 247, 238)";
      document.body.style.color =
        theme === "dark" ? "rgb(221, 221, 221)" : "rgb(51, 51, 51)";
    }, site);
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
      });
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBe(width);
      const badge = page.locator('[data-card="2"] [data-ngh-owned="badge"]');
      await expect(badge).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
      await expect(page.locator('[data-card="2"]')).toHaveCSS(
        "background-color",
        "rgba(0, 0, 0, 0)",
      );
      for (const id of ["2", "3"]) {
        const contrast = await page
          .locator(`[data-card="${id}"] [data-ngh-owned="badge"]`)
          .evaluate((element) => {
            const style = getComputedStyle(element);
            const luminance = (color) =>
              color
                .match(/[\d.]+/gu)
                .slice(0, 3)
                .map(Number)
                .map((v) => v / 255)
                .map((v) =>
                  v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4,
                )
                .reduce(
                  (sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i],
                  0,
                );
            const foreground = luminance(style.color),
              background = luminance(style.backgroundColor);
            return (
              (Math.max(foreground, background) + 0.05) /
              (Math.min(foreground, background) + 0.05)
            );
          });
        expect(contrast).toBeGreaterThanOrEqual(4.5);
      }
      await page.screenshot({
        path: path.join(
          output,
          "screenshots",
          "final",
          `${info.project.name}-gm-site-${site}-system-${system}-${width}.png`,
        ),
        animations: "disabled",
      });
    }
  }
  // Failure is unknown, not a successful none cached for 60 seconds.
  await update(page, { lookup: 503 });
  await menu(page, "刷新本页");
  await tick(page);
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  await menu(page, "连接状态");
  expect(await page.evaluate(() => window.gmHarness.diagnostics())).toContain(
    "未知",
  );
  await update(page, { lookup: 200 });
  await tick(page, 15100);
  await expect(
    page.locator('[data-card="2"] [data-ngh-owned="badge"]'),
  ).toHaveText("已收录");
  await update(page, { status: 401 });
  await menu(page, "刷新本页");
  await tick(page);
  const auth = (await stats(page)).records.length;
  await tick(page, 120000);
  expect((await stats(page)).records.length).toBe(auth);
  await menu(page, "连接状态");
  expect(await page.evaluate(() => window.gmHarness.diagnostics())).toContain(
    "授权无效",
  );
  await update(page, { status: 200, lookup: 403 });
  await menu(page, "刷新本页");
  await tick(page);
  const disabled = (await stats(page)).records.length;
  await tick(page, 120000);
  expect((await stats(page)).records.length).toBe(disabled);
  await update(page, { lookup: 429, headers: "Retry-After: 120" });
  await menu(page, "刷新本页");
  await tick(page);
  const limited = (await stats(page)).records.length;
  await update(page, { lookup: 200, headers: "" });
  await tick(page, 119000);
  expect((await stats(page)).records.length).toBe(limited);
  await tick(page, 1100);
  expect((await stats(page)).records.length).toBeGreaterThan(limited);
  await update(page, { bad: "missing" });
  await menu(page, "刷新本页");
  await tick(page);
  const invalid = (await stats(page)).records.length;
  await tick(page, 70000);
  expect((await stats(page)).records.length).toBe(invalid);
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  for (const corrupt of [
    { bad: "duplicate" },
    { bad: "version" },
    { bad: null, redirect: true },
  ]) {
    await update(page, corrupt);
    await menu(page, "刷新本页");
    await tick(page);
    const count = (await stats(page)).records.length;
    await tick(page, 65000);
    expect((await stats(page)).records.length).toBe(count);
    await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  }
  await update(page, { redirect: false });
  await update(page, { bad: null, hold: true });
  await menu(page, "刷新本页");
  await tick(page);
  expect((await stats(page)).active).toBeGreaterThan(0);
  await page.evaluate(() => window.gmHarness.hidden(true));
  expect((await stats(page)).active).toBe(0);
  const hidden = (await stats(page)).records.length;
  await tick(page, 120000);
  expect((await stats(page)).records.length).toBe(hidden);
  await update(page, { hold: false });
  await page.evaluate(() => window.gmHarness.hidden(false));
  await tick(page);
  await expect(
    page.locator('[data-card="2"] [data-ngh-owned="badge"]'),
  ).toHaveText("已收录");
  // Reconfigure through the browser native prompt while old lookup callbacks are held.
  await update(page, { hold: true });
  await menu(page, "刷新本页");
  await tick(page);
  await update(page, { hold: false, states: {} });
  await configure(page, [service, "ngh1_" + "b".repeat(43)]);
  await tick(page);
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  await page.evaluate(() => window.gmHarness.late());
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  await page.evaluate(() => {
    history.pushState({}, "", "/g/5/private5/");
    document.body.innerHTML =
      '<section id="gd2"><h1 id="gn">匿名详情</h1></section>';
  });
  await update(page, { states: { 5: "needs_review" } });
  await tick(page);
  await expect(page.locator('#gd2 [data-ngh-owned="badge"]')).toHaveText(
    "记录待检查",
  );
  await update(page, { hold: true });
  await menu(page, "刷新本页");
  await tick(page);
  await page.evaluate(() => {
    history.pushState({}, "", "/g/7/private7/");
    document.querySelector("#gn").textContent = "匿名新详情";
    window.gmHarness.late();
  });
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  await update(page, { hold: false });
  await tick(page);
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  await menu(page, "清除配置与标记");
  await tick(page);
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
  expect((await stats(page)).configured).toBe(false);
  expect(
    await page.evaluate(
      () => JSON.stringify({ ...localStorage }) + document.body.textContent,
    ),
  ).not.toContain("ngh1_");
  state = await stats(page);
  expect(state.maxActive).toBeLessThanOrEqual(2);
  expect(
    state.records.every(
      (r) =>
        r.anonymous &&
        r.redirect === "error" &&
        !r.cookie &&
        (!r.ids || r.ids.length <= 200),
    ),
  ).toBe(true);
});
test("GM模拟：批量上限、缓存过期、并发与主动超时；不支持的沙箱拒绝秘密", async ({
  page,
}) => {
  test.setTimeout(120000);
  await open(page, galleryFixture(1200));
  await page.locator(".tiles").evaluate((element) => element.remove());
  await installGM(page, source, service, { allCollected: true });
  await tick(page);
  let s = await stats(page);
  expect(s.maxActive).toBeLessThanOrEqual(2);
  expect(s.records.flatMap((r) => r.ids || []).length).toBeLessThanOrEqual(
    1000,
  );
  await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(1000);
  await tick(page, 60500);
  s = await stats(page);
  for (const row of s.records)
    expect(row.ids?.length || 0).toBeLessThanOrEqual(200);
  expect(
    await page.locator('[data-ngh-owned="badge"]').count(),
  ).toBeLessThanOrEqual(1000);
  await page.reload();
  await installGM(page, source, service, { allCollected: true, hold: true });
  await tick(page);
  await tick(page, 10100);
  expect((await stats(page)).aborts).toBeGreaterThan(0);
  // A fresh document with missing/raw/js sandbox information exposes status only.
  await page.reload();
  await installGM(page, source, service, { sandboxMode: "raw" });
  expect(await page.evaluate(() => window.gmHarness.names())).toEqual([
    "连接状态",
  ]);
  expect((await stats(page)).storageReads).toBe(0);
  expect((await stats(page)).records).toHaveLength(0);
});

test("协议2配置两步原子提交、旧存储拒绝及长期／有限期严格校验", async ({
  page,
}) => {
  await open(page);
  await installGM(page, source, service);
  await tick(page);
  const baseline = await stats(page);
  for (const answers of [
    [null],
    [service, null],
    ["https://wrong.example.test"],
    ["javascript:alert(1)"],
    [service, "invalid-code"],
  ]) {
    expect(await configure(page, answers)).toBe(answers.length);
    await tick(page);
    const current = await stats(page);
    expect(current.records.length).toBe(baseline.records.length);
    expect(current.storageWrites).toBe(0);
    expect(current.storedProtocol).toBe(2);
    await expect(
      page.locator('[data-card="1"] [data-ngh-owned="badge"]'),
    ).toHaveText("已收录");
  }
  await configure(page, [service, "ngh1_" + "b".repeat(43)]);
  await tick(page);
  expect((await stats(page)).storageWrites).toBe(1);
  await page.reload();
  await installGM(page, source, service, {
    stored: {
      protocol_version: 1,
      service_url: service,
      token: "ngh1_" + "a".repeat(43),
    },
  });
  await tick(page);
  expect((await stats(page)).records).toHaveLength(0);
  expect((await stats(page)).storageWrites).toBe(0);
  expect((await stats(page)).storedProtocol).toBe(1);
  await menu(page, "连接状态");
  expect(await page.evaluate(() => window.gmHarness.diagnostics())).toContain(
    "检测到旧版配置",
  );
  // Both nullable indefinite grants and legacy finite grants retain a 60s cache.
  for (const expiry of [null, Math.floor(Date.now() / 1000) + 86400]) {
    await page.reload();
    await installGM(page, source, service, { expiry });
    await tick(page);
    await expect(
      page.locator('[data-card="1"] [data-ngh-owned="badge"]'),
    ).toHaveText("已收录");
    const count = (await stats(page)).records.length;
    await tick(page, 59000);
    expect((await stats(page)).records.length).toBe(count);
    await tick(page, 1500);
    expect((await stats(page)).records.length).toBeGreaterThan(count);
  }
  for (const options of [
    { bad: "expiry-missing" },
    { expiry: "null" },
    { expiry: 0 },
    { expiry: -1 },
    { expiry: 1.5 },
    { expiry: Number.MAX_SAFE_INTEGER },
    { expiry: NaN },
    { expiry: Infinity },
    { bad: "version" },
  ]) {
    await page.reload();
    await installGM(page, source, service, options);
    await tick(page);
    const count = (await stats(page)).records.length;
    await expect(page.locator('[data-ngh-owned="badge"]')).toHaveCount(0);
    await tick(page, 65000);
    expect((await stats(page)).records.length).toBe(count);
    await menu(page, "连接状态");
    expect(await page.evaluate(() => window.gmHarness.diagnostics())).toContain(
      "请更新脚本或服务",
    );
  }
});

test("初始化GM存储迟到不覆盖成功配置或清除状态", async ({ page }) => {
  await open(page);
  for (const operation of ["configure", "clear"]) {
    await page.reload();
    await installGM(page, source, service, { holdRead: true });
    expect((await stats(page)).storageReads).toBe(1);
    if (operation === "configure") {
      await configure(page, [service, "ngh1_" + "b".repeat(43)]);
      await tick(page);
      await update(page, { lookup: 401 });
      await menu(page, "刷新本页");
      await tick(page);
      await menu(page, "连接状态");
      expect(
        await page.evaluate(() => window.gmHarness.diagnostics()),
      ).toContain("授权无效");
    } else await menu(page, "清除配置与标记");
    const count = (await stats(page)).records.length;
    await page.evaluate(() => window.gmHarness.releaseRead());
    await tick(page);
    await menu(page, "连接状态");
    expect(await page.evaluate(() => window.gmHarness.diagnostics())).toContain(
      operation === "configure" ? "授权无效" : "已清除",
    );
    expect((await stats(page)).records.length).toBe(count);
    expect((await stats(page)).storageWrites).toBe(1);
    expect((await stats(page)).configured).toBe(operation === "configure");
  }
});
