import { test, expect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { installViteSource } from "./vite-source.mjs";
import { enhancementFixture } from "./enhancement-fixture.mjs";
installViteSource(test);
test("增强切换202、失败重试、三类409与503持久回读", async ({
  page,
  baseURL,
}, info) => {
  test.setTimeout(180000);
  const { get, feature } = await enhancementFixture(page, baseURL);
  await feature("work_recommendations", true);
  await feature("series_recommendations", true);
  let state = await get("/settings/enhancements"),
    mode = "pending",
    holdRead = false,
    releaseRead;
  const patches = [],
    reads = [],
    errors = [];
  state.features.work_recommendations.affected_tasks = 2;
  state.features.work_recommendations.impact_token = "anonymous-impact-a";
  const clone = () => structuredClone(state);
  function transition(key, target, attempt = 1) {
    state.revision++;
    state.features[key] = {
      ...state.features[key],
      enabled: target,
      effective_enabled: false,
      state: target ? "enabling" : "disabling",
      affected_tasks: 0,
      transition: {
        id: `anonymous-${key}`,
        feature: key,
        attempt,
        state: "pending",
        target_enabled: target,
        error_code: "",
        affected_tasks: 2,
        created_at: 1,
        updated_at: 2,
      },
    };
  }
  function complete(key, target) {
    state.revision++;
    state.features[key] = {
      ...state.features[key],
      enabled: target,
      effective_enabled: target,
      state: "stable",
      affected_tasks: 0,
      transition: state.features[key].transition
        ? { ...state.features[key].transition, state: "completed" }
        : null,
    };
  }
  await page.route("**/api/v1/settings/enhancements", async (route) => {
    if (route.request().method() === "GET") {
      reads.push("read");
      const saved = clone();
      if (holdRead) {
        holdRead = false;
        await new Promise((resolve) => (releaseRead = resolve));
      }
      await route.fulfill({ json: saved }).catch(() => {});
      return;
    }
    const patch = route.request().postDataJSON();
    patches.push(patch);
    if (mode === "revision") {
      state.revision++;
      await route.fulfill({
        status: 409,
        json: {
          error: { code: "enhancement_revision_conflict", message: "匿名冲突" },
        },
      });
      return;
    }
    if (mode === "impact" || mode === "transition") {
      await route.fulfill({
        status: 409,
        json: {
          error: {
            code:
              mode === "impact"
                ? "enhancement_impact_changed"
                : "enhancement_transition_conflict",
            message: "匿名冲突",
          },
        },
      });
      return;
    }
    if (mode === "storage") {
      complete(patch.feature, patch.enabled);
      await route.fulfill({
        status: 503,
        json: {
          error: {
            code: "enhancement_storage_unavailable",
            message: "匿名存储暂不可用",
          },
        },
      });
      return;
    }
    if (mode === "pending" || patch.retry_transition_id) {
      const previous = state.features[patch.feature].transition;
      transition(
        patch.feature,
        patch.enabled ?? previous.target_enabled,
        patch.retry_transition_id ? previous.attempt + 1 : 1,
      );
      await route.fulfill({ status: 202, json: clone() });
      return;
    }
    complete(patch.feature, patch.enabled);
    await route.fulfill({ json: clone() });
  });
  page.on("pageerror", (e) => errors.push(e.message));
  const work = page.getByRole("switch", { name: "版本推荐开关", exact: true });
  async function category(name) {
    if (info.project.name === "mobile") {
      await page
        .getByRole("combobox", { name: "设置分类", exact: true })
        .click();
      await page.getByRole("option", { name, exact: true }).click();
    } else
      await page
        .getByRole("navigation", { name: "设置分类" })
        .getByRole("button", { name, exact: true })
        .click();
  }
  async function hint() {
    await page.evaluate(() =>
      window.dispatchEvent(
        new CustomEvent("resources-changed", {
          detail: { type: "enhancements", resource_id: "global" },
        }),
      ),
    );
  }
  const dialog = page.getByRole("dialog", {
    name: "请重新核对增强设置",
    exact: true,
  });
  await page.goto("/admin/settings?section=enhancements");
  await expect(work).toBeChecked();
  await expect(work).toBeEnabled();
  await expect(
    page.getByRole("switch", { name: "系列推荐开关", exact: true }),
  ).toBeChecked();
  await expect(
    page.getByRole("switch", { name: "中文译库开关", exact: true }),
  ).toBeChecked();
  await work.click();
  await page
    .getByRole("button", { name: "确认暂停 2 个任务并关闭", exact: true })
    .click();
  await expect(work).not.toBeChecked();
  await expect(work).toBeDisabled();
  await expect(page.getByText("关闭收尾中", { exact: true })).toBeVisible();
  expect(patches).toHaveLength(1);
  expect(patches[0].impact_token).toBe("anonymous-impact-a");
  await category("账号与安全");
  await expect(page.getByLabel("当前密码", { exact: true })).toBeVisible();
  const before = reads.length;
  await page.waitForTimeout(1800);
  expect(reads.length).toBe(before);
  await category("增强功能");
  await expect(work).toBeDisabled();
  state.revision++;
  state.features.work_recommendations.state = "failed";
  state.features.work_recommendations.transition = {
    ...state.features.work_recommendations.transition,
    state: "failed",
    error_code: "drain_failed",
  };
  await hint();
  await expect(page.getByText("切换失败", { exact: true })).toBeVisible();
  const shots = path.resolve(
    import.meta.dirname,
    "../../../.runtime/maintenance/enhancements-ui/screenshots",
  );
  await mkdir(shots, { recursive: true });
  await page.screenshot({
    path: path.join(shots, `${info.project.name}-transition-failed.png`),
    fullPage: true,
  });
  const failedRevision = state.revision;
  holdRead = true;
  await page.getByRole("button", { name: "刷新增强设置", exact: true }).click();
  await expect.poll(() => !!releaseRead).toBe(true);
  await page
    .getByRole("region", { name: "版本推荐", exact: true })
    .getByRole("button", { name: "重试切换", exact: true })
    .click();
  await expect(page.getByText("关闭收尾中", { exact: true })).toBeVisible();
  expect(patches.at(-1)).toEqual({
    expected_revision: failedRevision,
    feature: "work_recommendations",
    retry_transition_id: "anonymous-work_recommendations",
  });
  expect(state.features.work_recommendations.transition.attempt).toBe(2);
  releaseRead();
  await expect(work).not.toBeChecked();
  await expect(work).toBeDisabled();
  complete("work_recommendations", false);
  await hint();
  await expect(work).toBeEnabled();
  mode = "revision";
  const count = patches.length;
  await work.click();
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("操作意图仍保留");
  await expect(work).not.toBeChecked();
  expect(patches.length).toBe(count + 1);
  const currentRevision = state.revision;
  await dialog
    .getByRole("button", { name: "采用当前状态重新核对", exact: true })
    .click();
  mode = "success";
  await page.getByRole("button", { name: "确认提交", exact: true }).click();
  await expect(work).toBeChecked();
  await expect(work).toBeEnabled();
  expect(patches.at(-1).expected_revision).toBe(currentRevision);
  state.features.work_recommendations.affected_tasks = 1;
  state.features.work_recommendations.impact_token = "old-task-impact";
  await hint();
  await expect(work).toBeEnabled();
  await work.click();
  await expect(
    page.getByRole("button", { name: "确认暂停 1 个任务并关闭", exact: true }),
  ).toBeVisible();
  state.features.work_recommendations.affected_tasks = 2;
  state.features.work_recommendations.impact_token = "changed-task-impact";
  mode = "impact";
  await page
    .getByRole("button", { name: "确认暂停 1 个任务并关闭", exact: true })
    .click();
  await expect(dialog).toContainText("任务影响已变化");
  await expect(dialog).toContainText("当前排队／运行任务：2 个");
  await expect(work).toBeChecked();
  await dialog
    .getByRole("button", { name: "采用当前状态重新核对", exact: true })
    .click();
  mode = "success";
  await page
    .getByRole("button", { name: "确认暂停 2 个任务并关闭", exact: true })
    .click();
  await expect(work).not.toBeChecked();
  await expect(work).toBeEnabled();
  expect(patches.at(-1).impact_token).toBe("changed-task-impact");
  mode = "storage";
  const storageCount = patches.length;
  await work.click();
  await expect(dialog).toContainText("存储暂不可用");
  await expect(work).toBeChecked();
  expect(patches.length).toBe(storageCount + 1);
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await expect(work).toBeChecked();
  mode = "transition";
  await work.click();
  await expect(dialog).toContainText("切换状态已变化");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await expect(work).toBeChecked();
  state.revision++;
  state.features.chinese_dictionary = {
    ...state.features.chinese_dictionary,
    state: "failed",
    effective_enabled: false,
    resource_state: "unavailable",
    transition: {
      id: "anonymous-dictionary",
      feature: "chinese_dictionary",
      attempt: 1,
      state: "failed",
      target_enabled: true,
      error_code: "offline_restore_unavailable",
      affected_tasks: 0,
      created_at: 1,
      updated_at: 2,
    },
  };
  await hint();
  const dictionary = page.getByRole("region", {
    name: "中文译库",
    exact: true,
  });
  await expect(dictionary).toContainText("译库离线恢复未完成");
  mode = "pending";
  await dictionary
    .getByRole("button", { name: "重试切换", exact: true })
    .click();
  expect(patches.at(-1).feature).toBe("chinese_dictionary");
  expect("enabled" in patches.at(-1)).toBe(false);
  await expect(dictionary).toContainText("开启收尾中");
  complete("chinese_dictionary", true);
  state.features.chinese_dictionary.resource_state = "not_downloaded";
  await hint();
  await expect(dictionary).toContainText("尚未安装译库");
  await expect(dictionary.getByText("译库可用", { exact: true })).toHaveCount(
    0,
  );
  expect(errors).toEqual([]);
});
