import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import {
  healthAccess,
  resetHealth,
  updateHealth,
  maintenanceProtected,
  healthWriteAllowed,
  registerHealthJob,
  readOnlyPost,
} from "../../src/shared/healthAccess.mjs";
import {
  healthConclusion,
  healthBytes,
  healthSelection,
  healthRecoveryActions,
  repairable,
  sourceBudget,
} from "../../src/shared/healthFlow.ts";
import { isBusinessWrite, writesPending } from "../../src/shared/writeState.ts";
import {
  maintenanceSection,
  consoleURL,
} from "../../src/shared/consoleRoutes.mjs";
const idle = {
  phase: "idle",
  repair_id: null,
  revision: 1,
  can_continue: false,
  can_rollback: false,
};
test("report completeness never turns failed, inaccessible or limited coverage into all healthy", () => {
  const v = { state: "completed", counts: {}, completion: "checked_scope" };
  assert.match(healthConclusion(v), /本次检测范围/);
  for (const status of ["manual", "stale", "not_checked", "inaccessible"])
    assert.match(
      healthConclusion({ ...v, counts: { [status]: 1 } }),
      /核对范围/,
    );
  assert.match(healthConclusion({ ...v, state: "failed" }), /失败/);
  assert.match(healthConclusion({ ...v, completion: "limited" }), /核对范围/);
  assert.match(
    healthConclusion({ ...v, counts: { confirmed: 1 } }),
    /确认异常/,
  );
  assert.equal(healthBytes(512), "512 B");
  assert.equal(healthBytes(1024 ** 2), "1.00 MiB");
  assert.equal(sourceBudget(8), 8 * 1024 ** 3);
  assert.equal(sourceBudget(1024), 1024 ** 4);
  for (const n of [0, -1, 1025, NaN, Infinity])
    assert.throws(() => sourceBudget(n));
  for (const f of [
    { status: "normal", repair: "projection" },
    { status: "stale", repair: "projection" },
    { status: "manual", repair: "preferred" },
    { status: "confirmed", repair: "invented" },
  ])
    assert.equal(repairable(f), false);
  assert.equal(repairable({ status: "confirmed", repair: "projection" }), true);
});
test("recovery belongs to the owner; completed idle repair can roll back, running cannot resume", () => {
  const r = { id: "a", state: "completed" },
    active = {
      ...idle,
      phase: "executing",
      repair_id: "a",
      can_continue: true,
      can_rollback: true,
    };
  assert.deepEqual(healthRecoveryActions(idle, r, "completed"), {
    continue: false,
    rollback: true,
  });
  assert.deepEqual(healthRecoveryActions(active, r, "running"), {
    continue: false,
    rollback: true,
  });
  assert.deepEqual(healthRecoveryActions(active, r, "paused"), {
    continue: true,
    rollback: true,
  });
  assert.deepEqual(
    healthRecoveryActions({ ...active, repair_id: "b" }, r, "paused"),
    { continue: false, rollback: false },
  );
  assert.deepEqual(healthRecoveryActions(active, r, "paused", true), {
    continue: false,
    rollback: false,
  });
  assert.equal(
    healthRecoveryActions(idle, { ...r, state: "rolled_back" }, "completed")
      .rollback,
    false,
  );
});
test("maintenance failure retains protection; exact controls and read-only POSTs only", () => {
  resetHealth();
  updateHealth({
    supported: true,
    state: { ...idle, phase: "paused", repair_id: "r" },
  });
  updateHealth({ error: true });
  assert.equal(maintenanceProtected(), true);
  assert.equal(healthAccess().state.phase, "paused");
  registerHealthJob("owner");
  for (const path of [
    "/admin/books/search",
    "/admin/editions/search",
    "/books/search",
    "/editions/search",
    "/tag-suggestions",
  ]) {
    assert.equal(readOnlyPost(path, "POST"), true);
    assert.equal(isBusinessWrite(path, "POST"), false);
    assert.equal(healthWriteAllowed(path, "POST"), true);
  }
  for (const path of [
    "/maintenance/health-checks",
    "/maintenance/health-repair-plans",
    "/maintenance/health-repair-plans/p/execute",
    "/maintenance/health-repairs/r/rollback",
    "/maintenance/health-state/recover",
    "/jobs/owner/resume",
    "/auth/logout",
  ])
    assert.equal(healthWriteAllowed(path, "POST"), true);
  for (const path of [
    "/books",
    "/maintenance/health-anything",
    "/maintenance/health-repairs/r/delete",
    "/jobs/other/resume",
    "/auth/password",
  ])
    assert.equal(healthWriteAllowed(path, "POST"), false);
  assert.equal(healthWriteAllowed("/books", "GET"), true);
  updateHealth({ state: idle, error: false });
  assert.equal(maintenanceProtected(), false);
  resetHealth();
  assert.equal(maintenanceSection("?section=health"), "health");
  assert.equal(
    consoleURL("/admin/maintenance?section=health&check=c&finding_page=2"),
    "/admin/maintenance?section=health&check=c&finding_page=2",
  );
});
test("JSON, raw role and file uploads all refuse protected writes before IO; search remains unlocked", async () => {
  const source = (
    await readFile(new URL("../../src/shared/api.ts", import.meta.url), "utf8")
  )
    .replaceAll(
      '"./scanFlow"',
      JSON.stringify(
        new URL("../../src/shared/scanFlow.ts", import.meta.url).href,
      ),
    )
    .replaceAll(
      '"./writeState"',
      JSON.stringify(
        new URL("../../src/shared/writeState.ts", import.meta.url).href,
      ),
    );
  const api = await import(
    `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source, { mode: "transform" })).toString("base64")}`
  );
  const old = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => {
    calls++;
    return Response.json({ items: [] });
  };
  updateHealth({
    supported: true,
    state: { ...idle, phase: "executing" },
    error: false,
  });
  try {
    for (const run of [
      () => api.request("/series", "POST", {}),
      () =>
        api.requestBinary(
          "/tag-catalog/a/images/foreground",
          "PUT",
          1,
          new Blob(["x"]),
          "image/png",
        ),
      () => api.upload("/books/a/cover", new File(["x"], "a.png"), 1),
    ])
      await assert.rejects(run(), (e) => e.code === "maintenance_active");
    assert.equal(calls, 0);
    assert.equal(writesPending(), false);
    await api.request("/tag-suggestions", "POST", {});
    assert.equal(calls, 1);
    assert.equal(writesPending(), false);
  } finally {
    resetHealth();
    globalThis.fetch = old;
  }
});

