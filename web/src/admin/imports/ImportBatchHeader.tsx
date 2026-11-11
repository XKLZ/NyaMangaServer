import { useRecommendationEnabled } from "../../shared/recommendations";
import {
  enhancementSettingsURL,
  featureForStage,
} from "../../shared/enhancementFlow";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import { App, Button, Dropdown, Space, Tag, Steps } from "antd";
import {
  ArrowLeftOutlined,
  MoreOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Link } from "react-router";
import type { ImportBatch } from "../../shared/imports";
import { importAPI, importStageNames } from "../../shared/imports";
import { useAction, ErrorNotice } from "../common";

export function ImportBatchHeader({
  batch,
  library,
  onRefresh,
  disabled = false,
}: {
  batch: ImportBatch;
  library: string;
  onRefresh: () => void;
  disabled?: boolean;
}) {
  const featureKey = featureForStage(batch.stage),
    featureEnabled = useRecommendationEnabled(featureKey),
    canExecute = !featureKey || !!featureEnabled;
  const action = useAction(),
    { modal, message } = App.useApp();
  const finished = ["completed", "cancelled"].includes(batch.state);
  const command = (kind: string) => {
    if (["resume", "retry"].includes(kind) && !canExecute) return;
    const execute = () =>
      action.run(async () => {
        await importAPI.control(batch, kind);
        onRefresh();
        void message.success("批次状态已更新");
      }, "");
    if (kind === "cancel" || kind === "recheck")
      modal.confirm({
        title: kind === "cancel" ? "取消此导入批次？" : "重新审核此批次？",
        content:
          kind === "cancel"
            ? "停止本批任务，保留已入库、采纳和整理结果。"
            : "保留已采纳资料与整理，撤销审核放行状态并回到元数据审核；不会自动重新联网。",
        okText: kind === "cancel" ? "取消批次" : "重新审核",
        cancelText: "继续审核",
        okButtonProps: { danger: kind === "cancel" },
        onOk: execute,
      });
    else void execute();
  };
  return (
    <header className="import-batch-header">
      <h1 className="sr-only">
        {library} · {importStageNames[batch.stage]}
      </h1>
      <div className="import-batch-topline">
        <Space>
          <Link to="/admin/imports" className="import-back">
            <ArrowLeftOutlined aria-hidden="true" /> 导入历史
          </Link>
          <strong>{library}</strong>
          <Tag>
            {(
              {
                processing: "处理中",
                review: "等待审核",
                paused: "已暂停",
                failed: "处理失败",
                completed: "已完成",
                cancelled: "已取消",
              } as Record<string, string>
            )[batch.state] ?? batch.state}
          </Tag>
        </Space>
        <Space>
          <Button
            aria-label="刷新当前状态"
            disabled={disabled || action.busy}
            icon={<ReloadOutlined aria-hidden="true" />}
            onClick={onRefresh}
          />
          <Dropdown
            disabled={disabled || action.busy}
            trigger={["click"]}
            menu={{
              items: [
                ...(!finished
                  ? [
                      {
                        key: "recheck",
                        label: "重新审核／处理输入冲突",
                        disabled: batch.stage === "scan",
                      },
                    ]
                  : []),
                ...(batch.state === "processing"
                  ? [{ key: "pause", label: "暂停处理" }]
                  : []),
                ...(batch.state === "paused"
                  ? [
                      {
                        key: "resume",
                        label: "恢复处理",
                        disabled: !canExecute,
                      },
                    ]
                  : []),
                ...(batch.state === "failed"
                  ? [
                      {
                        key: "retry",
                        label: "重试失败阶段",
                        disabled: !canExecute,
                      },
                    ]
                  : []),
                ...(featureKey && !canExecute
                  ? [
                      {
                        key: "enhancements",
                        label: (
                          <Link to={enhancementSettingsURL(featureKey)}>
                            前往增强设置
                          </Link>
                        ),
                      },
                    ]
                  : []),
                {
                  key: "tasks",
                  label: `任务详情（${batch.jobs?.length ?? 0}）`,
                  disabled: !batch.jobs?.length,
                },
                ...(!finished
                  ? [{ key: "cancel", label: "取消批次", danger: true }]
                  : []),
              ],
              onClick: ({ key }) => {
                if (key === "tasks")
                  modal.info({
                    title: "本批任务",
                    content: (
                      <ul className="import-task-list">
                        {batch.jobs?.map((j) => (
                          <li key={j.id}>
                            <Link to={taskURL(j.id)}>
                              {(
                                {
                                  scan: "扫描入库",
                                  metadata: "元数据获取",
                                  metadata_search: "来源查询",
                                  work_recommendation: "版本查找",
                                  series_recommendation: "系列查找",
                                } as Record<string, string>
                              )[j.type] ?? "后台任务"}{" "}
                              ·{" "}
                              {(
                                {
                                  completed: "已完成",
                                  running: "处理中",
                                  queued: "排队中",
                                  paused: "已暂停",
                                  cancelled: "已取消",
                                  failed: "失败",
                                } as Record<string, string>
                              )[j.state] ?? j.state}
                            </Link>
                          </li>
                        ))}
                      </ul>
                    ),
                    okText: "返回审核",
                  });
                else if (key !== "enhancements") command(key);
              },
            }}
          >
            <Button
              aria-label="批次更多操作"
              icon={<MoreOutlined aria-hidden="true" />}
            />
          </Dropdown>
        </Space>
      </div>
      <Steps
        className="import-stage-steps"
        size="small"
        responsive={false}
        current={Object.keys(importStageNames).indexOf(batch.stage)}
        items={Object.entries(importStageNames).map(([stage, title]) => ({
          title,
          description:
            batch.enhancement_stages?.[stage]?.state === "disabled_skipped"
              ? "未启用，已跳过"
              : batch.enhancement_stages?.[stage]?.state === "blocked"
                ? "功能未启用，已暂停"
                : undefined,
        }))}
      />
      <ErrorNotice error={action.error} />
    </header>
  );
}
