import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import {
  setActor,
  actorEpoch,
  actorAccess,
  setActorVerified,
  canEditLibrary,
  canViewLibrary,
  clientAccess,
  rememberResources,
  legacyActor,
  registerActorAbort,
} from "../../src/shared/actorAccess.mjs";
import {
  readingStatus,
  readingProgress,
  readingRequest,
} from "../../src/shared/readingFlow.ts";
const user = (id = "viewer", grants = { a: "viewer", b: "editor" }) => ({
  id,
  username: id,
  role: "user",
  enabled: true,
  must_change_password: false,
  acl_revision: 1,
  library_grants: grants,
  capabilities: { personal_state: true },
});
test("grants restrict writes and global endpoints, forced users never enter business", () => {
  setActor(user());
  rememberResources({
    items: [
      { id: "one", library_id: "a" },
      { id: "two", library_id: "b" },
    ],
  });
  assert.equal(canViewLibrary("a"), true);
  assert.equal(canEditLibrary("a"), false);
  assert.equal(canEditLibrary("b"), true);
  assert.equal(canViewLibrary("c"), false);
  for (const p of [
    "/settings/enhancements",
    "/library-roots",
    "/maintenance/status",
    "/accounts",
  ])
    assert.equal(clientAccess(p, "GET"), false, p);
  assert.equal(clientAccess("/settings/work-recommendations", "GET"), true);
  assert.equal(clientAccess("/books/one/overrides", "PUT", {}), false);
  assert.equal(clientAccess("/books/two/overrides", "PUT", {}), true);
  assert.equal(clientAccess("/books/one/reading-state", "PUT", {}), true);
  assert.equal(clientAccess("/tag-catalog/t/images/foreground", "PUT"), false);
  setActor(user("editor", { a: "editor", b: "editor" }));
  rememberResources({
    items: [
      { id: "one", library_id: "a" },
      { id: "two", library_id: "b" },
    ],
  });
  assert.equal(
    clientAccess("/metadata-jobs", "POST", { book_ids: ["one", "two"] }),
    false,
  );
  assert.equal(
    clientAccess("/metadata-jobs", "POST", { book_ids: ["one"] }),
    true,
  );
  setActorVerified(false);
  assert.equal(clientAccess("/books/two/overrides", "PUT", {}), false);
  assert.equal(clientAccess("/books", "GET"), true);
  setActor({ ...user(), must_change_password: true });
  assert.equal(clientAccess("/libraries", "GET"), false);
  assert.equal(clientAccess("/auth/password", "POST"), true);
  setActor(user("empty", {}));
  assert.equal(canViewLibrary("a"), false);
  assert.equal(
    clientAccess("/work-discovery-jobs", "POST", { library_id: "a" }),
    false,
  );
});
test("unchanged session preserves epoch/resources, ACL change aborts old reads", () => {
  setActor(user());
  const before = actorEpoch();
  let aborted = 0;
  const off = registerActorAbort(() => aborted++);
  rememberResources({ id: "one", library_id: "a" });
  setActorVerified(false);
  setActor(user());
  assert.equal(actorEpoch(), before);
  assert.equal(actorAccess().verified, true);
  assert.equal(aborted, 0);
  assert.equal(clientAccess("/books/one/overrides", "PUT"), false);
  setActor({ ...user(), acl_revision: 2 });
  assert.equal(actorEpoch(), before + 1);
  assert.equal(aborted, 1);
  off();
});
const reading = {
  edition_id: "e",
  page_index: null,
  recorded_page_count: null,
  content_revision: 1,
  current_content_revision: 2,
  current_page_count: null,
  stale: false,
  completed: false,
  completed_at: null,
  last_read_at: 12,
  read_count: 50,
  revision: 0,
};
test("reading status ignores count/time, unknown total and API zero page remain explicit", () => {
  assert.equal(readingStatus(reading), "未读");
  assert.equal(readingProgress(reading), "总页数未知");
  assert.equal(readingStatus({ ...reading, page_index: 0 }), "阅读中");
  assert.equal(
    readingProgress({ ...reading, page_index: 0 }),
    "第 1 页 / 总页数未知",
  );
  assert.equal(readingStatus({ ...reading, completed: true }), "已完成");
  assert.equal(
    readingProgress({
      ...reading,
      page_index: 1,
      stale: true,
      recorded_page_count: 7,
      current_page_count: 9,
    }),
    "第 2 页 / 7 页",
  );
  for (const action of ["complete", "uncomplete", "reset"])
    assert.deepEqual(readingRequest(reading, action, "frozen-key"), {
      expected_revision: 0,
      content_revision: 2,
      idempotency_key: "frozen-key",
      action,
    });
  assert.equal(
    readingRequest(reading, "reset", "frozen-key", true).accept_content_change,
    true,
  );
});
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
test("late JSON, binary and cover 401 body cannot log out a new actor; shared-source 403 keeps draft", async () => {
  const prior = globalThis.fetch,
    win = globalThis.window;
  const events = [];
  globalThis.window = {
    dispatchEvent: (e) => e.type !== "actor-invalidated" && events.push(e.type),
  };
  try {
    for (const run of [
      () => api.request("/books/one"),
      () => api.requestBinary("/tag-catalog/t/images/foreground", "DELETE", 1),
      () => api.upload("/books/one/cover", new File(["x"], "x.png"), 1),
    ]) {
      setActor(legacyActor("before"));
      let release, began;
      const body = new Promise((r) => (release = r)),
        started = new Promise((r) => (began = r));
      globalThis.fetch = async () => ({
        ok: false,
        status: 401,
        json: async () => {
          began();
          return body;
        },
      });
      const pending = run(),
        rejected = assert.rejects(pending, (e) => e.name === "AbortError");
      await started;
      setActor(legacyActor("after"));
      release({ error: { code: "unauthenticated" } });
      await rejected;
      assert.deepEqual(events, []);
    }
    setActor(user());
    globalThis.fetch = async () =>
      Response.json(
        { error: { code: "shared_source_requires_admin", message: "denied" } },
        { status: 403 },
      );
    await assert.rejects(
      api.request("/books/new/source", "POST", {}),
      (e) =>
        e.status === 403 &&
        e.message.includes("管理员") &&
        e.message.includes("保留"),
    );
    assert.deepEqual(events, []);
    globalThis.fetch = async () =>
      Response.json(
        {
          error: { code: "reading_revision_conflict" },
          current_state: { revision: 7 },
        },
        { status: 409 },
      );
    await assert.rejects(
      api.request("/books/one/reading-state", "PUT", {}),
      (e) => e.details.current_state.revision === 7,
    );
  } finally {
    globalThis.fetch = prior;
    globalThis.window = win;
    setActor(legacyActor("admin"));
  }
});
test("late revoked XHR error cannot expire another account", async () => {
  const previous = globalThis.XMLHttpRequest,
    win = globalThis.window;
  const events = [];
  let xhr;
  globalThis.window = {
    dispatchEvent: (e) => e.type !== "actor-invalidated" && events.push(e.type),
  };
  globalThis.XMLHttpRequest = class {
    constructor() {
      xhr = this;
      this.upload = {};
    }
    open() {}
    setRequestHeader() {}
    send() {}
    abort() {}
  };
  try {
    setActor(legacyActor("old"));
    const done = assert.rejects(
      api.uploadInstance(
        new File(["x"], "x.zip"),
        "anonymous-key",
        new AbortController().signal,
        () => {},
      ),
      (e) => e.name === "AbortError",
    );
    setActor(legacyActor("new"));
    xhr.status = 401;
    xhr.responseText = JSON.stringify({ error: { code: "unauthenticated" } });
    xhr.onload();
    await done;
    assert.deepEqual(events, []);
  } finally {
    globalThis.XMLHttpRequest = previous;
    globalThis.window = win;
  }
});
