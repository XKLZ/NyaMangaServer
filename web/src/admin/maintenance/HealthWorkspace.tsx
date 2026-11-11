import { randomIdentity } from "../../shared/randomIdentity";
import type { CSSProperties } from "react";
/** §2.1/§3.6: compact global controls, report first, one repair confirmation.
 * Existing Ant tokens; secondary history, technical details folded, 8/16/24 spacing. */
import {
  useCallback,
  useEffect,
  useState,
  useSyncExternalStore,
  useRef,
} from "react";
import { useLocation, useNavigate } from "react-router";
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Descriptions,
  Empty,
  Grid,
  InputNumber,
  Modal,
  Pagination,
  Select,
  Space,
  Table,
  Tag,
  theme,
} from "antd";
import { ReloadOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import {
  ApiError,
  request,
  subscribeWrites,
  writesPending,
} from "../../shared/api";
import type { Library } from "../../shared/api";
import {
  healthAccess,
  registerHealthJob,
  subscribeHealth,
  updateHealth,
} from "../../shared/healthAccess.mjs";
import {
  activeHealthJob,
  checkPhases,
  healthActions,
  healthBytes,
  healthCategories,
  healthConclusion,
  healthModes,
  healthStatuses,
  repairable,
  healthSelection,
  healthSelectionLimit,
  sourceBudget,
} from "../../shared/healthFlow";
import type {
  HealthCheck,
  HealthFinding,
  HealthMode,
  HealthPage,
  HealthPlan,
} from "../../shared/healthFlow";
import { ErrorNotice, time, useDirty } from "../common";
import { useHealthRead } from "./useHealthRead";
import { HealthPreferred } from "./HealthPreferred";
import { HealthTaskHistory } from "./HealthTaskHistory";
import { HealthRepairView } from "./HealthRepairView";
import "./health.css";
type Draft = {
  selected: Record<string, HealthFinding>;
  preferred: Record<string, { id: string; title: string }>;
  required: Record<string, boolean>;
  allowSources: boolean;
};
const emptyDraft = (): Draft => ({
  selected: {},
  preferred: {},
  required: {},
  allowSources: false,
});
const findingNames: Record<string, string> = {
  preferred_invalid: "主版本不属于当前作品",
  effective_mismatch: "有效字段、标签或搜索投影不一致",
  effective_valid: "元数据与搜索投影一致",
  tag_evidence_valid: "原作与角色证据一致",
  tag_evidence_mismatch: "原作或角色派生证据不一致",
  database_foreign_keys_invalid: "数据库外键关系异常",
  foreign_key_invalid: "数据库外键关系异常",
  foreign_key_valid: "外键关系校验通过",
  database_identity_invalid: "数据库身份不符合预期",
  database_integrity_invalid: "数据库完整性异常",
  migration_ledger_invalid: "迁移记录或校验和异常",
  schema_definition_mismatch: "表或索引定义不符合预期",
  archive_verified: "归档与受支持图片校验通过",
  database_identity_valid: "数据库身份有效",
  database_integrity_valid: "数据库完整性校验通过",
  schema_definition_valid: "表与索引定义符合预期",
  migration_ledger_valid: "迁移记录与校验和一致",
  dictionary_not_installed: "未安装译库，无需检查",
  dictionary_input_missing: "缺少原始译库缓存",
  dictionary_index_invalid: "译库派生索引不一致",
  dictionary_check_unavailable: "暂时无法核对译库",
  library_disabled: "书库已停用，未读源文件",
  root_unavailable: "书库根目录不可访问",
  root_identity_changed: "书库根身份已变化",
  root_unconfigured: "书库根未配置",
  source_unavailable: "源文件不可访问",
  source_changed: "源文件已变化",
  source_path_unsafe: "源路径需要人工核对",
  source_not_regular: "源对象不是普通文件",
  resource_limit: "达到读取或解码限制，未完成检查",
  managed_file_unavailable: "素材或派生文件不可访问",
  managed_content_invalid: "已保存文件的内容校验失败",
  managed_not_regular: "保存对象不是普通文件",
  managed_path_unsafe: "保存路径需要人工核对",
  feature_version_unsupported: "派生特征版本无法验证",
  derived_evicted: "派生缓存已正常淘汰",
  checkpoint_file_missing: "有效任务检查点引用的文件缺失",
  interrupted: "检查已中断",
  series_positions_gap: "系列编号存在间断",
  series_positions_start: "系列编号起点不符合规则",
  projection_mismatch: "元数据或搜索投影不一致",
  projection_invalid: "元数据或搜索投影不一致",
  target_changed_since_start: "检测后对象已变化",
  quick_scope: "快速检查未覆盖此项",
  outside_requested_mode: "不在本次检查模式内",
  verified: "校验通过",
  work_valid: "作品关系正常",
  series_valid: "系列关系正常",
  edition_file_valid: "版本文件身份正常",
  empty_work: "作品没有版本",
  file_identity_missing: "文件身份不完整",
  projection_input_incomplete: "重建依据不完整",
  source_budget_exceeded: "达到深检读取预算",
  source_disabled: "书库已停用，未读取源文件",
};
function problem(f: HealthFinding) {
  return (
    findingNames[f.code] ||
    (f.status === "normal"
      ? "本项已验证"
      : healthActions[f.repair]
        ? `${healthCategories[f.category] || "资料"}校验发现异常`
        : `${healthCategories[f.category] || "资料"}需要核对`)
  );
}
export function HealthWorkspace() {
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const location = useLocation(),
    navigate = useNavigate(),
    params = new URLSearchParams(location.search);
  const id = params.get("check") || "",
    repairID = params.get("repair") || "";
  const page = Math.max(1, Number(params.get("health_page")) || 1),
    findingPage = Math.max(1, Number(params.get("finding_page")) || 1),
    status = params.get("health_status") || "confirmed",
    category = params.get("health_category") || "";
  function url(values: Record<string, string | number | null>) {
    const q = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(values)) {
      if (v === null || v === "") q.delete(k);
      else q.set(k, String(v));
    }
    void navigate({ search: q.toString() }, { preventScrollReset: true });
  }
  const global = useSyncExternalStore(
      subscribeHealth,
      healthAccess,
      healthAccess,
    ),
    busy = useSyncExternalStore(subscribeWrites, writesPending, writesPending),
    supported = global.supported === true;
  const [mode, setMode] = useState<HealthMode>("application"),
    [libraries, setLibraries] = useState<string[]>([]),
    [budget, setBudget] = useState(8),
    [drafts, setDrafts] = useState<Record<string, Draft>>({}),
    [plan, setPlan] = useState<HealthPlan | null>(null),
    [planBlocked, setPlanBlocked] = useState(false),
    [error, setError] = useState<unknown>(null),
    [working, setWorking] = useState(false);
  const draft = drafts[id] || emptyDraft(),
    selected = Object.values(draft.selected);
  const check = useHealthRead<HealthCheck>(
    `/maintenance/health-checks/${encodeURIComponent(id)}`,
    supported && !!id,
    true,
  );
  const history = useHealthRead<HealthPage<HealthCheck>>(
    `/maintenance/health-checks?page=${page}&page_size=10`,
    supported,
  );
  const findings = useHealthRead<HealthPage<HealthFinding>>(
    `/maintenance/health-checks/${encodeURIComponent(id)}/findings?${new URLSearchParams({ page: String(findingPage), page_size: "25", ...(status && status !== "all" ? { status } : {}), ...(category ? { category } : {}) })}`,
    supported && !!id,
    activeHealthJob(check.data?.state),
  );
  const libs = useHealthRead<{ items: Library[] }>(
    "/libraries",
    supported && mode === "sources",
  );
  if (check.data) registerHealthJob(check.data.job_id);
  useDirty(
    Object.values(drafts).some((v) => Object.keys(v.selected).length > 0) ||
      mode !== "application" ||
      libraries.length > 0 ||
      !!plan,
  );
  const change = useCallback(
    (key: string, edit: (v: Draft) => Draft) =>
      setDrafts((old) => {
        const prior = old[key] || emptyDraft(),
          next = edit(prior);
        return next === prior ? old : { ...old, [key]: next };
      }),
    [setDrafts],
  );
  function required(work: string, v: boolean) {
    change(id, (d) =>
      d.required[work] === v
        ? d
        : { ...d, required: { ...d.required, [work]: v } },
    );
  }
  const createKey = useRef<{ input: string; key: string }>({
      input: "",
      key: "",
    }),
    executeKey = useRef<{ id: string; key: string }>({ id: "", key: "" });
  const locked = busy || working;
  async function act(fn: () => Promise<void>) {
    if (locked) return;
    setWorking(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e);
    } finally {
      setWorking(false);
      window.dispatchEvent(new Event("health-state-refresh"));
    }
  }
  function refresh() {
    check.reload();
    history.reload();
    findings.reload();
    window.dispatchEvent(new Event("health-state-refresh"));
  }
  async function start() {
    await act(async () => {
      const input = {
        mode,
        ...(mode === "sources"
          ? { library_ids: libraries, max_bytes: sourceBudget(budget) }
          : {}),
      };
      if (mode === "sources" && !libraries.length)
        throw Error("请选择启用的书库");
      const key = JSON.stringify(input);
      if (createKey.current.input !== key)
        createKey.current = { input: key, key: randomIdentity() };
      const result = await request<HealthCheck>(
        "/maintenance/health-checks",
        "POST",
        { ...input, request_key: createKey.current.key },
      );
      registerHealthJob(result.job_id);
      createKey.current = { input: "", key: "" };
      url({ check: result.id, repair: null, finding_page: null });
      history.reload();
    });
  }
  async function preview() {
    await act(async () => {
      try {
        const value = await request<HealthPlan>(
          "/maintenance/health-repair-plans",
          "POST",
          {
            request_key: randomIdentity(),
            check_id: id,
            finding_ids: selected.map((f) => f.id),
            preferred_editions: Object.fromEntries(
              Object.entries(draft.preferred).map(([k, v]) => [k, v.id]),
            ),
            allow_sources: draft.allowSources,
          },
        );
        setPlan(value);
        setPlanBlocked(false);
        executeKey.current = { id: value.id, key: randomIdentity() };
      } catch (e) {
        if (e instanceof ApiError && e.code === "health_result_stale")
          setError(
            Error(
              "检查结果已过期，选择保留。请重新检查并核对，不能直接重试旧结果。",
            ),
          );
        else throw e;
      }
    });
  }
  async function execute() {
    if (!plan || planBlocked) return;
    await act(async () => {
      try {
        const repair = await request<
          import("../../shared/healthFlow").HealthRepair
        >(
          `/maintenance/health-repair-plans/${encodeURIComponent(plan.id)}/execute`,
          "POST",
          {
            request_key: executeKey.current.key,
            expected_revision: plan.revision,
            confirm_maintenance: true,
          },
        );
        registerHealthJob(repair.job_id);
        updateHealth({ supported: true, error: true });
        setPlan(null);
        change(id, () => emptyDraft());
        url({ repair: repair.id });
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) setPlanBlocked(true);
        throw e;
      }
    });
  }
  useEffect(() => {
    if (!plan) return;
    const timer = setTimeout(
      () => setPlanBlocked(true),
      Math.max(0, plan.expires_at * 1000 - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [plan]);
  const previewEnabled =
    selected.length > 0 &&
    selected.length <= healthSelectionLimit(selected) &&
    selected.every(
      (f) =>
        f.repair !== "preferred" ||
        draft.required[f.object_id] === false ||
        !!draft.preferred[f.object_id],
    );
  return (
    <section
      className="health-workspace"
      aria-label="数据健康"
      style={
        {
          "--health-surface": token.colorBgContainer,
          "--health-border": token.colorBorderSecondary,
        } as CSSProperties
      }
    >
      <div className="health-toolbar">
        <Tag>全局</Tag>
        <Select
          aria-label="检查模式"
          value={mode}
          disabled={locked}
          onChange={setMode}
          options={Object.entries(healthModes).map(([value, label]) => ({
            value,
            label,
          }))}
        />
        <Button
          type={selected.length ? "default" : "primary"}
          disabled={
            !supported ||
            locked ||
            global.error ||
            global.state?.phase !== "idle"
          }
          onClick={() => void start()}
        >
          启动检查
        </Button>
        <Button
          type="text"
          icon={<ReloadOutlined />}
          aria-label="刷新健康报告"
          disabled={locked}
          onClick={refresh}
        />
      </div>
      {!supported && (
        <Alert
          type="info"
          title={
            global.supported === false
              ? "当前服务尚未支持数据健康，请升级后刷新。"
              : "正在核对服务支持状态…"
          }
        />
      )}
      {mode === "sources" && (
        <div className="health-source-options">
          <Select
            mode="multiple"
            aria-label="深检书库"
            placeholder="明确选择需要读取的启用书库"
            value={libraries}
            disabled={locked}
            onChange={setLibraries}
            options={libs.data?.items.map((v) => ({
              value: v.id,
              label: v.display_name + (v.disabled ? "（停用，不可深检）" : ""),
              disabled: !!v.disabled || !!v.trashed_at,
            }))}
          />
          <label>
            读取预算{" "}
            <InputNumber
              aria-label="深检读取预算GiB"
              min={0.001}
              max={1024}
              value={budget}
              disabled={locked}
              onChange={(v) => setBudget(v ?? 8)}
            />{" "}
            GiB
          </label>
          <p>未压缩图片载荷尝试预算，默认8 GiB、上限1 TiB；不是磁盘计量。</p>
          <ErrorNotice error={libs.error} retry={libs.reload} />
        </div>
      )}
      <ErrorNotice error={error} />
      {supported &&
        (repairID ||
          global.state?.repair_id ||
          global.state?.phase === "recovery_required") && (
          <HealthRepairView
            key={repairID || global.state?.repair_id || "recover"}
            id={repairID || global.state?.repair_id || ""}
            refreshReport={refresh}
          />
        )}
      <div className="health-columns">
        <details className="health-history" open={!!screens.md}>
          <summary>检查历史与维护记录</summary>
          <aside aria-label="检查历史">
            <h2>检查历史</h2>
            <Collapse
              size="small"
              destroyOnHidden
              items={[
                {
                  key: "tasks",
                  label: "修复与维护任务历史",
                  children: (
                    <HealthTaskHistory
                      page={Math.max(
                        1,
                        Number(params.get("health_task_page")) || 1,
                      )}
                      id={params.get("health_task") || ""}
                      update={url}
                    />
                  ),
                },
              ]}
            />
            <ErrorNotice error={history.error} retry={history.reload} />
            {history.data?.items.map((v) => (
              <button
                className={`health-history-item ${id === v.id ? "selected" : ""}`}
                key={v.id}
                data-check-id={v.id}
                onClick={() => url({ check: v.id, finding_page: null })}
              >
                <strong>{healthModes[v.mode]}</strong>
                <span>{time(v.created_at)}</span>
                <span>{healthConclusion(v)}</span>
              </button>
            ))}
            {history.data?.total === 0 && <p>还没有检查记录</p>}
            <Pagination
              size="small"
              simple
              current={page}
              pageSize={10}
              total={history.data?.total || 0}
              onChange={(v) => url({ health_page: v })}
            />
          </aside>
        </details>
        <div className="health-report">
          <ErrorNotice error={check.error} retry={check.reload} />
          {!id ? (
            <Empty
              image={<SafetyCertificateOutlined />}
              description="启动检查，或选择一份历史报告"
            />
          ) : (
            check.data && (
              <>
                <h2>{healthConclusion(check.data)}</h2>
                <Space wrap>
                  <Tag>{healthModes[check.data.mode]}</Tag>
                  <span>{checkPhases[check.data.phase] || "正在检查"}</span>
                  <span>检测时点 {time(check.data.created_at)}</span>
                  <span>{check.data.checked} 条结果</span>
                </Space>
                <p className="health-muted">
                  {check.data.mode === "sources"
                    ? `明确选择的${check.data.scope.library_ids?.length || 0}个书库及全局应用数据 · 源载荷已尝试 ${healthBytes(check.data.source_bytes_read)}`
                    : check.data.mode === "quick"
                      ? "数据库与核心组织关系 · 不读取源漫画"
                      : "全部应用数据 · 不读取源漫画"}
                </p>
                <div className="health-counts">
                  {Object.entries(healthStatuses).map(([k, label]) => (
                    <Button
                      type={status === k ? "primary" : "text"}
                      key={k}
                      onClick={() =>
                        url({
                          health_status: status === k ? "all" : k,
                          finding_page: null,
                        })
                      }
                    >
                      {label} {check.data!.counts[k] || 0}
                    </Button>
                  ))}
                </div>
                <Collapse
                  size="small"
                  items={[
                    {
                      key: "scope",
                      label: "冻结范围与分类数量",
                      children: (
                        <>
                          <p>按检测时点冻结；结果条数不是完成对象的百分比。</p>
                          <Descriptions
                            size="small"
                            column={2}
                            items={Object.entries(check.data.target_counts).map(
                              ([k, v]) => ({
                                key: k,
                                label:
                                  (
                                    {
                                      work: "作品",
                                      series: "系列",
                                      edition: "版本",
                                      asset: "人工素材",
                                      files: "派生文件",
                                      source: "源文件",
                                      cover_cache: "封面缓存",
                                      tag_cache: "标签图片",
                                      recommendation_index: "推荐索引",
                                      dictionary: "译库",
                                      series_block: "系列派生文件",
                                      tasks: "任务",
                                    } as Record<string, string>
                                  )[k] || k,
                                children: v,
                              }),
                            )}
                          />
                          {Object.entries(check.data.categories).map(
                            ([k, v]) => (
                              <p key={k}>
                                {healthCategories[k] || k}：
                                {Object.entries(v)
                                  .map(
                                    ([s, n]) =>
                                      `${healthStatuses[s] || s} ${n}`,
                                  )
                                  .join(" · ")}
                              </p>
                            ),
                          )}
                        </>
                      ),
                    },
                  ]}
                />
                {activeHealthJob(check.data.state) ||
                check.data.state === "paused" ? (
                  <Space>
                    {["pause", "cancel", "resume"]
                      .filter((v) =>
                        v === "resume"
                          ? check.data!.state === "paused"
                          : activeHealthJob(check.data!.state),
                      )
                      .map((v) => (
                        <Button
                          key={v}
                          disabled={locked}
                          onClick={() =>
                            void act(async () => {
                              await request(
                                `/jobs/${encodeURIComponent(check.data!.job_id)}/${v}`,
                                "POST",
                              );
                              refresh();
                            })
                          }
                        >
                          {
                            {
                              pause: "暂停检查",
                              cancel: "取消检查",
                              resume: "继续检查",
                            }[v]
                          }
                        </Button>
                      ))}
                  </Space>
                ) : null}
                {check.data.error_code && (
                  <Alert
                    type="error"
                    title="检查未完整完成，不能据此判断全部健康"
                    description="请核对分类结果和任务状态；失败不代表没有问题。"
                  />
                )}
                <div className="health-toolbar">
                  <Select
                    aria-label="问题分类"
                    value={category}
                    options={[
                      { value: "", label: "全部分类" },
                      ...Object.entries(healthCategories).map(
                        ([value, label]) => ({ value, label }),
                      ),
                    ]}
                    onChange={(v) =>
                      url({ health_category: v, finding_page: null })
                    }
                  />
                  <Button
                    disabled={status === "all"}
                    onClick={() =>
                      url({ health_status: "all", finding_page: null })
                    }
                  >
                    全部状态
                  </Button>
                </div>
                <ErrorNotice error={findings.error} retry={findings.reload} />
                <Table<HealthFinding>
                  size="small"
                  rowKey="id"
                  dataSource={findings.data?.items || []}
                  pagination={false}
                  loading={!findings.data && !findings.error}
                  rowSelection={{
                    selectedRowKeys: Object.keys(draft.selected),
                    preserveSelectedRowKeys: true,
                    getCheckboxProps: (f) => ({
                      disabled:
                        locked ||
                        (!draft.selected[f.id] && !repairable(f)) ||
                        (!draft.selected[f.id] &&
                          selected.length >=
                            healthSelectionLimit([...selected, f])),
                    }),
                    onSelect: (f, checked) =>
                      change(id, (d) => {
                        const next = { ...d.selected };
                        if (checked) next[f.id] = f;
                        else delete next[f.id];
                        return { ...d, selected: next };
                      }),
                    onSelectAll: (checked, _rows, changed) => {
                      try {
                        const next = healthSelection(
                          draft.selected,
                          changed,
                          checked,
                        );
                        change(id, (d) => ({ ...d, selected: next }));
                        setError(null);
                      } catch (e) {
                        setError(e);
                      }
                    },
                  }}
                  columns={[
                    {
                      title: "结果",
                      render: (_, f) => (
                        <div>
                          <Tag
                            color={
                              f.status === "confirmed"
                                ? "error"
                                : f.status === "normal"
                                  ? undefined
                                  : "warning"
                            }
                          >
                            {healthStatuses[f.status] || f.status}
                          </Tag>
                          <strong>{problem(f)}</strong>
                          <span className="health-object">
                            {(
                              {
                                work: "作品",
                                series: "系列",
                                edition: "版本",
                                asset: "素材",
                                scope: "范围",
                                database: "数据库",
                                source: "源文件",
                                cover_cache: "封面缓存",
                                tag_cache: "标签图片",
                                recommendation_index: "推荐索引",
                                dictionary: "译库",
                                series_block: "系列派生文件",
                                tasks: "任务",
                              } as Record<string, string>
                            )[f.object_kind] || "检查对象"}{" "}
                            ·{" "}
                            {typeof f.details?.name === "string"
                              ? f.details.name
                              : f.object_id.slice(0, 8)}
                          </span>
                          {healthActions[f.repair] && (
                            <span className="health-object">
                              {healthActions[f.repair]}
                            </span>
                          )}
                        </div>
                      ),
                    },
                    {
                      title: "详情",
                      render: (_, f) => (
                        <details>
                          <summary>检测详情</summary>
                          <p>
                            对象：{f.object_kind} · {f.object_id}
                          </p>
                          <p>
                            检测：{time(f.checked_at)} · 版本{" "}
                            {f.object_revision}
                          </p>
                          <p>诊断代码：{f.code}</p>
                          {Object.entries(f.details || {})
                            .filter(([, v]) =>
                              ["string", "number", "boolean"].includes(
                                typeof v,
                              ),
                            )
                            .map(([k, v]) => (
                              <p key={k}>
                                {(
                                  {
                                    name: "系列名称",
                                    actual_start: "实际起点",
                                    expected_start: "预期起点",
                                    member_count: "成员数",
                                  } as Record<string, string>
                                )[k] || k}
                                ：{String(v)}
                              </p>
                            ))}
                        </details>
                      ),
                    },
                  ]}
                />
                <Pagination
                  size="small"
                  current={findingPage}
                  pageSize={25}
                  total={findings.data?.total || 0}
                  showSizeChanger={false}
                  onChange={(v) => url({ finding_page: v })}
                />
                {selected
                  .filter((f) => f.repair === "preferred")
                  .map((f) => (
                    <PreferredDraft
                      key={f.object_id}
                      finding={f}
                      value={draft.preferred[f.object_id]}
                      onChange={(v) =>
                        change(id, (d) => ({
                          ...d,
                          preferred: { ...d.preferred, [f.object_id]: v },
                        }))
                      }
                      onRequired={required}
                    />
                  ))}
                <div className="health-repair-footer">
                  <span>
                    已选 {selected.length} 项（最多
                    {healthSelectionLimit(selected)}项）
                  </span>
                  {selected.some((f) => f.repair === "cover_cache") && (
                    <Checkbox
                      checked={draft.allowSources}
                      disabled={locked}
                      onChange={(e) =>
                        change(id, (d) => ({
                          ...d,
                          allowSources: e.target.checked,
                        }))
                      }
                    >
                      允许此次封面修复读取源图片
                    </Checkbox>
                  )}
                  <Button
                    disabled={locked || !selected.length}
                    onClick={() => change(id, () => emptyDraft())}
                  >
                    清空选择
                  </Button>
                  <Button
                    type="primary"
                    disabled={
                      locked ||
                      !previewEnabled ||
                      global.error ||
                      global.state?.phase !== "idle"
                    }
                    onClick={() => void preview()}
                  >
                    预览修复影响
                  </Button>
                </div>
              </>
            )
          )}
        </div>
      </div>
      <Modal
        open={!!plan}
        title="确认修复影响"
        styles={{ body: { maxHeight: "60dvh", overflowY: "auto" } }}
        onCancel={() => !locked && setPlan(null)}
        closable={!locked}
        keyboard={!locked}
        mask={{ closable: !locked }}
        footer={
          <Space>
            <Button disabled={locked} onClick={() => setPlan(null)}>
              返回选择
            </Button>
            <Button
              type="primary"
              disabled={
                locked ||
                planBlocked ||
                !plan ||
                global.error ||
                global.state?.phase !== "idle"
              }
              onClick={() => void execute()}
            >
              确认维护并修复
            </Button>
          </Space>
        }
      >
        <ErrorNotice error={error} />
        {planBlocked && (
          <Alert
            type="warning"
            title="计划已过期或对象发生变化，选择保留。返回选择后明确重建预览。"
          />
        )}
        {plan && (
          <>
            <p>
              应用将短暂全局只读，暂停 {plan.paused_tasks}{" "}
              个任务；结束后由你手动恢复。
            </p>
            <Descriptions
              column={1}
              size="small"
              items={[
                {
                  key: "space",
                  label: "预计所需空间",
                  children: healthBytes(plan.required_bytes),
                },
                {
                  key: "snap",
                  label: "一份数据库回退快照",
                  children: healthBytes(plan.snapshot_bytes),
                },
                {
                  key: "source",
                  label: "读取源图片",
                  children: plan.actions.some((a) => a.requires_sources)
                    ? "本次明确允许"
                    : "不读取",
                },
                {
                  key: "rollback",
                  label: "回退方式",
                  children:
                    "仅恢复此次目标及必要旧文件，后续编辑冲突时拒绝覆盖",
                },
                {
                  key: "expire",
                  label: "预览有效至",
                  children: time(plan.expires_at),
                },
              ]}
            />
            <ul>
              {plan.actions.map((a, i) => (
                <li key={i}>
                  {healthActions[a.kind] || "修复目标"} ·{" "}
                  {a.object_id.slice(0, 8)} ·{" "}
                  {problem(
                    draft.selected[a.finding_id] ||
                      selected.find((f) => f.object_id === a.object_id)!,
                  )}
                </li>
              ))}
            </ul>
          </>
        )}
      </Modal>
    </section>
  );
}
function PreferredDraft({
  finding,
  value,
  onChange,
  onRequired,
}: {
  finding: HealthFinding;
  value?: { id: string; title: string };
  onChange: (v: { id: string; title: string }) => void;
  onRequired: (work: string, required: boolean) => void;
}) {
  const changeRequired = useCallback(
    (v: boolean) => onRequired(finding.object_id, v),
    [onRequired, finding.object_id],
  );
  return (
    <HealthPreferred
      work={finding.object_id}
      value={value}
      onChange={onChange}
      onRequired={changeRequired}
    />
  );
}