test("whole-page selection refuses overflow atomically and does not truncate cross-page choices", () => {
  const rows = Array.from({ length: 102 }, (_, i) => ({
    id: String(i),
    status: "confirmed",
    repair: "projection",
  }));
  const prior = Object.fromEntries(rows.slice(0, 98).map((v) => [v.id, v]));
  assert.throws(
    () => healthSelection(prior, rows.slice(98), true),
    /整页选择未加入/,
  );
  assert.equal(Object.keys(prior).length, 98);
  assert.equal(
    Object.keys(healthSelection(prior, rows.slice(98, 100), true)).length,
    100,
  );
  assert.equal(
    Object.keys(healthSelection(prior, rows.slice(0, 25), false)).length,
    73,
  );
});

test("only homogeneous series numbering can exceed the ordinary repair cap", () => {
  const numbered = Array.from({ length: 231 }, (_, i) => ({
    id: `series-${i}`,
    status: "confirmed",
    repair: "series_positions",
  }));
  assert.equal(Object.keys(healthSelection({}, numbered, true)).length, 231);
  assert.throws(() =>
    healthSelection(
      {},
      [
        ...numbered,
        { id: "projection", status: "confirmed", repair: "projection" },
      ],
      true,
    ),
  );
  assert.throws(() =>
    healthSelection(
      {},
      Array.from({ length: 1001 }, (_, i) => ({
        ...numbered[0],
        id: `series-${i}`,
      })),
      true,
    ),
  );
});
