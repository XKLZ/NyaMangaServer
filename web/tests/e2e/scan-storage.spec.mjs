import { test, expect } from "@playwright/test";
import { mkdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { scanStorageFixture } from "./scan-storage-fixture.mjs";
import { routeViteSource } from "./vite-source.mjs";
const shots = path.resolve(
  import.meta.dirname,
  "../../../.runtime/tests/scan-storage-ui/screenshots",
);
async function screenshot(page, name, info) {
  await mkdir(shots, { recursive: true });
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    await expect(page.locator(".admin-shell")).toHaveClass(
      new RegExp(colorScheme),
    );
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
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
async function open(page, baseURL, url) {
  await page.goto(baseURL + url);
  await expect(page.locator(".admin-shell")).toBeVisible();
}

test("存储：真实冻结预览、一次执行、持久结果与素材保护", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(120000);
  await routeViteSource(page, baseURL);
  const f = await scanStorageFixture(page, baseURL);
  try {
    const protectedPath = path.join(
      f.config.data_dir,
      "assets/tags/fixture/retained.png",
    );
    const before = await readFile(protectedPath);
    await open(page, baseURL, "/admin/maintenance?section=storage");
    await expect(page.getByText("应用总占用", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: /长期资料/ }).click();
    await expect(page.getByText(/外部档案尚未核验/)).toBeVisible();
    await screenshot(page, "storage", info);
    await page.getByRole("button", { name: "清理建议", exact: true }).click();
    const modal = page.getByRole("dialog", { name: "清理建议", exact: true });
    // Keep default safe categories; user backup and referenced assets must survive.
    await modal.getByRole("button", { name: "生成清理预览" }).click();
    await expect(modal.getByText("预计释放", { exact: true })).toBeVisible();
    expect(new URL(page.url()).searchParams.get("cleanup_plan")).toBeTruthy();
    const planID = new URL(page.url()).searchParams.get("cleanup_plan");
    const preview = await f.get(`/maintenance/plans/${planID}`);
    expect(preview.input).toBeUndefined();
    expect(preview.summary.storage.planned.bytes).toBeGreaterThan(0);
    await modal.getByRole("button", { name: "确认清理", exact: true }).focus();
    await expect(
      modal.getByRole("button", { name: "确认清理", exact: true }),
    ).toBeFocused();
    await screenshot(page, "cleanup-preview", info);
    await modal.getByRole("button", { name: "确认清理", exact: true }).click();
    await expect(modal.getByText("实际释放", { exact: true })).toBeVisible();
    await expect
      .poll(
        async () =>
          (await f.get(`/maintenance/plans/${planID}`)).summary.cleanup_result
            ?.deleted_files || 0,
      )
      .toBeGreaterThan(0);
    const result = await f.get(`/maintenance/plans/${planID}`);
    await expect
      .poll(async () => (await f.get(`/jobs/${result.job_id}`)).state)
      .toBe("completed");
    await page.reload();
    await expect(
      page.getByRole("dialog").getByText("实际释放", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("清理全部完成", { exact: true })).toBeVisible();
    await screenshot(page, "cleanup-result", info);
    // Render the historical finalization failure without performing another cleanup.
    const planRoute = `**/api/v1/maintenance/plans/${planID}`;
    const jobRoute = `**/api/v1/jobs/${result.job_id}`;
    await page.route(planRoute, async (route) => {
      const response = await route.fetch(),
        body = await response.json();
      body.summary.cleanup_result.status = "finalization_failed";
      body.summary.cleanup_result.phase = "finalizing";
      body.summary.cleanup_result.error_code = "preview_expired";
      await route.fulfill({ response, json: body });
    });
    await page.route(jobRoute, async (route) => {
      const response = await route.fetch(),
        body = await response.json();
      body.state = "failed";
      body.error_code = "preview_expired";
      await route.fulfill({ response, json: body });
    });
    await page.reload();
    await expect(
      page.getByText("删除已完成，任务收尾失败", { exact: true }),
    ).toBeVisible();
    await screenshot(page, "cleanup-finalization-failure", info);
    await page.unroute(planRoute);
    await page.unroute(jobRoute);
    await page.reload();
    expect(await readFile(protectedPath)).toEqual(before);
    expect(
      (
        await stat(
          path.join(
            f.config.data_dir,
            "instance-recovery/anonymous-backup/archive.zip",
          ),
        )
      ).size,
    ).toBeGreaterThan(0);
    expect(
      (
        await stat(
          path.join(
            f.config.data_dir,
            "instance-recovery/anonymous-ended/unknown.txt",
          ),
        )
      ).size,
    ).toBeGreaterThan(0);
    await expect(
      async () =>
        await stat(path.join(f.config.data_dir, "tmp/anonymous-expired.bin")),
    ).rejects.toThrow();
    expect(
      (await f.get("/maintenance/restores/anonymous-ended")).operation
        .materials_cleaned,
    ).toBe(true);
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "关闭", exact: true })
      .last()
      .click();
    await page.goto(
      baseURL +
        "/admin/maintenance?section=recovery&kind=restores&operation=anonymous-ended",
    );
    await expect(page.getByText(/材料已清理/).last()).toBeVisible();
  } finally {
    await f.stop();
  }
});

