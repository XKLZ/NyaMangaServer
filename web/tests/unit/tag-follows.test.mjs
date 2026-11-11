import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import {
  catalogInput,
  tagDirectoryInput,
  tagWorkInput,
  tagWorkspaceURL,
} from "../../src/admin/tags/tagCatalogFlow.ts";
const load = async (path, replace) =>
  import(
    `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes((await readFile(new URL(path, import.meta.url), "utf8")).replace('import { request } from "./api";', replace), { mode: "transform" })).toString("base64")}`
  );
const flow = await load(
  "../../src/shared/tagFollows.ts",
  "const request = (...args) => globalThis.followTestRequest(...args);",
);
const catalog = await load(
  "../../src/shared/tagCatalog.ts",
  "const request = () => {};",
);
test("follow filter is directory-only; switching preserves conditions and resets page", () => {
  const search =
    "?category=content&namespace=female&q=long&sort=usage&order=desc&page=3&followed_only=true";
  assert.equal(catalogInput(search, "L").followed_only, undefined);
  assert.equal(tagDirectoryInput(search, "L").followed_only, true);
  assert.equal(
    tagDirectoryInput("?followed_only=false", "L").followed_only,
    undefined,
  );
  assert.equal(tagWorkInput(search, "L").followed_only, undefined);
  for (const suffix of ["/x", "/x/works", "/x/editions", "/x/relations"])
    for (const followed_only of [true, false])
      assert.equal(
        new URL(
          catalog.tagCatalogPath(`/tag-catalog${suffix}`, {
            library_id: "L",
            followed_only,
          }),
          "http://anon",
        ).searchParams.has("followed_only"),
        false,
      );
  const q = new URL(
    tagWorkspaceURL(search, { followed_only: null }),
    "http://anon",
  ).searchParams;
  assert.equal(q.has("page"), false);
  for (const key of ["category", "namespace", "q", "sort", "order"])
    assert.equal(q.get(key), new URLSearchParams(search).get(key));
});
test("missing fields and dictionary-only identities cannot invent a follow revision", () => {
  for (const value of [
    { id: "a" },
    { id: "", followed: false, follow_revision: 0 },
    { id: "a", followed: false, follow_revision: -1 },
    { id: "a", followed: false, follow_revision: NaN },
  ])
    assert.equal(flow.followValue(value), undefined);
  assert.deepEqual(
    flow.followValue({ id: "a", followed: false, follow_revision: 2 }),
    { tag_id: "a", followed: false, revision: 2 },
  );
  assert.throws(() =>
    flow.followChange({
      tag_id: "a",
      followed: false,
      revision: Number.MAX_SAFE_INTEGER,
    }),
  );
});
test("writes freeze the tag and tombstone revision, never send account or library", async () => {
  const base = { tag_id: "a/b", followed: false, revision: 2 };
  assert.deepEqual(flow.followChange(base), {
    path: "/tags/a%2Fb/follow",
    method: "PUT",
    body: { expected_revision: 2 },
  });
  assert.equal(flow.followChange({ ...base, followed: true }).method, "DELETE");
  globalThis.followTestRequest = async (path, method, body) => {
    assert.equal(path, "/tags/a%2Fb/follow");
    assert.equal(method, "PUT");
    assert.deepEqual(body, { expected_revision: 2 });
    return { tag_id: "a/b", followed: true, revision: 3 };
  };
  assert.deepEqual(await flow.changeTagFollow(base), {
    tag_id: "a/b",
    followed: true,
    revision: 3,
  });
  for (const response of [
    undefined,
    { tag_id: "wrong", followed: true, revision: 3 },
    { tag_id: "a/b", followed: false, revision: 3 },
    { tag_id: "a/b", followed: true, revision: 2 },
  ]) {
    globalThis.followTestRequest = async () => response;
    await assert.rejects(flow.changeTagFollow(base), /重新读取/);
  }
  globalThis.followTestRequest = async () => {
    throw { status: 409, code: "tag_follow_revision_conflict" };
  };
  await assert.rejects(flow.changeTagFollow(base), (e) => e.status === 409);
  assert.equal(base.revision, 2);
  delete globalThis.followTestRequest;
});
