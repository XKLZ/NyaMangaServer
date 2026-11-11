import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { instanceFixture } from "./instance-fixture.mjs";
installViteSource(test);
test("整实例备份、上传映射、正常重启激活、重登与受控回退", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  const fixture = await instanceFixture(page, baseURL);
  const screenshots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/tests/instance-ui/screenshots",
  );
  await mkdir(screenshots, { recursive: true });
  try {
    await fixture.login();
    await page.goto("/admin/maintenance?section=recovery");
    const area = page.getByRole("region", { name: "备份与恢复", exact: true });
    await expect(
      area.getByRole("button", { name: "创建备份", exact: true }),
    ).toBeEnabled();
    await area.getByRole("button", { name: "创建备份", exact: true }).click();
    await expect(page).toHaveURL(/operation=/);
    const backupID = new URL(page.url()).searchParams.get("operation");
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/backups/${backupID}`)).state,
        { timeout: 30000 },
      )
      .toBe("completed");
    const download = page.waitForEvent("download");
    await area
      .getByRole("button", { name: "下载完整备份", exact: true })
      .click();
    const archive = path.join(fixture.temp, "anonymous-instance.zip");
    await (await download).saveAs(archive);
    expect(
      (await fixture.get("/maintenance/instance-state")).background_hold,
    ).toBe(true);
    const release = area.getByRole("button", {
      name: "恢复后台自动处理",
      exact: true,
    });
    await expect(release).toBeVisible();
    await area.locator('input[type="file"]').setInputFiles(archive);
    await expect(page).toHaveURL(/kind=restores/);
    const restoreID = new URL(page.url()).searchParams.get("operation");
    await area.getByRole("button", { name: "校验备份包", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${restoreID}`)).operation
            .state,
        { timeout: 30000 },
      )
      .toBe("validated");
    const mapping = area.getByRole("combobox", {
      name: "匿名整实例书库的目标目录",
      exact: true,
    });
    await expect(mapping).toBeVisible();
    await expect(
      area.getByText("暂不关联漫画目录", { exact: true }),
    ).toBeVisible();
    await area.getByRole("checkbox", { name: /我已核对关联目录/ }).check();
    await area
      .getByRole("button", { name: "刷新备份与恢复", exact: true })
      .click();
    await expect(
      area.getByRole("checkbox", { name: /我已核对关联目录/ }),
    ).toBeChecked();
    await page.reload();
    await expect(
      area.getByRole("checkbox", { name: /我已核对关联目录/ }),
    ).toBeChecked();
    await area
      .getByRole("button", { name: "预览替换影响", exact: true })
      .click();
    await expect(
      area.getByRole("button", { name: "安排整实例替换", exact: true }),
    ).toBeVisible();
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
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth),
        ).toBe(width);
        await page.screenshot({
          path: path.join(
            screenshots,
            `${info.project.name}-mapping-${width}-${colorScheme}.png`,
          ),
          animations: "disabled",
        });
      }
    }
    const confirmations = [];
    page.on("request", (r) => {
      if (r.method() === "POST" && r.url().endsWith("/confirm"))
        confirmations.push(r.postDataJSON());
    });
    await area
      .getByRole("button", { name: "安排整实例替换", exact: true })
      .click();
    const confirm = page.getByRole("dialog", {
      name: "确认整实例替换",
      exact: true,
    });
    await expect(confirm).toContainText("不合并资料");
    await confirm
      .getByRole("button", { name: "确认替换并等待重启", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${restoreID}`)).operation
            .state,
        { timeout: 30000 },
      )
      .toBe("awaiting_restart");
    expect(confirmations).toHaveLength(1);
    await expect(
      area.getByText("已准备，尚未恢复", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("整实例维护保护中", { exact: true }),
    ).toBeVisible();
    await fixture.stop();
    await fixture.start();
    await area
      .getByRole("button", { name: "刷新备份与恢复", exact: true })
      .click();
    await expect(page).toHaveURL(/\/login/);
    await page
      .getByLabel("管理员密码", { exact: true })
      .fill("anonymous recovery password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`operation=${restoreID}`));
    await expect(
      area.getByLabel("重启激活结果").getByText("恢复已激活", { exact: true }),
    ).toBeVisible();
    const activation = await fixture.get("/maintenance/instance-activation");
    expect(activation.state).toBe("completed");
    const detail = await fixture.get(`/maintenance/restores/${restoreID}`);
    expect(detail.operation.state).toBe("completed");
    expect(
      (await fixture.get("/maintenance/instance-state")).background_hold,
    ).toBe(true);
    await area
      .getByRole("button", { name: "核对并安排回退", exact: true })
      .click();
    await page
      .getByRole("dialog", { name: "确认安排整实例回退", exact: true })
      .getByRole("button", { name: "确认回退并等待重启", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await fixture.get("/maintenance/instance-activation")).state,
      )
      .toBe("rollback_requested");
    await fixture.stop();
    await fixture.start();
    await page.reload();
    await expect(page).toHaveURL(/\/login/);
    await page
      .getByLabel("管理员密码", { exact: true })
      .fill("anonymous recovery password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(
      area.getByLabel("重启激活结果").getByText("已回退", { exact: true }),
    ).toBeVisible();
  } finally {
    await fixture.stop();
  }
});
