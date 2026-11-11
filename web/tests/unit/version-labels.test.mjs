import assert from "node:assert/strict";
import test from "node:test";
import {
  validateVersionLabels,
  versionLabels,
} from "../../src/shared/versionLabels.ts";

test("ordered version labels preserve composites and deduplicate without guessing", () => {
  assert.deepEqual(validateVersionLabels(["中文", "彩色", "中文"]), [
    "中文",
    "彩色",
  ]);
  assert.deepEqual(versionLabels({ version_label: "中文,彩色" }), [
    "中文,彩色",
  ]);
  assert.deepEqual(
    versionLabels({ version_labels: [], version_label: "old" }),
    [],
  );
  assert.deepEqual(validateVersionLabels([]), []);
  for (const invalid of [
    [" "],
    ["x\0"],
    ["字".repeat(129)],
    Array.from({ length: 21 }, (_, i) => String(i)),
  ])
    assert.throws(() => validateVersionLabels(invalid));
});

test("discovery failures use bounded user-facing reasons", async () => {
  const { discoveryFailure } =
    await import("../../src/shared/discoveryStatus.ts");
  assert.match(discoveryFailure("discovery_worker_unavailable"), /检查点/);
  assert.match(discoveryFailure("discovery_failed"), /旧记录/);
  assert.doesNotMatch(discoveryFailure("private-secret"), /private-secret/);
});
