import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import {
  tagImageMedia,
  tagImageContent,
  tagImageLimit,
} from "../../src/admin/tags/tagImageInput.ts";
import { writesPending } from "../../src/shared/writeState.ts";
test("tag images identify actual headers regardless of empty or misleading MIME and names", async () => {
  for (const [bytes, expected] of [
    [[137, 80, 78, 71, 13, 10, 26, 10], "image/png"],
    [[255, 216, 255], "image/jpeg"],
    [[66, 77, 0, 0], "image/bmp"],
    [new TextEncoder().encode("RIFFxxxxWEBPVP8 "), "image/webp"],
  ]) {
    for (const type of ["", "application/octet-stream", "image/wrong"])
      assert.equal(
        await tagImageMedia(
          new File([new Uint8Array(bytes)], "untrusted.svg", { type }),
        ),
        expected,
      );
  }
  for (const bytes of [
    new Uint8Array(),
    new TextEncoder().encode("GIF89a"),
    new TextEncoder().encode("<svg/>"),
    new TextEncoder().encode("AVIF"),
  ])
    await assert.rejects(tagImageMedia(new Blob([bytes])));
  await assert.rejects(
    tagImageMedia(new Blob([new Uint8Array(tagImageLimit + 1)])),
    /10 MiB/,
  );
});
test("display paths require an asset and positive frozen revision, never use asset paths", () => {
  assert.equal(
    tagImageContent("a/b", {
      role: "foreground",
      asset_id: "private-key",
      revision: 7,
    }),
    "/api/v1/tag-catalog/a%2Fb/images/foreground/content?revision=7",
  );
  for (const image of [
    undefined,
    { asset_id: null, revision: 2 },
    { asset_id: "id", revision: 0 },
    { asset_id: "id", revision: NaN },
  ])
    assert.equal(tagImageContent("tag", image), "");
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
const apiModule = await import(
  `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source, { mode: "transform" })).toString("base64")}`
);
test("raw PUT and bodyless DELETE share CSRF, errors and the submission lock", async () => {
  const prior = globalThis.fetch;
  const priorWindow = globalThis.window;
  const events = [];
  globalThis.window = { dispatchEvent: (event) => events.push(event.type) };
  try {
    globalThis.fetch = async () =>
      Response.json({ csrf_token: "anonymous-csrf" });
    await apiModule.api.login("anonymous");
    const file = new File(
      [new Uint8Array([137, 80, 78, 71])],
      "anonymous.png",
      { type: "" },
    );
    let release;
    const gate = new Promise((r) => {
      release = r;
    });
    globalThis.fetch = async (path, options) => {
      assert.equal(path, "/api/v1/tag-catalog/t/images/foreground");
      assert.equal(options.method, "PUT");
      assert.equal(options.credentials, "same-origin");
      assert.equal(options.headers["X-CSRF-Token"], "anonymous-csrf");
      assert.equal(options.headers["X-Expected-Revision"], "3");
      assert.equal(options.headers["Content-Type"], "image/png");
      assert.equal(options.body, file);
      assert.equal(writesPending(), true);
      await gate;
      return Response.json({ revision: 4 });
    };
    const pending = apiModule.requestBinary(
      "/tag-catalog/t/images/foreground",
      "PUT",
      3,
      file,
      "image/png",
    );
    assert.equal(writesPending(), true);
    release();
    assert.deepEqual(await pending, { revision: 4 });
    assert.equal(writesPending(), false);
    globalThis.fetch = async (_, options) => {
      assert.equal(options.method, "DELETE");
      assert.equal(options.body, undefined);
      assert.equal(options.headers["Content-Type"], undefined);
      return Response.json(
        { error: { code: "tag_image_revision_conflict", message: "冲突" } },
        { status: 409 },
      );
    };
    await assert.rejects(
      apiModule.requestBinary("/tag-catalog/t/images/background", "DELETE", 8),
      (e) => e.status === 409 && e.code === "tag_image_revision_conflict",
    );
    assert.equal(writesPending(), false);
    globalThis.fetch = async () => {
      throw new Error("offline");
    };
    await assert.rejects(
      apiModule.requestBinary("/x", "DELETE", 0),
      (e) => e.code === "network_error",
    );
    assert.equal(writesPending(), false);
    globalThis.fetch = async () =>
      Response.json({ error: { code: "unauthenticated" } }, { status: 401 });
    await assert.rejects(apiModule.requestBinary("/x", "DELETE", 0));
    assert.deepEqual(events, ["session-expired"]);
    await assert.rejects(apiModule.requestBinary("/x", "DELETE", -1), /版本/);
  } finally {
    globalThis.fetch = prior;
    globalThis.window = priorWindow;
  }
});

test("only conflicts require adopting another revision, while network and storage retry frozen values", async () => {
  const { imageFailureKind } =
    await import("../../src/admin/tags/tagImageInput.ts");
  assert.equal(imageFailureKind({ status: 409 }), "conflict");
  for (const status of [0, 500, 502, 503, 504])
    assert.equal(imageFailureKind({ status }), "retryable");
  for (const status of [400, 413, 415])
    assert.equal(imageFailureKind({ status }), "invalid");
  assert.equal(imageFailureKind(new Error("invalid file")), "invalid");
});
