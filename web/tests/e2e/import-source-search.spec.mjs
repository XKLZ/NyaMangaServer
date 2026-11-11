import { enableAnonymousRecommendations } from "./enhancement-fixture.mjs";
import { test, expect } from "@playwright/test";
import { readFile, mkdir, copyFile } from "node:fs/promises";
import { DatabaseSync } from "node:sqlite";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);
test("扫描自动切换、候选比较与标题搜索仅暂存，原审核关卡保留", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(150000);
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
  const longName =
    "[Anonymous circle] 匿名来源标题 星の旅行 " +
    "Long title ".repeat(8) +
    ".cbz";
  await copyFile(
    path.join(fixture.run_directory, "library", "合成1.cbz"),
    path.join(fixture.run_directory, "library", longName),
  );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous source comparison 2026",
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
    { root_key: "anonymous", display_name: "匿名候选比较" },
    201,
  );
  const batch = await post(
      "/import-batches",
      {
        library_id: lib.id,
        request_key: "source-comparison",
        allow_metadata_network: true,
      },
      202,
    ),
    base = `/import-batches/${batch.id}`;
  const errors = [],
    writes = [],
    images = [],
    details = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    if (r.method() === "GET" && /\/results\/[^/?]+$/.test(r.url()))
      details.push(r.url());
    if (r.method() === "POST")
      writes.push({ url: r.url(), body: r.postDataJSON() });
    if (/\/results\/[^/]+\/cover\?/.test(r.url())) images.push(r.url());
  });
  await page.addInitScript(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: (value) => {
          window.__copied = value;
          return Promise.resolve();
        },
        readText: () => Promise.reject(Error("anonymous denied")),
      },
    }),
  );
  await page.addInitScript(() => {
    window.__sourceBlobs = { created: [], revoked: [] };
    const create = URL.createObjectURL.bind(URL),
      revoke = URL.revokeObjectURL.bind(URL);
    URL.createObjectURL = (blob) => {
      const url = create(blob);
      window.__sourceBlobs.created.push(url);
      return url;
    };
    URL.revokeObjectURL = (url) => {
      window.__sourceBlobs.revoked.push(url);
      return revoke(url);
    };
  });
  // Read the scan state twice even if the very small fixture has already finished;
  // the page must move on using its own parent polling/SSE, without a manual click.
  let scanReads = 0;
  await page.route(`**/api/v1${base}`, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    if (scanReads++ < 2) {
      body.stage = "scan";
      body.state = "processing";
    }
    return route.fulfill({ response, json: body });
  });
  await page.goto(`/admin/imports?batch=${batch.id}`);
  await expect(page.locator(".import-review-row")).toHaveCount(4, {
    timeout: 20000,
  });
  const members = (await get(`${base}/members?page_size=100`)).items;
  const item = members.find(
    (v) => v.filename === longName && v.status === "multiple_matches",
  );
  expect(item).toBeTruthy();
  const row = page.getByTestId(`import-member-${item.id}`);
  await expect(
    row.getByText("发现2个候选·请选择", { exact: true }),
  ).toBeVisible();
  await expect(row.getByRole("checkbox")).not.toBeChecked();
  const dictionary = await post("/tag-translation-jobs", {}, 202);
  await expect
    .poll(async () => (await get(`/jobs/${dictionary.job_id}`)).state)
    .toBe("completed");
  const before = (await get(`/books/${item.id}`)).metadata;
  const siteTitle =
    "[ANON (Anonymous circle)] Long title: Mujintou JK! 星の旅行 Volume. 1 ~Long title School Village~ [Chinese] [Uncensored]";
  const originalTitle =
    "[ANON（匿名社團）] 星の旅行！Long title ～無人島と学校村～ 第１話 [中國翻訳] [無修正]";
  await page.route("**/source-searches/*/results?*", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.items = body.items.map((r) =>
      r.state !== "ready"
        ? r
        : {
            ...r,
            fields: {
              ...r.fields,
              title: siteTitle,
              title_original: originalTitle,
            },
            tags: [
              ...r.tags,
              ...Array.from({ length: 12 }, (_, i) => ({
                namespace: "other",
                name: `anonymous tag ${i}`,
                translation: `匿名标签 ${i}`,
              })),
            ],
          },
    );
    return route.fulfill({ response, json: body });
  });
  let failCover = true;
  let brokenCoverPath;
  await page.route("**/source-searches/*/results/*/cover?*", (route) => {
    brokenCoverPath ??= route.request().url();
    if (failCover && route.request().url() === brokenCoverPath) {
      return route.fulfill({
        status: 503,
        json: { error: { code: "network_error", message: "匿名封面失败" } },
      });
    }
    return route.continue();
  });
  await row.getByRole("button", { name: "比较候选", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "来源比较", exact: true });
  await expect(drawer).toBeVisible();
  const candidates = drawer.locator(".source-result");
  const scrollArea = () =>
    drawer.locator(
      page.viewportSize().width > 760
        ? ".source-candidate-scroll"
        : ".source-workspace",
    );
  await expect(candidates).toHaveCount(2);
  const unresolved = drawer.locator('.source-result:not([data-state="ready"])');
  await expect(unresolved).toHaveCount(0);
  await expect(
    drawer.getByRole("button", { name: "使用此候选", exact: true }),
  ).toHaveCount(2);
  await expect(
    candidates.nth(0).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeEnabled();
  await expect(
    candidates.nth(1).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeEnabled();
  await expect(
    drawer.getByRole("button", { name: "重试封面", exact: true }),
  ).toHaveCount(1);
  failCover = false;
  await drawer.getByRole("button", { name: "重试封面", exact: true }).click();
  await expect(
    drawer.getByRole("img", { name: "候选封面", exact: true }),
  ).toHaveCount(2);
  await expect
    .poll(() =>
      drawer
        .getByRole("img", { name: "候选封面", exact: true })
        .evaluateAll((items) =>
          items.every((img) => img.complete && img.naturalWidth > 0),
        ),
    )
    .toBe(true);
  expect(images.length).toBeGreaterThanOrEqual(3);
  const loadedImages = images.length;
  await expect(drawer.getByRole("button", { name: /对比封面/ })).toHaveCount(0);
  await expect(drawer.locator(".source-local-facts")).not.toContainText("语言");
  expect(
    await candidates.first().locator(".source-primary dt").allTextContents(),
  ).toEqual(["站点标题", "原文标题", "页数", "发布时间"]);
  await expect(
    candidates
      .first()
      .getByRole("region", { name: "更多候选资料", exact: true }),
  ).toHaveCount(0);
  expect(details).toHaveLength(0);
  const localArea = drawer.getByRole("region", {
    name: "本地对照",
    exact: true,
  });
  async function assertReferencePosition() {
    // CSS media queries can change before React switches its scroll owner and
    // restores the saved position. Wait for the visible keyboard behavior.
    if (page.viewportSize().width > 760)
      await expect(localArea).toHaveAttribute("tabindex", "0");
    else await expect(localArea).not.toHaveAttribute("tabindex");
    await scrollArea().evaluate((el) => {
      el.scrollTop = 0;
    });
    const before = await localArea.boundingBox();
    await scrollArea().evaluate((el) => {
      el.scrollTop = 180;
    });
    await expect
      .poll(() => scrollArea().evaluate((el) => el.scrollTop))
      .toBeGreaterThan(100);
    const after = await localArea.boundingBox();
    if (page.viewportSize().width > 760) {
      expect(Math.abs(after.y - before.y)).toBeLessThan(1);
      const viewport = await scrollArea().boundingBox();
      expect(viewport.y).toBeGreaterThanOrEqual(after.y + after.height - 1);
      expect(after.height).toBeLessThanOrEqual(
        (await drawer.locator(".source-workspace").boundingBox()).height * 0.4 +
          1,
      );
    } else {
      expect(after.y).toBeLessThan(before.y - 100);
      expect(
        await drawer
          .locator(".source-candidate-scroll")
          .evaluate((el) => getComputedStyle(el).display),
      ).toBe("contents");
    }
    await scrollArea().evaluate((el) => {
      el.scrollTop = 0;
    });
  }
  await assertReferencePosition();
  await expect(
    candidates
      .first()
      .getByRole("button", { name: "复制完整站点标题", exact: true }),
  ).toHaveText(siteTitle);
  await expect(
    candidates
      .nth(1)
      .getByRole("button", { name: "复制完整原文标题", exact: true }),
  ).toHaveText(originalTitle);
  await candidates
    .first()
    .getByRole("button", { name: "复制完整站点标题", exact: true })
    .click();
  expect(await page.evaluate(() => window.__copied)).toBe(siteTitle);
  await candidates
    .first()
    .getByRole("button", { name: "复制完整原文标题", exact: true })
    .click();
  expect(await page.evaluate(() => window.__copied)).toBe(originalTitle);
  await expect(
    candidates.first().locator(".source-tag-group .ant-tag"),
  ).toHaveCount(0);
  const expand = candidates.first().getByRole("button", { name: /^更多资料/ });
  await expand.scrollIntoViewIfNeeded();
  const localBeforeExpand = await localArea.boundingBox();
  const toggleBeforeExpand = await expand.boundingBox();
  await expand.click();
  if (page.viewportSize().width > 760)
    expect(
      Math.abs((await localArea.boundingBox()).y - localBeforeExpand.y),
    ).toBeLessThan(1);
  expect(
    Math.abs(
      (
        await candidates
          .first()
          .getByRole("button", { name: "收起资料", exact: true })
          .boundingBox()
      ).y - toggleBeforeExpand.y,
    ),
  ).toBeLessThan(2);
  await expect(
    candidates.first().locator(".source-tag-group .ant-tag"),
  ).toHaveCount(14);
  await expect(
    candidates.first().getByRole("button", { name: "收起资料", exact: true }),
  ).toHaveAttribute("aria-expanded", "true");
  await expect(
    candidates.nth(1).locator(".source-tag-group .ant-tag"),
  ).toHaveCount(0);
  await expect(
    candidates.first().getByText("匿名作者", { exact: false }),
  ).toBeVisible();
  expect(images).toHaveLength(loadedImages);
  await candidates
    .first()
    .getByRole("button", { name: "复制完整画廊网址", exact: true })
    .click();
  expect(await page.evaluate(() => window.__copied)).toMatch(
    /^https:\/\/e-hentai.org\/g\//,
  );
  await expect(
    candidates.first().getByRole("link", { name: "打开画廊", exact: true }),
  ).toHaveAttribute("target", "_blank");
  const shotdir = path.join(
    fixture.run_directory,
    `source-search-${info.project.name}`,
  );
  await mkdir(shotdir, { recursive: true });
  async function screenshots(name, position = "top") {
    for (const width of info.project.name === "desktop"
      ? [1280, 1440]
      : [390]) {
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
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        ).toBe(true);
        expect(
          await scrollArea().evaluate((el) => el.scrollWidth <= el.clientWidth),
        ).toBe(true);
        const bounds = await drawer.boundingBox();
        if (width > 760) {
          expect(Math.abs(bounds.x - (width - bounds.width) / 2)).toBeLessThan(
            2,
          );
          expect(bounds.height).toBeCloseTo(820, 0);
          expect(
            await drawer
              .locator(".source-local-text")
              .evaluate((el) => el.clientWidth),
          ).toBeGreaterThan(700);
        } else {
          expect(bounds.height).toBeCloseTo(844, 0);
        }
        const texts = drawer.locator(".source-title-copy .source-text");
        for (const text of await texts.all()) {
          const flow = await text.evaluate((el) => {
            const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
            let node,
              lastTop = -Infinity,
              lastLeft = -Infinity;
            while ((node = walker.nextNode())) {
              for (let i = 0; i < node.textContent.length; i++) {
                if (/\s/.test(node.textContent[i])) continue;
                const range = document.createRange();
                range.setStart(node, i);
                range.setEnd(node, i + 1);
                const rect = range.getBoundingClientRect();
                if (
                  rect.top < lastTop - 3 ||
                  (Math.abs(rect.top - lastTop) < 3 && rect.left < lastLeft - 1)
                )
                  return false;
                lastTop = rect.top;
                lastLeft = rect.left;
              }
            }
            return (
              getComputedStyle(el).display === "block" &&
              [...el.querySelectorAll("mark")].every(
                (mark) =>
                  getComputedStyle(mark).display === "inline" &&
                  getComputedStyle(mark).textDecorationLine === "none",
              )
            );
          });
          expect(
            flow,
            "highlighted and plain fragments must share ordinary line flow",
          ).toBe(true);
        }
        await scrollArea().evaluate((el, target) => {
          const node =
            target === "candidate" ? el.querySelector(".source-result") : null;
          if (node)
            el.scrollTop +=
              node.getBoundingClientRect().top - el.getBoundingClientRect().top;
          else el.scrollTop = 0;
        }, position);
        const footer = await drawer.locator(".source-footer").boundingBox();
        expect(footer.y + footer.height).toBeLessThanOrEqual(
          page.viewportSize().height + 1,
        );
        await expect(page.locator(".ant-message-notice")).toHaveCount(0, {
          timeout: 7000,
        });
        await page.screenshot({
          path: path.join(
            shotdir,
            `${name}-${width}-${dark ? "dark" : "light"}.png`,
          ),
          animations: "disabled",
        });
      }
    }
  }
  await screenshots("compare");
  await candidates
    .first()
    .getByRole("button", { name: "收起资料", exact: true })
    .click();
  await screenshots("candidate", "candidate");
  if (info.project.name === "desktop") {
    const localTitle = localArea.locator(".source-titles dd").nth(1);
    const original = await localTitle.textContent();
    await localTitle.evaluate((el) => {
      el.textContent = "匿名长原文标题".repeat(120);
    });
    await page.setViewportSize({ width: 1280, height: 560 });
    await expect
      .poll(() => localArea.evaluate((el) => el.scrollHeight > el.clientHeight))
      .toBe(true);
    await localArea.focus();
    await localArea.press("PageDown");
    await expect
      .poll(() => localArea.evaluate((el) => el.scrollTop))
      .toBeGreaterThan(0);
    await assertReferencePosition();
    const localBox = await localArea.boundingBox();
    const listBox = await scrollArea().boundingBox();
    expect(listBox.height).toBeGreaterThan(150);
    expect(localBox.height).toBeLessThanOrEqual(
      (await drawer.locator(".source-workspace").boundingBox()).height * 0.4 +
        1,
    );
    await candidates
      .first()
      .getByRole("button", { name: "复制完整站点标题", exact: true })
      .focus();
    const focused = await candidates
      .first()
      .getByRole("button", { name: "复制完整站点标题", exact: true })
      .boundingBox();
    expect(focused.y).toBeGreaterThanOrEqual(listBox.y - 1);
    expect(focused.y + focused.height).toBeLessThanOrEqual(
      listBox.y + listBox.height + 1,
    );
    const footer = await drawer.locator(".source-footer").boundingBox();
    expect(footer.y + footer.height).toBeLessThanOrEqual(560);
    await page.screenshot({
      path: path.join(shotdir, "pinned-short-1280-dark.png"),
      animations: "disabled",
    });
    await localTitle.evaluate((el, text) => {
      el.textContent = text;
    }, original);
    await page.setViewportSize({ width: 1440, height: 900 });
    await localArea.evaluate((el) => {
      el.scrollTop = 0;
    });
    await page.setViewportSize({ width: 761, height: 900 });
    await assertReferencePosition();
    await page.setViewportSize({ width: 760, height: 900 });
    await assertReferencePosition();
    await page.setViewportSize({ width: 1440, height: 900 });
  }

  await candidates
    .first()
    .getByRole("button", { name: /^更多资料/ })
    .click();
  let failures = 0;
  let firstAttempt, releaseSubmit, submitStarted;
  const submitGate = new Promise((resolve) => (releaseSubmit = resolve));
  const submission = new Promise((resolve) => (submitStarted = resolve));
  await page.route("**/source-searches/*/select", async (route) => {
    if (failures++ === 0) {
      firstAttempt = route.request().postDataJSON();
      submitStarted();
      await submitGate;
      return route.fulfill({
        status: 503,
        json: { error: { code: "network_error", message: "匿名暂存失败" } },
      });
    }
    if (failures === 2) {
      expect(route.request().postDataJSON()).toEqual(firstAttempt);
      return route.fulfill({
        status: 409,
        json: {
          error: { code: "revision_conflict", message: "匿名选择版本冲突" },
        },
      });
    }
    return route.continue();
  });
  await candidates
    .nth(1)
    .getByRole("button", { name: "使用此候选", exact: true })
    .focus();
  await page.keyboard.press("Enter");
  await submission;
  await expect(
    drawer.getByRole("button", { name: "返回审核", exact: true }),
  ).toBeDisabled();
  await expect(drawer.locator(".ant-modal-close")).toHaveCount(0);
  await expect(
    candidates
      .first()
      .getByRole("button", { name: "复制完整站点标题", exact: true }),
  ).toBeDisabled();
  await expect(
    candidates.first().getByRole("button", { name: "收起资料", exact: true }),
  ).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(drawer).toBeVisible();
  releaseSubmit();
  await expect(drawer.getByText("匿名暂存失败", { exact: true })).toBeVisible();
  await expect(
    candidates.nth(1).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeEnabled();
  expect(writes.filter((v) => v.url.endsWith("/select"))).toHaveLength(1);
  await candidates
    .nth(1)
    .getByRole("button", { name: "使用此候选", exact: true })
    .click();
  await expect(
    drawer.getByText("匿名选择版本冲突", { exact: true }),
  ).toBeVisible();
  await expect(
    candidates.nth(1).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeDisabled();
  // Closing and reopening cannot silently replace the failed submission snapshot.
  await drawer.getByRole("button", { name: "返回审核", exact: true }).click();
  await row.getByRole("button", { name: "比较候选", exact: true }).click();
  await expect(candidates).toHaveCount(2);
  await expect(
    candidates.first().getByRole("button", { name: "收起资料", exact: true }),
  ).toBeVisible();
  await expect(
    candidates.nth(1).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeDisabled();
  await drawer
    .getByRole("button", { name: "核对当前版本", exact: true })
    .click();
  await expect(
    candidates.nth(1).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeEnabled();
  await candidates
    .nth(1)
    .getByRole("button", { name: "使用此候选", exact: true })
    .click();
  const replay = writes.filter((v) => v.url.endsWith("/select")).at(-1).body;
  expect(replay.result_id).toBe(firstAttempt.result_id);
  expect(replay.member_revision).toBe(firstAttempt.member_revision);
  expect(replay.edition_revision).toBe(firstAttempt.edition_revision);
  await expect(drawer).not.toBeVisible();
  expect(
    await page.evaluate(() =>
      window.__sourceBlobs.created.every((url) =>
        window.__sourceBlobs.revoked.includes(url),
      ),
    ),
  ).toBe(true);
  await expect(
    row.getByRole("button", { name: "比较候选", exact: true }),
  ).toBeFocused();
  await expect(row.getByRole("checkbox")).not.toBeChecked();
  expect((await get(`/books/${item.id}`)).metadata).toEqual(before);
  const after = (await get(`${base}/members?page_size=100`)).items.find(
    (v) => v.id === item.id,
  );
  expect(after.candidate.url).toContain("/g/2/");
  expect(after.decision).toBe("");
  // Title tab does not POST on open or typing. A submit makes a separate query.
  await row
    .getByRole("button", { name: "按标题查找来源", exact: true })
    .click();
  await expect(
    drawer.getByRole("tab", { name: "按标题查找", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  const count = writes.filter((v) => v.url.endsWith("/source-searches")).length;
  const query = drawer.getByRole("textbox", { name: "搜索标题", exact: true });
  await query.fill("匿名 星の旅行");
  await expect(
    drawer.getByText("输入标题，点击搜索查找来源", { exact: true }),
  ).toBeVisible();
  expect(writes.filter((v) => v.url.endsWith("/source-searches"))).toHaveLength(
    count,
  );
  await query.press("Enter");
  await expect(candidates).toHaveCount(2);
  await expect.poll(() => unresolved.count()).toBe(0);
  expect(
    writes.filter((v) => v.url.endsWith("/source-searches")).at(-1).body.mode,
  ).toBe("title");
  const sourceID = writes.filter((v) =>
    v.url.endsWith("/source-searches"),
  ).length;
  expect(sourceID).toBe(2);
  const freshMember = (await get(`${base}/members?page_size=100`)).items.find(
    (v) => v.id === item.id,
  );
  const titleID = freshMember.source_search_id;
  const db = new DatabaseSync(
    path.join(fixture.run_directory, "data", "app.sqlite"),
  );
  db.exec("PRAGMA busy_timeout=5000;BEGIN IMMEDIATE");
  for (let i = 3; i <= 17; i++)
    db.prepare(
      "INSERT INTO source_search_results(id,search_id,url,gallery_id,title,position) VALUES(?,?,?,?,?,?)",
    ).run(
      `anonymous-result-${i}`,
      titleID,
      `https://e-hentai.org/g/${i === 17 ? 999999 : i}/0123456789/`,
      String(i === 17 ? 999999 : i),
      `匿名分页候选 ${i}`,
      i,
    );
  db.prepare(
    "UPDATE source_searches SET continuation='unknown',revision=revision+1 WHERE id=?",
  ).run(titleID);
  db.exec("COMMIT");
  db.close();
  await expect(candidates).toHaveCount(10);
  await drawer.getByRole("button", { name: "查询更多", exact: true }).click();
  await expect(candidates).toHaveCount(7);
  await expect.poll(() => unresolved.count()).toBe(1);
  const laterLoad = writes.filter((v) => v.url.endsWith("/load-page")).at(-1);
  expect(laterLoad.body.page).toBe(2);
  await expect(
    candidates.first().getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeEnabled();
  await assertReferencePosition();
  await screenshots("title-search");
  // Restore the query, page, tag expansion and single-body scroll after closing.
  await candidates
    .first()
    .getByRole("button", { name: /^更多资料/ })
    .click();
  await scrollArea().evaluate((el) => {
    el.scrollTop = 450;
  });
  await expect
    .poll(() => scrollArea().evaluate((el) => el.scrollTop))
    .toBeGreaterThan(400);
  await drawer.getByRole("button", { name: "返回审核", exact: true }).click();
  await row
    .getByRole("button", { name: "按标题查找来源", exact: true })
    .click();
  await expect(query).toHaveValue("匿名 星の旅行");
  await expect(candidates).toHaveCount(7);
  await expect(
    candidates.first().getByRole("button", { name: "收起资料", exact: true }),
  ).toBeVisible();
  await expect
    .poll(() => scrollArea().evaluate((el) => el.scrollTop))
    .toBeGreaterThan(400);
  await candidates
    .first()
    .getByRole("button", { name: "使用此候选", exact: true })
    .click();
  await expect(drawer).not.toBeVisible();
  expect((await get(`/books/${item.id}`)).metadata).toEqual(before);
  // The original confirmation is still required to publish the staged candidate.
  await row.getByRole("checkbox").check();
  await page
    .getByRole("button", { name: "确认所选匹配（1）", exact: true })
    .click();
  await expect
    .poll(async () => (await get(`/books/${item.id}`)).metadata.source?.url)
    .toBe(
      (await get(`${base}/members?page_size=100`)).items.find(
        (v) => v.id === item.id,
      ).candidate.url,
    );
  expect((await get(base)).stage).toBe("metadata");
  // UI-only fault snapshots are deliberately mocked; the provider and backend
  // error contracts have separate anonymous server tests. No real site is used.
  const other = members.find((v) => v.id !== item.id && !v.scan_error);
  await page
    .getByTestId(`import-member-${other.id}`)
    .getByRole("button", { name: "按标题查找来源", exact: true })
    .click();
  await query.fill("匿名状态检查");
  await query.press("Enter");
  await expect(candidates).toHaveCount(2);
  await expect.poll(() => unresolved.count()).toBe(0);
  const second = (await get(`${base}/members?page_size=100`)).items.find(
    (v) => v.id === other.id,
  );
  const sid = second.source_search_id;
  let phase = "normal";
  const snapshot = (value) => {
    const state = { ...value };
    if (phase === "auth") {
      state.state = "paused";
      state.error_code = "provider_auth_required";
    }
    if (phase === "parse") {
      state.state = "failed";
      state.error_code = "invalid_response";
    }
    if (phase === "zero") {
      state.state = "completed";
      state.error_code = "";
      state.loaded_count = 0;
      state.continuation = "end";
    }
    if (phase === "partial") {
      state.state = "failed";
      state.error_code = "provider_rejected";
    }
    if (phase === "limit") {
      state.state = "completed";
      state.error_code = "";
      state.continuation = "limited";
    }
    return state;
  };
  await page.route(`**/source-searches/${sid}`, async (route) => {
    const response = await route.fetch();
    return route.fulfill({ response, json: snapshot(await response.json()) });
  });
  await page.route(`**/source-searches/${sid}/results?*`, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.search = snapshot(body.search);
    if (phase === "zero") body.items = [];
    if (phase === "partial")
      body.items[0] = {
        ...body.items[0],
        state: "failed",
        error_code: "provider_rejected",
      };
    return route.fulfill({ response, json: body });
  });
  const refreshSnapshot = async (value) => {
    phase = value;
    await page.evaluate(
      (id) =>
        window.dispatchEvent(
          new CustomEvent("resources-changed", {
            detail: { type: "source_search", resource_id: id },
          }),
        ),
      sid,
    );
  };
  await refreshSnapshot("auth");
  await expect(
    drawer.getByText("站点认证已失效，请检查站点凭据后恢复查询。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    drawer.getByRole("button", { name: "恢复查询", exact: true }),
  ).toBeVisible();
  await screenshots("authentication");
  await refreshSnapshot("parse");
  await expect(
    drawer.getByText("站点响应无法解析，请重试或检查站点设置。", {
      exact: true,
    }),
  ).toBeVisible();
  await refreshSnapshot("zero");
  await expect(
    drawer.getByText("没有找到候选，可改用标题查找", { exact: true }),
  ).toBeVisible();
  await refreshSnapshot("partial");
  await expect(
    drawer.getByText("本页 1 个候选获取失败，其他候选仍可比较。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    candidates.first().getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeDisabled();
  await expect(
    candidates.nth(1).getByRole("button", { name: "使用此候选", exact: true }),
  ).toBeEnabled();
  await refreshSnapshot("limit");
  await expect(drawer.getByText(/已达资源上限/)).toBeVisible();
  await expect(
    drawer.getByRole("button", { name: "查询更多", exact: true }),
  ).toHaveCount(0);
  await refreshSnapshot("normal");
  await expect(
    drawer.getByRole("button", { name: "查询更多", exact: true }),
  ).toHaveCount(0);
  let rejectQuery = true;
  await page.route(`**${base}/members/${other.id}/source-searches`, (route) => {
    if (rejectQuery) {
      rejectQuery = false;
      return route.fulfill({
        status: 503,
        json: { error: { code: "network_error", message: "匿名查询网络失败" } },
      });
    }
    return route.continue();
  });
  await query.fill("失败查询的草稿");
  await query.press("Enter");
  await expect(
    drawer.getByText("匿名查询网络失败", { exact: true }),
  ).toBeVisible();
  await expect(candidates).toHaveCount(2);
  await expect(query).toHaveValue("失败查询的草稿");
  await expect(drawer.getByText(/结果来自“匿名状态检查”/)).toBeVisible();
  let release, started;
  const gate = new Promise((r) => (release = r)),
    begin = new Promise((r) => (started = r));
  let held = false;
  await page.route(`**/source-searches/${sid}/results?*`, async (route) => {
    if (held) return route.fallback();
    held = true;
    const response = await route.fetch();
    const body = await response.json();
    body.items[0].title = "迟到旧结果不可展示";
    if (body.items[0].fields) body.items[0].fields.title = "迟到旧结果不可展示";
    started();
    await gate;
    try {
      await route.fulfill({ response, json: body });
    } catch {
      /* The read was aborted after switching queries. */
    }
  });
  await refreshSnapshot("normal");
  await begin;
  await drawer.getByRole("tab", { name: "匹配结果", exact: true }).click();
  await expect(candidates).toHaveCount(2);
  release();
  await expect(
    drawer.getByText("迟到旧结果不可展示", { exact: true }),
  ).toHaveCount(0);
  await drawer
    .getByRole("button", { name: "粘贴画廊网址重新匹配", exact: true })
    .click();
  await expect(drawer).not.toBeVisible();
  const manual = page
    .getByTestId(`import-member-${other.id}`)
    .getByRole("textbox", { name: "手工填写画廊网址", exact: true });
  await expect(manual).toBeVisible();
  await expect(manual).toBeFocused();
  await page
    .getByTestId(`import-member-${other.id}`)
    .getByRole("button", { name: "取消", exact: true })
    .click();
  await page.goto("/admin/jobs?type=metadata_search");
  await expect(
    page.getByRole("heading", { name: "任务中心", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "来源查询", exact: true }).first(),
  ).toBeVisible();
  expect(errors).toEqual([]);
});
