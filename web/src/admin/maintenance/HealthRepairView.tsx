import { randomIdentity } from "../../shared/randomIdentity";
import { useRef, useState, useSyncExternalStore } from "react";
import { Alert, Button, Descriptions, Modal, Space, Table, Tag } from "antd";
import {
  ApiError,
  request,
  subscribeWrites,
  writesPending,
} from "../../shared/api";
import type { Job } from "../../shared/api";
import {
  healthAccess,
  registerHealthJob,
  subscribeHealth,
} from "../../shared/healthAccess.mjs";
import {
  healthActions,
  healthPhases,
  healthRecoveryActions,
  healthErrors,
} from "../../shared/healthFlow";
import type { HealthRepair } from "../../shared/healthFlow";
import { ErrorNotice, time, useDirty } from "../common";
import { useHealthRead } from "./useHealthRead";
import { Link } from "react-router";
import { taskURL } from "../../shared/consoleRoutes.mjs";
type Confirm = {
  kind: "rollback" | "recover";
  revision: number;
  key: string;
  id: string;
};
export function HealthRepairView({
  id,
  refreshReport,
}: {
  id: string;
  refreshReport: () => void;
}) {
  const global = useSyncExternalStore(
      subscribeHealth,
      healthAccess,
      healthAccess,
    ),
    busy = useSyncExternalStore(subscribeWrites, writesPending, writesPending);
  const result = useHealthRead<HealthRepair>(
    `/maintenance/health-repairs/${encodeURIComponent(id)}`,
    !!id,
    true,
  );
  const job = useHealthRead<Job>(
    `/jobs/${encodeURIComponent(result.data?.job_id || "")}`,
    !!result.data,
    true,
  );
  const [confirmation, setConfirmation] = useState<Confirm | null>(null),
    [blocked, setBlocked] = useState(false),
    [error, setError] = useState<unknown>(null),
    [working, setWorking] = useState(false);
  const recoveryRequest = useRef(randomIdentity());
  useDirty(!!confirmation);
  const locked = busy || working,
    state = global.state,
    repair = result.data;
  if (repair) registerHealthJob(repair.job_id);
  const allowed = healthRecoveryActions(
    state,
    repair,
    job.data?.state || "",
    global.error,
  );
  async function run(fn: () => Promise<unknown>) {
    if (locked) return;
    setWorking(true);
    setError(null);
    try {
      await fn();
      result.reload();
      job.reload();
      refreshReport();
    } catch (e) {
      setError(e);
      if (e instanceof ApiError && e.status === 409) setBlocked(true);
    } finally {
      setWorking(false);
      window.dispatchEvent(new Event("health-state-refresh"));
    }
  }
  function confirm(kind: Confirm["kind"]) {
    const revision = kind === "rollback" ? repair?.revision : state?.revision;
    if (!revision) return;
    setBlocked(false);
    setError(null);
    setConfirmation({
      kind,
      revision,
      key: kind === "recover" ? recoveryRequest.current : randomIdentity(),
      id,
    });
  }
  async function execute() {
    if (!confirmation || blocked) return;
    await run(async () => {
      await request(
        confirmation.kind === "recover"
          ? "/maintenance/health-state/recover"
          : `/maintenance/health-repairs/${encodeURIComponent(confirmation.id)}/rollback`,
        "POST",
        {
          request_key: confirmation.key,
          expected_revision: confirmation.revision,
          confirm_maintenance: true,
        },
      );
      setConfirmation(null);
      recoveryRequest.current = randomIdentity();
    });
  }
  return (
    <section className="health-repair-status" aria-label="维护执行与恢复">
      <ErrorNotice error={result.error} retry={result.reload} />
      <ErrorNotice error={error} />
      {!id && state?.phase !== "idle" && (
        <>
          <Alert
            type="warning"
            title="维护保护待恢复"
            description="没有修复任务持有保护。恢复将核对数据库、连接权限和读取收尾，不自动继续业务任务。"
          />
          <Button
            disabled={
              locked ||
              !!state?.repair_id ||
              !state?.can_continue ||
              global.error
            }
            onClick={() => confirm("recover")}
          >
            核对并恢复写入保护
          </Button>
        </>
      )}
      {repair && (
        <>
          <h2>
            {repair.operation === "rollback" ? "回退" : "修复"} ·{" "}
            {healthPhases[repair.state] || "状态待核对"}
          </h2>
          <Space wrap>
            <Tag>
              {global.error
                ? "维护状态未确认"
                : state?.phase === "idle"
                  ? "写入保护已解除"
                  : "全局写入保护保留"}
            </Tag>
            <span>{healthPhases[state?.phase || ""] || "维护状态待核对"}</span>
            <span>更新 {time(repair.updated_at)}</span>
          </Space>
          {repair.materials_cleaned && <Tag>材料已清理 · 回退不可用</Tag>}
          {(repair.error_code || job.data?.error_code) && (
            <Alert
              type="warning"
              title="执行未完整结束，请核对步骤并选择继续或回退"
              description={
                healthErrors[repair.error_code || job.data?.error_code || ""] ||
                "请查看步骤，核对材料后明确继续或回退。"
              }
            />
          )}
          <Descriptions
            size="small"
            column={2}
            items={[
              { key: "step", label: "步骤检查点", children: repair.checkpoint },
              {
                key: "journal",
                label: "文件记录",
                children:
                  healthPhases[repair.journal_state || ""] ||
                  (repair.journal_state
                    ? "文件状态待核对"
                    : "无待完成文件记录"),
              },
            ]}
          />
          <Table
            size="small"
            rowKey="ordinal"
            pagination={false}
            dataSource={repair.steps}
            columns={[
              {
                title: "步骤",
                render: (_, s) => (
                  <span>
                    {s.ordinal + 1}.{" "}
                    {healthActions[s.action.kind] || s.action.kind}
                  </span>
                ),
              },
              {
                title: "结果",
                render: (_, s) => (
                  <>
                    <Tag
                      color={
                        s.state === "verified"
                          ? "success"
                          : s.state === "failed"
                            ? "error"
                            : undefined
                      }
                    >
                      {healthPhases[s.state] || "状态待核对"}
                    </Tag>
                    {s.file_state && (
                      <p>
                        文件：{healthPhases[s.file_state] || "文件状态待核对"}
                      </p>
                    )}
                    {s.error_code && (
                      <details>
                        <summary>诊断详情</summary>
                        {s.error_code}
                      </details>
                    )}
                  </>
                ),
              },
            ]}
          />
          <div className="health-toolbar">
            <Button
              disabled={locked || !allowed.continue}
              onClick={() =>
                void run(() =>
                  request(
                    `/jobs/${encodeURIComponent(repair.job_id)}/resume`,
                    "POST",
                  ),
                )
              }
            >
              继续维护
            </Button>
            {["queued", "running"].includes(job.data?.state || "") && (
              <>
                <Button
                  disabled={locked}
                  onClick={() =>
                    void run(() =>
                      request(
                        `/jobs/${encodeURIComponent(repair.job_id)}/pause`,
                        "POST",
                      ),
                    )
                  }
                >
                  暂停维护
                </Button>
                <Button
                  disabled={locked}
                  onClick={() =>
                    void run(() =>
                      request(
                        `/jobs/${encodeURIComponent(repair.job_id)}/cancel`,
                        "POST",
                      ),
                    )
                  }
                >
                  取消维护
                </Button>
              </>
            )}
            <Button
              danger
              disabled={locked || !allowed.rollback}
              onClick={() => confirm("rollback")}
            >
              回退此次修复
            </Button>
            <Button
              onClick={() => {
                result.reload();
                job.reload();
                window.dispatchEvent(new Event("health-state-refresh"));
              }}
            >
              重新核对状态
            </Button>
          </div>
          <details>
            <summary>
              被维护暂停的任务（{repair.paused_jobs?.length || 0}）
            </summary>
            <p>
              维护结束不会自动续跑。请核对后在任务页主动恢复；原先由你暂停的任务继续保持暂停。
            </p>
            {repair.paused_jobs?.map((v) => (
              <p key={v.id}>
                <Link to={taskURL(v.id)}>查看任务 {v.id}</Link> · 原状态{" "}
                {healthPhases[v.previous_state] || v.previous_state}
              </p>
            ))}
          </details>
        </>
      )}
      <Modal
        open={!!confirmation}
        styles={{ body: { maxHeight: "60dvh", overflowY: "auto" } }}
        title={
          confirmation?.kind === "rollback"
            ? "确认回退此次修复"
            : "确认恢复写入保护"
        }
        onCancel={() => !locked && setConfirmation(null)}
        keyboard={!locked}
        closable={!locked}
        mask={{ closable: !locked }}
        footer={
          <Space>
            <Button disabled={locked} onClick={() => setConfirmation(null)}>
              取消
            </Button>
            <Button
              danger={confirmation?.kind === "rollback"}
              type="primary"
              disabled={locked || blocked}
              onClick={() => void execute()}
            >
              确认{confirmation?.kind === "rollback" ? "回退" : "恢复"}
            </Button>
          </Space>
        }
      >
        <p>
          将保持全局维护保护，核对当前版本与文件记录。回退仅恢复本次目标，后续编辑冲突不会被覆盖；可能重新出现原检查问题。业务任务不会自动续跑。
        </p>
        {confirmation?.kind === "rollback" && repair && (
          <>
            <p>
              回退 {repair.steps.length} 个步骤；曾暂停{" "}
              {repair.paused_jobs?.length || 0} 个任务。
            </p>
            <ul>
              {repair.steps.map((step) => (
                <li key={step.ordinal}>
                  {healthActions[step.action.kind] || "回退目标"} ·{" "}
                  {step.action.object_id.slice(0, 8)}
                </li>
              ))}
            </ul>
          </>
        )}
        {blocked && (
          <Alert
            type="warning"
            title="版本或后续编辑已变化，输入保留。关闭后重新核对状态，再明确确认；不能自动重试。"
          />
        )}
        <ErrorNotice error={error} />
      </Modal>
    </section>
  );
}
