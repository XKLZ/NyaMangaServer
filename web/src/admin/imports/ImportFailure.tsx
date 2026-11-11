import { useRecommendationEnabled } from "../../shared/recommendations";
import {
  enhancementSettingsURL,
  featureForStage,
  featureNames,
} from "../../shared/enhancementFlow";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import { Alert, App, Button, Space, Tag, Typography } from "antd";
import { Link } from "react-router";
import type { ImportBatch, ImportFailure } from "../../shared/imports";
import { importAPI, importStageNames } from "../../shared/imports";
import { ErrorNotice, useAction } from "../common";
export function importFailureReason(f: ImportFailure) {
  return (
    (
      {
        enhancement_disabled: "所需增强功能未启用，已保留当前进度",
        invalid_checkpoint: "保存的推荐检查点不可用",
        runtime_unavailable: "推荐运行组件暂不可用",
        resource_budget: "推荐处理超出资源预算",
        database_constraint: "推荐方案保存发生数据冲突",
        storage_unavailable: "推荐数据存储暂不可用",
        storage_capacity: "推荐数据存储空间不足",
        proposal_invalid: "推荐方案未通过保存校验",
      } as Record<string, string>
    )[f.reason ?? ""] ??
    (
      {
        series_checkpoint_invalid: "系列推荐检查点无效",
        recommendation_worker_failed: "推荐处理未完成",
        recommendation_runtime_unavailable: "推荐运行组件不可用",
        series_discovery_failed: "系列推荐处理失败",
        series_publication_failed: "系列推荐方案保存失败",
        series_evidence_finalize_failed: "系列推荐记录收尾失败",
        discovery_failed: "版本推荐处理失败",
        cancelled: "推荐任务已终止",
      } as Record<string, string>
    )[f.error_code] ??
    (f.stage === "scan"
      ? "文件扫描未完成"
      : f.stage === "metadata"
        ? "元数据获取未完成"
        : "推荐任务未成功完成")
  );
}
const phases: Record<string, string> = {
  prepare_features: "准备特征",
  restore_checkpoint: "恢复检查点",
  compare: "比较作品",
  generate_proposals: "生成建议",
  publish: "保存建议",
  finalize_evidence: "保存比较记录",
};
export function ImportFailureSummary({ batch: b }: { batch: ImportBatch }) {
  const failures = b.task_failures ?? [],
    skipped = [
      ...new Set([
        ...(b.skipped_stages ?? []),
        ...Object.entries(b.enhancement_stages ?? {})
          .filter(([, value]) => value.state === "disabled_skipped")
          .map(([stage]) => stage),
      ]),
    ];
  return (
    <Space size={4} wrap>
      {failures.length > 0 && (
        <Tag color="error">
          {b.state === "completed" ? "曾有" : ""}
          {failures.length}项任务失败
        </Tag>
      )}
      {skipped.map((stage) => (
        <Tag key={stage} color="warning">
          {b.enhancement_stages?.[stage]?.state === "disabled_skipped"
            ? "未启用，已跳过"
            : "已跳过"}
          {importStageNames[stage as keyof typeof importStageNames] ?? "推荐"}
        </Tag>
      ))}
    </Space>
  );
}
export function ImportFailureNotice({
  batch: b,
  onRefresh,
  beforeAction,
  disabled = false,
}: {
  batch: ImportBatch;
  onRefresh: () => void;
  beforeAction?: () => void;
  disabled?: boolean;
}) {
  const key = featureForStage(b.stage),
    featureEnabled = useRecommendationEnabled(key),
    canExecute = !key || !!featureEnabled;
  const action = useAction(),
    { modal } = App.useApp();
  const failure = b.current_failure,
    skipped = b.skipped_stages ?? [],
    past = b.task_failures ?? [];
  async function control(kind: string) {
    if (["resume", "retry"].includes(kind) && !canExecute) return;
    beforeAction?.();
    await action.run(async () => {
      await importAPI.control(b, kind);
      onRefresh();
    }, "");
  }
  if (b.error_code === "enhancement_disabled" && key && b.state === "paused")
    return (
      <section className="import-failure-notice" aria-label="增强功能暂停导入">
        <Alert
          type="warning"
          showIcon
          title={`${featureNames[key]}未启用，导入已暂停`}
          description={
            <Space orientation="vertical">
              <span>
                当前进度和人工审核保持。开启功能后，请明确恢复；也可跳过本次推荐。
              </span>
              <Space wrap>
                <Link to={enhancementSettingsURL(key)}>前往增强设置</Link>
                <Button
                  disabled={disabled || action.busy || !featureEnabled}
                  onClick={() => void control("resume")}
                >
                  恢复本阶段
                </Button>
                <Button
                  disabled={disabled || action.busy}
                  onClick={() =>
                    modal.confirm({
                      title: "跳过本次推荐？",
                      content:
                        "保留当前归属和已完成审核，不采纳本次推荐；仍需确认后继续。",
                      okText: "明确跳过本次推荐",
                      cancelText: "返回",
                      onOk: () => control("skip_recommendation"),
                    })
                  }
                >
                  跳过本次推荐
                </Button>
              </Space>
            </Space>
          }
        />
        <ErrorNotice error={action.error} />
      </section>
    );
  if (failure && b.state === "failed")
    return (
      <section className="import-failure-notice" aria-label="当前推荐失败">
        <Alert
          type="error"
          showIcon
          title={`${failure.stage === "versions" ? "版本" : "系列"}推荐失败`}
          description={
            <Space orientation="vertical" size={8}>
              <span>
                {importFailureReason(failure)}
                {failure.phase && phases[failure.phase]
                  ? ` · ${phases[failure.phase]}`
                  : ""}
              </span>
              <Space wrap>
                <Typography.Text copyable>
                  任务编号：{failure.job_id}
                </Typography.Text>
                <Link to={taskURL(failure.job_id)}>任务详情</Link>
                {key && !canExecute && (
                  <Link to={enhancementSettingsURL(key)}>前往增强设置</Link>
                )}
              </Space>
              <Space wrap>
                <Button
                  disabled={
                    disabled || action.busy || !b.can_retry || !canExecute
                  }
                  loading={action.busy}
                  onClick={() => void control("retry")}
                >
                  重试{failure.stage === "versions" ? "版本" : "系列"}推荐
                </Button>
                <Button
                  disabled={disabled || action.busy}
                  onClick={() =>
                    modal.confirm({
                      title: `跳过本次${failure.stage === "versions" ? "版本" : "系列"}推荐？`,
                      content:
                        "保留当前归属，不采纳本次推荐。跳过后返回人工审核，仍需确认继续。",
                      okText: "明确跳过本次推荐",
                      cancelText: "返回",
                      onOk: () => control("skip_recommendation"),
                    })
                  }
                >
                  跳过本次{failure.stage === "versions" ? "版本" : "系列"}推荐
                </Button>
              </Space>
            </Space>
          }
        />
        <ErrorNotice error={action.error} />
      </section>
    );
  if (skipped.includes(b.stage))
    return (
      <Alert
        type="warning"
        showIcon
        title={`已跳过本次${b.stage === "versions" ? "版本" : "系列"}推荐`}
        description="当前归属保持不变，请确认本阶段后继续。"
      />
    );
  if (past.length && b.state === "completed")
    return (
      <Alert
        type="warning"
        showIcon
        title="批次已完成，但曾有任务失败"
        description={
          <Space orientation="vertical">
            {past.map((f) => (
              <span key={f.job_id}>
                {importFailureReason(f)} ·{" "}
                <Link to={taskURL(f.job_id)}>任务 {f.job_id}</Link>
              </span>
            ))}
          </Space>
        }
      />
    );
  return null;
}
