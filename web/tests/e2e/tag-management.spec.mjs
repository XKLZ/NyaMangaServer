import { test, expect } from "@playwright/test";
import { readFile, mkdir, copyFile } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);
test("标签目录、双向关系、独立CAS与当前库草稿保护", async ({
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
        `匿名标签${String(i).padStart(2, "0")}.cbz`,
      ),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous tags password 2026",
    },
  });
  expect(setup.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await setup.json()).csrf_token,
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
  const reads = [],
    writes = [],
    errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (!u.pathname.startsWith("/api/v1")) return;
    if (r.method() === "GET") reads.push(u);
    else writes.push({ url: u, body: r.postDataJSON(), method: r.method() });
  });
  await page.goto("/admin/tags");
  await expect(page.getByText("还没有书库", { exact: true })).toBeVisible();
  expect(
    reads.filter(
      (u) =>
        u.pathname.startsWith("/api/v1/tag-catalog") &&
        !u.pathname.includes("/images"),
    ),
  ).toHaveLength(0);
  const lib = await write(
      "/libraries",
      { root_key: "anonymous", display_name: "A 匿名标签" },
      "POST",
      201,
    ),
    otherLib = await write(
      "/libraries",
      { root_key: "anonymous-work", display_name: "B 独立标签" },
      "POST",
      201,
    );
  for (const l of [lib, otherLib]) {
    const job = await write(`/libraries/${l.id}/scans`, {}, "POST", 202);
    await expect
      .poll(async () => (await get(`/jobs/${job.job_id}`)).state)
      .toBe("completed");
  }
  const books = (await get(`/editions?library_id=${lib.id}&page_size=100`))
      .items,
    foreign = (await get(`/editions?library_id=${otherLib.id}&page_size=100`))
      .items;
  const t = (namespace, name) => ({ namespace, name });
  for (let i = 0; i < books.length; i++) {
    const b = await get(`/books/${books[i].id}`);
    let tags;
    if (i === 0)
      tags = [
        t("parody", "alpha"),
        t("character", "hero"),
        t("artist", "creator"),
        t("group", "circle"),
        t("language", "japanese"),
        t("female", "uniform"),
        t("unknown_ns", "odd"),
        ...Array.from({ length: 55 }, (_, j) =>
          t("artist", `author ${String(j).padStart(2, "0")}`),
        ),
      ];
    else if (i === 1) tags = [t("parody", "beta"), t("character", "hero")];
    else if (i === 2)
      tags = [
        t("parody", "alpha"),
        t("parody", "beta"),
        t("character", "hero"),
      ];
    else if (i === 3)
      tags = [
        t("parody", "alpha"),
        t("parody", "gamma"),
        t("character", "hero"),
        t("character", "newcomer"),
      ];
    else if (i === 4)
      tags = [t("parody", "original"), t("character", "orphan")];
    else
      tags = [
        t("parody", "alpha"),
        t("character", `person ${String(i).padStart(2, "0")}`),
      ];
    await write(
      `/books/${b.id}/overrides`,
      {
        expected_revision: b.revision,
        fields: {
          title: {
            op: "set",
            value: `匿名漫画 ${String(i).padStart(2, "0")} · 星の物語 Long bilingual title`,
          },
          tags: { op: "set", value: tags },
        },
      },
      "PATCH",
    );
  }
  for (const f of foreign) {
    const b = await get(`/books/${f.id}`);
    await write(
      `/books/${b.id}/overrides`,
      {
        expected_revision: b.revision,
        fields: {
          tags: {
            op: "set",
            value: [
              t("parody", "delta"),
              t("character", "zero"),
              t("artist", "unused creator"),
            ],
          },
        },
      },
      "PATCH",
    );
  }
  // Two physical Editions in one Work still count once as a Work.
  const org = await get(
    `/work-organization/context?edition_ids=${books[0].id},${books[5].id}`,
  );
  const work = org.editions.find((e) => e.id === books[0].id).work_id;
  await write(`/works/${work}/merge`, {
    action: "work_merge",
    id: work,
    work_ids: [org.editions.find((e) => e.id === books[5].id).work_id],
    expected_revisions: org.expected_revisions,
  });
  const directory = await get(
      `/tag-catalog?library_id=${lib.id}&category=parody`,
    ),
    alpha = directory.items.find((t) => t.name === "alpha");
  expect(alpha.work_count).toBe(alpha.edition_count - 1);
  const hero = (
    await get(`/tag-catalog?library_id=${lib.id}&category=character&q=hero`)
  ).items[0];
  const zero = (
    await get(`/tag-catalog?library_id=${lib.id}&category=character&q=newcomer`)
  ).items.find((t) => t.name === "newcomer");
  await write(
    `/tags/${alpha.id}/translations/zh-CN`,
    { expected_revision: alpha.translation_revision, text: "星之原作" },
    "PUT",
  );
  const relationTargets = (
    await get(
      `/tag-catalog?library_id=${lib.id}&category=character&page_size=100`,
    )
  ).items;
  const orphan = relationTargets.find((t) => t.name === "orphan"),
    excluded = relationTargets.find((t) => t.name === "person 06");
  await write(
    `/parody-character-relations/${alpha.id}/${orphan.id}`,
    { expected_revision: 0, decision: "confirmed" },
    "PUT",
  );
  await write(
    `/parody-character-relations/${alpha.id}/${excluded.id}`,
    { expected_revision: 0, decision: "excluded" },
    "PUT",
  );
  const display = await get(
    `/tag-catalog/${alpha.id}/relations?library_id=${lib.id}&view=display&page_size=100`,
  );
  expect(
    display.items.some(
      (r) => r.character.id === orphan.id && r.edition_count === 0,
    ),
  ).toBe(true);
  expect(display.items.some((r) => r.character.id === excluded.id)).toBe(false);
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/maintenance/tag-direct-original-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  async function displayShots(kind) {
    const active = page.locator(".tag-session:not([hidden])");
    await expect(active.locator(".tag-relation-cards")).toHaveAttribute(
      "aria-busy",
      "false",
    );
    await expect(
      active.locator(".tag-relation-cards .tag-catalog-card").first(),
    ).toBeVisible();
    await expect(active.locator(".tag-work-grid")).toHaveAttribute(
      "aria-busy",
      "false",
    );
    for (const colorScheme of ["light", "dark"]) {
      await page.emulateMedia({ colorScheme });
      for (const width of info.project.name === "mobile"
        ? [390]
        : [1280, 1440]) {
        await page.setViewportSize({
          width,
          height: info.project.name === "mobile" ? 844 : 900,
        });
        await page.screenshot({
          path: path.join(
            shots,
            `${info.project.name}-default-${kind}-${width}-${colorScheme}.png`,
          ),
          animations: "disabled",
        });
      }
    }
  }
  const target = `/admin/tags?library_id=${lib.id}&tag=${alpha.id}`;
  await page.goto(target);
  await expect(
    page.getByRole("heading", { name: "星之原作", exact: true }),
  ).toBeVisible();
  const details = page.locator(".tag-detail-modal"),
    catalog = page.getByRole("region", { name: "标签目录" });
  await expect(
    catalog.getByRole("link", { name: "delta", exact: true }),
  ).toHaveCount(0);
  await expect(details.locator(".tag-heading-count")).toHaveText(
    `${alpha.work_count} 个作品`,
  );
  await expect(details.locator(".tag-display-relations:visible")).toBeVisible();
  await expect(details.locator(".tag-work-card").first()).toBeVisible();
  await expect(
    details.getByRole("button", { name: "将此角色关联到该原作", exact: true }),
  ).toHaveCount(0);
  await expect(
    details.getByRole("region", { name: "关联漫画", exact: true }),
  ).toHaveCount(0);
  expect(
    reads
      .filter((u) => u.pathname.endsWith(`/${alpha.id}/relations`))
      .every((u) => u.searchParams.get("view") === "display"),
  ).toBe(true);
  expect(
    reads.filter((u) => u.pathname.endsWith(`/${alpha.id}/editions`)),
  ).toHaveLength(0);
  await expect(
    details.locator(".tag-relation-cards .tag-catalog-card"),
  ).toHaveCount(24);
  await expect(
    details.locator(".tag-display-relations .tag-pager"),
  ).toContainText(`${display.total} 个关联`);
  await expect(details.locator(".tag-relation-cards [title]")).toHaveCount(0);
  await expect(
    details.locator(`.tag-relation-cards [data-tag-id="${orphan.id}"]`),
  ).toBeVisible();
  await expect(
    details.locator(`.tag-relation-cards [data-tag-id="${excluded.id}"]`),
  ).toHaveCount(0);
  await displayShots("original");
  const defaultInput = details.getByRole("textbox", { name: "标签中文译名" });
  await defaultInput.fill("父标签草稿");
  await details
    .locator(".tag-relation-cards")
    .getByRole("link", { name: "hero", exact: true })
    .click();
  await expect(
    details.getByRole("heading", { name: "hero", exact: true }),
  ).toBeVisible();
  await expect(details.locator(".tag-display-relations:visible")).toBeVisible();
  await expect(
    details.getByRole("button", { name: "将此角色关联到该原作", exact: true }),
  ).toHaveCount(0);
  await displayShots("character");
  await details
    .getByRole("button", { name: "返回上一个标签", exact: true })
    .click();
  await expect(defaultInput).toHaveValue("父标签草稿");
  await details
    .getByRole("button", { name: "放弃译名修改", exact: true })
    .click();
  await details
    .getByRole("button", { name: "编辑角色归属", exact: true })
    .click();
  await expect(
    details.getByText("原作归属竞争", { exact: true }).first(),
  ).toBeVisible();
  const input = details.getByRole("textbox", { name: "标签中文译名" }),
    save = details.getByRole("button", { name: "保存译名", exact: true });
  await expect(save).toBeDisabled();
  await input.fill("临时译名");
  await expect(save).toBeEnabled();
  await input.fill("星之原作");
  await expect(save).toBeDisabled();
  await input.fill("草稿保留");
  const before = await get(`/tag-catalog/${alpha.id}?library_id=${lib.id}`);
  await write(
    `/tags/${alpha.id}/translations/zh-CN`,
    { expected_revision: before.translation_revision, text: "另一窗口译名" },
    "PUT",
  );
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", {
        detail: { type: "tag_relations" },
      }),
    ),
  );
  await expect(
    details.getByText("译名已变化，草稿保留", { exact: true }),
  ).toBeVisible();
  await expect(input).toHaveValue("草稿保留");
  await expect(save).toBeDisabled();
  await details
    .getByRole("button", { name: "核对后保留草稿并采用此版本" })
    .click();
  await save.click();
  await expect(save).toBeDisabled();
  await expect(input).toHaveValue("草稿保留");
  const translationWrite = writes
    .filter((w) =>
      w.url.pathname.endsWith(`/tags/${alpha.id}/translations/zh-CN`),
    )
    .at(-1);
  expect(translationWrite.body.expected_revision).toBe(
    before.translation_revision + 1,
  );
  await input.fill("resize draft");
  const viewport = page.viewportSize();
  await page.setViewportSize({ width: 800, height: 900 });
  await expect(input).toHaveValue("resize draft");
  await page.setViewportSize(viewport);
  await expect(input).toHaveValue("resize draft");
  await details.getByRole("button", { name: "放弃译名修改" }).click();
  await input.fill("book retained draft");
  await details
    .locator(".tag-edition-info")
    .first()
    .getByRole("link")
    .first()
    .click();
  const bookDialog = page.getByRole("dialog", { name: /漫画详情/ });
  await expect(bookDialog).toBeVisible();
  await bookDialog.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(input).toHaveValue("book retained draft");
  await details.getByRole("button", { name: "放弃译名修改" }).click();
  const heroRow = () =>
    details
      .locator(".tag-relation-row")
      .filter({ has: page.getByRole("link", { name: "hero", exact: true }) });
  await heroRow().getByRole("link", { name: "hero", exact: true }).click();
  await expect(
    details.getByRole("heading", { name: "hero", exact: true }),
  ).toBeVisible();
  await details
    .getByRole("button", { name: "返回上一个标签", exact: true })
    .click();
  await heroRow()
    .getByRole("button", { name: "查看漫画", exact: true })
    .click();
  const editionTitle = await details
    .locator(".tag-edition-info")
    .first()
    .getByRole("link")
    .first()
    .textContent();
  const edition = (
    await get(
      `/tag-catalog/${alpha.id}/editions?library_id=${lib.id}&related_tag_id=${hero.id}&q=${encodeURIComponent(editionTitle)}`,
    )
  ).items.find((e) => e.title === editionTitle);
  await details
    .getByRole("button", { name: "仅此版本取消自动匹配", exact: true })
    .first()
    .click();
  let dialog = page.getByRole("dialog", {
    name: "仅此版本取消自动匹配",
    exact: true,
  });
  await expect(dialog.getByText(/不删除原始标签/)).toBeVisible();
  await write(
    `/editions/${edition.id}/character-suppressions/${alpha.id}/${hero.id}`,
    { expected_revision: 0 },
    "PUT",
  );
  await write(
    `/editions/${edition.id}/character-suppressions/${alpha.id}/${hero.id}`,
    { expected_revision: 1 },
    "DELETE",
  );
  await dialog
    .getByRole("button", { name: "确认仅此版本取消自动匹配", exact: true })
    .click();
  await expect(
    dialog.getByRole("button", {
      name: "确认仅此版本取消自动匹配",
      exact: true,
    }),
  ).toBeDisabled();
  await dialog.getByRole("button", { name: "读取当前状态以核对" }).click();
  await dialog
    .getByRole("button", { name: "保留此操作并采用当前版本" })
    .click();

  await dialog
    .getByRole("button", { name: "确认仅此版本取消自动匹配", exact: true })
    .click();
  await expect(dialog).not.toBeVisible();
  await details.getByLabel("漫画关系分区").click();
  await page.getByRole("option", { name: "逐本否决", exact: true }).click();
  await details
    .getByRole("button", { name: "恢复此版本自动匹配", exact: true })
    .click();
  dialog = page.getByRole("dialog", {
    name: "恢复此版本自动匹配",
    exact: true,
  });
  await dialog
    .getByRole("button", { name: "确认恢复此版本自动匹配", exact: true })
    .click();
  await expect(dialog).not.toBeVisible();
  await details.getByRole("button", { name: "添加关联", exact: true }).click();
  const picker = page.getByRole("dialog", { name: /添加角色关系/ });
  await expect(picker.getByRole("checkbox", { name: /零使用/ })).toHaveCount(0);
  await picker
    .getByRole("searchbox", { name: "搜索关系目标原文或译名" })
    .fill("newcomer");
  await picker
    .getByRole("searchbox", { name: "搜索关系目标原文或译名" })
    .press("Enter");
  await picker
    .getByRole("row")
    .filter({ hasText: "character:newcomer" })
    .getByRole("radio")
    .check();
  await picker.getByRole("button", { name: "核对影响", exact: true }).click();
  dialog = page.getByRole("dialog", {
    name: "将此角色关联到该原作",
    exact: true,
  });
  await expect(
    dialog.getByText("全局人工决定 · 影响所有书库", { exact: true }),
  ).toBeVisible();
  await write(
    `/parody-character-relations/${alpha.id}/${zero.id}`,
    { expected_revision: 0, decision: "confirmed" },
    "PUT",
  );
  await write(
    `/parody-character-relations/${alpha.id}/${zero.id}`,
    { expected_revision: 1 },
    "DELETE",
  );
  await dialog.getByRole("button", { name: "确认归属", exact: true }).click();
  await expect(dialog.getByText(/标签关系已变化/)).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "确认归属", exact: true }),
  ).toBeDisabled();
  await dialog.getByRole("button", { name: "读取当前状态以核对" }).click();
  await dialog
    .getByRole("button", { name: "保留此操作并采用当前版本" })
    .click();
  await dialog.getByRole("button", { name: "确认归属", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const zeroRow = details
    .locator(".tag-relation-row")
    .filter({ has: page.getByRole("link", { name: "newcomer", exact: true }) });
  await details
    .getByRole("searchbox", { name: "搜索关系原文或译名" })
    .fill("newcomer");
  await details
    .getByRole("searchbox", { name: "搜索关系原文或译名" })
    .press("Enter");
  await expect(zeroRow.getByText("人工关联", { exact: true })).toBeVisible();
  await expect(
    zeroRow.getByText("已归属 1 作品 / 1 版本", { exact: true }),
  ).toBeVisible();
  await zeroRow
    .getByRole("button", { name: "排除此关联", exact: true })
    .click();
  dialog = page.getByRole("dialog", { name: "排除此关联", exact: true });
  await dialog
    .getByRole("button", { name: "确认排除此关联", exact: true })
    .click();
  await expect(dialog).not.toBeVisible();
  await expect(
    zeroRow.getByText("全局排除", { exact: true }).first(),
  ).toBeVisible();
  await zeroRow
    .getByRole("button", { name: "恢复自动匹配", exact: true })
    .click();
  dialog = page.getByRole("dialog", { name: "恢复自动匹配", exact: true });
  await dialog
    .getByRole("button", { name: "确认恢复自动匹配", exact: true })
    .click();
  await expect(dialog).not.toBeVisible();
  const aba = writes.filter((w) =>
    w.url.pathname.endsWith(
      `/parody-character-relations/${alpha.id}/${zero.id}`,
    ),
  );
  expect(aba.map((w) => w.body.expected_revision)).toEqual([0, 2, 3, 4]);
  await details.getByRole("button", { name: /查看未归类/ }).click();
  await expect(page).toHaveURL(/partition=unclassified/);
  await details.locator(".tag-detail-heading").scrollIntoViewIfNeeded();
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    for (const width of info.project.name === "mobile" ? [390] : [1280, 1440]) {
      await page.setViewportSize({
        width,
        height: info.project.name === "mobile" ? 844 : 900,
      });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: path.join(
          shots,
          `${info.project.name}-${width}-${colorScheme}.png`,
        ),
        fullPage: false,
        animations: "disabled",
      });
    }
  }
  await input.fill("跨库草稿");
  // A centered Modal intentionally owns focus; close uses the shared draft guard.
  await details
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  const guard = page.getByRole("dialog", {
    name: "有尚未保存的修改",
    exact: true,
  });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(input).toHaveValue("跨库草稿");
  await details.getByRole("button", { name: "放弃译名修改" }).click();
  await details
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  await page.goto(`/admin/tags?library_id=${lib.id}&category=artist&page=2`);
  await expect(catalog.getByText("56 个标签", { exact: true })).toBeVisible();
  await expect(
    catalog.getByRole("link", { name: "creator", exact: true }),
  ).toBeVisible();
  const creator = (
    await get(`/tag-catalog?library_id=${lib.id}&category=artist&q=creator`)
  ).items[0];
  await write(
    `/tags/${creator.id}/translations/zh-CN`,
    { expected_revision: creator.translation_revision, text: "匿名作者" },
    "PUT",
  );
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", {
        detail: { type: "tag_translation" },
      }),
    ),
  );
  const restart = catalog.getByRole("button", {
    name: "重新载入此列表",
    exact: true,
  });
  await expect(restart).toBeVisible();
  await restart.click();
  await expect(
    catalog.getByRole("link", { name: "匿名作者", exact: true }),
  ).toBeVisible();
  const catalogSecond = reads.filter(
    (u) =>
      u.pathname === "/api/v1/tag-catalog" &&
      u.searchParams.get("page") === "2",
  );
  expect(catalogSecond.at(-1).searchParams.get("snapshot")).toBeTruthy();
  for (const [category, namespace, name] of [
    ["content", "unknown_ns", "odd"],
    ["content", "language", "japanese"],
    ["group", "", "circle"],
  ]) {
    await page.goto(
      `/admin/tags?library_id=${lib.id}&category=${category}&namespace=${namespace}`,
    );
    await expect(
      catalog.getByRole("link", { name, exact: true }),
    ).toBeVisible();
  }
  await catalog.getByRole("link", { name: "circle", exact: true }).click();
  await expect(
    details.getByRole("heading", { name: "circle", exact: true }),
  ).toBeVisible();
  await expect(
    details.getByRole("region", { name: "原作角色关系", exact: true }),
  ).toHaveCount(0);
  const closeOrdinary = details.getByRole("button", {
    name: "关闭标签详情",
    exact: true,
  });
  await closeOrdinary.focus();
  await closeOrdinary.press("Enter");
  await expect(
    catalog.getByRole("link", { name: "circle", exact: true }),
  ).toBeFocused();
  for (const q of ["草稿保留", "alpha"]) {
    await page.goto(`/admin/tags?library_id=${lib.id}&q=${q}`);
    await expect(
      catalog.getByRole("link", { name: "草稿保留", exact: true }),
    ).toBeVisible();
  }
  // Each independently paginated endpoint bootstraps its own token.
  await page.goto(`${target}&relations=edit&r_page=2&e_page=2`);
  await expect
    .poll(
      () =>
        reads.filter(
          (u) =>
            u.pathname.endsWith(`/${alpha.id}/editions`) &&
            u.searchParams.get("page") === "2",
        ).length,
    )
    .toBeGreaterThan(0);
  const readToken = (suffix) =>
    reads
      .filter(
        (u) =>
          u.pathname.endsWith(`/${alpha.id}/${suffix}`) &&
          u.searchParams.get("page") === "2",
      )
      .at(-1)
      ?.searchParams.get("snapshot");
  await expect.poll(() => readToken("relations")).toBeTruthy();
  expect(readToken("editions")).toBeTruthy();
  expect(readToken("editions")).not.toBe(readToken("relations"));
  await page.goto(`${target}&book=${foreign[0].id}`);
  await expect(
    page.getByText("此漫画不属于当前书库", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("tab", { name: "元数据", exact: true }),
  ).toHaveCount(0);
  expect(new URL(page.url()).searchParams.get("library_id")).toBe(lib.id);

  await page.goto(target);
  await expect(
    details.getByRole("heading", { name: "草稿保留", exact: true }),
  ).toBeVisible();
  await details.getByRole("button", { name: "恢复继承", exact: true }).click();
  await expect(save).toBeEnabled();
  await save.click();
  await expect(
    details.getByRole("heading", { name: "alpha", exact: true }),
  ).toBeVisible();
  const original = directory.items.find((t) => t.placeholder);
  expect(original).toBeTruthy();
  await page.goto(
    `/admin/tags?library_id=${lib.id}&tag=${original.id}&relations=edit`,
  );
  await expect(
    details.getByRole("button", { name: "添加关联", exact: true }),
  ).toBeDisabled();
  const gamma = directory.items.find((t) => t.name === "gamma");
  await page.goto(
    `/admin/tags?library_id=${lib.id}&tag=${gamma.id}&relations=edit`,
  );
  await expect(
    details.getByText("本版本唯一剩余匹配", { exact: true }).first(),
  ).toBeVisible();
  // A late response from A must never populate B after switching scopes.
  let release, started;
  const held = new Promise((r) => (release = r)),
    ready = new Promise((r) => (started = r));
  await page.route("**/api/v1/tag-catalog?**", async (route) => {
    const u = new URL(route.request().url());
    if (
      u.searchParams.get("library_id") === lib.id &&
      u.searchParams.get("q") === "author"
    ) {
      const response = await route.fetch();
      started();
      await held;
      await route.fulfill({ response }).catch(() => {});
    } else await route.fallback();
  });
  await page.goto(`/admin/tags?library_id=${lib.id}&category=artist&q=author`);
  await ready;
  await page.getByRole("combobox", { name: "当前书库", exact: true }).click();
  await page.getByRole("option", { name: "B 独立标签", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`library_id=${otherLib.id}`));
  release();
  await expect(
    catalog.getByText("此范围暂无标签", { exact: true }),
  ).toBeVisible();
  await expect(catalog.getByRole("link", { name: /author/ })).toHaveCount(0);
  const currentLibrary = (await get("/libraries")).items.find(
    (l) => l.id === lib.id,
  );
  await write(
    `/libraries/${lib.id}/status`,
    { expected_revision: currentLibrary.revision, disabled: true },
    "PATCH",
  );
  await page.goto(
    `${target}&relations=edit&r_q=hero&related_tag=${hero.id}&partition=evidence`,
  );
  await expect(details.getByText("已停用", { exact: true })).toBeVisible();
  await expect(
    details
      .getByRole("button", { name: "仅此版本取消自动匹配", exact: true })
      .first(),
  ).toBeDisabled();
  await expect(
    details
      .getByRole("button", { name: "将此角色关联到该原作", exact: true })
      .first(),
  ).toBeEnabled();
  await details.locator(".tag-detail-heading").scrollIntoViewIfNeeded();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth))
    .toBe(page.viewportSize().width);
  await page.screenshot({
    path: path.join(shots, `${info.project.name}-disabled.png`),
    fullPage: false,
    animations: "disabled",
  });
  expect(errors).toEqual([]);
  expect(
    reads
      .filter(
        (u) =>
          u.pathname.startsWith("/api/v1/tag-catalog") &&
          !u.pathname.includes("/images"),
      )
      .every((u) => !!u.searchParams.get("library_id")),
  ).toBe(true);
});
