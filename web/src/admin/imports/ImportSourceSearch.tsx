import { randomIdentity } from "../../shared/randomIdentity";
import { originalTitle } from "../books/bookTitle";
/** §2.1/§3.6/§4.3: local evidence precedes full-width candidate articles.
 * Each article stages only its own frozen result. React/Ant tokens,
 * 12/16/24 spacing, neutral surfaces, 14px body/12px secondary, no new palette.
 * A title query is a form submission; opening/typing never starts that query. */
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Alert,
  App,
  Button,
  Modal,
  Empty,
  Form,
  Input,
  Pagination,
  Space,
  Spin,
  Tabs,
  Tag,
  Tooltip,
} from "antd";
import {
  ExportOutlined,
  PictureOutlined,
  DownOutlined,
  UpOutlined,
  SearchOutlined,
  SnippetsOutlined,
} from "@ant-design/icons";
import { ApiError, request } from "../../shared/api";
import type { Book } from "../../shared/api";
import { importAPI } from "../../shared/imports";
import type { ImportMember } from "../../shared/imports";
import { sourceSearchAPI } from "../../shared/sourceSearch";
import type {
  SearchMode,
  SourceResult,
  SourceSearch,
} from "../../shared/sourceSearch";
import { resourceEvent } from "../../shared/events";
import { useRetainedRemote } from "../useRetainedRemote";
import {
  sourceChoiceStale,
  sourceResultTags,
  sourceChannel,
  moreSearchPage,
  searchError,
  searchRunning,
} from "./importSourceFlow";
import type { SourceAttempt, SourceChannel } from "./importSourceFlow";
import { titleOverlap } from "./importReviewFlow";
import { categories, languages } from "../FieldsEditor";
import { namespaces } from "../tagInput";
import { Cover } from "../versions/versionManagementViews";
import { useSourceCovers } from "../useSourceCovers";
import { ErrorNotice } from "../common";
import "./importSourceSearch.css";
import { sourceTitleQuery } from "../books/bookTitle";

type Draft = {
  tab: SearchMode;
  q: string;
  matches: SourceChannel;
  title: SourceChannel;
  latestID?: string;
};
const language = (value?: string | null) =>
  languages.find(([key]) => key === value)?.[1] ?? value ?? "未知";
const category = (value?: string | null) =>
  categories.find(([key]) => key === value)?.[1] ?? value ?? "未知";
