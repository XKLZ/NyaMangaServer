import { test, expect } from "@playwright/test";
import { readFile, mkdir, copyFile } from "node:fs/promises";
import path from "node:path";
import { installViteSource, routeViteSource } from "./vite-source.mjs";
installViteSource(test);

test("全局当前书库：双库、明确范围、深链、草稿与过期读取隔离", async ({
  page,
  baseURL,
  context,
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
  for (let i = 0; i < 27; i++)
    await copyFile(
      path.join(fixture.run_directory, "work-library", "合成1.cbz"),
      path.join(
        fixture.run_directory,
        "work-library",
        `匿名范围分页${String(i).padStart(2, "0")}.cbz`,
      ),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous library context 2026",
    },
  });
  expect(setup.status()).toBe(200);
  const account = await setup.json(),
    headers = { Origin: baseURL, "X-CSRF-Token": account.csrf_token };
  async function get(url) {
    const r = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(r.status(), url).toBe(200);
    return r.json();
  }
  async function write(url, data, status = 200, method = "POST") {
    const r = await page.request.fetch(`${baseURL}/api/v1${url}`, {
      method,
      headers,
      data,
    });
    expect(r.status(), url).toBe(status);
    return r.json();
  }
  const old = await write(
    "/libraries",
    { root_key: "anonymous", display_name: "A 匿名旧书库" },
    201,
  );
  const fresh = await write(
    "/libraries",
    { root_key: "anonymous-work", display_name: "B 匿名新书库" },
    201,
  );
  const identities = {};
  for (const library of [old, fresh]) {
    const scan = await write(`/libraries/${library.id}/scans`, {}, 202);
    await expect
      .poll(async () => (await get(`/jobs/${scan.job_id}`)).state)
      .toBe("completed");
    const books = (
      await get(`/editions?library_id=${library.id}&page_size=100`)
    ).items;
    for (let i = 0; i < books.length; i++) {
      const book = await get(`/books/${books[i].id}`);
      await write(
        `/books/${book.id}/overrides`,
        {
          expected_revision: book.revision,
          fields: {
            title_original: {
              op: "set",
              value: `${library.id === old.id ? "旧库" : "新库"}原文作品 ${i}`,
            },
          },
        },
        200,
        "PATCH",
      );
    }
    const org = await get(
      `/work-organization/context?edition_ids=${books
        .slice(0, 2)
        .map((b) => b.id)
        .join(",")}`,
    );
    const work = org.editions.find((b) => b.id === books[0].id).work_id;
    await write(`/works/${work}/merge`, {
      action: "work_merge",
      id: work,
      work_ids: [org.editions.find((b) => b.id === books[1].id).work_id],
      expected_revisions: org.expected_revisions,
    });
    const series = await write("/series", {
      action: "series_create",
      library_id: library.id,
      name: `${library.id === old.id ? "旧库" : "新库"}系列`,
      work_ids: [work],
      expected_revisions: (
        await get(`/work-organization/context?work_ids=${work}`)
      ).expected_revisions,
    });
    identities[library.id] = {
      books,
      work,
      series: series.id,
      scan: scan.job_id,
    };
  }
  async function status(library, disabled) {
    const now = (await get("/libraries")).items.find(
      (l) => l.id === library.id,
    );
    return write(
      `/libraries/${library.id}/status`,
      { expected_revision: now.revision, disabled },
      200,
      "PATCH",
    );
  }
  const freshCount = identities[fresh.id].books.length;
  const oldCount = identities[old.id].books.length;
  await status(old, true);
  const errors = [],
    reads = [],
    writes = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (r) => {
    const url = new URL(r.url());
    if (!url.pathname.startsWith("/api/")) return;
    if (r.method() === "GET") reads.push(url);
    else if (url.pathname === "/api/v1/admin/books/search")
      reads.push({
        pathname: url.pathname,
        library: r.postDataJSON().library_id,
        body: r.postDataJSON(),
      });
    else writes.push({ url, body: r.postDataJSON() });
  });
  const picker = () =>
    page.getByRole("combobox", { name: "当前书库", exact: true });
  const header = () => page.locator(".current-library-header");
  async function choose(library, target = page) {
    await target
      .getByRole("combobox", { name: "当前书库", exact: true })
      .click();
    await target
      .getByRole("option", { name: library.display_name, exact: true })
      .click();
  }
  async function disabledLibrary(target = page) {
    await target
      .getByRole("button", { name: "书库高级选项", exact: true })
      .click();
    await target
      .getByRole("menuitem", { name: "查看停用书库", exact: true })
      .click();
    const dialog = target.getByRole("dialog", {
      name: "查看停用书库",
      exact: true,
    });
    await dialog
      .getByRole("combobox", { name: "停用书库", exact: true })
      .click();
    await target
      .getByRole("option", { name: new RegExp(`^${old.display_name}`) })
      .click();
  }
  async function scope(value) {
    await page
      .getByRole("combobox", { name: "书库查看范围", exact: true })
      .click();
    await page.getByRole("option", { name: value, exact: true }).click();
  }
  async function nav(name) {
    name =
      { 系列管理: "系列", 版本管理: "版本", 任务中心: "维护中心" }[name] ||
      name;
    if (info.project.name === "mobile")
      await page
        .getByRole("button", { name: "打开管理导航", exact: true })
        .click();
    await page
      .getByRole("menuitem", { name, exact: true })
      .getByRole("link", { name, exact: true })
      .click();
  }
  async function atLibrary(id) {
    await expect(page).toHaveURL(new RegExp(`library_id=${id}`));
    await expect(picker()).toBeEnabled();
  }
  const groupReads = () =>
    reads.filter((r) =>
      [
        "/api/v1/works",
        "/api/v1/series",
        "/api/v1/work-proposals",
        "/api/v1/series-proposals",
      ].includes(r.pathname),
    );
  await page.goto("/admin/versions");
  await atLibrary(fresh.id);
  await expect(page.locator(".version-group-row")).toHaveCount(1);
  await expect(page.locator(".version-group-row")).toContainText("新库");
  expect(
    groupReads().every((r) => r.searchParams.get("library_id") === fresh.id),
  ).toBe(true);
  await expect(
    page.getByRole("combobox", { name: "版本管理书库", exact: true }),
  ).toHaveCount(0);
  await nav("系列管理");
  await atLibrary(fresh.id);
  await expect(page.locator(".version-group-row")).toHaveCount(1);
  await page.reload();
  await atLibrary(fresh.id);
  await page.goBack();
  await atLibrary(fresh.id);
  await page.goForward();
  await atLibrary(fresh.id);
  // Explicit invalid / conflicting scope must never fall back to all rows.
  let mark = groupReads().length;
  await page.goto(`/admin/versions?library_id=missing`);
  await expect(
    page.getByText("链接指定的书库不存在，请选择其他书库。", { exact: true }),
  ).toBeVisible();
  expect(groupReads()).toHaveLength(mark);
  await page.goto(`/admin/series?library=${old.id}&library_id=${fresh.id}`);
  await expect(
    page.getByText("链接中的书库参数冲突，请重新选择书库。", { exact: true }),
  ).toBeVisible();
  expect(groupReads()).toHaveLength(mark);
  await page.goto(
    `/admin/works/${identities[old.id].work}?library_id=${fresh.id}`,
  );
  await atLibrary(old.id);
  expect(new URL(page.url()).pathname).toBe("/admin/versions");
  expect(
    groupReads()
      .slice(mark)
      .every((r) => r.searchParams.get("library_id") === old.id),
  ).toBe(true);
  await page.goto(
    `/admin/series?library_id=${fresh.id}&series=${identities[old.id].series}`,
  );
  await atLibrary(old.id);
  await expect(header()).toContainText("已停用");
  await expect(page.locator(".series-name-button")).toHaveText("旧库系列");
  expect(
    groupReads()
      .slice(mark)
      .every((r) => r.searchParams.get("library_id") === old.id),
  ).toBe(true);
  const proposal = {
    id: "anonymous-proposal",
    job_id: "anonymous-owner-job",
    kind: "add_to_work",
    target_work_id: identities[fresh.id].work,
    target_work_name: "匿名目标作品",
    tier: "strong",
    primary: true,
    revision: 1,
    state: "pending",
    members: identities[fresh.id].books.slice(0, 3).map((b, i) => ({
      id: b.id,
      work_id: i < 2 ? identities[fresh.id].work : b.work_id,
      title: `匿名候选 ${i}`,
      role: i < 2 ? "existing" : "new",
      version_labels: [],
      languages: [],
      content_revision: b.content_revision,
      cover_revision: b.cover_revision || 0,
    })),
    new_edition_ids: [identities[fresh.id].books[2].id],
    risks: [],
    blocked: false,
    can_undo: false,
    effects: [],
  };
  await page.route("**/api/v1/work-proposals/anonymous-proposal*", (route) =>
    route.fulfill({ json: proposal }),
  );
  await page.route("**/api/v1/jobs/anonymous-owner-job", (route) =>
    route.fulfill({
      json: {
        id: "anonymous-owner-job",
        library_id: "",
        type: "work_recommendation",
        state: "completed",
        stage: "completed",
        done: 0,
        failed: 0,
      },
    }),
  );
  mark = groupReads().length;
  await page.goto(
    `/admin/versions?proposal=anonymous-proposal&library_id=${old.id}`,
  );
  await atLibrary(fresh.id);
  expect(
    groupReads()
      .slice(mark)
      .every((r) => r.searchParams.get("library_id") === fresh.id),
  ).toBe(true);
  await page.goto(`/admin/versions?library=${fresh.id}&library_id=${fresh.id}`);
  await atLibrary(fresh.id);
  expect(new URL(page.url()).searchParams.has("library")).toBe(false);
  await page.goto("/admin/books?library=");
  await atLibrary(fresh.id);
  await expect(page.locator(".book-list-entry")).toHaveCount(freshCount);
  await disabledLibrary();
  await atLibrary(old.id);
  await expect(page.locator(".book-list-entry")).toHaveCount(oldCount);
  await expect
    .poll(() => reads.filter((r) => r.body).at(-1)?.body)
    .toMatchObject({
      library_id: old.id,
      include_disabled: true,
    });
  await choose(fresh);
  await atLibrary(fresh.id);
  await page.getByRole("button", { name: /^更多筛选/ }).click();
  await scope("全部书库");
  await expect(page).toHaveURL(/library_scope=all/);
  await expect(page.locator(".book-list-entry")).toHaveCount(freshCount);
  await expect
    .poll(() => reads.filter((r) => r.body).at(-1)?.body)
    .toMatchObject({
      library_id: "",
      include_disabled: false,
    });
  await page.getByRole("button", { name: /^更多筛选/ }).click();
  await page
    .getByRole("checkbox", { name: "包括停用书库", exact: true })
    .click();
  await expect(
    page.getByRole("checkbox", { name: "包括停用书库", exact: true }),
  ).toBeChecked();
  await expect(page.locator(".book-list-entry")).toHaveCount(
    freshCount + oldCount,
  );
  await page
    .locator(".ant-table-tbody tr[data-row-key] .ant-checkbox-input")
    .first()
    .check();
  const oldEntry = page
    .locator(".book-list-entry")
    .filter({ hasText: "旧库原文作品 0" })
    .first();
  await oldEntry.locator(".book-cover-button").scrollIntoViewIfNeeded();
  const listScroll = await page.evaluate(() => window.scrollY);
  await page.locator(".ant-table-tbody").evaluate((el) => {
    window.__scopeTable = el;
  });
  await oldEntry.locator(".book-cover-button").click();
  const rangeBook = page.getByRole("dialog", { name: /^漫画详情/ });
  await expect(rangeBook).toBeVisible();
  expect(new URL(page.url()).searchParams.get("library_scope")).toBe("all");
  await rangeBook.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(rangeBook).not.toBeVisible();
  expect(
    await page
      .locator(".ant-table-tbody")
      .evaluate((el) => el === window.__scopeTable),
  ).toBe(true);
  await expect(
    page.locator(
      ".ant-table-tbody tr[data-row-key] .ant-checkbox-input:checked",
    ),
  ).toHaveCount(1);
  await expect
    .poll(async () =>
      Math.abs((await page.evaluate(() => window.scrollY)) - listScroll),
    )
    .toBeLessThan(2);
  await nav("版本管理");
  await atLibrary(fresh.id);
  expect(new URL(page.url()).searchParams.has("library_scope")).toBe(false);
  // Real saved format-1 records: empty library explicitly means all.
  const savedBase = {
    query: "",
    file_state: "",
    scope: "all",
    collapse: "none",
    sort: "indexed_at",
    order: "desc",
    page_size: 50,
  };
  const savedAll = await write(
    "/saved-searches",
    {
      name: "匿名跨库条件",
      query_version: 1,
      expected_revision: 0,
      conditions: { ...savedBase, library_id: "" },
    },
    200,
  );
  await page.goto(`/admin/books?library_id=${fresh.id}`);
  await page.getByRole("button", { name: "已保存的搜索", exact: true }).click();
  await page.getByRole("combobox", { name: "已保存搜索", exact: true }).click();
  await page.getByRole("option", { name: savedAll.name, exact: true }).click();
  await page.getByRole("button", { name: "打开保存搜索", exact: true }).click();
  await expect(page).toHaveURL(/library_scope=all/);
  await page.getByRole("button", { name: "已保存的搜索", exact: true }).click();
  await page
    .getByRole("textbox", { name: "搜索名称", exact: true })
    .fill("匿名更新跨库条件");
  await page.getByRole("button", { name: "保存当前搜索", exact: true }).click();
  await expect
    .poll(
      () =>
        writes.filter((v) => v.url.pathname === "/api/v1/saved-searches")
          .length,
    )
    .toBeGreaterThan(0);
  const savedWire = writes
    .filter((v) => v.url.pathname === "/api/v1/saved-searches")
    .at(-1).body.conditions;
  for (const key of [
    "page",
    "snapshot_revision",
    "library_scope",
    "current_library",
  ])
    expect(savedWire).not.toHaveProperty(key);
  await page
    .getByRole("dialog", { name: "保存常用搜索", exact: true })
    .getByRole("button", { name: /^(Close|关闭)$/ })
    .click();
  const savedOld = await write("/saved-searches", {
    name: "匿名停用库条件",
    query_version: 1,
    expected_revision: 0,
    conditions: { ...savedBase, library_id: old.id },
  });
  await page.reload();
  await page.getByRole("button", { name: "已保存的搜索", exact: true }).click();
  await page.getByRole("combobox", { name: "已保存搜索", exact: true }).click();
  await page.getByRole("option", { name: savedOld.name, exact: true }).click();
  await page.getByRole("button", { name: "打开保存搜索", exact: true }).click();
  await atLibrary(old.id);
  await expect
    .poll(() => reads.filter((r) => r.body).at(-1)?.body)
    .toMatchObject({ library_id: old.id, include_disabled: true });
  await choose(fresh);
  await atLibrary(fresh.id);
  await page.route("**/api/v1/saved-searches", async (route) => {
    if (route.request().method() !== "GET") return route.fallback();
    const response = await route.fetch(),
      body = await response.json();
    body.items.push({
      ...savedOld,
      id: "anonymous-invalid-saved",
      name: "匿名失效库条件",
      conditions: { ...savedBase, library_id: "missing" },
    });
    return route.fulfill({ response, json: body });
  });
  await page.reload();
  await expect(page.locator(".book-list-entry")).toHaveCount(freshCount);
  const beforeInvalidSaved = reads.filter((r) => r.body).length;
  await page.getByRole("button", { name: "已保存的搜索", exact: true }).click();
  await page.getByRole("combobox", { name: "已保存搜索", exact: true }).click();
  await page
    .getByRole("option", { name: "匿名失效库条件", exact: true })
    .click();
  await page.getByRole("button", { name: "打开保存搜索", exact: true }).click();
  await expect(
    page.getByText("链接指定的书库不存在，请选择其他书库。", { exact: true }),
  ).toBeVisible();
  expect(reads.filter((r) => r.body)).toHaveLength(beforeInvalidSaved);
  await choose(fresh);
  await atLibrary(fresh.id);
  await page.addInitScript((id) => {
    if (sessionStorage.getItem("anonymous-stale-preference") !== "yes") return;
    sessionStorage.removeItem("anonymous-stale-preference");
    localStorage.setItem(
      `nyamanga.ui.current-library.account.v1:${id}`,
      "missing",
    );
    sessionStorage.removeItem(`nyamanga.ui.current-library.tab.v1:${id}`);
  }, account.account_id);
  await page.evaluate(() =>
    sessionStorage.setItem("anonymous-stale-preference", "yes"),
  );
  await page.goto("/admin/versions");
  await expect(
    page.getByText("上次书库已不可用，已选择首个启用书库", { exact: true }),
  ).toBeVisible();
  await atLibrary(fresh.id);
  // Current/history/all scopes do not filter global management pages.
  await nav("任务中心");
  await atLibrary(fresh.id);
  await expect
    .poll(() =>
      reads
        .filter((r) => r.pathname === "/api/v1/jobs")
        .at(-1)
        ?.searchParams.get("library_id"),
    )
    .toBe(fresh.id);
  await scope("全部书库");
  await expect(page).toHaveURL(/library_scope=all/);
  await expect
    .poll(() =>
      reads
        .filter((r) => r.pathname === "/api/v1/jobs")
        .at(-1)
        ?.searchParams.get("library_id"),
    )
    .toBe(null);
  await page.goto(`/admin/imports?library_id=${fresh.id}`);
  await expect(
    page.getByRole("combobox", { name: "书库查看范围", exact: true }),
  ).toBeVisible();
  await scope("全部书库");
  await expect(page).toHaveURL(/library_scope=all/);
  const batch = await write(
    "/import-batches",
    {
      library_id: fresh.id,
      request_key: "anonymous-library-scope",
      allow_metadata_network: true,
    },
    202,
  );
  await page.goto(`/admin/imports?library_id=${old.id}&batch=${batch.id}`);
  await atLibrary(fresh.id);
  expect(new URL(page.url()).searchParams.get("batch")).toBe(batch.id);
  await disabledLibrary();
  await atLibrary(old.id);
  expect(new URL(page.url()).searchParams.has("batch")).toBe(false);
  await page.route("**/api/v1/jobs/anonymous-global", (route) =>
    route.fulfill({
      json: {
        id: "anonymous-global",
        library_id: "",
        type: "tag_translation",
        state: "completed",
        stage: "completed",
        total: 0,
        done: 0,
        failed: 0,
        generation: 1,
        created_at: 0,
        updated_at: 0,
      },
    }),
  );
  await page.route("**/api/v1/jobs/anonymous-global/import-batch", (route) =>
    route.fulfill({
      status: 404,
      json: { error: { code: "not_found", message: "anonymous global job" } },
    }),
  );
  await page.route("**/api/v1/jobs/anonymous-global/items?*", (route) =>
    route.fulfill({ json: { items: [], total: 0, page: 1, page_size: 50 } }),
  );
  await page.goto("/admin/jobs/anonymous-global");
  await expect(
    page.getByRole("heading", { name: /中文译库更新/ }),
  ).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/admin/maintenance");
  expect(new URL(page.url()).searchParams.get("task")).toBe("anonymous-global");
  expect(new URL(page.url()).searchParams.has("library_id")).toBe(false);
  // A successful retry consumes its submitted URL draft before opening the new
  // job. A failed retry must keep that draft and never navigate away.
  for (const suffix of ["old", "new"]) {
    const id = `anonymous-scope-retry-${suffix}`;
    await page.route(`**/api/v1/jobs/${id}`, (route) =>
      route.fulfill({
        json: {
          id,
          library_id: fresh.id,
          type: "metadata",
          mode: "selected",
          state: "failed",
          stage: "finished",
          total: 1,
          done: 0,
          failed: 1,
          generation: 1,
          created_at: 0,
          updated_at: 0,
        },
      }),
    );
    await page.route(`**/api/v1/jobs/${id}/items?*`, (route) =>
      route.fulfill({
        json: {
          items: [
            {
              ordinal: 0,
              book_id: identities[fresh.id].books[0].id,
              state: "failed",
              relative_path: "anonymous.cbz",
              candidates: [
                {
                  url: "https://e-hentai.org/g/1/anonymous",
                  title: "匿名重试候选",
                },
              ],
            },
          ],
          total: 1,
          page: 1,
          page_size: 50,
        },
      }),
    );
  }
  let retryFails = true;
  await page.route("**/api/v1/jobs/anonymous-scope-retry-old/retry", (route) =>
    retryFails
      ? route.fulfill({
          status: 503,
          json: {
            error: { code: "retry_failed", message: "匿名任务重试失败" },
          },
        })
      : route.fulfill({
          status: 202,
          json: { job_id: "anonymous-scope-retry-new" },
        }),
  );
  await page.goto("/admin/jobs/anonymous-scope-retry-old");
  await page
    .getByRole("combobox", { name: "选择画廊候选", exact: true })
    .click();
  await page.getByRole("option", { name: "匿名重试候选", exact: true }).click();
  await page
    .getByRole("button", { name: "重试所选 1 项", exact: true })
    .click();
  await expect(
    page.getByText("匿名任务重试失败", { exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL(/task=anonymous-scope-retry-old/);
  await expect(
    page
      .getByRole("combobox", { name: "选择画廊候选", exact: true })
      .locator(".."),
  ).toContainText("匿名重试候选");
  retryFails = false;
  await page
    .getByRole("button", { name: "重试所选 1 项", exact: true })
    .click();
  await expect(page).toHaveURL(/task=anonymous-scope-retry-new/);
  await expect(
    page.getByRole("dialog", { name: "有尚未保存的修改", exact: true }),
  ).not.toBeVisible();
  await page.goto(`/admin/libraries?library_id=${fresh.id}`);
  await expect(
    page.getByRole("row").filter({ hasText: old.display_name }),
  ).toBeVisible();
  await expect(
    page.getByRole("row").filter({ hasText: fresh.display_name }),
  ).toBeVisible();
  await page.goto(`/admin/maintenance?section=trash&library_id=${fresh.id}`);
  await expect(header().getByText("全局", { exact: true })).toBeVisible();
  await page.goto(`/admin/settings/metadata?library_id=${fresh.id}`);
  await expect(header().getByText("全局", { exact: true })).toBeVisible();
  await page.goto(`/admin/books?library_id=${fresh.id}&page_size=25`);
  await expect(page.locator(".book-list-entry")).toHaveCount(25);
  await page.locator(".ant-pagination-item-2").click();
  await expect
    .poll(() => reads.filter((r) => r.body).at(-1)?.body)
    .toMatchObject({ page: 2, library_id: fresh.id });
  expect(
    reads.filter((r) => r.body).at(-1).body.snapshot_revision,
  ).toBeTruthy();
  await page
    .locator(".ant-table-tbody tr[data-row-key] .ant-checkbox-input")
    .first()
    .check();
  await disabledLibrary();
  await atLibrary(old.id);
  await expect
    .poll(() => reads.filter((r) => r.body).at(-1)?.body)
    .toMatchObject({ page: 1, library_id: old.id });
  expect(reads.filter((r) => r.body).at(-1).body).not.toHaveProperty(
    "snapshot_revision",
  );
  await expect(
    page.locator(".ant-table-selection-column .ant-checkbox-input:checked"),
  ).toHaveCount(0);
  // A series name draft cancels the entire library transition, including preferences.
  await page.goto(
    `/admin/series?library_id=${fresh.id}&series=${identities[fresh.id].series}`,
  );
  await atLibrary(fresh.id);
  await expect(page.locator(".series-name-button")).toHaveText("新库系列");
  await expect(page.locator(".series-member-row")).toHaveCount(1);
  await page.locator(".series-name-button").click();
  const name = page.getByRole("textbox", { name: "系列名称", exact: true });
  await name.fill("匿名保留草稿");
  const initialURL = page.url();
  const preference = await page.evaluate(
    (id) =>
      localStorage.getItem(`nyamanga.ui.current-library.account.v1:${id}`),
    account.account_id,
  );
  await disabledLibrary();
  const guard = page.getByRole("dialog", {
    name: "有尚未保存的修改",
    exact: true,
  });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
  expect(page.url()).toBe(initialURL);
  await expect(name).toHaveValue("匿名保留草稿");
  expect(
    await page.evaluate(
      (id) =>
        localStorage.getItem(`nyamanga.ui.current-library.account.v1:${id}`),
      account.account_id,
    ),
  ).toBe(preference);
  await disabledLibrary();
  await guard
    .getByRole("button", { name: "放弃修改并离开", exact: true })
    .click();
  await atLibrary(old.id);
  expect(new URL(page.url()).searchParams.has("series")).toBe(false);
  await expect(
    page.getByRole("textbox", { name: "系列名称", exact: true }),
  ).toHaveCount(0);
  // A late response from the old library cannot replace the new list.
  let releaseOld, oldStarted;
  const oldGate = new Promise((resolve) => (releaseOld = resolve)),
    oldRead = new Promise((resolve) => (oldStarted = resolve));
  let held = false;
  await page.route("**/api/v1/works?*", async (route) => {
    if (
      new URL(route.request().url()).searchParams.get("library_id") !==
        old.id ||
      held
    )
      return route.fallback();
    held = true;
    const response = await route.fetch();
    const body = await response.json();
    body.items.forEach((w) => {
      w.title_original = "旧库慢响应不可显示";
    });
    oldStarted();
    await oldGate;
    try {
      await route.fulfill({ response, json: body });
    } catch {
      /* Aborted old identity. */
    }
  });
  await page.goto(`/admin/versions?library_id=${old.id}`);
  await oldRead;
  await choose(fresh);
  await atLibrary(fresh.id);
  await expect(page.locator(".version-group-row")).toContainText("新库");
  releaseOld();
  await expect(
    page.getByText("旧库慢响应不可显示", { exact: true }),
  ).toHaveCount(0);
  // Catalog snapshots exercise UI failure and lifecycle cases without touching real data.
  let catalogPhase = "normal";
  await page.route("**/api/v1/libraries", async (route) => {
    if (route.request().method() !== "GET") return route.fallback();
    if (catalogPhase === "fail")
      return route.fulfill({
        status: 503,
        json: {
          error: { code: "catalog_unavailable", message: "匿名目录读取失败" },
        },
      });
    const response = await route.fetch(),
      body = await response.json();
    if (catalogPhase === "none") body.items = [];
    if (catalogPhase === "all-disabled")
      body.items = body.items.map((l) => ({ ...l, disabled: true }));
    if (catalogPhase === "disabled")
      body.items = body.items.map((l) =>
        l.id === fresh.id ? { ...l, disabled: true } : l,
      );
    if (catalogPhase === "trash")
      body.items = body.items.map((l) =>
        l.id === fresh.id ? { ...l, trashed_at: 1 } : l,
      );
    return route.fulfill({ response, json: body });
  });
  const notify = () =>
    page.evaluate(() =>
      window.dispatchEvent(
        new CustomEvent("resources-changed", { detail: { type: "library" } }),
      ),
    );
  catalogPhase = "none";
  mark = groupReads().length;
  await page.goto("/admin/versions");
  await expect(page.getByText("还没有书库", { exact: true })).toBeVisible();
  expect(groupReads()).toHaveLength(mark);
  catalogPhase = "all-disabled";
  await page.reload();
  await expect(page.getByText("暂无启用书库", { exact: true })).toBeVisible();
  expect(groupReads()).toHaveLength(mark);
  catalogPhase = "fail";
  await page.reload();
  await expect(
    page.getByText("匿名目录读取失败", { exact: true }),
  ).toBeVisible();
  expect(groupReads()).toHaveLength(mark);
  catalogPhase = "normal";
  await page.getByRole("button", { name: "重新加载", exact: true }).click();
  await atLibrary(fresh.id);
  catalogPhase = "fail";
  await notify();
  await expect(
    page.getByRole("button", { name: "重试书库目录", exact: true }),
  ).toBeVisible();
  await expect(picker()).toBeDisabled();
  await expect(page.locator(".version-group-row")).toHaveCount(1);
  catalogPhase = "normal";
  await page.getByRole("button", { name: "重试书库目录", exact: true }).click();
  await expect(picker()).toBeEnabled();
  await page.goto(
    `/admin/books?library_id=${fresh.id}&book=${identities[fresh.id].books[0].id}`,
  );
  const bookDialog = page.getByRole("dialog", { name: /^漫画详情/ });
  await bookDialog.getByRole("tab", { name: "元数据", exact: true }).click();
  const title = bookDialog.getByRole("textbox", {
    name: "其他标题",
    exact: true,
  });
  await title.fill("匿名冻结草稿");
  catalogPhase = "disabled";
  await notify();
  await expect(header()).toContainText("已停用");
  expect(new URL(page.url()).searchParams.get("library_id")).toBe(fresh.id);
  catalogPhase = "trash";
  await notify();
  await expect(
    page.getByText("当前书库已在回收站，请恢复书库或选择其他书库。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(bookDialog).not.toBeVisible();
  catalogPhase = "normal";
  await notify();
  await expect(bookDialog).toBeVisible();
  await expect(title).toHaveValue("匿名冻结草稿");
  let releaseSave, saveStarted;
  const saveGate = new Promise((resolve) => (releaseSave = resolve)),
    saveRead = new Promise((resolve) => (saveStarted = resolve));
  await page.route("**/api/v1/books/*/overrides", async (route) => {
    saveStarted();
    await saveGate;
    return route.continue();
  });
  await bookDialog
    .getByRole("button", { name: "保存修改", exact: true })
    .click();
  await saveRead;
  await expect(picker()).toBeDisabled();
  releaseSave();
  await expect(picker()).toBeEnabled();
  await bookDialog.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(bookDialog).not.toBeVisible();
  // An enabled choice in another tab updates account preference, not this tab.
  await status(old, false);
  await notify();
  const peer = await context.newPage();
  await routeViteSource(peer, baseURL);
  await peer.goto("/admin/versions");
  await expect(peer.locator(".current-library-header")).toContainText(
    fresh.display_name,
  );
  await choose(old, peer);
  await expect(peer).toHaveURL(new RegExp(`library_id=${old.id}`));
  await expect(header()).toContainText(fresh.display_name);
  await peer.close();
  await status(old, true);
  await notify();
  // No hidden library-changing task controls or recommendation runs were sent.
  expect(
    writes.filter((r) =>
      /\/(pause|cancel|work-discovery-jobs|series-discovery-jobs)$/.test(
        r.url.pathname,
      ),
    ),
  ).toHaveLength(0);
  const shots = path.join(
    fixture.run_directory,
    `current-library-${info.project.name}`,
  );
  await mkdir(shots, { recursive: true });
  await page.goto(
    `/admin/series?library_id=${fresh.id}&series=${identities[fresh.id].series}`,
  );
  await atLibrary(fresh.id);
  await expect(page.locator(".series-name-button")).toHaveText("新库系列");
  await expect(page.locator(".series-member-row")).toHaveCount(1);
  for (const width of info.project.name === "desktop" ? [1280, 1440] : [390]) {
    await page.setViewportSize({
      width,
      height: info.project.name === "desktop" ? 900 : 844,
    });
    for (const dark of [false, true]) {
      const toggle = page.getByRole("switch", { name: "深色主题" });
      await toggle.evaluate((el, value) => {
        if (el.getAttribute("aria-checked") !== String(value)) el.click();
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
        path: path.join(shots, `${width}-${dark ? "dark" : "light"}.png`),
        animations: "disabled",
      });
    }
  }
  for (const [name, url, label] of [
    ["all", "/admin/jobs?library_scope=all", "全部书库"],
    ["disabled", `/admin/versions?library_id=${old.id}`, "已停用"],
    ["global", "/admin/maintenance?section=trash", "全局"],
  ]) {
    await page.goto(url);
    await expect(picker()).toBeEnabled();
    const marker = header()
      .locator(".ant-tag")
      .filter({ hasText: new RegExp(`^${label}$`) });
    await expect(marker).toBeVisible();
    if (name === "disabled")
      await expect(page.locator(".version-group-row")).toHaveCount(1);
    if (name === "all")
      await expect(
        page.getByRole("heading", { name: "任务中心", exact: true }),
      ).toBeVisible();
    if (name === "global")
      await expect(
        page.getByRole("heading", { name: "维护中心", exact: true }),
      ).toBeVisible();
    for (const dark of [false, true]) {
      const toggle = page.getByRole("switch", { name: "深色主题" });
      await toggle.evaluate((el, value) => {
        if (el.getAttribute("aria-checked") !== String(value)) el.click();
      }, dark);
      await expect(toggle).toHaveAttribute("aria-checked", String(dark));
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: path.join(shots, `${name}-${dark ? "dark" : "light"}.png`),
        animations: "disabled",
      });
    }
  }
  expect(errors).toEqual([]);
});
