import { test, expect } from "@playwright/test";
import { readFile, copyFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);
test("五类单列作品、原文标题与完整详情内版本切换", async ({
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
  for (let i = 0; i < 29; i++)
    await copyFile(
      path.join(fixture.run_directory, "library", "合成1.cbz"),
      path.join(
        fixture.run_directory,
        "library",
        `作品卡片${String(i).padStart(2, "0")}.cbz`,
      ),
    );
  const auth = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous work cards 2026",
    },
  });
  expect(auth.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await auth.json()).csrf_token,
  };
  async function get(url) {
    const r = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(r.status(), url).toBe(200);
    return r.json();
  }
  async function write(url, data = {}, method = "POST", status = 200) {
    const r = await page.request.fetch(`${baseURL}/api/v1${url}`, {
      method,
      headers,
      data,
    });
    expect(r.status(), url).toBe(status);
    return r.json();
  }
  async function completed(id) {
    await expect
      .poll(async () => (await get(`/jobs/${id}`)).state)
      .toBe("completed");
  }
  const lib = await write(
    "/libraries",
    { root_key: "anonymous", display_name: "匿名作品库" },
    "POST",
    201,
  );
  await completed(
    (await write(`/libraries/${lib.id}/scans`, {}, "POST", 202)).job_id,
  );
  const books = (await get(`/editions?library_id=${lib.id}&page_size=100`))
      .items,
    t = (namespace, name) => ({ namespace, name });
  for (let i = 0; i < books.length; i++) {
    const b = await get(`/books/${books[i].id}`);
    const tags = [
      t("parody", "demo original"),
      t("character", "demo character"),
      t("artist", "demo artist"),
      t("group", "demo group"),
      t("language", "japanese"),
      t("unknown_ns", "custom content"),
    ];
    if (i === 2)
      tags.push(
        ...Array.from({ length: 19 }, (_, n) =>
          t(
            "artist",
            `long artist ${n} 星の旅行 長い名前 with multilingual words`,
          ),
        ),
      );
    await write(
      `/books/${b.id}/overrides`,
      {
        expected_revision: b.revision,
        fields: {
          title: {
            op: "set",
            value:
              i === 0
                ? "首选独有词"
                : i === 1
                  ? "命中非首选 Needle"
                  : i === 4
                    ? "匿名作品 04 星の物語 Long bilingual title ".repeat(7)
                    : `匿名作品 ${String(i).padStart(2, "0")} 星の物語 Long title`,
          },
          title_original: {
            op: "set",
            value:
              i === 4
                ? "［匿名］星の長い物語 日本語の原文タイトル ".repeat(9)
                : `[匿名] 星の物語 ${String(i).padStart(2, "0")}`,
          },
          tags: { op: "set", value: tags },
        },
      },
      "PATCH",
    );
  }
  const org = await get(
      `/work-organization/context?edition_ids=${books[0].id},${books[1].id}`,
    ),
    work = org.editions.find((e) => e.id === books[0].id).work_id;
  await write(`/works/${work}/merge`, {
    action: "work_merge",
    id: work,
    work_ids: [org.editions.find((e) => e.id === books[1].id).work_id],
    expected_revisions: org.expected_revisions,
  });
  const current = await get(`/works/${work}`);
  await write(
    `/works/${work}`,
    {
      action: "work_name",
      id: work,
      name: "［原样］Work 00 (特别篇)",
      name_op: "set",
      expected_revisions: { [`work:${work}`]: current.revision },
    },
    "PATCH",
  );
  const b0 = await get(`/books/${books[0].id}`);
  await write(
    `/books/${b0.id}/overrides`,
    {
      expected_revision: b0.revision,
      fields: { tags: { op: "set", value: [t("group", "only preferred")] } },
    },
    "PATCH",
  );
  const hidden = await get(`/books/${books[2].id}`);
  await write(
    `/editions/${hidden.id}/user-state`,
    { expected_revision: hidden.user_state.revision, hidden: true },
    "PATCH",
  );
  const groupContext = await get(`/work-organization/context?work_ids=${work}`);
  await write("/series", {
    action: "series_create",
    library_id: lib.id,
    name: "匿名所属系列",
    work_ids: [work],
    expected_revisions: groupContext.expected_revisions,
  });
  const tag = (
    await get(`/tag-catalog?library_id=${lib.id}&category=artist&q=demo artist`)
  ).items[0];
  await write(
    `/tags/${tag.id}/translations/zh-CN`,
    { expected_revision: tag.translation_revision, text: "匿名画师" },
    "PUT",
  );
  const apiPage = await get(
      `/tag-catalog/${tag.id}/works?library_id=${lib.id}&page_size=24`,
    ),
    target = apiPage.items.find((w) => w.id === work);
  expect(apiPage.total).toBe(books.length - 1);
  expect(target.cover.edition_id).toBe(books[0].id);
  // Live identities across the existing comic, version and series entry points
  // all use their own original-title summary without renaming Work/Series.
  await page.goto(`/admin/books?library_id=${lib.id}`);
  await expect(
    page.getByRole("button", { name: "复制标题 星の物語 00", exact: true }),
  ).toBeVisible();
  await page.goto(
    `/admin/versions?library_id=${lib.id}&work=${encodeURIComponent(work)}`,
  );
  await expect(
    page.locator(".version-group-row .version-row-title"),
  ).toHaveText("星の物語 00");
  await expect(
    page.getByRole("button", {
      name: "复制标题 [匿名] 星の物語 01",
      exact: true,
    }),
  ).toBeVisible();
  const withSeries = await get(`/works/${work}`);
  await page.goto(
    `/admin/series?library_id=${lib.id}&series=${withSeries.series_id}`,
  );
  await expect(
    page.getByRole("button", {
      name: "复制标题 [匿名] 星の物語 00",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "匿名所属系列", exact: true }),
  ).toBeVisible();
  const reads = [],
    errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    if (r.method() === "GET" && new URL(r.url()).pathname.startsWith("/api/"))
      reads.push(new URL(r.url()));
  });
  const directory = page.getByRole("region", { name: "标签目录" }),
    tagModal = page.locator(".tag-detail-modal"),
    workModal = page.getByRole("dialog", { name: /漫画详情/ });
  const card = (id) => directory.locator(`[data-tag-id="${id}"]`),
    workCard = (id) => tagModal.locator(`[data-work-id="${id}"]`);
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/maintenance/tag-direct-original-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  async function shot(name) {
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBe(page.viewportSize().width);
    await page.screenshot({
      path: path.join(shots, `${info.project.name}-${name}.png`),
      fullPage: !(await page.locator(".ant-modal-wrap:visible").count()),
      animations: "disabled",
    });
  }
  await page.route(`**/api/v1/editions/${books[3].id}/cover?**`, (route) =>
    route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "not_found", message: "匿名缺封面" },
      }),
    }),
  );
  await page.goto(`/admin/tags?library_id=${lib.id}&category=artist`);
  await expect(card(tag.id)).toBeVisible();
  await expect(directory.locator(".tag-catalog-card")).toHaveCount(20);
  await expect(directory.locator("table")).toHaveCount(0);
  await shot("directory");
  await card(tag.id).click();
  await expect(
    tagModal.getByRole("heading", { name: "匿名画师", exact: true }),
  ).toBeVisible();
  await expect(tagModal.locator(".tag-work-card")).toHaveCount(24);
  await expect(tagModal.locator(".tag-heading-count")).toHaveText(
    `${apiPage.total} 个作品`,
  );
  await expect(
    tagModal.getByRole("button", { name: "否决本书推断", exact: true }),
  ).toHaveCount(0);
  await expect(tagModal.getByText(/个版本/)).toHaveCount(0);
  expect(
    reads.filter((u) =>
      /\/tag-catalog\/[^/]+\/(relations|editions)$/.test(u.pathname),
    ),
  ).toHaveLength(0);
  expect(
    reads.filter((u) => /^\/api\/v1\/works\//.test(u.pathname)),
  ).toHaveLength(0);
  await expect(workCard(work).locator("img")).toHaveAttribute(
    "src",
    new RegExp(`/editions/${books[0].id}/cover\\?content_revision=`),
  );
  await expect(
    tagModal.locator('.tag-work-cover [aria-label="暂无作品封面"]').first(),
  ).toBeVisible();
  for (const scheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
      });
      await shot(`multi-${width}-${scheme}`);
    }
  }
  const translation = tagModal.getByRole("textbox", { name: "标签中文译名" });
  await translation.fill("未提交译名草稿");
  await expect(
    tagModal.getByRole("button", { name: "保存译名", exact: true }),
  ).toBeEnabled();
  const parentBody = tagModal.locator(".ant-modal-body");
  await parentBody.evaluate((e) => (e.scrollTop = 120));
  const parentScroll = await parentBody.evaluate((e) => e.scrollTop);
  await expect(workCard(work)).toHaveAccessibleName(target.title_original);
  await expect(workCard(work)).not.toHaveAttribute("title");
  await workCard(work).hover();
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await workCard(work).click();
  const editor = workModal;
  await expect(editor).toBeVisible();
  await expect(editor.locator(".book-dialog-heading")).toContainText(
    target.title_original,
  );
  await expect(
    editor.getByRole("button", { name: "匿名所属系列", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".ant-modal-mask:visible")).toHaveCount(1);
  expect(
    reads.filter((u) => u.pathname === `/api/v1/works/${work}/editions`),
  ).toHaveLength(0);
  await shot("direct-complete-detail");
  await editor.getByRole("button", { name: "其他版本", exact: true }).click();
  await expect(editor.locator(".book-version-option")).toHaveCount(2);
  await expect(page).toHaveURL(/details=versions/);
  await editor.locator(`[data-version-id="${books[1].id}"]`).click();
  await expect(editor.locator(".book-dialog-heading")).toContainText(
    "星の物語 01",
  );
  await expect(page.locator(".ant-modal-mask:visible")).toHaveCount(1);
  await editor.getByRole("tab", { name: "元数据", exact: true }).click();
  await editor.getByLabel("其他标题", { exact: true }).fill("未提交版本草稿");
  await editor.getByRole("button", { name: "其他版本", exact: true }).click();
  await editor.locator(`[data-version-id="${books[0].id}"]`).click();
  const switchGuard = page.getByRole("dialog", {
    name: "放弃当前版本未保存的修改？",
    exact: true,
  });
  await expect(switchGuard).toBeVisible();
  await switchGuard
    .getByRole("button", { name: "继续编辑", exact: true })
    .click();
  await expect(editor.getByLabel("其他标题", { exact: true })).toHaveValue(
    "未提交版本草稿",
  );
  await page.evaluate(() => history.back());
  await expect(editor.getByLabel("其他标题", { exact: true })).toHaveValue(
    "未提交版本草稿",
  );
  await expect(page).not.toHaveURL(/details=versions/);
  await page.evaluate(() => history.back());
  const navigationGuard = page.getByRole("dialog", {
    name: "有尚未保存的修改",
    exact: true,
  });
  await expect(navigationGuard).toBeVisible();
  await navigationGuard
    .getByRole("button", { name: "继续编辑", exact: true })
    .click();
  await expect(editor.getByLabel("其他标题", { exact: true })).toHaveValue(
    "未提交版本草稿",
  );
  await editor
    .locator(".book-dialog-heading")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "放弃尚未保存的编辑？", exact: true })
    .getByRole("button", { name: "放弃并关闭", exact: true })
    .click();
  await expect(translation).toHaveValue("未提交译名草稿");
  await expect(workCard(work)).toBeFocused();
  await expect(parentBody).toHaveJSProperty("scrollTop", parentScroll);
  await tagModal
    .getByRole("button", { name: "放弃译名修改", exact: true })
    .click();
  // API Work count and cross-page uniqueness are used directly, with an independent snapshot.
  const firstIDs = await tagModal
    .locator(".tag-work-card")
    .evaluateAll((es) => es.map((e) => e.dataset.workId));
  await tagModal.getByRole("listitem", { name: "2", exact: true }).click();
  await expect(tagModal.locator(".tag-work-card")).toHaveCount(
    apiPage.total - 24,
  );
  const secondIDs = await tagModal
    .locator(".tag-work-card")
    .evaluateAll((es) => es.map((e) => e.dataset.workId));
  expect(new Set([...firstIDs, ...secondIDs]).size).toBe(apiPage.total);
  const workReads = reads.filter(
    (u) => u.pathname === `/api/v1/tag-catalog/${tag.id}/works`,
  );
  expect(
    workReads
      .find((u) => u.searchParams.get("page") === "2")
      .searchParams.get("snapshot"),
  ).toBeTruthy();
  for (const u of workReads)
    for (const forbidden of [
      "category",
      "namespace",
      "include_unused",
      "related_tag_id",
      "partition",
      "view",
    ])
      expect(u.searchParams.has(forbidden)).toBe(false);
  // A catalog change invalidates this Work page; only an explicit restart renews it.
  const latest = await get(`/tag-catalog/${tag.id}?library_id=${lib.id}`);
  await write(
    `/tags/${tag.id}/translations/zh-CN`,
    { expected_revision: latest.translation_revision, text: "匿名画师更新" },
    "PUT",
  );
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", {
        detail: { type: "tag_translation" },
      }),
    ),
  );
  await expect(
    tagModal.getByRole("button", { name: "重新载入此列表", exact: true }),
  ).toBeVisible();
  await tagModal
    .getByRole("button", { name: "重新载入此列表", exact: true })
    .click();
  await expect(
    tagModal.getByRole("button", { name: "重新载入此列表", exact: true }),
  ).toHaveCount(0);
  await tagModal
    .getByRole("searchbox", { name: "搜索关联作品", exact: true })
    .fill("Needle");
  await tagModal
    .getByRole("searchbox", { name: "搜索关联作品", exact: true })
    .press("Enter");
  await expect(workCard(work)).toBeVisible();
  await expect(tagModal.locator(".tag-work-card")).toHaveCount(1);
  // Recycled preferred Edition keeps the original Work name but uses the server's replacement representative.
  const freshBook = await get(`/books/${books[0].id}`),
    plan = await write("/maintenance/plans", {
      operation: "trash",
      book_ids: [freshBook.id],
      expected_revisions: { [`book:${freshBook.id}`]: freshBook.revision },
      options: {},
    });
  await completed(
    (await write(`/maintenance/plans/${plan.id}/execute`, {}, "POST", 202))
      .job_id,
  );
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", { detail: { type: "book" } }),
    ),
  );
  await expect(workCard(work).locator("img")).toHaveAttribute(
    "src",
    new RegExp(`/editions/${books[1].id}/cover\\?`),
  );
  await expect(workCard(work)).toHaveAccessibleName(target.title_original);
  await workCard(work).click();
  await expect(editor.locator(".book-dialog-heading")).toContainText(
    "星の物語 01",
  );
  await expect(page).toHaveURL(new RegExp(`work=${encodeURIComponent(work)}`));
  await editor.getByRole("button", { name: "其他版本", exact: true }).click();
  await editor.locator(`[data-version-id="${books[0].id}"]`).click();
  await expect(
    page.getByText("此版本已在回收站，不能在此编辑。", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("tab", { name: "元数据", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "返回标签", exact: true }).click();
  await expect(page).not.toHaveURL(/[?&]work=/);
  await expect(workModal).toHaveCount(0);
  await expect(tagModal).toBeVisible();
  await translation.focus();
  for (const scheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
      });
      await shot(`ordinary-${width}-${scheme}`);
    }
  }
  // Leave a pending Work read, then open another identity before its late reply.
  await tagModal
    .getByRole("searchbox", { name: "搜索关联作品", exact: true })
    .fill("");
  await tagModal
    .getByRole("searchbox", { name: "搜索关联作品", exact: true })
    .press("Enter");
  await expect(tagModal.locator(".tag-work-card")).toHaveCount(24);
  let releaseOld, startedOld;
  const oldGate = new Promise((r) => (releaseOld = r)),
    oldReady = new Promise((r) => (startedOld = r));
  await page.route(
    `**/api/v1/works/${encodeURIComponent(work)}`,
    async (route) => {
      const response = await route.fetch();
      startedOld();
      await oldGate;
      await route.fulfill({ response }).catch(() => {});
    },
  );
  await workCard(work).click();
  await oldReady;
  await workModal
    .getByRole("button", { name: "返回标签", exact: true })
    .click();
  const otherWork = apiPage.items.find((v) => v.id !== work);
  await workCard(otherWork.id).click();
  await expect(workModal.locator(".book-dialog-heading")).toContainText(
    otherWork.title_original,
  );
  releaseOld();
  await expect(workModal.locator(".book-dialog-heading")).toContainText(
    otherWork.title_original,
  );
  await workModal.getByRole("button", { name: "关闭", exact: true }).click();
  await tagModal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  await expect(card(tag.id)).toBeFocused();
  // All five direct categories; ordinary content has no diagnostic endpoint or inference actions.
  for (const [category, name] of [
    ["parody", "demo original"],
    ["character", "demo character"],
    ["group", "demo group"],
    ["content", "custom content"],
  ]) {
    await page.goto(
      `/admin/tags?library_id=${lib.id}&category=${category}${category === "content" ? "&namespace=unknown_ns" : ""}`,
    );
    const summary = (
      await get(
        `/tag-catalog?library_id=${lib.id}&category=${category}&q=${name}`,
      )
    ).items[0];
    await card(summary.id).click();
    await expect(
      tagModal.getByRole("heading", { name, exact: true }),
    ).toBeVisible();
    await expect(tagModal.locator(".tag-work-card").first()).toBeVisible();
    await expect(
      tagModal.getByRole("button", { name: "否决本书推断", exact: true }),
    ).toHaveCount(0);
  }
  // Legacy Work/internal-version deep links resolve straight to complete details,
  // retaining the server replacement representative after preferred recycling.
  await page.goto(
    `/admin/tags?library_id=${lib.id}&category=artist&tag=${tag.id}&work=${encodeURIComponent(work)}&details=versions`,
  );
  await expect(editor.locator(".book-dialog-heading")).toContainText(
    "星の物語 01",
  );
  await expect(editor.locator(".book-version-option")).toHaveCount(2);
  await expect(page.locator(".ant-modal-mask:visible")).toHaveCount(1);
  await editor.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(tagModal).toBeVisible();
  // A global tag identity never makes a foreign Work switch the workspace library.
  const foreignLib = await write(
    "/libraries",
    { root_key: "anonymous-work", display_name: "另一匿名库" },
    "POST",
    201,
  );
  await completed(
    (await write(`/libraries/${foreignLib.id}/scans`, {}, "POST", 202)).job_id,
  );
  const foreignWork = (await get(`/editions?library_id=${foreignLib.id}`))
    .items[0].work_id;
  await page.goto(
    `/admin/tags?library_id=${lib.id}&category=artist&tag=${tag.id}&work=${foreignWork}`,
  );
  await expect(
    workModal.getByText("此作品不属于当前书库，请返回标签重新选择。", {
      exact: true,
    }),
  ).toBeVisible();
  expect(new URL(page.url()).searchParams.get("library_id")).toBe(lib.id);
  await expect(
    workModal.getByRole("button", { name: "其他版本", exact: true }),
  ).toHaveCount(0);
  await workModal
    .getByRole("button", { name: "返回标签", exact: true })
    .click();
  await tagModal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  // If the source edit removes the opener card, closing still restores useful focus.
  const tiny = (
    await get(
      `/tag-catalog?library_id=${lib.id}&category=artist&q=long%20artist%200`,
    )
  ).items[0];
  await card(tiny.id).click();
  await expect(tagModal.locator(".tag-heading-count")).toHaveText("1 个作品");
  const tagged = await get(`/books/${books[2].id}`);
  await write(
    `/books/${tagged.id}/overrides`,
    {
      expected_revision: tagged.revision,
      fields: {
        tags: {
          op: "set",
          value: tagged.metadata.fields.tags.filter(
            (t) => !(t.namespace === tiny.namespace && t.name === tiny.name),
          ),
        },
      },
    },
    "PATCH",
  );
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", { detail: { type: "tags" } }),
    ),
  );
  await expect(card(tiny.id)).toHaveCount(0);
  await expect(tagModal.locator(".tag-heading-count")).toHaveText("0 个作品");
  await tagModal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  await expect(
    directory.getByRole("searchbox", {
      name: "搜索标签原文或译名",
      exact: true,
    }),
  ).toBeFocused();
  expect(
    reads.filter((u) => /\/tag-catalog\/[^/]+\/editions$/.test(u.pathname)),
  ).toHaveLength(0);
  expect(
    reads
      .filter((u) => /\/tag-catalog\/[^/]+\/relations$/.test(u.pathname))
      .every((u) => u.searchParams.get("view") === "display"),
  ).toBe(true);
  expect(errors).toEqual([]);
});
