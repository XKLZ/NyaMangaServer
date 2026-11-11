import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { compatibilityFixture, password } from "./compatibility-fixture.mjs";
import { routeViteSource } from "./vite-source.mjs";
const shots = path.resolve(
  import.meta.dirname,
  "../../../.runtime/tests/compatibility-ui/screenshots",
);
async function screenshot(page, name, info) {
  await mkdir(shots, { recursive: true });
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    if (await page.locator(".admin-shell").count())
      await expect(page.locator(".admin-shell")).toHaveClass(
        new RegExp(colorScheme),
      );
    if (await page.locator('[aria-label="深色主题"]').count())
      await expect(page.locator('[aria-label="深色主题"]')).toHaveAttribute(
        "aria-checked",
        String(colorScheme === "dark"),
      );
    await expect
      .poll(async () =>
        page.locator(".ant-modal-content:visible").evaluateAll(
          (nodes, scheme) =>
            nodes.every((n) => {
              const values = getComputedStyle(n)
                .backgroundColor.match(/\d+/g)
                ?.map(Number);
              return (
                !values ||
                values[3] === 0 ||
                (scheme === "dark" ? values[0] < 100 : values[0] > 200)
              );
            }),
          colorScheme,
        ),
      )
      .toBe(true);
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
      });
      await page.evaluate(async () => {
        await Promise.all(
          document
            .getAnimations()
            .filter(
              (a) => a.effect?.getComputedTiming().iterations !== Infinity,
            )
            .map((a) => a.finished.catch(() => {})),
        );
      });
      await page.screenshot({
        path: path.join(
          shots,
          `${info.project.name}-${width}-${name}-${colorScheme}.png`,
        ),
        animations: "disabled",
      });
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBe(width);
    }
  }
}
test("三账号三书库：改密门禁、只读浏览、阅读CAS与实时撤权", async ({
  page,
  browser,
  baseURL,
}, info) => {
  test.setTimeout(150000);
  await routeViteSource(page, baseURL);
  const f = await compatibilityFixture(page, baseURL);
  let viewer, editor;
  try {
    const [a, b, c] = f.libraries;
    await f.grant("fixture-viewer", { [a.id]: "viewer", [b.id]: "viewer" });
    await f.grant("fixture-editor", { [a.id]: "editor", [c.id]: "viewer" });
    const book0 = await f.get("/books/fixture-edition-0");
    const adminFavoriteBefore = book0.user_state?.favorite || false;
    await f.write(
      "/books/fixture-edition-0/overrides",
      {
        expected_revision: book0.revision,
        fields: {
          tags: {
            op: "set",
            value: [
              { namespace: "artist", name: "anonymous" },
              { namespace: "parody", name: "anonymous origin" },
              { namespace: "custom", name: "Anonymous custom tag" },
            ],
          },
        },
      },
      "PATCH",
    );
    const options = {
      viewport:
        info.project.name === "mobile"
          ? { width: 390, height: 844 }
          : { width: 1440, height: 900 },
      isMobile: info.project.name === "mobile",
      hasTouch: info.project.name === "mobile",
    };
    viewer = await browser.newContext(options);
    editor = await browser.newContext(options);
    const v = await viewer.newPage(),
      e = await editor.newPage();
    await routeViteSource(v, baseURL);
    await routeViteSource(e, baseURL);
    const requests = [],
      failed = [],
      forced = [];
    v.on("request", (r) => {
      if (r.url().includes("/api/"))
        requests.push({ url: new URL(r.url()).pathname, method: r.method() });
    });
    v.on("response", (r) => {
      if (r.status() === 403) failed.push(r.url());
    });
    e.on("request", (r) => {
      if (r.url().includes("/api/")) forced.push(new URL(r.url()).pathname);
    });
    await f.login(e, "fixture-editor");
    await expect(
      e.getByRole("heading", { name: "首次登录，请修改密码" }),
    ).toBeVisible();
    await e.waitForTimeout(150);
    expect(forced.every((p) => p.startsWith("/api/v1/auth/"))).toBe(true);
    await screenshot(e, "forced-password", info);
    const secret = "Anonymous editor new password";
    await e.getByLabel("当前密码", { exact: true }).fill(password);
    await e.getByLabel("新密码", { exact: true }).fill(secret);
    await e.getByLabel("确认新密码", { exact: true }).fill(secret);
    await e.getByRole("button", { name: /保存新密码/ }).click();
    await expect(
      e.getByRole("heading", { name: "登录", exact: true }),
    ).toBeVisible();
    await f.login(e, "fixture-editor", secret);
    await expect(e.locator(".admin-shell")).toBeVisible();
    await f.login(v, "fixture-viewer");
    await expect(v.locator(".admin-shell")).toBeVisible();
    await v.goto(
      `${baseURL}/admin/books?library=${a.id}&book=fixture-edition-0`,
    );
    const detail = v.locator(".book-dialog");
    await expect(detail).toBeVisible();
    await detail.evaluate(async (node) => {
      await Promise.all(
        node
          .getAnimations({ subtree: true })
          .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
          .map((a) => a.finished.catch(() => {})),
      );
      await new Promise((r) =>
        requestAnimationFrame(() => requestAnimationFrame(r)),
      );
    });
    await expect(
      detail.getByRole("tab", { name: "元数据", exact: true }),
    ).toBeVisible();
    await expect(
      detail.getByRole("tab", { name: "封面", exact: true }),
    ).toHaveCount(0);
    await expect(detail.getByText(/12 页/)).toBeVisible();
    await expect(
      detail.getByRole("region", { name: "阅读状态" }),
    ).toBeVisible();
    await screenshot(v, "viewer-reading", info);
    await detail.getByRole("tab", { name: "元数据", exact: true }).click();
    await expect(
      detail.getByRole("heading", { name: "custom", exact: true }),
    ).toBeVisible();
    await expect(
      detail.getByText("anonymous custom tag", { exact: true }),
    ).toBeVisible();
    await expect(detail.getByRole("textbox")).toHaveCount(0);
    await detail.getByRole("tab", { name: "概览", exact: true }).click();
    await detail.getByRole("button", { name: "收藏这本", exact: true }).click();
    await expect(
      detail.getByRole("button", { name: "取消收藏", exact: true }),
    ).toBeVisible();
    expect(
      (await f.get("/books/fixture-edition-0")).user_state?.favorite || false,
    ).toBe(adminFavoriteBefore);
    expect((await f.get("/books/fixture-edition-0")).user_state).toEqual(
      book0.user_state,
    );
    await detail.getByRole("button", { name: "取消收藏", exact: true }).click();
    await expect(
      detail.getByRole("button", { name: "收藏这本", exact: true }),
    ).toBeVisible();
    expect(
      (await f.get("/books/fixture-edition-0")).user_state?.favorite || false,
    ).toBe(adminFavoriteBefore);
    expect((await f.get("/books/fixture-edition-0")).user_state).toEqual(
      book0.user_state,
    );

    expect(
      requests.filter(
        (r) => r.method === "POST" && r.url.endsWith("/reading-sessions"),
      ),
    ).toHaveLength(0);
    await detail.getByRole("button", { name: "取消完成", exact: true }).click();
    const reading = v.getByRole("dialog", { name: "取消完成", exact: true });
    const state = await f.get(
      "/editions/fixture-edition-0/reading-state",
      viewer.request,
    );
    await f.write(
      "/editions/fixture-edition-0/reading-state",
      {
        expected_revision: state.revision,
        content_revision: state.current_content_revision,
        idempotency_key: "anonymous-race-12345",
        action: "reset",
      },
      "PUT",
      viewer.request,
    );
    await reading.getByRole("button", { name: "保存阅读状态" }).click();
    await expect(
      reading.getByText("阅读状态或内容版本已变化，未自动重试"),
    ).toBeVisible();
    await expect(
      reading.getByRole("button", { name: "保存阅读状态" }),
    ).toBeDisabled();
    await reading
      .getByRole("button", { name: "读取当前状态并返回核对" })
      .click();
    await expect(reading).toHaveCount(0);
    const readingPayloads = [];
    await v.route(
      "**/api/v1/editions/fixture-edition-0/reading-state",
      async (route) => {
        if (route.request().method() !== "PUT") return route.continue();
        readingPayloads.push(route.request().postDataJSON());
        if (readingPayloads.length === 1) return route.abort("connectionreset");
        return route.continue();
      },
    );
    await detail.getByRole("button", { name: "标记完成", exact: true }).click();
    const complete = v.getByRole("dialog", { name: "标记完成", exact: true });
    await complete.getByRole("button", { name: "保存阅读状态" }).click();
    await expect(
      complete.getByText("无法连接服务，请检查网络后重试"),
    ).toBeVisible();
    await complete.getByRole("button", { name: "保存阅读状态" }).click();
    await expect(complete).toHaveCount(0);
    expect(readingPayloads).toHaveLength(2);
    expect(readingPayloads[1]).toEqual(readingPayloads[0]);
    await v.unroute("**/api/v1/editions/fixture-edition-0/reading-state");
    await expect
      .poll(
        async () =>
          (
            await f.get(
              "/editions/fixture-edition-0/reading-state",
              viewer.request,
            )
          ).completed,
      )
      .toBe(true);
    expect(
      (await f.get("/editions/fixture-edition-0/reading-state")).completed,
    ).toBe(false);
    const completed = await f.get(
      "/editions/fixture-edition-0/reading-state",
      viewer.request,
    );
    expect(completed.read_count).toBe(state.read_count);
    f.sql(
      "UPDATE files SET content_revision=8,page_count=0 WHERE edition_id='fixture-edition-0';",
    );
    await detail.getByRole("button", { name: "刷新", exact: true }).click();
    await expect(
      detail.getByText("已完成 · 内容已变化", { exact: true }),
    ).toBeVisible();
    await detail
      .getByRole("button", { name: "重置阅读状态", exact: true })
      .click();
    const reset = v.getByRole("dialog", { name: "重置阅读状态", exact: true });
    await expect(
      reset.getByRole("button", { name: "保存阅读状态" }),
    ).toBeDisabled();
    await reset
      .getByRole("checkbox", { name: "接受当前内容版本并清除旧进度" })
      .check();
    await reset.getByRole("button", { name: "保存阅读状态" }).click();
    await expect(reset).toHaveCount(0);
    const cleared = await f.get(
      "/editions/fixture-edition-0/reading-state",
      viewer.request,
    );
    expect(cleared).toMatchObject({
      page_index: null,
      completed: false,
      stale: false,
      current_page_count: null,
      read_count: completed.read_count,
      last_read_at: completed.last_read_at,
    });
    expect((await f.get("/books/fixture-edition-0")).user_state).toEqual(
      book0.user_state,
    );
    await expect(detail.getByText("总页数未知", { exact: true })).toBeVisible();
    await screenshot(v, "unknown-reading", info);
    await v.goto(`${baseURL}/admin/books?library=${b.id}`);
    await v.getByRole("button", { name: "保存条件", exact: true }).click();
    const saved = v.getByRole("dialog", { name: "保存常用搜索", exact: true });
    await saved
      .getByRole("textbox", { name: "搜索名称", exact: true })
      .fill("Anonymous viewer search");
    await saved
      .getByRole("button", { name: "保存当前搜索", exact: true })
      .click();
    await expect
      .poll(async () =>
        (await f.get("/saved-searches", viewer.request)).items.some(
          (x) => x.name === "Anonymous viewer search",
        ),
      )
      .toBe(true);
    expect(
      (await f.get("/saved-searches")).items.some(
        (x) => x.name === "Anonymous viewer search",
      ),
    ).toBe(false);
    await v.goto(`${baseURL}/admin/tags?library=${a.id}&category=artist`);
    const artist = (
      await f.get(
        `/tag-catalog?library_id=${a.id}&category=artist`,
        viewer.request,
      )
    ).items.find((x) => x.name === "anonymous");
    const star = v.locator(`[data-tag-id="${artist.id}"] .tag-follow-button`);
    await star.focus();
    await star.click();
    await expect(star).toHaveAttribute("aria-pressed", "true");
    await v.locator(`[data-tag-id="${artist.id}"]`).getByRole("link").click();
    const tagDetail = v.locator(".tag-detail-modal");
    await expect(tagDetail).toBeVisible();
    await expect(
      tagDetail.getByRole("textbox", { name: "标签中文译名", exact: true }),
    ).toHaveCount(0);
    await expect(tagDetail.locator(".tag-role-preview")).toHaveCount(0);
    await v.goto(`${baseURL}/admin/tags?library=${a.id}&category=parody`);
    const parody = (
      await f.get(
        `/tag-catalog?library_id=${a.id}&category=parody`,
        viewer.request,
      )
    ).items[0];
    await v.locator(`[data-tag-id="${parody.id}"]`).getByRole("link").click();
    await expect(tagDetail.locator(".tag-role-preview").first()).toBeDisabled();
    await expect(
      tagDetail.getByRole("button", { name: "删除主体图", exact: true }),
    ).toHaveCount(0);
    await v.goto(`${baseURL}/admin/settings?section=enhancements`);
    await expect(
      v.getByRole("heading", { name: "修改本人密码" }),
    ).toBeVisible();
    await expect(v.getByRole("button", { name: "创建账号" })).toHaveCount(0);
    await v.goto(`${baseURL}/admin/maintenance?section=health`);
    await expect(
      v.getByRole("tab", { name: "任务", exact: true }),
    ).toBeVisible();
    await expect(
      v.getByRole("tab", { name: "数据健康", exact: true }),
    ).toHaveCount(0);
    await v.goto(`${baseURL}/admin/libraries`);
    await expect(
      v.getByText("匿名库 B", { exact: true }).first(),
    ).toBeVisible();
    await expect(v.getByText("匿名库 C", { exact: true })).toHaveCount(0);
    expect(failed).toEqual([]);
    expect(
      requests.some((r) =>
        /\/settings\/(enhancements|storage)|\/maintenance\/|\/library-roots/.test(
          r.url,
        ),
      ),
    ).toBe(false);
    await v.goto(
      `${baseURL}/admin/books?library=${a.id}&book=fixture-edition-0`,
    );
    await expect(detail).toBeVisible();
    await detail.evaluate(async (node) => {
      await Promise.all(
        node
          .getAnimations({ subtree: true })
          .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
          .map((a) => a.finished.catch(() => {})),
      );
      await new Promise((r) =>
        requestAnimationFrame(() => requestAnimationFrame(r)),
      );
    });
    await f.grant("fixture-viewer", {});
    await expect(v.getByText("尚未获得书库授权，请联系管理员")).toBeVisible();
    await expect(detail).toHaveCount(0);
    await screenshot(v, "no-grants", info);
    expect(
      requests.filter(
        (r) => r.method === "GET" && r.url.endsWith("/reading-state"),
      ),
    ).toHaveLength(0);
  } finally {
    await viewer?.close();
    await editor?.close();
    await f.stop();
  }
});

