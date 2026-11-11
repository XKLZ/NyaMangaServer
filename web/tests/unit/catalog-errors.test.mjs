import test from "node:test";
import assert from "node:assert/strict";
import { reason } from "../../src/shared/catalog.ts";

test("series storage failures and unknown failures never blame source files", () => {
  assert.match(reason("series_publication_failed"), /方案保存失败/);
  assert.match(reason("series_evidence_finalize_failed"), /收尾失败/);
  for (const code of [
    "series_discovery_failed",
    "series_publication_failed",
    "series_evidence_finalize_failed",
    "series_checkpoint_invalid",
    "unknown_future_error",
  ]) {
    assert.doesNotMatch(reason(code), /源文件/);
  }
  assert.match(reason("file_missing"), /源文件已缺失/);
  assert.match(reason("invalid_archive"), /归档损坏/);
});
