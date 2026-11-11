import type { Job } from "./api";
export const stateNames: Record<string, string> = {
  available: "可用",
  missing: "文件缺失",
  corrupt: "归档损坏",
  needs_review: "待检查",
  queued: "等待执行",
  running: "正在执行",
  paused: "已暂停",
  completed: "已完成",
  failed: "失败",
  cancelled: "已取消",
};
export const errorNames: Record<string, string> = {
  enhancement_disabled: "所需增强功能未启用，请在增强设置中开启后明确恢复任务",
  enhancement_revision_conflict: "增强设置版本已变化，请重新核对",
  enhancement_impact_changed: "任务影响已变化，请重新确认",
  enhancement_transition_conflict: "增强功能切换状态已变化",
  series_discovery_failed: "系列查找失败，请查看任务诊断后重试",
  series_publication_failed: "系列方案保存失败，请查看任务诊断后重试",
  series_evidence_finalize_failed: "系列比较记录收尾失败，请查看任务诊断后重试",
  series_checkpoint_invalid: "系列查找检查点不可用，请查看任务诊断",
  series_resource_budget_exceeded: "系列查找超出资源预算",
  series_runtime_unavailable: "系列查找运行组件暂不可用",
  series_inputs_changed_start_new_discovery: "系列查找输入已变化，请重新查找",
  discovery_resource_budget_exceeded: "超出比较或证据预算；没有静默截断",
  discovery_failed: "同作品查找失败，请查看诊断后重新查找",
  organization_changed_resume_to_refresh:
    "组织在发布期间变化，恢复任务可复用已算证据重新整理方案",
  recommendation_runtime_unavailable:
    "离线运行组件不可用，请检查模型与配置后恢复",

  no_sha1: "没有可用的代表图 SHA1，可手动填写画廊网址",
  no_match: "未找到匹配画廊，可手动指定网址后重试",
  multiple_matches: "发现多个画廊，请核对候选并选择后重试",
  webinfo_invalid: "归档提示文件无效，已继续其他识别方式",
  provider_auth_required: "站点认证不可用，请更新 Cookie 后恢复任务",
  provider_action_required: "站点存在验证码、封禁或访问限制，请处理后恢复",
  rate_limited: "站点限速，请等待后恢复任务",
  provider_unavailable: "站点服务暂不可用",
  provider_rejected: "画廊网址或令牌被站点拒绝，请核对",
  provider_secrets_unavailable: "站点凭据文件或密钥不可用",
  network_error: "网络连接失败，请检查出口设置",
  network_timeout: "站点请求超时",
  invalid_response: "站点响应格式无效",
  response_too_large: "站点响应超过允许大小",
  dictionary_checksum_mismatch: "译库摘要不一致，已保留旧版",
  invalid_dictionary: "译库格式无效，已保留旧版",
  blocked_destination: "目标或重定向不在允许范围内",
  revision_conflict: "来源选择已变化，请重新确认",
  retries_exhausted: "已达到重试次数，请检查后重试",
  cover_cache_failed: "封面缓存暂不可用，浏览时将尝试重新生成",
  invalid_archive: "归档损坏、加密或格式不受支持",
  invalid_archive_name: "归档中存在不安全的路径",
  invalid_archive_encoding: "页名编码无法解析，请核对本文件编码",
  parsing_changed: "页序已变化，请刷新资料并核对页码",
  lookup_resource_limit: "代表图摘要读取超过资源预算",
  lookup_page_corrupt: "代表图片读取或校验失败",
  lookup_invalid_archive_encoding: "代表图页名编码无法解析",
  lookup_invalid_archive: "代表图归档读取失败",
  lookup_file_unavailable: "代表图源文件暂不可读取",
  lookup_content_changed: "读取摘要期间文件内容变化",
  lookup_parsing_changed: "读取摘要期间页序变化",
  lookup_failed: "代表图摘要未完成",
  resource_limit: "超过归档或图片资源限制",
  page_corrupt: "图片损坏或格式不受支持",
  file_unavailable: "文件无法读取",
  content_changed: "扫描期间文件发生变化，请重新扫描",
  root_unavailable: "书库目录不可用",
  root_changed: "书库目录身份发生变化，请检查配置",
  suspicious_empty_root: "目录突然为空，已保留原有书籍状态",
  file_missing: "源文件已缺失",
  index_settings_changed: "归档编码已更改，请重新扫描以更新页目录",
};
export const message = (e: unknown) =>
  e instanceof Error ? e.message : "请求失败，请重试";
export const reason = (code: string) =>
  errorNames[code] ??
  (code.startsWith("lookup_")
    ? `代表图摘要：${errorNames[code.slice(7)] || "读取未完成"}`
    : "处理失败，请查看诊断或重试");
export const active = (j: Job) => j.state === "queued" || j.state === "running";
