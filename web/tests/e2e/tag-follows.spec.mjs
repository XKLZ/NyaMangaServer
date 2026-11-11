import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { tagImageFixture } from "./tag-image-fixture.mjs";
installViteSource(test);
test("标签关注独立星标、跨库集合、冲突和草稿保护", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(240000);
  const { lib, other, tag, zero, get, write, headers } = await tagImageFixture(
    page,
    baseURL,
  );
  expect((await get("/settings/service")).database_schema).toBe(20);
  const artists = (
    await get(`/tag-catalog?library_id=${lib.id}&category=artist&page_size=100`)
  ).items;
  const errors = [],
    reads = [],
    writes = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (u.pathname.startsWith("/api/v1/tag-catalog")) reads.push(u);
    if (u.pathname.endsWith("/follow") && r.method() !== "GET")
      writes.push({
        method: r.method(),
        body: r.postDataJSON(),
        headers: r.headers(),
      });
  });
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/tests/tag-follows-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  async function screenshot(label) {
    for (const colorScheme of ["light", "dark"]) {
      await page.emulateMedia({ colorScheme });
      await expect(page.locator(".admin-shell")).toHaveClass(
        new RegExp(colorScheme),
      );
      for (const width of info.project.name === "mobile"
        ? [390]
        : [1280, 1440]) {
        await page.setViewportSize({
          width,
          height: info.project.name === "mobile" ? 844 : 900,
        });
        await expect
          .poll(() => page.evaluate(() => document.documentElement.scrollWidth))
          .toBe(width);
        await page.screenshot({
          path: path.join(
            shots,
            `${info.project.name}-${label}-${width}-${colorScheme}.png`,
          ),
          animations: "disabled",
        });
      }
    }
  }
  const directory = page.locator(".tag-directory"),
    modal = page.locator(".tag-detail-modal");
  const card = (id) => directory.locator(`[data-tag-id="${id}"]`);
  const star = (id) => card(id).locator(".tag-follow-button");
  async function visit(category, extra = "") {
    await page.goto(
      `/admin/tags?library_id=${lib.id}&category=${category}${extra}`,
    );
    await expect(
      directory.locator("article.tag-catalog-card").first(),
    ).toBeVisible();
  }
  await visit("artist");
  const artist = artists[0],
    button = star(artist.id);
  await page.mouse.move(0, 0);
  await expect(button).toHaveCSS(
    "opacity",
    info.project.name === "desktop" ? "0" : "1",
  );
  expect(await button.evaluate((n) => n.closest("a"))).toBeNull();
  await card(artist.id).hover();
  await expect(button).toHaveCSS("opacity", "1");
  let release, started;
  const gate = new Promise((r) => (release = r)),
    begin = new Promise((r) => (started = r));
  const endpoint = `**/api/v1/tags/${artist.id}/follow`;
  await page.route(endpoint, async (route) => {
    started();
    await gate;
    await route.continue();
  });
  await button.click();
  await begin;
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await expect(button).toBeDisabled();
  release();
  await expect(button).toHaveAttribute("aria-pressed", "true");
  await page.unroute(endpoint);
  await expect(modal).not.toBeVisible();
  expect(writes[0].method).toBe("PUT");
  expect(writes[0].body).toEqual({ expected_revision: 0 });
  expect(writes[0].headers["x-csrf-token"]).toBeTruthy();
  await directory.getByText("已关注", { exact: true }).click();
  await expect(page).toHaveURL(/followed_only=true/);
  await expect(directory.getByText("1 个标签", { exact: true })).toBeVisible();
  await card(artist.id).getByRole("link").click();
  const title = modal.getByRole("textbox", {
    name: "标签中文译名",
    exact: true,
  });
  await title.fill("匿名关注草稿");
  const detailStar = modal.locator(".tag-follow-detail");
  await detailStar.click();
  await expect(detailStar).toHaveAttribute("aria-pressed", "false");
  await expect(directory.getByText("0 个标签", { exact: true })).toBeVisible();
  await expect(title).toHaveValue("匿名关注草稿");
  await expect(modal).toBeVisible();
  await detailStar.click();
  await expect(detailStar).toHaveAttribute("aria-pressed", "true");
  await expect(title).toHaveValue("匿名关注草稿");
  await screenshot("detail-draft");
  await modal
    .getByRole("button", { name: "放弃译名修改", exact: true })
    .click();
  await modal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  // Real API mutation arrives through private SSE, not a fake revision in the UI.
  const aState = await get(`/tag-catalog/${artist.id}?library_id=${lib.id}`);
  await write(
    `/tags/${artist.id}/follow`,
    { expected_revision: aState.follow_revision },
    "DELETE",
  );
  await expect(directory.getByText("0 个标签", { exact: true })).toBeVisible();
  await visit("artist");
  await card(artist.id).hover();
  await page.route(endpoint, (route) =>
    route.fulfill({
      status: 503,
      json: { error: { code: "internal_error", message: "anonymous storage" } },
    }),
  );
  await button.click();
  await expect(
    page.getByText("关注操作结果未确认，原状态保留，请重新读取后再操作。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(button).toHaveAttribute("aria-pressed", "false");
  await page.unroute(endpoint);
  // Background rereads cannot adopt the failed operation's base version.
  const before = await get(`/tag-catalog/${artist.id}?library_id=${lib.id}`);
  await write(
    `/tags/${artist.id}/follow`,
    { expected_revision: before.follow_revision },
    "PUT",
  );
  await expect(button).toHaveAttribute("aria-pressed", "false");
  const detailPath = `**/api/v1/tag-catalog/${artist.id}?*`;
  await page.route(detailPath, (route) =>
    route.fulfill({
      status: 503,
      json: { error: { code: "internal_error", message: "anonymous read" } },
    }),
  );
  await page
    .getByRole("button", { name: "重新读取关注状态", exact: true })
    .click();
  await expect(
    page.getByText("读取关注状态失败，原状态保留，请再次读取。", {
      exact: true,
    }),
  ).toBeVisible();
  await page.unroute(detailPath);
  await page
    .getByRole("button", { name: "重新读取关注状态", exact: true })
    .click();
  await expect(button).toHaveAttribute("aria-pressed", "true");
  await page.route(endpoint, (route) =>
    route.fulfill({
      status: 409,
      json: {
        error: {
          code: "tag_follow_revision_conflict",
          message: "anonymous conflict",
        },
      },
    }),
  );
  const mark = writes.length;
  await button.click();
  await expect(
    page.getByText("关注状态已变化，请重新读取并核对后再操作。", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(button).toHaveAttribute("aria-pressed", "true");
  expect(writes.length).toBe(mark + 1);
  await page.unroute(endpoint);
  await page
    .getByRole("button", { name: "重新读取关注状态", exact: true })
    .click();
  await expect(button).toBeEnabled();
  // Whole personal collection: 51 local artists, plus an actual zero-work tag from another library.
  for (const a of artists.slice(0, 51)) {
    const s = await get(`/tag-catalog/${a.id}?library_id=${lib.id}`);
    if (!s.followed)
      await write(
        `/tags/${a.id}/follow`,
        { expected_revision: s.follow_revision },
        "PUT",
      );
  }
  await write(`/tags/${zero.id}/follow`, { expected_revision: 0 }, "PUT");
  await visit("artist", "&followed_only=true&page=2");
  await expect(directory.getByText("51 个标签", { exact: true })).toBeVisible();
  const last = directory.locator("article.tag-catalog-card");
  await expect(last).toHaveCount(1);
  await last.hover();
  const lastID = await last.getAttribute("data-tag-id");
  await last.locator(".tag-follow-button").focus();
  await page.keyboard.press("Enter");
  await expect(directory.getByText("50 个标签", { exact: true })).toBeVisible();
  await expect
    .poll(() => new URL(page.url()).searchParams.get("page"))
    .toBeNull();
  await expect
    .poll(() =>
      page.evaluate(() =>
        document.activeElement?.getAttribute("data-follow-tag"),
      ),
    )
    .not.toBeNull();
  await screenshot("followed-directory");
  await visit("character", "&followed_only=true");
  await expect(card(zero.id)).toContainText("0 个作品");
  await card(zero.id).getByRole("link").click();
  await expect(modal.locator(".tag-follow-detail")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await modal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  await visit("parody");
  await card(tag.id).getByRole("link").click();
  const related = modal.locator(
    `.tag-relation-cards [data-tag-id="${zero.id}"]`,
  );
  await expect(related.locator(".tag-follow-button")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await related.hover();
  await related.locator(".tag-follow-button").click();
  await expect(modal.locator("h2")).toHaveText(tag.translation || tag.name);
  await expect(related.locator(".tag-follow-button")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  // Keep a failed image File and translation draft across follow refreshes.
  await page.route(
    `**/api/v1/tag-catalog/${tag.id}/images/foreground`,
    (route) =>
      route.fulfill({
        status: 503,
        json: { error: { code: "internal_error", message: "anonymous image" } },
      }),
  );
  const file = {
    name: "anonymous-pending.png",
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aoc0AAAAASUVORK5CYII=",
      "base64",
    ),
  };
  await modal.getByLabel("选择主体图", { exact: true }).setInputFiles(file);
  await expect(modal.getByText(file.name, { exact: true })).toBeVisible();
  await title.fill("关注后保留译名");
  await modal.locator(".tag-follow-detail").click();
  await expect(modal.locator(".tag-follow-detail")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(title).toHaveValue("关注后保留译名");
  await expect(modal.getByText(file.name, { exact: true })).toBeVisible();
  await screenshot("image-draft");
  await modal
    .getByRole("button", { name: "取消主体图操作", exact: true })
    .click();
  await modal
    .getByRole("button", { name: "放弃译名修改", exact: true })
    .click();
  await modal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  for (const category of ["group", "content"]) {
    await visit(category);
    const s = directory.locator("article.tag-catalog-card").first();
    await s.hover();
    await s.locator(".tag-follow-button").click();
    await expect(s.locator(".tag-follow-button")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(modal).not.toBeVisible();
  }
  await screenshot("content");
  await page.getByRole("combobox", { name: "当前书库", exact: true }).click();
  await page.getByRole("option", { name: "B 共享图片", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(other.id));
  await page.goto(
    `/admin/tags?library_id=${other.id}&category=parody&followed_only=true`,
  );
  await expect(card(tag.id).locator(".tag-follow-button")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(card(tag.id)).toContainText("3 个作品");
  // Abort an old library response during an actual SPA library switch.
  await visit("artist");
  let finishRead, readStarted, readFinished;
  const readGate = new Promise((r) => {
      finishRead = r;
    }),
    readBegin = new Promise((r) => {
      readStarted = r;
    }),
    readEnd = new Promise((r) => {
      readFinished = r;
    });
  const catalogPattern = "**/api/v1/tag-catalog?*";
  await page.route(catalogPattern, async (route) => {
    if (
      new URL(route.request().url()).searchParams.get("library_id") !== lib.id
    )
      return route.fallback();
    const response = await route.fetch();
    readStarted();
    await readGate;
    try {
      await route.fulfill({ response });
    } finally {
      readFinished();
    }
  });
  await directory.getByRole("button", { name: "刷新", exact: true }).click();
  await readBegin;
  await page.getByRole("combobox", { name: "当前书库", exact: true }).click();
  await page.getByRole("option", { name: "B 共享图片", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(other.id));
  await expect(directory.getByText("1 个标签", { exact: true })).toBeVisible();
  finishRead();
  await readEnd;
  await page.unroute(catalogPattern);
  await expect(directory.locator("article.tag-catalog-card")).toHaveCount(1);
  // Compatibility: absence of fields never implies an initial revision or success.
  await page.route(catalogPattern, async (route) => {
    const response = await route.fetch(),
      json = await response.json();
    for (const item of json.items) {
      delete item.followed;
      delete item.follow_revision;
    }
    await route.fulfill({ response, json });
  });
  await visit("parody");
  await expect(star(tag.id)).toBeDisabled();
  const beforeUnsupported = writes.length;
  await card(tag.id).hover();
  expect(writes.length).toBe(beforeUnsupported);
  await page.unroute(catalogPattern);
  for (const u of reads)
    if (u.pathname !== "/api/v1/tag-catalog")
      expect(u.searchParams.has("followed_only")).toBe(false);
  expect(
    writes.every((w) => Object.keys(w.body).join() === "expected_revision"),
  ).toBe(true);
  expect(errors).toEqual([]);
});
