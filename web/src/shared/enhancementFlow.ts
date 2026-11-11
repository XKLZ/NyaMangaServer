/** Configuration permission and resource readiness are independent. A read never
 * validates a model or starts work; only an explicit operation can do that. */
export type FeatureKey =
  | "work_recommendations"
  | "series_recommendations"
  | "chinese_dictionary"
  | "local_gallery_highlight";
export type FeatureState = {
  enabled: boolean;
  effective_enabled: boolean;
  state: "stable" | "enabling" | "disabling" | "failed";
  affected_tasks: number;
  impact_token: string;
  resource_state: string;
  transition: null | {
    id: string;
    feature: FeatureKey;
    attempt: number;
    state: "pending" | "completed" | "failed";
    target_enabled: boolean;
    error_code: string;
    affected_tasks: number;
    created_at: number;
    updated_at: number;
  };
};
export type Enhancements = {
  revision: number;
  features: Record<
    Exclude<FeatureKey, "local_gallery_highlight">,
    FeatureState
  > & { local_gallery_highlight?: FeatureState };
};
export type EnhancementPatch = {
  expected_revision: number;
  feature: FeatureKey;
} & (
  | {
      enabled: boolean;
      confirm_pause?: true;
      impact_token?: string;
      retry_transition_id?: never;
    }
  | {
      retry_transition_id: string;
      enabled?: never;
      confirm_pause?: never;
      impact_token?: never;
    }
);
export type ServiceInfo = {
  version: string;
  commit: string;
  started_at: string;
  uptime_seconds: number;
  os: string;
  architecture: string;
  database_schema: number;
  database_baseline: string;
  state: string;
};
export const featureNames: Record<FeatureKey, string> = {
  work_recommendations: "版本推荐",
  series_recommendations: "系列推荐",
  chinese_dictionary: "中文译库",
  local_gallery_highlight: "本地画廊高亮",
};
export const enhancementSettingsURL = (feature?: FeatureKey) =>
  `/admin/settings?section=enhancements${feature ? `&focus=${feature}` : ""}`;
export function featureForJob(type?: string): FeatureKey | undefined {
  return (
    {
      work_recommendation: "work_recommendations",
      series_recommendation: "series_recommendations",
      tag_translation: "chinese_dictionary",
    } as Record<string, FeatureKey>
  )[type || ""];
}
export function featureForStage(stage: string): FeatureKey | undefined {
  return stage === "versions"
    ? "work_recommendations"
    : stage === "series"
      ? "series_recommendations"
      : undefined;
}
export function featureStatus(feature: FeatureState) {
  return feature.state === "failed"
    ? "切换失败"
    : feature.state === "enabling"
      ? "开启收尾中"
      : feature.state === "disabling"
        ? "关闭收尾中"
        : feature.enabled
          ? "已开启"
          : "未启用";
}
export function resourceLabel(state?: string, feature?: FeatureKey) {
  if (feature === "local_gallery_highlight" && state === "ready")
    return "无需额外资源";
  return (
    (
      {
        configured_unverified: "已配置，运行时验证",
        unverified: "已配置，运行时验证",
        not_configured: "资源尚未配置",
        unavailable: "资源暂不可用",
        ready: "译库可用",
        not_downloaded: "尚未安装译库",
        restoring: "正在离线恢复",
        disabled: "译库未启用",
      } as Record<string, string>
    )[state || ""] || "资源状态待确认"
  );
}
export function canStartRecommendation(
  feature: FeatureState | undefined,
  supported: boolean,
  resource?: string,
) {
  return (
    !!feature?.effective_enabled &&
    supported &&
    ["configured_unverified", "unverified", "ready"].includes(
      resource || feature.resource_state,
    )
  );
}
export function togglePatch(
  data: Enhancements,
  feature: FeatureKey,
  enabled: boolean,
): EnhancementPatch {
  const current = data.features[feature];
  if (!current) throw Error("服务尚未支持此功能，请升级后刷新");
  return {
    expected_revision: data.revision,
    feature,
    enabled,
    ...(!enabled &&
    feature !== "local_gallery_highlight" &&
    current.affected_tasks > 0
      ? { confirm_pause: true as const, impact_token: current.impact_token }
      : {}),
  };
}
export function enhancementError(code: string) {
  return (
    (
      {
        enhancement_revision_conflict:
          "增强设置已变化，请核对当前状态后重新确认。",
        enhancement_impact_changed:
          "任务影响已变化，请核对新的任务数量后重新确认。",
        enhancement_transition_conflict: "切换状态已变化，请核对后重试。",
        enhancement_storage_unavailable:
          "设置存储暂不可用，正在重新读取持久状态；请核对结果后再操作。",
        enhancement_disabled: "所需增强功能未启用，请先在增强设置中开启。",
        drain_failed: "任务收尾未完成，功能仍不可执行。",
        offline_restore_unavailable: "译库离线恢复未完成，功能仍不可执行。",
      } as Record<string, string>
    )[code] || "增强设置操作未完成，请核对当前状态后重试。"
  );
}

/** Frozen import flags select the next stage; old batches without flags retain
 * both historical review gates. Enabling later cannot add a skipped stage. */
export function nextImportStage(
  flags: Record<string, { enabled: boolean }> | undefined,
  after: "metadata" | "versions",
) {
  if (after === "metadata" && flags?.versions?.enabled !== false)
    return "versions";
  return flags?.series?.enabled !== false ? "series" : "finished";
}
