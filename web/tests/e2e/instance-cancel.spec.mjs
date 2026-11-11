import { test, expect } from "@playwright/test";
import http from "node:http";
import { installViteSource } from "./vite-source.mjs";
import { instanceFixture } from "./instance-fixture.mjs";
installViteSource(test);
test("旧23不请求新接口、停止上传按原身份放弃、迟到CAS不复活及离页停止读取", async ({
  page,
  baseURL,
}) => {
  test.setTimeout(90000);
  const fixture = await instanceFixture(page, baseURL);
  let incoming;
  try {
    const reads = [];
    page.on("request", (r) => {
      if (r.method() === "GET") reads.push(new URL(r.url()).pathname);
    });
    await page.route("**/api/v1/settings/service", async (route) => {
      const response = await route.fetch(),
        json = await response.json();
      json.database_schema = 23;
      await route.fulfill({ response, json });
    });
    await fixture.login();
    await page.goto("/admin/maintenance?section=recovery");
    const area = page.getByRole("region", { name: "备份与恢复", exact: true });
    await expect(
      area.getByText(
        "当前服务尚未支持整实例备份与恢复，请升级至schema24后刷新。",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      area.getByRole("button", { name: "创建备份", exact: true }),
    ).toBeDisabled();
    expect(
      reads.filter((v) => /maintenance\/(backups|restores|instance-)/.test(v)),
    ).toEqual([]);
    const book = await fixture.get("/books/fixture-edition-0");
    await fixture.write(
      "/books/fixture-edition-0/overrides",
      {
        expected_revision: book.revision,
        fields: {
          title: { op: "set", value: "Anonymous schema23 compatibility" },
        },
      },
      "PATCH",
      200,
    );
    await page.unroute("**/api/v1/settings/service");
    await area
      .getByRole("button", { name: "刷新备份与恢复", exact: true })
      .click();
    await expect(
      area.getByRole("button", { name: "上传备份包", exact: true }),
    ).toBeEnabled();
    let key, release;
    await page.route("**/api/v1/maintenance/restores", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      key = route.request().headers()["idempotency-key"];
      await new Promise((resolve) => (release = resolve));
      await route.abort("failed").catch(() => {});
    });
    await area.locator('input[type="file"]').setInputFiles({
      name: "cancelled.zip",
      mimeType: "application/zip",
      buffer: Buffer.from("anonymous incoming archive"),
    });
    await area
      .getByRole("button", { name: "停止上传并核对状态", exact: true })
      .click();
    release();
    await expect(
      area.getByRole("button", { name: "查询本次操作", exact: true }),
    ).toBeVisible();
    await page.unroute("**/api/v1/maintenance/restores");
    await area
      .getByRole("button", { name: "查询本次操作", exact: true })
      .click();
    await expect(area.getByText(/此刻尚未查到操作/)).toBeVisible();
    const session = await fixture.get("/auth/session"),
      cookies = await page.context().cookies(baseURL),
      payload = Buffer.from("anonymous incoming archive");
    let resolveResponse, rejectResponse;
    const response = new Promise((resolve, reject) => {
      resolveResponse = resolve;
      rejectResponse = reject;
    });
    incoming = http.request(
      baseURL + "/api/v1/maintenance/restores",
      {
        method: "POST",
        headers: {
          Origin: baseURL,
          "Content-Type": "application/zip",
          "Content-Length": payload.length,
          "Idempotency-Key": key,
          "X-CSRF-Token": session.csrf_token,
          Cookie: cookies.map((c) => `${c.name}=${c.value}`).join("; "),
        },
      },
      (r) => {
        r.resume();
        r.on("end", () => resolveResponse(r.statusCode));
      },
    );
    incoming.on("error", rejectResponse);
    incoming.write(payload.subarray(0, 9));
    let operation;
    await expect
      .poll(async () => {
        operation = (
          await fixture.get(
            `/maintenance/restores?request_key=${encodeURIComponent(key)}&page=1`,
          )
        )[0];
        return operation?.state;
      })
      .toBe("pending");
    await area
      .getByRole("button", { name: "查询本次操作", exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`operation=${operation.id}`));
    await area.getByRole("button", { name: "放弃此恢复", exact: true }).click();
    incoming.end(payload.subarray(9));
    expect(await response).toBe(409);
    await expect
      .poll(
        async () =>
          (await fixture.get(`/maintenance/restores/${operation.id}`)).operation
            .state,
      )
      .toBe("abandoned");
    await expect(
      area.getByText("已放弃此恢复操作", { exact: true }),
    ).toBeVisible();
    await expect(
      area.getByRole("button", { name: "校验备份包", exact: true }),
    ).toHaveCount(0);
    await page.getByRole("tab", { name: "任务", exact: true }).click();
    const path = `/api/v1/maintenance/restores/${operation.id}`;
    reads.length = 0;
    await page.evaluate(() =>
      window.dispatchEvent(
        new CustomEvent("resources-changed", {
          detail: { type: "instance_operation" },
        }),
      ),
    );
    // One full 3-second polling interval confirms the inactive view stopped its timer.
    await page.waitForTimeout(3300);
    expect(reads.filter((v) => v === path)).toEqual([]);
  } finally {
    incoming?.destroy();
    await fixture.stop();
  }
});
