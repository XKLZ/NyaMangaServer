import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";
async function nav(page, label) {
  const mobile = page.getByRole("button", { name: "打开管理导航" });
  if (await mobile.isVisible()) await mobile.click();
  await page.getByRole("menuitem", { name: label, exact: true }).last().click();
}
async function confirm(page) {
  const preview = page.getByRole("dialog", {
    name: "整理变更预览",
    exact: true,
  });
  await expect(preview).toBeVisible();
  await preview
    .getByRole("button", { name: "确认保存整理", exact: true })
    .click();
  await expect(preview).toHaveCount(0);
}
test("Work管理：合并、首选、拆分移归、系列、冲突与旧链接", async ({
  page,
  baseURL,
}) => {
  test.setTimeout(90000);
  const state = JSON.parse(
    await readFile(
      path.resolve(
        import.meta.dirname,
        `../../../.runtime/tests/e2e-state-${new URL(baseURL).port}.json`,
      ),
      "utf8",
    ),
  );
  const token = await readFile(state.token_file, "utf8");
  const initialized = (
    await (await page.request.get(`${baseURL}/api/v1/auth/status`)).json()
  ).initialized;
  const setup = await page.request.post(
    `${baseURL}/api/v1/auth/${initialized ? "login" : "setup"}`,
    {
      headers: { Origin: baseURL },
      data: {
        ...(initialized ? {} : { setup_token: token }),
        password: "Anonymous new password 2026",
      },
    },
  );
  expect(setup.status()).toBe(200);
  const csrf = (await setup.json()).csrf_token;
  async function write(url, method, data, status = 200) {
    const r = await page.request.fetch(`${baseURL}/api/v1${url}`, {
      method,
      headers: { Origin: baseURL, "X-CSRF-Token": csrf },
      data,
    });
    expect(r.status(), `${method} ${url}`).toBe(status);
    return r.json();
  }
  const library = await write(
    "/libraries",
    "POST",
    { root_key: "anonymous-work", display_name: "匿名Work书库" },
    201,
  );
  const scan = await write(`/libraries/${library.id}/scans`, "POST", {}, 202);
  await expect(async () =>
    expect(
      (await (await page.request.get(`/api/v1/jobs/${scan.job_id}`)).json())
        .state,
    ).toBe("completed"),
  ).toPass();
  const books = (
    await (
      await page.request.get(`/api/v1/editions?library_id=${library.id}`)
    ).json()
  ).items;
  const b1 = books.find((b) => b.title === "合成1"),
    b2 = books.find((b) => b.title === "合成2");
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (
      m.type() === "error" &&
      !m.text().startsWith("Failed to load resource:")
    )
      errors.push(m.text());
  });
  page.on("response", (r) => {
    const p = new URL(r.url()).pathname;
    if (
      r.status() >= 400 &&
      !(r.status() === 409 && p === "/api/v1/work-organization/preview")
    )
      errors.push(`${r.status()} ${p}`);
  });
  await page.goto(`/admin/works/${b1.work_id}`);
  await page.getByLabel("作品名称", { exact: true }).fill("匿名作品");
  await page.getByRole("button", { name: "保存名称", exact: true }).click();
  await confirm(page);
  await page.getByRole("button", { name: "合并其他作品", exact: true }).click();
  let drawer = page.getByRole("dialog", { name: "整理作品关系", exact: true });
  await drawer
    .getByRole("row")
    .filter({ hasText: "合成2" })
    .getByRole("checkbox")
    .check();
  await drawer
    .getByRole("button", { name: "查看整理影响", exact: true })
    .click();
  await confirm(page);
  await page
    .getByRole("row")
    .filter({ hasText: "合成2" })
    .getByRole("button", { name: "设为首选版本", exact: true })
    .click();
  await confirm(page);
  await page
    .getByRole("row")
    .filter({ hasText: "合成1" })
    .getByRole("button", { name: "编辑标签", exact: true })
    .click();
  await page.getByLabel("合成1版本标签", { exact: true }).fill("中文");
  await page.getByLabel("合成1版本标签", { exact: true }).press("Enter");
  await page.getByLabel("合成1版本标签", { exact: true }).press("Escape");
  await page.getByRole("button", { name: "查看标签影响", exact: true }).click();
  await confirm(page);
  await nav(page, "系列管理");
  await page.getByRole("button", { name: "创建系列", exact: true }).click();
  await page.getByLabel("新系列名称", { exact: true }).fill("匿名系列");
  await page.getByLabel("新系列书库", { exact: true }).click();
  await page.getByRole("option", { name: "匿名Work书库", exact: true }).click();
  await page.getByRole("button", { name: "查看创建预览", exact: true }).click();
  await confirm(page);
  const seriesPath = new URL(page.url()).pathname;
  await page.getByRole("button", { name: "添加成员", exact: true }).click();
  drawer = page.getByRole("dialog", { name: "向系列添加作品", exact: true });
  for (const name of ["匿名作品", "损坏"])
    await drawer
      .getByRole("row")
      .filter({ hasText: name })
      .getByRole("checkbox")
      .check();
  await drawer
    .getByRole("button", { name: "查看整理影响", exact: true })
    .click();
  await confirm(page);
  await page
    .getByRole("button", { name: /^下移 / })
    .first()
    .press("Enter");
  await confirm(page);
  await page
    .getByRole("button", { name: "按标题自然排序", exact: true })
    .click();
  await confirm(page);
  await page.goto(`/admin/works/${b1.work_id}`);
  await page.getByRole("button", { name: "拆分作品", exact: true }).click();
  drawer = page.getByRole("dialog", { name: "整理作品关系", exact: true });
  await drawer
    .getByRole("row")
    .filter({ hasText: "合成2" })
    .getByRole("checkbox")
    .check();
  await expect(
    drawer.getByRole("button", { name: "查看整理影响", exact: true }),
  ).toBeDisabled();
  await drawer.getByLabel("剩余作品首选版本", { exact: true }).click();
  await page.getByRole("option", { name: "合成1", exact: true }).click();
  await expect(
    drawer.getByRole("checkbox", { name: /保留原系列/ }),
  ).not.toBeChecked();
  await drawer
    .getByRole("button", { name: "查看整理影响", exact: true })
    .click();
  await confirm(page);
  const one = await (
    await page.request.get(`/api/v1/works/${b1.work_id}`)
  ).json();
  expect(one.edition_count).toBe(1);
  expect(one.preferred_edition_id).toBe(b1.id);
  expect(one.series_id).toBe(seriesPath.split("/").at(-1));
  const splitBook = await (
    await page.request.get(`/api/v1/editions/${b2.id}`)
  ).json();
  expect(splitBook.work_id).not.toBe(b1.work_id);
  expect(
    (
      await (
        await page.request.get(`/api/v1/works/${splitBook.work_id}`)
      ).json()
    ).series_id,
  ).toBeUndefined();
  await page.goto(`/editions/${b2.id}`);
  await expect(page).toHaveURL(new RegExp(`book=${b2.id}`));
  await page.getByRole("tab", { name: "组织", exact: true }).click();
  await page.getByRole("button", { name: "移归其他作品", exact: true }).click();
  await page
    .getByRole("row")
    .filter({ hasText: "匿名作品" })
    .getByRole("radio")
    .check();
  await page.getByRole("button", { name: "查看整理影响", exact: true }).click();
  await confirm(page);
  const moved = await (
    await page.request.get(`/api/v1/editions/${b2.id}`)
  ).json();
  expect(moved.work_id).toBe(b1.work_id);
  expect(moved.organization.series[0].id).toBe(one.series_id);
  await page.goto("/admin/version-groups/old-group-id");
  await expect(page).toHaveURL(/\/admin\/works\?model_migrated=1/);
  await expect(page.getByText(/版本组已统一为作品/)).toBeVisible();
  await page.goto(`/admin/works/${b1.work_id}`);
  await page.getByLabel("作品名称", { exact: true }).fill("保留的名称草稿");
  const current = await (
    await page.request.get(`/api/v1/works/${b1.work_id}`)
  ).json();
  await write(`/works/${b1.work_id}`, "PATCH", {
    name_op: "set",
    name: "其他窗口名称",
    expected_revisions: { [`work:${b1.work_id}`]: current.revision },
  });
  await page.getByRole("button", { name: "保存名称", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "核对当前版本并重新预览", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("作品名称", { exact: true })).toHaveValue(
    "保留的名称草稿",
  );
  await page
    .getByRole("button", { name: "核对当前版本并重新预览", exact: true })
    .click();
  await confirm(page);
  await page.goto("/admin/works?query=保留的名称草稿");
  await page
    .getByRole("button", { name: "保留的名称草稿", exact: true })
    .click();
  await page.getByRole("button", { name: "返回列表", exact: true }).click();
  await expect(page).toHaveURL(/query=/);
  await expect(page.getByLabel("搜索组织名称", { exact: true })).toHaveValue(
    "保留的名称草稿",
  );
  await page.goto(`/books/${b1.id}`);
  await expect(page.getByRole("dialog", { name: /漫画详情/ })).toBeVisible();
  const after = await (
    await page.request.get(`/api/v1/editions/${b1.id}`)
  ).json();
  expect(
    after.organization.editions.find((e) => e.id === b1.id).version_labels,
  ).toEqual(["中文"]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBeTruthy();
  expect(errors).toEqual([]);
});
