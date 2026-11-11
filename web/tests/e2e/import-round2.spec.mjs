import { enableAnonymousRecommendations } from "./enhancement-fixture.mjs";
import { test, expect } from "@playwright/test";
import { copyFile, readFile, mkdir } from "node:fs/promises";
import { DatabaseSync } from "node:sqlite";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);

test("导入入口、失败与历史、60项连续审核保留DOM和草稿", async ({
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
        `匿名连续审核${String(i).padStart(2, "0")}.cbz`,
      ),
    );
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: {
      setup_token: await readFile(fixture.token_file, "utf8"),
      password: "Anonymous round two password 2026",
    },
  });
  expect(setup.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await setup.json()).csrf_token,
  };
  await enableAnonymousRecommendations(page, baseURL, headers);
  async function get(url) {
    const response = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(response.status(), url).toBe(200);
    return response.json();
  }
  async function post(url, data = {}, status = 200) {
    const response = await page.request.post(`${baseURL}/api/v1${url}`, {
      headers,
      data,
    });
    expect(response.status(), url).toBe(status);
    return response.json();
  }
  const lib = await post(
    "/libraries",
    { root_key: "anonymous", display_name: "匿名连续导入" },
    201,
  );
  const other = await post(
    "/libraries",
    { root_key: "anonymous-work", display_name: "匿名停用书库" },
    201,
  );
  const scan = await post(`/libraries/${other.id}/scans`, {}, 202);
  await expect
    .poll(async () => (await get(`/jobs/${scan.job_id}`)).state)
    .toBe("completed");
  const otherNow = (await get("/libraries")).items.find(
    (v) => v.id === other.id,
  );
  expect(otherNow.edition_count).toBe(3);
  expect(
    (
      await page.request.patch(
        `${baseURL}/api/v1/libraries/${other.id}/status`,
        {
          headers,
          data: { expected_revision: otherNow.revision, disabled: true },
        },
      )
    ).status(),
  ).toBe(200);
  const errors = [],
    memberURLs = new Set(),
    writes = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    if (/\/members\?/.test(r.url())) memberURLs.add(r.url());
    if (r.method() === "POST") writes.push(r.url());
  });
  await page.goto("/admin/libraries");
  const row = page.getByRole("row").filter({ hasText: "匿名连续导入" });
  const disabledRow = page.getByRole("row").filter({ hasText: "匿名停用书库" });
  await expect(
    disabledRow.getByRole("button", { name: "导入漫画", exact: true }),
  ).toBeDisabled();
  await expect(
    disabledRow.getByRole("cell", { name: "3", exact: true }),
  ).toHaveCount(2);
  await expect(
    page.getByRole("heading", { name: "书库", exact: true }),
  ).toHaveClass("sr-only");
  const shots = path.join(fixture.run_directory, `round2-${info.project.name}`);
  await mkdir(shots, { recursive: true });
  async function screenshot(name) {
    for (const width of info.project.name === "desktop"
      ? [1280, 1440]
      : [390]) {
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
        await page.screenshot({
          path: path.join(
            shots,
            `${name}-${width}-${dark ? "dark" : "light"}.png`,
          ),
          animations: "disabled",
        });
      }
    }
  }
  await screenshot("libraries");
  // Dispatch twice in the same event turn: the ref lock admits only one POST.
  await row
    .getByRole("button", { name: "导入漫画", exact: true })
    .evaluate((el) => {
      el.click();
      el.click();
    });
  await expect(page).toHaveURL(/batch=/);
  const id = new URL(page.url()).searchParams.get("batch"),
    base = `/import-batches/${id}`;
  expect(writes.filter((v) => v.endsWith("/import-batches"))).toHaveLength(1);
  await expect
    .poll(
      async () => {
        const b = await get(base);
        return `${b.stage}:${b.state}`;
      },
      { timeout: 30000 },
    )
    .toBe("metadata:review");
  let all = (await get(`${base}/members?page_size=100`)).items;
  expect(all).toHaveLength(60);
  const withCandidate = all.find((v) => !v.scan_error);
  await post(
    `${base}/members/${withCandidate.id}/refetch`,
    {
      expected_revision: withCandidate.revision,
      gallery_url: "https://e-hentai.org/g/1001/0123456789/",
    },
    202,
  );
  await expect.poll(async () => (await get(base)).state).toBe("review");
  all = (await get(`${base}/members?page_size=100`)).items;
  await page.goto("/admin/libraries");
  await expect(
    row.getByRole("button", { name: "继续导入", exact: true }),
  ).toBeVisible();
  await row.getByRole("button", { name: "继续导入", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`batch=${id}`));
  expect(writes.filter((v) => v.endsWith("/import-batches"))).toHaveLength(1);
  // Simulate a stale list racing with another tab's active batch.
  let staleLibrary = true;
  await page.route("**/api/v1/libraries", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    if (staleLibrary) {
      body.items.find((v) => v.id === lib.id).active_import = null;
    }
    return route.fulfill({ response, json: body });
  });
  await page.route("**/api/v1/import-batches", (route) => {
    // Both directory readers see the same stale snapshot until the attempted
    // create; its 409 recovery must then receive the current directory.
    if (route.request().method() === "POST") staleLibrary = false;
    return route.fallback();
  });
  await page.goto("/admin/libraries");
  await row.getByRole("button", { name: "导入漫画", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`batch=${id}`));
  expect(writes.filter((v) => v.endsWith("/import-batches"))).toHaveLength(2);
  expect((await get(`/import-batches?library_id=${lib.id}`)).total).toBe(1);
  // An anonymous in-progress read keeps polling active while all writes remain
  // formal APIs. Shorten only its timer to exercise 60 responses in bounded time.
  await page.addInitScript(() => {
    const interval = window.setInterval.bind(window);
    window.setInterval = (fn, delay, ...args) =>
      interval(fn, delay === 2000 ? 200 : delay, ...args);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { readText: () => Promise.reject(Error("anonymous denied")) },
    });
  });
  let processingRead = true,
    failRead = false;
  await page.route(`**/api/v1${base}`, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    if (processingRead) body.state = "processing";
    await route.fulfill({ response, json: body });
  });
  let observedPending = 60,
    memberReads = 0;
  await page.route(`**/api/v1${base}/members?*`, async (route) => {
    if (failRead)
      return route.fulfill({
        status: 503,
        json: {
          error: {
            code: "anonymous_read_failure",
            message: "anonymous read failure",
          },
        },
      });
    const response = await route.fetch();
    const body = await response.json();
    observedPending = (await get(base)).pending;
    memberReads++;
    return route.fulfill({ response, json: body });
  });
  await page.goto(`/admin/imports?batch=${id}&filter=all`);
  const cards = page.locator(".import-review-row");
  await expect(cards).toHaveCount(25);
  const anchorID = `import-member-${withCandidate.id}`;
  const anchor = page.getByTestId(anchorID);
  await anchor
    .getByRole("button", { name: "编辑候选资料", exact: true })
    .click();
  const editor = page.getByRole("dialog", {
    name: "核对并修改候选资料",
    exact: true,
  });
  const titleDraft = editor.getByRole("textbox", {
    name: "其他标题",
    exact: true,
  });
  await titleDraft.fill("匿名编辑草稿保持");
  const beforeReads = memberReads;
  await expect.poll(() => memberReads).toBeGreaterThan(beforeReads + 2);
  await expect(titleDraft).toHaveValue("匿名编辑草稿保持");
  await expect(titleDraft).toBeFocused();
  await editor.locator(".ant-drawer-close").click();
  await page.getByRole("button", { name: "放弃修改", exact: true }).click();
  await expect(editor).not.toBeVisible();
  await anchor.getByRole("button", { name: "查看资料", exact: true }).click();
  await anchor
    .getByRole("button", { name: "粘贴网址重新匹配", exact: true })
    .click();
  const input = anchor.getByRole("textbox", {
    name: "手工填写画廊网址",
    exact: true,
  });
  await input.fill("https://e-hentai.org/g/55/0123456789/");
  await input.focus();
  await page
    .locator(".import-review-scroll")
    .evaluate((el) => (el.scrollTop = 30));
  const retainedScroll = await page
    .locator(".import-review-scroll")
    .evaluate((el) => el.scrollTop);
  await page.evaluate((testID) => {
    const card = document.querySelector(`[data-testid="${testID}"]`);
    window.__retained = { card, image: card.querySelector("img"), removed: 0 };
    new MutationObserver((records) => {
      for (const r of records)
        for (const n of r.removedNodes)
          if (
            n === window.__retained.card ||
            n.contains?.(window.__retained.card)
          )
            window.__retained.removed++;
    }).observe(document.querySelector(".import-review-frame"), {
      childList: true,
      subtree: true,
    });
  }, anchorID);
  failRead = true;
  await expect(
    page.getByText("更新失败，已保留当前内容", { exact: true }),
  ).toBeVisible();
  await expect(cards).toHaveCount(25);
  await expect(input).toBeFocused();
  failRead = false;
  await expect(
    page.getByText("更新失败，已保留当前内容", { exact: true }),
  ).toHaveCount(0);
  const ordered = all
    .filter((v) => `import-member-${v.id}` !== anchorID)
    .concat(all.filter((v) => `import-member-${v.id}` === anchorID));
  for (let i = 0; i < ordered.length; i++) {
    const item = ordered[i];
    await post(`${base}/reviews`, {
      items: [
        { id: item.id, expected_revision: item.revision, decision: "skip" },
      ],
    });
    await expect.poll(() => observedPending).toBe(59 - i);
    await expect(input).toHaveValue("https://e-hentai.org/g/55/0123456789/");
    await expect(input).toBeFocused();
    expect(
      await page
        .locator(".import-review-scroll")
        .evaluate((el) => el.scrollTop),
    ).toBe(retainedScroll);
    expect(
      await page.evaluate(() => ({
        connected: window.__retained.card.isConnected,
        removed: window.__retained.removed,
        image:
          window.__retained.card.querySelector("img") ===
          window.__retained.image,
      })),
    ).toEqual({ connected: true, removed: 0, image: true });
  }
  expect((await get(base)).pending).toBe(0);
  expect((await get(base)).stage).toBe("metadata");
  expect(
    [...memberURLs].some((v) => new URL(v).searchParams.has("refresh")),
  ).toBe(false);
  await screenshot("retained-metadata");
  await input.fill("");
  await anchor.getByRole("button", { name: "取消", exact: true }).click();
  processingRead = false;
  // Seed only this anonymous database. Retry/skip/completion use real HTTP.
  const db = new DatabaseSync(
    path.join(fixture.run_directory, "data", "app.sqlite"),
  );
  db.exec("PRAGMA busy_timeout=5000; BEGIN IMMEDIATE");
  db.prepare(
    "UPDATE import_batches SET stage='versions',state='processing',revision=revision+1 WHERE id=?",
  ).run(id);
  db.prepare(
    "INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES('anonymous-failed-version',?,'versions')",
  ).run(id);
  db.prepare(
    "INSERT INTO jobs(id,library_id,type,state,stage,generation,error_code,created_at,updated_at) VALUES('anonymous-failed-version',?,'work_recommendation','failed','pairs',1,'recommendation_worker_failed',unixepoch(),unixepoch())",
  ).run(lib.id);
  db.prepare(
    "UPDATE import_batches SET state='failed',revision=revision+1 WHERE id=?",
  ).run(id);
  db.exec("COMMIT");
  await page.getByRole("button", { name: "刷新当前状态", exact: true }).click();
  await expect(page.getByText("版本推荐失败", { exact: true })).toBeVisible();
  await expect(
    page.getByText("没有待处理的归属建议", { exact: true }),
  ).toHaveCount(0);
  await screenshot("failed-recommendation");
  await page.route(`**${base}/control/retry`, (route) =>
    route.fulfill({
      status: 503,
      json: {
        error: {
          code: "anonymous_retry_failure",
          message: "anonymous retry failure",
        },
      },
    }),
  );
  await page.getByRole("button", { name: "重试版本推荐", exact: true }).click();
  await expect(
    page.getByText("anonymous retry failure", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "跳过本次版本推荐", exact: true })
    .click();
  await page
    .getByRole("button", { name: "明确跳过本次推荐", exact: true })
    .click();
  await expect(
    page.getByText("已跳过本次版本推荐", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "刷新当前状态", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "进入系列审核", exact: true }),
  ).toBeEnabled();
  expect((await get(base)).stage).toBe("versions");
  expect(
    writes.filter((v) => v.endsWith("organization-review/complete")),
  ).toHaveLength(0);
  await page.getByRole("button", { name: "进入系列审核", exact: true }).click();
  await expect.poll(async () => (await get(base)).stage).toBe("series");
  expect(
    writes.filter((v) => v.endsWith("organization-review/complete")),
  ).toHaveLength(1);
  // A historical completed batch retains its old failed child and is read-only.
  db.exec("BEGIN IMMEDIATE");
  db.prepare(
    "INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES('anonymous-old-complete',?,'anonymous-old-complete','finished','completed')",
  ).run(other.id);
  db.prepare(
    "INSERT INTO import_children(job_id,batch_id,stage) VALUES('anonymous-failed-version','anonymous-old-complete','versions') ON CONFLICT(job_id) DO UPDATE SET batch_id=excluded.batch_id",
  ).run();
  db.exec("COMMIT");
  db.close();
  await page.goto(`/admin/imports?library_id=${other.id}&state=completed`);
  await expect(
    page.getByRole("row").filter({ hasText: "匿名停用书库" }),
  ).toContainText("曾有1项任务失败");
  await expect(
    page.getByRole("button", { name: "导入漫画", exact: true }),
  ).toHaveCount(0);
  await screenshot("history");
  await page.getByRole("button", { name: "查看记录", exact: true }).click();
  await expect(
    page.getByText("批次已完成，但曾有任务失败", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /重试.*推荐/ })).toHaveCount(0);
  expect(errors).toEqual([]);
});
