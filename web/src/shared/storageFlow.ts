export type StorageCount = {
  kind: string;
  group?: string;
  bytes: number;
  files: number;
  cleanable_bytes: number;
  cleanable_files: number;
  protected_bytes: number;
  protected_files: number;
  reasons: Record<string, number>;
};
export type StorageOperation = StorageCount & {
  id: string;
  operation_id: string;
  state: string;
  revision: number;
};
export type Storage = {
  categories: StorageCount[];
  groups?: StorageCount[];
  operations?: Record<string, StorageOperation>;
  total_bytes?: number;
  cleanable_bytes?: number;
  protected_bytes?: number;
  statistics_at?: number;
  suggested_categories?: string[];
};
export const storageGroups: Record<string, { name: string; purpose: string }> =
  {
    long_term: {
      name: "长期资料",
      purpose: "资料库、漫画封面、标签图片、已安装译库与用户备份",
    },
    rebuildable: {
      name: "可重建缓存",
      purpose: "加快图片展示、来源比较与推荐查找",
    },
    temporary: { name: "任务临时文件", purpose: "上传和处理中的临时内容" },
    recovery: {
      name: "恢复与回退材料",
      purpose: "恢复工作副本、升级快照及健康修复回退材料",
    },
  };
export const storageNames: Record<string, string> = {
  database: "资料库",
  assets: "漫画封面与标签素材",
  user_backups: "用户备份",
  translation_cache: "已安装标签译库",
  tag_translations: "已安装标签译库",
  covers: "全部封面缓存",
  tag_images: "全部标签展示缓存",
  redundant_covers: "冗余封面缓存",
  redundant_tag_images: "冗余标签展示缓存",
  import_preview_cache: "候选展示缓存",
  recommendation_indexes: "推荐特征缓存",
  tmp: "过期临时文件",
  snapshots: "旧升级快照",
  instance_recovery: "恢复工作副本",
  health_rollback: "健康修复回退材料",
  migration_originals: "已核验的迁移原图副本",
  orphan_assets: "无引用素材",
  terminal_jobs: "已结束任务",
  other_application_data: "其他应用数据",
};
export const storageReasons: Record<string, string> = {
  retained_application_data: "应用资料与配置受保护",
  valid_cache: "正在使用的展示缓存",
  redundant_cache: "已无用途的展示缓存",
  retained_recommendation_evidence: "推荐记录与冻结证据受保护",
  valid_feature_cache: "有效特征，清理后需重新计算",
  referenced_asset: "正在使用的长期素材",
  unreferenced_asset: "已无引用的素材",
  archive_verification_required: "外部档案尚未核验，暂不能清理",
  verified_external_original: "外部档案已核验，原图副本无当前引用",
  recent_upload: "近期上传，暂时保留",
  active_or_recent_temporary: "任务仍在使用或文件尚未过期",
  expired_temporary: "已过期的临时内容",
  latest_upgrade_snapshot: "保留最近一次升级快照与说明",
  older_upgrade_snapshot: "较早的升级回退点",
  finished_health_rollback: "已结束修复的回退材料",
  user_backup: "完整用户备份长期保留",
  active_instance_operation: "恢复或备份尚未结束",
  finished_instance_material: "已结束恢复的工作副本",
  unconfirmed_offline_staging: "已识别的离线恢复暂存",
  removes_rollback_point: "清理后将失去仍有效的实例回退点",
  pending_write: "文件正在写入",
  unknown_data: "未知应用数据保持不变",
};
export const storageReason = (key: string) =>
  storageReasons[key] || "受保护的应用资料";
export const bytesText = (n = 0) => {
  if (n < 1024) return `${n} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = n / 1024,
    i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(value < 10 ? 2 : 1)} ${units[i]}`;
};
export const fullCacheCategories = [
  "covers",
  "tag_images",
  "import_preview_cache",
  "recommendation_indexes",
];
export const materialCategories = ["instance_recovery", "health_rollback"];
// An empty operation_ids means ALL on the server. Remove an empty material category too.
export function cleanupOptions(
  categories: string[],
  selected: string[],
  operations: Record<string, StorageOperation>,
) {
  const allowed = selected.filter(
    (id) => operations[id] && categories.includes(operations[id].kind),
  );
  return {
    categories: categories.filter(
      (kind) =>
        !materialCategories.includes(kind) ||
        allowed.some((id) => operations[id].kind === kind),
    ),
    operation_ids: allowed,
  };
}
export function suggestedCleanup(storage: Storage) {
  const operations = storage.operations || {};
  const ids = Object.values(operations)
    .filter(
      (op) => op.cleanable_files > 0 && !op.reasons.removes_rollback_point,
    )
    .map((op) => op.id);
  return cleanupOptions(
    (storage.suggested_categories || []).filter(
      (key) => storageNames[key] && !fullCacheCategories.includes(key),
    ),
    ids,
    operations,
  );
}
export type CleanupSummary = {
  storage?: {
    planned?: {
      files: number;
      bytes: number;
      protected_files: number;
      protected_bytes: number;
      impacts: Record<string, string>;
      clears_valid_caches: boolean;
    };
  };
  cleanup_result?: {
    released_bytes: number;
    deleted_files: number;
    skipped_files: number;
    failed_files: number;
    next_index: number;
    unconfirmed_files?: number;
    phase?: "deleting" | "finalizing" | "finished";
    status?:
      "running" | "completed" | "partial" | "failed" | "finalization_failed";
    error_code?: string;
  };
};

export function cleanupOutcome(
  summary: CleanupSummary | undefined,
  job?: { state: string; error_code?: string },
) {
  const r = summary?.cleanup_result;
  const complete =
    r &&
    summary?.storage?.planned?.files === r.next_index &&
    r.failed_files === 0 &&
    r.skipped_files === 0;
  if (
    r?.status === "finalization_failed" ||
    (job?.state === "failed" && complete)
  )
    return "删除已完成，任务收尾失败";
  if (job?.state === "failed" || r?.status === "failed") return "清理执行失败";
  if (job?.state === "cancelled") return "清理已取消";
  if (job?.state === "paused") return "清理已暂停";
  if (job?.state === "completed")
    return r && (r.skipped_files > 0 || r.failed_files > 0)
      ? "清理完成，部分项目已跳过"
      : "清理全部完成";
  return r?.phase === "finalizing" ? "正在结算清理结果…" : "正在清理…";
}
export function cleanupFailure(code?: string) {
  return (
    (
      {
        preview_expired:
          "预览有效期校验失败，请先核对清理结果，不要重复执行删除。",
        revision_conflict: "文件或引用已变化，请核对结果后重新预览。",
        permission_denied: "没有权限处理相关文件，请检查目录权限。",
        storage_full: "存储空间不足，结果已保留。",
        not_found: "必要材料不存在，请核对任务诊断。",
        forbidden: "执行权限已失效，请联系管理员。",
      } as Record<string, string>
    )[code || ""] || "请查看任务诊断；已确认的清理计数保持，不要直接重复删除。"
  );
}
