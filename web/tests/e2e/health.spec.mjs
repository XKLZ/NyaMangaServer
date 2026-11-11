import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { healthFixture } from "./health-fixture.mjs";
installViteSource(test);
test("全局数据健康：真实检查、分页首选、冻结修复、维护保护与回退", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(240000);
  const { get, write, sql, finish, create, manyBooks } = await healthFixture(
    page,
    baseURL,
  );
  expect((await get("/settings/service")).database_schema).toBe(23);
  const requests = [],
    errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    if (r.method() === "POST")
      requests.push({ url: new URL(r.url()).pathname, body: r.postDataJSON() });
  });
  const target = "/admin/maintenance?section=health";
  const schema = "**/api/v1/settings/service";
  await page.route(schema, async (route) => {
    const response = await route.fetch(),
      json = await response.json();
    json.database_schema = 22;
    await route.fulfill({ response, json });
  });
  await page.goto(target);
  const area = page.getByRole("region", { name: "数据健康", exact: true });
  await expect(
    area.getByText("当前服务尚未支持数据健康，请升级后刷新。", { exact: true }),
  ).toBeVisible();
  await expect(
    area.getByRole("button", { name: "启动检查", exact: true }),
  ).toBeDisabled();
  await page.unroute(schema);
  await area.getByRole("button", { name: "刷新健康报告", exact: true }).click();
  await expect(
    area.getByRole("button", { name: "启动检查", exact: true }),
  ).toBeEnabled();
  await expect(page.getByText("还没有书库", { exact: true })).toHaveCount(0);
  await area.getByRole("button", { name: "启动检查", exact: true }).click();
  await expect(page).toHaveURL(/check=/);
  let check = new URL(page.url()).searchParams.get("check");
  await finish(check);
  expect(requests.find((r) => r.url.endsWith("/health-checks")).body.mode).toBe(
    "application",
  );
  expect(
    requests.find((r) => r.url.endsWith("/health-checks")).body.library_ids,
  ).toBeUndefined();
  // A completed quick check is still limited coverage, not an all-healthy declaration.
  await area.getByRole("combobox", { name: "检查模式" }).click();
  await page.getByRole("option", { name: "快速检查", exact: true }).click();
  await area.getByRole("button", { name: "启动检查", exact: true }).click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get("check"))
    .not.toBe(check);
  check = new URL(page.url()).searchParams.get("check");
  await finish(check);
  await expect(
    area.getByRole("heading", {
      name: "检查已结束，请核对范围与未检查项",
      exact: true,
    }),
  ).toBeVisible();
  // Build a real >25-member Work; its wrong preferred ID is valid globally but outside this Work.
  const { lib, books } = await manyBooks();
  const chosen = books.slice(0, 28),
    targetWork = chosen[0].work_id,
    outsider = books.at(-1);
  const context = await get(
    `/work-organization/context?work_ids=${chosen.map((v) => v.work_id).join(",")}`,
  );
  await write(
    `/works/${targetWork}/merge`,
    {
      action: "work_merge",
      id: targetWork,
      work_ids: chosen.slice(1).map((v) => v.work_id),
      preferred_edition_id: chosen[0].id,
      expected_revisions: context.expected_revisions,
    },
    "POST",
    200,
  );
  sql(
    "UPDATE works SET preferred_edition_id=?,revision=revision+1 WHERE id=?",
    [outsider.id, targetWork],
    false,
  );
  // An offline, unarmed recommendation queue item is paused, never executed or auto-resumed.
  sql(
    "INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('anonymous-paused-business','work_recommendation','queued','processing',1,unixepoch(),unixepoch())",
  );
  const report = await create();
  await page.goto(target + `&check=${report.id}&health_status=confirmed`);
  const rows = area.locator(".health-report .ant-table-tbody > tr");
  await expect(rows.filter({ hasText: "主版本不属于当前作品" })).toHaveCount(1);
  await rows
    .filter({ hasText: "主版本不属于当前作品" })
    .getByRole("checkbox")
    .check();
  const preferred = area.locator(".health-preferred");
  await expect(
    preferred.getByText("必须明确选择本作品的一个版本", { exact: true }),
  ).toBeVisible();
  await expect(
    area.getByRole("button", { name: "预览修复影响", exact: true }),
  ).toBeDisabled();
  await preferred.locator(".ant-pagination-item-2").click();
  await expect(preferred.getByRole("radio")).toHaveCount(3);
  await preferred.getByRole("radio").last().check();
  await area.getByRole("button", { name: "刷新健康报告", exact: true }).click();
  await expect(
    area.getByText("已选 1 项（最多100项）", { exact: true }),
  ).toBeVisible();
  const preview = area.getByRole("button", {
    name: "预览修复影响",
    exact: true,
  });
  await expect(preview).toBeEnabled();
  await preview.click();
  let modal = page.getByRole("dialog", { name: "确认修复影响", exact: true });
  await expect(modal).toContainText("一份数据库回退快照");
  await expect(modal).toContainText("暂停 1 个任务");
  const plans = requests.filter((r) => r.url.endsWith("/health-repair-plans"));
  expect(plans.at(-1).body.preferred_editions[targetWork]).toBeTruthy();
  const planRequest = await get(`/maintenance/health-checks/${report.id}`);
  expect(planRequest.state).toBe("completed");
  // Expire the stored plan without modifying the frozen report or selected member.
  sql(
    "UPDATE health_repair_plans SET expires_at=0,summary_json=json_set(summary_json,'$.expires_at',0) WHERE check_id=?",
    [report.id],
  );
  await modal
    .getByRole("button", { name: "确认维护并修复", exact: true })
    .click();
  await expect(
    modal.getByText(
      "计划已过期或对象发生变化，选择保留。返回选择后明确重建预览。",
      { exact: true },
    ),
  ).toBeVisible();
  await modal.getByRole("button", { name: "返回选择", exact: true }).click();
  await expect(
    area.getByText("已选 1 项（最多100项）", { exact: true }),
  ).toBeVisible();
  await preview.click();
  modal = page.getByRole("dialog", { name: "确认修复影响", exact: true });
  await modal
    .getByRole("button", { name: "确认维护并修复", exact: true })
    .click();
  await expect(page).toHaveURL(/repair=/);
  const repairID = new URL(page.url()).searchParams.get("repair");
  const repair = area.getByRole("region", {
    name: "维护执行与恢复",
    exact: true,
  });
  await expect(
    repair.getByRole("button", { name: "暂停维护", exact: true }),
  ).toBeVisible();
  await expect(
    repair.getByRole("button", { name: "继续维护", exact: true }),
  ).toBeDisabled();
  await repair.getByRole("button", { name: "暂停维护", exact: true }).click();
  await expect
    .poll(async () => (await get(`/jobs/${repairID}`)).state, {
      timeout: 20000,
    })
    .toBe("paused");
  await expect(
    repair.getByRole("button", { name: "继续维护", exact: true }),
  ).toBeEnabled();
  // A failed state refresh keeps protection; ordinary writes never turn into an unlock.
  const stateRoute = "**/api/v1/maintenance/health-state";
  await page.route(stateRoute, (r) =>
    r.fulfill({
      status: 503,
      json: {
        error: {
          code: "health_unavailable",
          message: "anonymous state unavailable",
        },
      },
    }),
  );
  await repair
    .getByRole("button", { name: "重新核对状态", exact: true })
    .click();
  await expect(
    page.getByText("维护状态暂不可读，继续保留写入保护", { exact: true }),
  ).toBeVisible();
  await page.unroute(stateRoute);
  await repair
    .getByRole("button", { name: "重新核对状态", exact: true })
    .click();
  await expect(
    repair.getByRole("button", { name: "继续维护", exact: true }),
  ).toBeEnabled();
  await repair.getByRole("button", { name: "继续维护", exact: true }).click();
  await expect(
    repair.getByRole("button", { name: "取消维护", exact: true }),
  ).toBeVisible();
  await repair.getByRole("button", { name: "取消维护", exact: true }).click();
  await expect
    .poll(async () => (await get(`/jobs/${repairID}`)).state)
    .toBe("cancelled");
  await expect(
    repair.getByRole("button", { name: "继续维护", exact: true }),
  ).toBeEnabled();
  await expect(
    repair.getByText("全局写入保护保留", { exact: true }),
  ).toBeVisible();
  await repair.getByRole("button", { name: "继续维护", exact: true }).click();
  await expect
    .poll(
      async () => (await get(`/maintenance/health-repairs/${repairID}`)).state,
      { timeout: 30000 },
    )
    .toBe("completed");
  await expect(
    repair.getByText("写入保护已解除", { exact: true }),
  ).toBeVisible();
  await expect(
    repair.getByRole("button", { name: "回退此次修复", exact: true }),
  ).toBeEnabled();
  expect((await get("/jobs/anonymous-paused-business")).state).toBe("paused");
  expect(
    (await get(`/maintenance/health-repairs/${repairID}`)).paused_jobs.some(
      (v) => v.id === "anonymous-paused-business",
    ),
  ).toBe(true);
  // Completed work remains reachable from server-paginated maintenance task history.
  await page.goto(target);
  if (info.project.name === "mobile")
    await area.locator(".health-history > summary").click();
  await area.getByText("修复与维护任务历史", { exact: true }).click();
  const history = area.getByRole("region", {
    name: "维护任务历史",
    exact: true,
  });
  await history.locator("button.health-history-item").first().click();
  await history
    .getByRole("button", { name: "查看修复与回退", exact: true })
    .click();
  await expect(page).toHaveURL(new RegExp(`repair=${repairID}`));
  await repair
    .getByRole("button", { name: "回退此次修复", exact: true })
    .click();
  const rollback = page.getByRole("dialog", {
    name: "确认回退此次修复",
    exact: true,
  });
  await expect(rollback).toContainText("后续编辑冲突不会被覆盖");
  await rollback.getByRole("button", { name: "确认回退", exact: true }).click();
  await expect
    .poll(
      async () => (await get(`/maintenance/health-repairs/${repairID}`)).state,
      { timeout: 30000 },
    )
    .toBe("rolled_back");
  await expect(
    repair.getByRole("button", { name: "回退此次修复", exact: true }),
  ).toBeDisabled();
  // Source mode requires explicit libraries and exposes the real attempted-payload budget.
  await area.getByRole("combobox", { name: "检查模式" }).click();
  await page.getByRole("option", { name: "源文件深检", exact: true }).click();
  await area.getByRole("combobox", { name: "深检书库" }).click();
  await page
    .getByRole("option", { name: lib.display_name, exact: true })
    .click();
  await page.keyboard.press("Escape");
  await expect(
    area.getByRole("spinbutton", { name: "深检读取预算GiB" }),
  ).toHaveValue("8");
  await area.getByRole("button", { name: "启动检查", exact: true }).click();
  await expect(page).toHaveURL(/check=/);
  const sourceID = new URL(page.url()).searchParams.get("check");
  await finish(sourceID);
  const sourceInput = requests
    .filter((r) => r.url.endsWith("/health-checks"))
    .at(-1).body;
  expect(sourceInput.library_ids).toEqual([lib.id]);
  expect(sourceInput.max_bytes).toBe(8 * 1024 ** 3);
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
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
      });
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBe(width);
      await page.screenshot({
        path: path.join(
          screenshots,
          `${info.project.name}-report-${width}-${colorScheme}.png`,
        ),
        animations: "disabled",
      });
    }
  }
  expect(errors).toEqual([]);
});
