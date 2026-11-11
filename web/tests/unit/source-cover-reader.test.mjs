import test from "node:test";
import assert from "node:assert/strict";
import { readSourceCover } from "../../src/admin/sourceCoverFlow.ts";

test("source cover reads use the same-origin proxy, frozen path and abort signal", async () => {
  const controller = new AbortController();
  let requested;
  const path =
    "/api/v1/import-batches/anonymous/results/one/cover?result_revision=3";
  const url = await readSourceCover(
    path,
    controller.signal,
    async (value, options) => {
      requested = { value, options };
      return new Response(
        new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }),
      );
    },
  );
  try {
    assert.equal(requested.value, path);
    assert.equal(requested.options.credentials, "same-origin");
    assert.equal(requested.options.signal, controller.signal);
    assert.match(url, /^blob:/);
  } finally {
    URL.revokeObjectURL(url);
  }
});
test("source cover failures distinguish revision conflicts from availability", async () => {
  for (const [status, text] of [
    [409, /候选已变化/],
    [503, /封面暂不可用/],
  ]) {
    await assert.rejects(
      readSourceCover(
        "/anonymous",
        new AbortController().signal,
        async () => new Response(null, { status }),
      ),
      text,
    );
  }
});
test("source cover rejects invalid media types and oversized PNG bodies", async () => {
  for (const blob of [
    new Blob(["wrong"], { type: "text/html" }),
    new Blob([new Uint8Array(2 * 1024 * 1024 + 1)], { type: "image/png" }),
  ]) {
    await assert.rejects(
      readSourceCover(
        "/anonymous",
        new AbortController().signal,
        async () => new Response(blob),
      ),
      /封面响应无效/,
    );
  }
});
test("aborted source cover reads never produce a URL from a late body", async () => {
  const controller = new AbortController();
  await assert.rejects(
    readSourceCover("/anonymous", controller.signal, async () => {
      controller.abort();
      return new Response(new Blob(["anonymous"], { type: "image/png" }));
    }),
    { name: "AbortError" },
  );
});
