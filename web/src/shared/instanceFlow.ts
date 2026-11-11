export type InstanceOperation = {
  materials_cleaned?: boolean;
  id: string;
  kind: "backup" | "restore";
  state: string;
  phase: string;
  revision: number;
  bytes: number;
  sha256: string;
  error_code: string;
  created_at: number;
  updated_at: number;
};
export type InstanceState = {
  operation_id: string | null;
  background_hold: boolean;
  write_epoch: number;
  revision: number;
};
export type InstanceActivation = {
  materials_cleaned?: boolean;
  id: string;
  state: string;
  revision: number;
  completed_files: number;
  total_files: number;
  background_hold: boolean;
  requires_restart: boolean;
};
export type RootMapping = {
  library_id: string;
  target_root_key: string | null;
};
export type SourceRoot = {
  library_id: string;
  root_key: string;
  identity: string;
  display_name: string;
};
export type InstanceSummary = {
  build: { version: string; commit: string };
  schema_version: number;
  object_counts: Record<string, number>;
  source_roots: SourceRoot[];
  files: number;
  expanded_bytes: number;
  contains_credentials_and_password_hashes: boolean;
};
export type InstancePreview = {
  operation: InstanceOperation;
  object_counts: Record<string, number>;
  source_roots: SourceRoot[];
  root_mappings: RootMapping[];
  space: Record<string, number>;
  archive_bytes: number;
  expanded_bytes: number;
  linked_files: number;
  unlinked_libraries: number;
  source_content_verified: boolean;
  replaces_whole_instance: boolean;
  requires_relogin: boolean;
  contains_credentials_and_password_hashes: boolean;
  target_fingerprint: string;
  replaces_unhealthy_target: boolean;
};
export type RestoreDetail = {
  operation: InstanceOperation;
  summary: InstanceSummary | null;
  target_roots: { root_key: string; display_name: string }[];
  root_mappings: RootMapping[];
  preview: InstancePreview | null;
};
export const instanceStates: Record<string, string> = {
  pending: "等待处理",
  running: "正在备份",
  uploaded: "已上传，等待校验",
  validating: "正在校验",
  validated: "校验通过",
  previewed: "目录已核对",
  preparing: "正在准备替换",
  awaiting_restart: "已准备，等待正常重启",
  completed: "已完成",
  interrupted: "操作已中断",
  failed: "操作失败",
  cancelled: "已取消",
  abandon_requested: "正在放弃，请等待收尾",
  abandoned: "已放弃",
  rollback_requested: "已安排回退，等待正常重启",
  rolled_back: "已回退",
  preserving: "正在保全原实例",
  activating: "正在激活",
  rolling_back: "正在回退",
};
export const instancePhases: Record<string, string> = {
  draining: "暂停后台并收尾读写",
  compressing: "生成ZIP备份包",
  complete: "备份完成",
  upload: "上传",
  uploaded: "上传完成",
  validating: "校验包与数据库",
  validated: "校验完成",
  previewed: "目录映射已核对",
  awaiting_restart: "等待正常重启",
  activated: "恢复已激活",
  abandoned: "已放弃",
  pending: "等待处理",
};
export const instanceErrors: Record<string, string> = {
  instance_build_mismatch:
    "应用构建或备份格式不一致，请使用制作备份时的同一构建。",
  invalid_instance_package:
    "备份包损坏、内容不完整或超出限制，请重新取得完整备份包。",
  instance_mapping_invalid:
    "目录中的文件未通过对应检查，请重新选择目录，或选择暂不关联。",
  storage_full: "可用空间不足，请释放目标数据盘空间后重新核对。",
  permission_denied: "目标数据或凭据位置不可写，请检查服务权限后重试。",
  instance_material_missing: "备份或回退所需材料缺失，请核对服务上的原文件。",
  instance_secrets_unavailable: "凭据及密钥不匹配，请检查目标凭据配置。",
  cancelled: "操作已取消，未完成的包不可下载。",
  request_timeout: "传输或验证已停止，请先核对持久操作状态，勿重复新建操作。",
  maintenance_active: "全局维护保护中，请先查看当前维护操作。",
  instance_recovery_required:
    "恢复保护需要人工核对，请查看操作状态及离线恢复说明。",
  revision_conflict: "操作或目标已变化，输入保留；请明确重新核对后再预览。",
  invalid_instance_request: "请求无效，请核对目录映射和确认内容。",
  instance_operation_failed:
    "操作停止，请查看当前阶段与检查点，再决定取消或离线处理。",
  process_interrupted: "服务中断，保护仍可能保留；请核对后明确取消或放弃。",
  restore_upload_failed: "上传未完成，请核对历史记录，再明确放弃并重新选择包。",
};
export const instanceActive = (state: string) =>
  [
    "pending",
    "running",
    "validating",
    "preparing",
    "abandon_requested",
  ].includes(state);
export function initialMappings(detail: RestoreDetail): RootMapping[] {
  return (detail.summary?.source_roots || []).map((r) => ({
    library_id: r.library_id,
    target_root_key:
      detail.root_mappings.find((v) => v.library_id === r.library_id)
        ?.target_root_key ?? null,
  }));
}
export function mappingsEqual(a: RootMapping[], b: RootMapping[]) {
  return (
    a.length === b.length &&
    a.every((v) =>
      b.some(
        (x) =>
          x.library_id === v.library_id &&
          x.target_root_key === v.target_root_key,
      ),
    )
  );
}
export function recoveryReturn(value: string): string | null {
  const url = new URL(value, "http://localhost");
  if (
    url.pathname !== "/admin/maintenance" ||
    url.searchParams.get("section") !== "recovery"
  )
    return null;
  const q = new URLSearchParams({ section: "recovery" });
  for (const key of ["operation", "kind", "backup_page", "restore_page"]) {
    const v = url.searchParams.get(key);
    if (v && /^[a-zA-Z0-9_-]{1,128}$/.test(v)) q.set(key, v);
  }
  return "/admin/maintenance?" + q;
}
export function rememberRecoveryReturn(value: string) {
  const target = recoveryReturn(value);
  if (target)
    try {
      sessionStorage.setItem("instance-return", target);
    } catch {
      /* Optional restoration storage. */
    }
}
export function takeRecoveryReturn(): string | null {
  try {
    const value = sessionStorage.getItem("instance-return");
    sessionStorage.removeItem("instance-return");
    return value ? recoveryReturn(value) : null;
  } catch {
    return null;
  }
}

/** Only these stable pre-handler/build checks prove that this new request created no operation. */
export function instanceRequestUncreated(status: number, code: string) {
  return (
    (status === 401 && code === "unauthenticated") ||
    (status === 403 && ["csrf_rejected", "origin_rejected"].includes(code)) ||
    (status === 409 && code === "instance_build_mismatch")
  );
}
