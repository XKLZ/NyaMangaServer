import test from "node:test";
import assert from "node:assert/strict";
import {
  isBusinessWrite,
  subscribeWrites,
  trackWrite,
  writesPending,
} from "../../src/shared/writeState.ts";
test("writes lock scope until all concurrent work settles and notify subscribers", async () => {
  const events = [];
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  const unsubscribe = subscribeWrites(() => events.push(writesPending()));
  const first = trackWrite(async () => {
    await gate;
    return "saved";
  });
  assert.equal(writesPending(), true);
  const second = trackWrite(async () => {
    throw Error("anonymous failure");
  });
  await assert.rejects(second);
  assert.equal(writesPending(), true);
  release();
  assert.equal(await first, "saved");
  assert.equal(writesPending(), false);
  assert.deepEqual(events, [true, true, true, false]);
  unsubscribe();
});
test("search POSTs remain reads and failed or aborted writes release the lock", async () => {
  assert.equal(isBusinessWrite("/admin/books/search", "POST"), false);
  assert.equal(isBusinessWrite("/books/search", "POST"), false);
  assert.equal(isBusinessWrite("/libraries", "GET"), false);
  assert.equal(isBusinessWrite("/anonymous/overrides", "PATCH"), true);
  assert.equal(isBusinessWrite("/anonymous/cover", "POST"), true);
  await trackWrite(async () => {
    assert.equal(writesPending(), false);
  }, false);
  await assert.rejects(
    trackWrite(async () => {
      throw new DOMException("Aborted", "AbortError");
    }),
  );
  assert.equal(writesPending(), false);
});
