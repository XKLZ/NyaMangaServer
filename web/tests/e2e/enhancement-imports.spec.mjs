import { test, expect } from "@playwright/test";
import { installViteSource } from "./vite-source.mjs";
import { enhancementFixture } from "./enhancement-fixture.mjs";
installViteSource(test);
test("导入冻结跳过、中途停用暂停及译库停用保留人工标签", async ({
  page,
  baseURL,
}) => {
  test.setTimeout(240000);
  const { get, write, feature } = await enhancementFixture(page, baseURL);
  const lib = await write(
    "/libraries",
    { root_key: "anonymous", display_name: "匿名冻结关闭" },
    "POST",
    201,
  );
  const first = await write(
    "/import-batches",
    {
      library_id: lib.id,
      request_key: "anonymous-enhancement-off",
      allow_metadata_network: true,
    },
    "POST",
    202,
  );
  await expect
    .poll(
      async () => {
        const b = await get(`/import-batches/${first.id}`);
        return `${b.stage}:${b.state}`;
      },
      { timeout: 30000 },
    )
    .toBe("metadata:review");
  const frozen = await get(`/import-batches/${first.id}`);
  expect(frozen.enhancement_stages.versions.state).toBe("disabled_skipped");
  expect(frozen.enhancement_stages.series.state).toBe("disabled_skipped");
  await feature("work_recommendations", true);
  await feature("series_recommendations", true);
  await page.goto(`/admin/imports?batch=${first.id}&library_id=${lib.id}`);
  await expect(
    page
      .locator(".import-stage-steps")
      .getByText("未启用，已跳过", { exact: true }),
  ).toHaveCount(2);
  const writes = [];
  page.on("request", (r) => {
    if (r.method() !== "GET" && r.url().includes("/api/v1/"))
      writes.push(new URL(r.url()).pathname);
  });
  async function skipMetadata() {
    await page.getByRole("button", { name: "本页全选", exact: true }).click();
    await page
      .getByRole("button", { name: "本次不更新所选元数据", exact: true })
      .click();
    await page.getByRole("button", { name: "本次不更新", exact: true }).click();
  }
  await skipMetadata();
  await expect(page.getByText("导入已完成", { exact: true })).toBeVisible();
  const complete = await get(`/import-batches/${first.id}`);
  expect(complete.stage).toBe("finished");
  expect(
    complete.jobs.some((j) =>
      ["work_recommendation", "series_recommendation"].includes(j.type),
    ),
  ).toBe(false);
  expect(writes.filter((p) => p.endsWith("/control/continue"))).toHaveLength(1);
  const secondLib = await write(
    "/libraries",
    { root_key: "anonymous-work", display_name: "匿名冻结开启" },
    "POST",
    201,
  );
  const second = await write(
    "/import-batches",
    {
      library_id: secondLib.id,
      request_key: "anonymous-enhancement-on",
      allow_metadata_network: true,
    },
    "POST",
    202,
  );
  await expect
    .poll(
      async () => {
        const b = await get(`/import-batches/${second.id}`);
        return `${b.stage}:${b.state}`;
      },
      { timeout: 30000 },
    )
    .toBe("metadata:review");
  await page.goto(
    `/admin/imports?batch=${second.id}&library_id=${secondLib.id}`,
  );
  await skipMetadata();
  await expect
    .poll(async () => (await get(`/import-batches/${second.id}`)).stage)
    .toBe("versions");
  async function disableWork() {
    const state = await get("/settings/enhancements"),
      f = state.features.work_recommendations;
    await write(
      "/settings/enhancements",
      {
        expected_revision: state.revision,
        feature: "work_recommendations",
        enabled: false,
        ...(f.affected_tasks
          ? { confirm_pause: true, impact_token: f.impact_token }
          : {}),
      },
      "PATCH",
    );
    await expect
      .poll(
        async () =>
          (await get("/settings/enhancements")).features.work_recommendations
            .state,
      )
      .toBe("stable");
    await expect
      .poll(async () => (await get(`/import-batches/${second.id}`)).error_code)
      .toBe("enhancement_disabled");
  }
  await disableWork();
  const paused = page.getByRole("region", { name: "增强功能暂停导入" });
  await expect(paused).toBeVisible();
  await expect(
    paused.getByRole("button", { name: "恢复本阶段", exact: true }),
  ).toBeDisabled();
  expect(
    (await get(`/import-batches/${second.id}`)).enhancement_stages.versions
      .state,
  ).toBe("blocked");
  await feature("work_recommendations", true);
  expect((await get(`/import-batches/${second.id}`)).state).toBe("paused");
  await expect(
    paused.getByRole("button", { name: "恢复本阶段", exact: true }),
  ).toBeEnabled();
  await paused.getByRole("button", { name: "恢复本阶段", exact: true }).click();
  await expect
    .poll(async () => (await get(`/import-batches/${second.id}`)).state)
    .toBe("processing");
  await disableWork();
  await expect(paused).toBeVisible();
  const beforeSkip = writes.length;
  await paused
    .getByRole("button", { name: "跳过本次推荐", exact: true })
    .click();
  await page.getByRole("button", { name: "返回", exact: true }).click();
  expect(writes.length).toBe(beforeSkip);
  await paused
    .getByRole("button", { name: "跳过本次推荐", exact: true })
    .click();
  await page
    .getByRole("button", { name: "明确跳过本次推荐", exact: true })
    .click();
  await expect
    .poll(async () => (await get(`/import-batches/${second.id}`)).state)
    .toBe("review");
  expect(
    (await get(`/import-batches/${second.id}`)).enhancement_stages.versions
      .state,
  ).toBe("skipped");
  await expect(
    page.getByRole("button", { name: "进入系列审核", exact: true }),
  ).toBeEnabled(); // a remaining human gate, no automatic skip
  // A historical runtime failure must also respect a feature disabled later.
  // Only this anonymous read is mocked; no retry or stage control is submitted.
  const priorBatch = await get(`/import-batches/${second.id}`),
    priorJob = priorBatch.jobs.find((j) => j.type === "work_recommendation"),
    batchPath = `**/api/v1/import-batches/${second.id}`;
  await page.route(batchPath, (route) =>
    route.fulfill({
      json: {
        ...priorBatch,
        state: "failed",
        error_code: "runtime_unavailable",
        can_retry: true,
        current_failure: {
          job_id: priorJob.id,
          stage: "versions",
          error_code: "runtime_unavailable",
        },
      },
    }),
  );
  await page.reload();
  const failed = page.getByRole("region", {
    name: "当前推荐失败",
    exact: true,
  });
  await expect(failed).toBeVisible();
  await expect(
    failed.getByRole("button", { name: "重试版本推荐", exact: true }),
  ).toBeDisabled();
  await expect(
    failed.getByRole("link", { name: "前往增强设置", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "批次更多操作", exact: true }).click();
  await expect(
    page.getByRole("menuitem", { name: "重试失败阶段", exact: true }),
  ).toHaveAttribute("aria-disabled", "true");
  await page.keyboard.press("Escape");
  await page.unroute(batchPath);
  const book = (await get(`/editions?library_id=${lib.id}`)).items[0],
    detail = await get(`/books/${book.id}`);
  await write(
    `/books/${book.id}/overrides`,
    {
      expected_revision: detail.revision,
      fields: {
        tags: {
          op: "set",
          value: [
            { namespace: "artist", name: "manual artist" },
            { namespace: "artist", name: "second artist" },
          ],
        },
      },
    },
    "PATCH",
  );
  const tag = (
    await get(`/tag-catalog?library_id=${lib.id}&category=artist&q=manual`)
  ).items[0];
  await write(
    `/tags/${tag.id}/translations/zh-CN`,
    { expected_revision: tag.translation_revision, text: "手工译名" },
    "PUT",
  );
  await feature("chinese_dictionary", false);
  const suggestions = await write("/tag-suggestions", {
    namespace: "artist",
    q: "manual",
    exclude: [],
    page: 1,
    page_size: 50,
  });
  expect(suggestions.dictionary_state).toBe("disabled");
  expect(suggestions.items[0].translation).toBe("手工译名");
  expect(suggestions.items[0].dictionary_match).toBeNull();
  await page.goto(`/admin/books?library_id=${lib.id}&book=${book.id}`);
  const modal = page.getByRole("dialog", { name: /^漫画详情/ });
  await modal.getByRole("tab", { name: "元数据", exact: true }).click();
  const artist = modal.getByRole("region", { name: "作者标签", exact: true });
  await expect(artist).toContainText("手工译名");
  const input = artist.getByRole("combobox", {
    name: "添加作者标签",
    exact: true,
  });
  await expect(input).toBeEnabled();
  await input.fill("second");
  await expect(
    artist.getByText("译库未启用，仅查询本地标签", { exact: true }),
  ).toBeVisible();
  await expect(artist.getByText("未收录", { exact: true })).toHaveCount(0);
  await expect(artist).toContainText("manual artist");
  await input.fill("");
});
