import { nextImportStage } from "../../shared/enhancementFlow";
import { useRetainedRemote } from "../useRetainedRemote";
/** Content: filename and best matching source title lead, state/actions support, details unfold.
 * §2.8: one primary action in a reserved footer. §3.6: 8/12/16/24 Ant spacing.
 * No remote image reads until the cover comparison is intentionally opened. */
import { useCallback, useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import {
  Alert,
  App,
  Button,
  Dropdown,
  Empty,
  Pagination,
  Select,
  Space,
  Spin,
  theme,
} from "antd";
import type {
  ImportBatch,
  ImportMember,
  ImportMemberPage,
  ImportSelection,
} from "../../shared/imports";
import { importAPI, memberSelection } from "../../shared/imports";
import {
  emptyReviewSelection,
  reconcileReviewSelection,
  selectReviewItem,
  clearReviewSelection,
  finishMetadataReview,
} from "./importReviewFlow";
import type { ReviewSelectionState } from "./importReviewFlow";
import { ErrorNotice, useAction, useDirty, useURLValue } from "../common";
import {
  CheckSquareOutlined,
  SwitcherOutlined,
  UnorderedListOutlined,
} from "@ant-design/icons";
import { request } from "../../shared/api";
import { matchingIntent } from "../sourceFlow";
import { ImportBatchHeader } from "./ImportBatchHeader";
import { ImportReviewCard } from "./ImportReviewCard";
import { ImportMetadataEditor } from "./ImportMetadataEditor";
import "./importReview.css";
import { ImportSourceSearch } from "./ImportSourceSearch";

function useReviewStorage<T>(key: string, fallback: () => T) {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = sessionStorage.getItem(key);
      return raw ? (JSON.parse(raw) as T) : fallback();
    } catch {
      return fallback();
    }
  });
  useEffect(() => {
    try {
      sessionStorage.setItem(key, JSON.stringify(value));
    } catch {
      /* Keep an in-memory draft when storage is unavailable. */
    }
  }, [key, value]);
  return [value, setValue] as const;
}
export function ImportMetadataWorkspace({
  batch: b,
  library,
  onRefresh,
}: {
  batch: ImportBatch;
  library: string;
  onRefresh: () => void;
}) {
  const nextStage = nextImportStage(b.enhancement_stages, "metadata"),
    nextAction =
      nextStage === "finished"
        ? "完成导入"
        : nextStage === "series"
          ? "进入系列审核"
          : "进入版本审核",
    nextStart =
      nextStage === "finished"
        ? "完成导入"
        : nextStage === "series"
          ? "开始系列审核"
          : "开始版本审核";
  const [page, setPage] = useURLValue("page", 1),
    [filter, setFilter] = useURLValue("filter", "pending");
  const [selection, setSelection] = useReviewStorage<ReviewSelectionState>(
      `import:${b.id}:review-selection-v2`,
      emptyReviewSelection,
    ),
    [urls, setURLs] = useReviewStorage<Record<string, string>>(
      `import:${b.id}:urls`,
      () => ({}),
    );
  const [refresh, setRefresh] = useState(0),
    [identities, setIdentities] = useState<ImportSelection[]>([]),
    [selectionError, setSelectionError] = useState<unknown>(null),
    [selectionLoading, setSelectionLoading] = useState(false),
    [editing, setEditing] = useState<ImportMember | null>(null),
    [advanceError, setAdvanceError] = useState<unknown>(null);
  const [comparing, setComparing] = useState<{
    member: ImportMember;
    mode?: "matches" | "title";
  } | null>(null);
  const compareFocus = useRef<HTMLElement | null>(null);
  const compareReturnInput = useRef<string | null>(null);
  const [returningFromCompare, setReturningFromCompare] = useState(false);
  const [manualInput, setManualInput] = useState<string | null>(null);
  function closeCompare() {
    setComparing(null);
    setReturningFromCompare(true);
    reloadMembers();
    onRefresh();
  }
  const advancing = useRef(false);
  const action = useAction(),
    { message, modal } = App.useApp(),
    { token } = theme.useToken();
  const path = `/import-batches/${b.id}/members?stage=metadata&page=${page}&page_size=25&filter=${filter}`;
  const members = useRetainedRemote<ImportMemberPage>(path),
    rows = members.data?.items ?? [];
  const reloadMembers = members.reload;
  useEffect(() => {
    if (comparing || !returningFromCompare || members.refreshing) return;
    const frame = requestAnimationFrame(() => {
      const inputID = compareReturnInput.current;
      const target = inputID
        ? document.getElementById(`gallery-${inputID}`)
        : compareFocus.current;
      if (target?.isConnected) {
        target.focus({ preventScroll: true });
        if (inputID) target.scrollIntoView({ block: "nearest" });
      }
      setReturningFromCompare(false);
    });
    return () => cancelAnimationFrame(frame);
  }, [
    comparing,
    returningFromCompare,
    members.refreshing,
    members.data,
    manualInput,
  ]);
  const focusReturn = useRef<HTMLElement | null>(null),
    pendingFocus = useRef(false);
  const editSelection = useRef<{
    item?: ImportSelection;
    excluded: boolean;
    stale: boolean;
  } | null>(null);
  const latestSelection = useRef(selection);
  useEffect(() => {
    latestSelection.current = selection;
  }, [selection]);
  const canReview = ["processing", "review"].includes(b.state),
    chosen = Object.values(selection.selected),
    stale = Object.keys(selection.stale).length;
  useDirty(Object.keys(urls).length > 0 || action.busy);
  const reload = useCallback(() => {
    onRefresh();
    reloadMembers();
    setRefresh((v) => v + 1);
  }, [onRefresh, reloadMembers]);
  useEffect(() => {
    let cancelled = false;
    void Promise.resolve().then(() => {
      if (!cancelled) {
        reloadMembers();
        setRefresh((v) => v + 1);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [b, reloadMembers]);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(async () => {
      setSelectionLoading(true);
      setSelectionError(null);
      try {
        const first = await importAPI.selection(b.id, 1, controller.signal);
        if (first.total > 1000)
          throw Error("本批超过1000项，请按页分批勾选；不会自动截断整批选择。");
        const all = [...first.items];
        for (let p = 2; p <= Math.ceil(first.total / 100); p++) {
          const next = await importAPI.selection(b.id, p, controller.signal);
          if (next.total !== first.total)
            throw Error("成员状态已变化，请刷新后重新核对整批选择。");
          all.push(...next.items);
        }
        if (controller.signal.aborted) return;
        setIdentities(all);
        setSelection((old) => reconcileReviewSelection(old, all));
      } catch (e) {
        if (!controller.signal.aborted) setSelectionError(e);
      } finally {
        if (!controller.signal.aborted) setSelectionLoading(false);
      }
    });
    return () => controller.abort();
  }, [b.id, b.revision, refresh, setSelection]);
  const select = (item: ImportSelection, checked: boolean) => {
    try {
      setSelection(selectReviewItem(latestSelection.current, item, checked));
    } catch (e) {
      void message.warning(e instanceof Error ? e.message : "无法选择");
    }
  };
  const remove = (ids: string[]) =>
    setSelection((old) => {
      let next = old;
      for (const id of ids) {
        const item = old.selected[id] ?? identities.find((v) => v.id === id);
        if (item) next = selectReviewItem(next, item, false);
      }
      return next;
    });
  async function advanceAfterReview(handled: string[] = []) {
    if (
      advancing.current ||
      Object.keys(urls).some((id) => !handled.includes(id))
    )
      return;
    advancing.current = true;
    setAdvanceError(null);
    try {
      await finishMetadataReview(
        () => request<ImportBatch>(`/import-batches/${b.id}`),
        (latest) => importAPI.control(latest, "continue"),
      );
    } catch (e) {
      setAdvanceError(e);
    } finally {
      advancing.current = false;
      reload();
    }
  }
  function requestReview(decision: string, items: ImportSelection[]) {
    if (decision !== "skip") {
      void review(decision, items);
      return;
    }
    modal.confirm({
      title: `本次不更新这 ${items.length} 项元数据？`,
      content:
        "不采纳本次候选，保留已有元数据和人工修改，并将这些条目标记为本次已处理。不会删除漫画。",
      okText: "本次不更新",
      cancelText: "继续核对",
      onOk: () => review("skip", items),
    });
  }
  async function review(decision: string, items: ImportSelection[]) {
    if (!items.length) return;
    await action.run(async () => {
      await importAPI.review(
        b,
        items.map((v) => ({
          id: v.id,
          expected_revision: v.expected_revision,
          expected_edition_revision: v.expected_edition_revision,
          candidate_revision: v.candidate_revision,
          decision,
        })),
      );
      remove(items.map((v) => v.id));
      void message.success(`已处理 ${items.length} 项`);
      await advanceAfterReview(items.map((v) => v.id));
    }, "");
  }
  async function refetch(v: ImportMember, paste: boolean) {
    let accepted = false;
    await action.run(async () => {
      let url = urls[v.id] ?? v.candidate?.url ?? "";
      if (paste) {
        try {
          url = await navigator.clipboard.readText();
          setURLs((old) => ({ ...old, [v.id]: url }));
        } catch {
          throw Error("无法读取剪贴板，请在展开的输入框中填写画廊网址。");
        }
      }
      url = url.trim();
      if (url) matchingIntent("gallery", url, false);
      remove([v.id]);
      await importAPI.refetch(b.id, v, url);
      accepted = true;
      setManualInput(null);
      setURLs((old) => {
        const next = { ...old };
        delete next[v.id];
        return next;
      });
      void message.success("已排队重新匹配；获取失败时保留原候选。");
      reload();
    }, "");
    return accepted;
  }
  function closeEditor(saved = false) {
    const id = editing?.id,
      previous = editSelection.current;
    if (id && !saved && previous)
      setSelection((old) => {
        const next = {
          selected: { ...old.selected },
          excluded: { ...old.excluded },
          stale: { ...old.stale },
        };
        if (previous.item) next.selected[id] = previous.item;
        if (previous.excluded) next.excluded[id] = true;
        else delete next.excluded[id];
        if (previous.stale) next.stale[id] = true;
        else delete next.stale[id];
        return next;
      });
    setEditing(null);
    editSelection.current = null;
    pendingFocus.current = true;
  }
  useEffect(() => {
    if (
      editing ||
      members.loading ||
      members.loadedPath !== path ||
      !pendingFocus.current
    )
      return;
    pendingFocus.current = false;
    if (focusReturn.current?.isConnected)
      focusReturn.current.focus({ preventScroll: true });
    else
      document
        .querySelector<HTMLElement>(
          ".import-review-row button, .import-review-footer button",
        )
        ?.focus({ preventScroll: true });
  }, [editing, members.loading, members.loadedPath, path, members.data]);
  const selectPage = (invert: boolean) => {
    let next = latestSelection.current;
    try {
      for (const v of rows.filter(
        (v) => !v.decision && v.status !== "fetching",
      )) {
        const item = memberSelection(v);
        next = selectReviewItem(
          next,
          item,
          invert ? !next.selected[v.id] : true,
        );
      }
      setSelection(next);
    } catch (e) {
      void message.warning(e instanceof Error ? e.message : "无法选择");
    }
  };
  const draftCount = Object.keys(urls).length,
    allReady =
      chosen.length > 0 &&
      chosen.every((v) => v.candidate_acceptable && !urls[v.id]);
  const style = {
    "--review-bg": token.colorBgContainer,
    "--review-text": token.colorText,
    "--review-secondary": token.colorTextSecondary,
    "--review-border": token.colorBorderSecondary,
    "--review-fill": token.colorFillAlter,
    "--review-primary": token.colorPrimary,
    "--review-highlight": token.colorPrimaryBgHover,
    "--review-selected-border": token.colorPrimaryBorder,
    "--review-warning": token.colorWarningText,
    "--review-warning-bg": token.colorWarningBg,
    "--review-warning-border": token.colorWarningBorder,
    "--review-error-bg": token.colorErrorBg,
    "--review-error-border": token.colorErrorBorder,
    "--review-disabled": token.colorTextDisabled,
    "--review-error": token.colorErrorText,
    "--review-radius": `${token.borderRadius}px`,
  } as CSSProperties;
  return (
    <div className="import-review-frame" style={style}>
      <ImportBatchHeader
        batch={b}
        library={library}
        onRefresh={reload}
        disabled={action.busy}
      />
      <ErrorNotice
        error={
          action.error ||
          (!members.data ? members.error : null) ||
          selectionError
        }
        retry={reload}
      />
      <div className="import-refresh-status" role="status">
        {members.data && members.error ? (
          <>
            <span>更新失败，已保留当前内容</span>
            <Button type="link" size="small" onClick={reload}>
              重试
            </Button>
          </>
        ) : null}
      </div>
      {b.error_code && (
        <Alert
          type="error"
          showIcon
          title="批次需要重新核对"
          description="请通过更多操作查看任务或重新审核，已采纳资料保留。"
        />
      )}
      {stale > 0 && (
        <Alert
          type="warning"
          showIcon
          title={`${stale}项资料或状态已变化，已移出可提交选择；请重新核对后勾选。`}
        />
      )}
      <div className="import-selection-tools">
        <Space size={4} wrap>
          <Button
            disabled={!canReview || action.busy || !rows.length}
            onClick={() => selectPage(false)}
            aria-label="本页全选"
            icon={<CheckSquareOutlined aria-hidden="true" />}
          />
          <Button
            disabled={!canReview || action.busy || !rows.length}
            onClick={() => selectPage(true)}
            aria-label="本页反选"
            icon={<SwitcherOutlined aria-hidden="true" />}
          />
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                {
                  key: "all",
                  label: "选择整批可采纳项",
                  disabled: selectionLoading || !!selectionError,
                },
                { key: "none", label: "取消选择", disabled: !chosen.length },
              ],
              onClick: ({ key }) => {
                if (key === "none")
                  setSelection(clearReviewSelection(selection, identities));
                else {
                  const ready = identities.filter(
                    (v) => v.candidate_acceptable && !v.decision && !urls[v.id],
                  );
                  if (ready.length > 1000) {
                    void message.warning("超过1000项，请分批选择");
                    return;
                  }
                  let next = selection;
                  for (const v of ready) next = selectReviewItem(next, v, true);
                  setSelection(next);
                }
              },
            }}
          >
            <Button
              aria-label="整批选择"
              icon={<UnorderedListOutlined aria-hidden="true" />}
              disabled={!canReview || action.busy}
            />
          </Dropdown>
        </Space>
        <Select
          aria-label="审核筛选"
          value={filter}
          disabled={action.busy}
          options={[
            { value: "pending", label: `待审核 ${b.pending}` },
            { value: "all", label: `全部 ${b.total}` },
            { value: "errors", label: "未匹配与异常" },
          ]}
          onChange={(v) => {
            setFilter(v);
            setPage(1);
          }}
        />
      </div>
      <div className="import-review-scroll" tabIndex={-1}>
        <Spin spinning={members.loading}>
          <div className="import-compact-list">
            {rows.map((v) => (
              <ImportReviewCard
                key={v.id}
                batch={b.id}
                member={v}
                checked={!!selection.selected[v.id]}
                disabled={!canReview || action.busy}
                url={urls[v.id]}
                showURLInput={manualInput === v.id}
                onURL={(value) => {
                  if (value === undefined) setManualInput(null);
                  select(memberSelection(v), false);
                  setURLs((old) => {
                    const next = { ...old };
                    if (value === undefined) delete next[v.id];
                    else next[v.id] = value;
                    return next;
                  });
                }}
                onSelect={(checked) => select(memberSelection(v), checked)}
                onReview={(decision) =>
                  requestReview(decision, [memberSelection(v)])
                }
                onRefetch={(paste) => refetch(v, paste)}
                onEdit={() => {
                  focusReturn.current = document.activeElement as HTMLElement;
                  editSelection.current = {
                    item: selection.selected[v.id],
                    excluded: !!selection.excluded[v.id],
                    stale: !!selection.stale[v.id],
                  };
                  select(memberSelection(v), false);
                  setEditing(v);
                }}
                onCompare={(mode, trigger) => {
                  setReturningFromCompare(false);
                  compareReturnInput.current = null;
                  compareFocus.current =
                    trigger ?? (document.activeElement as HTMLElement);
                  setComparing({ member: v, mode });
                }}
                onRetry={() =>
                  void action.run(async () => {
                    await importAPI.retryFiles(b, [v.id]);
                    reload();
                  }, "")
                }
              />
            ))}
          </div>
          {!members.loading && !members.error && !rows.length && (
            <Empty
              description={
                b.pending ? "此筛选下没有待审核条目" : "本阶段已全部处理"
              }
            />
          )}
        </Spin>
      </div>
      <Pagination
        className="import-review-pagination"
        current={page}
        pageSize={25}
        total={members.data?.total ?? 0}
        showSizeChanger={false}
        onChange={setPage}
        disabled={action.busy}
      />
      <footer className="import-review-footer">
        <div>
          <strong>已选 {chosen.length} 项</strong>
          <span>
            {draftCount
              ? `${draftCount} 项网址尚未提交`
              : b.pending
                ? `待处理 ${b.pending} 项`
                : "本阶段已全部处理"}
          </span>
        </div>
        <Space size={8} wrap>
          {b.pending > 0 && (
            <Button
              disabled={!chosen.length || action.busy || !canReview}
              onClick={() => requestReview("skip", chosen)}
            >
              本次不更新所选元数据
            </Button>
          )}
          <Button
            type="primary"
            loading={action.busy}
            disabled={
              !canReview ||
              draftCount > 0 ||
              (b.pending > 0 ? !allReady : b.state !== "review")
            }
            onClick={() => {
              if (b.pending > 0) void review("accept", chosen);
              else void action.run(() => advanceAfterReview(), "");
            }}
          >
            {b.pending > 0
              ? `确认所选匹配（${chosen.length}）`
              : advanceError
                ? `重试${nextAction}`
                : nextStart}
          </Button>
        </Space>
      </footer>
      {!!advanceError && (
        <Alert
          type="error"
          showIcon
          title={`审核已保存，${nextAction}失败`}
          description={<ErrorNotice error={advanceError} />}
        />
      )}
      {comparing && (
        <ImportSourceSearch
          key={comparing.member.id}
          batch={b.id}
          member={comparing.member}
          initialTab={comparing.mode}
          disabled={!canReview || action.busy}
          onClose={closeCompare}
          onSelected={() => {
            const id = comparing.member.id;
            setSelection((old) => {
              const selected = { ...old.selected };
              delete selected[id];
              return { ...old, selected, stale: { ...old.stale, [id]: true } };
            });
            closeCompare();
            reload();
            void message.success("候选已更新，请核对后确认匹配");
          }}
          onPaste={() => {
            const member = comparing.member;
            closeCompare();
            void refetch(member, true).then((success) => {
              if (!success) {
                compareReturnInput.current = member.id;
                setManualInput(member.id);
                setReturningFromCompare(true);
              }
            });
          }}
        />
      )}
      {editing && (
        <ImportMetadataEditor
          batch={b.id}
          member={editing}
          onClose={() => closeEditor()}
          onSaved={() => {
            remove([editing.id]);
            setURLs((old) => {
              const next = { ...old };
              delete next[editing.id];
              return next;
            });
            closeEditor(true);
            void action.run(() => advanceAfterReview([editing.id]), "");
            void message.success("资料已保存并确认");
          }}
        />
      )}
    </div>
  );
}
