import { test, expect } from "@playwright/test";
import { copyFile, readFile } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);

test("工作台成员：原文、下方候选、标签、主副切换、移出与整组拆分", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(120000);
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
  for (const file of ["匿名3.cbz", "匿名4.cbz"])
    await copyFile(
      path.join(state.run_directory, "library", "合成1.cbz"),
      path.join(state.run_directory, "library", file),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(state.token_file, "utf8"),
      password: "Anonymous workspace members password 2026",
    },
  });
  expect(setup.status()).toBe(200);
  const csrf = (await setup.json()).csrf_token;
  const headers = { Origin: baseURL, "X-CSRF-Token": csrf };
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
    { root_key: "anonymous", display_name: "匿名工作台" },
    201,
  );
  const scan = await post(`/libraries/${lib.id}/scans`, {}, 202);
  await expect(async () =>
    expect((await get(`/jobs/${scan.job_id}`)).state).toBe("completed"),
  ).toPass();
  const editions = (await get(`/editions?library_id=${lib.id}&page_size=100`))
    .items;
  expect(editions).toHaveLength(5);
  for (let i = 0; i < editions.length; i++) {
    const b = await get(`/books/${editions[i].id}`);
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
              value: `[Circle] 匿名の原文 ${i} [日本語]`,
            },
          },
        },
      },
    );
    expect(r.status()).toBe(200);
  }
  const ids = editions.slice(0, 2).map((e) => e.id),
    context = await get(
      `/work-organization/context?edition_ids=${ids.join(",")}`,
    );
  const work = context.editions.find((e) => e.id === ids[0]).work_id,
    other = context.editions.find((e) => e.id === ids[1]).work_id;
  await post(`/works/${work}/merge`, {
    action: "work_merge",
    id: work,
    work_ids: [other],
    expected_revisions: context.expected_revisions,
  });
  const discovery = await post(
    "/work-discovery-jobs",
    { library_id: lib.id },
    202,
  );
  await expect(async () =>
    expect((await get(`/jobs/${discovery.job_id}`)).state).toBe("completed"),
  ).toPass({ timeout: 15000 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`/admin/versions?library=${lib.id}`);
  if (info.project.name === "mobile")
    await page
      .locator(".version-region-switch .ant-segmented-item")
      .filter({ hasText: "成员与预览" })
      .click();
  await expect(
    page.getByRole("heading", { name: "选择一个版本组", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".version-selection-empty .ant-empty-image"),
  ).toBeVisible();
  await page.goto(`/admin/versions?library=${lib.id}&work=${work}`);
  const center = page.locator(".version-center"),
    rows = center.locator(".version-member"),
    candidates = center.locator(".version-candidates-below");
  await expect(rows).toHaveCount(2);
  await expect(
    rows
      .first()
      .getByRole("button", { name: /匿名の原文/ })
      .first(),
  ).toBeVisible();
  await expect(
    center.getByRole("heading", { name: /匿名の原文/ }),
  ).toBeVisible();
  await expect(
    page
      .locator(".version-left .version-group-row")
      .filter({ hasText: /Translation/ }),
  ).toHaveCount(0);
  await expect(candidates).toBeVisible();
  await expect(
    page.getByRole("button", { name: "查看加入候选", exact: true }),
  ).toHaveCount(0);
  if (info.project.name === "desktop")
    for (const width of [1280, 1440, 1680]) {
      await page.setViewportSize({ width, height: 900 });
      for (const collapsed of [false, true]) {
        const shell = page.locator(".admin-shell");
        if (
          (await shell.getAttribute("class")).includes(
            "navigation-collapsed",
          ) !== collapsed
        )
          await page
            .getByRole("button", {
              name: collapsed ? "收起管理导航" : "展开管理导航",
              exact: true,
            })
            .click();
        await expect
          .poll(async () =>
            Math.round(
              (await page.locator(".desktop-sidebar").boundingBox()).width,
            ),
          )
          .toBe(collapsed ? 64 : 218);
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        ).toBe(true);
        const member = await rows.last().boundingBox(),
          candidate = await candidates.boundingBox();
        expect(candidate.y).toBeGreaterThan(member.y);
        await page.screenshot({
          path: info.outputPath(`versions-${width}-${collapsed}.png`),
          fullPage: true,
        });
      }
    }
  const cover = rows
    .first()
    .getByRole("button", { name: "打开漫画详情", exact: true });
  await cover.click();
  const detail = page.getByRole("dialog", { name: /漫画详情/ });
  await expect(detail).toBeVisible();
  await detail.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(detail).not.toBeVisible();
  await expect(rows).toHaveCount(2);
  // Clipboard rejection must never report success.
  await page.evaluate(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: async () => {
          throw Error("anonymous denial");
        },
      },
    }),
  );
  await rows.first().locator(".version-member-title").click();
  await expect(
    page.getByText("复制失败，请检查浏览器剪贴板权限后重试", { exact: true }),
  ).toBeVisible();
  await page.evaluate(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: async (value) => {
          window.__copiedTitle = value;
        },
      },
    }),
  );
  await rows.first().locator(".version-member-title").click();
  await expect(page.getByText("标题已复制", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => window.__copiedTitle)).toMatch(
    /^匿名の原文/,
  );
  const edit = rows.first().getByRole("button", { name: /编辑 .* 的版本标签/ });
  await edit.focus();
  await edit.click();
  const pop = page.locator(".version-label-popover"),
    input = pop.getByRole("combobox");
  await input.fill("匿名标签");
  await input.press("Enter");
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    pop
      .locator(".ant-select-selection-item-content")
      .filter({ hasText: "匿名标签" }),
  ).toBeVisible();
  let rejectLabel = true;
  await page.route("**/api/v1/editions/*/version-labels", (route) => {
    if (rejectLabel && route.request().method() === "PUT") {
      rejectLabel = false;
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "revision_conflict", message: "匿名版本冲突" },
        }),
      });
    }
    return route.continue();
  });
  await pop.getByRole("button", { name: "保存", exact: true }).click();
  await expect(pop.getByText("匿名版本冲突", { exact: true })).toBeVisible();
  await expect(
    pop.getByRole("button", { name: "保存", exact: true }),
  ).toBeDisabled();
  await expect(
    pop
      .locator(".ant-select-selection-item-content")
      .filter({ hasText: "匿名标签" }),
  ).toBeVisible();
  await pop.getByRole("button", { name: "重新核对", exact: true }).click();
  const recheck = page.getByRole("dialog", {
    name: "核对当前版本标签",
    exact: true,
  });
  await recheck
    .getByRole("button", { name: "确认并保留输入", exact: true })
    .click();
  await pop.getByRole("button", { name: "保存", exact: true }).click();
  await expect(pop).not.toBeVisible();
  await rows.getByRole("button", { name: /设为主版本/ }).click();
  await expect(page.getByText("已设为主版本", { exact: true })).toBeVisible();
  await center
    .getByRole("button", { name: "版本组更多操作", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "加入版本", exact: true }).click();
  const join = page.getByRole("dialog", { name: /加入版本 · 匿名の原文/ });
  await expect(join).toBeVisible();
  expect(
    (await join.locator(".ant-modal-body").boundingBox()).height,
  ).toBeLessThanOrEqual(680);
  expect(
    await join
      .locator(".ant-modal-body")
      .evaluate((el) => getComputedStyle(el).overflowY),
  ).toBe("auto");
  const search = join.getByRole("searchbox", {
    name: "查找可加入版本",
    exact: true,
  });
  await search.fill("Translation 2");
  await search.press("Enter");
  await expect(join.getByRole("radio")).toHaveCount(1);
  await join.getByRole("radio").click();
  await join.getByRole("button", { name: "查看整理影响", exact: true }).click();
  const preview = page.getByRole("dialog", {
    name: "整理变更预览",
    exact: true,
  });
  await preview
    .getByRole("button", { name: "确认保存整理", exact: true })
    .click();
  await expect(join).not.toBeVisible();
  await expect(rows).toHaveCount(3);
  // A row removal is a single short confirmation, not the generic preview UI.
  await rows.first().getByRole("button", { name: /移出 / }).focus();
  await rows.first().getByRole("button", { name: /移出 / }).click();
  const remove = page.getByRole("dialog", { name: /移出版本/ });
  await expect(
    page.getByRole("dialog", { name: "整理变更预览", exact: true }),
  ).toHaveCount(0);
  await expect(
    remove.getByRole("button", { name: "确认拆分", exact: true }),
  ).toBeDisabled();
  await remove
    .getByRole("combobox", { name: "剩余作品首选版本", exact: true })
    .click();
  await page
    .locator(".ant-select-dropdown:visible .ant-select-item-option")
    .first()
    .click();
  await remove.getByRole("button", { name: "确认拆分", exact: true }).click();
  await expect(rows).toHaveCount(2);
  const remainingContext = await get(
      `/work-organization/context?edition_ids=${ids.join(",")}`,
    ),
    moved = remainingContext.editions.find((e) => e.work_id !== work);
  await post(`/works/${work}/merge`, {
    action: "work_merge",
    id: work,
    work_ids: [moved.work_id],
    expected_revisions: remainingContext.expected_revisions,
  });
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(rows).toHaveCount(3);
  const sharedSeries = await post("/series", {
    action: "series_create",
    library_id: lib.id,
    name: "匿名继承系列",
    work_ids: [work],
    expected_revisions: (
      await get(`/work-organization/context?work_ids=${work}`)
    ).expected_revisions,
  });
  const groupedIds = (await get(`/works/${work}/editions`)).items.map(
    (m) => m.id,
  );
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(center.getByText("已归属系列", { exact: true })).toBeVisible();

  await center
    .getByRole("button", { name: "版本组更多操作", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "移除该组", exact: true }).click();
  const separate = page.getByRole("dialog", { name: /移除该组/ });
  await expect(
    separate.getByText("将移除此组下的所有内容？", { exact: true }),
  ).toBeVisible();
  await expect(
    separate.getByText("所有作品默认继承系列。", { exact: true }),
  ).toBeVisible();
  await expect(
    separate.getByRole("checkbox", {
      name: "新作品继承原系列，紧接原作品排列",
      exact: true,
    }),
  ).toBeChecked();
  const removed = page.waitForRequest(
    (r) => r.url().endsWith(`/works/${work}/separate`) && r.method() === "POST",
  );
  await separate.getByRole("button", { name: "确认移除", exact: true }).click();
  expect((await removed).postDataJSON().keep_series).toBe(true);
  await expect(rows).toHaveCount(1);
  expect((await get(`/editions?library_id=${lib.id}`)).total).toBe(5);
  // The series workspace uses the same below-members presentation and read summaries.
  const currentWorks = (await get(`/works?library_id=${lib.id}&page_size=100`))
    .items;
  const inherited = currentWorks.filter((w) =>
    groupedIds.includes(w.preferred_edition_id),
  );
  expect(inherited).toHaveLength(3);
  expect(inherited.every((w) => w.series_id === sharedSeries.id)).toBe(true);
  const series = sharedSeries;
  const sjob = await post(
    "/series-discovery-jobs",
    { library_id: lib.id },
    202,
  );
  await expect(async () =>
    expect((await get(`/jobs/${sjob.job_id}`)).state).toBe("completed"),
  ).toPass({ timeout: 15000 });
  await page.goto(`/admin/series?library=${lib.id}&series=${series.id}`);
  const scenter = page.locator(".version-center"),
    scandidates = scenter.locator(".version-candidates-below");
  await expect(scenter.locator(".version-member-title").first()).toContainText(
    "匿名の原文",
  );
  await expect(scandidates).toBeVisible();
  for (const width of info.project.name === "desktop"
    ? [1280, 1440, 1680]
    : [390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    expect((await scandidates.boundingBox()).y).toBeGreaterThan(
      (await scenter.locator(".version-member").last().boundingBox()).y,
    );
    await page.screenshot({
      path: info.outputPath(`series-${width}.png`),
      fullPage: true,
    });
  }
  await expect(
    scandidates.getByRole("button", { name: "预览加入", exact: true }),
  ).toHaveCount(0);
  await scandidates
    .getByRole("button", { name: "加入系列", exact: true })
    .first()
    .click();
  await expect(scenter.locator(".series-member-row")).toHaveCount(5);
  expect((await get(`/series/${series.id}`)).member_count).toBe(5);
  expect(errors).toEqual([]);
});
