import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import {
  instanceRequestUncreated,
  initialMappings,
  mappingsEqual,
  instanceActive,
  recoveryReturn,
} from "../../src/shared/instanceFlow.ts";
import {
  resetHealth,
  updateHealth,
  healthWriteAllowed,
  maintenanceProtected,
} from "../../src/shared/healthAccess.mjs";
import { writesPending } from "../../src/shared/writeState.ts";
import {
  maintenanceSection,
  consoleURL,
} from "../../src/shared/consoleRoutes.mjs";
test("mapping defaults are explicit null and only saved source identities survive", () => {
  const detail = {
    summary: { source_roots: [{ library_id: "a" }, { library_id: "b" }] },
    root_mappings: [
      { library_id: "a", target_root_key: "configured" },
      { library_id: "foreign", target_root_key: "ignore" },
    ],
  };
  const result = initialMappings(detail);
  assert.deepEqual(result, [
    { library_id: "a", target_root_key: "configured" },
    { library_id: "b", target_root_key: null },
  ]);
  assert.equal(mappingsEqual(result, [...result].reverse()), true);
  assert.equal(mappingsEqual(result, []), false);
  assert.equal(
    mappingsEqual(result, [
      { library_id: "a", target_root_key: null },
      result[1],
    ]),
    false,
  );
});
test("recovery URL keeps identities, never incoming credentials or arbitrary redirects", () => {
  assert.equal(maintenanceSection("?section=recovery"), "recovery");
  assert.equal(
    consoleURL(
      "/admin/maintenance?section=recovery&operation=fixture&kind=restores",
    ),
    "/admin/maintenance?section=recovery&operation=fixture&kind=restores",
  );
  assert.equal(recoveryReturn("/login?password=private"), null);
  assert.equal(
    recoveryReturn(
      "/admin/maintenance?section=recovery&operation=fixture&kind=restores&password=private&path=%2Fprivate",
    ),
    "/admin/maintenance?section=recovery&operation=fixture&kind=restores",
  );
  for (const state of [
    "pending",
    "running",
    "validating",
    "preparing",
    "abandon_requested",
  ])
    assert.equal(instanceActive(state), true);
  for (const state of [
    "uploaded",
    "validated",
    "completed",
    "abandoned",
    "awaiting_restart",
  ])
    assert.equal(instanceActive(state), false);
});
test("background hold does not make business editing read-only; exact recovery controls bypass instance barrier", () => {
  resetHealth();
  updateHealth({
    supported: true,
    state: { phase: "idle" },
    instanceSupported: true,
    instanceState: { background_hold: true },
  });
  assert.equal(maintenanceProtected(), false);
  assert.equal(healthWriteAllowed("/books/a/overrides", "PATCH"), true);
  updateHealth({ state: { phase: "instance_maintenance" } });
  assert.equal(maintenanceProtected(), true);
  for (const p of [
    "/maintenance/backups",
    "/maintenance/restores",
    "/maintenance/backups/a/cancel",
    "/maintenance/restores/a/validate",
    "/maintenance/restores/a/preview",
    "/maintenance/restores/a/confirm",
    "/maintenance/restores/a/abandon",
    "/maintenance/restores/a/rollback",
    "/maintenance/instance-state/release",
  ])
    assert.equal(healthWriteAllowed(p, "POST"), true);
  for (const p of [
    "/maintenance/restores/a/delete",
    "/maintenance/instance-whatever",
    "/maintenance/backups/a/cancel/extra",
  ])
    assert.equal(healthWriteAllowed(p, "POST"), false);
  assert.equal(healthWriteAllowed("/books/a/overrides", "PATCH"), false);
  resetHealth();
});
async function apiModule() {
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
  return import(
    `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source, { mode: "transform" })).toString("base64")}`
  );
}
test("ZIP upload sends the original File, CSRF and original identity, holds writes and releases on abort", async () => {
  const api = await apiModule(),
    old = globalThis.XMLHttpRequest,
    oldFetch = globalThis.fetch;
  let xhr;
  class FakeXHR {
    headers = {};
    upload = {};
    status = 201;
    responseText = JSON.stringify({ id: "saved" });
    constructor() {
      xhr = this;
    }
    open(method, url) {
      this.method = method;
      this.url = url;
    }
    setRequestHeader(k, v) {
      this.headers[k] = v;
    }
    send(body) {
      this.body = body;
    }
    abort() {
      this.onabort();
    }
  }
  globalThis.XMLHttpRequest = FakeXHR;
  try {
    globalThis.fetch = async () =>
      Response.json({
        account_id: "anonymous",
        csrf_token: "anonymous-csrf",
        expires_at: "future",
      });
    await api.api.login("anonymous password");
    const file = new File(["anonymous archive"], "anonymous.zip"),
      controller = new AbortController();
    const pending = api.uploadInstance(
      file,
      "same-original-key",
      controller.signal,
      () => {},
    );
    assert.equal(writesPending(), true);
    assert.equal(xhr.body, file);
    assert.equal(xhr.headers["Content-Type"], "application/zip");
    assert.equal(xhr.headers["X-CSRF-Token"], "anonymous-csrf");
    assert.equal(xhr.headers["Idempotency-Key"], "same-original-key");
    xhr.onload();
    assert.deepEqual(await pending, { id: "saved" });
    assert.equal(writesPending(), false);
    const cancelled = new AbortController(),
      p = api.uploadInstance(
        file,
        "another-original-key",
        cancelled.signal,
        () => {},
      );
    cancelled.abort();
    await assert.rejects(p, { name: "AbortError" });
    assert.equal(writesPending(), false);
  } finally {
    globalThis.XMLHttpRequest = old;
    globalThis.fetch = oldFetch;
  }
});
test("download preflight consumes no complete package or Blob and exposes missing history file", async () => {
  const api = await apiModule(),
    old = globalThis.fetch;
  let cancelled = false,
    range;
  try {
    globalThis.fetch = async (_, options) => {
      range = options.headers.Range;
      return new Response(
        new ReadableStream({
          cancel() {
            cancelled = true;
          },
        }),
        { status: 206 },
      );
    };
    await api.checkInstanceDownload("fixture");
    assert.equal(range, "bytes=0-0");
    assert.equal(cancelled, true);
    globalThis.fetch = async () =>
      Response.json(
        { error: { code: "not_found", message: "missing" } },
        { status: 404 },
      );
    await assert.rejects(
      api.checkInstanceDownload("fixture"),
      (e) => e.status === 404 && e.code === "not_found",
    );
  } finally {
    globalThis.fetch = old;
  }
});

test("only stable paired pre-creation refusals release an unresolved request identity", () => {
  for (const [status, code] of [
    [401, "unauthenticated"],
    [403, "csrf_rejected"],
    [403, "origin_rejected"],
    [409, "instance_build_mismatch"],
  ])
    assert.equal(instanceRequestUncreated(status, code), true);
  for (const [status, code] of [
    [0, "network_error"],
    [409, "revision_conflict"],
    [409, "instance_recovery_required"],
    [500, "instance_operation_failed"],
    [403, "permission_denied"],
    [422, "invalid_instance_package"],
    [500, "csrf_rejected"],
  ])
    assert.equal(instanceRequestUncreated(status, code), false);
});
