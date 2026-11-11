import { enableAnonymousRecommendations } from "./enhancement-fixture.mjs";
import { test, expect } from "@playwright/test";
import { copyFile, readFile } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);

test("系列工作台：分页草稿、连续处理、原子组成、拖动与整条加入", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  const state = JSON.parse(
    await readFile(
      path.resolve(
        import.meta.dirname,
        `../../../.runtime/tests/e2e-state-${new URL(baseURL).port}.json`,
      ),
      "utf8",
    ),
  );
  expect(
    state.run_directory.startsWith(
      path.resolve(import.meta.dirname, "../../../.runtime/tests/runs/e2e-"),
    ),
  ).toBe(true);
  for (let i = 3; i < 31; i++)
    await copyFile(
      path.join(state.run_directory, "library", "合成1.cbz"),
      path.join(state.run_directory, "library", `匿名${i}.cbz`),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(state.token_file, "utf8"),
      password: "Anonymous series workspace password 2026",
    },
  });
  expect(setup.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await setup.json()).csrf_token,
  };
  await enableAnonymousRecommendations(page, baseURL, headers);
  async function get(url) {
    const r = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(r.status(), url).toBe(200);
    return r.json();
  }
  async function post(url, data = {}, status = 200) {
    const r = await page.request.post(`${baseURL}/api/v1${url}`, {
      headers,
      data,
    });
    expect(r.status(), url).toBe(status);
    return r.json();
  }
  const lib = await post(
    "/libraries",
    { root_key: "anonymous", display_name: "匿名系列工作台" },
    201,
  );
  const scan = await post(`/libraries/${lib.id}/scans`, {}, 202);
  await expect(async () =>
    expect((await get(`/jobs/${scan.job_id}`)).state).toBe("completed"),
  ).toPass({ timeout: 15000 });
  const works = (await get(`/works?library_id=${lib.id}&page_size=100`)).items;
  expect(works).toHaveLength(31);
  for (let i = 0; i < works.length; i++) {
    const b = await get(`/books/${works[i].preferred_edition_id}`);
    const r = await page.request.patch(
      `${baseURL}/api/v1/books/${b.id}/overrides`,
      {
        headers,
        data: {
          expected_revision: b.revision,
          fields: {
            title: { op: "set", value: `Translation ${i}` },
            title_original: {
              op: "set",
              value: `[Circle] 匿名作品 ${i} [日本語]`,
            },
          },
        },
      },
    );
    expect(r.status()).toBe(200);
  }
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const writes = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/series-proposals/"))
      writes.push({ url: r.url(), body: r.postDataJSON() });
  });
  await page.goto(`/admin/series?library=${lib.id}`);
  if (info.project.name === "mobile")
    await page
      .locator(".version-region-switch .ant-segmented-item")
      .filter({ hasText: "成员与预览" })
      .click();
  await expect(
    page.getByRole("heading", { name: "选择一个系列", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".version-selection-empty .ant-empty-image"),
  ).toBeVisible();
  const j = await post("/series-discovery-jobs", { library_id: lib.id }, 202);
  await expect(async () =>
    expect((await get(`/jobs/${j.job_id}`)).state).toBe("completed"),
  ).toPass({ timeout: 60000 });
  const plans = (
    await get(`/series-proposals?library_id=${lib.id}&kind=create_series`)
  ).items;
  expect(plans).toHaveLength(2);
  const small = plans.find((p) => p.member_count === 3),
    large = plans.find((p) => p.member_count === 28);
  await page.goto(
    `/admin/series?library=${lib.id}&tab=new&proposal=${encodeURIComponent(small.id)}`,
  );
  let editor = page.getByLabel("系列方案", { exact: true });
  async function continueCompletedNavigation() {
    const guard = page.getByRole("dialog", {
      name: "操作已完成，继续切换？",
      exact: true,
    });
    await expect(guard).toBeVisible();
    await guard.getByRole("button", { name: "继续切换", exact: true }).click();
    await expect(guard).not.toBeVisible();
  }
  await expect(editor.getByRole("checkbox")).toHaveCount(3);
  await editor
    .getByRole("button", { name: "锁定连续处理", exact: true })
    .click();
  await editor.getByRole("button", { name: "忽略方案", exact: true }).click();
  await continueCompletedNavigation();
  await expect(page).toHaveURL(
    new RegExp(`proposal=${encodeURIComponent(large.id)}`),
  );
  await expect(editor.getByRole("checkbox")).toHaveCount(25);
  await editor
    .getByRole("textbox", { name: "系列名称", exact: true })
    .fill("匿名交互系列");
  await editor.getByRole("checkbox").last().uncheck();
  async function drag(handle, target) {
    await handle.scrollIntoViewIfNeeded();
    const a = await handle.boundingBox(),
      b = await target.boundingBox();
    if (info.project.name === "mobile") {
      const cdp = await page.context().newCDPSession(page);
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchStart",
        touchPoints: [{ x: a.x + a.width / 2, y: a.y + a.height / 2 }],
      });
      for (let i = 1; i <= 6; i++)
        await cdp.send("Input.dispatchTouchEvent", {
          type: "touchMove",
          touchPoints: [
            {
              x:
                a.x +
                a.width / 2 +
                ((b.x + b.width / 2 - a.x - a.width / 2) * i) / 6,
              y:
                a.y +
                a.height / 2 +
                ((b.y + b.height - 6 - a.y - a.height / 2) * i) / 6,
            },
          ],
        });
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchEnd",
        touchPoints: [],
      });
      await cdp.detach();
    } else {
      await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
      await page.mouse.down();
      await page.mouse.move(b.x + b.width / 2, b.y + b.height - 6, {
        steps: 8,
      });
      await page.mouse.up();
    }
  }
  let rows = editor.locator(".series-proposal-member");
  const firstId = await rows.first().getAttribute("data-series-drop-row"),
    secondId = await rows.nth(1).getAttribute("data-series-drop-row");
  await drag(rows.first().locator(".series-drag-handle"), rows.nth(1));
  await expect(rows.first()).toHaveAttribute("data-series-drop-row", secondId);
  await expect(rows.nth(1)).toHaveAttribute("data-series-drop-row", firstId);
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    editor.getByRole("textbox", { name: "系列名称", exact: true }),
  ).toHaveValue("匿名交互系列");
  await expect(editor.getByRole("checkbox").last()).not.toBeChecked();
  expect(writes.filter((w) => w.url.endsWith("/resolve"))).toHaveLength(0);
  const footer = editor.locator(".series-proposal-actions");
  const sizes = info.project.name === "desktop" ? [1280, 1440, 1680] : [390];
  for (const width of sizes) {
    await page.setViewportSize({
      width,
      height: info.project.name === "mobile" ? 844 : 900,
    });
    const box = await footer.boundingBox();
    expect(box.y + box.height).toBeLessThanOrEqual(
      page.viewportSize().height + 1,
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: info.outputPath(`series-proposal-${width}.png`),
    });
  }
  const accept = page.waitForResponse(
    (r) =>
      r
        .url()
        .endsWith(`/series-proposals/${encodeURIComponent(large.id)}/accept`) &&
      r.request().method() === "POST",
  );
  await editor.getByRole("button", { name: "组成系列", exact: true }).click();
  const response = await accept;
  expect(response.status()).toBe(200);
  expect(response.request().postDataJSON()).toMatchObject({
    customize: true,
    expected_revision: large.revision,
    name: "匿名交互系列",
  });
  expect(response.request().postDataJSON().work_ids).toHaveLength(27);
  const accepted = await response.json();
  await continueCompletedNavigation();
  await expect(page).toHaveURL(/tab=new/);
  await expect(editor).toHaveCount(0);
  await page.goto(
    `/admin/series?library=${lib.id}&series=${accepted.target_series_id}`,
  );
  const center = page.locator(".version-center");
  rows = center.locator(".series-member-row");
  await expect(rows).toHaveCount(25);
  await expect(
    center.getByRole("button", { name: "匿名交互系列", exact: true }),
  ).toBeVisible();
  await expect(
    center.getByRole("button", { name: /上移|下移|查看版本/ }),
  ).toHaveCount(0);
  for (const name of ["添加成员", "自动排序", "删除系列"])
    await expect(
      center.getByRole("button", { name, exact: true }),
    ).toBeVisible();
  await center
    .getByRole("button", { name: "匿名交互系列", exact: true })
    .click();
  const nameInput = center.getByRole("textbox", {
      name: "系列名称",
      exact: true,
    }),
    save = center.getByRole("button", { name: "保存系列名称", exact: true });
  await expect(save).toBeDisabled();
  await nameInput.fill("匿名修改系列");
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(nameInput).toHaveValue("匿名修改系列");
  let reject = true;
  await page.route("**/api/v1/series/*", (route) => {
    if (reject && route.request().method() === "PATCH") {
      reject = false;
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "revision_conflict", message: "匿名名称冲突" },
        }),
      });
    }
    return route.continue();
  });
  await save.click();
  await expect(center.getByText("匿名名称冲突", { exact: true })).toBeVisible();
  await expect(save).toBeDisabled();
  await expect(nameInput).toHaveValue("匿名修改系列");
  await center
    .getByRole("button", { name: "重新核对名称", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "重新核对系列名称？", exact: true })
    .getByRole("button", { name: "重新核对", exact: true })
    .click();
  await save.click();
  await expect(
    center.getByRole("button", { name: "匿名修改系列", exact: true }),
  ).toBeVisible();
  const member = await rows.first().getAttribute("data-series-drop-row");
  let handle = rows.first().locator(".series-drag-handle");
  await handle.focus();
  await handle.press("Space");
  await handle.press("End");
  await handle.press("Enter");
  await expect(async () =>
    expect(
      (
        await get(`/series/${accepted.target_series_id}?summary=false`)
      ).work_ids.at(-1),
    ).toBe(member),
  ).toPass({ timeout: 10000 });
  await expect(rows).toHaveCount(2);
  handle = rows.last().locator(".series-drag-handle");
  await handle.focus();
  await handle.press("Space");
  await handle.press("Home");
  await handle.press("Enter");
  await expect(async () =>
    expect(
      (await get(`/series/${accepted.target_series_id}?summary=false`))
        .work_ids[0],
    ).toBe(member),
  ).toPass({ timeout: 10000 });
  await expect(rows).toHaveCount(25);
  const candidates = center.locator(".version-candidates-below");
  await expect(candidates).toBeVisible({ timeout: 15000 });
  await expect(
    candidates.getByRole("button", { name: "预览加入", exact: true }),
  ).toHaveCount(0);
  let joinPlan = (
    await get(
      `/series-proposals?library_id=${lib.id}&kind=add_to_series&target_series_id=${accepted.target_series_id}&alternatives=true`,
    )
  ).items[0];
  expect(joinPlan.new_work_ids).toHaveLength(4);
  await candidates
    .getByRole("button", { name: "打开漫画详情", exact: true })
    .first()
    .click();
  const detail = page.getByRole("dialog", { name: /漫画详情/ });
  await expect(detail).toBeVisible();
  await detail.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(detail).not.toBeVisible();
  const oldOrder = (
    await get(`/series/${accepted.target_series_id}?summary=false`)
  ).work_ids;
  handle = candidates.locator(".series-drag-handle").first();
  await handle.focus();
  await handle.press("Space");
  await handle.press("Home");
  const staleAccept = page.waitForResponse(
    (r) => r.url().endsWith("/accept") && r.request().method() === "POST",
  );
  await handle.press("Enter");
  expect((await staleAccept).status()).toBe(409);
  expect((await get(`/series/${accepted.target_series_id}`)).member_count).toBe(
    27,
  );
  await expect(candidates).toBeVisible();
  // Manual reordering must not silently rebase an old recommendation. Only an
  // explicit new anonymous discovery captures that changed organization.
  const refreshedJob = await post(
    "/series-discovery-jobs",
    { library_id: lib.id },
    202,
  );
  await expect(async () =>
    expect((await get(`/jobs/${refreshedJob.job_id}`)).state).toBe("completed"),
  ).toPass({ timeout: 60000 });
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  joinPlan = (
    await get(
      `/series-proposals?library_id=${lib.id}&kind=add_to_series&target_series_id=${accepted.target_series_id}&alternatives=true`,
    )
  ).items[0];
  await expect(
    page.getByTestId(`series-candidate-${joinPlan.id}`),
  ).toBeVisible();
  handle = page
    .getByTestId(`series-candidate-${joinPlan.id}`)
    .locator(".series-drag-handle");
  await handle.focus();
  await handle.press("Space");
  await handle.press("Home");
  await handle.press("Enter");
  await expect(async () =>
    expect(
      (await get(`/series/${accepted.target_series_id}?summary=false`))
        .member_count,
    ).toBe(31),
  ).toPass({ timeout: 10000 });
  const joined = await get(
    `/series/${accepted.target_series_id}?summary=false`,
  );
  expect(joined.work_ids.slice(4)).toEqual(oldOrder);
  expect(new Set(joined.work_ids.slice(0, 4))).toEqual(
    new Set(joinPlan.new_work_ids),
  );
  await page.getByRole("button", { name: "撤销", exact: true }).click();
  await expect(async () =>
    expect(
      (await get(`/series/${accepted.target_series_id}?summary=false`))
        .work_ids,
    ).toEqual(oldOrder),
  ).toPass({ timeout: 10000 });
  await expect(rows).toHaveCount(25);
  await page.screenshot({ path: info.outputPath("series-members.png") });
  expect(
    (await get(`/editions?library_id=${lib.id}&page_size=100`)).total,
  ).toBe(31);
  expect(errors).toEqual([]);
});
