import { randomIdentity } from "../../shared/randomIdentity";
/** §2.1/§3.3: creation primary, compact history beside the active operation.
 * Ant tokens, 8/16/24 spacing; no nested explanation-card stack. */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useLocation, useNavigate } from "react-router";
import {
  Alert,
  Button,
  Empty,
  Grid,
  Modal,
  Progress,
  Segmented,
  Space,
  Spin,
  Tag,
  Tooltip,
} from "antd";
import {
  DownloadOutlined,
  ReloadOutlined,
  UploadOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import {
  ApiError,
  checkInstanceDownload,
  request,
  subscribeWrites,
  uploadInstance,
  writesPending,
} from "../../shared/api";
import { healthAccess, subscribeHealth } from "../../shared/healthAccess.mjs";
import { healthBytes } from "../../shared/healthFlow";
import type {
  InstanceActivation,
  InstanceOperation,
  InstanceState,
  RestoreDetail,
} from "../../shared/instanceFlow";
import {
  instanceActive,
  instanceRequestUncreated,
  instanceErrors,
  instancePhases,
  instanceStates,
  rememberRecoveryReturn,
} from "../../shared/instanceFlow";
import { ErrorNotice, time, useDirty } from "../common";
import { InstanceRestore } from "./InstanceRestore";
import type { PendingInstance, RestoreDraft } from "./instanceDrafts";
import { readInstanceMemory, writeInstanceMemory } from "./instanceDrafts";
import { useHealthRead } from "./useHealthRead";
import "./instance.css";
const base = "/maintenance/";
type Kind = "backups" | "restores";
export function InstanceWorkspace() {
  const desktop = !!Grid.useBreakpoint().md;
  const location = useLocation(),
    navigate = useNavigate(),
    q = new URLSearchParams(location.search);
  const kind: Kind = q.get("kind") === "restores" ? "restores" : "backups",
    id = q.get("operation") || "";
  const page = Math.max(
    1,
    Number(q.get(kind === "backups" ? "backup_page" : "restore_page")) || 1,
  );
  const global = useSyncExternalStore(
    subscribeHealth,
    healthAccess,
    healthAccess,
  );
  const service = useHealthRead<{ database_schema: number }>(
    "/settings/service",
    true,
  );
  const supported = !!service.data && service.data.database_schema >= 24;
  const [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null),
    [notice, setNotice] = useState("");
  const writing = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const working = busy || writing;
  const gate = useRef(false),
    controller = useRef<AbortController | null>(null),
    fileInput = useRef<HTMLInputElement>(null);
  const [sent, setSent] = useState<{ sent: number; total: number } | null>(
    null,
  );
  const [pending, setPending] = useState<PendingInstance | null>(() =>
    readInstanceMemory("pending", null),
  );
  const [drafts, setDrafts] = useState<Record<string, RestoreDraft>>(() =>
    readInstanceMemory("drafts", {}),
  );
  useDirty(!!pending || Object.values(drafts).some((v) => v.dirty));
  const [confirmation, setConfirmation] = useState<{
    type: "release" | "rollback";
    revision: number;
    id: string;
    key: string;
  } | null>(null);
  const [blocked, setBlocked] = useState(false);
  const selected = useHealthRead<InstanceOperation | RestoreDetail>(
    base + kind + "/" + encodeURIComponent(id),
    supported && !!id,
    true,
  );
  const op =
    selected.data &&
    ("operation" in selected.data ? selected.data.operation : selected.data);
  const history = useHealthRead<InstanceOperation[]>(
    `${base}${kind}?page=${page}&page_size=10`,
    supported,
    !!op && instanceActive(op.state),
  );
  const state = useHealthRead<InstanceState>(
    base + "instance-state",
    supported,
    true,
  );
  const activation = useHealthRead<InstanceActivation>(
    base + "instance-activation",
    supported,
    true,
  );
  const log = activation.data;
  const frozenDraft = drafts[id];
  const rolledBack = !!op && log?.id === op.id && log.state === "rolled_back";
  function url(patch: Record<string, string | null>) {
    const next = new URLSearchParams(location.search);
    next.set("section", "recovery");
    for (const [key, value] of Object.entries(patch))
      if (value === null) next.delete(key);
      else next.set(key, value);
    void navigate("/admin/maintenance?" + next, { replace: false });
  }
  function choose(operation: InstanceOperation) {
    url({
      operation: operation.id,
      kind: operation.kind === "restore" ? "restores" : "backups",
    });
  }
  const updateDraft = useCallback(
    (draft: RestoreDraft) => {
      setDrafts((prior) => {
        const value = { ...prior, [id]: draft };
        writeInstanceMemory("drafts", value);
        return value;
      });
    },
    [id],
  );
  function keepPending(value: PendingInstance | null) {
    setPending(value);
    writeInstanceMemory("pending", value);
  }
  function refresh() {
    service.reload();
    selected.reload();
    history.reload();
    state.reload();
    activation.reload();
    window.dispatchEvent(new Event("health-state-refresh"));
  }
  async function run(work: () => Promise<void>) {
    if (gate.current) return;
    gate.current = true;
    setBusy(true);
    setError(null);
    setNotice("");
    try {
      await work();
    } catch (e) {
      setError(
        e instanceof ApiError && instanceErrors[e.code]
          ? new ApiError(e.status, e.code, instanceErrors[e.code], e.requestId)
          : e,
      );
    } finally {
      gate.current = false;
      setBusy(false);
    }
  }
  async function createBackup() {
    await run(async () => {
      const identity = { kind: "backups" as const, key: randomIdentity() };
      keepPending(identity);
      try {
        const value = await request<InstanceOperation>(
          base + "backups",
          "POST",
          { request_key: identity.key },
        );
        keepPending(null);
        choose(value);
        refresh();
      } catch (error) {
        // Only proven pre-creation rejections release this request identity.
        if (
          error instanceof ApiError &&
          instanceRequestUncreated(error.status, error.code)
        )
          keepPending(null);
        throw error;
      }
    });
  }
  async function upload(file: File) {
    if (pending || gate.current) return;
    if (
      file.size === 0 ||
      file.size > 256 * 1024 ** 3 ||
      !file.name.toLowerCase().endsWith(".zip")
    ) {
      setError(Error("请选择完整ZIP备份包，大小须大于0且不超过256 GiB。"));
      return;
    }
    const own = new AbortController();
    controller.current = own;
    await run(async () => {
      const identity = { kind: "restores" as const, key: randomIdentity() };
      keepPending(identity);
      setSent({ sent: 0, total: file.size });
      try {
        const value = await uploadInstance<InstanceOperation>(
          file,
          identity.key,
          own.signal,
          (sent, total) => setSent({ sent, total }),
        );
        keepPending(null);
        choose(value);
        refresh();
      } catch (error) {
        if (
          error instanceof ApiError &&
          instanceRequestUncreated(error.status, error.code)
        )
          keepPending(null);
        throw error;
      } finally {
        controller.current = null;
        setSent(null);
      }
    });
  }
  async function findPending() {
    if (!pending) return;
    const captured = pending;
    await run(async () => {
      const items = await request<InstanceOperation[]>(
        `${base}${captured.kind}?${new URLSearchParams({ request_key: captured.key, page: "1" })}`,
      );
      if (items[0]) {
        keepPending(null);
        choose(items[0]);
        refresh();
      } else
        setNotice(
          "此刻尚未查到操作，迟到请求仍可能落库。原请求身份保留，请稍后再次查询；不会自动新建或重传。",
        );
    });
  }
  async function openOwner() {
    if (!state.data?.operation_id) return;
    const owner = state.data.operation_id;
    await run(async () => {
      let value: InstanceOperation;
      try {
        value = await request<InstanceOperation>(base + "backups/" + owner);
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 404)) throw e;
        value = (await request<RestoreDetail>(base + "restores/" + owner))
          .operation;
      }
      choose(value);
    });
  }
  async function download() {
    if (!op || op.state !== "completed" || op.kind !== "backup") return;
    await run(async () => {
      try {
        await checkInstanceDownload(op.id);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404)
          throw new ApiError(
            404,
            e.code,
            "备份记录存在，但完整包已不可用；请核对服务上的备份文件或重新创建备份。",
            e.requestId,
          );
        throw e;
      }
      const link = document.createElement("a");
      link.href = `/api/v1/maintenance/backups/${encodeURIComponent(op.id)}/download`;
      link.download = `instance-${op.id}.zip`;
      link.click();
      setNotice("已交给浏览器下载。包未加密，请按秘密保管。");
    });
  }
  async function finishConfirmation() {
    const captured = confirmation;
    if (!captured || blocked) return;
    await run(async () => {
      try {
        if (captured.type === "release")
          await request(base + "instance-state/release", "POST", {
            expected_revision: captured.revision,
            allow_automatic_background: true,
          });
        else
          await request(base + `restores/${captured.id}/rollback`, "POST", {
            expected_revision: captured.revision,
            request_key: captured.key,
          });
        setConfirmation(null);
        refresh();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) setBlocked(true);
        throw e;
      }
    });
  }
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    if (op?.state === "awaiting_restart" || log?.requires_restart)
      rememberRecoveryReturn(location.pathname + location.search);
  }, [op?.state, log?.requires_restart, location.pathname, location.search]);
  const activationError =
    activation.error instanceof ApiError && activation.error.status === 404
      ? null
      : activation.error;
  const unsupported = !!service.data && !supported;
  return (
    <section className="instance-workspace" aria-label="备份与恢复">
      <div className="instance-toolbar">
        <Tag>全局</Tag>
        <Button
          type={!id ? "primary" : "default"}
          icon={<SafetyCertificateOutlined aria-hidden="true" />}
          disabled={
            !supported || working || !!pending || global.state?.phase !== "idle"
          }
          onClick={() => void createBackup()}
        >
          创建备份
        </Button>
        <Button
          icon={<UploadOutlined aria-hidden="true" />}
          disabled={!supported || working || !!pending}
          onClick={() => fileInput.current?.click()}
        >
          上传备份包
        </Button>
        <Tooltip title="刷新备份与恢复" trigger={["hover", "focus"]}>
          <Button
            type="text"
            aria-label="刷新备份与恢复"
            icon={<ReloadOutlined />}
            disabled={working}
            onClick={refresh}
          />
        </Tooltip>
        <input
          ref={fileInput}
          type="file"
          accept=".zip,application/zip"
          aria-label="选择整实例备份包"
          hidden
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (file) void upload(file);
          }}
        />
      </div>
      <p className="instance-private">
        私有完整包未加密，含密码摘要、凭据和私人资料。包含数据库及应用素材，不含源漫画、模型、运行环境或程序。
      </p>
      {!service.data && !service.error && <Spin />}
      <ErrorNotice error={service.error} retry={service.reload} />
      {unsupported && (
        <Alert
          type="info"
          showIcon
          title="当前服务尚未支持整实例备份与恢复，请升级至schema24后刷新。"
        />
      )}
      {sent && (
        <div className="instance-transfer" role="status">
          <Progress
            percent={Math.floor((sent.sent / Math.max(sent.total, 1)) * 100)}
            status="active"
          />
          <span>
            已传输 {healthBytes(sent.sent)} / {healthBytes(sent.total)}
            {sent.sent === sent.total ? " · 等待服务端保存收据" : ""}
          </span>
          <Button onClick={() => controller.current?.abort()}>
            停止上传并核对状态
          </Button>
        </div>
      )}
      {pending && !sent && (
        <Alert
          type="warning"
          showIcon
          title="请求结果待核对，原请求身份已保留"
          description={
            <Space wrap>
              <span>
                不自动新建或重传；请按原请求身份找回，查到后才能取消或放弃。
              </span>
              <Button
                disabled={working || !supported}
                onClick={() => void findPending()}
              >
                查询本次操作
              </Button>
            </Space>
          }
        />
      )}
      <ErrorNotice
        error={
          confirmation ||
          (error instanceof ApiError && error.code === "revision_conflict")
            ? null
            : error
        }
      />
      {notice && <Alert type="info" title={notice} showIcon />}
      <ErrorNotice error={state.error} retry={state.reload} />
      {state.data?.operation_id && state.data.operation_id !== id && (
        <div className="instance-hold">
          <Tag color="warning">整实例维护保护</Tag>
          <Button disabled={working} onClick={() => void openOwner()}>
            查看当前操作
          </Button>
        </div>
      )}
      {state.data?.background_hold && !state.data.operation_id && (
        <div className="instance-hold">
          <span>后台自动处理已暂停，业务编辑可用。</span>
          <Button
            disabled={working || global.error || global.state?.phase !== "idle"}
            onClick={() => {
              setBlocked(false);
              setConfirmation({
                type: "release",
                revision: state.data!.revision,
                id: "",
                key: "",
              });
            }}
          >
            恢复后台自动处理
          </Button>
        </div>
      )}
      {supported && (
        <>
          {log && !(log.id === id && log.requires_restart) && (
            <div className="instance-activation" aria-label="重启激活结果">
              <Space wrap>
                <Tag color={log.state === "completed" ? "success" : "warning"}>
                  {log.state === "completed"
                    ? "恢复已激活"
                    : instanceStates[log.state] || "激活状态待核对"}
                </Tag>
                {log.total_files > 0 && (
                  <span>
                    {log.completed_files} / {log.total_files} 个文件
                  </span>
                )}
                {log.state === "completed" && !log.materials_cleaned && (
                  <Button
                    disabled={working}
                    onClick={() => {
                      setBlocked(false);
                      setConfirmation({
                        type: "rollback",
                        revision: log.revision,
                        id: log.id,
                        key: randomIdentity(),
                      });
                    }}
                  >
                    核对并安排回退
                  </Button>
                )}
              </Space>
              {log.materials_cleaned && <Tag>材料已清理 · 回退不可用</Tag>}
              {log.requires_restart && (
                <p>
                  已安排操作，尚未完成切换。请正常停止并重启服务，重新登录后核对这里的结果。
                </p>
              )}
              {log.state === "completed" && (
                <p className="instance-muted">
                  账号已由备份替换。新业务写入后不可整实例回退，后台调度与原暂停任务不会自动恢复。
                </p>
              )}
            </div>
          )}
          <ErrorNotice error={activationError} retry={activation.reload} />
          <div className="instance-columns">
            <aside aria-label="备份与恢复记录">
              <details
                className="instance-history"
                open={desktop ? true : undefined}
              >
                <summary>备份与恢复记录</summary>
                <Segmented
                  block
                  aria-label="记录类型"
                  value={kind}
                  options={[
                    { label: "备份", value: "backups" },
                    { label: "恢复", value: "restores" },
                  ]}
                  disabled={working}
                  onChange={(value) => url({ kind: value, operation: null })}
                />
                <ErrorNotice error={history.error} retry={history.reload} />
                {history.data?.map((v) => (
                  <button
                    key={v.id}
                    className={`instance-history-item ${v.id === id ? "selected" : ""}`}
                    aria-pressed={v.id === id}
                    onClick={() => choose(v)}
                    disabled={working}
                  >
                    <strong>{instanceStates[v.state] || "状态待核对"}</strong>
                    <span>{time(v.created_at)}</span>
                    {v.materials_cleaned && (
                      <span>材料已清理 · 回退不可用</span>
                    )}
                    <span>
                      {healthBytes(v.bytes)} · {v.id.slice(0, 8)}
                    </span>
                  </button>
                ))}
                {history.data?.length === 0 && (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description="暂无记录"
                  />
                )}
                <Space className="instance-pager">
                  <Button
                    disabled={page <= 1 || working}
                    onClick={() =>
                      url({
                        [kind === "backups" ? "backup_page" : "restore_page"]:
                          String(page - 1),
                      })
                    }
                  >
                    上一页
                  </Button>
                  <span>第 {page} 页</span>
                  <Button
                    disabled={history.data?.length !== 10 || working}
                    onClick={() =>
                      url({
                        [kind === "backups" ? "backup_page" : "restore_page"]:
                          String(page + 1),
                      })
                    }
                  >
                    下一页
                  </Button>
                </Space>
              </details>
            </aside>
            <div className="instance-current">
              <ErrorNotice error={selected.error} retry={selected.reload} />
              {!id ? (
                <Empty
                  image={<SafetyCertificateOutlined aria-hidden="true" />}
                  description="创建备份，或从记录中选择操作查看进度"
                />
              ) : !op ? (
                <Spin />
              ) : (
                <>
                  <div className="instance-operation-head">
                    <h2>{op.kind === "backup" ? "实例备份" : "实例恢复"}</h2>
                    <Tag
                      color={
                        op.state === "completed"
                          ? "success"
                          : op.error_code
                            ? "error"
                            : "default"
                      }
                    >
                      {rolledBack
                        ? "已回退"
                        : instanceStates[op.state] || "状态待核对"}
                    </Tag>
                    <span>{time(op.updated_at)}</span>
                  </div>
                  {op.materials_cleaned && <Tag>材料已清理 · 回退不可用</Tag>}
                  {!["preparing", "awaiting_restart"].includes(op.state) && (
                    <p>
                      {rolledBack
                        ? "已完成回退，历史操作收据保留"
                        : instancePhases[op.phase] ||
                          instanceStates[op.state] ||
                          "等待状态"}
                      {op.bytes > 0 ? ` · ${healthBytes(op.bytes)}` : ""}
                    </p>
                  )}
                  <details className="instance-diagnostics">
                    <summary>技术标识</summary>
                    <p>
                      操作 {op.id} · 版本 {op.revision}
                    </p>
                    {selected.data &&
                      "summary" in selected.data &&
                      selected.data.summary && (
                        <p>schema {selected.data.summary.schema_version}</p>
                      )}
                    {op.sha256 && <code>{op.sha256}</code>}
                  </details>
                  {op.kind === "backup" ? (
                    <>
                      {op.error_code && (
                        <Alert
                          type="error"
                          showIcon
                          title={
                            instanceErrors[op.error_code] ||
                            "备份未完成，请核对当前阶段。"
                          }
                        />
                      )}
                      {op.state === "completed" ? (
                        <Button
                          type="primary"
                          icon={<DownloadOutlined aria-hidden="true" />}
                          disabled={working}
                          onClick={() => void download()}
                        >
                          下载完整备份
                        </Button>
                      ) : (
                        <p className="instance-muted">
                          只在冻结快照期间保护写入，随后压缩独立材料。未完成的包不可下载；已暂停任务不会自动恢复。
                        </p>
                      )}
                      {op.state !== "completed" && op.state !== "cancelled" && (
                        <Button
                          danger
                          disabled={working}
                          onClick={() =>
                            void run(async () => {
                              await request(
                                `${base}backups/${op.id}/cancel`,
                                "POST",
                                { expected_revision: op.revision },
                              );
                              refresh();
                            })
                          }
                        >
                          {op.state === "interrupted"
                            ? "取消并释放此操作保护"
                            : "取消备份"}
                        </Button>
                      )}
                    </>
                  ) : rolledBack ? (
                    <p>已恢复此次替换前的整实例。原暂停任务仍需手动恢复。</p>
                  ) : op.materials_cleaned ? (
                    <p>
                      操作收据保留。恢复工作材料已清理，如需再次恢复，请重新上传备份包。
                    </p>
                  ) : selected.data && "operation" in selected.data ? (
                    <InstanceRestore
                      detail={selected.data}
                      draft={frozenDraft}
                      onDraft={updateDraft}
                      busy={working}
                      error={error}
                      run={run}
                      reload={refresh}
                    />
                  ) : null}
                </>
              )}
            </div>
          </div>
        </>
      )}
      <Modal
        title={
          confirmation?.type === "release"
            ? "恢复后台自动处理"
            : "确认安排整实例回退"
        }
        open={!!confirmation}
        onCancel={() => {
          if (!working) {
            setConfirmation(null);
            setError(null);
          }
        }}
        closable={!working}
        mask={{ closable: !working }}
        keyboard={!working}
        footer={
          <Space>
            <Button
              disabled={working}
              onClick={() => {
                setConfirmation(null);
                setError(null);
              }}
            >
              返回
            </Button>
            <Button
              type="primary"
              danger={confirmation?.type === "rollback"}
              loading={working}
              disabled={blocked}
              onClick={() => void finishConfirmation()}
            >
              {confirmation?.type === "release"
                ? "确认恢复后台调度"
                : "确认回退并等待重启"}
            </Button>
          </Space>
        }
      >
        {!blocked &&
          (confirmation?.type === "release" ? (
            <p>
              将恢复周期扫描、导入后续、推荐重验等自动编排。原有paused任务仍需逐项手动恢复；不会改变增强功能开关。
            </p>
          ) : (
            <p>
              将恢复此次替换前保全的整实例，需正常停止并重启应用，之后重新登录。仅原构建且没有新业务写入时可用；发生新编辑会拒绝覆盖。
            </p>
          ))}
        {blocked && (
          <Alert
            type="warning"
            title={
              confirmation?.type === "rollback"
                ? "当前条件已变化，未安排回退"
                : "后台暂停状态已变化"
            }
            description={
              confirmation?.type === "rollback"
                ? "请返回核对。恢复后若已发生新的业务写入，就不能整实例回退。"
                : "请返回重新核对后台暂停状态，再明确恢复。"
            }
          />
        )}
        <ErrorNotice error={blocked ? null : error} />
        {blocked && error instanceof ApiError && error.requestId && (
          <details>
            <summary>请求编号</summary>
            <code>{error.requestId}</code>
          </details>
        )}
      </Modal>
    </section>
  );
}
