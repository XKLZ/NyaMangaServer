import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { deflateSync } from "node:zlib";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
installViteSource(test);
function png(width, height, foreground = false, alternate = false) {
  const crc = (bytes) => {
    let n = 0xffffffff;
    for (const byte of bytes) {
      n ^= byte;
      for (let i = 0; i < 8; i++) n = (n >>> 1) ^ (n & 1 ? 0xedb88320 : 0);
    }
    return (n ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type), data]),
      out = Buffer.alloc(body.length + 8);
    out.writeUInt32BE(data.length);
    body.copy(out, 4);
    out.writeUInt32BE(crc(body), body.length + 4);
    return out;
  };
  const rows = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const i = y * (width * 4 + 1) + 1 + x * 4;
      const transparent = foreground && (x < width / 4 || x > (width * 3) / 4);
      rows.set(
        alternate
          ? [230, 170, 70, transparent ? 0 : 255]
          : foreground
            ? [90, 125, 230, transparent ? 0 : 255]
            : [150 + (x % 100), 160 + (y % 90), 190, 255],
        i,
      );
    }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 6;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(rows)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}
import { tagImageFixture } from "./tag-image-fixture.mjs";

test("独立主体图片与详情选好即保存、直接删除及失败保护", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  await page.addInitScript(() =>
    document.addEventListener(
      "DOMContentLoaded",
      () => {
        const policy = document.createElement("meta");
        policy.httpEquiv = "Content-Security-Policy";
        policy.content = "img-src 'self' data:";
        document.head.append(policy);
      },
      { once: true },
    ),
  );
  const {
    lib,
    other,
    tag,
    zero,
    initial,
    bookBefore,
    metadataBefore,
    get,
    upload,
    headers,
  } = await tagImageFixture(page, baseURL);
  const fgBytes = png(80, 160, true),
    bgBytes = png(240, 120),
    replacement = png(80, 160, true, true);
  const hero = (
    await get(`/tag-catalog?library_id=${lib.id}&category=character`)
  ).items[0];
  await upload(tag.id, "foreground", 0, fgBytes);
  await upload(tag.id, "background", 0, bgBytes);
  await upload(hero.id, "foreground", 0, fgBytes);
  await upload(hero.id, "background", 0, bgBytes);
  const reads = [],
    writes = [],
    errors = [];
  let chooserCount = 0;
  page.on("filechooser", () => chooserCount++);
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (request.method() === "GET") reads.push(url);
    else if (/\/images\/(foreground|background)$/.test(url.pathname))
      writes.push({
        method: request.method(),
        url,
        headers: request.headers(),
        bytes: request.postDataBuffer(),
      });
  });
  const modal = page.locator(".tag-detail-modal"),
    fg = modal.getByRole("region", { name: "主体图", exact: true }),
    bg = modal.getByRole("region", { name: "背景图", exact: true });
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/maintenance/tag-image-hover-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  async function screenshots(label) {
    for (const colorScheme of ["light", "dark"]) {
      await page.emulateMedia({ colorScheme });
      await expect(page.locator(".admin-shell")).toHaveClass(
        new RegExp(colorScheme),
      );
      const themeInput = modal.getByRole("textbox", { name: "标签中文译名" });
      if (await themeInput.count())
        await expect(themeInput).toHaveCSS(
          "background-color",
          colorScheme === "dark" ? "rgb(20, 20, 20)" : "rgb(255, 255, 255)",
        );
      for (const width of info.project.name === "mobile"
        ? [390]
        : [1280, 1440]) {
        await page.setViewportSize({
          width,
          height: info.project.name === "mobile" ? 844 : 900,
        });
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth),
        ).toBe(width);
        if (label.endsWith("-info")) {
          await leaveFrame();
          await fg
            .getByRole("button", { name: "主体图信息", exact: true })
            .focus();
          await expect(page.locator(".ant-popover:visible")).toHaveCSS(
            "opacity",
            "1",
          );
        }
        await page.screenshot({
          path: path.join(
            shots,
            `${info.project.name}-${label}-${width}-${colorScheme}.png`,
          ),
          animations: label.endsWith("-info") ? "allow" : "disabled",
        });
      }
    }
  }
  await page.goto(`/admin/tags?library_id=${lib.id}&category=parody`);
  const card = page.locator(`.tag-directory [data-tag-id="${tag.id}"]`);
  await expect(card.locator("img")).toHaveCount(1);
  await expect(card.locator(".tag-artwork")).toHaveCSS("aspect-ratio", "2 / 1");
  await expect(card.locator("img")).toHaveCSS("object-fit", "contain");
  await expect(card.locator(".tag-artwork")).toHaveCSS(
    "background-color",
    "rgba(0, 0, 0, 0)",
  );
  expect(
    reads.filter((url) => /background\/content$/.test(url.pathname)),
  ).toHaveLength(0);
  expect(reads.filter((url) => /\/images$/.test(url.pathname))).toHaveLength(0);
  await screenshots("original-directory");
  await card.click();
  await expect(
    fg.getByRole("img", { name: "主体图", exact: true }),
  ).toBeVisible();
  await expect(
    bg.getByRole("img", { name: "背景图", exact: true }),
  ).toBeVisible();
  await expect(
    modal.getByRole("button", { name: "编辑图片", exact: true }),
  ).toHaveCount(0);
  await expect(modal.getByRole("button", { name: /^保存主体图$/ })).toHaveCount(
    0,
  );
  await expect(fg.getByText(/JPEG、PNG/)).toHaveCount(0);
  const information = fg.getByRole("button", {
      name: "主体图信息",
      exact: true,
    }),
    controls = fg.locator(".tag-image-corners"),
    hint = fg.locator(".tag-image-picker-hint"),
    popover = page.locator(".ant-popover:visible"),
    image = (await get(`/tag-catalog/${tag.id}/images`)).foreground,
    imageInfo = `${image.width} × ${image.height} · ${image.format.toUpperCase()}`;
  async function leaveFrame() {
    await modal
      .getByRole("button", { name: "关闭标签详情", exact: true })
      .focus();
    await page.mouse.move(0, 0);
    await expect(popover).toHaveCount(0);
  }
  await leaveFrame();
  await expect(controls).toHaveCSS(
    "opacity",
    info.project.name === "desktop" ? "0" : "1",
  );
  await expect(hint).toHaveCSS("opacity", "0");
  for (const button of await controls.getByRole("button").all())
    await expect(button).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  const cornerPosition = await controls.evaluate((element) => {
    const frame = element.closest(".tag-image-frame").getBoundingClientRect(),
      box = element.getBoundingClientRect();
    return {
      right: frame.right - box.right,
      bottom: frame.bottom - box.bottom,
    };
  });
  expect(cornerPosition).toEqual({ right: 4, bottom: 4 });
  await screenshots("image-idle");
  if (info.project.name === "desktop") {
    await fg.locator(".tag-role-preview").hover();
    await expect(controls).toHaveCSS("opacity", "1");
    await expect(hint).toHaveCSS("opacity", "1");
    await expect(hint).toHaveText("点击更换");
    await screenshots("image-hover");
    await information.hover();
    await expect(popover).toHaveText(imageInfo);
    await popover.hover();
    // The popup is outside the frame. Its open state keeps the icons visible.
    expect(
      await fg.locator(".tag-image-frame").evaluate((n) => n.matches(":hover")),
    ).toBe(false);
    await expect(controls).toHaveCSS("opacity", "1");
    await leaveFrame();
  }
  if (info.project.name === "desktop")
    await fg.locator(".tag-role-preview").hover();
  await information.click();
  await expect(popover).toHaveText(imageInfo);
  expect(chooserCount).toBe(0);
  await screenshots("image-info");
  await leaveFrame();
  await information.focus();
  await expect(popover).toHaveText(imageInfo);
  await expect(hint).toHaveCSS("opacity", "1");
  await page.keyboard.press("Escape");
  await expect(popover).toHaveCount(0);
  await expect(modal).toBeVisible();
  await expect(
    fg.getByRole("button", { name: "替换主体图", exact: true }),
  ).toBeEnabled();
  // Native picker cancellation is a no-op.
  const chooserEvent = page.waitForEvent("filechooser");
  await fg.getByRole("button", { name: "替换主体图", exact: true }).click();
  await (await chooserEvent).setFiles([]);
  expect(chooserCount).toBe(1);
  expect(writes).toHaveLength(0);
  await screenshots("separate-detail");
  let release, started;
  const gate = new Promise((resolve) => {
      release = resolve;
    }),
    ready = new Promise((resolve) => {
      started = resolve;
    });
  const hold = async (route) => {
    started();
    await gate;
    await route.continue();
  };
  await page.route(`**/api/v1/tag-catalog/${tag.id}/images/foreground`, hold);
  await fg.getByLabel("选择主体图", { exact: true }).setInputFiles({
    name: "auto-save.png",
    mimeType: "",
    buffer: replacement,
  });
  await ready;
  await expect(hint).toHaveCSS("opacity", "0");
  await expect(
    modal.getByRole("button", { name: "关闭标签详情", exact: true }),
  ).toBeDisabled();
  await expect(
    bg.getByRole("button", { name: "替换背景图", exact: true }),
  ).toBeDisabled();
  release();
  await page.unroute(`**/api/v1/tag-catalog/${tag.id}/images/foreground`, hold);
  await expect(fg.getByRole("status")).toHaveText("已保存");
  expect(writes[0].headers["x-expected-revision"]).toBe("1");
  expect(writes[0].bytes.equals(replacement)).toBe(true);
  expect(writes[0].headers["content-type"]).toBe("image/png");
  expect(writes[0].headers["x-csrf-token"]).toBe(headers["X-CSRF-Token"]);
  // A 503 retains the old image and file, with a same-revision retry.
  let failed = false;
  const outage = async (route) => {
    if (!failed) {
      failed = true;
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "tag_image_storage_unavailable",
            message: "匿名图片存储暂不可用",
          },
        }),
      });
    } else await route.fallback();
  };
  await page.route(`**/api/v1/tag-catalog/${tag.id}/images/foreground`, outage);
  await fg.getByLabel("选择主体图", { exact: true }).setInputFiles({
    name: "retry-same-version.png",
    mimeType: "image/png",
    buffer: fgBytes,
  });
  await expect(
    fg.getByText("retry-same-version.png", { exact: true }),
  ).toBeVisible();
  await expect(
    fg.getByRole("img", { name: "主体图", exact: true }),
  ).toHaveAttribute("src", /revision=2$/);
  await expect(
    fg.getByRole("button", { name: "读取当前主体图", exact: true }),
  ).toHaveCount(0);
  await modal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  const guard = page.getByRole("dialog", {
    name: "有尚未保存的修改",
    exact: true,
  });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "继续编辑", exact: true }).click();
  await upload(tag.id, "background", 1, bgBytes); // SSE for another role never rebases this pending role.
  await fg.getByRole("button", { name: "重试上传主体图", exact: true }).click();
  await expect(fg.getByRole("status")).toHaveText("已保存");
  await page.unroute(
    `**/api/v1/tag-catalog/${tag.id}/images/foreground`,
    outage,
  );
  expect(
    writes.slice(1, 3).map((w) => w.headers["x-expected-revision"]),
  ).toEqual(["2", "2"]);
  // Real CAS conflict: show the new server image and require an explicit retry.
  let once = false;
  const race = async (route) => {
    if (!once) {
      once = true;
      await upload(tag.id, "foreground", 3, fgBytes);
    }
    await route.continue();
  };
  await page.route(`**/api/v1/tag-catalog/${tag.id}/images/foreground`, race);
  await fg.getByLabel("选择主体图", { exact: true }).setInputFiles({
    name: "conflict-keeps-file.png",
    mimeType: "image/png",
    buffer: replacement,
  });
  await expect(
    fg.getByText("当前图片已变化，请核对后重试", { exact: true }),
  ).toBeVisible();
  await expect(
    fg.getByRole("img", { name: "当前主体图", exact: true }),
  ).toHaveAttribute("src", /revision=4$/);
  await expect(
    fg.getByText("conflict-keeps-file.png", { exact: true }),
  ).toBeVisible();
  await page.unroute(`**/api/v1/tag-catalog/${tag.id}/images/foreground`, race);
  await fg
    .getByRole("button", { name: "核对后重试主体图", exact: true })
    .click();
  await expect(fg.getByRole("status")).toHaveText("已保存");
  expect(writes.slice(-2).map((w) => w.headers["x-expected-revision"])).toEqual(
    ["3", "4"],
  );
  // Rapid continuous selection supersedes the earlier file before writing.
  await page.evaluate(() => {
    const read = FileReader.prototype.readAsDataURL;
    const queued = [];
    window.pendingImageReads = () => queued.length;
    window.releaseImageReads = () =>
      queued.splice(0).forEach((release) => release());
    FileReader.prototype.readAsDataURL = function (file) {
      queued.push(() => {
        if (this.readyState !== FileReader.DONE) read.call(this, file);
      });
    };
  });
  const count = writes.length;
  await fg.getByLabel("选择主体图", { exact: true }).setInputFiles({
    name: "superseded.png",
    mimeType: "image/png",
    buffer: fgBytes,
  });
  await expect
    .poll(() => page.evaluate(() => window.pendingImageReads()))
    .toBe(1);
  await fg.getByLabel("选择主体图", { exact: true }).setInputFiles({
    name: "latest-only.png",
    mimeType: "image/png",
    buffer: replacement,
  });
  await expect
    .poll(() => page.evaluate(() => window.pendingImageReads()))
    .toBe(2);
  await page.evaluate(() => window.releaseImageReads());
  await expect(fg.getByRole("status")).toHaveText("已保存");
  expect(writes.length).toBe(count + 1);
  expect(writes.at(-1).bytes.equals(replacement)).toBe(true);
  const beforeLeave = writes.length;
  await fg.getByLabel("选择主体图", { exact: true }).setInputFiles({
    name: "discard-on-leave.png",
    mimeType: "image/png",
    buffer: fgBytes,
  });
  await expect
    .poll(() => page.evaluate(() => window.pendingImageReads()))
    .toBe(1);
  await modal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  await guard
    .getByRole("button", { name: "放弃修改并离开", exact: true })
    .click();
  await expect(modal).not.toBeVisible();
  await page.evaluate(() => window.releaseImageReads());
  expect(writes.length).toBe(beforeLeave);
  // Character cards use only the contained portrait foreground, at a narrower width.
  await page.goto(`/admin/tags?library_id=${lib.id}&category=character`);
  const portrait = page.locator(`.tag-directory [data-tag-id="${hero.id}"]`);
  await expect(portrait.locator("img")).toHaveCount(1);
  await expect(portrait.locator(".tag-artwork")).toHaveCSS(
    "aspect-ratio",
    "2 / 3",
  );
  await portrait.click();
  await expect(
    fg.getByRole("button", { name: "替换主体图", exact: true }),
  ).toBeVisible();
  await expect(fg.locator(".tag-role-preview")).toHaveCSS(
    "aspect-ratio",
    "2 / 3",
  );
  await screenshots("character-detail");
  await fg.getByRole("button", { name: "主体图信息", exact: true }).focus();
  await expect(popover).toHaveText(imageInfo);
  await page.keyboard.press("Escape");
  if (info.project.name === "mobile")
    await expect(controls).toHaveCSS("opacity", "1");
  await fg.getByRole("button", { name: "删除主体图", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(fg.getByRole("status")).toHaveText("已删除");
  expect(writes.at(-1).method).toBe("DELETE");
  expect(writes.at(-1).bytes).toBeNull();
  expect(chooserCount).toBe(1);
  await expect(
    fg.getByRole("button", { name: "添加主体图", exact: true }),
  ).toBeEnabled();
  await expect(hint).toHaveText("点击添加");
  await fg.getByRole("button", { name: "添加主体图", exact: true }).focus();
  await expect(hint).toHaveCSS("opacity", "1");
  await information.click();
  await expect(popover).toHaveText(
    "点击导入JPEG、PNG、WebP、BMP 格式图片 · 最多10 MiB",
  );
  await screenshots("empty-image-info");
  expect(chooserCount).toBe(1);
  await leaveFrame();
  await expect(page.getByRole("dialog", { name: /确认移除/ })).toHaveCount(0);
  expect(
    (await get(`/tag-catalog/${hero.id}/images`)).background.revision,
  ).toBe(1);
  expect(await get(`/books/${bookBefore.id}`)).toEqual(metadataBefore);
  expect(
    (await get(`/tag-catalog/${tag.id}?library_id=${lib.id}`))
      .translation_revision,
  ).toBe(initial.translation_revision);
  expect(
    (
      await get(
        `/tag-catalog/${tag.id}/relations?library_id=${lib.id}&view=all`,
      )
    ).items.find((row) => row.character.id === zero.id).revision,
  ).toBe(1);
  await modal
    .getByRole("button", { name: "关闭标签详情", exact: true })
    .click();
  await page.goto(`/admin/tags?library_id=${other.id}&tag=${tag.id}`);
  await expect(
    fg.getByRole("img", { name: "主体图", exact: true }),
  ).toHaveAttribute("src", /revision=6$/);
  expect(errors).toEqual([]);
});
