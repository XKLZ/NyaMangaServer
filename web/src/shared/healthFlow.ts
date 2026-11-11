export type HealthMode = "quick" | "application" | "sources";
export type HealthCheck = {
  id: string;
  job_id: string;
  mode: HealthMode;
  scope: {
    request_key: string;
    mode: HealthMode;
    library_ids?: string[];
    max_bytes?: number;
  };
  rules_version: number;
  phase: number;
  cursor: string;
  checked: number;
  source_bytes_read: number;
  bytes_read: number;
  target_counts: Record<string, number>;
  created_at: number;
  finished_at: number | null;
  revision: number;
  error_code: string;
  state: string;
  categories: Record<string, Record<string, number>>;
  counts: Record<string, number>;
  completion: string;
};
export type HealthFinding = {
  id: string;
  check_id: string;
  category: string;
  object_kind: string;
  object_id: string;
  object_revision: number;
  fingerprint: string;
  status: string;
  code: string;
  repair: string;
  details: Record<string, unknown>;
  checked_at: number;
};
export type HealthAction = {
  finding_id: string;
  kind: string;
  object_id: string;
  fingerprint: string;
  preferred_edition_id?: string;
  allow_sources?: boolean;
  requires_sources: boolean;
};
export type HealthPlan = {
  id: string;
  check_id: string;
  revision: number;
  actions: HealthAction[];
  paused_tasks: number;
  snapshot_bytes: number;
  required_bytes: number;
  rollback: string;
  expires_at: number;
};
export type HealthRepair = {
  materials_cleaned?: boolean;
  id: string;
  plan_id: string;
  job_id: string;
  state: string;
  operation: string;
  revision: number;
  checkpoint: number;
  error_code: string;
  created_at: number;
  updated_at: number;
  journal_state?: string;
  paused_jobs: {
    id: string;
    previous_state: string;
    previous_generation: number;
  }[];
  steps: {
    ordinal: number;
    action: HealthAction;
    state: string;
    error_code: string;
    file_state?: string;
  }[];
};
export type HealthState = {
  phase: string;
  repair_id: string | null;
  revision: number;
  updated_at: number;
  paused_tasks: number;
  can_continue: boolean;
  can_rollback: boolean;
};
export type HealthPage<T> = {
  items: T[];
  total: number;
  page: number;
  page_size: number;
};
export const healthURL = (repair = "") =>
  `/admin/maintenance?section=health${repair ? `&repair=${encodeURIComponent(repair)}` : ""}`;
export const healthModes = {
  quick: "快速检查",
  application: "应用数据检查",
  sources: "源文件深检",
};
export const healthStatuses: Record<string, string> = {
  normal: "正常",
  confirmed: "确认异常",
  inaccessible: "不可访问",
  manual: "需人工判断",
  not_checked: "未检查",
  stale: "已过期",
};
export const healthCategories: Record<string, string> = {
  database: "数据库",
  organization: "组织关系",
  projections: "元数据与标签",
  assets: "人工素材",
  caches: "缓存",
  indexes: "派生索引",
  tasks: "任务",
  sources: "源文件",
};
export const healthActions: Record<string, string> = {
  projection: "重建元数据与搜索",
  tag_evidence: "重建标签证据",
  cover_cache: "重建封面缓存",
  tag_cache: "重建标签图片缓存",
  dictionary: "离线恢复译库",
  preferred: "修正主版本",
  series_positions: "规范系列编号",
  quarantine_recommendation: "隔离无效派生文件",
};
export const healthPhases: Record<string, string> = {
  executing: "正在执行修复",
  preserving: "正在保存回退材料",
  rollback_requested: "已请求回退",
  initial_ready: "回退材料已就绪",
  preserved: "原文件已保留",
  prepared: "文件已暂存",
  published: "文件已发布",
  restored: "文件已恢复",
  idle: "未处于维护保护",
  draining: "正在暂停任务并等待读取收尾",
  repairing: "正在修复",
  rollback: "正在回退",
  rolling_back: "正在回退",
  finishing: "正在复查并结束维护",
  recovery_required: "维护保护待恢复",
  paused: "已暂停",
  failed: "执行失败",
  cancelled: "已取消，保护仍保留",
  running: "执行中",
  queued: "排队中",
  completed: "已完成",
  rolled_back: "已回退",
  verified: "复查通过",
  pending: "待执行",
  rollback_pending: "回退文件待完成",
};
export const checkPhases = [
  "数据库校验",
  "作品关系",
  "系列顺序",
  "版本数据",
  "任务检查点",
  "人工素材",
  "派生文件",
  "源文件",
  "检查结束",
];
export const repairable = (f: HealthFinding) =>
  f.status === "confirmed" && Object.hasOwn(healthActions, f.repair);