function Overlap({ filename, title }: { filename: string; title: string }) {
  const parts = useMemo(
    () => titleOverlap([filename, title])[1],
    [filename, title],
  );
  return (
    <span className="source-text">
      {parts.map((p, i) =>
        p.matched ? <mark key={i}>{p.text}</mark> : p.text,
      )}
    </span>
  );
}
export function ImportSourceSearch({
  batch,
  member,
  initialTab,
  disabled,
  onClose,
  onSelected,
  onPaste,
}: {
  batch: string;
  member: ImportMember;
  initialTab?: SearchMode;
  disabled: boolean;
  onClose: () => void;
  onSelected: () => void;
  onPaste: () => void;
}) {
  const storage = `import:${batch}:${member.id}:source-compare-v1`;
  const [draft, setDraft] = useState<Draft>(() => {
    try {
      const saved = JSON.parse(
        sessionStorage.getItem(storage) ?? "null",
      ) as Draft | null;
      if (saved?.matches && saved?.title)
        return {
          ...saved,
          tab: initialTab ?? saved.tab,
          matches: sourceChannel(saved.matches),
          title: sourceChannel(saved.title),
        };
    } catch {
      /* Use a fresh local query draft. */
    }
    return {
      tab: initialTab ?? "matches",
      latestID: member.source_search_id,
      q: sourceTitleQuery(member.filename),
      matches: {
        ...sourceChannel(),
        id: initialTab === "title" ? "" : (member.source_search_id ?? ""),
      },
      title: sourceChannel(),
    };
  });
  const channel = draft[draft.tab],
    api = useMemo(() => sourceSearchAPI(batch, member.id), [batch, member.id]);
  const [versions, setVersions] = useState({
    member: member.revision,
    edition: member.edition_revision,
  });
  const local = useRetainedRemote<Book>(
    `/books/${encodeURIComponent(member.id)}`,
  );
  const search = useRetainedRemote<SourceSearch>(
    `/import-batches/${batch}/members/${member.id}/source-searches/${channel.id}`,
    !!channel.id,
  );
  const s = search.data;
  const results = useRetainedRemote<{
    search: SourceSearch;
    items: SourceResult[];
  }>(
    `/import-batches/${batch}/members/${member.id}/source-searches/${channel.id}/results?page=${channel.page}&page_size=10`,
    !!channel.id,
  );
  const attempt = channel.attempt;
  const [busy, setBusy] = useState(false),
    [selecting, setSelecting] = useState(""),
    [error, setError] = useState<unknown>(null),
    [conflict, setConflict] = useState(false);
  const [selectionError, setSelectionError] = useState<{
    id: string;
    error: unknown;
  } | null>(null);
  const [pendingPage, setPendingPage] = useState<{
    id: string;
    page: number;
    previous: number;
    loaded: number;
  } | null>(null);
  const alive = useRef(true),
    lock = useRef(false),
    bootstrap = useRef(false);
  const [mobile, setMobile] = useState(
    () => window.matchMedia("(max-width: 760px)").matches,
  );
  const workspaceRef = useRef<HTMLDivElement>(null);
  const candidateScrollRef = useRef<HTMLDivElement>(null);
  const listRef = mobile ? workspaceRef : candidateScrollRef;
  useEffect(() => {
    const query = window.matchMedia("(max-width: 760px)");
    const change = () => setMobile(query.matches);
    query.addEventListener("change", change);
    return () => query.removeEventListener("change", change);
  }, []);
  const { message } = App.useApp();
  const mode = draft.tab;
  const update = useCallback(
    (fn: (c: SourceChannel) => SourceChannel) =>
      setDraft((d) => ({ ...d, [d.tab]: fn(d[d.tab]) })),
    [],
  );
  useEffect(() => {
    try {
      sessionStorage.setItem(storage, JSON.stringify(draft));
    } catch {
      /* Memory remains usable. */
    }
  }, [draft, storage]);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const keys = useRef(new Map<string, string>());
  const keyFor = (signature: string) => {
    let key = keys.current.get(signature);
    if (!key) {
      key = randomIdentity();
      keys.current.set(signature, key);
    }
    return key;
  };
  async function run(work: () => Promise<void>, resultID?: string) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError(null);
    setSelectionError(null);
    try {
      await work();
    } catch (e) {
      if (alive.current) {
        if (resultID) setSelectionError({ id: resultID, error: e });
        else setError(e);
        if (
          e instanceof ApiError &&
          e.status === 409 &&
          e.code !== "resource_busy"
        ) {
          setConflict(true);
          update((c) => ({
            ...c,
            attempt: c.attempt
              ? { ...c.attempt, requiresReview: true }
              : undefined,
          }));
        }
      }
    } finally {
      lock.current = false;
      if (alive.current) setBusy(false);
    }
  }
  const create = useCallback(
    async (queryMode: SearchMode, q: string) => {
      if (lock.current || disabled) return;
      lock.current = true;
      setBusy(true);
      setError(null);
      const signature = `${queryMode}:${q}:${versions.member}`;
      let key = keys.current.get(signature);
      if (!key) {
        key = randomIdentity();
        keys.current.set(signature, key);
      }
      try {
        const value = await api.create(queryMode, q, versions.member, key);
        if (!alive.current) return;
        keys.current.delete(signature);
        setDraft((d) => ({
          ...d,
          tab: queryMode,
          latestID: value.id,
          [queryMode]: { ...sourceChannel(), id: value.id },
        }));
        setPendingPage(null);
        setConflict(false);
      } catch (e) {
        if (alive.current) {
          setError(e);
          if (
            e instanceof ApiError &&
            e.status === 409 &&
            e.code !== "resource_busy"
          )
            setConflict(true);
        }
      } finally {
        lock.current = false;
        if (alive.current) setBusy(false);
      }
    },
    [api, disabled, versions.member],
  );
  useEffect(() => {
    if (mode !== "matches" || channel.id || bootstrap.current) return;
    const timer = setTimeout(() => {
      bootstrap.current = true;
      void create("matches", "");
    }, 0);
    return () => clearTimeout(timer);
  }, [mode, channel.id, create]);
  // A server-side latest query may be a title query after browser draft loss.
  useEffect(() => {
    if (s && s.mode !== mode)
      void Promise.resolve().then(() => {
        if (alive.current)
          setDraft((d) => ({
            ...d,
            tab: s.mode,
            q: s.mode === "title" ? s.q : d.q,
            [s.mode]: { ...sourceChannel(), id: s.id },
            [mode]: sourceChannel(),
          }));
      });
  }, [s, mode]);
  const reloadSearch = search.reload,
    reloadResults = results.reload;
  useEffect(() => {
    if (s) {
      reloadResults();
    }
  }, [s, reloadResults]);
  useEffect(() => {
    if (!channel.id) return;
    let merged: ReturnType<typeof setTimeout> | undefined;
    const read = () => {
      if (document.visibilityState === "visible") reloadSearch();
    };
    const notify = (e: Event) => {
      const d = (e as CustomEvent<{ type?: string; resource_id?: string }>)
        .detail;
      if (
        d?.type &&
        (d.type !== "source_search" || d.resource_id !== channel.id)
      )
        return;
      if (!merged)
        merged = setTimeout(() => {
          merged = undefined;
          read();
        }, 250);
    };
    const timer = searchRunning(s) ? setInterval(read, 1500) : undefined;
    window.addEventListener(resourceEvent, notify);
    document.addEventListener("visibilitychange", read);
    return () => {
      clearInterval(timer);
      clearTimeout(merged);
      window.removeEventListener(resourceEvent, notify);
      document.removeEventListener("visibilitychange", read);
    };
  }, [channel.id, s, reloadSearch]);
  useEffect(() => {
    if (!pendingPage || pendingPage.id !== s?.id || searchRunning(s)) return;
    if (["paused", "cancelled"].includes(s.state)) {
      void Promise.resolve().then(() => {
        if (alive.current) setPendingPage(null);
      });
      return;
    }
    const controller = new AbortController();
    void api
      .page(s.id, pendingPage.page, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return;
        if (searchRunning(value.search)) {
          reloadSearch();
          return;
        }
        if (value.items.length) {
          update((c) => ({ ...c, page: pendingPage.page, listScroll: 0 }));
          reloadResults();
        }
        if (
          (!value.items.length || pendingPage.page === pendingPage.previous) &&
          s.loaded_count === pendingPage.loaded &&
          s.continuation === "end"
        )
          void message.info("已查完，没有更多候选");
        setPendingPage(null);
      })
      .catch((e) => {
        if (!controller.signal.aborted) {
          setError(e);
          setPendingPage(null);
        }
      });
    return () => controller.abort();
  }, [s, pendingPage, api, update, reloadResults, reloadSearch, message]);
  // Restore only a newly displayed identity, never a background read.
  const latestDraft = useRef(draft),
    restoredList = useRef("");
  useLayoutEffect(() => {
    latestDraft.current = draft;
  }, [draft]);
  useLayoutEffect(() => {
    const key = `${mobile ? "mobile" : "desktop"}:${channel.id}:${channel.page}`;
    if (results.data && listRef.current && restoredList.current !== key) {
      listRef.current.scrollTop =
        latestDraft.current[latestDraft.current.tab].listScroll;
      restoredList.current = key;
    }
  }, [channel.id, channel.page, results.data, mobile, listRef]);
  async function loadPage(page: number) {
    if (!s) return;
    await run(async () => {
      const signature = `load:${s.id}:${s.revision}:${page}`;
      const next = await api.load(s, page, keyFor(signature));
      if (!alive.current) return;
      keys.current.delete(signature);
      setPendingPage({
        id: s.id,
        page,
        previous: channel.page,
        loaded: s.loaded_count,
      });
      reloadSearch();
      // POST can be idempotently complete without a new worker.
      if (!searchRunning(next)) reloadResults();
    });
  }
  async function control(action: string) {
    if (!s) return;
    await run(async () => {
      const next = await api.control(s, action);
      if (!alive.current) return;
      if (action === "retry" || action === "resume")
        setPendingPage({
          id: s.id,
          page: next.active_page,
          previous: channel.page,
          loaded: s.loaded_count,
        });
      reloadSearch();
    });
  }
  async function recheck() {
    await run(async () => {
      const [editor, book, latest] = await Promise.all([
        importAPI.editor(batch, member.id),
        request<Book>(`/books/${member.id}`),
        s ? api.get(s.id) : Promise.resolve(null),
      ]);
      const current = {
        member: editor.member_revision,
        edition: book.revision,
      };
      const result =
        latest && attempt
          ? await api.result(latest.id, attempt.result.id)
          : null;
      if (!alive.current) return;
      setVersions(current);
      if (latest && result)
        update((c) => ({
          ...c,
          attempt: { search: latest, result, ...current },
        }));
      local.reload();
      reloadSearch();
      reloadResults();
      setConflict(false);
      setError(null);
    });
  }
  const rows = results.data?.items ?? [];
  const coverPaths = rows
    .map((row) => {
      const r = attempt?.result.id === row.id ? attempt.result : row;
      return s && r.state === "ready" ? api.cover(s, r) : "";
    })
    .filter(Boolean);
  const covers = useSourceCovers(`${channel.id}:${channel.page}`, coverPaths);
  const resultRevision = attempt
    ? Math.max(
        attempt.result.revision,
        rows.find((r) => r.id === attempt.result.id)?.revision ?? 0,
      )
    : undefined;
  const currentSearch =
    results.data && results.data.search.revision > (s?.revision ?? 0)
      ? results.data.search
      : s;
  const stale = sourceChoiceStale(attempt, currentSearch, resultRevision);
  const needsReview = conflict || !!attempt?.requiresReview || stale;
  const previousQuery = !!draft.latestID && !!s && draft.latestID !== s.id;
  const canUse = (r: SourceResult) =>
    r.state === "ready" &&
    !needsReview &&
    !previousQuery &&
    !disabled &&
    !busy &&
    !searchRunning(s) &&
    !!results.data &&
    results.data.search.revision >= (s?.revision ?? 0);
  async function select(result: SourceResult) {
    if (!canUse(result) || lock.current || !results.data) return;
    const frozen: SourceAttempt =
      attempt?.result.id === result.id
        ? attempt
        : { search: results.data.search, result, ...versions };
    update((c) => ({ ...c, attempt: frozen }));
    setSelecting(result.id);
    await run(async () => {
      await api.select(frozen.search, frozen.result, {
        member: frozen.member,
        edition: frozen.edition,
      });
      if (alive.current) onSelected();
    }, result.id);
    if (alive.current) setSelecting("");
  }
  async function copy(value: string, label: string) {
    try {
      await navigator.clipboard.writeText(value);
      void message.success(`${label}已复制`);
    } catch {
      void message.error("复制失败，请检查浏览器剪贴板权限后重试");
    }
  }
  function rememberScroll(event: React.UIEvent<HTMLDivElement>) {
    if (
      event.target !== event.currentTarget ||
      event.currentTarget !== listRef.current ||
      !results.data ||
      restoredList.current !==
        `${mobile ? "mobile" : "desktop"}:${channel.id}:${channel.page}`
    )
      return;
    const top = event.currentTarget.scrollTop;
    update((c) => ({ ...c, listScroll: top }));
  }
  const maxPage = Math.max(1, Math.ceil((s?.loaded_count ?? 0) / 10));
  const complete = s?.continuation === "end",
    limited = s?.continuation === "limited" || (s?.loaded_count ?? 0) >= 200;
  const failed = rows.filter((r) => r.state === "failed").length;
  return (
    <Modal
      open
      centered
      title="来源比较"
      width={1200}
      className="import-source-modal"
      wrapClassName="import-source-modal-wrap"
      onCancel={onClose}
      closable={!busy}
      keyboard={!busy}
      mask={{ closable: !busy }}
      footer={
        <div className="source-footer">
          <div className="source-paging">
            {channel.id && (
              <>
                <Pagination
                  simple
                  current={channel.page}
                  pageSize={10}
                  total={maxPage * 10}
                  showSizeChanger={false}
                  disabled={busy || searchRunning(s) || !!pendingPage}
                  onChange={(p) => void loadPage(p)}
                />
                {!complete && !limited && (
                  <Button
                    disabled={
                      busy || searchRunning(s) || !!pendingPage || disabled
                    }
                    onClick={() =>
                      void loadPage(moreSearchPage(channel.page, rows.length))
                    }
                  >
                    查询更多
                  </Button>
                )}
                {!s?.error_code && s?.state === "failed" && (
                  <Button disabled={busy} onClick={() => void control("retry")}>
                    重试查询
                  </Button>
                )}
                {!s && !!error && (
                  <Button
                    onClick={() =>
                      void create(mode, mode === "title" ? draft.q : "")
                    }
                    disabled={busy || conflict || disabled}
                  >
                    重新查询
                  </Button>
                )}
              </>
            )}
          </div>
          <Button onClick={onClose} disabled={busy}>
            返回审核
          </Button>
        </div>
      }
    >
      <div
        className="source-workspace"
        ref={workspaceRef}
        onScroll={rememberScroll}
      >
        <section
          className="source-local"
          aria-label="本地对照"
          tabIndex={mobile ? undefined : 0}
        >
          <h3>本地对照</h3>
          <div className="source-local-content">
            <div className="source-local-cover">
              <Cover
                id={member.id}
                content={member.content_revision}
                revision={member.cover_revision}
                preview={false}
              />
            </div>
            <div className="source-local-text">
              <dl className="source-titles">
                <div>
                  <dt>完整文件名</dt>
                  <dd>{member.filename}</dd>
                </div>
                <div>
                  <dt>现有标题</dt>
                  <dd>{originalTitle(local.data || member)}</dd>
                </div>
              </dl>
              <div className="source-local-facts">
                <span>页数：{local.data?.page_count ?? "未知"}</span>
              </div>
            </div>
          </div>
          <ErrorNotice error={local.error} />
        </section>
        <div
          className="source-candidate-scroll"
          ref={candidateScrollRef}
          onScroll={rememberScroll}
        >
          <div className="source-controls">
            <Tabs
              activeKey={mode}
              onChange={(v) => {
                setDraft((d) => ({ ...d, tab: v as SearchMode }));
                setError(null);
                setConflict(false);
                setPendingPage(null);
              }}
              items={[
                { key: "matches", label: "匹配结果", disabled: busy },
                { key: "title", label: "按标题查找", disabled: busy },
              ]}
            />
            <Tooltip title="粘贴画廊网址重新匹配" trigger={["hover", "focus"]}>
              <Button
                type="text"
                aria-label="粘贴画廊网址重新匹配"
                icon={<SnippetsOutlined aria-hidden="true" />}
                disabled={busy || disabled}
                onClick={onPaste}
              />
            </Tooltip>
          </div>
          {mode === "title" && (
            <Form
              className="source-search-form"
              onFinish={() => void create("title", draft.q.trim())}
            >
              <label htmlFor={`source-query-${member.id}`}>搜索标题</label>
              <div>
                <Input
                  id={`source-query-${member.id}`}
                  name="source_title"
                  autoComplete="off"
                  maxLength={256}
                  value={draft.q}
                  disabled={busy || disabled}
                  onChange={(e) =>
                    setDraft((d) => ({ ...d, q: e.target.value }))
                  }
                  placeholder="输入作品标题…"
                />
                <Button
                  htmlType="submit"
                  icon={<SearchOutlined aria-hidden="true" />}
                  loading={busy}
                  disabled={disabled || !draft.q.trim()}
                >
                  搜索
                </Button>
              </div>
            </Form>
          )}
          <div className="source-feedback" aria-live="polite">
            <ErrorNotice error={error || search.error || results.error} />
            {needsReview && !rows.some((r) => r.id === attempt?.result.id) && (
              <Alert
                type="warning"
                showIcon
                title="资料或查询版本已变化"
                description="当前候选和输入已保留，请核对后再使用。"
                action={
                  <Button disabled={busy} onClick={() => void recheck()}>
                    核对当前版本
                  </Button>
                }
              />
            )}
            {!!s &&
              (s.error_code || s.state === "failed") &&
              !(failed > 0 && rows.some((r) => r.state === "ready")) && (
                <Alert
                  type={s.state === "paused" ? "warning" : "error"}
                  showIcon
                  title={searchError(s.error_code)}
                  action={
                    <Button
                      disabled={busy || disabled}
                      onClick={() =>
                        void control(s.state === "paused" ? "resume" : "retry")
                      }
                    >
                      {s.state === "paused" ? "恢复查询" : "重试查询"}
                    </Button>
                  }
                />
              )}
            {previousQuery && (
              <Alert
                type="info"
                showIcon
                title="这是先前查询的结果"
                description="重新查询后才能使用此处的候选。"
              />
            )}
            {s && (
              <div className="source-progress">
                <span>
                  {s.mode === "title" ? `结果来自“${s.q}” · ` : ""}
                  {searchRunning(s)
                    ? "正在查询… · "
                    : s.state === "paused"
                      ? "查询已暂停 · "
                      : s.state === "cancelled"
                        ? "查询已取消 · "
                        : ""}
                  已加载 {s.loaded_count} 个候选 ·{" "}
                  {complete
                    ? "已查完"
                    : limited
                      ? "已达资源上限"
                      : s.continuation === "more"
                        ? "还有结果"
                        : "结果范围尚未确认"}
                  {pendingPage ? ` · 正在加载第${pendingPage.page}页…` : ""}
                </span>
                <Space>
                  {s.state === "paused" && !s.error_code && (
                    <Button
                      size="small"
                      disabled={busy || disabled}
                      onClick={() => void control("resume")}
                    >
                      恢复查询
                    </Button>
                  )}
                  {!searchRunning(s) && (
                    <Button
                      size="small"
                      disabled={busy || disabled || conflict}
                      onClick={() =>
                        void create(
                          mode,
                          mode === "title" ? draft.q.trim() : "",
                        )
                      }
                    >
                      重新查询
                    </Button>
                  )}
                  {searchRunning(s) && (
                    <Button
                      size="small"
                      onClick={() => void control("pause")}
                      disabled={busy || disabled}
                    >
                      暂停
                    </Button>
                  )}
                  {searchRunning(s) && (
                    <Button
                      size="small"
                      onClick={() => void control("cancel")}
                      disabled={busy || disabled}
                    >
                      取消查询
                    </Button>
                  )}
                </Space>
              </div>
            )}
            {!!failed && (
              <div className="source-progress">
                <span>本页 {failed} 个候选获取失败，其他候选仍可比较。</span>
                <Button
                  size="small"
                  disabled={busy || searchRunning(s) || disabled}
                  onClick={() => void loadPage(channel.page)}
                >
                  重试本页失败候选
                </Button>
              </div>
            )}
          </div>

          <section className="source-candidates" aria-label="候选列表">
            <div className="source-result-list">
              {rows.map((row) => {
                const r = attempt?.result.id === row.id ? attempt.result : row;
                const expanded = !!channel.expanded[r.id];
                return (
                  <article
                    key={r.id}
                    className="source-result"
                    data-state={r.state}
                    aria-label={`候选 ${originalTitle({ ...row, ...row.fields, title: row.fields?.title ?? row.title })}`}
                    data-testid={`source-result-${r.id}`}
                  >
                    <div className="source-primary">
                      <div className="source-candidate-cover">
                        {s && r.state === "ready" ? (
                          <>
                            {covers.images[api.cover(s, r)]?.url ? (
                              <img
                                src={covers.images[api.cover(s, r)].url}
                                width={100}
                                height={140}
                                alt="候选封面"
                                onError={() => covers.fail(api.cover(s, r))}
                              />
                            ) : covers.images[api.cover(s, r)]?.error ? (
                              <div className="source-cover-error" role="status">
                                <PictureOutlined aria-hidden="true" />
                                <span>
                                  {covers.images[api.cover(s, r)].error}
                                </span>
                                <Button
                                  type="text"
                                  size="small"
                                  disabled={busy || disabled}
                                  onClick={() => covers.retry(api.cover(s, r))}
                                >
                                  重试封面
                                </Button>
                              </div>
                            ) : (
                              <Spin size="small" aria-label="加载候选封面" />
                            )}
                          </>
                        ) : (
                          <PictureOutlined aria-hidden="true" />
                        )}
                      </div>
                      <div className="source-primary-text">
                        {r.state === "ready" ? (
                          <SourceFields
                            result={r}
                            filename={member.filename}
                            disabled={busy || disabled}
                            onCopy={(value) => void copy(value, "标题")}
                          />
                        ) : (
                          <>
                            <h3>
                              {originalTitle({
                                ...r,
                                ...r.fields,
                                title: r.fields?.title ?? r.title,
                              })}
                            </h3>
                            {r.state === "failed" ? (
                              <Alert
                                type="error"
                                showIcon
                                title="该候选不可用"
                                description={searchError(r.error_code)}
                                action={
                                  <Button
                                    disabled={
                                      busy || searchRunning(s) || disabled
                                    }
                                    onClick={() => void loadPage(channel.page)}
                                  >
                                    重试此候选
                                  </Button>
                                }
                              />
                            ) : (
                              <Space>
                                <Spin size="small" />
                                <span>准备候选资料中…</span>
                              </Space>
                            )}
                          </>
                        )}
                      </div>
                    </div>
                    {(selectionError?.id === r.id ||
                      (needsReview && attempt?.result.id === r.id)) && (
                      <div className="source-card-feedback" aria-live="polite">
                        <ErrorNotice
                          error={
                            selectionError?.id === r.id
                              ? selectionError.error
                              : null
                          }
                        />
                        {needsReview && attempt?.result.id === r.id && (
                          <Alert
                            type="warning"
                            showIcon
                            title="资料或查询版本已变化"
                            description="当前候选和输入已保留，请核对后再使用。"
                            action={
                              <Button
                                disabled={busy}
                                onClick={() => void recheck()}
                              >
                                核对当前版本
                              </Button>
                            }
                          />
                        )}
                      </div>
                    )}
                    <div className="source-card-actions">
                      <Button
                        type="text"
                        icon={
                          expanded ? (
                            <UpOutlined aria-hidden="true" />
                          ) : (
                            <DownOutlined aria-hidden="true" />
                          )
                        }
                        disabled={busy || disabled || r.state !== "ready"}
                        aria-expanded={expanded}
                        aria-controls={`source-details-${r.id}`}
                        onClick={() =>
                          update((c) => {
                            const next = { ...c.expanded };
                            if (next[r.id]) delete next[r.id];
                            else next[r.id] = true;
                            return { ...c, expanded: next };
                          })
                        }
                      >
                        {expanded ? "收起资料" : "更多资料"}
                      </Button>
                      <Button
                        type="primary"
                        disabled={!canUse(r)}
                        loading={selecting === r.id}
                        onClick={() => void select(r)}
                      >
                        使用此候选
                      </Button>
                    </div>
                    {expanded && r.state === "ready" && (
                      <section
                        className="source-extra"
                        id={`source-details-${r.id}`}
                        aria-label="更多候选资料"
                      >
                        <SourceDetails result={r} />
                        <div className="source-links">
                          <Tooltip
                            title="复制完整画廊网址"
                            trigger={["hover", "focus"]}
                          >
                            <Button
                              className="source-url"
                              aria-label="复制完整画廊网址"
                              disabled={busy || disabled}
                              onClick={() => void copy(r.url, "网址")}
                            >
                              {new URL(r.url).pathname.replace(/^\/|\/$/g, "")}
                            </Button>
                          </Tooltip>
                          <Tooltip
                            title="打开画廊"
                            trigger={["hover", "focus"]}
                          >
                            <Button
                              type="text"
                              aria-label="打开画廊"
                              disabled={busy || disabled}
                              href={r.url}
                              target="_blank"
                              rel="noreferrer"
                              icon={<ExportOutlined aria-hidden="true" />}
                            />
                          </Tooltip>
                        </div>
                      </section>
                    )}
                  </article>
                );
              })}
              {!rows.length && (busy || searchRunning(s) || results.loading) ? (
                <div className="source-empty">
                  <Spin aria-label="加载候选" />
                </div>
              ) : (
                !rows.length && (
                  <Empty
                    description={
                      !s
                        ? mode === "title"
                          ? "输入标题，点击搜索查找来源"
                          : "尚未加载匹配结果"
                        : s.error_code
                          ? "查询尚未完成"
                          : complete
                            ? "没有找到候选，可改用标题查找"
                            : "没有已加载的候选"
                    }
                  />
                )
              )}
            </div>
            {!channel.id && mode === "matches" && !!error && (
              <Button
                disabled={busy || conflict || disabled}
                onClick={() => void create("matches", "")}
              >
                重新查找匹配
              </Button>
            )}
          </section>
        </div>
      </div>
    </Modal>
  );
}
function SourceFields({
  result: r,
  filename,
  disabled,
  onCopy,
}: {
  result: SourceResult;
  filename: string;
  disabled: boolean;
  onCopy: (value: string) => void;
}) {
  const f = r.fields;
  if (!f) return <Empty description="未提供完整资料" />;
  const published = f.published_at ? new Date(f.published_at) : null;
  const date =
    published && !Number.isNaN(published.valueOf())
      ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(
          published,
        )
      : f.published_at || "未知";
  return (
    <>
      <dl className="source-titles">
        {[
          ["站点标题", f.title],
          ["原文标题", f.title_original],
        ].map(([label, title]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>
              {title ? (
                <button
                  type="button"
                  className="source-title-copy"
                  aria-label={`复制完整${label}`}
                  disabled={disabled}
                  onClick={() => onCopy(title)}
                >
                  <Overlap filename={filename} title={title} />
                </button>
              ) : (
                "未提供"
              )}
            </dd>
          </div>
        ))}
      </dl>
      <dl className="source-facts source-primary-facts">
        <div>
          <dt>页数</dt>
          <dd>{r.page_count ?? "未知"}</dd>
        </div>
        <div>
          <dt>发布时间</dt>
          <dd>{date}</dd>
        </div>
      </dl>
    </>
  );
}
function SourceDetails({ result: r }: { result: SourceResult }) {
  const f = r.fields;
  if (!f) return null;
  const tags = sourceResultTags(r);
  const groups = [...new Set(tags.map((t) => t.namespace))].sort((a, b) => {
    const ai = namespaces.findIndex(([n]) => n === a),
      bi = namespaces.findIndex(([n]) => n === b);
    return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi);
  });
  return (
    <>
      <dl className="source-facts">
        <div>
          <dt>分类</dt>
          <dd>{category(f.category)}</dd>
        </div>
        <div>
          <dt>语言</dt>
          <dd>{language(f.language)}</dd>
        </div>
        <div>
          <dt>评分</dt>
          <dd>{f.rating ?? "未知"}</dd>
        </div>
      </dl>
      {f.description && <p className="source-description">{f.description}</p>}
      {!!tags.length && (
        <div className="source-tags">
          {groups.map((ns) => (
            <div className="source-tag-group" key={ns}>
              <h4>
                {namespaces.find(([n]) => n === ns)?.[1] ?? ns}{" "}
                <small>{ns}</small>
              </h4>
              <Space size={[4, 8]} wrap>
                {tags
                  .filter((t) => t.namespace === ns)
                  .map((t) => (
                    <Tag key={`${t.namespace}:${t.name}`}>
                      {t.translation || t.name}
                      {t.translation && t.translation !== t.name && (
                        <span className="source-tag-original"> {t.name}</span>
                      )}
                    </Tag>
                  ))}
              </Space>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