test("扫描与编码：真实预算异常、候选CAS、页序封面和阅读", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(150000);
  await routeViteSource(page, baseURL);
  const f = await scanStorageFixture(page, baseURL);
  try {
    const scan = await f.write(
      "/libraries/fixture-library/scans",
      {},
      "POST",
      202,
    );
    await expect
      .poll(async () => (await f.get(`/jobs/${scan.job_id}`)).state, {
        timeout: 30000,
      })
      .toBe("completed");
    const items = (await f.get(`/jobs/${scan.job_id}/items?page_size=50`))
      .items;
    const encoding = items.find(
      (v) => v.relative_path === "anonymous-encoding.cbz",
    );
    const partial = items.find(
      (v) => v.relative_path === "anonymous-budget.cbz",
    );
    expect(partial.scan_result.classification).toBe("partial");
    expect(partial.scan_result.issues[0].resource).toBe(
      "estimated_memory_bytes",
    );
    expect(encoding.error_code).toBe("invalid_archive_encoding");
    await open(
      page,
      baseURL,
      `/admin/maintenance?section=tasks&task=${scan.job_id}&library=fixture-library`,
    );
    await expect(
      page.getByText("已入库，部分处理未完成", { exact: true }),
    ).toBeVisible();
    await screenshot(page, "scan-results", info);
    await page.getByRole("button", { name: "核对页名编码" }).click();
    const dialog = page.getByRole("dialog", { name: "核对本文件页名编码" });
    await expect(
      dialog.getByRole("radio", { name: "Shift-JIS", exact: true }),
    ).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: "保存本文件编码" }),
    ).toBeDisabled();
    await dialog.getByRole("radio", { name: "Shift-JIS", exact: true }).check();
    await expect(
      dialog.locator(".encoding-candidate li").first(),
    ).toBeVisible();
    expect(
      await dialog
        .locator(".encoding-candidate li")
        .first()
        .evaluate((n) => parseFloat(getComputedStyle(n).fontSize)),
    ).toBeGreaterThanOrEqual(14);
    await screenshot(page, "encoding-preview", info);
    const old = await f.get(`/editions/${encoding.book_id}`);
    // Real revision conflict while a selection is frozen, no automatic rebase.
    f.sql(
      `UPDATE editions SET revision=revision+1 WHERE id='${encoding.book_id}'`,
    );
    await dialog.getByRole("button", { name: "保存本文件编码" }).click();
    await expect(
      dialog.getByText("文件或解析依据已变化，原选择保留"),
    ).toBeVisible();
    await expect(
      dialog.getByRole("radio", { name: "Shift-JIS", exact: true }),
    ).toBeChecked();
    await expect(
      dialog.getByRole("button", { name: "保存本文件编码" }),
    ).toBeDisabled();
    await screenshot(page, "encoding-conflict", info);
    await dialog.getByRole("button", { name: "重新读取并核对" }).click();
    await expect(
      dialog.getByRole("radio", { name: "Shift-JIS", exact: true }),
    ).toBeEnabled();
    await dialog.getByRole("radio", { name: "Shift-JIS", exact: true }).check();
    await dialog.getByRole("button", { name: "保存本文件编码" }).click();
    await expect(
      dialog.getByText("本文件编码已保存", { exact: true }),
    ).toBeVisible();
    const book = await f.get(`/editions/${encoding.book_id}`);
    expect(book.content_revision).toBe(old.content_revision);
    expect(book.file.parsing_revision).toBeGreaterThan(1);
    await dialog.getByRole("button", { name: "返回", exact: true }).click();
    // Simulate old persisted reading coordinates; only the anonymous fixture is altered.
    f.sql(
      `INSERT INTO reading_states(account_id,edition_id,page_index,recorded_page_count,content_revision,completed,last_read_at,read_count,revision,parsing_revision) SELECT id,'${book.id}',0,2,${book.content_revision},0,1,3,1,1 FROM accounts WHERE singleton=1`,
    );
    await page.goto(
      baseURL + `/admin/books?library=fixture-library&book=${book.id}`,
    );
    const detail = page.locator(".book-dialog");
    await expect(detail).toBeVisible();
    await detail
      .getByRole("button", { name: "重置阅读状态", exact: true })
      .click();
    const reading = page.getByRole("dialog", {
      name: "重置阅读状态",
      exact: true,
    });
    await expect(
      reading.getByRole("button", { name: "保存阅读状态" }),
    ).toBeDisabled();
    await reading
      .getByRole("checkbox", { name: "已核对页序，接受并清除旧进度" })
      .check();
    await screenshot(page, "reading-parsing", info);
    const savedReading = page.waitForRequest(
      (r) => r.url().endsWith("/reading-state") && r.method() === "PUT",
    );
    await reading.getByRole("button", { name: "保存阅读状态" }).click();
    const body = (await savedReading).postDataJSON();
    expect(body.parsing_revision).toBe(book.file.parsing_revision);
    expect(body.accept_parsing_change).toBe(true);
    expect(body.accept_content_change).toBeUndefined();
    await expect(reading).toBeHidden();
    await detail.getByRole("tab", { name: "封面", exact: true }).click();
    const reads = [];
    page.on("request", (r) => {
      if (/\/pages(?:\/|\?)/.test(r.url())) reads.push(r.url());
    });
    await detail.getByRole("button", { name: "加载图片", exact: true }).click();
    await detail
      .getByRole("button", { name: "选择第 1 页封面", exact: true })
      .click();
    const coverRequest = page.waitForRequest(
      (r) => r.url().endsWith("/cover-selection") && r.method() === "PUT",
    );
    await detail.getByRole("button", { name: "应用封面" }).click();
    expect((await coverRequest).postDataJSON().parsing_revision).toBe(
      book.file.parsing_revision,
    );
    expect(reads.length).toBeGreaterThan(0);
    expect(
      reads.every(
        (u) =>
          new URL(u).searchParams.get("parsing_revision") ===
          String(book.file.parsing_revision),
      ),
    ).toBe(true);
    await expect(detail.getByText("已选择归档页作为封面")).toBeVisible();
    await screenshot(page, "cover-parsing", info);
    const readingState = (await f.get(`/editions/${book.id}`)).reading_state;
    expect(readingState.parsing_stale).toBe(false);
    expect(readingState.read_count).toBe(3);
    await detail
      .getByRole("button", { name: "关闭", exact: true })
      .first()
      .click();
    await page.goto(
      baseURL +
        `/admin/maintenance?section=tasks&task=${scan.job_id}&library=fixture-library`,
    );
    const budgetRow = page
      .getByRole("row")
      .filter({ hasText: "anonymous-budget.cbz" });
    await budgetRow.getByRole("checkbox").check();
    const retry = page.waitForResponse(
      (r) =>
        /\/jobs\/[^/]+\/retry$/.test(r.url()) &&
        r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "重试所选 1 项" }).click();
    const accepted = await (await retry).json();
    await expect
      .poll(async () => (await f.get(`/jobs/${accepted.job_id}`)).state)
      .toBe("completed");
    const retried = await f.get(`/jobs/${accepted.job_id}/items?page_size=50`);
    expect(retried.items).toHaveLength(1);
    expect(retried.items[0].relative_path).toBe("anonymous-budget.cbz");
    expect(retried.items[0].scan_result.stages.lookup).toBe("completed");
  } finally {
    await f.stop();
  }
});

