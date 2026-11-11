/** §2.1/§3.3/§3.6: five direct categories, a single compact filter row, then
 * identity cards. One centered content layer at a time; existing Ant tokens,
 * 14/12px typography and 8/16/24px rhythm. Work cards remain server-paginated. */
import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  AutoComplete,
  Button,
  Empty,
  Input,
  Modal,
  Pagination,
  Segmented,
  Select,
  Skeleton,
  Tag,
  Tooltip,
  theme,
} from "antd";
import { useLocation, useNavigate } from "react-router";
import { ArrowLeftOutlined } from "@ant-design/icons";
import { BusinessReadContext } from "../../shared/readBoundary";
import { TagNavigation } from "./TagNavigation";
import {
  backTag,
  currentTrail,
  normalizeTagDetailSearch,
  readTagTrail,
  resolveTagTrail,
  startTagTrail,
  tagDetailSearch,
  tagPath,
  trailState,
  visitTag,
} from "./tagTrail";
import { resourceEvent } from "../../shared/events";
import { subscribeWrites, writesPending } from "../../shared/api";
import { tagCatalogPath } from "../../shared/tagCatalog";
import type {
  CatalogDetail,
  CatalogTag,
  TagCategory,
  TagWork,
} from "../../shared/tagCatalog";
import { useCurrentLibrary } from "../CurrentLibrary";
import { FormLock, RefreshButton } from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
import { namespaces } from "../tagInput";
import {
  tagDirectoryInput,
  normalizeTagDirectoryURL,
  tagCategories,
  tagName,
  tagWorkspaceURL,
} from "./tagCatalogFlow";
import { TagDetails } from "./TagDetails";
import { TagComicDetails } from "./TagWorkView";
import { TagCard } from "./TagCard";
import { TagFollowButton, TagFollowProvider } from "./TagFollow";
import { TagReadError } from "./tagViews";
import { useTagPage } from "./useTagPage";
import "./tagCatalog.css";
import { useTagWorkspace } from "./useTagWorkspace";
type SavedLayer = { element: HTMLElement | null; scroll: number };
type TagSession = {
  id: string;
  library: string;
  search: string;
  data: CatalogDetail | null;
  epoch: number;
};
export function TagsPage() {
  const scope = useCurrentLibrary();
  return (
    <TagFollowProvider key={scope.id} library={scope.id}>
      <TagWorkspace />
    </TagFollowProvider>
  );
}
function TagWorkspace() {
  const scope = useCurrentLibrary(),
    location = useLocation(),
    navigate = useNavigate(),
    { token } = theme.useToken(),
    readable = useContext(BusinessReadContext);
  const normalized = normalizeTagDirectoryURL(location.search);
  const pageRef = useRef<HTMLElement>(null),
    scroller = useRef<HTMLDivElement>(null);
  const params = new URLSearchParams(location.search),
    input = tagDirectoryInput(location.search, scope.id),
    id = params.get("tag") || "",
    work = id ? params.get("work") || "" : "",
    book = id ? params.get("book") || "" : "";
  useTagWorkspace(pageRef, scroller, JSON.stringify(input));
  const busy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const [refreshEpoch, setRefreshEpoch] = useState(0),
    [overlay, setOverlay] = useState(false),
    [selected, setSelected] = useState<TagWork | null>(null);
  const focus = useRef<{
    tags: Record<string, SavedLayer>;
    directory?: HTMLElement;
  }>({ tags: {} });
  const list = useTagPage<CatalogTag>("/tag-catalog", input, !normalized),
    detail = useRetainedRemote<CatalogDetail>(
      tagCatalogPath(`/tag-catalog/${encodeURIComponent(id)}`, {
        library_id: scope.id,
      }),
      !!id && !work && !book && !overlay,
    );
  const [sessions, setSessions] = useState<TagSession[]>([]);
  const activeDetail =
    detail.data ||
    sessions.find((s) => s.library === scope.id && s.id === id)?.data ||
    null;
  const trail = useMemo(
    () =>
      resolveTagTrail(location.search, location.state, scope.id, location.key),
    [location.search, location.state, scope.id, location.key],
  );
  // Canonicalize old links and hidden character filters before their read hook
  // can run. State is committed by the router, so a cancelled guard changes none
  // of the trail or retained forms.
  let canonical = normalized || `/admin/tags${location.search}`;
  let nextState = location.state;
  if (trail) {
    const source = readTagTrail(location.state, scope.id, id)
      ? canonical.slice("/admin/tags".length)
      : params.has("parent_tag")
        ? trail.nodes.at(-1)!.search
        : canonical.slice("/admin/tags".length);
    const clean = normalizeTagDetailSearch(source, activeDetail?.namespace);
    canonical = tagPath(clean);
    nextState = trailState(currentTrail(trail, clean));
  }
  const canonicalState = JSON.stringify(nextState ?? null),
    locationState = JSON.stringify(location.state ?? null);
  useEffect(() => {
    if (
      canonical !== `/admin/tags${location.search}` ||
      canonicalState !== locationState
    )
      void navigate(canonical, {
        replace: true,
        preventScrollReset: true,
        state: JSON.parse(canonicalState),
      });
  }, [canonical, canonicalState, locationState, location.search, navigate]);
  useEffect(() => {
    let alive = true;
    void Promise.resolve().then(() => {
      if (!alive) return;
      const kept = new Set(
        trail?.nodes.map((node) => `${scope.id}:${node.id}`) || [],
      );
      for (const key of Object.keys(focus.current.tags))
        if (!kept.has(key)) delete focus.current.tags[key];
      setSessions((old) => {
        if (!trail) return old.length ? [] : old;
        return trail.nodes.map((node) => {
          const previous = old.find(
            (s) => s.library === scope.id && s.id === node.id,
          );
          return {
            id: node.id,
            library: scope.id,
            search: node.search,
            data:
              node.id === id
                ? detail.data || previous?.data || null
                : previous?.data || null,
            epoch: node.id === id ? refreshEpoch : previous?.epoch || 0,
          };
        });
      });
    });
    return () => {
      alive = false;
    };
  }, [trail, id, scope.id, detail.data, refreshEpoch]);
  const navigation = {
    related(target: string) {
      const next = trail && visitTag(trail, location.search, target);
      return {
        to: next
          ? tagPath(next.nodes.at(-1)!.search)
          : location.pathname + location.search,
        state: trailState(next),
        replace: true,
        same: target === id,
      };
    },
    stateFor(path: string) {
      const query = new URL(path, "http://tag-workspace.invalid").search;
      return trailState(trail && currentTrail(trail, query));
    },
  };
  const [search, setSearch] = useState<string | null>(null),
    [namespace, setNamespace] = useState<string | null>(null);
  const layerKey = `${scope.id}:${id}`;
  const remember = useCallback(() => {
    const e =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    focus.current.tags[layerKey] = {
      element: e,
      scroll: e?.closest(".ant-modal-body")?.scrollTop || 0,
    };
  }, [layerKey]);
  const restore = useCallback(() => {
    const saved = focus.current.tags[layerKey],
      body = document.querySelector(".tag-detail-modal .ant-modal-body");
    if (body) body.scrollTop = saved?.scroll || 0;
    const target =
      saved?.element?.isConnected && !saved.element.closest("[hidden]")
        ? saved.element
        : document.querySelector<HTMLElement>(
            '.tag-detail-modal button[aria-label="关闭标签详情"]',
          );
    target?.focus({ preventScroll: true });
  }, [layerKey]);
  const update = (values: Record<string, string | number | null>) => {
    if ((values.work || values.book) && !work && !book && !overlay) remember();
    const target =
      values.tag === null && trail
        ? tagPath(trail.directory)
        : tagWorkspaceURL(location.search, values);
    void navigate(target, {
      preventScrollReset: true,
      replace:
        values.tag === null ||
        !(
          "work" in values ||
          "book" in values ||
          "details" in values ||
          "preview" in values
        ),
      state: values.tag === null ? null : navigation.stateFor(target),
    });
  };
  const onOverlay = setOverlay;
  const reloadList = list.reload,
    restartList = list.restart,
    reloadDetail = detail.reload;
  const followFocus = useRef<{
    element: HTMLElement;
    index: number;
    library: string;
  } | null>(null);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const listener = (event: Event) => {
      const change = (
        event as CustomEvent<{ type?: string; focusTarget?: HTMLElement }>
      ).detail;
      const following = change?.type === "tag_follow";
      const focused =
        change?.focusTarget instanceof HTMLElement
          ? change.focusTarget
          : document.activeElement;
      if (
        following &&
        focused instanceof HTMLElement &&
        scroller.current?.contains(focused)
      ) {
        const buttons = Array.from(
          scroller.current.querySelectorAll<HTMLElement>("[data-follow-tag]"),
        );
        followFocus.current = {
          element: focused,
          index: Math.max(0, buttons.indexOf(focused)),
          library: scope.id,
        };
      }
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (following) restartList();
        else reloadList();
        reloadDetail();
      }, 200);
    };
    window.addEventListener(resourceEvent, listener);
    return () => {
      clearTimeout(timer);
      window.removeEventListener(resourceEvent, listener);
    };
  }, [reloadList, restartList, reloadDetail, scope.id]);
  useEffect(() => {
    if (!list.data || list.pending) return;
    const lastPage = Math.max(1, Math.ceil(list.data.total / 50));
    if ((input.page || 1) > lastPage && input.followed_only) {
      if (id) return; // An open detail owns its drafts even after leaving the list.
      void navigate(tagWorkspaceURL(location.search, { page: lastPage }), {
        replace: true,
        preventScrollReset: true,
      });
      return;
    }
    const saved = followFocus.current;
    if (!saved || saved.library !== scope.id || id || saved.element.isConnected)
      return;
    followFocus.current = null;
    if (document.activeElement !== document.body) return;
    const buttons =
      scroller.current?.querySelectorAll<HTMLElement>("[data-follow-tag]");
    const target =
      buttons?.[Math.min(saved.index, buttons.length - 1)] ||
      pageRef.current?.querySelector<HTMLElement>(
        '[aria-label="标签关注范围"] input, [aria-label="标签关注范围"] button',
      );
    target?.focus({ preventScroll: true });
  }, [
    list.data,
    list.pending,
    scope.id,
    id,
    input.page,
    input.followed_only,
    location.search,
    navigate,
  ]);
  const onWorkRows = useCallback(
    (items: TagWork[]) => {
      setSelected((previous) => {
        const match =
          previous &&
          items.find(
            (v) => v.id === previous.id && v.library_id === previous.library_id,
          );
        return match && JSON.stringify(previous) !== JSON.stringify(match)
          ? match
          : previous;
      });
    },
    [setSelected],
  );
  const changed = () => {
    list.restart();
    detail.reload();
    setRefreshEpoch((v) => v + 1);
  };
  const tagActive = !!id && !work && !book && !overlay;
  useEffect(() => {
    if (!tagActive || !readable) return;
    const frame = requestAnimationFrame(restore);
    return () => cancelAnimationFrame(frame);
  }, [tagActive, readable, restore]);
  function returnTag() {
    const previous = trail && backTag(trail);
    if (previous)
      void navigate(tagPath(previous.nodes.at(-1)!.search), {
        replace: true,
        preventScrollReset: true,
        state: trailState(previous),
      });
  }
  const openSessions = (trail?.nodes || []).map((node) => ({
    id: node.id,
    library: scope.id,
    search: node.id === id ? location.search : node.search,
    data:
      node.id === id
        ? activeDetail
        : sessions.find((s) => s.library === scope.id && s.id === node.id)
            ?.data || null,
    epoch:
      node.id === id
        ? refreshEpoch
        : sessions.find((s) => s.library === scope.id && s.id === node.id)
            ?.epoch || 0,
  }));

  return (
    <FormLock busy={busy}>
      <section
        className="tag-page"
        ref={pageRef}
        style={
          {
            "--tag-bg": token.colorBgContainer,
            "--tag-border": token.colorBorderSecondary,
            "--tag-muted": token.colorTextSecondary,
            "--tag-selected": token.controlItemBgActive,
            "--tag-hover": token.colorBgTextHover,
          } as React.CSSProperties
        }
      >
        <h1 className="sr-only">标签</h1>
        <div className="tag-category-bar">
          <Segmented
            className="tag-desktop-category"
            aria-label="标签分类"
            value={input.category}
            options={Object.entries(tagCategories).map(([value, label]) => ({
              value,
              label,
            }))}
            onChange={(category) => {
              setSearch(null);
              setNamespace(null);
              update({ category, namespace: null });
            }}
          />
          <Select
            className="tag-mobile-category"
            aria-label="标签分类"
            value={input.category}
            options={Object.entries(tagCategories).map(([value, label]) => ({
              value,
              label,
            }))}
            onChange={(category: TagCategory) => {
              setSearch(null);
              setNamespace(null);
              update({ category, namespace: null });
            }}
          />
        </div>
        <section className="tag-directory" aria-label="标签目录">
          <div className="tag-filters">
            <Segmented
              aria-label="标签关注范围"
              value={input.followed_only ? "followed" : "all"}
              options={[
                { value: "all", label: "全部" },
                { value: "followed", label: "已关注" },
              ]}
              onChange={(value) =>
                update({
                  followed_only: value === "followed" ? "true" : null,
                })
              }
            />
            <Input.Search
              aria-label="搜索标签原文或译名"
              value={search ?? input.q}
              onChange={(e) => setSearch(e.target.value)}
              onSearch={(q) => update({ q })}
              placeholder="搜索原文或中文译名…"
              allowClear
              maxLength={512}
            />
            {input.category === "content" && (
              <AutoComplete
                options={namespaces
                  .filter(
                    ([ns]) =>
                      !["parody", "character", "artist", "group"].includes(ns),
                  )
                  .map(([value, label]) => ({
                    value,
                    label: `${label} · ${value}`,
                  }))}
                value={namespace ?? input.namespace}
                onChange={setNamespace}
                onSelect={(namespace) => update({ namespace })}
              >
                <Input.Search
                  aria-label="内容命名空间"
                  placeholder="命名空间，如 language…"
                  allowClear
                  maxLength={64}
                  onSearch={(namespace) => update({ namespace })}
                />
              </AutoComplete>
            )}
            <Select
              aria-label="标签排序"
              value={`${input.sort}:${input.order}`}
              options={[
                { value: "name:asc", label: "名称 · 升序" },
                { value: "name:desc", label: "名称 · 降序" },
                { value: "usage:desc", label: "使用数 · 降序" },
                { value: "usage:asc", label: "使用数 · 升序" },
              ]}
              onChange={(v) => {
                const [sort, order] = v.split(":");
                update({ sort, order });
              }}
            />
            <RefreshButton reload={changed} />
          </div>
          <div
            className="tag-directory-scroll"
            ref={scroller}
            aria-label="标签卡片区域"
          >
            <TagReadError error={list.error} restart={list.restart} />
            <div
              className="tag-catalog-grid"
              data-category={input.category}
              aria-busy={list.pending}
            >
              {list.pending && !list.data
                ? Array.from({ length: 12 }, (_, i) => (
                    <div className="tag-catalog-card tag-compact-card" key={i}>
                      <Skeleton active title paragraph={{ rows: 1 }} />
                    </div>
                  ))
                : list.data?.items.map((tag) => (
                    <TagCard
                      key={tag.id}
                      tag={tag}
                      to={tagPath(tagDetailSearch(location.search, tag.id))}
                      state={trailState(
                        startTagTrail(
                          location.search,
                          scope.id,
                          tag.id,
                          `${location.key}:${tag.id}`,
                        ),
                      )}
                      onOpen={(element) => {
                        focus.current.directory = element;
                      }}
                    />
                  ))}
            </div>
            {list.data?.items.length === 0 && (
              <div className="tag-catalog-empty">
                <Empty description="此范围暂无标签" />
              </div>
            )}
          </div>
          <div
            className="tag-pager tag-directory-footer"
            aria-label="标签目录分页"
          >
            <span>{list.data?.total ?? "—"} 个标签</span>
            <Pagination
              size="small"
              showLessItems
              current={input.page}
              total={list.data?.total || 0}
              pageSize={50}
              showSizeChanger={false}
              onChange={(page) => update({ page })}
            />
          </div>
        </section>
        <TagNavigation.Provider value={navigation}>
          <Modal
            getContainer={false}
            open={tagActive}
            forceRender
            centered
            aria-label="标签详情"
            width={1120}
            className="tag-content-modal tag-detail-modal"
            wrapClassName="tag-content-wrap"
            footer={null}
            transitionName=""
            maskTransitionName=""
            focusable={{ focusTriggerAfterClose: false }}
            closable={{ disabled: busy, "aria-label": "关闭标签详情" }}
            keyboard={!busy}
            mask={{ closable: !busy }}
            onCancel={() => !busy && update({ tag: null })}
            afterOpenChange={(open) => {
              if (open) restore();
              else if (!work && !book && !overlay) {
                const target = focus.current.directory?.isConnected
                  ? focus.current.directory
                  : document.querySelector<HTMLElement>(
                      '.tag-directory input[aria-label="搜索标签原文或译名"]',
                    );
                target?.focus({ preventScroll: true });
              }
            }}
            title={
              <div className="tag-detail-heading">
                {trail && trail.nodes.length > 1 && (
                  <Tooltip title="返回上一个标签" trigger={["hover", "focus"]}>
                    <Button
                      type="text"
                      aria-label="返回上一个标签"
                      onClick={returnTag}
                      icon={<ArrowLeftOutlined aria-hidden />}
                    />
                  </Tooltip>
                )}
                <h2>{activeDetail ? tagName(activeDetail) : "标签详情"}</h2>
                {activeDetail && (
                  <>
                    <Tag className="tag-prefix">
                      {activeDetail.namespace}:{activeDetail.name}
                    </Tag>
                    <TagFollowButton tag={activeDetail} detail />
                    <span className="tag-heading-count">
                      {activeDetail.work_count} 个作品
                    </span>
                    {activeDetail.library_disabled && (
                      <Tag color="warning">已停用</Tag>
                    )}
                  </>
                )}
              </div>
            }
          >
            {openSessions.map((session) => (
              <div
                key={`${session.library}:${session.id}`}
                hidden={session.id !== id}
                className="tag-session"
              >
                <BusinessReadContext.Provider
                  value={readable && !work && !book && session.id === id}
                >
                  <TagDetails
                    id={session.id}
                    search={session.search}
                    library={scope.id}
                    detail={session.data}
                    error={session.id === id ? detail.error : null}
                    refresh={detail.reload}
                    onChanged={changed}
                    onRoute={update}
                    refreshEpoch={session.epoch}
                    active={tagActive && session.id === id}
                    imagesMounted={!work && !book && session.id === id}
                    onOverlay={onOverlay}
                    onWorkRows={onWorkRows}
                    onRemember={remember}
                    onSelectWork={(value) => {
                      remember();
                      setSelected(value);
                    }}
                  />
                </BusinessReadContext.Provider>
              </div>
            ))}
          </Modal>
          {(work || book) && (
            <TagComicDetails
              key={`${scope.id}:${work || book}`}
              workID={work}
              bookID={book}
              tag={id}
              library={scope.id}
              seed={
                selected?.id === work && selected.library_id === scope.id
                  ? selected
                  : null
              }
              onRoute={update}
              onChanged={changed}
            />
          )}
        </TagNavigation.Provider>
      </section>
    </FormLock>
  );
}
