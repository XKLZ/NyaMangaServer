import { test, expect } from "@playwright/test";
import { installViteSource } from "./vite-source.mjs";
import { instanceFixture } from "./instance-fixture.mjs";
installViteSource(test);
test("鉴权和CSRF前置拒绝不遗留待查身份，重新登录或刷新后可明确重发", async ({
  page,
  baseURL,
}) => {
  test.setTimeout(90000);
  const fixture = await instanceFixture(page, baseURL);
  try {
    await fixture.login();
    await page.goto("/admin/maintenance?section=recovery");
    const area = page.getByRole("region", { name: "备份与恢复", exact: true });
    await expect(
      area.getByRole("button", { name: "创建备份", exact: true }),
    ).toBeEnabled();
    // A real anonymous unauthenticated response, not a fabricated failure body.
    await page.context().clearCookies();
    await area.getByRole("button", { name: "创建备份", exact: true }).click();
    await expect(page).toHaveURL(/\/login/);
    await page
      .getByLabel("管理员密码", { exact: true })
      .fill("anonymous recovery password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page).toHaveURL(/section=recovery/);
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toHaveCount(0);
    await expect(
      area.getByRole("button", { name: "创建备份", exact: true }),
    ).toBeEnabled();
    let deniedKey;
    await page.route("**/api/v1/maintenance/backups", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      deniedKey = route.request().postDataJSON().request_key;
      await route.continue({
        headers: {
          ...route.request().headers(),
          "x-csrf-token": "anonymous-invalid-csrf",
        },
      });
    });
    await area.getByRole("button", { name: "创建备份", exact: true }).click();
    await expect(
      area.getByText("请求验证失效，请刷新页面", { exact: true }),
    ).toBeVisible();
    expect(
      await fixture.get(`/maintenance/backups?request_key=${deniedKey}&page=1`),
    ).toEqual([]);
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toHaveCount(0);
    await page.unroute("**/api/v1/maintenance/backups");
    await page.reload();
    await area.getByRole("button", { name: "创建备份", exact: true }).click();
    await expect(page).toHaveURL(/operation=/);
    const backup = new URL(page.url()).searchParams.get("operation");
    await expect
      .poll(
        async () => (await fixture.get(`/maintenance/backups/${backup}`)).state,
        { timeout: 30000 },
      )
      .toBe("completed");
    let originKey;
    await page.route("**/api/v1/maintenance/restores", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      originKey = route.request().headers()["idempotency-key"];
      // Chromium rewrites forbidden Origin on continue; API forwarding preserves this real invalid header.
      const response = await route.fetch({
        headers: {
          ...route.request().headers(),
          origin: "http://anonymous-invalid-origin.invalid",
        },
      });
      await route.fulfill({ response });
    });
    const zip = {
      name: "anonymous.zip",
      mimeType: "application/zip",
      buffer: Buffer.from("anonymous validation-only bytes"),
    };
    await area.locator('input[type="file"]').setInputFiles(zip);
    await expect(
      area.getByText("请求来源不允许", { exact: true }),
    ).toBeVisible();
    expect(
      await fixture.get(
        `/maintenance/restores?request_key=${originKey}&page=1`,
      ),
    ).toEqual([]);
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toHaveCount(0);
    await page.unroute("**/api/v1/maintenance/restores");
    await page.reload();
    await area.locator('input[type="file"]').setInputFiles(zip);
    await expect(page).toHaveURL(/kind=restores/);
    const restore = new URL(page.url()).searchParams.get("operation");
    expect(
      (await fixture.get(`/maintenance/restores/${restore}`)).operation.state,
    ).toBe("uploaded");
    await area.getByRole("button", { name: "放弃此恢复", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${restore}`)).operation
            .state,
      )
      .toBe("abandoned");
  } finally {
    await fixture.stop();
  }
});
