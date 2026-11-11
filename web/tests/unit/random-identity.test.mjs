import test from "node:test";
import assert from "node:assert/strict";
import { randomIdentity } from "../../src/shared/randomIdentity.ts";

test("uses native randomUUID with its receiver in secure contexts", () => {
  const source = {
    randomUUID() {
      assert.equal(this, source);
      return "native-identity";
    },
    getRandomValues() {
      assert.fail("fallback should not run");
    },
  };
  assert.equal(randomIdentity(source), "native-identity");
});

test("LAN HTTP fallback uses 16 secure bytes and fixes v4/variant bits", () => {
  for (const fill of [0, 255]) {
    let calls = 0;
    const source = {
      getRandomValues(bytes) {
        assert.equal(this, source);
        assert.equal(bytes.length, 16);
        calls++;
        return bytes.fill(fill);
      },
    };
    const identity = randomIdentity(source);
    assert.match(
      identity,
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
    assert.equal(
      identity,
      fill
        ? "ffffffff-ffff-4fff-bfff-ffffffffffff"
        : "00000000-0000-4000-8000-000000000000",
    );
    assert.equal(calls, 1);
  }
});

test("does not replace missing secure randomness with weak identities", () => {
  assert.throws(() => randomIdentity({}), /安全随机数/);
});
