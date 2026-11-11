import assert from "node:assert/strict";
import test from "node:test";
import {
  matchingIntent,
  sourceDraftDirty,
} from "../../src/admin/sourceFlow.ts";
import {
  canLoadPages,
  coverCommand,
  coverPageSize,
  loadCoverImages,
  validateCoverFile,
} from "../../src/admin/coverFlow.ts";

test("rematch mode preserves cache intent for automatic only", () => {
  assert.deepEqual(matchingIntent("automatic", "unfinished", false), {
    book_urls: {},
    force_refresh: false,
  });
  assert.equal(matchingIntent("automatic", "", true).force_refresh, true);
  assert.deepEqual(
    matchingIntent("gallery", " https://e-hentai.org/g/1/0123456789/ ", true),
    {
      gallery_url: "https://e-hentai.org/g/1/0123456789/",
      force_refresh: false,
    },
  );
  assert.throws(() =>
    matchingIntent("gallery", "https://example.com/g/1/123/", false),
  );
});
test("unsubmitted URL draft stays protected while changing modes or obtaining an older preview", () => {
  assert.equal(sourceDraftDirty("unfinished", ""), true);
  assert.equal(sourceDraftDirty("new url", "old url"), true);
  assert.equal(sourceDraftDirty("submitted", "submitted"), false);
  assert.equal(sourceDraftDirty("", "submitted"), false);
});
test("source page loading is gated and bounded to eight entries", () => {
  const book = { page_count: 50 };
  assert.equal(coverPageSize, 8);
  assert.equal(canLoadPages(book, true, false), false);
  assert.equal(canLoadPages(book, false, true), false);
  assert.equal(
    canLoadPages({ ...book, library_disabled: true }, true, true),
    false,
  );
  assert.equal(canLoadPages({ ...book, trashed_at: 1 }, true, true), false);
  assert.equal(canLoadPages(book, true, true), true);
});
test("cover candidates retain captured versions and never silently rebase", () => {
  const candidate = {
    mode: "page",
    page_index: 3,
    expected_revision: 4,
    content_revision: 9,
    url: "blob:anonymous",
    name: "3.jpg",
  };
  assert.deepEqual(coverCommand(candidate, 9), {
    mode: "page",
    page_index: 3,
    expected_revision: 4,
    content_revision: 9,
  });
  assert.throws(() => coverCommand(candidate, 10), /内容已变化/);
  assert.equal(candidate.expected_revision, 4);
  assert.deepEqual(coverCommand({ ...candidate, mode: "asset" }, 9), {
    mode: "asset",
    expected_revision: 4,
  });
});
test("staged upload validates format and size before application", () => {
  for (const type of ["image/jpeg", "image/png", "image/webp"])
    assert.doesNotThrow(() =>
      validateCoverFile({ type, size: 10 * 1024 * 1024 }),
    );
  assert.throws(
    () => validateCoverFile({ type: "image/gif", size: 1 }),
    /仅支持/,
  );
  assert.throws(
    () => validateCoverFile({ type: "image/png", size: 10 * 1024 * 1024 + 1 }),
    /10 MiB/,
  );
});
test("cover workers cap concurrency at two and publish independent image errors", async () => {
  let running = 0,
    maximum = 0;
  const published = [];
  await loadCoverImages(
    Array.from({ length: 8 }, (_, i) => i),
    new AbortController().signal,
    async (i) => {
      running++;
      maximum = Math.max(maximum, running);
      await Promise.resolve();
      running--;
      if (i === 3) throw new Error("anonymous failure");
      return `blob:${i}`;
    },
    (i, result) => published.push([i, result]),
    () => assert.fail("active response should not be discarded"),
  );
  assert.equal(maximum, 2);
  assert.equal(published.length, 8);
  assert.equal(published.find(([i]) => i === 3)[1].error, "anonymous failure");
});
test("cancelled page does not start queued reads or publish late results, releases URLs", async () => {
  const controller = new AbortController();
  const pending = [];
  const discarded = [];
  const load = loadCoverImages(
    [1, 2, 3, 4],
    controller.signal,
    (i) => new Promise((resolve) => pending.push(() => resolve(`blob:${i}`))),
    () => assert.fail("stale result"),
    (url) => discarded.push(url),
  );
  assert.equal(pending.length, 2);
  controller.abort();
  pending.forEach((resolve) => resolve());
  await load;
  assert.deepEqual(discarded.sort(), ["blob:1", "blob:2"]);
});

test("BMP uploads are accepted under the existing upload size budget", () => {
  for (const type of ["image/bmp", "image/x-ms-bmp"])
    assert.doesNotThrow(() => validateCoverFile({ type, size: 1024 }));
  assert.throws(() =>
    validateCoverFile({ type: "image/bmp", size: 10 * 1024 * 1024 + 1 }),
  );
});
