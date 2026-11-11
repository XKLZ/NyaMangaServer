import { test, expect } from "@playwright/test";
import { mkdir, unlink } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { instanceFixture } from "./instance-fixture.mjs";
installViteSource(test);
test("结果不明按原身份找回、坏包与中断放弃、映射冲突和新编辑拒绝回退", async ({
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
    const posts = [];
    page.on("request", (r) => {
      if (r.method() === "POST") posts.push(new URL(r.url()).pathname);
    });
    // A failed browser connection has not reached the server yet; the old identity may arrive later.
    let oldKey;
    await page.route("**/api/v1/maintenance/backups", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      oldKey = route.request().postDataJSON().request_key;
      await route.abort("failed");
    });
    await area.getByRole("button", { name: "创建备份", exact: true }).click();
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toBeVisible();
    await page.unroute("**/api/v1/maintenance/backups");
    await area
      .getByRole("button", { name: "查询本次操作", exact: true })
      .click();
    await expect(area.getByText(/此刻尚未查到操作/)).toBeVisible();
    expect(
      posts.filter((v) => v === "/api/v1/maintenance/backups"),
    ).toHaveLength(1);
    await page.reload();
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toBeVisible();
    const late = await fixture.write(
      "/maintenance/backups",
      { request_key: oldKey },
      "POST",
      202,
    );
    await area
      .getByRole("button", { name: "查询本次操作", exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`operation=${late.id}`));
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/backups/${late.id}`)).state,
        { timeout: 30000 },
      )
      .toBe("completed");
    const d = page.waitForEvent("download");
    await area
      .getByRole("button", { name: "下载完整备份", exact: true })
      .click();
    const archive = path.join(fixture.temp, "anonymous-retained.zip");
    await (await d).saveAs(archive);
    // The authenticated response is lost after the actual upload has been saved.
    let uploadKey;
    await page.route("**/api/v1/maintenance/restores", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      uploadKey = route.request().headers()["idempotency-key"];
      await route.fetch();
      await route.abort("failed");
    });
    await area.locator('input[type="file"]').setInputFiles({
      name: "invalid.zip",
      mimeType: "application/zip",
      buffer: Buffer.from("anonymous invalid archive"),
    });
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toBeVisible();
    await page.unroute("**/api/v1/maintenance/restores");
    await area
      .getByRole("button", { name: "查询本次操作", exact: true })
      .click();
    const invalid = (
      await fixture.get(
        `/maintenance/restores?request_key=${encodeURIComponent(uploadKey)}&page=1`,
      )
    )[0];
    await expect(page).toHaveURL(new RegExp(`operation=${invalid.id}`));
    await area.getByRole("button", { name: "校验备份包", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${invalid.id}`)).operation
            .state,
      )
      .toBe("failed");
    await expect(
      area.getByText(/备份包损坏、内容不完整或超出限制/),
    ).toBeVisible();
    await area.getByRole("button", { name: "放弃此恢复", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${invalid.id}`)).operation
            .state,
      )
      .toBe("abandoned");
    // Missing historical archive is an inline error, never a silent partial download.
    await unlink(
      path.join(
        fixture.directory,
        "data/instance-recovery",
        late.id,
        "archive.zip",
      ),
    );
    await page.goto(
      `/admin/maintenance?section=recovery&kind=backups&operation=${late.id}`,
    );
    await area
      .getByRole("button", { name: "下载完整备份", exact: true })
      .click();
    await expect(
      area.getByText(/备份记录存在，但完整包已不可用/),
    ).toBeVisible();
    await area.locator('input[type="file"]').setInputFiles(archive);
    await expect(page).toHaveURL(/kind=restores/);
    const id = new URL(page.url()).searchParams.get("operation");
    await area.getByRole("button", { name: "校验备份包", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${id}`)).operation.state,
      )
      .toBe("validated");
    const mapping = area.getByRole("combobox", {
      name: "匿名整实例书库的目标目录",
      exact: true,
    });
    await mapping.focus();
    await page.keyboard.press("ArrowDown");
    await page
      .getByRole("option", { name: "匿名整实例样本", exact: true })
      .click();
    await area.getByRole("checkbox", { name: /我已核对关联目录/ }).focus();
    await page.keyboard.press("Space");
    await area
      .getByRole("button", { name: "预览替换影响", exact: true })
      .click();
    await expect(
      area.getByRole("button", { name: "安排整实例替换", exact: true }),
    ).toBeVisible();
    const book = await fixture.get("/books/fixture-edition-0");
    await fixture.write(
      "/books/fixture-edition-0/overrides",
      {
        expected_revision: book.revision,
        fields: {
          title: { op: "set", value: "Anonymous target changed after preview" },
        },
      },
      "PATCH",
      200,
    );
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
    await area
      .getByRole("button", { name: "安排整实例替换", exact: true })
      .click();
    const confirm = page.getByRole("dialog", {
      name: "确认整实例替换",
      exact: true,
    });
    await confirm
      .getByRole("button", { name: "确认替换并等待重启", exact: true })
      .click();
    await expect(
      confirm.getByRole("button", { name: "确认替换并等待重启", exact: true }),
    ).toBeDisabled();
    await expect(
      confirm.getByText("目标已变化，请返回映射区重新核对并预览。", {
        exact: true,
      }),
    ).toBeVisible();
    await confirm
      .getByRole("button", { name: "返回核对", exact: true })
      .click();
    await expect(
      area.getByText("匿名整实例样本", { exact: true }),
    ).toBeVisible();
    await page.reload();
    await expect(
      area.getByText("匿名整实例样本", { exact: true }),
    ).toBeVisible();
    await area
      .getByRole("button", { name: "重新核对当前版本", exact: true })
      .click();
    await area
      .getByRole("button", { name: "预览替换影响", exact: true })
      .click();
    await area
      .getByRole("button", { name: "安排整实例替换", exact: true })
      .click();
    await confirm
      .getByRole("button", { name: "确认替换并等待重启", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${id}`)).operation.state,
      )
      .toBe("awaiting_restart");
    await expect(
      area.getByText("已准备，尚未恢复", { exact: true }),
    ).toBeVisible();
    await expect(confirm).not.toBeVisible();
    await page.evaluate(() => window.scrollTo(0, 0));
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
    await page.screenshot({
      path: path.join(screenshots, `${info.project.name}-awaiting-restart.png`),
      animations: "disabled",
    });
    await page.goto("/admin/books");
    await page
      .getByRole("button", { name: "查看备份与恢复", exact: true })
      .click();
    await expect(page).toHaveURL(/section=recovery/);
    await area
      .getByRole("button", { name: "查看当前操作", exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`operation=${id}`));
    await fixture.stop();
    await fixture.start();
    await page.reload();
    await expect(page).toHaveURL(/\/login/);
    await page
      .getByLabel("管理员密码", { exact: true })
      .fill("anonymous recovery password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(
      area.getByLabel("重启激活结果").getByText("恢复已激活", { exact: true }),
    ).toBeVisible();
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: path.join(screenshots, `${info.project.name}-activated.png`),
      animations: "disabled",
    });
    const restored = await fixture.get("/books/fixture-edition-0");
    await fixture.write(
      "/books/fixture-edition-0/overrides",
      {
        expected_revision: restored.revision,
        fields: { title: { op: "set", value: "Anonymous new business edit" } },
      },
      "PATCH",
      200,
    );
    await area
      .getByRole("button", { name: "核对并安排回退", exact: true })
      .click();
    const rollback = page.getByRole("dialog", {
      name: "确认安排整实例回退",
      exact: true,
    });
    await rollback
      .getByRole("button", { name: "确认回退并等待重启", exact: true })
      .click();
    await expect(
      rollback.getByRole("button", { name: "确认回退并等待重启", exact: true }),
    ).toBeDisabled();
    expect((await fixture.get("/maintenance/instance-activation")).state).toBe(
      "completed",
    );
    expect(
      (await fixture.get("/books/fixture-edition-0")).metadata.fields.title,
    ).toBe("Anonymous new business edit");
    await page.screenshot({
      path: path.join(
        screenshots,
        `${info.project.name}-new-edit-rejects-rollback.png`,
      ),
      animations: "disabled",
    });
    await rollback.getByRole("button", { name: "返回", exact: true }).click();
    expect(
      posts.filter((v) => v.endsWith("/instance-state/release")),
    ).toHaveLength(0);
    await area
      .getByRole("button", { name: "恢复后台自动处理", exact: true })
      .click();
    await page
      .getByRole("dialog", { name: "恢复后台自动处理", exact: true })
      .getByRole("button", { name: "确认恢复后台调度", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await fixture.get("/maintenance/instance-state")).background_hold,
      )
      .toBe(false);
    expect((await fixture.get("/jobs/fixture-paused")).state).toBe("paused");
  } finally {
    await fixture.stop();
  }
});
