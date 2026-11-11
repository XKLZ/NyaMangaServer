import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { healthFixture } from "./health-fixture.mjs";
installViteSource(test);
test("跨页草稿与上限原子选择、成功后仍保护、全局只读及无owner恢复", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  const { get, write, sql, create, finish, library } = await healthFixture(
    page,
    baseURL,
  );
  const reads = [];
  page.on("request", (r) => {
    if (r.method() === "GET") reads.push(new URL(r.url()).pathname);
  });
  const first = await create("quick"),
    target = "/admin/maintenance?section=health";
  const synthetic = Array.from({ length: 110 }, (_, i) => ({
    id: `synthetic-${i}`,
    check_id: first.id,
    category: "projections",
    object_kind: "edition",
    object_id: `anonymous-edition-${i}`,
    object_revision: 1,
    fingerprint: "anonymous-frozen",
    status: "confirmed",
    code: "effective_mismatch",
    repair: "projection",
    details: {},
    checked_at: Math.floor(Date.now() / 1000),
  }));
  // Controlled synthetic report is only for cross-page selection; it is never submitted for repair.
  await page.route(
    (url) =>
      url.pathname === `/api/v1/maintenance/health-checks/${first.id}/findings`,
    (route) => {
      const page = Number(
        new URL(route.request().url()).searchParams.get("page") || 1,
      );
      return route.fulfill({
        json: {
          items: synthetic.slice((page - 1) * 25, page * 25),
          total: 110,
          page,
          page_size: 25,
        },
      });
    },
  );
  await page.goto(target + `&check=${first.id}`);
  const area = page.getByRole("region", { name: "数据健康", exact: true }),
    report = area.locator(".health-report"),
    pager = report.locator(":scope > .ant-pagination");
  await expect(report.locator("tbody tr")).toHaveCount(25);
  reads.length = 0;
  const refreshed = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname ===
      `/api/v1/maintenance/health-checks/${first.id}/findings`,
  );
  await page.evaluate(() => {
    for (let i = 0; i < 500; i++)
      window.dispatchEvent(
        new CustomEvent("resources-changed", {
          detail: { type: "health_state" },
        }),
      );
  });
  await refreshed;
  expect(
    reads.filter(
      (p) => p === `/api/v1/maintenance/health-checks/${first.id}/findings`,
    ).length,
  ).toBeLessThanOrEqual(2);
  expect(
    reads.filter((p) => p === "/api/v1/maintenance/health-state").length,
  ).toBeLessThanOrEqual(2);

  for (let p = 1; p <= 3; p++) {
    if (p > 1) await pager.locator(`.ant-pagination-item-${p}`).click();
    await expect(report.locator("tbody tr").first()).toHaveAttribute(
      "data-row-key",
      `synthetic-${(p - 1) * 25}`,
    );
    await report.locator("thead").getByRole("checkbox").check();
  }
  await pager.locator(".ant-pagination-item-4").click();
  await expect(report.locator("tbody tr").first()).toHaveAttribute(
    "data-row-key",
    "synthetic-75",
  );
  for (const checkbox of (
    await report.locator("tbody").getByRole("checkbox").all()
  ).slice(0, 23))
    await checkbox.check();
  await expect(
    area.getByText("已选 98 项（最多100项）", { exact: true }),
  ).toBeVisible();
  await pager.locator(".ant-pagination-item-5").click();
  await expect(report.locator("tbody tr").first()).toHaveAttribute(
    "data-row-key",
    "synthetic-100",
  );
  await report.locator("thead").getByRole("checkbox").click();
  await expect(
    area.getByText("最多选择100项。本次整页选择未加入，请减少选择后重试。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    area.getByText("已选 98 项（最多100项）", { exact: true }),
  ).toBeVisible();
  // Successful new check must not release a still-dirty draft belonging to the prior check.
  await area.getByRole("button", { name: "启动检查", exact: true }).click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get("check"))
    .not.toBe(first.id);
  await finish(new URL(page.url()).searchParams.get("check"));
  async function guard() {
    await page.getByRole("tab", { name: "存储与清理", exact: true }).click();
    const prompt = page.getByRole("dialog", {
      name: "有尚未保存的修改",
      exact: true,
    });
    await expect(prompt).toBeVisible();
    await prompt.getByRole("button", { name: "继续编辑", exact: true }).click();
  }
  await guard();
  await area.getByRole("button", { name: "刷新健康报告", exact: true }).click();
  await guard();
  async function openHistory() {
    const summary = area.locator(".health-history > summary");
    if (
      info.project.name === "mobile" &&
      !(await area.locator(".health-history").evaluate((n) => n.open))
    )
      await summary.click();
  }
  await openHistory();
  await area.locator(`[data-check-id="${first.id}"]`).click();
  await expect(
    area.getByText("已选 98 项（最多100项）", { exact: true }),
  ).toBeVisible();
  const lib = await library(),
    books = (await get(`/editions?library_id=${lib.id}&page_size=100`)).items;
  sql("DELETE FROM effective_edition_properties WHERE edition_id=?", [
    books[0].id,
  ]);
  const actual = await create();
  await area.getByRole("button", { name: "刷新健康报告", exact: true }).click();
  await openHistory();
  await area.locator(`[data-check-id="${actual.id}"]`).click();
  await expect(page).toHaveURL(new RegExp(`check=${actual.id}`));
  const actualFinding = (
    await get(
      `/maintenance/health-checks/${actual.id}/findings?status=confirmed&page_size=100`,
    )
  ).items.find((v) => v.repair === "projection");
  expect(actualFinding).toBeTruthy();
  await expect(
    report.locator(`tbody tr[data-row-key="${actualFinding.id}"]`),
  ).toBeVisible();
  if (info.project.name === "mobile") {
    await area.locator(".health-history > summary").click();
    await expect(area.locator(".health-history")).not.toHaveAttribute("open");
    await page.evaluate(() => window.scrollTo(0, 0));
    const screenshots = path.resolve(
      import.meta.dirname,
      "../../../.runtime/tests/health-ui/screenshots",
    );
    await mkdir(screenshots, { recursive: true });
    for (const colorScheme of ["light", "dark"]) {
      await page.emulateMedia({ colorScheme });
      await expect(page.locator(".admin-shell")).toHaveClass(
        new RegExp(colorScheme),
      );
      await page.screenshot({
        path: path.join(
          screenshots,
          `mobile-report-collapsed-390-${colorScheme}.png`,
        ),
        animations: "disabled",
      });
    }
  }
  await report
    .locator("tbody tr")
    .filter({ hasText: "重建元数据与搜索" })
    .getByRole("checkbox")
    .check();
  await area.getByRole("button", { name: "预览修复影响", exact: true }).click();
  await page
    .getByRole("dialog", { name: "确认修复影响", exact: true })
    .getByRole("button", { name: "确认维护并修复", exact: true })
    .click();
  await expect(page).toHaveURL(/repair=/);
  const rid = new URL(page.url()).searchParams.get("repair");
  await expect
    .poll(async () => (await get(`/maintenance/health-repairs/${rid}`)).state, {
      timeout: 30000,
    })
    .toBe("completed");
  await guard();
  await area.getByRole("button", { name: "刷新健康报告", exact: true }).click();
  await guard();
  await openHistory();
  await area.getByText("修复与维护任务历史", { exact: true }).click();
  const history = area.getByRole("region", {
    name: "维护任务历史",
    exact: true,
  });
  const checkLookup = `**/api/v1/maintenance/health-checks/${first.id}`;
  await page.route(checkLookup, (r) =>
    r.fulfill({
      status: 503,
      json: {
        error: {
          code: "health_lookup_unavailable",
          message: "匿名历史读取失败",
        },
      },
    }),
  );
  await history.locator(`[data-task-id="${first.id}"]`).click();
  await expect(
    history.getByText("匿名历史读取失败", { exact: true }),
  ).toBeVisible();
  await expect(
    history.getByRole("link", { name: "查看其他维护任务", exact: true }),
  ).toHaveCount(0);
  await page.unroute(checkLookup);
  await page.route(checkLookup, (r) =>
    r.fulfill({
      status: 404,
      json: { error: { code: "not_found", message: "匿名缺失报告" } },
    }),
  );
  await history.getByRole("button", { name: "重新加载", exact: true }).click();
  await expect(
    history.getByRole("link", { name: "查看其他维护任务", exact: true }),
  ).toBeVisible();
  await page.unroute(checkLookup);

  await area.locator(`[data-check-id="${first.id}"]`).click();
  await expect(
    area.getByText("已选 98 项（最多100项）", { exact: true }),
  ).toBeVisible();
  await area.getByRole("button", { name: "清空选择", exact: true }).click();
  await page.getByRole("tab", { name: "存储与清理", exact: true }).click();
  await expect(area).not.toBeVisible();
  reads.length = 0;
  const globalRead = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/v1/maintenance/health-state",
  );
  await page.evaluate(() => {
    for (let i = 0; i < 100; i++)
      window.dispatchEvent(
        new CustomEvent("resources-changed", {
          detail: { type: "health_state" },
        }),
      );
  });
  await globalRead;
  expect(reads.some((p) => p.includes("/maintenance/health-checks"))).toBe(
    false,
  );
  await page.getByRole("tab", { name: "数据健康", exact: true }).click();
  await expect(area).toBeVisible();
  // Ownerless protection is real anonymous state, not an invented UI unlock.
  sql(
    "UPDATE health_state SET phase='recovery_required',repair_id=NULL,revision=revision+1 WHERE singleton=1",
  );
  await area.getByRole("button", { name: "刷新健康报告", exact: true }).click();
  await expect(page.getByText(/全局维护保护 · 维护保护待恢复/)).toBeVisible();
  const protectedWrite = await write("/series", {}, "POST", 503);
  expect(protectedWrite.error.code).toBe("maintenance_active");
  await page.evaluate(async () => {
    const moduleURL = performance
      .getEntriesByType("resource")
      .map((v) => v.name)
      .find((v) => new URL(v).pathname === "/src/shared/api.ts");
    if (!moduleURL) throw Error("Anonymous API module missing");
    const api = await import(moduleURL);
    await api.request("/tag-suggestions", "POST", { namespace: "artist" });
    await api.request("/admin/books/search", "POST", {
      page: 1,
      page_size: 25,
      collapse: "none",
      visibility: "active",
      include_disabled: true,
    });
    let rejected = false;
    try {
      await api.request("/series", "POST", {});
    } catch (error) {
      rejected = error.code === "maintenance_active";
    }
    if (!rejected) throw Error("Protected write was not blocked");
  });
  // Global status must not let an old completed repair impersonate the current owner.
  await expect(
    area
      .getByRole("region", { name: "维护执行与恢复", exact: true })
      .getByRole("button", { name: "回退此次修复", exact: true }),
  ).toBeDisabled();
  await page.goto(`/admin/books?library_id=${lib.id}&book=${books[0].id}`);
  const book = page.getByRole("dialog", { name: /漫画详情/ });
  await book.getByRole("tab", { name: "元数据", exact: true }).click();
  const title = book.getByLabel("其他标题", { exact: true });
  await title.fill("Anonymous protected draft");
  await book.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(title).toHaveValue("Anonymous protected draft");
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", {
        detail: { type: "health_state" },
      }),
    ),
  );
  await expect(title).toHaveValue("Anonymous protected draft");
  await book.getByRole("button", { name: "关闭", exact: true }).click();
  const discard = page.getByRole("dialog", {
    name: "放弃尚未保存的编辑？",
    exact: true,
  });
  await expect(discard).toBeVisible();
  await discard.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(title).toHaveValue("Anonymous protected draft");
  await book.getByRole("button", { name: "关闭", exact: true }).click();
  await discard
    .getByRole("button", { name: "放弃并关闭", exact: true })
    .click();
  await expect(book).not.toBeVisible();
  await page.goto(target + `&check=${first.id}`);
  await area
    .getByRole("button", { name: "核对并恢复写入保护", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "确认恢复写入保护", exact: true })
    .getByRole("button", { name: "确认恢复", exact: true })
    .click();
  await expect
    .poll(async () => (await get("/maintenance/health-state")).phase)
    .toBe("idle");
  await expect(page.getByText(/全局维护保护 ·/)).toHaveCount(0);
  const current = await get(`/books/${books[0].id}`);
  await write(
    `/books/${books[0].id}/overrides`,
    {
      expected_revision: current.revision,
      fields: {
        title: { op: "set", value: "Anonymous subsequent human edit" },
      },
    },
    "PATCH",
    200,
  );
  await page.goto(target + `&repair=${rid}`);
  const repair = area.getByRole("region", {
    name: "维护执行与恢复",
    exact: true,
  });
  await expect(
    repair.getByRole("button", { name: "回退此次修复", exact: true }),
  ).toBeEnabled();
  await repair
    .getByRole("button", { name: "回退此次修复", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "确认回退此次修复", exact: true })
    .getByRole("button", { name: "确认回退", exact: true })
    .click();
  await expect
    .poll(async () => (await get(`/maintenance/health-repairs/${rid}`)).state, {
      timeout: 20000,
    })
    .toBe("paused");
  expect((await get("/maintenance/health-state")).phase).not.toBe("idle");
  expect((await get(`/books/${books[0].id}`)).metadata.fields.title).toBe(
    "Anonymous subsequent human edit",
  );
  await expect(
    repair.getByText("全局写入保护保留", { exact: true }),
  ).toBeVisible();
  const screenshots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/tests/health-ui/screenshots",
  );
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({
    path: path.join(screenshots, `${info.project.name}-rollback-conflict.png`),
    animations: "disabled",
  });
});
