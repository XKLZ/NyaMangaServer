import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { tagImageFixture } from "./tag-image-fixture.mjs";
installViteSource(test);
test("标签唯一轨迹、关系单行与隐藏筛选清理", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  const { lib, tag, get, write, metadataBefore } = await tagImageFixture(
    page,
    baseURL,
  );
  const tags = [
    { namespace: "parody", name: "image original" },
    { namespace: "character", name: "image hero" },
    ...Array.from({ length: 27 }, (_, i) => ({
      namespace: "character",
      name: `browse person ${String(i).padStart(2, "0")}`,
    })),
    ...["alpha", "beta", "gamma"].map((n) => ({
      namespace: "parody",
      name: `browse ${n}`,
    })),
  ];
  await write(
    `/books/${metadataBefore.id}/overrides`,
    {
      expected_revision: metadataBefore.revision,
      fields: { tags: { op: "set", value: tags } },
    },
    "PATCH",
  );
  const originals = (
    await get(`/tag-catalog?library_id=${lib.id}&category=parody&q=browse`)
  ).items;
  const characters = (
    await get(
      `/tag-catalog?library_id=${lib.id}&category=character&q=browse&page_size=100`,
    )
  ).items;
  const [a, b, c] = ["alpha", "beta", "gamma"].map((n) =>
    originals.find((t) => t.name === `browse ${n}`),
  );
  const chosen = characters.find((t) => t.name === "browse person 23");
  for (const person of characters)
    await write(
      `/parody-character-relations/${a.id}/${person.id}`,
      { expected_revision: 0, decision: "confirmed" },
      "PUT",
    );
  for (const original of [b, c, tag])
    await write(
      `/parody-character-relations/${original.id}/${chosen.id}`,
      { expected_revision: 0, decision: "confirmed" },
      "PUT",
    );
  const reads = [],
    errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    if (r.method() === "GET" && r.url().includes("/api/v1/tag-catalog/"))
      reads.push(new URL(r.url()));
  });
  const root = `/admin/tags?library_id=${lib.id}&category=parody&q=browse&sort=usage&order=desc`;
  await page.goto(root);
  const directory = page.getByRole("region", { name: "标签目录" }),
    modal = page.locator(".tag-detail-modal"),
    active = modal.locator(".tag-session:not([hidden])");
  await directory.getByRole("link", { name: a.name, exact: true }).click();
  const row = active.getByRole("region", { name: "关联角色卡片" });
  await expect(row.locator(".tag-catalog-card")).toHaveCount(24);
  await expect(
    modal.getByRole("button", { name: "返回上一个标签", exact: true }),
  ).toHaveCount(0);
  expect(new URL(page.url()).searchParams.get("category")).toBe("parody");
  await row.focus();
  await page.keyboard.press("End");
  await expect.poll(() => row.evaluate((n) => n.scrollLeft)).toBeGreaterThan(0);
  const boxes = await row
    .locator(".tag-catalog-card")
    .evaluateAll((ns) => ns.map((n) => n.getBoundingClientRect().top));
  expect(new Set(boxes).size).toBe(1);
  const input = active.getByRole("textbox", { name: "标签中文译名" });
  await input.fill("根标签草稿");
  const last = row.getByRole("link", { name: chosen.name, exact: true });
  await last.focus();
  const savedLeft = await row.evaluate((n) => n.scrollLeft);
  await last.click();
  await expect(
    modal.getByRole("heading", { name: chosen.name, exact: true }),
  ).toBeVisible();
  await expect(
    active.getByRole("textbox", { name: "搜索关联标签" }),
  ).toHaveCount(0);
  await expect(
    active.locator(".tag-parody-wrap .tag-catalog-card"),
  ).toHaveCount(4);
  expect(new URL(page.url()).searchParams.get("q")).toBe("browse");
  await active
    .locator(".tag-relation-cards")
    .getByRole("link", { name: b.name, exact: true })
    .click();
  await expect(
    modal.getByRole("heading", { name: b.name, exact: true }),
  ).toBeVisible();
  await active
    .getByRole("textbox", { name: "标签中文译名" })
    .fill("将移除的子标签草稿");
  await active
    .locator(".tag-relation-cards")
    .getByRole("link", { name: chosen.name, exact: true })
    .click();
  await expect(
    page.getByRole("dialog", { name: "有尚未保存的修改" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(
    modal.getByRole("heading", { name: b.name, exact: true }),
  ).toBeVisible();
  expect(
    (await page.evaluate(() => history.state.usr.tagTrail.nodes)).map(
      (n) => n.id,
    ),
  ).toEqual([a.id, chosen.id, b.id]);
  await active
    .locator(".tag-relation-cards")
    .getByRole("link", { name: chosen.name, exact: true })
    .click();
  await page
    .getByRole("button", { name: "放弃修改并离开", exact: true })
    .click();
  await expect(
    modal.getByRole("heading", { name: chosen.name, exact: true }),
  ).toBeVisible();
  await expect(modal.locator(".tag-session")).toHaveCount(2);
  await active
    .locator(".tag-relation-cards")
    .getByRole("link", { name: a.name, exact: true })
    .click();
  await expect(input).toHaveValue("根标签草稿");
  await expect(modal.locator(".tag-session")).toHaveCount(1);
  await expect(
    modal.getByRole("button", { name: "返回上一个标签", exact: true }),
  ).toHaveCount(0);
  await expect
    .poll(() => row.evaluate((n) => n.scrollLeft))
    .toBeCloseTo(savedLeft, 0);
  await active
    .getByRole("button", { name: "放弃译名修改", exact: true })
    .click();
  // Related visits replace, initial directory visit pushes. Browser Back skips
  // the whole related exploration and Forward restores its current trail.
  await page.goBack();
  await expect(modal).toBeHidden();
  expect(new URL(page.url()).searchParams.get("q")).toBe("browse");
  await page.goForward();
  await expect(modal).toBeVisible();
  await page.reload();
  await expect(
    modal.getByRole("heading", { name: a.name, exact: true }),
  ).toBeVisible();
  await active
    .locator(".tag-relation-cards")
    .getByRole("link", { name: chosen.name, exact: true })
    .click();
  await page.reload();
  await expect(
    modal.getByRole("button", { name: "返回上一个标签", exact: true }),
  ).toBeVisible();
  await modal
    .getByRole("button", { name: "返回上一个标签", exact: true })
    .click();
  await expect(
    modal.getByRole("heading", { name: a.name, exact: true }),
  ).toBeVisible();
  // Page two is a new row at its start; returning to page one restores its row.
  await active.locator(".tag-display-relations .ant-pagination-item-2").click();
  await expect(row.locator(".tag-catalog-card")).toHaveCount(3);
  await expect.poll(() => row.evaluate((n) => n.scrollLeft)).toBe(0);
  await active.locator(".tag-display-relations .ant-pagination-item-1").click();
  await expect(row.locator(".tag-catalog-card")).toHaveCount(24);
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/maintenance/tag-direct-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  for (const dark of [false, true]) {
    await page.emulateMedia({ colorScheme: dark ? "dark" : "light" });
    await expect(page.locator(".admin-shell")).toHaveClass(
      new RegExp(dark ? "dark" : "light"),
    );
    await expect(
      active.getByRole("textbox", { name: "标签中文译名" }),
    ).toHaveCSS(
      "background-color",
      dark ? "rgb(20, 20, 20)" : "rgb(255, 255, 255)",
    );
    await page.screenshot({
      path: path.join(
        shots,
        `${info.project.name}-relation-row-${dark ? "dark" : "light"}.png`,
      ),
      fullPage: true,
    });
  }
  if (info.project.name === "desktop") {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.screenshot({
      path: path.join(shots, "desktop-1440-relation-row-dark.png"),
      fullPage: true,
    });
  }
  // A legacy hidden search must not produce even one filtered display request.
  reads.length = 0;
  await page.goto(
    `${root}&tag=${chosen.id}&d_q=stale-hidden&d_page=7&d_sort=count&d_order=desc&display_snapshot=obsolete`,
  );
  await expect(
    active.locator(".tag-parody-wrap .tag-catalog-card"),
  ).toHaveCount(4);
  for (const key of ["d_q", "d_page", "d_sort", "d_order", "display_snapshot"])
    expect(new URL(page.url()).searchParams.has(key)).toBe(false);
  expect(
    reads
      .filter((u) => u.pathname.endsWith(`/${chosen.id}/relations`))
      .every(
        (u) => !u.searchParams.get("q") && u.searchParams.get("page") === "1",
      ),
  ).toBe(true);
  await active.locator(".tag-display-relations").scrollIntoViewIfNeeded();
  await page.screenshot({
    path: path.join(shots, `${info.project.name}-parody-wrap-dark.png`),
    fullPage: true,
  });
  const before = reads.length;
  await page.evaluate(() =>
    window.dispatchEvent(
      new CustomEvent("resources-changed", {
        detail: { type: "tag_relations" },
      }),
    ),
  );
  await expect.poll(() => reads.length).toBeGreaterThan(before);
  expect(
    reads.slice(before).some((u) => u.pathname.includes(`/${a.id}/`)),
  ).toBe(false);
  expect(errors).toEqual([]);
});