export const activeHealthJob = (state?: string) =>
  state === "queued" || state === "running";
export function healthConclusion(v: HealthCheck) {
  if (v.state !== "completed")
    return v.state === "failed"
      ? "检查失败，报告不完整"
      : v.state === "cancelled"
        ? "检查已取消，报告不完整"
        : "检查尚未完成";
  if (v.counts.confirmed) return "发现确认异常";
  if (
    v.completion !== "checked_scope" ||
    ["not_checked", "inaccessible", "stale", "manual"].some(
      (k) => v.counts[k] > 0,
    )
  )
    return "检查已结束，请核对范围与未检查项";
  return "本次检测范围内未发现确认异常";
}
export function sourceBudget(gib: number) {
  const bytes = Math.floor(gib * 1024 ** 3);
  if (
    !Number.isFinite(gib) ||
    gib > 1024 ||
    !Number.isSafeInteger(bytes) ||
    bytes < 1
  )
    throw Error("预算须大于0且不超过1024 GiB");
  return bytes;
}

export function healthBytes(n: number) {
  if (!Number.isFinite(n) || n < 0) return "未知";
  for (const [unit, power] of [
    ["TiB", 4],
    ["GiB", 3],
    ["MiB", 2],
    ["KiB", 1],
  ] as const)
    if (n >= 1024 ** power) return `${(n / 1024 ** power).toFixed(2)} ${unit}`;
  return `${Math.floor(n)} B`;
}

export function healthRecoveryActions(
  state: HealthState | null,
  repair: HealthRepair | null,
  job: string,
  error = false,
) {
  if (!state || !repair || error) return { continue: false, rollback: false };
  const owner = state.repair_id === repair.id && state.phase !== "idle";
  return {
    continue:
      owner &&
      state.can_continue &&
      ["paused", "cancelled", "failed"].includes(job),
    rollback:
      !repair.materials_cleaned &&
      ((owner && state.can_rollback) ||
        (state.phase === "idle" && repair.state === "completed")),
  };
}
export const healthErrors: Record<string, string> = {
  health_operation_failed: "操作已停止，请核对检查点",
  health_recovery_required: "维护保护需要继续或回退",
  health_result_stale: "检测依据已变化，请重新检查",
  storage_full: "存储空间不足",
  permission_denied: "材料不可访问，请核对权限",
  request_timeout: "操作超时，请核对持久任务状态",
};

export function healthSelection(
  current: Record<string, HealthFinding>,
  changed: HealthFinding[],
  checked: boolean,
) {
  const next = { ...current };
  for (const f of changed) {
    if (!checked) delete next[f.id];
    else if (repairable(f)) next[f.id] = f;
  }
  const limit = healthSelectionLimit(Object.values(next));
  if (Object.keys(next).length > limit)
    throw Error(`最多选择${limit}项。本次整页选择未加入，请减少选择后重试。`);
  return next;
}

export function healthSelectionLimit(findings: HealthFinding[]) {
  return findings.every((f) => f.repair === "series_positions") ? 1000 : 100;
}
