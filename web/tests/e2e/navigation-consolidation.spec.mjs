import { test, expect } from "@playwright/test";
import { readFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);
test("七项导航、维护范围、按需设置与退出草稿保护", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(240000);
  const fixture = JSON.parse(
    await readFile(
      path.resolve(
        import.meta.dirname,
        `../../../.runtime/tests/e2e-state-${new URL(baseURL).port}.json`,
      ),
      "utf8",
    ),
  );
  expect(
    fixture.run_directory.startsWith(
      path.resolve(import.meta.dirname, "../../../.runtime/tests/runs/e2e-"),
    ),
  ).toBe(true);
  const password = "Anonymous navigation password 2026",
    nextPassword = "Anonymous changed password 2026";
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: { setup_token: await readFile(fixture.token_file, "utf8"), password },
  });
  expect(setup.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await setup.json()).csrf_token,
  };
  const errors = [],
    reads = [],
    writes = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (!u.pathname.startsWith("/api/")) return;
    if (r.method() === "GET") reads.push(u);
    else writes.push({ url: u, body: r.postDataJSON() });
  });
  async function get(value) {
    const r = await page.request.get(`${baseURL}/api/v1${value}`);
    expect(r.status(), value).toBe(200);
    return r.json();
  }
  async function write(value, data, status = 200, method = "POST") {
    const r = await page.request.fetch(`${baseURL}/api/v1${value}`, {
      method,
      headers,
      data,
    });
    expect(r.status(), value).toBe(status);
    return r.json();
  }
  async function completed(id) {
    await expect
      .poll(async () => (await get(`/jobs/${id}`)).state)
      .toBe("completed");
  }
  async function openNav() {
    if (info.project.name === "mobile")
      await page
        .getByRole("button", { name: "打开管理导航", exact: true })
        .click();
  }
  async function nav(name) {
    await openNav();
    await page
      .getByRole("menuitem", { name, exact: true })
      .last()
      .getByRole("link", { name, exact: true })
      .click();
  }
  async function category(name) {
    if (info.project.name === "mobile") {
      await page
        .getByRole("combobox", { name: "设置分类", exact: true })
        .click();
      await page.getByRole("option", { name, exact: true }).click();
    } else
      await page
        .getByRole("navigation", { name: "设置分类" })
        .getByRole("button", { name, exact: true })
        .click();
  }
  async function exit() {
    await openNav();
    await page
      .getByRole("button", { name: "退出登录", exact: true })
      .last()
      .click();
  }
  const header = () => page.locator(".admin-header");
  const guard = () =>
    page.getByRole("dialog", { name: "有尚未保存的修改", exact: true });
  // Global maintenance and security remain usable with no enabled libraries.
  await page.goto("/admin/maintenance?view=trash");
  await expect(
    page.getByRole("tab", { name: "回收站", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("回收站为空", { exact: true })).toBeVisible();
  expect(reads.filter((r) => r.pathname === "/api/v1/jobs")).toHaveLength(0);
  await page.goto("/admin/maintenance");
  await expect(page.getByText("还没有书库", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "回收站", exact: true }).click();
  await expect(page.getByText("回收站为空", { exact: true })).toBeVisible();
  const beforeSecurity = reads.length;
  await page.goto("/admin/settings/security");
  await expect(page.getByLabel("当前密码", { exact: true })).toBeVisible();
  expect(
    reads
      .slice(beforeSecurity)
      .filter((r) => r.pathname.startsWith("/api/v1/settings/")),
  ).toHaveLength(0);
  const lib = await write(
    "/libraries",
    { root_key: "anonymous", display_name: "匿名导航书库" },
    201,
  );
  const scan = await write(`/libraries/${lib.id}/scans`, {}, 202);
  await completed(scan.job_id);
  const book = (await get(`/editions?library_id=${lib.id}&page_size=50`))
    .items[0];
  await page.goto("/admin/books");
  await expect(page.locator(".book-list-entry")).toHaveCount(3);
  expect(
    await header()
      .getByRole("button", { name: "退出登录", exact: true })
      .count(),
  ).toBe(0);
  await openNav();
  const navigation =
    info.project.name === "mobile"
      ? page.getByRole("dialog", { name: "管理导航", exact: true })
      : page.locator(".desktop-sidebar");
  await expect(navigation.getByRole("menuitem")).toHaveCount(7);
  for (const label of [
    "漫画",
    "书库",
    "系列",
    "版本",
    "标签",
    "维护中心",
    "设置",
  ])
    await expect(
      navigation.getByRole("menuitem", { name: label, exact: true }),
    ).toHaveCount(1);
  if (info.project.name === "mobile") await page.keyboard.press("Escape");
  else {
    await page
      .getByRole("button", { name: "展开管理导航", exact: true })
      .click();
    await page.setViewportSize({ width: 1280, height: 360 });
    const exitBox = await navigation
      .getByRole("button", { name: "退出登录", exact: true })
      .boundingBox();
    const menuBox = await navigation.locator(".navigation-menu").boundingBox();
    expect(exitBox.y).toBeGreaterThanOrEqual(menuBox.y + menuBox.height);
    expect(exitBox.y + exitBox.height).toBeLessThanOrEqual(360);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.reload();
    await expect(
      page.getByRole("button", { name: "收起管理导航", exact: true }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "收起管理导航", exact: true })
      .click();
  }
  // The old metadata default becomes a real manga filter, including deep links.
  await page.goto(
    `/admin/metadata?library=${lib.id}&book=${book.id}&file_state=available`,
  );
  await expect(page).toHaveURL(/\/admin\/books\?/);
  expect(new URL(page.url()).searchParams.get("metadata_state")).toBe("none");
  const details = page.getByRole("dialog", { name: /^漫画详情/ });
  await expect(details).toBeVisible();
  await details.getByRole("button", { name: "关闭", exact: true }).click();
  // A real task is placed on a later list page to verify list-state restoration.
  const realJob = await get(`/jobs/${scan.job_id}`);
  await page.route("**/api/v1/jobs?*", (route) =>
    route.fulfill({
      json: { items: [realJob], total: 151, page: 3, page_size: 50 },
    }),
  );
  let mark = reads.length;
  await page.goto(
    `/admin/jobs?library_id=${lib.id}&type=scan&state=completed&page=3`,
  );
  await expect(
    page.getByRole("button", { name: "书库扫描", exact: true }),
  ).toBeVisible();
  expect(
    reads
      .slice(mark)
      .filter((r) => r.pathname.startsWith("/api/v1/maintenance/")),
  ).toHaveLength(0);
  await page.getByRole("button", { name: "书库扫描", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`task=${scan.job_id}`));
  await page.getByRole("button", { name: "返回任务列表", exact: true }).click();
  const back = new URL(page.url());
  expect(back.searchParams.get("page")).toBe("3");
  expect(back.searchParams.get("type")).toBe("scan");
  expect(back.searchParams.get("state")).toBe("completed");
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`task=${scan.job_id}`));
  await page.goForward();
  await expect(
    page.getByRole("button", { name: "书库扫描", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("combobox", { name: "书库查看范围", exact: true })
    .click();
  await page.getByRole("option", { name: "全部书库", exact: true }).click();
  await expect(page).toHaveURL(/library_scope=all/);
  await expect
    .poll(() =>
      reads
        .filter((r) => r.pathname === "/api/v1/jobs")
        .at(-1)
        ?.searchParams.get("library_id"),
    )
    .toBe(null);
  mark = reads.length;
  await page.getByRole("tab", { name: "回收站", exact: true }).click();
  await expect(page.getByText("回收站为空", { exact: true })).toBeVisible();
  // Give the former 2s polling timer a full turn after it was unmounted.
  const count = reads.filter((r) => r.pathname === "/api/v1/jobs").length;
  await page.waitForTimeout(2200);
  expect(reads.filter((r) => r.pathname === "/api/v1/jobs")).toHaveLength(
    count,
  );
  expect(
    reads
      .slice(mark)
      .filter((r) => r.pathname === "/api/v1/maintenance/storage"),
  ).toHaveLength(0);
  await expect(header().getByText("全局", { exact: true })).toBeVisible();
  const shots = path.join(
    fixture.run_directory,
    `navigation-${info.project.name}`,
  );
  await mkdir(shots, { recursive: true });
  async function screenshot(name) {
    for (const width of info.project.name === "desktop"
      ? [1280, 1440]
      : [390]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "desktop" ? 900 : 844,
      });
      await page.mouse.move(
        width - 16,
        info.project.name === "desktop" ? 880 : 824,
      );
      for (const dark of [false, true]) {
        const toggle = page.getByRole("switch", { name: "深色主题" });
        await toggle.evaluate((el, v) => {
          if (el.getAttribute("aria-checked") !== String(v)) el.click();
        }, dark);
        await expect(toggle).toHaveAttribute("aria-checked", String(dark));
        await expect(page.locator(".ant-message-notice")).toHaveCount(0, {
          timeout: 7000,
        });
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        ).toBe(true);
        await page.screenshot({
          path: path.join(
            shots,
            `${name}-${width}-${dark ? "dark" : "light"}.png`,
          ),
          animations: "disabled",
        });
      }
    }
  }
  await screenshot("trash");
  await page.getByRole("tab", { name: "存储与清理", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "预览清理范围", exact: true }),
  ).toBeVisible();
  await screenshot("storage");
  // Restore and permanent purge still require their original impact preview.
  async function trashBook() {
    const b = await get(`/books/${book.id}`);
    const p = await write("/maintenance/plans", {
      operation: "trash",
      book_ids: [book.id],
      expected_revisions: { [`book:${book.id}`]: b.revision },
      options: {},
    });
    const j = await write(`/maintenance/plans/${p.id}/execute`, {}, 202);
    await completed(j.job_id);
  }
  await trashBook();
  await page.getByRole("tab", { name: "回收站", exact: true }).click();
  await page.getByRole("button", { name: "恢复", exact: true }).click();
  async function confirmMaintenance(name, purge = false) {
    const d = page.getByRole("dialog", { name, exact: true });
    await d.getByRole("button", { name: "查看影响预览", exact: true }).click();
    await expect(d.getByText("影响摘要", { exact: true })).toBeVisible();
    const confirm = d.getByRole("button", {
      name: `确认执行${name}`,
      exact: true,
    });
    if (purge) {
      await expect(confirm).toBeDisabled();
      await d.getByRole("checkbox").check();
    }
    await confirm.click();
    await expect(page).toHaveURL(/section=tasks.*task=|task=.*section=tasks/);
    const id = new URL(page.url()).searchParams.get("task");
    await completed(id);
  }
  await confirmMaintenance("恢复索引");
  await trashBook();
  await page.getByRole("tab", { name: "回收站", exact: true }).click();
  await page.getByRole("button", { name: "永久清除", exact: true }).click();
  await confirmMaintenance("永久清除应用记录", true);
  // Control requests keep the task identity; use simulated states so no actual
  // worker is paused or resumed by the browser. A pending write locks logout.
  let controlState = "running",
    releasePause,
    pauseStarted;
  const pauseGate = new Promise((resolve) => {
    releasePause = resolve;
  });
  const pauseRead = new Promise((resolve) => {
    pauseStarted = resolve;
  });
  const controlJob = () => ({
    ...realJob,
    id: "anonymous-navigation-control",
    type: "metadata",
    mode: "selected",
    state: controlState,
  });
  await page.route("**/api/v1/jobs/anonymous-navigation-control", (route) =>
    route.fulfill({ json: controlJob() }),
  );
  await page.route(
    "**/api/v1/jobs/anonymous-navigation-control/items?*",
    (route) =>
      route.fulfill({ json: { items: [], total: 0, page: 1, page_size: 50 } }),
  );
  for (const [operation, state] of [
    ["pause", "paused"],
    ["resume", "running"],
    ["cancel", "cancelled"],
  ])
    await page.route(
      `**/api/v1/jobs/anonymous-navigation-control/${operation}`,
      async (route) => {
        if (operation === "pause") {
          pauseStarted();
          await pauseGate;
        }
        controlState = state;
        await route.fulfill({ json: controlJob() });
      },
    );
  await page.goto("/admin/jobs/anonymous-navigation-control");
  await page.getByRole("button", { name: "暂停任务", exact: true }).click();
  await pauseRead;
  await expect(
    page.locator(".navigation-exit .logout-button").first(),
  ).toBeDisabled();
  releasePause();
  await page.getByRole("button", { name: "恢复任务", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "暂停任务", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "取消任务", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "暂停任务", exact: true }),
  ).toHaveCount(0);
  expect(new URL(page.url()).searchParams.get("task")).toBe(
    "anonymous-navigation-control",
  );
  // Settings categories mount only their own resources; credentials share the metadata page.
  mark = reads.length;
  await nav("设置");
  await expect(page.getByText("服务端版本", { exact: true })).toBeVisible();
  await category("元数据与网络");
  await expect(page.getByLabel("代理出口", { exact: true })).toBeVisible();
  await expect(page.getByRole("tab")).toHaveCount(0);
  expect(
    reads
      .slice(mark)
      .filter(
        (r) =>
          r.pathname === "/api/v1/settings/tag-translations" ||
          r.pathname === "/api/v1/tags",
      ),
  ).toHaveLength(0);
  await screenshot("network");
  await page.locator("#settings-credentials").scrollIntoViewIfNeeded();
  await page.getByLabel("Cookie：ipb_pass_hash操作", { exact: true }).click();
  await page.getByRole("option", { name: "替换", exact: true }).click();
  const secret = page.getByLabel("Cookie：ipb_pass_hash", { exact: true });
  await secret.fill("anonymous-write-only-secret");
  await category("增强功能");
  await guard().getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(secret).toHaveValue("anonymous-write-only-secret");
  await exit();
  const logout = page.getByRole("dialog", {
    name: "放弃未保存修改并退出？",
    exact: true,
  });
  await expect(logout).toBeVisible();
  await logout.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(logout).not.toBeVisible();
  if (info.project.name === "mobile") {
    await expect(
      page.getByRole("dialog", { name: "管理导航", exact: true }),
    ).not.toBeVisible();
    await expect(
      page.getByRole("button", { name: "打开管理导航", exact: true }),
    ).toBeFocused();
  }
  await page.getByRole("button", { name: "保存凭据", exact: true }).click();
  await expect(secret).toHaveCount(0);
  await expect(page.getByText("凭据已加密保存", { exact: true })).toBeVisible();
  const provider = await get("/settings/metadata-provider");
  expect(provider.configured.ipb_pass_hash).toBe(true);
  expect(JSON.stringify(provider)).not.toContain("anonymous-write-only-secret");
  await screenshot("credentials");
  await category("增强功能");
  await page
    .getByRole("button", { name: "展开中文译库配置", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "安装／更新中文译库", exact: true }),
  ).toBeVisible();
  await screenshot("translations");
  // Mock only the explicitly clicked update job; it must open the new task route.
  const beforeUpdate = writes.length;
  await page.route("**/api/v1/tag-translation-jobs", (route) =>
    route.fulfill({ status: 202, json: { job_id: scan.job_id } }),
  );
  await page
    .getByRole("button", { name: "安装／更新中文译库", exact: true })
    .click();
  await expect(page).toHaveURL(new RegExp(`task=${scan.job_id}`));
  expect(
    writes
      .slice(beforeUpdate)
      .filter((r) => r.url.pathname === "/api/v1/tag-translation-jobs"),
  ).toHaveLength(1);
  await page.goto("/admin/settings/security");
  await expect(page.getByLabel("当前密码", { exact: true })).toBeVisible();
  await screenshot("security");
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByLabel("新密码", { exact: true }).fill(nextPassword);
  await page.getByLabel("确认新密码", { exact: true }).fill(nextPassword);
  await category("增强功能");
  await guard().getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(page.getByLabel("新密码", { exact: true })).toHaveValue(
    nextPassword,
  );
  await page.getByRole("button", { name: /保存新密码/ }).click();
  await expect(
    page.getByRole("heading", { name: "管理员登录", exact: true }),
  ).toBeVisible();
  expect(
    (await page.request.get(`${baseURL}/api/v1/auth/session`)).status(),
  ).toBe(401);
  await page.getByLabel("管理员密码", { exact: true }).fill(nextPassword);
  await page.getByRole("button", { name: /登录/ }).click();
  await expect(page.locator(".book-list-entry")).toHaveCount(2);
  await exit();
  await expect(
    page.getByRole("heading", { name: "管理员登录", exact: true }),
  ).toBeVisible();
  await expect(logout).not.toBeVisible();
  expect(errors).toEqual([]);
});
