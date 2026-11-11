import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { enhancementFixture } from "./enhancement-fixture.mjs";
installViteSource(test);
test("四区设置、真实权限开关、任务暂停与全局草稿保护", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(240000);
  const { get, write, library, password } = await enhancementFixture(
    page,
    baseURL,
  );
  const initial = await get("/settings/enhancements");
  expect(initial.features.work_recommendations.enabled).toBe(false);
  expect(initial.features.series_recommendations.enabled).toBe(false);
  expect(initial.features.chinese_dictionary.enabled).toBe(true);
  const errors = [],
    reads = [],
    writes = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    const url = new URL(r.url());
    if (!url.pathname.startsWith("/api/")) return;
    if (r.method() === "GET") reads.push(url);
    else writes.push({ url, method: r.method(), body: r.postDataJSON() });
  });
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
  async function nav(name) {
    if (info.project.name === "mobile")
      await page
        .getByRole("button", { name: "打开管理导航", exact: true })
        .click();
    await page
      .getByRole("menuitem", { name, exact: true })
      .last()
      .getByRole("link", { name, exact: true })
      .click();
  }
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/maintenance/enhancements-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  async function screenshot(label) {
    await expect(page.locator(".ant-message-notice")).toHaveCount(0);
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
        await page.evaluate(() => window.scrollTo({ top: 0 }));
        await page.screenshot({
          path: path.join(
            shots,
            `${info.project.name}-${label}-${width}-${colorScheme}.png`,
          ),
          animations: "disabled",
          fullPage: true,
        });
      }
    }
  }
  await page.goto("/admin/settings");
  const service = await get("/settings/service");
  await expect(page.getByText(service.version, { exact: true })).toBeVisible();
  await expect(page.getByText("19", { exact: true })).toBeVisible();
  expect(service.version).not.toBe("0.5.0-dev");
  expect(
    reads.some((u) => u.pathname.endsWith("/settings/metadata-provider")),
  ).toBe(false);
  expect(reads.some((u) => u.pathname.endsWith("/settings/enhancements"))).toBe(
    false,
  );
  expect(reads.some((u) => u.pathname.endsWith("/diagnostics"))).toBe(false);
  await screenshot("overview");
  await category("增强功能");
  const work = page.getByRole("switch", { name: "版本推荐开关", exact: true }),
    series = page.getByRole("switch", { name: "系列推荐开关", exact: true }),
    dict = page.getByRole("switch", { name: "中文译库开关", exact: true });
  await expect(work).not.toBeChecked();
  await expect(series).not.toBeChecked();
  await expect(dict).toBeChecked();
  await expect(work).toBeEnabled();
  expect(
    reads.some((u) => u.pathname.endsWith("/settings/tag-translations")),
  ).toBe(false);
  await screenshot("enhancements-new");
  let release, started;
  const gate = new Promise((r) => (release = r)),
    begin = new Promise((r) => (started = r));
  const hold = async (route) => {
    started();
    await gate;
    await route.continue();
  };
  await page.route("**/api/v1/settings/enhancements", async (route) =>
    route.request().method() === "PATCH" ? hold(route) : route.fallback(),
  );
  await work.click();
  await begin;
  await expect(work).not.toBeChecked();
  await expect(work).toBeDisabled();
  release();
  await expect(work).toBeChecked();
  await expect(work).toBeEnabled();
  await page.unroute("**/api/v1/settings/enhancements");
  expect(
    writes.filter((w) => w.url.pathname.endsWith("/settings/enhancements"))[0]
      .body,
  ).toEqual({
    expected_revision: initial.revision,
    feature: "work_recommendations",
    enabled: true,
  });
  await series.click();
  await expect(series).toBeChecked();
  await expect(series).toBeEnabled();
  await page
    .getByRole("button", { name: "展开中文译库配置", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "安装／更新中文译库", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("尚未安装", { exact: true })).toBeVisible();
  expect(
    writes.some((w) => w.url.pathname.endsWith("/tag-translation-jobs")),
  ).toBe(false);
  expect(reads.some((u) => u.pathname === "/api/v1/tags")).toBe(false);
  await screenshot("dictionary");
  const lib = await library();
  await library("anonymous-work");
  await page.reload(); // Libraries were created by the isolated API fixture.
  await expect(work).toBeChecked();
  await nav("版本");
  await expect(
    page.getByRole("button", { name: "查找版本", exact: true }),
  ).toBeEnabled();
  const caps = await get("/settings/work-recommendations");
  expect(caps.available).toBe(false);
  const result = page.waitForResponse(
    (r) =>
      r.url().endsWith("/api/v1/work-discovery-jobs") &&
      r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "查找版本", exact: true }).click();
  const jobID = (await (await result).json()).job_id;
  await nav("设置");
  await category("增强功能");
  await expect(work).toBeEnabled();
  const beforeCancel = writes.filter((w) =>
    w.url.pathname.endsWith("/settings/enhancements"),
  ).length;
  await work.click();
  const confirmation = page.getByRole("dialog", {
    name: "关闭版本推荐？",
    exact: true,
  });
  await expect(confirmation).toContainText("将暂停");
  await confirmation.getByRole("button", { name: "取消", exact: true }).click();
  expect(
    writes.filter((w) => w.url.pathname.endsWith("/settings/enhancements"))
      .length,
  ).toBe(beforeCancel);
  await expect(work).toBeChecked();
  await work.click();
  await expect(confirmation).toBeVisible();
  await screenshot("pause-confirmation");
  await confirmation
    .getByRole("button", { name: /确认暂停.*任务并关闭/ })
    .click();
  await expect(work).not.toBeChecked();
  await expect(work).toBeEnabled();
  await expect
    .poll(async () => (await get(`/jobs/${jobID}`)).state)
    .toBe("paused");
  const pause = writes
    .filter((w) => w.url.pathname.endsWith("/settings/enhancements"))
    .at(-1).body;
  expect(pause.confirm_pause).toBe(true);
  expect(pause.impact_token).toBeTruthy();
  await work.click();
  await expect(work).toBeChecked();
  await expect(work).toBeEnabled();
  expect((await get(`/jobs/${jobID}`)).state).toBe("paused");
  await page.reload();
  await expect(work).toBeChecked();
  await expect(series).toBeChecked();
  await work.click();
  await expect(work).not.toBeChecked();
  await expect(work).toBeEnabled(); // no queued/running: no confirmation
  await page.goto(
    `/admin/maintenance?section=tasks&task=${jobID}&library_id=${lib.id}`,
  );
  await expect(
    page.getByRole("button", { name: "恢复任务", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("link", { name: "增强设置", exact: true }),
  ).toBeVisible();
  await nav("系列");
  await expect(
    page.getByRole("button", { name: "查找系列", exact: true }),
  ).toBeEnabled();
  await nav("版本");
  await expect(
    page.getByRole("button", { name: "查找版本", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "添加版本", exact: true }),
  ).toHaveCount(0); // no selected group; manual navigation remains
  await expect(page.getByText("已有组", { exact: true })).toBeVisible();
  // Disabling recommendations must not disable explicit manual organization.
  const books = (await get(`/editions?library_id=${lib.id}`)).items,
    context = await get(
      `/work-organization/context?edition_ids=${books[0].id},${books[1].id}`,
    ),
    target = context.editions.find((e) => e.id === books[0].id).work_id;
  await write(`/works/${target}/merge`, {
    action: "work_merge",
    id: target,
    work_ids: [context.editions.find((e) => e.id === books[1].id).work_id],
    expected_revisions: context.expected_revisions,
  });
  await page.goto(`/admin/versions?library_id=${lib.id}&work=${target}`);
  await page
    .getByRole("button", { name: "版本组更多操作", exact: true })
    .click();
  await expect(
    page.getByRole("menuitem", { name: "加入版本", exact: true }),
  ).not.toHaveAttribute("aria-disabled", "true");
  await page.getByRole("menuitem", { name: "加入版本", exact: true }).click();
  const manual = page.getByRole("dialog", { name: /^加入版本 ·/ });
  await expect(manual).toBeVisible();
  await manual.getByRole("button", { name: "取消", exact: true }).click();
  await nav("设置");
  await category("元数据与网络");
  const proxy = page.getByRole("textbox", { name: "代理出口", exact: true });
  await proxy.fill("http://127.0.0.1:4444");
  const urlBefore = page.url();
  await category("账号与安全");
  const guard = page.getByRole("dialog", {
    name: "有尚未保存的修改",
    exact: true,
  });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
  expect(page.url()).toBe(urlBefore);
  await expect(proxy).toHaveValue("http://127.0.0.1:4444");
  await page
    .getByRole("button", { name: "刷新元数据设置", exact: true })
    .click();
  await expect(proxy).toHaveValue("http://127.0.0.1:4444");
  await page.getByLabel("Cookie：ipb_pass_hash操作", { exact: true }).click();
  await page.getByRole("option", { name: "替换", exact: true }).click();
  const secret = page.getByLabel("Cookie：ipb_pass_hash", { exact: true });
  await secret.fill("anonymous-write-only-value");
  await page.getByRole("button", { name: "保存凭据", exact: true }).click();
  await expect(secret).toHaveCount(0);
  await expect(proxy).toHaveValue("http://127.0.0.1:4444");
  await expect(
    page.getByText("站点设置版本已变化，草稿保留", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "核对后按当前版本保存草稿", exact: true })
    .click();
  await page
    .getByRole("button", { name: "保存站点与网络", exact: true })
    .click();
  await expect(
    page.getByText("站点与网络已保存", { exact: true }),
  ).toBeVisible();
  const persisted = await get("/settings/metadata-provider");
  expect(persisted.proxy_url).toBe("http://127.0.0.1:4444");
  expect(JSON.stringify(persisted)).not.toContain("anonymous-write-only-value");
  await screenshot("metadata");
  expect(
    writes.some((w) =>
      w.url.pathname.endsWith("/settings/metadata-provider/test"),
    ),
  ).toBe(false);
  await category("账号与安全");
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await category("概览");
  await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(page.getByLabel("当前密码", { exact: true })).toHaveValue(
    password,
  );
  await screenshot("security");
  await category("概览");
  await guard
    .getByRole("button", { name: "放弃修改并离开", exact: true })
    .click();
  await expect(page.getByText(service.version, { exact: true })).toBeVisible();
  const mark = reads.length;
  await page.waitForTimeout(1700);
  expect(
    reads
      .slice(mark)
      .filter(
        (u) =>
          u.pathname.endsWith("/settings/enhancements") ||
          u.pathname.endsWith("/settings/tag-translations") ||
          u.pathname.endsWith("/settings/metadata-provider"),
      ),
  ).toHaveLength(0);
  await page.goto("/admin/settings?section=translations");
  await expect(page).toHaveURL(/section=enhancements/);
  await expect(
    page.getByRole("button", { name: "安装／更新中文译库", exact: true }),
  ).toBeVisible();
  await page.goto("/settings/metadata?view=secrets");
  await expect(page).toHaveURL(/section=metadata/);
  await expect(page.locator("#settings-credentials")).toBeFocused();
  expect(errors).toEqual([]);
});
