import { test, expect } from "@playwright/test";
import { copyFile, readFile } from "node:fs/promises";
import path from "node:path";

import { installViteSource } from "./vite-source.mjs";
installViteSource(test);

test("版本管理：自动重验、直接成组、标签保护、加入与撤销", async ({
  page,
  baseURL,
  browser,
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
  // Only copies the generator's anonymous archive inside this fresh anonymous run.
  expect(
    state.run_directory.startsWith(
      path.resolve(import.meta.dirname, "../../../.runtime/tests/runs/e2e-"),
    ),
  ).toBeTruthy();
  for (const filename of ["合成3.cbz", "匿名替代.cbz"])
    await copyFile(
      path.join(state.run_directory, "library", "合成1.cbz"),
      path.join(state.run_directory, "library", filename),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(state.token_file, "utf8"),
      password: "Anonymous work discovery password 2026",
    },
  });
  expect(setup.status()).toBe(200);
  const csrf = (await setup.json()).csrf_token;
  async function write(url, data = {}, status = 200) {
    const r = await page.request.post(`${baseURL}/api/v1${url}`, {
      headers: { Origin: baseURL, "X-CSRF-Token": csrf },
      data,
    });
    expect(r.status(), url).toBe(status);
    return status === 204 ? null : r.json();
  }
  async function get(url) {
    const r = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(r.status(), url).toBe(200);
    return r.json();
  }
  const stranger = await browser.newContext();
  expect(
    (await stranger.request.get(`${baseURL}/api/v1/work-proposals`)).status(),
  ).toBe(401);
  await stranger.close();
  expect(
    (
      await page.request.post(`${baseURL}/api/v1/work-discovery-jobs`, {
        headers: { Origin: baseURL },
        data: { library_id: "anonymous" },
      })
    ).status(),
  ).toBe(403);
  const lib = await write(
    "/libraries",
    { root_key: "anonymous", display_name: "匿名整理书库" },
    201,
  );
  const scan = await write(`/libraries/${lib.id}/scans`, {}, 202);
  await expect(async () =>
    expect((await get(`/jobs/${scan.job_id}`)).state).toBe("completed"),
  ).toPass({ timeout: 10000 });
  const editions = (await get(`/editions?library_id=${lib.id}`)).items;
  expect(editions).toHaveLength(5);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (
      m.type() === "error" &&
      !m.text().startsWith("Failed to load resource:")
    )
      errors.push(m.text());
  });
  await page.goto(`/admin/recommendations?library=${lib.id}`);
  await expect(
    page.getByRole("heading", { name: "版本管理", exact: true }),
  ).toBeAttached();
  await expect(page).toHaveURL(/admin\/versions/);
  if (info.project.name === "desktop") {
    const shell = page.locator(".admin-shell");
    await expect(shell).toHaveClass(/navigation-collapsed/);
    await page
      .getByRole("button", { name: "展开管理导航", exact: true })
      .click();
    await expect(shell).not.toHaveClass(/navigation-collapsed/);
    await page.reload();
    await expect(shell).not.toHaveClass(/navigation-collapsed/);
    await page
      .getByRole("button", { name: "收起管理导航", exact: true })
      .click();
    await page.reload();
    await expect(shell).toHaveClass(/navigation-collapsed/);
  }
  expect((await get("/jobs?type=work_recommendation")).total).toBe(0);
  const started = page.waitForResponse(
    (r) =>
      r.url().endsWith("/api/v1/work-discovery-jobs") &&
      r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "查找版本", exact: true }).click();
  const response = await started;
  expect(response.request().postDataJSON()).toEqual({
    library_id: lib.id,
    full_recompute: false,
  });
  const job = (await response.json()).job_id;
  await expect(async () =>
    expect((await get(`/jobs/${job}`)).state).toBe("completed"),
  ).toPass({ timeout: 15000 });
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  const original = (
    await get(`/work-proposals?library_id=${lib.id}&kind=create_work`)
  ).items[0];
  expect(original.new_edition_ids).toHaveLength(3);
  await page.locator(".version-left .version-group-row").first().click();
  const editor = page.getByLabel("版本方案", { exact: true });
  await expect(editor.getByRole("checkbox")).toHaveCount(3);
  const excluded = original.members.find(
    (m) => m.work_id === original.target_work_id,
  );
  const resolved = page.waitForResponse(
    (r) =>
      r.url().endsWith(`/work-proposals/${original.id}/resolve`) &&
      r.request().method() === "POST",
  );
  await editor
    .getByRole("checkbox", { name: `包含 ${excluded.title}`, exact: true })
    .uncheck();
  const selected = await (await resolved).json();
  expect(selected.new_edition_ids).toHaveLength(2);
  expect(selected.target_work_id).not.toBe(original.target_work_id);
  await expect(
    editor.getByRole("button", { name: "组成版本组", exact: true }),
  ).toBeEnabled();
  await expect(
    editor.getByText("保留的版本组已变化", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "更新成员方案", exact: true }),
  ).toHaveCount(0);
  // SSE only signals updates: it must not replace the excluded selection.
  await page.evaluate(() =>
    window.dispatchEvent(new Event("resources-changed")),
  );
  await expect(
    editor.getByRole("checkbox", {
      name: `包含 ${excluded.title}`,
      exact: true,
    }),
  ).not.toBeChecked();
  if (info.project.name === "desktop") {
    for (const width of [1680, 1440, 390, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      await expect(
        editor.getByRole("checkbox", {
          name: `包含 ${excluded.title}`,
          exact: true,
        }),
      ).not.toBeChecked();
    }
  }
  await expect(page.getByText("整理资料有更新", { exact: true })).toHaveCount(
    0,
  );
  await page.screenshot({
    path: info.outputPath("version-proposal.png"),
    fullPage: true,
  });
  await editor.getByRole("button", { name: "组成版本组", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(async () =>
    expect((await get(`/works/${selected.target_work_id}`)).edition_count).toBe(
      2,
    ),
  ).toPass();
  await expect(
    page.locator(".version-center").getByText("主版本", { exact: true }),
  ).toBeVisible();
  const grouped = (await get(`/works/${selected.target_work_id}/editions`))
    .items;
  expect(grouped.every((m) => m.label_state.mode === "auto")).toBe(true);
  // Existing and new routes keep the stable selected Work.
  await page.goto(`/admin/works/${selected.target_work_id}?library=${lib.id}`);
  await expect(page).toHaveURL(
    (url) => url.searchParams.get("work") === selected.target_work_id,
  );
  await expect(
    page.locator(".version-center").getByText("主版本", { exact: true }),
  ).toBeVisible();
  await expect(async () =>
    expect(
      (
        await get(
          `/work-proposals?target_work_id=${selected.target_work_id}&kind=add_to_work&alternatives=true`,
        )
      ).total,
    ).toBeGreaterThan(0),
  ).toPass({ timeout: 10000 });
  const candidateArea = page.locator(".version-candidates-below");
  async function refreshCandidates() {
    const loaded = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.pathname === "/api/v1/work-proposals" &&
        url.searchParams.get("target_work_id") === selected.target_work_id &&
        response.request().method() === "GET" &&
        response.status() === 200
      );
    });
    await page.getByRole("button", { name: "刷新", exact: true }).click();
    await loaded;
    await expect(candidateArea.locator(".ant-spin-spinning")).toHaveCount(0);
  }
  await refreshCandidates();
  await expect(
    candidateArea.getByRole("button", { name: "加入", exact: true }).first(),
  ).toBeVisible();
  await candidateArea
    .getByRole("button", { name: "忽略", exact: true })
    .first()
    .click();
  await expect(page.getByText("已忽略方案", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "恢复", exact: true }).click();
  await expect(page.getByText("已恢复建议", { exact: true })).toBeVisible();
  await refreshCandidates();
  await candidateArea
    .getByRole("button", { name: "加入", exact: true })
    .first()
    .click();
  await expect(async () =>
    expect(
      (await get(`/works/${selected.target_work_id}`)).edition_count,
    ).toBeGreaterThan(2),
  ).toPass();
  await page.getByRole("button", { name: "撤销", exact: true }).click();
  await expect(async () =>
    expect((await get(`/works/${selected.target_work_id}`)).edition_count).toBe(
      2,
    ),
  ).toPass();
  // A second discovery computes no previously covered pairs.
  const secondStart = page.waitForResponse(
    (r) =>
      r.url().endsWith("/api/v1/work-discovery-jobs") &&
      r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "查找版本", exact: true }).click();
  const second = (await (await secondStart).json()).job_id;
  await expect(async () =>
    expect((await get(`/jobs/${second}`)).state).toBe("completed"),
  ).toPass({ timeout: 15000 });
  const stats = await get(`/work-discovery-jobs/${second}`);
  expect(stats.computed_pairs).toBe(0);
  expect(stats.reused_pairs).toBe(10);
  const retainedRow = page.locator(".version-group-row").first();
  await retainedRow.evaluate((el) => {
    el.dataset.scrollProbe = "retained";
  });
  const refreshed = page.waitForResponse(
    (r) => r.url().includes("/api/v1/works?") && r.request().method() === "GET",
  );
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await refreshed;
  await expect(retainedRow).toHaveAttribute("data-scroll-probe", "retained");
  await page.screenshot({
    path: info.outputPath("version-management.png"),
    fullPage: true,
  });
  if (info.project.name === "desktop") {
    await page.setViewportSize({ width: 1680, height: 1000 });
    const left = page.locator(".version-left"),
      center = page.locator(".version-center");
    const boxes = await Promise.all([left, center].map((v) => v.boundingBox()));
    expect(boxes[0].x + boxes[0].width).toBeLessThan(boxes[1].x);
    // Recommended candidates keep their DOM/scroll when changing layout.
    const host = page.locator(".version-candidates-below");
    await expect(host.getByText("手动查找", { exact: true })).toHaveCount(0);
    await host.evaluate((el) => {
      el.dataset.draftProbe = "retained";
    });
    for (const width of [1440, 1280, 390, 1680]) {
      await page.setViewportSize({ width, height: 900 });
      if (width === 1280) {
        await page
          .getByRole("button", { name: "展开管理导航", exact: true })
          .click();
        await expect(page.locator(".version-management")).toHaveAttribute(
          "data-layout",
          "drawer",
        );
      }
      await expect(host).toHaveAttribute("data-draft-probe", "retained");
      await expect(host.getByText("推荐加入", { exact: true })).toBeVisible();
      if (width !== 390)
        expect(
          (await page.locator(".version-center").boundingBox()).width,
        ).toBeGreaterThan(400);
      await page.screenshot({
        path: info.outputPath(`layout-${width}.png`),
        fullPage: true,
      });
    }
    await page
      .getByRole("button", { name: "收起管理导航", exact: true })
      .click();
    await page
      .getByRole("button", { name: "版本组更多操作", exact: true })
      .click();
    await page
      .getByRole("menuitem", {
        name: "修改组名",
        exact: true,
      })
      .click();
    const advanced = page.getByRole("dialog", { name: /修改组名/ });
    await advanced
      .getByRole("textbox", { name: "版本组名称", exact: true })
      .fill("Anonymous unsaved draft");
    await advanced.getByRole("button", { name: "关闭", exact: true }).click();
    const guard = page.getByRole("dialog", {
      name: "放弃未提交的修改？",
      exact: true,
    });
    await expect(guard).toBeVisible();
    await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
    await expect(
      advanced.getByRole("textbox", { name: "版本组名称", exact: true }),
    ).toHaveValue("Anonymous unsaved draft");
    await advanced.getByRole("button", { name: "取消", exact: true }).click();
    await guard.getByRole("button", { name: "放弃修改", exact: true }).click();
    await expect(advanced).not.toBeVisible();
  }
  // Manual label writes advance organization revisions. Verify them after the
  // proposal round trip so they cannot invalidate that scenario's frozen input.
  const target = grouped[0];
  const labelButton = page.getByRole("button", {
    name: `编辑 ${target.title_original?.trim() || target.title} 的版本标签`,
    exact: true,
  });
  await labelButton.focus();
  await labelButton.click();
  const labelModal = page.locator(".version-label-popover");
  // Explicitly empty labels are a manual decision, never silently repopulated.
  const input = labelModal.getByRole("combobox");
  await input.click();
  await input.fill("Anonymous layout draft");
  await input.press("Enter");
  if (info.project.name === "desktop") {
    await page.setViewportSize({ width: 1680, height: 900 });
    await expect(
      labelModal
        .locator(".ant-select-selection-item-content")
        .filter({ hasText: "Anonymous layout draft" }),
    ).toBeVisible();
    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(
      labelModal
        .locator(".ant-select-selection-item-content")
        .filter({ hasText: "Anonymous layout draft" }),
    ).toBeVisible();
  }

  await input.press("Escape");
  const clear = labelModal.getByRole("button", { name: "Clear", exact: true });
  if (await clear.count()) {
    await clear.focus();
    await clear.press("Enter");
  }
  // Returning to the initial empty value must not submit a phantom change.
  expect(target.version_labels).toEqual([]);
  await expect(
    labelModal.getByRole("button", { name: "保存", exact: true }),
  ).toBeDisabled();
  await input.fill("Anonymous manual label");
  await input.press("Enter");
  await input.press("Escape");
  await labelModal.getByRole("button", { name: "保存", exact: true }).click();
  await expect(labelModal).not.toBeVisible();
  const manual = (
    await get(`/works/${selected.target_work_id}/editions`)
  ).items.find((m) => m.id === target.id);
  expect(manual.version_labels).toEqual(["Anonymous manual label"]);
  expect(manual.label_state.mode).toBe("manual");
  await labelButton.click();
  await expect(labelModal).toBeVisible();
  await clear.focus();
  await clear.press("Enter");
  await input.press("Escape");
  await labelModal.getByRole("button", { name: "保存", exact: true }).click();
  await expect(labelModal).not.toBeVisible();
  expect(
    (await get(`/works/${selected.target_work_id}/editions`)).items.find(
      (m) => m.id === target.id,
    ).version_labels,
  ).toEqual([]);
  await page
    .getByRole("button", { name: "版本组更多操作", exact: true })
    .click();
  await page
    .getByRole("menuitem", { name: "生成智能标签", exact: true })
    .click();
  await expect(
    page.getByText("已生成智能标签，人工值保持不变", { exact: true }),
  ).toBeVisible();
  expect(
    (await get(`/works/${selected.target_work_id}/editions`)).items.find(
      (m) => m.id === target.id,
    ).version_labels,
  ).toEqual([]);
  expect(errors).toEqual([]);
});

