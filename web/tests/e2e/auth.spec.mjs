import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";
test.describe.configure({ mode: "serial" });
const password = "Anonymous old password 2026";
const nextPassword = "Anonymous new password 2026";
function watch(page) {
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => {
    if (response.status() < 400) return;
    const path = new URL(response.url()).pathname;
    const expected =
      (response.status() === 401 &&
        ["/api/v1/auth/session", "/api/v1/auth/login"].includes(path)) ||
      (response.status() === 409 &&
        /^\/api\/v1\/books\/[^/]+\/overrides$/.test(path));
    if (!expected) errors.push(`HTTP ${response.status()} ${path}`);
  });
  page.on("requestfailed", (request) => {
    const error = request.failure()?.errorText ?? "request failed";
    if (!/ERR_ABORTED|AbortError/i.test(error))
      errors.push(`${error}: ${new URL(request.url()).pathname}`);
  });
  page.on("console", (message) => {
    // Browser network diagnostics are checked by response/requestfailed above.
    // CSP and application errors never pass through this exception.
    if (
      message.type() === "error" &&
      !message.text().startsWith("Failed to load resource:")
    )
      errors.push(message.text());
  });
  return errors;
}

async function nav(page, label) {
  const mobile = page.getByRole("button", { name: "打开管理导航" });
  if (await mobile.isVisible()) await mobile.click();
  await page.getByRole("menuitem", { name: label, exact: true }).last().click();
}
async function write(page, baseURL, url, method, data) {
  const session = await (
    await page.request.get(`${baseURL}/api/v1/auth/session`)
  ).json();
  return page.request.fetch(`${baseURL}/api/v1${url}`, {
    method,
    headers: { Origin: baseURL, "X-CSRF-Token": session.csrf_token },
    ...(data === undefined ? {} : { data }),
  });
}
async function complete(page) {
  await expect(page.getByText(/已处理 \d+ \/ \d+ 项/)).toBeVisible();
  await expect(async () => {
    const id = new URL(page.url()).pathname.split("/").at(-1);
    const j = await (await page.request.get(`/api/v1/jobs/${id}`)).json();
    expect(j.state).toBe("completed");
  }).toPass({ timeout: 20000 });
  await page.getByRole("button", { name: "刷新状态", exact: true }).click();
}
async function confirmOrganization(page) {
  await expect(
    page.getByRole("dialog", { name: "整理变更预览" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "确认保存整理", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "整理变更预览" })).toHaveCount(
    0,
  );
}
async function maintenance(page, operation) {
  const dialog = page.getByRole("dialog", { name: operation, exact: true });
  await dialog
    .getByRole("button", { name: "查看影响预览", exact: true })
    .click();
  await expect(dialog.getByText("影响摘要", { exact: true })).toBeVisible();
  if (operation === "永久清除应用记录")
    await dialog.getByRole("checkbox").check();
  await dialog
    .getByRole("button", { name: `确认执行${operation}`, exact: true })
    .click();
  await expect(page).toHaveURL(/\/admin\/jobs\//);
  await complete(page);
}
async function noOverflow(page) {
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
}

test("管理员初始化、登录失败、密码修改与会话撤销", async ({
  page,
  browser,
  baseURL,
}, info) => {
  const errors = watch(page);
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
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "初始化管理员", exact: true }),
  ).toBeVisible();
  await page.getByLabel("一次性初始化凭据", { exact: true }).fill(token);
  await page.getByLabel("管理员密码", { exact: true }).fill(password);
  await page.getByLabel("确认密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "创建管理员" }).click();
  await expect(
    page.getByRole("heading", { name: "漫画管理", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "漫画管理", exact: true }),
  ).toBeVisible();
  const other = await browser.newContext({ ...info.project.use, baseURL });
  const second = await other.newPage();
  await second.goto("/login");
  await second
    .getByLabel("管理员密码", { exact: true })
    .fill("incorrect anonymous password");
  await second.getByRole("button", { name: "登录", exact: true }).click();
  await expect(second.getByRole("alert")).toBeVisible();
  await second.getByLabel("管理员密码", { exact: true }).fill(password);
  await second.getByLabel("管理员密码", { exact: true }).press("Enter");
  await expect(
    second.getByRole("heading", { name: "漫画管理", exact: true }),
  ).toBeVisible();
  await nav(page, "账号安全");
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByLabel("新密码", { exact: true }).fill(nextPassword);
  await page.getByLabel("确认新密码", { exact: true }).fill(nextPassword);
  await page.getByRole("button", { name: "保存新密码" }).click();
  await expect(
    page.getByRole("heading", { name: "管理员登录", exact: true }),
  ).toBeVisible();
  await second.reload();
  await expect(
    second.getByRole("heading", { name: "管理员登录", exact: true }),
  ).toBeVisible();
  await other.close();
  await noOverflow(page);
  expect(errors).toEqual([]);
});