test("管理员工作台：权限复核保留草稿焦点、临时失败暂停提交、账号CAS不静默重试", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(120000);
  await routeViteSource(page, baseURL);
  const f = await compatibilityFixture(page, baseURL);
  try {
    const a = f.libraries[0];
    await page.goto(
      `${baseURL}/admin/books?library=${a.id}&book=fixture-edition-0`,
    );
    const detail = page.locator(".book-dialog");
    await expect(detail).toBeVisible();
    await detail.evaluate(async (node) => {
      await Promise.all(
        node
          .getAnimations({ subtree: true })
          .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
          .map((a) => a.finished.catch(() => {})),
      );
      await new Promise((r) =>
        requestAnimationFrame(() => requestAnimationFrame(r)),
      );
    });
    await detail.getByRole("tab", { name: "元数据", exact: true }).click();
    const title = detail.getByRole("textbox", {
      name: "原文标题",
      exact: true,
    });
    await title.fill("Anonymous unsaved draft");
    await title.focus();
    await title.evaluate((n) => (n.dataset.identity = "retained-draft"));
    await page.evaluate(() =>
      document.dispatchEvent(new Event("visibilitychange")),
    );
    await expect(title).toBeEnabled();
    await expect(title).toBeFocused();
    await expect(title).toHaveValue("Anonymous unsaved draft");
    expect(await title.getAttribute("data-identity")).toBe("retained-draft");
    let fail = true;
    await page.route("**/api/v1/auth/session", async (route) =>
      fail
        ? route.fulfill({
            status: 503,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "temporary", message: "anonymous unavailable" },
            }),
          })
        : route.continue(),
    );
    await page.evaluate(() =>
      document.dispatchEvent(new Event("visibilitychange")),
    );
    await expect(
      page.getByText("账号权限暂未确认，草稿保留；提交已暂停"),
    ).toBeVisible();
    await expect(title).toHaveValue("Anonymous unsaved draft");
    await expect(
      detail.getByRole("button", { name: "保存修改", exact: true }),
    ).toBeDisabled();
    fail = false;
    await detail.getByRole("button", { name: "重试权限读取" }).click();
    await expect(title).toBeEnabled();
    await expect(title).toHaveValue("Anonymous unsaved draft");
    expect(await title.getAttribute("data-identity")).toBe("retained-draft");
    await expect(title).toBeFocused();
    await screenshot(page, "metadata-draft", info);
    await page.goto(`${baseURL}/admin/settings?section=security`);
    const accountSection = page.getByRole("region", {
      name: "账号管理",
      exact: true,
    });
    await expect(accountSection).toBeVisible();
    await accountSection
      .getByRole("button", { name: "创建账号", exact: true })
      .click();
    const create = page.getByRole("dialog", { name: "创建账号", exact: true });
    await create
      .getByRole("textbox", { name: "账号用户名", exact: true })
      .fill("anonymous-new");
    await create
      .getByLabel("账号临时密码", { exact: true })
      .fill("Anonymous temporary password");
    await create.getByRole("button", { name: "保存账号" }).click();
    await expect(create).toHaveCount(0);
    const row = accountSection
      .getByRole("row")
      .filter({ hasText: "anonymous-new" });
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: "编辑授权" }).click();
    const edit = page.getByRole("dialog", { name: "编辑账号 · anonymous-new" });
    await edit.getByRole("combobox", { name: "匿名库 B授权" }).click();
    await page.getByRole("option", { name: "查看", exact: true }).click();
    const accounts = await f.get("/accounts"),
      account = accounts.items.find((x) => x.username === "anonymous-new");
    await f.grant(account.id, { [a.id]: "viewer" });
    await edit.getByRole("button", { name: "保存账号" }).click();
    await expect(edit.getByText(/账号已变化或受最后管理员保护/)).toBeVisible();
    await expect(
      edit.getByRole("group", { name: "账号保存结果", exact: true }),
    ).toBeFocused();
    await expect(edit.getByRole("button", { name: "保存账号" })).toBeDisabled();
    await screenshot(page, "account-conflict", info);
    await edit.getByRole("button", { name: "放弃修改" }).click();
    await accountSection
      .getByRole("button", { name: "刷新账号", exact: true })
      .click();
    await row.getByRole("button", { name: "编辑授权" }).click();
    await edit.getByRole("combobox", { name: "匿名库 B授权" }).click();
    await page.getByRole("option", { name: "查看", exact: true }).click();
    await edit.getByRole("button", { name: "保存账号" }).click();
    await expect(edit).toHaveCount(0);
    expect(
      (await f.get("/accounts")).items.find((x) => x.id === account.id)
        .library_grants[f.libraries[1].id],
    ).toBe("viewer");
    await row.getByRole("button", { name: "编辑授权" }).click();
    await edit
      .getByLabel("重置账号密码", { exact: true })
      .fill("Anonymous reset password");
    fail = true;
    await page.evaluate(() =>
      document.dispatchEvent(new Event("visibilitychange")),
    );
    await expect(
      page.getByText("账号权限暂未确认，草稿保留；提交已暂停"),
    ).toBeVisible();
    await expect(edit.getByRole("button", { name: "保存账号" })).toBeDisabled();
    await expect(edit.getByLabel("重置账号密码", { exact: true })).toHaveValue(
      "Anonymous reset password",
    );
    fail = false;
    await edit.getByRole("button", { name: "重试权限读取" }).click();
    await expect(edit.getByRole("button", { name: "保存账号" })).toBeEnabled();
    await edit.getByRole("button", { name: "保存账号" }).click();
    await expect(edit.getByText("确认账号影响", { exact: true })).toBeVisible();
    await edit.getByRole("button", { name: "确认影响并保存" }).click();
    await expect(edit).toHaveCount(0);
    await row.getByRole("button", { name: "编辑授权" }).click();
    await expect(edit.getByLabel("重置账号密码", { exact: true })).toHaveValue(
      "",
    );
    await edit
      .getByRole("checkbox", { name: "启用账号", exact: true })
      .uncheck();
    await edit.getByRole("button", { name: "保存账号" }).click();
    await edit.getByRole("button", { name: "确认影响并保存" }).click();
    await expect(edit).toHaveCount(0);
    expect(
      (await f.get("/accounts")).items.find((x) => x.id === account.id).enabled,
    ).toBe(false);
    const headingBox = await accountSection
      .getByRole("heading", { name: "账号管理", exact: true })
      .boundingBox();
    const tableBox = await accountSection
      .locator(".ant-table-wrapper")
      .boundingBox();
    expect(Math.abs(headingBox.x - tableBox.x)).toBeLessThan(2);
    await screenshot(page, "accounts", info);
  } finally {
    await f.stop();
  }
});