test("版本管理：导航、长标题与容器断点布局", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(60000);
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
  ).toBeTruthy();
  const initialized = (
    await (await page.request.get(`${baseURL}/api/v1/auth/status`)).json()
  ).initialized;
  const signedIn = await page.request.post(
    `${baseURL}/api/v1/auth/${initialized ? "login" : "setup"}`,
    {
      headers: { Origin: baseURL },
      data: {
        ...(!initialized
          ? { setup_token: await readFile(state.token_file, "utf8") }
          : {}),
        password: "Anonymous work discovery password 2026",
      },
    },
  );
  expect(signedIn.status()).toBe(200);
  const csrf = (await signedIn.json()).csrf_token;
  async function get(url) {
    const r = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(r.status(), url).toBe(200);
    return r.json();
  }
  async function post(url, data = {}, status = 200) {
    const r = await page.request.post(`${baseURL}/api/v1${url}`, {
      headers: { Origin: baseURL, "X-CSRF-Token": csrf },
      data,
    });
    expect(r.status(), url).toBe(status);
    return r.json();
  }
  const lib = await post(
    "/libraries",
    { root_key: "anonymous-work", display_name: "匿名布局书库" },
    201,
  );
  const scan = await post(`/libraries/${lib.id}/scans`, {}, 202);
  await expect(async () =>
    expect((await get(`/jobs/${scan.job_id}`)).state).toBe("completed"),
  ).toPass();
  const editions = (await get(`/editions?library_id=${lib.id}`)).items.filter(
    (e) => e.file_state === "available",
  );
  const ids = editions.slice(0, 2).map((e) => e.id);
  const context = await get(
    `/work-organization/context?edition_ids=${ids.join(",")}`,
  );
  const target = context.editions.find((e) => e.id === ids[0]).work_id;
  const other = context.editions.find((e) => e.id === ids[1]).work_id;
  await post(`/works/${target}/merge`, {
    action: "work_merge",
    id: target,
    work_ids: [other],
    expected_revisions: context.expected_revisions,
  });
  const current = await get(
    `/work-organization/context?work_ids=${encodeURIComponent(target)}`,
  );
  const longTitle =
    "匿名長いタイトル・原文とサークル情報を含む表示検証用の作品名 ".repeat(4);
  const representative = await get(
    `/books/${current.works.find((w) => w.id === target).preferred_edition_id}`,
  );
  const rename = await page.request.patch(
    `${baseURL}/api/v1/books/${representative.id}/overrides`,
    {
      headers: { Origin: baseURL, "X-CSRF-Token": csrf },
      data: {
        expected_revision: representative.revision,
        fields: { title_original: { op: "set", value: longTitle } },
      },
    },
  );
  expect(rename.status()).toBe(200);
  await page.goto(`/admin/versions?library=${lib.id}`);
  const row = page
    .locator(".version-group-row")
    .filter({ hasText: longTitle.trim() });
  await expect(row).toBeVisible();
  const title = row.locator("strong");
  expect(
    await title.evaluate(
      (el) =>
        el.clientHeight <=
        Math.ceil(parseFloat(getComputedStyle(el).lineHeight)) * 2 + 1,
    ),
  ).toBe(true);
  expect((await row.boundingBox()).height).toBeLessThan(115);
  expect((await row.locator("img").boundingBox()).width).toBe(48);
  expect((await row.locator("img").boundingBox()).height).toBe(66);
  await row.focus();
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await row.click();
  await page.mouse.move(0, 0);
  if (info.project.name === "desktop") {
    const workspace = page.locator(".version-layout");
    const shell = page.locator(".admin-shell");
    for (const width of [1280, 1440, 1680]) {
      await page.setViewportSize({ width, height: 900 });
      for (const collapsed of [true, false]) {
        const name = collapsed ? "收起管理导航" : "展开管理导航";
        if (
          (await shell.getAttribute("class")).includes(
            "navigation-collapsed",
          ) !== collapsed
        )
          await page.getByRole("button", { name, exact: true }).click();
        const available = width - (collapsed ? 64 : 218) - 56;
        const expected = available >= 1240 ? "wide" : "drawer";
        await expect(page.locator(".version-management")).toHaveAttribute(
          "data-layout",
          expected,
        );
        await expect(async () =>
          expect(
            Math.round(
              (await page.locator(".desktop-sidebar").boundingBox()).width,
            ),
          ).toBe(collapsed ? 64 : 218),
        ).toPass();
        expect((await workspace.boundingBox()).y).toBeLessThanOrEqual(140);
        const center = await page.locator(".version-center").boundingBox();
        expect(center.width).toBeGreaterThanOrEqual(
          expected === "wide" ? 600 : 400,
        );
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        ).toBe(true);
        if (collapsed) {
          await page
            .locator(".desktop-sidebar")
            .getByRole("menuitem", { name: "漫画", exact: true })
            .focus();
          await expect(
            page.getByRole("tooltip", { name: "漫画", exact: true }),
          ).toBeVisible();
          await page.getByRole("button", { name: "刷新", exact: true }).focus();
        }
        await page.screenshot({
          path: info.outputPath(
            `versions-${width}-${collapsed ? "collapsed" : "expanded"}.png`,
          ),
          fullPage: true,
        });
      }
    }
  } else {
    await page
      .getByRole("button", { name: "打开管理导航", exact: true })
      .click();
    await expect(
      page.getByRole("dialog", { name: "管理导航", exact: true }),
    ).toBeVisible();
    await page
      .getByRole("dialog", { name: "管理导航", exact: true })
      .getByRole("button", { name: "关闭", exact: true })
      .click();
    await expect(
      page.getByRole("dialog", { name: "管理导航", exact: true }),
    ).not.toBeVisible();
    await expect(page.locator(".version-management")).toHaveAttribute(
      "data-layout",
      "mobile",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: info.outputPath("versions-390.png"),
      fullPage: true,
    });
  }
  // No selection: the center uses the released width and no empty right column.
  await page.goto(`/admin/versions?library=${lib.id}`);
  if (info.project.name === "desktop")
    await expect(page.locator(".version-right")).not.toBeVisible();
  await expect(page.getByText("整理资料有更新", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByRole("button", { name: "版本查找任务状态", exact: true }),
  ).toHaveCount(0);
});
