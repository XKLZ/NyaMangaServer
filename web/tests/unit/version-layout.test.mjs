import assert from "node:assert/strict";
import test from "node:test";
import { versionLayout } from "../../src/admin/versions/versionLayout.ts";
test("version workspace uses its available container width", () => {
  for (const width of [0, 390, 799])
    assert.equal(versionLayout(width), "mobile");
  for (const width of [800, 1006, 1160, 1239])
    assert.equal(versionLayout(width), "drawer");
  for (const width of [1240, 1320, 1500])
    assert.equal(versionLayout(width), "wide");
  // One viewport can use a drawer or three columns as navigation changes.
  assert.equal(versionLayout(1440 - 218 - 56), "drawer");
  assert.equal(versionLayout(1440 - 64 - 56), "wide");
});
