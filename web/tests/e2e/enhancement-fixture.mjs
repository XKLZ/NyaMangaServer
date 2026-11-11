import { readFile } from "node:fs/promises";
import path from "node:path";
import { expect } from "@playwright/test";
export async function enhancementFixture(page, baseURL) {
  const origin = new URL(baseURL);
  if (!["18081", "18082"].includes(origin.port))
    throw Error("Anonymous enhancement port required");
  const state = JSON.parse(
    await readFile(
      path.resolve(
        import.meta.dirname,
        `../../../.runtime/tests/e2e-state-${origin.port}.json`,
      ),
      "utf8",
    ),
  );
  expect(
    state.run_directory.startsWith(
      path.resolve(import.meta.dirname, "../../../.runtime/tests/runs/e2e-"),
    ),
  ).toBe(true);
  const password = "Anonymous enhancement password 2026";
  const setup = await page.request.post(`${baseURL}/api/v1/auth/setup`, {
    headers: { Origin: baseURL },
    data: { setup_token: await readFile(state.token_file, "utf8"), password },
  });
  expect(setup.status()).toBe(200);
  const headers = {
    Origin: baseURL,
    "X-CSRF-Token": (await setup.json()).csrf_token,
  };
  async function get(url) {
    const response = await page.request.get(`${baseURL}/api/v1${url}`);
    expect(response.status(), url).toBe(200);
    return response.json();
  }
  async function write(url, data = {}, method = "POST", status) {
    const response = await page.request.fetch(`${baseURL}/api/v1${url}`, {
      method,
      headers,
      data,
    });
    if (status) expect(response.status(), url).toBe(status);
    else expect([200, 201, 202]).toContain(response.status());
    return response.json();
  }
  async function feature(key, enabled) {
    const settings = await get("/settings/enhancements");
    await write(
      "/settings/enhancements",
      { feature: key, enabled, expected_revision: settings.revision },
      "PATCH",
    );
    await expect
      .poll(
        async () => (await get("/settings/enhancements")).features[key].state,
      )
      .toBe("stable");
  }
  async function library(root = "anonymous") {
    const lib = await write(
      "/libraries",
      {
        root_key: root,
        display_name: root === "anonymous" ? "A 匿名设置" : "B 匿名设置",
      },
      "POST",
      201,
    );
    const task = await write(`/libraries/${lib.id}/scans`, {}, "POST", 202);
    await expect
      .poll(async () => (await get(`/jobs/${task.job_id}`)).state)
      .toBe("completed");
    return lib;
  }
  return { get, write, feature, library, password, headers };
}
export async function enableAnonymousRecommendations(page, baseURL, headers) {
  for (const feature of ["work_recommendations", "series_recommendations"]) {
    const get = async () =>
      (
        await page.request.get(`${baseURL}/api/v1/settings/enhancements`)
      ).json();
    const state = await get();
    const result = await page.request.patch(
      `${baseURL}/api/v1/settings/enhancements`,
      {
        headers,
        data: { expected_revision: state.revision, feature, enabled: true },
      },
    );
    expect([200, 202]).toContain(result.status());
    await expect
      .poll(async () => (await get()).features[feature].effective_enabled)
      .toBe(true);
  }
}