test("专业管理台：导入、来源、编辑冲突、封面、组织与回收站", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  const errors = watch(page);
  const authState = await (
    await page.request.get(`${baseURL}/api/v1/auth/status`)
  ).json();
  if (!authState.initialized) {
    const anonymousState = JSON.parse(
      await readFile(
        path.resolve(
          import.meta.dirname,
          `../../../.runtime/tests/e2e-state-${new URL(baseURL).port}.json`,
        ),
        "utf8",
      ),
    );
    const anonymousToken = await readFile(anonymousState.token_file, "utf8");
    const initialized = await page.request.post(
      `${baseURL}/api/v1/auth/setup`,
      {
        headers: { Origin: baseURL },
        data: { setup_token: anonymousToken, password: nextPassword },
      },
    );
    expect(initialized.status()).toBe(200);
    await page.context().clearCookies();
  }
  await page.goto("/login");
  await page.getByLabel("管理员密码", { exact: true }).fill(nextPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "漫画管理", exact: true }),
  ).toBeVisible();
  await nav(page, "书库");
  await page.getByRole("button", { name: "添加书库", exact: true }).click();
  const libraryModal = page.getByRole("dialog", {
    name: "添加书库",
    exact: true,
  });
  await libraryModal
    .getByLabel("书库名称", { exact: true })
    .fill("匿名测试书库");
  await libraryModal
    .getByRole("button", { name: "添加书库", exact: true })
    .click();
  await expect(libraryModal).toHaveCount(0);
  await page
    .getByRole("button", { name: "更多操作 匿名测试书库", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "高级维护", exact: true }).click();
  await page
    .getByRole("button", { name: "仅更新文件索引", exact: true })
    .click();
  await complete(page);
  await expect(page.getByText("归档损坏、加密或格式不受支持")).toBeVisible();
  await nav(page, "漫画管理");
  await expect(
    page.getByRole("button", { name: "查看漫画详情 合成1", exact: true }),
  ).toBeVisible();
  const all = await (await page.request.get(`${baseURL}/api/v1/books`)).json();
  const b1 = all.items.find((b) => b.title === "合成1");
  const b2 = all.items.find((b) => b.title === "合成2");
  const broken = all.items.find((b) => b.title === "损坏");
  expect(all.total).toBe(3);
  await page
    .getByRole("button", { name: "查看漫画详情 合成1", exact: true })
    .click();
  await page.getByRole("tab", { name: "来源匹配" }).click();
  await page.getByRole("button", { name: "重新匹配", exact: true }).click();
  await page.getByText("指定画廊", { exact: true }).click();
  await page
    .getByLabel("手动指定画廊网址")
    .fill("https://e-hentai.org/g/1/0123456789/");
  await page.getByRole("button", { name: "获取来源预览", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "确认采纳此来源", exact: true }),
  ).toBeVisible({ timeout: 20000 });
  await page
    .getByRole("button", { name: "确认采纳此来源", exact: true })
    .click();
  await expect(page.getByText("已采纳来源，人工编辑保持不变")).toBeVisible();
  await page.getByRole("tab", { name: "元数据" }).click();
  await page.getByLabel("其他标题", { exact: true }).fill("人工整理标题");
  const original = await (
    await page.request.get(`${baseURL}/api/v1/books/${b1.id}`)
  ).json();
  const changed = await write(
    page,
    baseURL,
    `/books/${b1.id}/overrides`,
    "PATCH",
    {
      expected_revision: original.revision,
      fields: { description: { op: "set", value: "另一窗口的编辑" } },
    },
  );
  expect(changed.status()).toBe(200);
  await page.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(page.getByText("检测到其他编辑，草稿已保留")).toBeVisible();
  await expect(page.getByLabel("其他标题", { exact: true })).toHaveValue(
    "人工整理标题",
  );
  await page
    .getByRole("button", { name: "核对后使用当前版本，保留草稿" })
    .click();
  await page.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(page.getByText("修改已保存", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "封面", exact: true }).click();
  await page.getByRole("button", { name: "加载图片", exact: true }).click();
  await page
    .getByRole("button", { name: "选择第 1 页封面", exact: true })
    .click();
  await expect(
    page.getByRole("img", { name: "待选封面", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "应用封面", exact: true }).click();
  await expect(page.getByText("已选择归档页作为封面")).toBeVisible();
  await page.getByText("人工上传", { exact: true }).last().click();
  // Use the anonymous fixture's real page bytes as a private test-only upload.
  const image = await page.request.get(
    `${baseURL}/api/v1/books/${b1.id}/pages/1/image?content_revision=${b1.content_revision}`,
  );
  expect(image.ok()).toBeTruthy();
  await page.getByLabel("上传人工封面", { exact: true }).setInputFiles({
    name: "anonymous-cover.png",
    mimeType: "image/png",
    buffer: await image.body(),
  });
  await page.getByRole("button", { name: "应用封面", exact: true }).click();
  await expect(page.getByText("人工封面已保存")).toBeVisible();
  await page.getByRole("button", { name: "恢复自动", exact: true }).click();
  await expect(page.getByText("已恢复自动封面")).toBeVisible();
  await page.getByRole("tab", { name: "元数据" }).click();
  await page.getByLabel("其他标题", { exact: true }).fill("未提交标题");
  await page
    .getByRole("dialog", { name: /漫画详情/ })
    .getByRole("button", { name: "关闭", exact: true })
    .first()
    .click();
  await expect(
    page.getByRole("dialog", { name: "放弃尚未保存的编辑？" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(page.getByLabel("其他标题", { exact: true })).toHaveValue(
    "未提交标题",
  );
  await page.getByRole("button", { name: "更多编辑操作", exact: true }).click();
  await page
    .getByRole("menuitem", { name: "放弃全部修改", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: /漫画详情/ })
    .getByRole("button", { name: "关闭", exact: true })
    .first()
    .click();
  await page
    .getByRole("checkbox", { name: "选择 人工整理标题", exact: true })
    .check();
  await page.getByRole("checkbox", { name: "选择 合成2", exact: true }).check();
  await page.getByRole("button", { name: "整理所选漫画", exact: true }).click();
  const bulk = page.getByRole("dialog", { name: /整理所选漫画/ });
  await bulk
    .getByRole("row")
    .filter({ hasText: "人工整理标题" })
    .getByRole("radio")
    .check();
  await bulk.getByRole("button", { name: "查看整理影响", exact: true }).click();
  await confirmOrganization(page);
  await page.getByRole("button", { name: "清除选择", exact: true }).click();
  await nav(page, "作品管理");
  await page.getByRole("button", { name: "人工整理标题", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "拆为独立作品" }),
  ).toBeVisible();
  await page
    .getByRole("row")
    .filter({ hasText: "人工整理标题" })
    .getByRole("button", { name: "编辑标签", exact: true })
    .click();
  await page.getByLabel("人工整理标题版本标签", { exact: true }).fill("修订版");
  await page.getByLabel("人工整理标题版本标签", { exact: true }).press("Enter");
  await page
    .getByLabel("人工整理标题版本标签", { exact: true })
    .press("Escape");
  await page.getByRole("button", { name: "查看标签影响", exact: true }).click();
  await confirmOrganization(page);
  await page
    .getByRole("button", { name: "设为首选版本", exact: true })
    .and(page.locator(":enabled"))
    .last()
    .click();
  await confirmOrganization(page);
  await nav(page, "系列管理");
  await page.getByRole("button", { name: "创建系列", exact: true }).click();
  await page.getByLabel("新系列名称", { exact: true }).fill("匿名系列");
  await page.getByLabel("新系列书库", { exact: true }).click();
  await page.getByRole("option", { name: "匿名测试书库", exact: true }).click();
  await page.getByRole("button", { name: "查看创建预览" }).click();
  await confirmOrganization(page);
  await page.getByRole("button", { name: "匿名系列", exact: true }).click();
  await page.getByRole("button", { name: "添加成员", exact: true }).click();
  const add = page.getByRole("dialog", { name: "向系列添加作品" });
  await add
    .getByRole("row")
    .filter({ hasText: "合成2" })
    .getByRole("checkbox")
    .check();
  await add
    .getByRole("row")
    .filter({ hasText: "损坏" })
    .getByRole("checkbox")
    .check();
  await add.getByRole("button", { name: "查看整理影响" }).click();
  await confirmOrganization(page);
  await page
    .getByRole("button", { name: /^下移 / })
    .first()
    .click();
  await confirmOrganization(page);
  await page
    .getByRole("button", { name: "按标题自然排序", exact: true })
    .click();
  await confirmOrganization(page);
  await page.getByLabel("系列名称", { exact: true }).fill("");
  await expect(page.getByLabel("系列名称", { exact: true })).toHaveValue("");
  await expect(
    page.getByRole("button", { name: "保存名称", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "放弃名称修改" }).click();
  await page.getByRole("button", { name: "删除系列", exact: true }).click();
  await confirmOrganization(page);
  await nav(page, "漫画管理");
  await page.getByLabel("搜索标题或标签", { exact: true }).fill("人工整理");
  await page.getByLabel("搜索标题或标签", { exact: true }).press("Enter");
  await expect(
    page.getByRole("status").filter({ hasText: "1 本漫画" }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("搜索标题或标签", { exact: true })).toHaveValue(
    "人工整理",
  );
  await page.getByRole("button", { name: "已保存的搜索", exact: true }).click();
  await page.getByLabel("搜索名称", { exact: true }).fill("人工整理清单");
  await page.getByRole("button", { name: "保存当前搜索", exact: true }).click();
  await expect(page.getByText("搜索已保存", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.goto(`/admin/books?book=${b1.id}`);
  await page.getByRole("button", { name: "移入回收站", exact: true }).click();
  await maintenance(page, "移入回收站");
  await nav(page, "维护中心");
  await page.getByRole("button", { name: "恢复", exact: true }).click();
  await maintenance(page, "恢复索引");
  await nav(page, "书库");
  await page
    .getByRole("button", { name: "更多操作 匿名测试书库", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "停用书库", exact: true }).click();
  await expect(page.getByText("已停用", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "导入漫画", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "更多操作 匿名测试书库", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "启用书库", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "导入漫画", exact: true }),
  ).toBeEnabled();
  await nav(page, "站点与译库");
  await page.getByLabel("默认检索站点", { exact: true }).click();
  await page.getByRole("option", { name: "ExHentai", exact: true }).click();
  await page
    .getByLabel("代理出口", { exact: true })
    .fill("socks5://127.0.0.1:7891");
  await page
    .getByRole("button", { name: "保存站点与网络", exact: true })
    .click();
  await page.getByRole("tab", { name: "凭据与认证" }).click();
  await page.getByLabel("Cookie：ipb_pass_hash操作", { exact: true }).click();
  await page.getByRole("option", { name: "替换", exact: true }).click();
  await page
    .getByLabel("Cookie：ipb_pass_hash", { exact: true })
    .fill("anonymous-browser-secret");
  await page.getByRole("button", { name: "保存凭据", exact: true }).click();
  await expect(
    page.getByLabel("Cookie：ipb_pass_hash", { exact: true }),
  ).toHaveCount(0);
  expect(
    await (
      await page.request.get(`${baseURL}/api/v1/settings/metadata-provider`)
    ).text(),
  ).not.toContain("anonymous-browser-secret");
  await page.getByRole("tab", { name: "中文标签译库" }).click();
  await page
    .getByRole("button", { name: "在线更新中文译库", exact: true })
    .click();
  await complete(page);
  await nav(page, "站点与译库");
  await page.getByRole("tab", { name: "中文标签译库" }).click();
  await expect(page.getByText("anonymous-v1", { exact: true })).toBeVisible();
  await nav(page, "漫画管理");
  await noOverflow(page);
  await settledAdminPage(page, "漫画管理");
  await page.screenshot({
    path: info.outputPath("admin-books.png"),
    fullPage: true,
  });
  await nav(page, "任务中心");
  await settledAdminPage(page, "任务中心");
  await page.screenshot({
    path: info.outputPath("admin-tasks.png"),
    fullPage: true,
  });
  await noOverflow(page);
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "管理员登录", exact: true }),
  ).toBeVisible();
  expect(errors).toEqual([]);
  expect(b2).toBeTruthy();
  expect(broken).toBeTruthy();
});

async function settledAdminPage(page, title) {
  await expect(
    page.getByRole("heading", { name: title, exact: true }),
  ).toBeVisible();
  await expect(page.locator(".ant-drawer-mask")).toHaveCount(0);
  await expect(
    page.locator(".desktop-sidebar .ant-menu-item-selected"),
  ).toContainText(title);
}
