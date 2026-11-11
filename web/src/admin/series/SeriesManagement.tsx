import { usePermissions, EditOnly } from "../../shared/permissions";
import { useRecommendationEnabled } from "../../shared/recommendations";
import { EnhancementStatus } from "../EnhancementStatus";

import { taskURL } from "../../shared/consoleRoutes.mjs";
import { useEffect, useRef, useState } from "react";
import {
  App as AntApp,
  Button,
  Card,
  Drawer,
  Dropdown,
  Input,
  Modal,
  Popover,
  Segmented,
  Select,
  Space,
  Spin,
  Typography,
  Empty,
  Tag,
} from "antd";
import {
  MoreOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { useCurrentLibrary } from "../CurrentLibrary";
import { useLocation, useNavigate } from "react-router";
import { request } from "../../shared/api";
import type { Job, Listing } from "../../shared/api";
import type {
  Command,
  Series,
  WorkCoverSummary,
} from "../../shared/organization";
import { seriesDiscovery } from "../../shared/seriesDiscovery";
import type { SeriesProposal } from "../../shared/seriesDiscovery";
import { useRecommendationCapability } from "../../shared/recommendations";
import { ErrorNotice, query, useAction, useDirty } from "../common";
import {
  Cover,
  Pager,
  useVersionFeedback,
  useVersionRemote as useRemote,
  useWorkspaceFrame,
} from "../versions/versionManagementViews";
import { seriesDirty, seriesDraft } from "./seriesDiscoveryFlow";
import type { SeriesDraft } from "./seriesDiscoveryFlow";
import { SeriesProposalEditor } from "./SeriesProposalEditor";
import { SeriesMembers } from "./SeriesMembers";
import { SeriesPendingPanel } from "./SeriesPending";
import { OperationPreview } from "../OrganizationControls";
import { SeriesDragProvider, SeriesDragHandle } from "./SeriesDrag";
import { nextSeriesReview, seriesJoinOrder } from "./seriesDragFlow";
import type { SeriesDrop } from "./seriesDragFlow";
import {
  EditionCover,
  EditionTitle,
  WorkspaceBookDetails,
} from "../EditionIdentity";
import "../versions/versionManagement.css";
import "./seriesManagement.css";

function SeriesCover({ cover }: { cover?: WorkCoverSummary | null }) {
  return cover ? (
    <Cover
      id={cover.edition_id}
      content={cover.content_revision}
      revision={cover.cover_revision}
      preview={false}
    />
  ) : (
    <div className="series-cover-empty" aria-label="暂无系列封面">
      —
    </div>
  );
}
export function SeriesManagementPage() {
  const location = useLocation(),
    navigate = useNavigate(),
    params = new URLSearchParams(location.search);
  const scope = useCurrentLibrary();
  const library = scope.id;
  const editable = usePermissions().edit(library);
  const tab = editable ? (params.get("tab") ?? "groups") : "groups",
    seriesID = params.get("series") ?? "",
    proposalID = params.get("proposal") ?? "",
    jobID = params.get("job") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1),
    filter = params.get("q") ?? "",
    state = params.get("state") ?? "accepted",
    memberPage = Math.max(1, Number(params.get("member_page")) || 1);
  const [refresh, setRefresh] = useState(0),
    [candidatePage, setCandidatePage] = useState(1),
    [region, setRegion] = useState(
      seriesID || proposalID ? "members" : "groups",
    ),
    [editorBusy, setEditorBusy] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, SeriesDraft>>({}),
    [lastDecision, setLastDecision] = useState<SeriesProposal | null>(null),
    [creating, setCreating] = useState(false),
    [newName, setNewName] = useState(""),
    [command, setCommand] = useState<Command | null>(null);
  const action = useAction(),
    { modal, message } = AntApp.useApp();
  useVersionFeedback(action);
  useDirty(
    Object.values(drafts).some(seriesDirty) ||
      editorBusy ||
      !!command ||
      creating,
  );
  const pageRef = useRef<HTMLDivElement>(null),
    workspaceRef = useRef<HTMLDivElement>(null),
    { mode: screen, height } = useWorkspaceFrame(pageRef, workspaceRef);
  const [dragBusy, setDragBusy] = useState(false);
  const [reviewLocked, setReviewLocked] = useState(() => {
    try {
      return localStorage.getItem("nya.series.reviewLocked") === "true";
    } catch {
      return false;
    }
  });
  function lockReview(value: boolean) {
    setReviewLocked(value);
    try {
      localStorage.setItem("nya.series.reviewLocked", String(value));
    } catch {
      /* Persistence is optional. */
    }
  }
  const {
    capability,
    error: capabilityError,
    canStartFor,
  } = useRecommendationCapability(editable);
  const supported =
    !!capability?.series_discovery && !!capability?.series_proposals;
  const featureEnabled = useRecommendationEnabled("series_recommendations");
  const canStart = editable && canStartFor("series");
  const groups = useRemote<Listing<Series>>(
    `/series?${query({ library_id: library, query: filter, page, page_size: 25, refresh })}`,
    undefined,
    "GET",
    tab === "groups",
  );
  const proposals = useRemote<Listing<SeriesProposal>>(
    `/series-proposals?${query({ library_id: library, job_id: jobID, kind: tab === "new" ? "create_series" : (params.get("kind") ?? ""), state: tab === "history" ? state : "pending", page, page_size: 25, refresh })}`,
    undefined,
    "GET",
    tab !== "groups" && supported,
  );
  const selected = useRemote<Series>(
    `/series/${encodeURIComponent(seriesID)}?summary=false&refresh=${refresh}`,
    undefined,
    "GET",
    !!seriesID,
  );
  const detail = useRemote<SeriesProposal>(
    `/series-proposals/${encodeURIComponent(proposalID)}?refresh=${refresh}`,
    undefined,
    "GET",
    !!proposalID && supported,
  );
  const candidates = useRemote<Listing<SeriesProposal>>(
    `/series-proposals?${query({ library_id: library, target_series_id: seriesID, kind: "add_to_series", state: "pending", alternatives: true, page: candidatePage, page_size: 10, refresh })}`,
    undefined,
    "GET",
    !!selected.data && supported,
  );
  const jobs = useRemote<Listing<Job>>(
    `/jobs?${query({ type: "series_recommendation", library_id: library, page: 1, page_size: 20, refresh })}`,
    undefined,
    "GET",
    !!library && supported,
  );
  const job = useRemote<Job>(
    `/jobs/${encodeURIComponent(jobID)}`,
    undefined,
    "GET",
    !!jobID && !!library && supported,
  );
  const currentJob = jobID ? (job.data ?? undefined) : jobs.data?.items[0];
  const task =
      currentJob?.type === "series_recommendation" &&
      currentJob.library_id === library
        ? currentJob
        : undefined,
    active = task && ["queued", "running", "paused"].includes(task.state);
  const reloadJobs = jobs.reload,
    reloadJob = job.reload;
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => {
      reloadJobs();
      if (jobID) reloadJob();
    }, 1500);
    return () => clearInterval(timer);
  }, [active, jobID, reloadJobs, reloadJob]);
  const loadedProposal = detail.data;
  if (loadedProposal && !drafts[loadedProposal.id]) {
    setDrafts((all) => ({
      ...all,
      [loadedProposal.id]: seriesDraft(loadedProposal),
    }));
  }
  function url(values: Record<string, string | number | null>) {
    const next = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(values)) {
      if (v === null || v === "") next.delete(k);
      else next.set(k, String(v));
    }
    void navigate(`/admin/series?${next}`, { preventScrollReset: true });
  }
  function reload() {
    setRefresh((r) => r + 1);
  }
  function choose(p: SeriesProposal) {
    url({
      proposal: p.id,
      series: p.kind === "add_to_series" ? p.target_series_id : null,
    });
    setRegion("members");
  }
  async function done(p: SeriesProposal, source?: SeriesProposal) {
    setLastDecision(p);
    const origin = source?.id ?? (p.id === proposalID ? proposalID : "");
    if (origin)
      setDrafts((all) => {
        const next = { ...all };
        delete next[origin];
        return next;
      });
    void message.success(
      p.state === "accepted"
        ? "系列整理已完成"
        : p.state === "dismissed"
          ? "方案已忽略"
          : p.state === "undone"
            ? "已撤销整理"
            : "方案已恢复",
    );
    if (
      reviewLocked &&
      tab === "new" &&
      source?.kind === "create_series" &&
      ["accepted", "dismissed"].includes(p.state)
    ) {
      try {
        const oldIndex = Math.max(
          0,
          proposals.data?.items.findIndex((v) => v.id === source.id) ?? 0,
        );
        const path = (nextPage: number) =>
          `/series-proposals?${query({ library_id: library, job_id: jobID, kind: "create_series", state: "pending", page: nextPage, page_size: 25 })}`;
        let result = await request<Listing<SeriesProposal>>(path(page)),
          nextPage = page;
        if (!result.items.length && result.total > 0) {
          nextPage = Math.max(1, Math.ceil(result.total / 25));
          result = await request<Listing<SeriesProposal>>(path(nextPage));
        }
        const next = nextSeriesReview(result.items, oldIndex);
        url({
          tab: "new",
          page: nextPage,
          proposal: next?.id ?? null,
          series: null,
        });
        setRegion(next ? "members" : "groups");
        if (!next) void message.info("推荐新系列已处理完");
      } catch (error) {
        action.setError(error);
        url({ tab: "new", proposal: null, series: null });
      }
    } else if (p.state === "accepted") {
      url({ series: p.target_series_id, proposal: null, tab: "groups" });
      setRegion("members");
    } else if (source || p.id === proposalID) url({ proposal: p.id });
    reload();
  }
  async function join(p: SeriesProposal, drop?: SeriesDrop) {
    if (!featureEnabled)
      throw Error("系列推荐未启用，请联系管理员核对功能配置。");
    await action.run(async () => {
      if (drop && drop.scope !== `series:${p.target_series_id}`)
        throw Error("只能拖入当前推荐对应的系列");
      const order = seriesJoinOrder(p, drop);
      await done(
        await seriesDiscovery.decide(
          p,
          "accept",
          order.join() === p.order.join() ? undefined : order,
        ),
      );
    }, "");
  }
  const selectedProposal = detail.data,
    draft = selectedProposal
      ? (drafts[selectedProposal.id] ?? seriesDraft(selectedProposal))
      : null;
  const showCandidates = !!selected.data && !proposalID;
  const pendingPanel = params.get("panel") === "pending";
  const dirtyEntries = Object.entries(drafts).filter(([, d]) => seriesDirty(d));
  const candidateContent = (
    <>
      <Typography.Text strong>推荐加入</Typography.Text>
      <ErrorNotice error={candidates.error} retry={candidates.reload} />
      <Spin spinning={candidates.loading}>
        {candidates.data?.items.map((p) => (
          <Card
            key={p.id}
            size="small"
            className="version-candidate series-candidate"
            data-testid={`series-candidate-${p.id}`}
          >
            <div className="series-candidate-heading">
              <Typography.Text strong>
                {p.new_work_ids.length} 个推荐作品{p.primary ? "" : " · 备选"}
              </Typography.Text>
              <SeriesDragHandle
                disabled={action.busy || editorBusy}
                item={{
                  kind: "recommendation",
                  id: p.id,
                  scope: `series:${p.target_series_id}`,
                  label: `${p.new_work_ids.length} 个推荐作品`,
                  order: [...p.order],
                  proposal: p,
                }}
              />
            </div>
            <div className="series-candidate-previews">
              {p.new_member_previews?.map((m) => (
                <div className="series-candidate-preview" key={m.work_id}>
                  <EditionCover
                    disabled={action.busy || editorBusy || dragBusy}
                    member={{
                      id: m.preferred_edition_id,
                      content_revision: m.content_revision,
                      cover_revision: m.cover_revision,
                    }}
                  />
                  <div className="version-member-text">
                    <EditionTitle member={m} />
                  </div>
                </div>
              ))}
            </div>
            {p.new_work_ids.length > (p.new_member_previews?.length ?? 0) && (
              <Typography.Text type="secondary">
                另{" "}
                {p.new_work_ids.length - (p.new_member_previews?.length ?? 0)}{" "}
                个作品
              </Typography.Text>
            )}
            <Space wrap>
              <Button
                size="small"
                type="primary"
                disabled={
                  !featureEnabled ||
                  action.busy ||
                  editorBusy ||
                  dragBusy ||
                  p.name_required
                }
                onClick={() => void join(p)}
              >
                加入系列
              </Button>
              <Button
                size="small"
                disabled={action.busy || editorBusy || dragBusy}
                onClick={() =>
                  void action.run(
                    async () =>
                      done(await seriesDiscovery.decide(p, "dismiss")),
                    "",
                  )
                }
              >
                忽略方案
              </Button>
            </Space>
          </Card>
        ))}
      </Spin>
      <Pager data={candidates.data} onChange={setCandidatePage} />
    </>
  );
  return (
    <SeriesDragProvider onActivity={setDragBusy}>
      <div
        ref={pageRef}
        className="version-management series-management"
        data-layout={screen}
      >
        <h1 className="sr-only">系列管理</h1>
        <div className="version-toolbar">
          <Space wrap>
            <EditOnly library={library}>
              <Button
                type="primary"
                icon={<SearchOutlined aria-hidden="true" />}
                loading={action.busy}
                disabled={
                  !library ||
                  !!scope.library?.disabled ||
                  !canStart ||
                  !supported ||
                  !!active ||
                  editorBusy
                }
                onClick={() =>
                  void action.run(async () => {
                    const j = await seriesDiscovery.start(library);
                    url({ job: j.job_id });
                    reloadJobs();
                  }, "系列查找已启动")
                }
              >
                查找系列
              </Button>
            </EditOnly>
            <EnhancementStatus feature="series_recommendations" resources />
            <Button
              icon={<ReloadOutlined aria-hidden="true" />}
              disabled={editorBusy || dragBusy}
              onClick={reload}
            >
              刷新
            </Button>
            {task && (
              <Popover
                trigger="click"
                title="系列查找任务"
                content={
                  <div className="version-task-details">
                    <p>
                      {task.stage} · {task.done}/{task.total ?? "—"}
                    </p>
                    <Button onClick={() => void navigate(taskURL(task.id))}>
                      任务详情
                    </Button>
                    {active && (
                      <Space>
                        {["queued", "running", "paused"].includes(
                          task.state,
                        ) && (
                          <Button
                            onClick={() =>
                              void action.run(async () => {
                                await request(
                                  `/jobs/${task.id}/${task.state === "paused" ? "resume" : "pause"}`,
                                  "POST",
                                );
                                reloadJobs();
                                reloadJob();
                              }, "任务状态已更新")
                            }
                          >
                            {task.state === "paused" ? "恢复查找" : "暂停查找"}
                          </Button>
                        )}
                        <Button
                          onClick={() =>
                            modal.confirm({
                              title: "取消本次系列查找？",
                              content: "已有系列与作品关系保留。",
                              okText: "取消查找",
                              cancelText: "继续查找",
                              onOk: () =>
                                action.run(async () => {
                                  await request(
                                    `/jobs/${task.id}/cancel`,
                                    "POST",
                                  );
                                  reloadJobs();
                                  reloadJob();
                                }, "查找已取消"),
                            })
                          }
                        >
                          取消查找
                        </Button>
                      </Space>
                    )}
                  </div>
                }
              >
                <Button aria-label="系列查找任务状态">
                  查找 ·{" "}
                  {task.state === "completed"
                    ? "已完成"
                    : task.state === "paused"
                      ? "已暂停"
                      : active
                        ? "进行中"
                        : task.state === "failed"
                          ? "失败"
                          : "已取消"}
                </Button>
              </Popover>
            )}
            {lastDecision?.can_undo && (
              <Button
                onClick={() =>
                  void action.run(
                    async () =>
                      done(await seriesDiscovery.decide(lastDecision, "undo")),
                    "",
                  )
                }
              >
                撤销
              </Button>
            )}
            {lastDecision?.state === "dismissed" && (
              <Button
                onClick={() =>
                  void action.run(
                    async () =>
                      done(
                        await seriesDiscovery.decide(lastDecision, "restore"),
                      ),
                    "",
                  )
                }
              >
                恢复方案
              </Button>
            )}
            <EditOnly library={library}>
              <Dropdown
                menu={{
                  items: [
                    { key: "create", label: "创建空系列" },
                    {
                      key: "pending",
                      label: "待核对与备选",
                      disabled:
                        !library || !!scope.library?.disabled || !supported,
                    },
                    { key: "history", label: "整理历史" },
                    ...dirtyEntries.map(([id, d]) => ({
                      key: `draft:${id}`,
                      label: `继续草稿：${d.name || "未命名系列"}`,
                    })),
                  ],
                  onClick: ({ key }) => {
                    if (key === "create") setCreating(true);
                    else if (key === "pending") url({ panel: "pending" });
                    else if (key.startsWith("draft:")) {
                      url({ proposal: key.slice(6) });
                      setRegion("members");
                    } else url({ tab: "history", state: "accepted", page: 1 });
                  },
                }}
              >
                <Button aria-label="系列管理更多操作" icon={<MoreOutlined />} />
              </Dropdown>
            </EditOnly>
          </Space>
        </div>
        <ErrorNotice
          error={action.error || jobs.error || job.error || capabilityError}
        />
        <div className="version-region-switch">
          <Segmented
            block
            value={region}
            onChange={setRegion}
            options={[
              { label: "系列", value: "groups" },
              { label: "成员与预览", value: "members" },
            ]}
          />
        </div>
        <div
          ref={workspaceRef}
          className={`version-layout version-region-${region} ${showCandidates ? "has-candidates" : ""}`}
          style={screen !== "mobile" || !!proposalID ? { height } : undefined}
        >
          <section
            className="version-column version-left"
            aria-label="系列列表"
          >
            <Segmented
              block
              value={tab}
              disabled={editorBusy || dragBusy}
              onChange={(v) => url({ tab: v, page: 1, proposal: null })}
              options={[
                { label: "已有系列", value: "groups" },
                ...(editable ? [{ label: "推荐新系列", value: "new" }] : []),
                ...(tab === "history"
                  ? [{ label: "历史", value: "history" }]
                  : []),
              ]}
            />
            {tab === "groups" && (
              <Input.Search
                aria-label="搜索系列"
                placeholder="搜索系列名称"
                defaultValue={filter}
                onSearch={(q) => url({ q, page: 1 })}
              />
            )}
            {tab === "history" && (
              <Select
                aria-label="系列方案状态"
                value={state}
                options={[
                  { value: "accepted", label: "已采纳／可撤销" },
                  { value: "dismissed", label: "已忽略" },
                  { value: "undone", label: "已撤销" },
                  { value: "pending", label: "待处理" },
                  { value: "superseded", label: "已过期" },
                ]}
                onChange={(v) => url({ state: v, page: 1 })}
              />
            )}
            <ErrorNotice
              error={tab === "groups" ? groups.error : proposals.error}
              retry={tab === "groups" ? groups.reload : proposals.reload}
            />
            <div className="version-scroll">
              <Spin
                spinning={tab === "groups" ? groups.loading : proposals.loading}
              >
                <div className="version-group-list">
                  {tab === "groups"
                    ? groups.data?.items.map((s) => (
                        <div key={s.id}>
                          <button
                            type="button"
                            disabled={editorBusy || dragBusy}
                            className={`version-group-row ${s.id === seriesID && !proposalID ? "selected" : ""}`}
                            aria-pressed={s.id === seriesID && !proposalID}
                            onClick={() => {
                              url({
                                series: s.id,
                                proposal: null,
                                member_page: 1,
                              });
                              setRegion("members");
                              setCandidatePage(1);
                            }}
                          >
                            <SeriesCover cover={s.cover} />
                            <span>
                              <strong className="version-row-title">
                                {s.name}
                              </strong>
                              <Tag>{s.member_count} 个作品</Tag>
                            </span>
                          </button>
                        </div>
                      ))
                    : proposals.data?.items.map((p) => (
                        <div key={p.id}>
                          <button
                            type="button"
                            disabled={editorBusy || dragBusy}
                            className={`version-group-row ${p.id === proposalID ? "selected" : ""}`}
                            aria-pressed={p.id === proposalID}
                            onClick={() => choose(p)}
                          >
                            <SeriesCover cover={p.cover} />
                            <span>
                              <strong className="version-row-title">
                                {p.name || "未命名系列"}
                              </strong>
                              <div className="version-facts">
                                <Tag>{p.member_count} 个作品</Tag>
                                <Tag>
                                  {p.kind === "create_series"
                                    ? "组成新系列"
                                    : "加入系列"}
                                </Tag>
                              </div>
                            </span>
                          </button>
                        </div>
                      ))}
                </div>
                {(tab === "groups" ? groups.data : proposals.data)?.total ===
                  0 && (
                  <p className="version-empty">
                    {tab === "groups"
                      ? "暂无系列，可查找推荐或创建空系列"
                      : "暂无此类系列方案"}
                  </p>
                )}
              </Spin>
            </div>
            <Pager
              data={tab === "groups" ? groups.data : proposals.data}
              onChange={(page) => url({ page })}
            />
          </section>
          <section
            className="version-column version-center"
            aria-label="系列成员与预览"
          >
            <div className="version-center-body">
              <ErrorNotice
                error={detail.error || selected.error}
                retry={reload}
              />
              {proposalID ? (
                selectedProposal && draft ? (
                  <SeriesProposalEditor
                    key={proposalID}
                    incoming={selectedProposal}
                    draft={draft}
                    onDraft={(d) =>
                      setDrafts((all) => ({ ...all, [proposalID]: d }))
                    }
                    onDone={(p) => done(p, draft.baseline)}
                    onReload={(p) => {
                      setDrafts((all) => ({
                        ...all,
                        [proposalID]: seriesDraft(p),
                      }));
                      detail.setData(p);
                    }}
                    onBusy={setEditorBusy}
                    reviewLocked={reviewLocked}
                    onReviewLocked={lockReview}
                  />
                ) : (
                  <Spin />
                )
              ) : selected.data ? (
                <SeriesMembers
                  key={seriesID}
                  series={selected.data}
                  page={memberPage}
                  refresh={refresh}
                  onPage={(p) => url({ member_page: p })}
                  onChanged={(r) => {
                    if (r?.command.action === "series_delete")
                      url({ series: null, member_page: 1 });
                    reload();
                  }}
                  onJoin={join}
                  disabled={editorBusy || dragBusy}
                />
              ) : (
                <div className="version-selection-empty">
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={
                      <>
                        <Typography.Title level={4}>
                          选择一个系列
                        </Typography.Title>
                        <Typography.Text type="secondary">
                          从左侧选择系列或推荐方案，查看作品
                        </Typography.Text>
                      </>
                    }
                  />
                </div>
              )}
              {showCandidates &&
                (!!candidates.data?.items.length || !!candidates.error) && (
                  <section
                    className="version-candidates-below"
                    aria-label="加入候选"
                  >
                    {candidateContent}
                  </section>
                )}
            </div>
          </section>
        </div>
        <Drawer
          title="待核对与备选"
          open={pendingPanel}
          onClose={() => url({ panel: null })}
          size={680}
        >
          {pendingPanel && (
            <SeriesPendingPanel
              library={library}
              job={task}
              refresh={refresh}
              onJob={(id) => url({ job: id })}
              onChoose={(id) => {
                url({ proposal: id, panel: null });
                setRegion("members");
              }}
            />
          )}
        </Drawer>
        <Modal
          title="创建空系列"
          open={creating}
          okText="查看创建影响"
          cancelText="取消"
          okButtonProps={{ disabled: !newName.trim() || !library }}
          onCancel={() => {
            setCreating(false);
            setNewName("");
          }}
          onOk={() =>
            setCommand({
              action: "series_create",
              library_id: library,
              name: newName,
              work_ids: [],
              expected_revisions: {},
            })
          }
        >
          <Input
            aria-label="新系列名称"
            maxLength={200}
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
          />
          {!library && <p>请先在工具栏选择书库。</p>}
        </Modal>
        {command && (
          <OperationPreview
            command={command}
            onCancel={() => setCommand(null)}
            onDone={(r) => {
              setCommand(null);
              setCreating(false);
              setNewName("");
              url({ series: r.id, proposal: null, tab: "groups" });
              setRegion("members");
              reload();
            }}
          />
        )}
        <WorkspaceBookDetails onChanged={reload} />
      </div>
    </SeriesDragProvider>
  );
}
