import { ImportFailureNotice } from "./ImportFailure";
import "../libraries/libraryImport.css";
import {
  suggestionAction,
  suggestionName,
} from "./importOrganizationPresentation";
/** §2.1/§3.6: one suggestion at a time; members support the concrete result.
 * React/Ant SPA, existing light/dark tokens, 8/12/16/24 spacing, no new palette.
 * All stylesheet dependencies are explicit, including direct import deep links. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  App,
  Button,
  Drawer,
  Dropdown,
  Empty,
  Pagination,
  Segmented,
  Space,
  Spin,
  Tag,
} from "antd";
import { MoreOutlined } from "@ant-design/icons";
import { useLocation, useNavigate } from "react-router";
import { request } from "../../shared/api";
import { reason } from "../../shared/catalog";
import type { Listing } from "../../shared/api";
import { importAPI, importProposalTransport } from "../../shared/imports";
import type { ImportBatch, OrganizationReview } from "../../shared/imports";
import { ImportBatchHeader } from "./ImportBatchHeader";
import { ErrorNotice, useAction, useDirty, useURLValue } from "../common";
import { Cover, useVersionRemote } from "../versions/versionManagementViews";
import { SeriesDragProvider } from "../series/SeriesDrag";
import { SeriesProposalEditor } from "../series/SeriesProposalEditor";
import { seriesDraft, seriesDirty } from "../series/seriesDiscoveryFlow";
import type { SeriesDraft } from "../series/seriesDiscoveryFlow";
import { ImportWorkProposal } from "./ImportWorkProposal";
import { BookEditor } from "../books/BookEditor";
import {
  nextSuggestionIndex,
  shouldCompleteReview,
  workReviewDraft,
  workReviewDirty,
} from "./importOrganizationFlow";
import type {
  OrganizationProposal,
  WorkReviewDraft,
} from "./importOrganizationFlow";
import "../versions/versionManagement.css";
import "../series/seriesManagement.css";
import "./importReview.css";
import "./importOrganizationReview.css";

function useStoredDrafts<T>(key: string) {
  const [value, setValue] = useState<Record<string, T>>(() => {
    try {
      return JSON.parse(sessionStorage.getItem(key) ?? "{}");
    } catch {
      return {};
    }
  });
  useEffect(() => {
    try {
      sessionStorage.setItem(key, JSON.stringify(value));
    } catch {
      /* In-memory drafts remain available. */
    }
  }, [key, value]);
  return [value, setValue] as const;
}
function QueueCover({ proposal: p }: { proposal: OrganizationProposal }) {
  if ("members" in p) {
    const m = p.members.find((m) => m.role === "new") ?? p.members[0];
    return m ? (
      <Cover
        id={m.id}
        content={m.content_revision}
        revision={m.cover_revision}
        preview={false}
      />
    ) : null;
  }
  return p.cover ? (
    <Cover
      id={p.cover.edition_id}
      content={p.cover.content_revision}
      revision={p.cover.cover_revision}
      preview={false}
    />
  ) : null;
}
export function ImportOrganizationWorkspace({
  id,
  batchData: b,
  reloadParent,
  library,
}: {
  id: string;
  batchData: ImportBatch;
  reloadParent: () => void;
  library: string;
}) {
  const location = useLocation(),
    navigate = useNavigate(),
    { message, modal } = App.useApp();
  const organization = b.stage === "versions" || b.stage === "series",
    series = b.stage === "series";
  const [page, setPage] = useURLValue("review_page", 1),
    [region, setRegion] = useState("members");
  const selectionKey = `${b.stage}_proposal`,
    selectedID = new URLSearchParams(location.search).get(selectionKey) ?? "";
  const [workDrafts, setWorkDrafts] = useStoredDrafts<WorkReviewDraft>(
      `import:${id}:work-review-drafts`,
    ),
    [seriesDrafts, setSeriesDrafts] = useStoredDrafts<SeriesDraft>(
      `import:${id}:series-drafts`,
    );
  const [busy, setBusy] = useState(false),
    [dragBusy, setDragBusy] = useState(false),
    [autoPending, setAutoPending] = useState(false),
    [advanceError, setAdvanceError] = useState<unknown>(null);
  const [secondary, setSecondary] = useState<"" | "alternatives" | "history">(
      "",
    ),
    [historyState, setHistoryState] = useState("accepted"),
    [secondaryPage, setSecondaryPage] = useState(1),
    [recordStage, setRecordStage] = useState<"versions" | "series">(
      b.stage === "versions" ? "versions" : "series",
    ),
    [book, setBook] = useState<string | null>(null);
  const completing = useRef(false),
    mounted = useRef(true),
    action = useAction();
  const base = `/import-batches/${id}`,
    stem = series ? "series-proposals" : "work-proposals";
  const transport = useMemo(() => importProposalTransport(id), [id]);
  const queuePath = `${base}/${stem}?state=pending&alternatives=false&page=${page}&page_size=25`;
  const queue = useVersionRemote<Listing<OrganizationProposal>>(
    queuePath,
    undefined,
    "GET",
    organization,
  );
  const summary = useVersionRemote<OrganizationReview>(
    `${base}/organization-review`,
    undefined,
    "GET",
    organization,
  );
  const proposals = queue.data?.items ?? [],
    selected = selectedID || proposals[0]?.id || "";
  const detail = useVersionRemote<OrganizationProposal>(
    `${base}/${stem}/${encodeURIComponent(selected)}`,
    undefined,
    "GET",
    organization && !!selected,
  );
  const history = useVersionRemote<Listing<OrganizationProposal>>(
    `${base}/${secondary === "history" ? (recordStage === "series" ? "series-proposals" : "work-proposals") : stem}?state=${secondary === "history" ? historyState : "pending"}&alternatives=true&only_alternatives=${secondary === "alternatives"}&page=${secondaryPage}&page_size=25`,
    undefined,
    "GET",
    b.stage !== "scan" && !!secondary,
  );
  const hasDrafts =
    Object.values(workDrafts).some(workReviewDirty) ||
    Object.values(seriesDrafts).some(seriesDirty);
  const locked = busy || dragBusy || action.busy;
  useDirty(hasDrafts || locked);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const reloadQueue = queue.reload,
    reloadSummary = summary.reload,
    reloadDetail = detail.reload,
    reloadHistory = history.reload;
  const refreshViews = useCallback(() => {
    reloadQueue();
    reloadSummary();
    reloadDetail();
    reloadHistory();
  }, [reloadQueue, reloadSummary, reloadDetail, reloadHistory]);
  const refresh = useCallback(() => {
    reloadParent();
    refreshViews();
  }, [reloadParent, refreshViews]);
  useEffect(() => {
    refreshViews();
  }, [b, refreshViews]);
  useEffect(() => {
    if (
      !organization ||
      (!autoPending &&
        !summary.data?.revalidating &&
        !summary.data?.active_tasks &&
        b.state !== "processing")
    )
      return;
    const timer = setInterval(refreshViews, 1500);
    return () => clearInterval(timer);
  }, [
    organization,
    autoPending,
    summary.data?.revalidating,
    summary.data?.active_tasks,
    b.state,
    refreshViews,
  ]);
  useEffect(() => {
    if (
      (b.state === "failed" ||
        b.error_code === "enhancement_disabled" ||
        summary.data?.recommendation_skipped) &&
      autoPending
    )
      void Promise.resolve().then(() => {
        if (mounted.current) setAutoPending(false);
      });
  }, [
    b.state,
    b.error_code,
    summary.data?.recommendation_skipped,
    autoPending,
  ]);
  function select(id: string, pageValue = page) {
    const params = new URLSearchParams(location.search);
    if (id) params.set(selectionKey, id);
    else params.delete(selectionKey);
    if (pageValue === 1) params.delete("review_page");
    else params.set("review_page", String(pageValue));
    void navigate({ search: params.toString() }, { preventScrollReset: true });
    setRegion("members");
  }
  const complete = useCallback(async () => {
    if (completing.current) return;
    completing.current = true;
    setAdvanceError(null);
    setAutoPending(false);
    await action.run(async () => {
      try {
        const fresh = await importAPI.organizationReview(id);
        if (!fresh.can_complete)
          throw Error("建议仍在更新或有待处理项，请刷新后继续。");
        await importAPI.completeOrganization(id, fresh);
      } catch (e) {
        if (mounted.current) setAdvanceError(e);
      } finally {
        if (mounted.current) {
          completing.current = false;
          refresh();
        }
      }
    }, "");
  }, [id, refresh, action]);
  // Only explicit successful proposal actions arm this gate. Polling can wait
  // for revalidation, but cannot initiate a fresh stage on its own.
  useEffect(() => {
    if (!autoPending || !summary.data || locked || hasDrafts) return;
    if (summary.data.pending_proposals > 0 && !summary.data.revalidating) {
      void Promise.resolve().then(() => {
        if (mounted.current) setAutoPending(false);
      });
      return;
    }
    if (shouldCompleteReview(autoPending, summary.data, hasDrafts))
      void complete();
  }, [autoPending, summary.data, locked, hasDrafts, complete]);
  async function done() {
    setAdvanceError(null);
    const before = proposals.findIndex((p) => p.id === selected);
    setWorkDrafts((all) => {
      const next = { ...all };
      delete next[selected];
      return next;
    });
    setSeriesDrafts((all) => {
      const next = { ...all };
      delete next[selected];
      return next;
    });
    let currentPage = page;
    let fresh = await request<Listing<OrganizationProposal>>(queuePath);
    if (!fresh.items.length && currentPage > 1) {
      currentPage = Math.max(1, Math.ceil(fresh.total / 25));
      fresh = await request<Listing<OrganizationProposal>>(
        `${base}/${stem}?state=pending&alternatives=false&page=${currentPage}&page_size=25`,
      );
    }
    if (!mounted.current) return;
    const review = await importAPI.organizationReview(id);
    if (!mounted.current) return;
    queue.setData(fresh);
    summary.setData(review);
    setAutoPending(true);
    select(
      fresh.items[nextSuggestionIndex(fresh.items.length, before)]?.id ?? "",
      currentPage,
    );
    refresh();
    void message.success("已处理当前建议");
  }
  const incoming = detail.data;
  const work = incoming && "members" in incoming ? incoming : undefined;
  const seriesProposal =
    incoming && "new_work_ids" in incoming ? incoming : undefined;
  const ready = summary.data?.can_complete && !hasDrafts;
  const nextAction =
    series || b.enhancement_stages?.series?.enabled === false
      ? "完成导入"
      : "进入系列审核";
  return (
    <div className="import-organization-frame">
      <ImportBatchHeader
        batch={b}
        library={library}
        onRefresh={refresh}
        disabled={locked}
      />
      <ImportFailureNotice
        batch={b}
        onRefresh={refresh}
        beforeAction={() => setAutoPending(false)}
        disabled={locked}
      />
      <ErrorNotice
        error={queue.error || summary.error || action.error}
        retry={refresh}
      />
      {!!advanceError && (
        <Alert
          type="error"
          showIcon
          title={`整理已保存，${nextAction}失败`}
          description={<ErrorNotice error={advanceError} />}
          action={
            <Button disabled={locked} onClick={() => void complete()}>
              重试
            </Button>
          }
        />
      )}
      {!organization ? (
        <div className="import-organization-empty">
          {b.stage === "scan" && b.state === "failed" && (
            <Alert
              type="error"
              showIcon
              title="扫描入库失败"
              description={reason(
                b.jobs?.find((j) => j.type === "scan" && j.state === "failed")
                  ?.error_code || b.error_code,
              )}
            />
          )}
          <Empty
            description={
              b.stage === "scan"
                ? b.state === "failed"
                  ? "扫描入库失败，请查看任务详情或重试"
                  : b.state === "paused"
                    ? "扫描已暂停"
                    : "正在扫描入库…"
                : b.state === "completed"
                  ? "导入已完成"
                  : "批次已结束"
            }
          />
          {b.stage !== "scan" && (
            <Button
              onClick={() => {
                setSecondary("history");
                setSecondaryPage(1);
              }}
            >
              查看整理记录
            </Button>
          )}
        </div>
      ) : (
        <>
          <div className="import-organization-mobile-switch">
            <Segmented
              block
              value={region}
              onChange={setRegion}
              disabled={locked}
              options={[
                { label: "建议列表", value: "queue" },
                { label: "当前建议", value: "members" },
              ]}
            />
          </div>
          <div className={`import-organization-layout region-${region}`}>
            <aside
              className="import-organization-queue"
              aria-label="归属建议列表"
            >
              <div className="import-organization-queue-heading">
                <strong>{series ? "系列建议" : "版本建议"}</strong>
                <Space size={4}>
                  <Tag>
                    {summary.data?.pending_proposals ?? queue.data?.total ?? 0}{" "}
                    项
                  </Tag>
                  <Dropdown
                    disabled={locked}
                    menu={{
                      items: [
                        { key: "alternatives", label: "其他建议" },
                        { key: "history", label: "处理记录" },
                        ...Object.entries(workDrafts)
                          .filter(([, d]) => workReviewDirty(d))
                          .map(([key, d]) => ({
                            key: `draft:${key}`,
                            label: `继续修改：${suggestionName(d.baseline)}`,
                          })),
                        ...Object.entries(seriesDrafts)
                          .filter(([, d]) => seriesDirty(d))
                          .map(([key, d]) => ({
                            key: `draft:${key}`,
                            label: `继续修改：${d.name || "未命名系列"}`,
                          })),
                        ...(hasDrafts
                          ? [{ key: "discard", label: "放弃未提交调整" }]
                          : []),
                      ],
                      onClick: ({ key }) => {
                        if (key.startsWith("draft:")) {
                          select(key.slice(6));
                          return;
                        }
                        if (key === "discard") {
                          modal.confirm({
                            title: "放弃未提交的成员、名称和顺序调整？",
                            okText: "放弃调整",
                            cancelText: "继续修改",
                            onOk: () => {
                              setWorkDrafts({});
                              setSeriesDrafts({});
                            },
                          });
                          return;
                        }
                        if (key === "history")
                          setRecordStage(series ? "series" : "versions");
                        setSecondary(key as "alternatives" | "history");
                        setSecondaryPage(1);
                      },
                    }}
                  >
                    <Button
                      type="text"
                      icon={<MoreOutlined />}
                      aria-label="审核记录与其他建议"
                    />
                  </Dropdown>
                </Space>
              </div>
              <div className="import-organization-queue-scroll">
                <Spin spinning={queue.loading}>
                  <div className="version-group-list">
                    {proposals.map((p) => (
                      <button
                        key={p.id}
                        className={`version-group-row ${selected === p.id ? "selected" : ""}`}
                        type="button"
                        aria-pressed={selected === p.id}
                        disabled={locked}
                        onClick={() => select(p.id)}
                      >
                        <QueueCover proposal={p} />
                        <span>
                          <strong className="version-row-title">
                            {suggestionName(p)}
                          </strong>
                          <Space size={4} wrap>
                            <Tag>
                              {"members" in p
                                ? p.members.length
                                : p.member_count}{" "}
                              {series ? "本作品" : "个版本"}
                            </Tag>
                            <span className="import-queue-kind">
                              {suggestionAction(p)}
                            </span>
                          </Space>
                        </span>
                      </button>
                    ))}
                  </div>
                </Spin>
              </div>
              {queue.data && queue.data.total > 25 && (
                <Pagination
                  size="small"
                  current={page}
                  total={queue.data.total}
                  pageSize={25}
                  showSizeChanger={false}
                  disabled={locked}
                  onChange={(v) => {
                    setPage(v);
                    select("", v);
                  }}
                />
              )}
            </aside>
            <main
              className="import-organization-detail"
              aria-label="当前归属建议"
            >
              <ErrorNotice error={detail.error} retry={detail.reload} />
              {!selected ? (
                <div className="import-organization-empty">
                  <Empty
                    description={
                      b.state === "failed"
                        ? "推荐未成功完成，请重试或明确跳过"
                        : summary.data?.recommendation_skipped
                          ? "本次推荐已跳过，当前归属保持不变"
                          : summary.data?.active_tasks ||
                              summary.data?.revalidating
                            ? "正在更新建议…"
                            : "没有待处理的归属建议"
                    }
                  />
                  {ready && (
                    <Button
                      type="primary"
                      loading={action.busy}
                      onClick={() => void complete()}
                    >
                      {nextAction}
                    </Button>
                  )}
                  {hasDrafts && (
                    <p>还有尚未处理的建议草稿，可从建议列表的更多操作继续。</p>
                  )}
                </div>
              ) : !incoming ? (
                <Spin />
              ) : (
                <SeriesDragProvider onActivity={setDragBusy}>
                  {work && (
                    <ImportWorkProposal
                      key={selected}
                      disabled={!["processing", "review"].includes(b.state)}
                      incoming={work}
                      draft={workDrafts[selected] ?? workReviewDraft(work)}
                      onDraft={(d) =>
                        setWorkDrafts((all) => ({ ...all, [selected]: d }))
                      }
                      onDone={done}
                      onBusy={setBusy}
                      transport={transport}
                      onOpenBook={setBook}
                    />
                  )}
                  {seriesProposal && (
                    <SeriesProposalEditor
                      key={selected}
                      externalDisabled={
                        !["processing", "review"].includes(b.state)
                      }
                      incoming={seriesProposal}
                      draft={
                        seriesDrafts[selected] ?? seriesDraft(seriesProposal)
                      }
                      onDraft={(d) =>
                        setSeriesDrafts((all) => ({ ...all, [selected]: d }))
                      }
                      onDone={done}
                      onReload={(p, rebased) => {
                        setSeriesDrafts((all) => ({
                          ...all,
                          [selected]: rebased ?? seriesDraft(p),
                        }));
                        detail.setData(p);
                      }}
                      onBusy={setBusy}
                      reviewLocked
                      continuousControls={false}
                      onReviewLocked={() => {}}
                      reviewMode
                      transport={transport}
                      onOpenBook={setBook}
                    />
                  )}
                </SeriesDragProvider>
              )}
            </main>
          </div>
          <footer className="import-organization-status" aria-live="polite">
            {b.state === "failed"
              ? "推荐失败，尚未完成本阶段"
              : summary.data?.recommendation_skipped
                ? "已明确跳过，等待确认继续"
                : summary.data?.active_tasks
                  ? "正在生成归属建议…"
                  : summary.data?.revalidating
                    ? "正在更新剩余建议…"
                    : hasDrafts
                      ? "成员、名称或顺序有未提交修改"
                      : `${summary.data?.pending_proposals ?? 0} 项建议待处理`}
            {!!advanceError && (
              <Button disabled={locked} onClick={() => void complete()}>
                重试{nextAction}
              </Button>
            )}
          </footer>
        </>
      )}
      <Drawer
        title={secondary === "history" ? "处理记录" : "其他建议"}
        open={!!secondary}
        onClose={() => setSecondary("")}
        size={680}
      >
        {secondary === "history" && (
          <Segmented
            value={recordStage}
            options={[
              { label: "版本归属", value: "versions" },
              { label: "系列归属", value: "series" },
            ]}
            onChange={(v) => {
              setRecordStage(v === "versions" ? "versions" : "series");
              setSecondaryPage(1);
            }}
          />
        )}
        {secondary === "history" && (
          <Segmented
            value={historyState}
            options={[
              { label: "已采纳", value: "accepted" },
              { label: "未采用", value: "dismissed" },
              { label: "已撤销", value: "undone" },
            ]}
            onChange={(v) => {
              setHistoryState(v);
              setSecondaryPage(1);
            }}
          />
        )}
        <ErrorNotice error={history.error} retry={history.reload} />
        <Spin spinning={history.loading}>
          {history.data?.items.map((p) => (
            <div key={p.id} className="import-history-row">
              <QueueCover proposal={p} />
              <div>
                <strong>{suggestionName(p)}</strong>
                <p>{suggestionAction(p)}</p>
              </div>
              {secondary === "alternatives" ? (
                <Button
                  onClick={() => {
                    select(p.id);
                    setSecondary("");
                  }}
                >
                  查看建议
                </Button>
              ) : (
                <Space>
                  {p.state === "dismissed" && (
                    <Button
                      disabled={
                        locked || !organization || b.stage !== recordStage
                      }
                      onClick={() =>
                        void action.run(async () => {
                          setAutoPending(false);
                          if ("members" in p)
                            await transport.workDecide(p, "restore");
                          else await transport.decide(p, "restore");
                          refresh();
                        }, "")
                      }
                    >
                      恢复建议
                    </Button>
                  )}
                  {p.can_undo && (
                    <Button
                      disabled={locked}
                      onClick={() =>
                        void action.run(async () => {
                          setAutoPending(false);
                          if ("members" in p)
                            await transport.workDecide(p, "undo");
                          else await transport.decide(p, "undo");
                          refresh();
                        }, "")
                      }
                    >
                      撤销整理
                    </Button>
                  )}
                </Space>
              )}
            </div>
          ))}
        </Spin>
        {!history.loading && history.data?.total === 0 && (
          <Empty
            description={
              secondary === "history" ? "暂无处理记录" : "暂无其他建议"
            }
          />
        )}
        {history.data && history.data.total > 25 && (
          <Pagination
            current={secondaryPage}
            pageSize={25}
            total={history.data.total}
            showSizeChanger={false}
            onChange={setSecondaryPage}
          />
        )}
      </Drawer>
      {book && (
        <BookEditor
          key={book}
          id={book}
          onClose={() => setBook(null)}
          onChanged={refresh}
          navigate={(path) => void navigate(path)}
        />
      )}
    </div>
  );
}
