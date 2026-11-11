import { enableAnonymousRecommendations } from "./enhancement-fixture.mjs";
import { test, expect } from "@playwright/test";
import { readFile, copyFile, mkdir } from "node:fs/promises";
import { DatabaseSync } from "node:sqlite";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);

test("导入审核：60项跨页选择、人工原子编辑、按需封面与三关门禁", async ({
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
  for (let i = 0; i < 57; i++)
    await copyFile(
      path.join(fixture.run_directory, "library", "合成1.cbz"),
      path.join(
        fixture.run_directory,
        "library",
        `${String(i).padStart(2, "0")} [匿名社团] 匿名来源标题 星の旅行 [日本語].cbz`,
      ),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous review workspace password 2026",
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
    { root_key: "anonymous", display_name: "匿名60项审核" },
    201,
  );
  const batch = await post(
    "/import-batches",
    {
      library_id: lib.id,
      request_key: `anonymous-review-${info.project.name}`,
      allow_metadata_network: true,
    },
    202,
  );
  const base = `/import-batches/${batch.id}`;
  async function waitStage(stage) {
    await expect
      .poll(
        async () => {
          const b = await get(base);
          return `${b.stage}:${b.state}`;
        },
        { timeout: 60000 },
      )
      .toBe(`${stage}:review`);
  }
  await waitStage("metadata");
  let all = (await get(`${base}/members?page_size=100`)).items;
  expect(all).toHaveLength(60);
  const good = all.filter((v) => !v.scan_error),
    noMatch = good[0],
    protectedItem = good[1],
    failedFetch = good[2],
    broken = all.find((v) => v.scan_error);
  expect(good).toHaveLength(59);
  expect(broken).toBeTruthy();
  for (let i = 1; i < good.length; i++)
    await post(
      `${base}/members/${good[i].id}/refetch`,
      {
        expected_revision: good[i].revision,
        gallery_url: `https://e-hentai.org/g/${1000 + i}/0123456789/`,
      },
      202,
    );
  await waitStage("metadata");
  // Only the isolated anonymous database is used to seed a no-match response.
  // All adoption, editing, bulk review and gate changes below use formal APIs.
  const db = new DatabaseSync(
    path.join(fixture.run_directory, "data", "app.sqlite"),
  );
  db.exec("PRAGMA busy_timeout=5000");
  db.prepare(
    "UPDATE import_members SET fetch_error='no_match',revision=revision+1 WHERE batch_id=? AND edition_id=?",
  ).run(batch.id, noMatch.id);
  db.prepare("UPDATE import_batches SET revision=revision+1 WHERE id=?").run(
    batch.id,
  );
  db.close();
  const book = await get(`/books/${protectedItem.id}`);
  const changed = await page.request.patch(
    `${baseURL}/api/v1/books/${book.id}/overrides`,
    {
      headers,
      data: {
        expected_revision: book.revision,
        fields: { title: { op: "set", value: "人工保留标题" } },
      },
    },
  );
  expect(changed.status()).toBe(200);
  const dictionary = await post("/tag-translation-jobs", {}, 202);
  await expect
    .poll(async () => (await get(`/jobs/${dictionary.job_id}`)).state, {
      timeout: 10000,
    })
    .toBe("completed");
  await page.addInitScript(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        readText: () =>
          window.__clipboardURL
            ? Promise.resolve(window.__clipboardURL)
            : Promise.reject(Error("anonymous denied")),
        writeText: (text) => {
          window.__copied = text;
          return Promise.resolve();
        },
      },
    }),
  );
  const errors = [],
    external = [],
    covers = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("https://**", (route) => {
    external.push(route.request().url());
    return route.abort();
  });
  page.on("request", (r) => {
    if (r.url().includes("/candidate-cover?")) covers.push(r.url());
  });
  await page.goto(`/admin/imports?batch=${batch.id}`);
  const frame = page.locator(".import-review-frame"),
    cards = frame.locator(".import-review-row");
  await expect(cards).toHaveCount(25);
  await expect(
    frame.locator(".import-review-footer > div:first-child"),
  ).toHaveText(/已选 58 项.*待处理 60 项/);
  await expect(page.getByRole("combobox", { name: "导入书库" })).toHaveCount(0);
  await expect(
    frame.getByRole("button", { name: "开始版本审核", exact: true }),
  ).toHaveCount(0);
  expect(covers).toHaveLength(0);
  const shotdir = path.join(
    fixture.run_directory,
    `review-${info.project.name}`,
  );
  await mkdir(shotdir, { recursive: true });
  for (const width of info.project.name === "desktop" ? [1280, 1440] : [390]) {
    await page.setViewportSize({
      width,
      height: info.project.name === "desktop" ? 900 : 844,
    });
    for (const dark of [false, true]) {
      const toggle = page.getByRole("switch", { name: "深色主题" });
      if ((await toggle.getAttribute("aria-checked")) !== String(dark))
        await toggle.click();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      const footer = await frame.locator(".import-review-footer").boundingBox();
      expect(footer.y + footer.height).toBeLessThanOrEqual(
        page.viewportSize().height + 1,
      );
      if (width === 1440) {
        const scroll = await frame
          .locator(".import-review-scroll")
          .boundingBox();
        const fourth = await cards.nth(2).boundingBox();
        expect(fourth.y + fourth.height).toBeLessThanOrEqual(
          scroll.y + scroll.height + 1,
        );
      }
      await page.evaluate(() => {
        for (const animation of document.getAnimations())
          if (animation.effect?.getTiming().iterations !== Infinity) {
            try {
              animation.finish();
            } catch {
              /* Ignore already removed animations. */
            }
          }
      });
      await page.screenshot({
        animations: "disabled",
        path: path.join(shotdir, `${width}-${dark ? "dark" : "light"}.png`),
      });
    }
  }
  const first = page.getByTestId(`import-member-${protectedItem.id}`);
  await first
    .getByRole("button", {
      name: `复制文件名 ${protectedItem.filename}`,
      exact: true,
    })
    .click();
  expect(await page.evaluate(() => window.__copied)).toBe(
    protectedItem.filename,
  );
  expect(protectedItem.filename).toMatch(/\.cbz$/i);
  const toggleDetails = first.getByRole("button", {
    name: "查看资料",
    exact: true,
  });
  await toggleDetails.scrollIntoViewIfNeeded();
  const beforeToggle = await toggleDetails.boundingBox();
  const checkboxBox = await first.getByRole("checkbox").boundingBox();
  expect(
    Math.abs(
      beforeToggle.x +
        beforeToggle.width / 2 -
        checkboxBox.x -
        checkboxBox.width / 2,
    ),
  ).toBeLessThan(1);
  expect(
    await first.evaluate((el) => getComputedStyle(el).backgroundColor),
  ).toBe(
    await frame
      .locator(".import-review-footer")
      .evaluate((el) => getComputedStyle(el).backgroundColor),
  );
  await expect(first.locator("mark").first()).toHaveCSS("box-shadow", "none");
  await first.locator(".import-title-copy").first().hover();
  await expect(first.locator(".import-title-copy").first()).toHaveCSS(
    "text-decoration-line",
    "none",
  );
  await toggleDetails.click();
  await expect(first.getByText("匿名作者", { exact: true })).toBeVisible();
  let afterToggle = await toggleDetails.boundingBox();
  expect(Math.abs(afterToggle.y - beforeToggle.y)).toBeLessThan(2);
  await expect(toggleDetails).toBeFocused();
  await toggleDetails.click();
  await expect(first.getByRole("region", { name: "候选完整资料" })).toHaveCount(
    0,
  );
  afterToggle = await toggleDetails.boundingBox();
  expect(Math.abs(afterToggle.y - beforeToggle.y)).toBeLessThan(2);
  const pasteHint = first.getByRole("button", {
    name: "粘贴网址重新匹配",
    exact: true,
  });
  await pasteHint.focus();
  await expect(
    page.getByRole("tooltip", { name: "粘贴网址并重新匹配", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Tab");
  const sourceLink = first.getByRole("link", {
    name: "打开当前候选画廊",
    exact: true,
  });
  await expect(sourceLink).toHaveAttribute("target", "_blank");
  await expect(sourceLink).toHaveAttribute(
    "href",
    /^https:\/\/e-hentai.org\/g\//,
  );
  const compare = first.getByRole("button", {
    name: `查看封面对比 ${protectedItem.filename}`,
    exact: true,
  });
  await compare.focus();
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).toBeVisible();
  expect(covers).toHaveLength(1);
  await page.keyboard.press("Tab");
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).not.toBeVisible();
  await compare.focus();
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).toBeVisible();
  expect(covers).toHaveLength(1);
  await page.screenshot({
    path: path.join(shotdir, "cover-compare.png"),
    animations: "disabled",
  });
  await page.keyboard.press("Escape");
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).not.toBeVisible();
  let imageRequests = 0;
  await page.route(
    `**/members/${failedFetch.id}/candidate-cover?*`,
    (route) => {
      imageRequests++;
      if (imageRequests === 1)
        return route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "thumbnail_unavailable",
              message: "anonymous failure",
            },
          }),
        });
      return route.continue();
    },
  );
  const failureCover = page
    .getByTestId(`import-member-${failedFetch.id}`)
    .getByRole("button", {
      name: `查看封面对比 ${failedFetch.filename}`,
      exact: true,
    });
  await failureCover.focus();
  await expect(
    page.getByText("远端封面暂不可用，仍可审核文字资料。", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Tab");
  await failureCover.focus();
  await expect(
    page.getByText("远端封面暂不可用，仍可审核文字资料。", { exact: true }),
  ).toBeVisible();
  expect(imageRequests).toBe(1);
  await page.getByRole("button", { name: "重试封面", exact: true }).click();
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).toBeVisible();
  expect(imageRequests).toBe(2);
  await page.keyboard.press("Escape");
  let release, started;
  const gate = new Promise((r) => (release = r)),
    begin = new Promise((r) => (started = r));
  let held = true;
  await page.route(
    `**/members/${good[3].id}/candidate-cover?*`,
    async (route) => {
      if (!held) return route.continue();
      held = false;
      const response = await route.fetch();
      started();
      await gate;
      try {
        await route.fulfill({ response });
      } catch {
        /* The abandoned browser request was canceled. */
      }
    },
  );
  const lateCover = page
    .getByTestId(`import-member-${good[3].id}`)
    .getByRole("button", {
      name: `查看封面对比 ${good[3].filename}`,
      exact: true,
    });
  await lateCover.focus();
  await begin;
  await page.keyboard.press("Escape");
  release();
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).not.toBeVisible();
  await page.keyboard.press("Tab");
  await lateCover.focus();
  await expect(
    page.getByRole("img", { name: "远端候选封面", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await first.getByRole("checkbox").uncheck();
  await frame
    .getByRole("button", { name: "刷新当前状态", exact: true })
    .click();
  await expect(
    frame.locator(".import-review-footer > div:first-child"),
  ).toHaveText(/已选 57 项.*待处理 60 项/);
  await expect(first.getByRole("checkbox")).not.toBeChecked();
  await frame
    .locator(".import-review-pagination .ant-pagination-item-2")
    .click();
  await expect(cards).toHaveCount(25);
  await expect(cards.first().getByRole("checkbox")).toBeChecked();
  await frame.getByRole("button", { name: "本页反选", exact: true }).click();
  await expect(
    frame.locator(".import-review-footer > div:first-child"),
  ).toHaveText(/已选 32 项.*待处理 60 项/);
  await frame.getByRole("button", { name: "本页全选", exact: true }).click();
  await expect(
    frame.locator(".import-review-footer > div:first-child"),
  ).toHaveText(/已选 57 项.*待处理 60 项/);
  async function selectMenu(name) {
    await frame.getByRole("button", { name: "整批选择", exact: true }).click();
    await page.getByRole("menuitem", { name, exact: true }).click();
  }
  await selectMenu("取消选择");
  await frame
    .getByRole("button", { name: "刷新当前状态", exact: true })
    .click();
  await expect(
    frame.locator(".import-review-footer > div:first-child"),
  ).toHaveText(/已选 0 项.*待处理 60 项/);
  await selectMenu("选择整批可采纳项");
  await expect(
    frame.locator(".import-review-footer > div:first-child"),
  ).toHaveText(/已选 58 项.*待处理 60 项/);
  await frame
    .locator(".import-review-pagination .ant-pagination-item-1")
    .click();
  const unmatched = page.getByTestId(`import-member-${noMatch.id}`);
  await expect(
    unmatched.getByText("未找到匹配", { exact: true }),
  ).toBeVisible();
  await unmatched
    .getByRole("button", { name: "粘贴网址重新匹配", exact: true })
    .click();
  await expect(
    frame.getByText("无法读取剪贴板，请在展开的输入框中填写画廊网址。", {
      exact: true,
    }),
  ).toBeVisible();
  const input = unmatched.getByRole("textbox", {
    name: "手工填写画廊网址",
    exact: true,
  });
  await input.fill("https://e-hentai.org/g/55/0123456789/");
  await page.getByRole("link", { name: "导入历史", exact: true }).click();
  const guard = page.getByRole("dialog", {
    name: "有尚未保存的修改",
    exact: true,
  });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
  page.once("dialog", (d) => d.accept());
  await page.reload();
  await expect(input).toHaveValue("https://e-hentai.org/g/55/0123456789/");
  await unmatched.getByRole("button", { name: "取消", exact: true }).click();
  await unmatched
    .getByRole("button", { name: "人工录入", exact: true })
    .click();
  let drawer = page.getByRole("dialog", {
    name: "人工录入元数据",
    exact: true,
  });
  await expect(drawer).toBeVisible();
  await drawer
    .getByRole("textbox", { name: "其他标题", exact: true })
    .fill("手工录入匿名元数据");
  await drawer.getByRole("button", { name: "保存并确认", exact: true }).click();
  await expect(drawer).not.toBeVisible();
  expect((await get(`/books/${noMatch.id}`)).metadata.source).toBeNull();
  await first
    .getByRole("button", { name: "编辑候选资料", exact: true })
    .click();
  drawer = page.getByRole("dialog", {
    name: "核对并修改候选资料",
    exact: true,
  });
  await expect(
    drawer.getByRole("textbox", { name: "其他标题", exact: true }),
  ).toHaveValue("人工保留标题");
  await drawer
    .getByRole("textbox", { name: "描述", exact: true })
    .fill("审核抽屉中的人工补充");
  await page.screenshot({
    path: path.join(shotdir, "metadata-editor.png"),
    animations: "disabled",
  });
  let deny = true;
  await page.route("**/manual-review", (route) => {
    if (deny) {
      deny = false;
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "revision_conflict", message: "匿名审核版本冲突" },
        }),
      });
    }
    return route.continue();
  });
  await drawer
    .getByRole("button", { name: "采纳并保存修改", exact: true })
    .click();
  await expect(
    drawer.getByText("匿名审核版本冲突", { exact: true }),
  ).toBeVisible();
  await expect(
    drawer.getByRole("textbox", { name: "描述", exact: true }),
  ).toHaveValue("审核抽屉中的人工补充");
  await expect(
    drawer.getByRole("button", { name: "采纳并保存修改", exact: true }),
  ).toBeDisabled();
  await expect(
    drawer.getByRole("button", {
      name: "核对后使用当前版本，保留草稿",
      exact: true,
    }),
  ).toBeVisible();
  await drawer
    .getByRole("button", { name: "核对后使用当前版本，保留草稿", exact: true })
    .click();
  await drawer
    .getByRole("button", { name: "采纳并保存修改", exact: true })
    .click();
  await expect(drawer).not.toBeVisible();
  const saved = await get(`/books/${protectedItem.id}`);
  expect(saved.metadata.fields.title).toBe("人工保留标题");
  expect(saved.metadata.fields.description).toBe("审核抽屉中的人工补充");
  expect(saved.metadata.source).not.toBeNull();
  expect(saved.metadata.fields.title_original).toBe("Anonymous original");
  const old = page.getByTestId(`import-member-${failedFetch.id}`),
    oldURL = (await get(`${base}/members?page_size=100`)).items.find(
      (v) => v.id === failedFetch.id,
    ).candidate.url;
  await page.evaluate(
    () => (window.__clipboardURL = "https://e-hentai.org/g/999999/0123456789/"),
  );
  await old
    .getByRole("button", { name: "粘贴网址重新匹配", exact: true })
    .click();
  await waitStage("metadata");
  await frame
    .getByRole("button", { name: "刷新当前状态", exact: true })
    .click();
  await expect(
    old.getByRole("button", { name: "复制当前候选网址", exact: true }),
  ).toHaveText(new URL(oldURL).pathname.replace(/^\/|\/$/g, ""));
  await expect(old.getByText(/新请求.*999999/)).toBeVisible();
  await old.getByRole("button", { name: "确认旧匹配", exact: true }).click();
  expect((await get(`/books/${failedFetch.id}`)).metadata.source.url).toBe(
    oldURL,
  );
  // The remaining candidate set is cross-page and submitted in one real request.
  await expect(
    frame.getByRole("button", { name: "确认所选匹配（56）", exact: true }),
  ).toBeEnabled();
  const reviewing = page.waitForResponse(
    (r) =>
      r.url().endsWith(`${base}/reviews`) && r.request().method() === "POST",
  );
  await frame
    .getByRole("button", { name: "确认所选匹配（56）", exact: true })
    .click();
  const bulk = await reviewing;
  expect(bulk.status()).toBe(200);
  expect(bulk.request().postDataJSON().items).toHaveLength(56);
  await expect(cards).toHaveCount(1);
  const fault = page.getByTestId(`import-member-${broken.id}`);
  await expect(fault.getByText("源文件异常", { exact: true })).toBeVisible();
  let deniedAdvance = false,
    advanceRequests = 0;
  await page.route(`**${base}/control/continue`, (route) => {
    advanceRequests++;
    if (info.project.name === "desktop" && !deniedAdvance) {
      deniedAdvance = true;
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "conflict", message: "anonymous stage conflict" },
        }),
      });
    }
    return route.continue();
  });
  await fault
    .getByRole("button", { name: "该项更多操作", exact: true })
    .click();
  await page
    .getByRole("menuitem", { name: "本次不更新元数据", exact: true })
    .click();
  await page.getByRole("button", { name: "本次不更新", exact: true }).click();
  if (info.project.name === "desktop") {
    await expect(
      page.getByText("审核已保存，进入版本审核失败", { exact: true }),
    ).toBeVisible();
    expect((await get(base)).pending).toBe(0);
    await page
      .getByRole("button", { name: "刷新当前状态", exact: true })
      .click();
    await expect(
      frame.getByRole("button", { name: "重试进入版本审核", exact: true }),
    ).toBeEnabled();
    expect(advanceRequests).toBe(1);
    await frame
      .getByRole("button", { name: "重试进入版本审核", exact: true })
      .click();
  }
  await waitStage("versions");
  await page.getByRole("button", { name: "刷新当前状态", exact: true }).click();

  const organizationFrame = page.locator(".import-organization-frame");
  await expect(
    organizationFrame.getByRole("button", {
      name: "确认当前归属",
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    organizationFrame.getByText("匹配说明与风险", { exact: true }),
  ).toHaveCount(0);
  await expect(organizationFrame.getByText(/保留版本组/)).toHaveCount(0);
  const orgShots = async (stage) => {
    for (const dark of [false, true]) {
      const switcher = page.getByRole("switch", { name: "深色主题" });
      if ((await switcher.getAttribute("aria-checked")) !== String(dark))
        await switcher.click();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      const footer = await organizationFrame
        .locator(".import-proposal-footer, .series-proposal-actions")
        .boundingBox();
      if (footer)
        expect(footer.y + footer.height).toBeLessThanOrEqual(
          page.viewportSize().height + 1,
        );
      await page.screenshot({
        path: path.join(shotdir, `${stage}-${dark ? "dark" : "light"}.png`),
        animations: "disabled",
      });
    }
  };
  await expect(
    organizationFrame.getByRole("region", { name: "版本方案" }),
  ).toBeVisible();
  await orgShots("versions");
  // A direct entry must load all member styles without visiting global managers.
  expect(
    await organizationFrame
      .locator(".version-member-title")
      .first()
      .evaluate((el) => getComputedStyle(el).borderTopStyle),
  ).toBe("none");
  let workPlans = (
    await get(`${base}/work-proposals?alternatives=false&page_size=25`)
  ).items;
  if (workPlans.length) {
    const p = workPlans[0];
    const excluded = p.members
      .slice(0, 25)
      .find((m) => m.role === "new" && m.work_id !== p.target_work_id);
    if (excluded) {
      const check = page
        .getByTestId(`review-version-${excluded.id}`)
        .getByRole("checkbox");
      await check.uncheck();
      await page
        .getByRole("button", { name: "刷新当前状态", exact: true })
        .click();
      await expect(check).not.toBeChecked();
    }
    await organizationFrame
      .getByRole("button", { name: "打开漫画详情", exact: true })
      .first()
      .click();
    const bookDialog = page
      .getByRole("dialog")
      .filter({ has: page.getByText(/漫画详情/) });
    await expect(bookDialog).toBeVisible();
    await bookDialog.getByRole("button", { name: "关闭", exact: true }).click();
    let reject = true;
    await page.route(`**${base}/work-proposals/*/accept`, (route) => {
      if (reject) {
        reject = false;
        return route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "revision_conflict",
              message: "anonymous work conflict",
            },
          }),
        });
      }
      return route.continue();
    });
    const acceptWork = organizationFrame.getByRole("button", {
      name: p.kind === "create_work" ? "组成版本组" : "加入版本组",
      exact: true,
    });
    await acceptWork.click();
    await expect(
      organizationFrame.getByText("anonymous work conflict", { exact: true }),
    ).toBeVisible();
    if (excluded)
      await expect(
        page.getByTestId(`review-version-${excluded.id}`).getByRole("checkbox"),
      ).not.toBeChecked();
    await expect(acceptWork).toBeDisabled();
    await organizationFrame
      .getByRole("button", { name: "核对当前建议", exact: true })
      .click();
    if (excluded)
      await page
        .getByRole("button", { name: "使用当前建议", exact: true })
        .click();
    await acceptWork.click();
  }
  // Explicitly reject any newly generated remaining plans, never per-member reviews.
  for (let i = 0; i < 100; i++) {
    const status = await get(base);
    if (status.stage !== "versions") break;
    const rejectButton = organizationFrame.getByRole("button", {
      name: "不采用",
      exact: true,
    });
    if ((await rejectButton.isVisible()) && (await rejectButton.isEnabled()))
      await rejectButton.click();
    else {
      const go = organizationFrame.getByRole("button", {
        name: "进入系列审核",
        exact: true,
      });
      if ((await go.isVisible()) && (await go.isEnabled())) await go.click();
    }
    await page.waitForTimeout(100);
  }
  await waitStage("series");
  await page.getByRole("button", { name: "刷新当前状态", exact: true }).click();
  const seriesEditor = organizationFrame.getByLabel("系列方案", {
    exact: true,
  });
  await expect(seriesEditor).toBeVisible();
  await seriesEditor
    .getByRole("textbox", { name: "系列名称", exact: true })
    .fill("匿名批次系列");
  await page.getByRole("button", { name: "刷新当前状态", exact: true }).click();
  await expect(
    seriesEditor.getByRole("textbox", { name: "系列名称", exact: true }),
  ).toHaveValue("匿名批次系列");
  const handles = seriesEditor.getByRole("button", { name: /^拖动 / });
  if ((await handles.count()) > 1) {
    const originalOrder = await seriesEditor
      .locator(".series-proposal-member")
      .evaluateAll((rows) => rows.map((row) => row.dataset.seriesDropRow));
    await handles.first().focus();
    await page.keyboard.press("Space");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await expect
      .poll(() =>
        seriesEditor
          .locator(".series-proposal-member")
          .evaluateAll((rows) => rows.map((row) => row.dataset.seriesDropRow)),
      )
      .not.toEqual(originalOrder);
  }
  await orgShots("series");
  let seriesConflict = true;
  await page.route(`**${base}/series-proposals/*/accept`, (route) => {
    if (seriesConflict) {
      seriesConflict = false;
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "revision_conflict",
            message: "anonymous series conflict",
          },
        }),
      });
    }
    return route.continue();
  });
  await seriesEditor
    .getByRole("button", { name: "组成系列", exact: true })
    .click();
  await expect(
    seriesEditor.getByText("anonymous series conflict", { exact: true }),
  ).toBeVisible();
  await expect(
    seriesEditor.getByRole("button", { name: "组成系列", exact: true }),
  ).toBeDisabled();
  await expect(
    seriesEditor.getByRole("textbox", { name: "系列名称", exact: true }),
  ).toHaveValue("匿名批次系列");
  await seriesEditor
    .getByRole("button", { name: "加载当前方案", exact: true })
    .click();
  await page
    .getByRole("button", { name: "保留修改并重新核对", exact: true })
    .click();
  await expect(
    seriesEditor.getByRole("textbox", { name: "系列名称", exact: true }),
  ).toHaveValue("匿名批次系列");
  await seriesEditor
    .getByRole("button", { name: "组成系列", exact: true })
    .click();
  for (let i = 0; i < 100; i++) {
    const status = await get(base);
    if (status.stage === "finished") break;
    const rejectButton = organizationFrame.getByRole("button", {
      name: "不采用",
      exact: true,
    });
    if ((await rejectButton.isVisible()) && (await rejectButton.isEnabled()))
      await rejectButton.click();
    else {
      const finish = organizationFrame.getByRole("button", {
        name: "完成导入",
        exact: true,
      });
      if ((await finish.isVisible()) && (await finish.isEnabled()))
        await finish.click();
    }
    await page.waitForTimeout(100);
  }
  await expect(page.getByText("导入已完成", { exact: true })).toBeVisible();
  expect((await get(base)).state).toBe("completed");
  expect(
    (await get(`/editions?library_id=${lib.id}&page_size=100`)).total,
  ).toBe(60);
  await organizationFrame
    .getByRole("button", { name: "查看整理记录", exact: true })
    .click();
  const records = page.getByRole("dialog", { name: "处理记录", exact: true });
  await expect(records).toBeVisible();
  await expect(
    records.getByRole("button", { name: "撤销整理", exact: true }).first(),
  ).toBeEnabled();
  await records
    .getByRole("button", { name: "撤销整理", exact: true })
    .first()
    .click();
  await records.getByText("已撤销", { exact: true }).click();
  await expect(records.locator(".import-history-row")).not.toHaveCount(0);
  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
