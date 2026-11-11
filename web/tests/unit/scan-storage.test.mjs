import test from "node:test";
import assert from "node:assert/strict";
import {
  cleanupOptions,
  suggestedCleanup,
  bytesText,
  cleanupOutcome,
  cleanupFailure,
} from "../../src/shared/storageFlow.ts";
import {
  encodingCommand,
  pageIdentity,
  scanLimitMessage,
} from "../../src/shared/scanFlow.ts";
import { coverCommand } from "../../src/admin/coverFlow.ts";
import {
  readingRequest,
  readingContentChanged,
  readingProgress,
} from "../../src/shared/readingFlow.ts";
import { healthRecoveryActions } from "../../src/shared/healthFlow.ts";
const operations = {
  "instance:a": {
    id: "instance:a",
    kind: "instance_recovery",
    cleanable_files: 1,
    reasons: { finished_instance_material: 1 },
  },
  "instance:b": {
    id: "instance:b",
    kind: "instance_recovery",
    cleanable_files: 1,
    reasons: { removes_rollback_point: 1 },
  },
  "health:c": {
    id: "health:c",
    kind: "health_rollback",
    cleanable_files: 1,
    reasons: {},
  },
};
test("cleanup never sends an empty operation selection as all materials", () => {
  assert.deepEqual(
    cleanupOptions(
      ["instance_recovery", "health_rollback", "tmp"],
      [],
      operations,
    ),
    { categories: ["tmp"], operation_ids: [] },
  );
  assert.deepEqual(
    cleanupOptions(
      ["instance_recovery", "health_rollback"],
      ["instance:a", "unknown"],
      operations,
    ),
    { categories: ["instance_recovery"], operation_ids: ["instance:a"] },
  );
  const defaults = suggestedCleanup({
    operations,
    suggested_categories: [
      "instance_recovery",
      "health_rollback",
      "covers",
      "recommendation_indexes",
      "tmp",
      "unknown",
    ],
  });
  assert.deepEqual(defaults, {
    categories: ["instance_recovery", "health_rollback", "tmp"],
    operation_ids: ["instance:a", "health:c"],
  });
  assert.match(bytesText(512 * 1024 * 1024), /512.*MiB/);
});
const base = {
  revision: 4,
  content_revision: 8,
  parsing_revision: 2,
  inherited_encoding: "utf8",
  candidates: [
    { encoding: "utf8", valid: false },
    { encoding: "shift_jis", valid: true, directory_digest: "frozen" },
  ],
};
test("encoding freezes revision and digest, inherit must have a valid preview", () => {
  assert.deepEqual(encodingCommand(base, "shift_jis"), {
    expected_revision: 4,
    content_revision: 8,
    parsing_revision: 2,
    encoding: "shift_jis",
    directory_digest: "frozen",
  });
  assert.throws(() => encodingCommand(base, "inherit"), /不能解析/);
  assert.equal(
    encodingCommand({ ...base, inherited_encoding: "shift_jis" }, "inherit")
      .encoding,
    null,
  );
  assert.equal(base.revision, 4);
});
test("page reads and page cover commands carry actual parsing generation", () => {
  assert.equal(
    pageIdentity({ content_revision: 8, file: { parsing_revision: 3 } }),
    "content_revision=8&parsing_revision=3",
  );
  assert.equal(
    pageIdentity({ content_revision: 8 }),
    "content_revision=8&parsing_revision=1",
  );
  const candidate = {
    mode: "page",
    content_revision: 8,
    parsing_revision: 3,
    expected_revision: 4,
    page_index: 2,
  };
  assert.equal(coverCommand(candidate, 8, 3).parsing_revision, 3);
  assert.throws(() => coverCommand(candidate, 8, 4), /页序已变化/);
  assert.equal(candidate.parsing_revision, 3);
  assert.equal(
    coverCommand({ ...candidate, mode: "asset" }, 8, 4).parsing_revision,
    undefined,
  );
});
test("reading distinguishes content and parsing change and keeps retry identity", () => {
  const state = {
    revision: 4,
    content_revision: 8,
    current_content_revision: 8,
    current_parsing_revision: 3,
    parsing_stale: true,
    stale: true,
    recorded_page_count: 12,
    current_page_count: 10,
    page_index: 8,
  };
  assert.equal(readingContentChanged(state), false);
  const body = readingRequest(state, "reset", "fixed", false, true);
  assert.equal(body.parsing_revision, 3);
  assert.equal(body.accept_parsing_change, true);
  assert.equal(body.accept_content_change, undefined);
  assert.deepEqual(readingRequest(state, "reset", "fixed", false, true), body);
  assert.match(readingProgress(state), /9.*12/);
  assert.equal(
    readingRequest(
      { ...state, current_parsing_revision: undefined },
      "reset",
      "legacy",
    ).parsing_revision,
    undefined,
  );
});
test("cleaned health materials cannot offer rollback even for completed history", () => {
  assert.equal(
    healthRecoveryActions(
      { phase: "idle" },
      { state: "completed", materials_cleaned: true },
      "completed",
    ).rollback,
    false,
  );
  assert.equal(
    healthRecoveryActions(
      { phase: "idle" },
      { state: "completed" },
      "completed",
    ).rollback,
    true,
  );
});

test("resource diagnoses use stage and budget without calling encoding or memory corruption", () => {
  assert.equal(
    scanLimitMessage({
      stage: "cover",
      resource: "estimated_memory_bytes",
      actual: 100,
      limit: 32,
    }),
    "封面解码所需内存超过限制",
  );
  assert.equal(
    scanLimitMessage({ stage: "cover", code: "resource_limit" }),
    null,
  );
});

test("cleanup distinguishes unlink completion from finalization and old results", () => {
  const summary = {
    storage: { planned: { files: 2 } },
    cleanup_result: {
      next_index: 2,
      deleted_files: 2,
      skipped_files: 0,
      failed_files: 0,
      released_bytes: 12,
    },
  };
  assert.equal(
    cleanupOutcome(summary, { state: "failed", error_code: "preview_expired" }),
    "删除已完成，任务收尾失败",
  );
  assert.equal(cleanupOutcome(summary, { state: "completed" }), "清理全部完成");
  assert.equal(
    cleanupOutcome(
      {
        ...summary,
        cleanup_result: { ...summary.cleanup_result, skipped_files: 1 },
      },
      { state: "completed" },
    ),
    "清理完成，部分项目已跳过",
  );
  assert.equal(cleanupOutcome(undefined, { state: "failed" }), "清理执行失败");
  assert.equal(cleanupOutcome(summary, { state: "paused" }), "清理已暂停");
  assert.match(cleanupFailure("preview_expired"), /不要重复/);
  assert.match(cleanupFailure("unknown_failure"), /任务诊断/);
});