test("编辑者：真实只读能力、启动恢复采纳推荐、共享来源403保留候选", async ({
  page,
  browser,
  baseURL,
}, info) => {
  test.setTimeout(150000);
  await routeViteSource(page, baseURL);
  const f = await compatibilityFixture(page, baseURL, {
    recommendations: true,
    editorReady: true,
  });
  let context;
  try {
    const [a, b] = f.libraries;
    await f.grant("fixture-editor", { [a.id]: "viewer", [b.id]: "editor" });
    for (const feature of ["work_recommendations", "series_recommendations"]) {
      const state = await f.get("/settings/enhancements");
      await f.write(
        "/settings/enhancements",
        { expected_revision: state.revision, feature, enabled: true },
        "PATCH",
      );
      await expect
        .poll(
          async () =>
            (await f.get("/settings/enhancements")).features[feature]
              .effective_enabled,
        )
        .toBe(true);
    }
    const books = (
      await f.get(`/editions?library_id=${b.id}&page_size=100`)
    ).items.filter((x) => x.file_state === "available");
    expect(books.length).toBeGreaterThan(1);
    for (const item of books) {
      const current = await f.get(`/books/${item.id}`);
      await f.write(
        `/books/${item.id}/overrides`,
        {
          expected_revision: current.revision,
          fields: {
            title_original: { op: "set", value: "Anonymous same work" },
            tags: {
              op: "set",
              value: [{ namespace: "artist", name: "anonymous" }],
            },
          },
        },
        "PATCH",
      );
    }
    context = await browser.newContext({
      viewport:
        info.project.name === "mobile"
          ? { width: 390, height: 844 }
          : { width: 1440, height: 900 },
      isMobile: info.project.name === "mobile",
      hasTouch: info.project.name === "mobile",
    });
    const e = await context.newPage();
    await routeViteSource(e, baseURL);
    const denied = [],
      reads = [];
    e.on("response", (r) => {
      if (r.status() === 403) denied.push(new URL(r.url()).pathname);
    });
    e.on("request", (r) => {
      if (r.url().includes("/api/")) reads.push(new URL(r.url()).pathname);
    });
    await f.login(e, "fixture-editor");
    await expect(e.locator(".admin-shell")).toBeVisible();
    await e.goto(
      `${baseURL}/admin/books?library=${a.id}&book=fixture-edition-0`,
    );
    await expect(e.locator(".book-dialog")).toBeVisible();
    await expect(
      e.getByRole("tab", { name: "来源匹配", exact: true }),
    ).toHaveCount(0);
    await e.goto(`${baseURL}/admin/versions?library=${b.id}`);
    await expect(
      e.getByRole("button", { name: "查找版本", exact: true }),
    ).toBeEnabled();
    const capability = await f.get(
      "/settings/work-recommendations",
      context.request,
    );
    expect(
      capability.work_enabled && capability.discovery && capability.proposals,
    ).toBe(true);
    const started = e.waitForResponse(
      (r) =>
        r.url().endsWith("/work-discovery-jobs") &&
        r.request().method() === "POST",
    );
    await e.getByRole("button", { name: "查找版本", exact: true }).click();
    const response = await started;
    expect(response.status()).toBe(202);
    const job = (await response.json()).job_id;
    await e
      .getByRole("button", { name: "版本查找任务状态", exact: true })
      .click();
    await e.getByRole("button", { name: "暂停", exact: true }).click();
    await expect
      .poll(async () => (await f.get(`/jobs/${job}`)).state)
      .toBe("paused");
    await expect(
      e.getByRole("button", { name: "恢复", exact: true }),
    ).toBeEnabled();
    await e.getByRole("button", { name: "恢复", exact: true }).click();
    await expect
      .poll(async () => (await f.get(`/jobs/${job}`)).state, { timeout: 30000 })
      .toBe("completed");
    await e
      .getByRole("button", { name: "版本查找任务状态", exact: true })
      .click();
    await e.getByRole("button", { name: "刷新", exact: true }).click();
    const proposals = (
      await f.get(`/work-proposals?library_id=${b.id}&kind=create_work`)
    ).items;
    expect(proposals.length).toBeGreaterThan(0);
    const proposal = proposals.find((x) => x.primary && x.state === "pending");
    expect(proposal).toBeTruthy();
    await e.goto(
      `${baseURL}/admin/versions?library=${b.id}&tab=new&proposal=${proposal.id}`,
    );
    const editor = e.getByLabel("版本方案", { exact: true });
    await expect(
      editor.getByRole("button", { name: "组成版本组", exact: true }),
    ).toBeEnabled();
    await screenshot(e, "editor-proposal", info);
    await editor
      .getByRole("button", { name: "组成版本组", exact: true })
      .click();
    await expect
      .poll(async () => (await f.get(`/work-proposals/${proposal.id}`)).state)
      .toBe("accepted");
    expect(denied).toEqual([]);
    expect(reads).toContain("/api/v1/settings/work-recommendations");
    expect(reads.some((p) => p === "/api/v1/settings/enhancements")).toBe(
      false,
    );
    const id = books[0].id;
    f.sql(
      `INSERT INTO edition_sources VALUES('${id}','fixture-source','https://e-hentai.org/g/123456/abcdef1234/','manual',1);`,
    );
    await e.goto(`${baseURL}/admin/books?library=${b.id}&book=${id}`);
    const detail = e.locator(".book-dialog");
    await expect(detail).toBeVisible();
    await detail.evaluate(async (node) => {
      await Promise.all(
        node
          .getAnimations({ subtree: true })
          .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
          .map((a) => a.finished.catch(() => {})),
      );
      await new Promise((r) =>
        requestAnimationFrame(() => requestAnimationFrame(r)),
      );
    });
    await detail.getByRole("tab", { name: "元数据", exact: true }).click();
    await detail
      .getByRole("textbox", { name: "原文标题", exact: true })
      .fill("Anonymous editor changed title");
    await detail.getByRole("button", { name: "保存修改", exact: true }).click();
    await expect
      .poll(async () => (await f.get(`/books/${id}`)).title_original)
      .toBe("Anonymous editor changed title");
    await expect(detail.locator(".ant-btn-loading")).toHaveCount(0);
    await detail.getByRole("tab", { name: "来源匹配", exact: true }).click();
    await expect(
      detail.getByRole("tab", { name: "来源匹配", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await detail.getByRole("button", { name: "重新匹配", exact: true }).click();
    await detail.getByText("指定画廊", { exact: true }).click();
    await detail
      .getByLabel("手动指定画廊网址", { exact: true })
      .fill("https://e-hentai.org/g/123456/abcdef1234/");
    await detail
      .getByRole("button", { name: "获取来源预览", exact: true })
      .click();
    await expect(
      detail.getByRole("button", { name: "确认采纳此来源", exact: true }),
    ).toBeVisible({ timeout: 20000 });
    await detail
      .getByRole("button", { name: "确认采纳此来源", exact: true })
      .click();
    await expect(
      detail.getByText(
        "此来源被其他书库共同使用，需要管理员处理。候选与草稿已保留。",
      ),
    ).toBeVisible();
    await expect(
      detail.getByRole("button", { name: "确认采纳此来源", exact: true }),
    ).toBeEnabled();
    await expect(
      detail.getByLabel("手动指定画廊网址", { exact: true }),
    ).toHaveValue("https://e-hentai.org/g/123456/abcdef1234/");
    await screenshot(e, "editor-shared-source", info);
  } finally {
    await context?.close();
    await f.stop();
  }
});

test("独立实例：同一工作台换号不复用个人状态或详情", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(60000);
  await routeViteSource(page, baseURL);
  const f = await compatibilityFixture(page, baseURL);
  try {
    const a = f.libraries[0],
      before = await f.get("/books/fixture-edition-0");
    await page.goto(
      `${baseURL}/admin/books?library=${a.id}&book=fixture-edition-0`,
    );
    const detail = page.locator(".book-dialog");
    await expect(
      detail
        .getByRole("region", { name: "阅读状态" })
        .getByText("未读", { exact: true }),
    ).toBeVisible();
    await detail.getByRole("button", { name: "关闭", exact: true }).click();
    async function signOut() {
      if (info.project.name === "mobile")
        await page
          .getByRole("button", { name: "打开管理导航", exact: true })
          .click();
      await page.getByRole("button", { name: "退出登录", exact: true }).click();
      await expect(
        page.getByRole("heading", { name: "登录", exact: true }),
      ).toBeVisible();
    }
    async function signIn(username) {
      await page.getByLabel("用户名", { exact: true }).fill(username);
      await page.getByLabel("密码", { exact: true }).fill(password);
      await page.getByRole("button", { name: "登录", exact: true }).click();
      await expect(page.locator(".admin-shell")).toBeVisible();
      await expect(detail).toHaveCount(0);
    }
    await page
      .locator(".admin-shell")
      .evaluate((n) => (n.dataset.actorMarker = "old-admin"));
    await signOut();
    await signIn("fixture-viewer");
    expect(
      await page.locator(".admin-shell").getAttribute("data-actor-marker"),
    ).toBeNull();
    await expect(page.locator(".selection-toolbar")).toHaveCount(0);
    await page.getByRole("button", { name: /查看详情.*匿名原文/ }).click();
    await expect(
      detail
        .getByRole("region", { name: "阅读状态" })
        .getByText("已完成", { exact: true }),
    ).toBeVisible();
    await detail.getByRole("button", { name: "关闭", exact: true }).click();
    await signOut();
    await signIn("admin");
    const after = await f.get("/books/fixture-edition-0");
    expect(after.user_state).toEqual(before.user_state);
    expect(after.reading_state).toEqual(before.reading_state);
  } finally {
    await f.stop();
  }
});
