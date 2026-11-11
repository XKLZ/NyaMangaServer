import test from "node:test";
import assert from "node:assert/strict";
import {
  canStartRecommendation,
  togglePatch,
  featureForJob,
  featureForStage,
  featureStatus,
  resourceLabel,
  enhancementError,
  nextImportStage,
} from "../../src/shared/enhancementFlow.ts";
const f = {
  enabled: true,
  effective_enabled: true,
  state: "stable",
  affected_tasks: 2,
  impact_token: "frozen-impact",
  resource_state: "configured_unverified",
  transition: null,
};
test("configured but unverified resources permit explicit discovery only with effective permission", () => {
  assert.equal(canStartRecommendation(f, true), true);
  assert.equal(
    canStartRecommendation({ ...f, effective_enabled: false }, true),
    false,
  );
  for (const state of ["enabling", "disabling", "failed"])
    assert.equal(
      canStartRecommendation({ ...f, state, effective_enabled: false }, true),
      false,
    );
  assert.equal(canStartRecommendation(f, false), false);
  for (const resource_state of ["not_configured", "unavailable", "unknown"])
    assert.equal(canStartRecommendation({ ...f, resource_state }, true), false);
  assert.equal(
    canStartRecommendation({ ...f, resource_state: "unverified" }, true),
    true,
  );
  assert.equal(resourceLabel(f.resource_state), "已配置，运行时验证");
});
test("toggle payload freezes revision and task impact, ordinary switches need no pause confirmation", () => {
  const data = { revision: 5, features: { work_recommendations: { ...f } } };
  const patch = togglePatch(data, "work_recommendations", false);
  data.revision = 6;
  data.features.work_recommendations.impact_token = "later";
  assert.deepEqual(patch, {
    expected_revision: 5,
    feature: "work_recommendations",
    enabled: false,
    confirm_pause: true,
    impact_token: "frozen-impact",
  });
  assert.deepEqual(
    togglePatch(
      {
        revision: 7,
        features: { work_recommendations: { ...f, affected_tasks: 0 } },
      },
      "work_recommendations",
      false,
    ),
    { expected_revision: 7, feature: "work_recommendations", enabled: false },
  );
  assert.equal(
    "confirm_pause" in togglePatch(data, "work_recommendations", true),
    false,
  );
});
test("transition, dictionary and errors do not imply usable resources or completed switching", () => {
  assert.equal(featureStatus({ ...f, state: "failed" }), "切换失败");
  assert.equal(featureStatus({ ...f, state: "enabling" }), "开启收尾中");
  assert.equal(featureStatus({ ...f, state: "disabling" }), "关闭收尾中");
  assert.equal(resourceLabel("not_downloaded"), "尚未安装译库");
  assert.equal(resourceLabel("disabled"), "译库未启用");
  assert.match(enhancementError("enhancement_impact_changed"), /影响已变化/);
  assert.match(enhancementError("enhancement_storage_unavailable"), /不|核对/);
  assert.equal(
    enhancementError("unknown-secret-worker-detail").includes("secret"),
    false,
  );
});
test("task and import gates map only controlled feature types", () => {
  assert.equal(featureForJob("work_recommendation"), "work_recommendations");
  assert.equal(
    featureForJob("series_recommendation"),
    "series_recommendations",
  );
  assert.equal(featureForJob("tag_translation"), "chinese_dictionary");
  assert.equal(featureForJob("metadata"), undefined);
  assert.equal(featureForStage("metadata"), undefined);
  assert.equal(featureForStage("versions"), "work_recommendations");
  assert.equal(nextImportStage(undefined, "metadata"), "versions");
  assert.equal(
    nextImportStage(
      { versions: { enabled: false }, series: { enabled: true } },
      "metadata",
    ),
    "series",
  );
  assert.equal(
    nextImportStage(
      { versions: { enabled: false }, series: { enabled: false } },
      "metadata",
    ),
    "finished",
  );
  assert.equal(
    nextImportStage({ series: { enabled: false } }, "versions"),
    "finished",
  );
});
