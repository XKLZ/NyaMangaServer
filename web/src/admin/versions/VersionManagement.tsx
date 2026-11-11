import { usePermissions, EditOnly } from "../../shared/permissions";
import { useRecommendationEnabled } from "../../shared/recommendations";
import { EnhancementStatus } from "../EnhancementStatus";

import { useEffect, useRef, useState } from "react";
import {
  App as AntApp,
  Button,
  Card,
  Dropdown,
  Empty,
  Input,
  Popover,
  Segmented,
  Select,
  Space,
  Spin,
  Tag,
  Typography,
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
import { organization } from "../../shared/organization";
import type { Work } from "../../shared/organization";
import { discovery } from "../../shared/workDiscovery";
import { discoveryFailure } from "../../shared/discoveryStatus";
import type { ProposalPage, WorkProposal } from "../../shared/workDiscovery";
import { useRecommendationCapability } from "../../shared/recommendations";
import {
  ErrorNotice,
  query,
  useAction,
  useRemote as useSource,
} from "../common";
import { ProposalEditor } from "./VersionProposal";
import { GroupMembers } from "./VersionGroup";
import {
  Cover,
  Pager,
  useVersionFeedback,
  useWorkspaceFrame,
  useVersionRemote as useRemote,
} from "./versionManagementViews";
import { cleanBookTitle, originalTitle } from "../books/bookTitle";
import { EditionCover, WorkspaceBookDetails } from "../EditionIdentity";
import "./versionManagement.css";
type Run = {
  phase: string;
  next_row: number;
  baseline_state?: string;
  failure_stage?: string;
  failure_reason?: string;
  failure_row?: number;
  resources?: {
    cache_hits: number;
    computed_inputs: number;
    model_loads: number;
  };
  reused_pairs: number;
  computed_pairs: number;
  reused_features: number;
  prepared_features: number;
};
export function VersionManagementPage() {
  const location = useLocation(),
    navigate = useNavigate();
  const params = new URLSearchParams(location.search);
  const scope = useCurrentLibrary();
  const library = scope.id;
  const editable = usePermissions().edit(library);
  const tab = editable ? (params.get("tab") ?? "groups") : "groups",
    work = params.get("work") ?? "",
    proposalID = params.get("proposal") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1),
    filter = params.get("q") ?? "",
    historyState = params.get("state") ?? "dismissed";
  const [refresh, setRefresh] = useState(0),
    [candidatePage, setCandidatePage] = useState(1),
    [region, setRegion] = useState(work || proposalID ? "members" : "groups");
  const pageRef = useRef<HTMLDivElement>(null);
  const workspaceRef = useRef<HTMLDivElement>(null);
  const { mode: screen, height: workspaceHeight } = useWorkspaceFrame(
    pageRef,
    workspaceRef,
  );
  const [loadedGroup, setLoadedGroup] = useState<string>("");
  const [lastDecision, setLastDecision] = useState<WorkProposal | null>(null);
  const action = useAction();
  const { message } = AntApp.useApp();
  useVersionFeedback(action);
  const { capability, canStartFor } = useRecommendationCapability(editable);
  const featureEnabled = useRecommendationEnabled("work_recommendations");
  const canStart = editable && canStartFor("work");
  function url(change: Record<string, string | number | null>) {
    const next = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(change)) {
      if (v === null || v === "") next.delete(k);
      else next.set(k, String(v));
    }
    void navigate(`/admin/versions?${next}`);
  }
  const groups = useRemote<Listing<Work>>(
    `/works?${query({ library_id: library, min_editions: 2, query: filter, page, page_size: 25, refresh })}`,
    undefined,
    "GET",
    tab === "groups",
  );
  const proposals = useRemote<ProposalPage>(
    `/work-proposals?${query({ library_id: library, kind: tab === "new" ? "create_work" : "", state: tab === "history" ? historyState : "pending", page, page_size: 25, refresh })}`,
    undefined,
    "GET",
    tab !== "groups" && !!capability?.discovery,
  );
  const detail = useSource<WorkProposal>(
    `/work-proposals/${encodeURIComponent(proposalID)}?refresh=${refresh}`,
    undefined,
    "GET",
    !!proposalID && !!capability?.discovery,
  );
  const candidates = useRemote<ProposalPage>(
    `/work-proposals?${query({ library_id: library, target_work_id: work, kind: "add_to_work", alternatives: true, state: "pending", page: candidatePage, page_size: 10, refresh })}`,
    undefined,
    "GET",
    !!work && !!capability?.discovery,
  );
  const jobs = useRemote<Listing<Job>>(
    `/jobs?${query({ type: "work_recommendation", library_id: library, page_size: 10, refresh })}`,
    undefined,
    "GET",
    !!library && !!capability?.discovery,
  );
  const latest = jobs.data?.items.find((j) => j.mode === "library");
  const active =
    latest && ["queued", "running", "paused"].includes(latest.state);
  const progress = useRemote<Run>(
    `/work-discovery-jobs/${latest?.id}`,
    undefined,
    "GET",
    !!latest && !!capability?.discovery,
  );
  const reloadJobs = jobs.reload,
    reloadProgress = progress.reload;
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => {
      reloadJobs();
      reloadProgress();
    }, 1500);
    return () => clearInterval(timer);
  }, [active, reloadJobs, reloadProgress]);
  function reload() {
    setRefresh((r) => r + 1);
  }
  async function start(full = false) {
    await action.run(async () => {
      if (!library) throw new Error("请先选择书库");
      await discovery.start(library, full);
      jobs.reload();
      progress.reload();
    }, "版本查找已启动，整理结果会保留到历史中");
  }
  function decided(p: WorkProposal) {
    setLastDecision(p);
    void message.success(
      p.state === "accepted"
        ? "已完成整理"
        : p.state === "dismissed"
          ? "已忽略方案"
          : p.state === "undone"
            ? "已撤销整理"
            : "已恢复建议",
    );
    reload();
    if (p.state === "accepted") {
      url({ work: p.target_work_id, proposal: null, tab: "groups" });
      setRegion("members");
    } else if (p.id === proposalID) url({ proposal: null });
  }
  async function batchLabels() {
    await action.run(
      async () => {
        const items = groups.data?.items ?? [];
        for (const g of items) {
          const context = await organization.context([], [g.id]);
          await request(`/works/${g.id}/version-labels`, "POST", {
            expected_revisions: context.expected_revisions,
          });
        }
        reload();
      },
      `已为本页 ${groups.data?.items.length ?? 0} 个版本组生成标签，人工值保持不变`,
    );
  }
  const selectedProposal = detail.data?.id === proposalID ? detail.data : null;
  const selectedWork = groups.data?.items.find((g) => g.id === work);
  const showCandidates =
    !!work &&
    (proposalID
      ? selectedProposal?.kind === "add_to_work"
      : loadedGroup === work || !!selectedWork);
  const candidateContent = (
    <>
      <Typography.Text strong>推荐加入</Typography.Text>
      {!work ? (
        <p className="version-empty">先选择一个已有版本组</p>
      ) : (
        <>
          <ErrorNotice error={candidates.error} retry={candidates.reload} />
          <div className="version-scroll">
            <Spin spinning={candidates.loading}>
              {candidates.data?.items.map((p) => (
                <Card size="small" className="version-candidate" key={p.id}>
                  <Typography.Text strong>
                    {p.members
                      .filter((m) => m.role === "new")
                      .map((m) => cleanBookTitle(originalTitle(m)))
                      .join("、")}
                  </Typography.Text>
                  <div className="version-candidate-covers">
                    {p.members
                      .filter((m) => m.role === "new")
                      .slice(0, 4)
                      .map((m) => (
                        <EditionCover key={m.id} member={m} />
                      ))}
                  </div>
                  <Tag color={p.tier === "strong" ? "green" : "gold"}>
                    {p.tier === "strong" ? "较强建议" : "需核对"}
                  </Tag>
                  {p.effects?.some(
                    (e) => e.from_series_id || e.to_series_id,
                  ) && <p>加入后继承目标版本组的系列归属。</p>}
                  {p.risks?.length ? (
                    <p>存在需核对的版本差异，请先预览。</p>
                  ) : null}
                  <Space wrap>
                    <Button
                      size="small"
                      onClick={() => {
                        url({ proposal: p.id });
                        setRegion("members");
                      }}
                    >
                      预览加入
                    </Button>
                    <Button
                      size="small"
                      type="primary"
                      disabled={!featureEnabled || p.blocked || action.busy}
                      onClick={() =>
                        void action.run(
                          async () =>
                            decided(await discovery.decide(p, "accept")),
                          "",
                        )
                      }
                    >
                      加入
                    </Button>
                    <Button
                      size="small"
                      disabled={action.busy}
                      onClick={() =>
                        void action.run(
                          async () =>
                            decided(await discovery.decide(p, "dismiss")),
                          "",
                        )
                      }
                    >
                      忽略
                    </Button>
                  </Space>
                </Card>
              ))}
              {!candidates.loading && candidates.data?.total === 0 && (
                <p className="version-empty">暂无有效加入建议</p>
              )}
            </Spin>
          </div>
          <Pager data={candidates.data} onChange={setCandidatePage} />
        </>
      )}
    </>
  );
  return (
    <div ref={pageRef} className="version-management" data-layout={screen}>
      <h1 className="sr-only">版本管理</h1>
      <div className="version-toolbar">
        <Space wrap>
          <EditOnly library={library}>
            <Button
              type="primary"
              icon={<SearchOutlined aria-hidden="true" />}
              disabled={
                !library || !!scope.library?.disabled || !!active || !canStart
              }
              loading={action.busy}
              onClick={() => void start()}
            >
              查找版本
            </Button>
          </EditOnly>
          <EnhancementStatus feature="work_recommendations" resources />
          {library && latest && (
            <Popover
              trigger="click"
              title="版本查找任务"
              content={
                <div className="version-task-details">
                  <>
                    <p>
                      {progress.data?.phase === "features"
                        ? "检查资料与特征缓存"
                        : progress.data?.phase === "pairs"
                          ? "查找相似版本"
                          : "生成方案"}{" "}
                      · {latest.done} / {latest.total}
                    </p>
                    {progress.data && (
                      <p>
                        复用资料登记 {progress.data.reused_features}
                        ，检查／准备资料 {progress.data.prepared_features}
                        ；复用配对 {progress.data.reused_pairs}，新增配对{" "}
                        {progress.data.computed_pairs}。
                        {progress.data.resources && (
                          <>
                            {" "}
                            特征缓存命中 {
                              progress.data.resources.cache_hits
                            }{" "}
                            次，实际推理{" "}
                            {progress.data.resources.computed_inputs}{" "}
                            次，模型加载 {progress.data.resources.model_loads}{" "}
                            次。
                          </>
                        )}
                      </p>
                    )}
                    {progress.data?.baseline_state ===
                      "no_compatible_baseline" && (
                      <p>
                        没有可验证的配对基线，本次补建检查点；已有模型特征仍会复用。
                      </p>
                    )}
                    {progress.data?.baseline_state === "reusing_checkpoint" && (
                      <p>
                        已找到兼容检查点，按输入校验后复用，只重算无法复用的配对。
                      </p>
                    )}
                    {progress.data?.baseline_state ===
                      "full_recompute_requested" && (
                      <p>本次已明确选择完整重算配对，模型特征仍可复用。</p>
                    )}
                    {(latest.state === "failed" || latest.state === "paused") &&
                      latest.error_code && (
                        <p role="status">
                          {discoveryFailure(
                            progress.data?.failure_reason || latest.error_code,
                          )}
                          {progress.data?.failure_stage && (
                            <>
                              {" "}
                              阶段：
                              {progress.data.failure_stage === "pairs"
                                ? "查找相似版本"
                                : progress.data.failure_stage === "features"
                                  ? "检查资料"
                                  : "生成与保存方案"}
                              ，已提交配对行 {progress.data.next_row}。
                            </>
                          )}
                        </p>
                      )}
                    <ErrorNotice error={progress.error} />
                    {active && (
                      <Space>
                        {(latest.state === "running" ||
                          latest.state === "paused") && (
                          <Button
                            onClick={() =>
                              void action.run(async () => {
                                await request(
                                  `/jobs/${latest.id}/${latest.state === "paused" ? "resume" : "pause"}`,
                                  "POST",
                                );
                                jobs.reload();
                              }, "任务状态已更新")
                            }
                          >
                            {latest.state === "paused" ? "恢复" : "暂停"}
                          </Button>
                        )}
                        <Button
                          onClick={() =>
                            void action.run(async () => {
                              await request(
                                `/jobs/${latest.id}/cancel`,
                                "POST",
                              );
                              jobs.reload();
                            }, "已取消查找")
                          }
                        >
                          取消查找
                        </Button>
                      </Space>
                    )}
                  </>
                </div>
              }
            >
              <Button aria-label="版本查找任务状态">
                查找 ·{" "}
                {active
                  ? latest.state === "paused"
                    ? "已暂停"
                    : "进行中"
                  : latest.state === "completed"
                    ? "已完成"
                    : latest.state === "failed"
                      ? "失败"
                      : "已取消"}
              </Button>
            </Popover>
          )}
          {lastDecision?.can_undo && (
            <Button
              onClick={() =>
                void action.run(async () => {
                  const p = await discovery.decide(lastDecision, "undo");
                  setLastDecision(p);
                  reload();
                }, "已撤销，成员资料不变")
              }
            >
              撤销
            </Button>
          )}
          {lastDecision?.state === "dismissed" && (
            <Button
              onClick={() =>
                void action.run(async () => {
                  await discovery.decide(lastDecision, "restore");
                  setLastDecision(null);
                  reload();
                }, "已恢复建议")
              }
            >
              恢复
            </Button>
          )}
          <EditOnly library={library}>
            <Dropdown
              menu={{
                items: [
                  {
                    key: "full",
                    label: "完整重新计算",
                    disabled:
                      !library ||
                      !!scope.library?.disabled ||
                      !!active ||
                      !canStart,
                  },
                  {
                    key: "batch",
                    label: "为本页版本组生成智能标签",
                    disabled: tab !== "groups" || !groups.data?.items.length,
                  },
                  { key: "history", label: "整理历史与已忽略方案" },
                ],
                onClick: ({ key }) =>
                  key === "full"
                    ? void start(true)
                    : key === "batch"
                      ? void batchLabels()
                      : url({ tab: "history", page: 1, proposal: null }),
              }}
            >
              <Button icon={<MoreOutlined />} aria-label="版本管理更多操作" />
            </Dropdown>
          </EditOnly>
          <Button icon={<ReloadOutlined aria-hidden="true" />} onClick={reload}>
            刷新
          </Button>
        </Space>
      </div>
      <ErrorNotice error={action.error} />
      <ErrorNotice error={library ? jobs.error : null} />
      <div className="version-region-switch">
        <Segmented
          block
          value={region}
          onChange={setRegion}
          options={[
            { label: "版本组", value: "groups" },
            { label: "成员与预览", value: "members" },
          ]}
        />
      </div>
      <div
        ref={workspaceRef}
        className={`version-layout version-region-${region} ${showCandidates ? "has-candidates" : ""}`}
        style={screen !== "mobile" ? { height: workspaceHeight } : undefined}
      >
        <section
          className="version-column version-left"
          aria-label="版本组列表"
        >
          <Segmented
            block
            value={tab}
            options={[
              { label: "已有组", value: "groups" },
              ...(editable ? [{ label: "推荐新组", value: "new" }] : []),
              ...(tab === "history"
                ? [{ label: "历史", value: "history" }]
                : []),
            ]}
            onChange={(v) => url({ tab: v, page: 1, proposal: null })}
          />
          {tab === "groups" ? (
            <>
              <Input.Search
                placeholder="搜索版本组"
                aria-label="搜索版本组"
                defaultValue={filter}
                onSearch={(v) => url({ q: v, page: 1 })}
              />
              <ErrorNotice error={groups.error} retry={groups.reload} />
              <div className="version-scroll">
                <Spin spinning={groups.loading}>
                  <div className="version-group-list">
                    {groups.data?.items.map((g) => (
                      <div key={g.id}>
                        <button
                          type="button"
                          className={`version-group-row ${g.id === work ? "selected" : ""}`}
                          aria-pressed={g.id === work}
                          onClick={() => {
                            url({ work: g.id, proposal: null });
                            setCandidatePage(1);
                            setRegion("members");
                          }}
                        >
                          <Cover
                            preview={false}
                            id={g.preferred_edition_id}
                            content={g.content_revision}
                            revision={g.cover_revision}
                          />
                          <span>
                            <strong className="version-row-title">
                              {cleanBookTitle(originalTitle(g))}
                            </strong>
                            <Tag>{g.edition_count} 个版本</Tag>
                          </span>
                        </button>
                      </div>
                    ))}
                  </div>
                  {groups.data?.total === 0 && (
                    <p className="version-empty">
                      暂无多版本作品，可查找推荐新组
                    </p>
                  )}
                </Spin>
              </div>
              <Pager data={groups.data} onChange={(p) => url({ page: p })} />
            </>
          ) : (
            <>
              {tab === "history" && (
                <Select
                  aria-label="整理历史状态"
                  value={historyState}
                  onChange={(v) => url({ state: v, page: 1 })}
                  options={[
                    { value: "dismissed", label: "已忽略" },
                    { value: "accepted", label: "已采纳" },
                    { value: "undone", label: "已撤销" },
                  ]}
                />
              )}
              <ErrorNotice error={proposals.error} retry={proposals.reload} />
              <div className="version-scroll">
                <Spin spinning={proposals.loading}>
                  <div className="version-group-list">
                    {proposals.data?.items.map((p) => (
                      <div key={p.id}>
                        <button
                          type="button"
                          className={`version-group-row ${p.id === proposalID ? "selected" : ""}`}
                          aria-pressed={p.id === proposalID}
                          onClick={() => {
                            url({
                              proposal: p.id,
                              work:
                                p.kind === "add_to_work"
                                  ? p.target_work_id
                                  : null,
                            });
                            setRegion("members");
                          }}
                        >
                          <Cover
                            preview={false}
                            id={p.members[0].id}
                            content={p.members[0].content_revision}
                            revision={p.members[0].cover_revision}
                          />
                          <span>
                            <strong className="version-row-title">
                              {cleanBookTitle(
                                originalTitle(
                                  p.members.find(
                                    (m) => m.work_id === p.target_work_id,
                                  ) ??
                                    p.members.find(
                                      (m) => m.role === "existing",
                                    ) ??
                                    p.members[0],
                                ),
                              )}
                            </strong>
                            <div className="version-facts">
                              <Tag>{p.members.length} 个版本</Tag>
                              <Tag>
                                {p.kind === "create_work"
                                  ? "组成新组"
                                  : "加入已有组"}
                              </Tag>
                            </div>
                            <Tag color={p.tier === "strong" ? "green" : "gold"}>
                              {p.tier === "strong" ? "较强建议" : "需核对"}
                            </Tag>
                          </span>
                        </button>
                      </div>
                    ))}
                  </div>
                  {!proposals.loading && proposals.data?.total === 0 && (
                    <p className="version-empty">
                      暂无方案，选择书库后点击查找版本
                    </p>
                  )}
                </Spin>
              </div>
              <Pager data={proposals.data} onChange={(p) => url({ page: p })} />
            </>
          )}
        </section>
        <section
          className="version-column version-center"
          aria-label="成员与预览"
        >
          <div className="version-center-body">
            <ErrorNotice error={detail.error} retry={detail.reload} />
            {proposalID ? (
              selectedProposal ? (
                <ProposalEditor
                  key={selectedProposal.id}
                  proposal={selectedProposal}
                  onDone={decided}
                />
              ) : (
                <Spin />
              )
            ) : work ? (
              <GroupMembers
                key={work}
                work={work}
                refresh={refresh}
                onRefresh={reload}
                onLoaded={setLoadedGroup}
              />
            ) : (
              <div className="version-selection-empty">
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={
                    <>
                      <Typography.Title level={4}>
                        选择一个版本组
                      </Typography.Title>
                      <Typography.Text type="secondary">
                        从左侧选择版本组或推荐方案，查看成员
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
      <WorkspaceBookDetails onChanged={reload} />
    </div>
  );
}
