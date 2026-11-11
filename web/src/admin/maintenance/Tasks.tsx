import { EncodingDialog } from "./EncodingDialog";
import { ScanDiagnostics, ScanStatus } from "./ScanDiagnostics";
import "./storage.css";
import { useRecommendationEnabled } from "../../shared/recommendations";
import { usePermissions, EditOnly } from "../../shared/permissions";
import { featureForJob } from "../../shared/enhancementFlow";
import { EnhancementStatus } from "../EnhancementStatus";
import { useLocation } from "react-router";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Progress,
  Select,
  Space,
  Table,
  Typography,
} from "antd";
import type { Job, JobItem, Listing } from "../../shared/api";
import { LibraryScopeControl, useCurrentLibrary } from "../CurrentLibrary";
import { request } from "../../shared/api";
import { metadata } from "../../shared/metadata";
import { reason } from "../../shared/catalog";
import {
  PageTitle,
  useURLValue,
  useRemote,
  useAction,
  useDirty,
  ActionNotice,
  ErrorNotice,
  StateTag,
  query,
  time,
} from "../common";
const stageNames: Record<string, string> = {
  enumerating: "枚举归档",
  processing: "处理成员",
  queued: "等待执行",
  completed: "完成",
  fetching: "获取来源",
  downloading: "下载译库",
  building: "构建译库",
  publishing: "保存结果",
  preparing: "准备特征",
  comparing: "比较版本",
  finished: "完成",
};
const names: Record<string, string> = {
  work_recommendation: "同作品推荐",
  series_recommendation: "系列推荐",
  scan: "书库扫描",
  metadata: "元数据获取",
  tag_translation: "中文译库更新",
  metadata_preview: "来源预览",
  metadata_search: "来源查询",
  maintenance: "维护操作",
  cover_rebuild: "封面重建",
};
export function TasksPage({
  id,
  navigate,
}: {
  id?: string;
  navigate: (path: string) => void;
}) {
  const location = useLocation();
  const [page, setPage] = useURLValue("page", 1);
  const [type, setType] = useURLValue("type", "");
  const [state, setState] = useURLValue("state", "");
  const scope = useCurrentLibrary();
  const library = scope.all ? "" : (scope.library?.id ?? "");
  const [itemPage, setItemPage] = useURLValue("member_page", 1);
  const [encodingBook, setEncodingBook] = useState("");
  const [selection, setSelection] = useState<number[]>([]);
  const [urls, setURLs] = useState<Record<string, string>>({});
  const [pausedRefresh, setPausedRefresh] = useState(false);
  const list = useRemote<Listing<Job>>(
    `/jobs?${query({ page, page_size: 50, type, state, library_id: library })}`,
    undefined,
    "GET",
    !id,
  );
  const job = useRemote<Job>(`/jobs/${id}`, undefined, "GET", !!id);
  const items = useRemote<Listing<JobItem>>(
    `/jobs/${id}/items?${query({ page: itemPage, page_size: 50 })}`,
    undefined,
    "GET",
    !!id,
  );
  const action = useAction();
  const releaseDraft = useDirty(Object.values(urls).some(Boolean));
  const reloadList = list.reload,
    reloadJob = job.reload,
    reloadItems = items.reload;
  useEffect(() => {
    if (pausedRefresh) return;
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") {
        reloadList();
        reloadJob();
        reloadItems();
      }
    }, 2000);
    return () => clearInterval(timer);
  }, [pausedRefresh, reloadList, reloadJob, reloadItems]);
  const j = job.data;
  const featureKey = featureForJob(j?.type),
    featureEnabled = useRecommendationEnabled(featureKey);
  const editable = usePermissions().edit(j?.library_id || "");
  const canExecute = editable && featureEnabled;
  async function control(actionName: string) {
    if (
      !editable ||
      !id ||
      (["resume", "retry"].includes(actionName) && !canExecute)
    )
      return;
    await action.run(async () => {
      if (actionName === "retry") {
        const r = await metadata.retry(
          id,
          j?.mode === "library" ? [] : selection,
          j?.mode === "library" ? {} : urls,
        );
        setURLs({});
        releaseDraft();
        navigate(taskURL(r.job_id, location.search));
      } else {
        await request(`/jobs/${id}/${actionName}`, "POST");
        job.reload();
        items.reload();
      }
    }, "任务操作已提交");
  }
  return (
    <>
      <PageTitle
        level={2}
        title={id ? "任务详情" : "任务中心"}
        subtitle="任务和成员结果持久化保存，暂停与取消后不会接受旧执行结果。"
        extra={
          id ? (
            <Space>
              <EditOnly library={j?.library_id || ""}>
                <ImportBatchLink id={id} navigate={navigate} />
              </EditOnly>
              {j?.type === "work_recommendation" && (
                <Button
                  onClick={() =>
                    navigate(
                      `/admin/recommendations?job=${id}${j.library_id ? `&library_id=${encodeURIComponent(j.library_id)}` : ""}`,
                    )
                  }
                >
                  查看整理方案
                </Button>
              )}
              {j?.type === "series_recommendation" && (
                <Button
                  onClick={() =>
                    navigate(
                      `/admin/series-recommendations?job=${id}${j.library_id ? `&library_id=${encodeURIComponent(j.library_id)}` : ""}`,
                    )
                  }
                >
                  查看系列方案
                </Button>
              )}
              <Button onClick={() => navigate(taskURL("", location.search))}>
                返回任务列表
              </Button>
            </Space>
          ) : undefined
        }
      />
      <ActionNotice action={action} />
      {encodingBook && (
        <EncodingDialog
          id={encodingBook}
          onClose={() => setEncodingBook("")}
          onChanged={() => {
            job.reload();
            items.reload();
          }}
        />
      )}
      {id && j && (
        <Typography.Title level={3} className="task-detail-heading">
          {names[j.type] ?? "当前任务"}
        </Typography.Title>
      )}
      {!id ? (
        <>
          <Card className="filter-card">
            <Space wrap>
              <Select
                aria-label="任务类型"
                value={type}
                options={[
                  { value: "", label: "全部类型" },
                  ...Object.entries(names).map(([value, label]) => ({
                    value,
                    label,
                  })),
                ]}
                onChange={(v) => {
                  setType(v);
                  setPage(1);
                }}
              />
              <Select
                aria-label="任务状态"
                value={state}
                options={[
                  { value: "", label: "全部状态" },
                  ...[
                    "queued",
                    "running",
                    "paused",
                    "completed",
                    "failed",
                    "cancelled",
                  ].map((value) => ({
                    value,
                    label: <StateTag state={value} />,
                  })),
                ]}
                onChange={(v) => {
                  setState(v);
                  setPage(1);
                }}
              />
              <LibraryScopeControl />
            </Space>
          </Card>
          <ErrorNotice error={list.error} />
          <Card className="table-card">
            <Table<Job>
              size="small"
              rowKey="id"
              loading={list.loading}
              dataSource={list.data?.items ?? []}
              scroll={{ x: 760 }}
              columns={[
                {
                  title: "任务",
                  render: (_: unknown, j: Job) => (
                    <Button
                      type="link"
                      onClick={() => navigate(taskURL(j.id, location.search))}
                    >
                      {names[j.type] ?? j.type}
                    </Button>
                  ),
                },
                {
                  title: "状态",
                  dataIndex: "state",
                  render: (state: string) => <StateTag state={state} />,
                },
                {
                  title: "阶段",
                  dataIndex: "stage",
                  render: (value: string) => stageNames[value] ?? value,
                },
                {
                  title: "进度",
                  render: (_: unknown, j: Job) =>
                    j.total === null ? "枚举中" : `${j.done} / ${j.total}`,
                },
                {
                  title: "处理结果",
                  render: (_: unknown, j: Job) =>
                    j.type === "scan" &&
                    (j.scan_completed || j.scan_partial || j.scan_blocked) ? (
                      <Space orientation="vertical" size={4}>
                        <span>{j.scan_completed || 0} 项完成</span>
                        {!!j.scan_partial && (
                          <span>{j.scan_partial} 项已入库，部分处理未完成</span>
                        )}
                        {!!j.scan_blocked && (
                          <span>{j.scan_blocked} 项无法检查</span>
                        )}
                      </Space>
                    ) : (
                      `${j.failed} 项失败`
                    ),
                },
                { title: "创建时间", dataIndex: "created_at", render: time },
              ]}
              pagination={{
                current: page,
                pageSize: 50,
                total: list.data?.total ?? 0,
                showSizeChanger: false,
                onChange: setPage,
              }}
            />
          </Card>
        </>
      ) : (
        <>
          <ErrorNotice error={job.error} />
          {j && (
            <>
              <Card>
                <Space orientation="vertical" className="full-width">
                  <Descriptions
                    size="small"
                    column={{ xs: 1, sm: 2 }}
                    items={[
                      { key: "type", label: "类型", children: names[j.type] },
                      {
                        key: "state",
                        label: "状态",
                        children: <StateTag state={j.state} />,
                      },
                      {
                        key: "stage",
                        label: "阶段",
                        children: stageNames[j.stage] ?? j.stage,
                      },
                      {
                        key: "date",
                        label: "创建时间",
                        children: time(j.created_at),
                      },
                      {
                        key: "error",
                        label: "诊断",
                        children: j.error_code ? reason(j.error_code) : "无",
                      },
                      {
                        key: "id",
                        label: "任务编号",
                        children: (
                          <Typography.Text copyable>{j.id}</Typography.Text>
                        ),
                      },
                    ]}
                  />
                  <Progress
                    percent={
                      j.total
                        ? Math.min(100, Math.round((j.done / j.total) * 100))
                        : 0
                    }
                    status={j.state === "failed" ? "exception" : undefined}
                  />
                  <span role="status">
                    {j.type === "scan" &&
                    (j.scan_completed || j.scan_partial || j.scan_blocked)
                      ? `已处理 ${j.done} / ${j.total ?? "未知"} 项 · ${j.scan_completed || 0} 项完成 · ${j.scan_partial || 0} 项已入库但部分处理未完成 · ${j.scan_blocked || 0} 项无法检查`
                      : `已处理 ${j.done} / ${j.total ?? "未知"} 项 · ${j.failed} 项失败`}
                  </span>
                  {featureKey && <EnhancementStatus feature={featureKey} />}
                  <Space wrap>
                    {editable &&
                      j.type !== "metadata_search" &&
                      ["queued", "running"].includes(j.state) && (
                        <Button
                          onClick={() => void control("pause")}
                          loading={action.busy}
                        >
                          暂停任务
                        </Button>
                      )}
                    {editable &&
                      j.type !== "metadata_search" &&
                      j.state === "paused" && (
                        <Button
                          type="primary"
                          onClick={() => void control("resume")}
                          disabled={!canExecute}
                          loading={action.busy}
                        >
                          恢复任务
                        </Button>
                      )}
                    {editable &&
                      j.type !== "metadata_search" &&
                      ["queued", "running", "paused"].includes(j.state) && (
                        <Button
                          danger
                          onClick={() => void control("cancel")}
                          loading={action.busy}
                        >
                          取消任务
                        </Button>
                      )}
                    <Button
                      onClick={() => {
                        job.reload();
                        items.reload();
                      }}
                    >
                      刷新状态
                    </Button>
                  </Space>
                </Space>
              </Card>
              <Card title="成员处理结果">
                <Alert
                  type="info"
                  title={
                    j.type === "metadata_search"
                      ? "请回到导入审核的来源比较中恢复或重试查询。"
                      : j.type === "scan"
                        ? "仅重试所选未完成步骤，保留已成功阶段和原任务历史。"
                        : j.mode === "library"
                          ? "全库发现按比较检查点恢复，不支持逐成员重试。终态后可重新查找整个书库。"
                          : "选择失败或待处理成员可创建新任务重试。多候选先确认画廊；已成功结果不会被无意重做。"
                  }
                />
                {editable &&
                  j.mode === "library" &&
                  ["completed", "failed", "cancelled"].includes(j.state) && (
                    <Button
                      onClick={() => void control("retry")}
                      disabled={!canExecute}
                      loading={action.busy}
                    >
                      重新查找整个书库
                    </Button>
                  )}
                <ErrorNotice error={items.error} />
                {editable &&
                  selection.length > 0 &&
                  j.type !== "metadata_search" && (
                    <Button
                      type="primary"
                      className="task-retry"
                      disabled={!canExecute}
                      loading={action.busy}
                      onClick={() => void control("retry")}
                    >
                      重试所选 {selection.length} 项
                    </Button>
                  )}
                <Table<JobItem>
                  size="small"
                  rowKey="ordinal"
                  loading={items.loading}
                  dataSource={items.data?.items ?? []}
                  scroll={{ x: 680 }}
                  rowSelection={
                    !editable ||
                    j.mode === "library" ||
                    j.type === "metadata_search"
                      ? undefined
                      : {
                          selectedRowKeys: selection,
                          preserveSelectedRowKeys: true,
                          getCheckboxProps: (i) => ({
                            disabled:
                              !["completed", "failed", "cancelled"].includes(
                                j.state,
                              ) ||
                              !["pending", "failed", "deferred"].includes(
                                i.state,
                              ),
                          }),
                          onChange: (keys) => setSelection(keys.map(Number)),
                        }
                  }
                  columns={[
                    {
                      title: "对象",
                      render: (_: unknown, i: JobItem) =>
                        i.relative_path || "应用数据",
                    },
                    {
                      title: "状态",
                      dataIndex: "state",
                      render: (state: string, item: JobItem) =>
                        item.scan_result ? (
                          <ScanStatus result={item.scan_result} />
                        ) : (
                          <StateTag state={state} />
                        ),
                    },
                    {
                      title: "错误与确认",
                      render: (_: unknown, i: JobItem) => (
                        <Space orientation="vertical">
                          {i.scan_result ? (
                            <ScanDiagnostics result={i.scan_result} />
                          ) : (
                            i.error_code && (
                              <Typography.Text type="danger">
                                {reason(i.error_code)}
                              </Typography.Text>
                            )
                          )}
                          {i.candidates?.length > 0 && (
                            <Select
                              aria-label="选择画廊候选"
                              className="candidate-select"
                              placeholder="确认一个画廊候选"
                              value={i.book_id ? urls[i.book_id] : undefined}
                              options={i.candidates.map((c) => ({
                                value: c.url,
                                label: c.title || c.url,
                              }))}
                              onOpenChange={setPausedRefresh}
                              onChange={(url) => {
                                if (i.book_id) {
                                  setURLs((v) => ({ ...v, [i.book_id!]: url }));
                                  setSelection((v) =>
                                    v.includes(i.ordinal)
                                      ? v
                                      : [...v, i.ordinal],
                                  );
                                }
                              }}
                            />
                          )}
                        </Space>
                      ),
                    },
                    {
                      title: "操作",
                      render: (_: unknown, i: JobItem) => (
                        <Space orientation="vertical">
                          {j.type === "scan" &&
                            i.book_id &&
                            editable &&
                            (i.error_code?.includes("encoding") ||
                              i.error_code === "parsing_changed" ||
                              i.scan_result?.issues.some(
                                (v) =>
                                  v.code.includes("encoding") ||
                                  v.code === "parsing_changed",
                              )) && (
                              <Button
                                size="small"
                                disabled={action.busy}
                                onClick={() => setEncodingBook(i.book_id!)}
                              >
                                核对页名编码
                              </Button>
                            )}
                          {i.book_id && (
                            <Button
                              size="small"
                              onClick={() =>
                                navigate(
                                  `/admin/books?book=${i.book_id}${i.preview_id ? `&preview=${i.preview_id}` : ""}`,
                                )
                              }
                            >
                              {i.preview_id ? "查看并确认来源预览" : "编辑漫画"}
                            </Button>
                          )}
                        </Space>
                      ),
                    },
                  ]}
                  pagination={{
                    current: itemPage,
                    pageSize: 50,
                    total: items.data?.total ?? 0,
                    showSizeChanger: false,
                    onChange: setItemPage,
                  }}
                />
              </Card>
            </>
          )}
        </>
      )}
    </>
  );
}

function ImportBatchLink({
  id,
  navigate,
}: {
  id: string;
  navigate: (path: string) => void;
}) {
  const link = useRemote<{ batch_id: string }>(`/jobs/${id}/import-batch`);
  return link.data?.batch_id ? (
    <Button
      onClick={() =>
        navigate(
          `/admin/imports?batch=${encodeURIComponent(link.data!.batch_id)}`,
        )
      }
    >
      返回导入审核
    </Button>
  ) : null;
}
