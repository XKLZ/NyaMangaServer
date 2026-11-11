// Hierarchy: totals and one cleanup action; grouped purposes secondary, reasons tertiary.
// Reuse Ant theme tokens, 8/16/24 spacing and normal-weight descriptions; no new palette.
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Collapse,
  Descriptions,
  Empty,
  Modal,
  Space,
  Spin,
  Tag,
  Typography,
} from "antd";
import { ClearOutlined, ReloadOutlined } from "@ant-design/icons";
import { ApiError, request } from "../../shared/api";
import type { Job } from "../../shared/api";
import type { MaintenancePlan } from "../types";
import { usePermissions } from "../../shared/permissions";
import {
  bytesText,
  cleanupOptions,
  cleanupOutcome,
  cleanupFailure,
  fullCacheCategories,
  materialCategories,
  storageGroups,
  storageNames,
  storageReason,
  suggestedCleanup,
} from "../../shared/storageFlow";
import type { CleanupSummary, Storage } from "../../shared/storageFlow";
import {
  ErrorNotice,
  StateTag,
  time,
  useDirty,
  useRemote,
  useURLValue,
} from "../common";
import "./storage.css";

export function StorageWorkspace() {
  const data = useRemote<Storage>("/maintenance/storage");
  const [planID, setPlanID] = useURLValue("cleanup_plan", "");
  const [open, setOpen] = useState(!!planID),
    [refreshing, setRefreshing] = useState(false),
    [error, setError] = useState<unknown>(null);
  const lock = useRef(false);
  async function refresh() {
    if (lock.current) return;
    lock.current = true;
    setRefreshing(true);
    setError(null);
    try {
      data.setData(await request<Storage>("/maintenance/storage?refresh=true"));
    } catch (e) {
      setError(e);
    } finally {
      lock.current = false;
      setRefreshing(false);
    }
  }
  const storage = data.data;
  return (
    <section className="storage-workspace" aria-label="存储与清理">
      <div className="storage-header">
        <div className="storage-totals">
          <div>
            <span>应用总占用</span>
            <strong>
              {storage
                ? bytesText(
                    storage.total_bytes ??
                      storage.categories.reduce((n, v) => n + v.bytes, 0),
                  )
                : "—"}
            </strong>
          </div>
          <div>
            <span>可释放空间</span>
            <strong>
              {storage?.groups ? bytesText(storage.cleanable_bytes) : "—"}
            </strong>
          </div>
        </div>
        <Space wrap>
          <Button
            icon={<ReloadOutlined aria-hidden="true" />}
            loading={refreshing || data.loading}
            onClick={() => void refresh()}
          >
            刷新
          </Button>
          <Button
            type="primary"
            icon={<ClearOutlined aria-hidden="true" />}
            disabled={!storage?.groups}
            onClick={() => setOpen(true)}
          >
            清理建议
          </Button>
        </Space>
      </div>
      <Typography.Paragraph type="secondary">
        {storage?.statistics_at
          ? `统计于 ${time(storage.statistics_at)} · `
          : ""}
        不含源漫画文件
      </Typography.Paragraph>
      <ErrorNotice error={error || data.error} />
      {storage && !storage.groups && (
        <Alert
          type="info"
          showIcon
          title="当前服务尚未支持分组清理，请等待服务升级后刷新。"
        />
      )}
      {storage?.groups && (
        <Collapse
          className="storage-groups"
          items={[...storage.groups]
            .sort(
              (a, b) =>
                Object.keys(storageGroups).indexOf(a.kind) -
                Object.keys(storageGroups).indexOf(b.kind),
            )
            .map((group) => ({
              key: group.kind,
              label: (
                <div className="storage-group-heading">
                  <div>
                    <strong>
                      {storageGroups[group.kind]?.name || "其他应用数据"}
                    </strong>
                    <p>
                      {storageGroups[group.kind]?.purpose || "保持现有用途"}
                    </p>
                  </div>
                  <strong>{bytesText(group.bytes)}</strong>
                </div>
              ),
              children: (
                <>
                  <div className="storage-counts">
                    <Tag>{group.files} 个文件</Tag>
                    <span>可释放 {bytesText(group.cleanable_bytes)}</span>
                    <span>保留 {bytesText(group.protected_bytes)}</span>
                  </div>
                  {storage.categories
                    .filter((category) => category.group === group.kind)
                    .map((category) => (
                      <div className="storage-category" key={category.kind}>
                        <div>
                          <strong>
                            {storageNames[category.kind] || "其他应用数据"}
                          </strong>
                          <span>
                            {category.files} 个文件 ·{" "}
                            {bytesText(category.bytes)}
                          </span>
                        </div>
                        <ul>
                          {Object.entries(category.reasons || {}).map(
                            ([reason, bytes]) => (
                              <li key={reason}>
                                {storageReason(reason)} · {bytesText(bytes)}
                              </li>
                            ),
                          )}
                        </ul>
                      </div>
                    ))}
                  {Object.values(storage.operations || {})
                    .filter((op) =>
                      storage.categories.some(
                        (c) => c.kind === op.kind && c.group === group.kind,
                      ),
                    )
                    .map((op, index) => (
                      <div className="storage-operation" key={op.id}>
                        <span>
                          {storageNames[op.kind]} {index + 1}
                        </span>
                        <Tag>
                          {(
                            {
                              completed: "已完成",
                              failed: "已结束，未完成",
                              cancelled: "已取消",
                              abandoned: "已放弃",
                              rolled_back: "已回退",
                              offline_unconfirmed: "离线暂存",
                              running: "进行中",
                              pending: "等待处理",
                              preparing: "正在准备",
                              awaiting_restart: "等待重启",
                            } as Record<string, string>
                          )[op.state] || "保留中的操作"}
                        </Tag>
                        <span>{bytesText(op.bytes)}</span>
                      </div>
                    ))}
                </>
              ),
            }))}
        />
      )}
      {storage?.groups && (
        <CleanupDialog
          open={open}
          planID={planID}
          setPlanID={setPlanID}
          storage={storage}
          onClose={() => setOpen(false)}
          onDone={data.reload}
        />
      )}
    </section>
  );
}
function CleanupDialog({
  open,
  planID,
  setPlanID,
  storage,
  onClose,
  onDone,
}: {
  open: boolean;
  planID: string;
  setPlanID: (id: string) => void;
  storage: Storage;
  onClose: () => void;
  onDone: () => void;
}) {
  const { verified } = usePermissions();
  const [choices, setChoices] = useState(() => suggestedCleanup(storage));
  const [plan, setPlan] = useState<MaintenancePlan | null>(null),
    [job, setJob] = useState<Job | null>(null),
    [error, setError] = useState<unknown>(null),
    [busy, setBusy] = useState<"preview" | "execute" | "">(""),
    [notice, setNotice] = useState("");
  const controller = useRef<AbortController | null>(null),
    lock = useRef(false);
  const [now, setNow] = useState(() => Date.now());
  const operations = storage.operations || {};
  const options = cleanupOptions(
    choices.categories,
    choices.operation_ids,
    operations,
  );
  const planned = (plan?.summary as CleanupSummary | undefined)?.storage
    ?.planned;
  const result = (plan?.summary as CleanupSummary | undefined)?.cleanup_result;
  const [restoring, setRestoring] = useState(false),
    [readVersion, setReadVersion] = useState(0);
  const expired = !!plan && now >= plan.expires_at * 1000;
  useDirty(open && !job);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    if (!open || !plan || job) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [open, plan, job]);
  const PlanContainer = job ? "details" : "div";
  const running = !!job && ["running", "queued"].includes(job.state);
  useEffect(() => {
    if (!open) return;
    if (!planID) {
      let live = true;
      void Promise.resolve().then(() => {
        if (live) {
          setPlan(null);
          setJob(null);
        }
      });
      return () => {
        live = false;
      };
    }
    const abort = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const current = await request<MaintenancePlan>(
          `/maintenance/plans/${encodeURIComponent(planID)}`,
          "GET",
          undefined,
          false,
          abort.signal,
        );
        if (current.operation !== "cleanup")
          throw new Error("该计划不是清理计划，请返回重新选择。");
        const next = current.job_id
          ? await request<Job>(
              `/jobs/${current.job_id}`,
              "GET",
              undefined,
              false,
              abort.signal,
            )
          : null;
        if (abort.signal.aborted) return;
        setPlan(current);
        setJob(next);
        setError(null);
        setRestoring(false);
        if (next && ["running", "queued"].includes(next.state))
          timer = setTimeout(() => void poll(), 1500);
        else if (next) onDone();
      } catch (e) {
        if (!abort.signal.aborted) {
          setError(e);
          setRestoring(false);
        }
      }
    }
    void Promise.resolve().then(() => {
      if (!abort.signal.aborted) {
        setRestoring(true);
        void poll();
      }
    });
    return () => {
      abort.abort();
      clearTimeout(timer);
    };
  }, [open, planID, job?.id, onDone, readVersion]);
  async function preview() {
    if (!verified || lock.current || !options.categories.length) return;
    lock.current = true;
    setBusy("preview");
    setError(null);
    setNotice("");
    const abort = new AbortController();
    controller.current = abort;
    // Server permits 30 minutes after authentication. Allow response overhead; never resend.
    const timer = setTimeout(() => abort.abort(), 31 * 60 * 1000);
    try {
      const next = await request<MaintenancePlan>(
        "/maintenance/plans",
        "POST",
        { operation: "cleanup", expected_revisions: {}, options },
        false,
        abort.signal,
      );
      if (!abort.signal.aborted) {
        setPlan(next);
        setPlanID(next.id);
        setNow(Date.now());
      }
    } catch (e) {
      if (abort.signal.aborted) setNotice("预览已取消，选择保留，未执行删除。");
      else setError(e);
    } finally {
      clearTimeout(timer);
      lock.current = false;
      setBusy("");
    }
  }
  async function execute() {
    if (
      !verified ||
      restoring ||
      lock.current ||
      !plan ||
      plan.id !== planID ||
      expired ||
      job
    )
      return;
    lock.current = true;
    setBusy("execute");
    setError(null);
    try {
      const accepted = await request<{ job_id: string }>(
        `/maintenance/plans/${plan.id}/execute`,
        "POST",
        {},
      );
      setJob({ id: accepted.job_id, state: "queued" } as Job);
    } catch (e) {
      setError(e);
    } finally {
      lock.current = false;
      setBusy("");
    }
  }
  function categoryList(keys: string[]) {
    return (
      <div className="storage-options">
        {keys.map((kind) => (
          <div key={kind}>
            <Checkbox
              disabled={!!busy}
              checked={choices.categories.includes(kind)}
              onChange={(e) =>
                setChoices((old) => ({
                  categories: e.target.checked
                    ? [...old.categories, kind]
                    : old.categories.filter((v) => v !== kind),
                  operation_ids: e.target.checked
                    ? [
                        ...new Set([
                          ...old.operation_ids,
                          ...Object.values(operations)
                            .filter(
                              (op) =>
                                op.kind === kind &&
                                op.cleanable_files > 0 &&
                                !op.reasons.removes_rollback_point,
                            )
                            .map((op) => op.id),
                        ]),
                      ]
                    : old.operation_ids.filter(
                        (id) => operations[id]?.kind !== kind,
                      ),
                }))
              }
            >
              {storageNames[kind]}
            </Checkbox>
            {choices.categories.includes(kind) &&
              materialCategories.includes(kind) && (
                <div className="storage-materials">
                  {Object.values(operations)
                    .filter((op) => op.kind === kind)
                    .map((op, index) => (
                      <Checkbox
                        key={op.id}
                        disabled={!!busy || op.cleanable_files === 0}
                        checked={choices.operation_ids.includes(op.id)}
                        onChange={(e) =>
                          setChoices((old) => ({
                            ...old,
                            operation_ids: e.target.checked
                              ? [...old.operation_ids, op.id]
                              : old.operation_ids.filter((id) => id !== op.id),
                          }))
                        }
                      >
                        <span>
                          {storageNames[kind]} {index + 1} ·{" "}
                          {bytesText(op.cleanable_bytes)}
                        </span>
                        <small>
                          {Object.keys(op.reasons)
                            .map(storageReason)
                            .join("；")}
                        </small>
                      </Checkbox>
                    ))}
                  {!Object.values(operations).some(
                    (op) => op.kind === kind,
                  ) && <span>暂无可清理材料</span>}
                </div>
              )}
          </div>
        ))}
      </div>
    );
  }
  const errorText =
    error instanceof ApiError
      ? {
          request_timeout:
            "预览超时并已停止，尚未执行删除。选择保留，可缩小范围后重新预览。",
          cleanup_preview_capacity:
            "本次预览范围超过容量，请减少类别或关联操作后重新预览。",
          revision_conflict: "资料已变化，原预览不能继续，请重新生成并核对。",
        }[error.code]
      : undefined;
  return (
    <Modal
      centered
      open={open}
      title="清理建议"
      width={760}
      className="storage-cleanup-modal"
      onCancel={() => !busy && onClose()}
      mask={{ closable: !busy }}
      closable={!busy}
      keyboard={!busy}
      footer={
        <Space wrap>
          {busy === "preview" ? (
            <Button onClick={() => controller.current?.abort()}>
              取消等待
            </Button>
          ) : (
            <Button disabled={!!busy} onClick={onClose}>
              {job ? "关闭" : "返回"}
            </Button>
          )}
          {!job && !busy && plan && (
            <Button
              onClick={() => {
                setPlan(null);
                setPlanID("");
                setError(null);
              }}
            >
              修改选择
            </Button>
          )}
          {!job && (
            <Button
              type="primary"
              loading={!!busy}
              disabled={
                !verified ||
                restoring ||
                (!!planID && plan?.id !== planID) ||
                !!busy ||
                (plan ? expired || !planned?.files : !options.categories.length)
              }
              onClick={() => void (plan ? execute() : preview())}
            >
              {plan ? "确认清理" : "生成清理预览"}
            </Button>
          )}
          {job && !running && (
            <Button
              onClick={() => {
                setPlan(null);
                setPlanID("");
                setJob(null);
                setError(null);
                setNotice("");
              }}
            >
              再次选择
            </Button>
          )}
        </Space>
      }
    >
      {errorText ? (
        <Alert type="error" showIcon title={errorText} />
      ) : (
        <ErrorNotice
          error={error}
          retry={planID ? () => setReadVersion((v) => v + 1) : undefined}
        />
      )}
      {notice && <Alert type="info" title={notice} />}
      {busy === "preview" && (
        <Alert
          type="info"
          showIcon
          icon={<Spin size="small" />}
          title="正在核对清理范围，尚未删除文件"
          description="大型恢复材料可能需要较长时间。可取消等待，选择会保留。"
        />
      )}
      {restoring && <Spin aria-label="读取清理计划" />}
      {!plan && !planID && (
        <>
          {categoryList(
            (storage.suggested_categories || []).filter(
              (key) => storageNames[key] && !fullCacheCategories.includes(key),
            ),
          )}
          <Collapse
            ghost
            items={[
              {
                key: "advanced",
                label: "其他清理方式",
                children: (
                  <>
                    <Typography.Paragraph type="secondary">
                      清空有效缓存后，图片展示需要重新生成；推荐特征可能在下次主动查找时重新计算。清理不会启动模型或联网。
                    </Typography.Paragraph>
                    {categoryList([
                      ...fullCacheCategories,
                      "orphan_assets",
                      "terminal_jobs",
                    ])}
                  </>
                ),
              },
            ]}
          />
        </>
      )}
      {plan && planned && (
        <PlanContainer className="storage-plan">
          {job && <summary>本次清理范围与保留项目</summary>}
          <Descriptions
            column={{ xs: 1, sm: 2 }}
            items={[
              {
                key: "release",
                label: "预计释放",
                children: bytesText(planned.bytes),
              },
              { key: "files", label: "清理文件", children: planned.files },
              {
                key: "keep",
                label: "继续保留",
                children: `${bytesText(planned.protected_bytes)} · ${planned.protected_files} 个文件`,
              },
              {
                key: "expire",
                label: "预览有效至",
                children: time(plan.expires_at),
              },
            ]}
          />
          {expired && !job && (
            <Alert
              showIcon
              type="warning"
              title="预览已过期，请重新生成并核对。"
            />
          )}
          {planned.clears_valid_caches && (
            <Alert
              showIcon
              type="warning"
              title="包含有效缓存，后续使用需要重新生成或计算。"
            />
          )}
          {Object.entries(planned.impacts || {}).map(([id, reason]) => (
            <Alert
              key={id}
              showIcon
              type={reason === "removes_rollback_point" ? "warning" : "info"}
              title={storageReason(reason)}
            />
          ))}
          <p>
            当前封面、标签素材、用户备份与必要应用资料保持保护；执行时再次核对引用和文件身份。
          </p>
          {planned.files === 0 && (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="所选范围没有可清理文件"
            />
          )}
        </PlanContainer>
      )}
      {result && (
        <Card
          size="small"
          title={cleanupOutcome(
            plan?.summary as CleanupSummary | undefined,
            job || undefined,
          )}
          className="storage-result"
        >
          <Descriptions
            column={{ xs: 1, sm: 2 }}
            items={[
              {
                key: "bytes",
                label: "实际释放",
                children: bytesText(result.released_bytes),
              },
              {
                key: "deleted",
                label: "已删除",
                children: `${result.deleted_files ?? 0} 个文件`,
              },
              {
                key: "skipped",
                label: "已跳过",
                children: `${result.skipped_files ?? 0} 个文件`,
              },
              {
                key: "failed",
                label: "失败",
                children: `${result.failed_files ?? 0} 个文件`,
              },
              ...(result.unconfirmed_files
                ? [
                    {
                      key: "unconfirmed",
                      label: "释放量未确认",
                      children: `${result.unconfirmed_files} 个文件（计入跳过）`,
                    },
                  ]
                : []),
            ]}
          />
          {(result.skipped_files > 0 || result.failed_files > 0) && (
            <p>
              文件身份或引用变化时会跳过；中断后发现文件已不存在时，也计入跳过，不计入释放空间。
            </p>
          )}
        </Card>
      )}
      {job && (
        <Card size="small" title="清理任务" className="storage-result">
          <StateTag state={job.state} />
          {!result && (
            <p role="status" aria-live="polite">
              {cleanupOutcome(plan?.summary as CleanupSummary | undefined, job)}
            </p>
          )}
          <Typography.Text copyable>{job.id}</Typography.Text>
          {job.error_code && (
            <Typography.Text type="secondary">
              诊断：{job.error_code}
            </Typography.Text>
          )}
          {job.error_code && (
            <ErrorNotice
              error={
                new Error(cleanupFailure(result?.error_code || job.error_code))
              }
            />
          )}
        </Card>
      )}
    </Modal>
  );
}