test("清理保护：取消等待、迟到预览、过期计划与旧任务兼容", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(120000);
  await routeViteSource(page, baseURL);
  const f = await scanStorageFixture(page, baseURL);
  let release;
  try {
    await open(page, baseURL, "/admin/maintenance?section=storage");
    await page.getByRole("button", { name: "清理建议", exact: true }).click();
    const modal = page.getByRole("dialog", { name: "清理建议", exact: true });
    // Wait on a real preview response to exercise cancel and stale publication.
    const held = new Promise((r) => (release = r));
    let captured = null;
    await page.route("**/api/v1/maintenance/plans", async (route) => {
      const response = await route.fetch();
      captured = await response.json();
      await held;
      await route.fulfill({ response }).catch(() => {});
    });
    await modal.getByRole("button", { name: "生成清理预览" }).click();
    await expect.poll(() => captured?.id).toBeTruthy();
    await modal.getByRole("button", { name: "取消等待" }).click();
    await expect(
      modal.getByText("预览已取消，选择保留，未执行删除。"),
    ).toBeVisible();
    release();
    await page.unroute("**/api/v1/maintenance/plans");
    await expect(
      modal.getByRole("checkbox", { name: "过期临时文件", exact: true }),
    ).toBeChecked();
    expect(new URL(page.url()).searchParams.get("cleanup_plan")).toBeNull();
    expect(
      (await stat(path.join(f.config.data_dir, "tmp/anonymous-expired.bin")))
        .size,
    ).toBe(2048);
    await screenshot(page, "cleanup-cancel", info);
    // Bounded server errors preserve choices and never execute a plan.
    for (const [status, code, message] of [
      [504, "request_timeout", "预览超时并已停止"],
      [422, "cleanup_preview_capacity", "本次预览范围超过容量"],
    ]) {
      await page.route("**/api/v1/maintenance/plans", (route) =>
        route.fulfill({
          status,
          json: {
            error: { code, message: "anonymous injected preview error" },
          },
        }),
      );
      await modal.getByRole("button", { name: "生成清理预览" }).click();
      await expect(modal.getByText(new RegExp(message))).toBeVisible();
      await expect(
        modal.getByRole("checkbox", { name: "过期临时文件", exact: true }),
      ).toBeChecked();
      await page.unroute("**/api/v1/maintenance/plans");
    }
    // All material options deselected must never become server-side 'all'.
    while (
      await modal
        .locator(".storage-materials input[type=checkbox]:checked")
        .count()
    )
      await modal
        .locator(".storage-materials input[type=checkbox]:checked")
        .first()
        .uncheck();
    const previewCall = page.waitForRequest(
      (r) => r.url().endsWith("/maintenance/plans") && r.method() === "POST",
    );
    await modal.getByRole("button", { name: "生成清理预览" }).click();
    const body = (await previewCall).postDataJSON();
    expect(body.options.categories).not.toContain("instance_recovery");
    expect(body.options.categories).not.toContain("health_rollback");
    await expect(modal.getByText("预计释放", { exact: true })).toBeVisible();
    const id = new URL(page.url()).searchParams.get("cleanup_plan");
    f.sql(
      `UPDATE maintenance_plans SET expires_at=1,summary_json=json_set(summary_json,'$.expires_at',1) WHERE id='${id}'`,
    );
    await modal.getByRole("button", { name: "返回", exact: true }).click();
    await page.reload();
    await expect(
      modal.getByText("预览已过期，请重新生成并核对。"),
    ).toBeVisible();
    await expect(
      modal.getByRole("button", { name: "确认清理", exact: true }),
    ).toBeDisabled();
    await screenshot(page, "cleanup-expired", info);
    await modal.getByRole("button", { name: "修改选择" }).click();
    expect(new URL(page.url()).searchParams.get("cleanup_plan")).toBeNull();
    await expect(
      modal.getByRole("checkbox", { name: "过期临时文件", exact: true }),
    ).toBeVisible();
    await page.goBack();
    await expect(
      modal.getByText("预览已过期，请重新生成并核对。"),
    ).toBeVisible();
    await expect(
      modal.getByRole("button", { name: "确认清理", exact: true }),
    ).toBeDisabled();
    await modal.getByRole("button", { name: "返回", exact: true }).click();
    await page.goto(
      baseURL +
        "/admin/maintenance?section=tasks&task=fixture-completed&library=fixture-library",
    );
    await expect(page.getByRole("heading", { name: "书库扫描" })).toBeVisible();
    await expect(page.getByText("合成1.cbz", { exact: true })).toBeVisible();
    await expect(
      page.getByText("已入库，部分处理未完成", { exact: true }),
    ).toHaveCount(0);
  } finally {
    release?.();
    await f.stop();
  }
});

test("系列编号诊断：名称、实际起点与规则版本", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(120000);
  await routeViteSource(page, baseURL);
  const f = await scanStorageFixture(page, baseURL, { seriesOrigin: true });
  try {
    const report = await f.write(
      "/maintenance/health-checks",
      { request_key: "anonymous-series-origin-check", mode: "quick" },
      "POST",
      202,
    );
    await expect
      .poll(
        async () =>
          (await f.get(`/maintenance/health-checks/${report.id}`)).state,
        { timeout: 30000 },
      )
      .toBe("completed");
    await open(
      page,
      baseURL,
      `/admin/maintenance?section=health&check=${report.id}&health_status=confirmed`,
    );
    const row = page
      .locator(".health-report .ant-table-tbody > tr")
      .filter({ hasText: "系列编号起点不符合规则" });
    await expect(row).toContainText("匿名系列编号");
    await row.getByText("检测详情", { exact: true }).click();
    await expect(row).toContainText("实际起点");
    await expect(row).toContainText("预期起点");
    await screenshot(page, "series-origin", info);
  } finally {
    await f.stop();
  }
});
