import { usePermissions, AdminOnly } from "../../shared/permissions";
import { ReadingBadge } from "./ReadingState";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import { useCallback, useMemo, useState } from "react";
import {
  Alert,
  App as AntApp,
  Tooltip,
  Button,
  Card,
  Checkbox,
  Dropdown,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import {
  ReloadOutlined,
  EditOutlined,
  EyeOutlined,
  StarOutlined,
  StarFilled,
  MoreOutlined,
  SaveOutlined,
  FilterOutlined,
  DownOutlined,
  BookOutlined,
} from "@ant-design/icons";
import { LibraryScopeControl, useCurrentLibrary } from "../CurrentLibrary";
import { useLocation, useNavigate } from "react-router";
import type { Book } from "../../shared/api";
import { metadata } from "../../shared/metadata";
import { organization } from "../../shared/organization";
import { defaults, search } from "../../shared/search";
import type { Conditions, Saved } from "../../shared/search";
import {
  PageTitle,
  ErrorNotice,
  StateTag,
  useRemote,
  useAction,
  ActionNotice,
} from "../common";
import type { AdminBooks } from "../types";
import { RecommendationLauncher } from "../versions/RecommendationLauncher";
import { BookEditor } from "./BookEditor";
import { OrganizeSelection } from "../OrganizeSelection";
import { MaintenanceDialog } from "../maintenance/Maintenance";
import { cleanBookTitle, copyBookTitle, preferredBookTitle } from "./bookTitle";
import { PropertyFilters } from "../PropertyFilters";
import {
  propertiesFromURL,
  savedConditions,
  writeSearchParams,
} from "../../shared/propertyFilters";
const base: Conditions = {
  ...defaults,
  properties: undefined,
  collapse: "none",
  page_size: 50,
  include_disabled: false,
  metadata_state: "",
};
function parse(value: string, review: boolean) {
  const q = new URLSearchParams(value);
  const next = { ...base, scope: "all", metadata_state: review ? "none" : "" };
  for (const key of Object.keys(base) as (keyof Conditions)[]) {
    if (q.has(key)) {
      if (key === "properties") continue;
      if (key === "page" || key === "page_size")
        next[key] = Math.max(1, Number(q.get(key)) || base[key]);
      else if (key === "include_disabled") next[key] = q.get(key) === "true";
      else if (key !== "snapshot_revision") next[key] = q.get(key)! as never;
    }
  }
  next.properties = propertiesFromURL(q);
  next.collapse = "none";
  next.page_size = [25, 50, 100].includes(next.page_size) ? next.page_size : 50;
  return next;
}
export function BooksPage({
  review,
  navigate,
}: {
  review: boolean;
  navigate: (path: string) => void;
}) {
  const permissions = usePermissions();
  const scope = useCurrentLibrary();
  const location = useLocation();
  const routerNavigate = useNavigate();
  const parsed = useMemo(() => {
    try {
      return { conditions: parse(location.search, review), error: null };
    } catch (error) {
      return { conditions: parse("", review), error };
    }
  }, [location.search, review]);
  const conditions = {
    ...parsed.conditions,
    library_id: scope.all ? "" : scope.id,
    include_disabled: scope.all
      ? parsed.conditions.include_disabled
      : !!scope.library?.disabled,
  };
  const [queryDraft, setQueryDraft] = useState({
    applied: conditions.query,
    value: conditions.query,
  });
  const query =
    queryDraft.applied === conditions.query
      ? queryDraft.value
      : conditions.query;
  const setQuery = (value: string) =>
    setQueryDraft({ applied: conditions.query, value });
  const [selection, setSelection] = useState<Record<string, Book>>({});
  const [organize, setOrganize] = useState(false);
  const [trash, setTrash] = useState<Book[]>([]);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [savedID, setSavedID] = useState("");
  const [moreFilters, setMoreFilters] = useState(false);
  const [filterReset, setFilterReset] = useState(0);
  const { message } = AntApp.useApp();
  const params = new URLSearchParams(location.search);
  const bookID = params.get("book") ?? "";
  const visibility = params.get("visibility") ?? "active";
  const includeDisabled = conditions.include_disabled ?? false;
  const [snapshot, setSnapshot] = useState<{
    key: string;
    value: string;
  } | null>(null);
  const conditionKey = JSON.stringify({
    ...conditions,
    page: 1,
    visibility,
    includeDisabled,
  });
  const body = {
    ...conditions,
    visibility,
    include_disabled: includeDisabled,
    ...(conditions.page > 1 && snapshot?.key === conditionKey
      ? { snapshot_revision: snapshot.value }
      : {}),
  };
  const loaded = useCallback(
    (value: AdminBooks) => {
      if (value.snapshot_revision)
        setSnapshot({ key: conditionKey, value: value.snapshot_revision });
    },
    [conditionKey, setSnapshot],
  );
  const listing = useRemote<AdminBooks>(
    "/admin/books/search",
    body,
    "POST",
    !parsed.error,
    loaded,
  );
  const saved = useRemote<{ items: Saved[] }>("/saved-searches");
  const action = useAction();
  function apply(
    change: Partial<Conditions> & {
      visibility?: string;
      include_disabled?: boolean;
    },
    replace = false,
  ) {
    const q = new URLSearchParams(location.search);
    q.delete("book");
    try {
      writeSearchParams(q, change);
    } catch (error) {
      action.setError(error);
      return false;
    }
    if (!("page" in change)) q.set("page", "1");
    void routerNavigate(
      { pathname: location.pathname, search: q.toString() },
      { replace, preventScrollReset: true },
    );
    return true;
  }
  function openBook(id: string) {
    const q = new URLSearchParams(location.search);
    q.set("book", id);
    q.delete("preview");
    void routerNavigate({ search: q.toString() }, { preventScrollReset: true });
  }
  const selected = Object.values(selection);
  async function batchMetadata() {
    await action.run(async () => {
      const job = await metadata.fetch(selected.map((b) => b.id));
      navigate(taskURL(job.job_id));
    }, "已启动元数据获取");
  }
  const tableColumns = [
    {
      title: "漫画",
      key: "title",
      render: (_: unknown, b: Book) => {
        const title = cleanBookTitle(preferredBookTitle(b));
        return (
          <div className="book-list-entry">
            <BookCover
              key={`${b.id}:${b.content_revision}:${b.cover_revision}`}
              book={b}
              onOpen={() => openBook(b.id)}
            />
            <div className="book-list-title">
              <Tooltip
                title={`点击复制标题：${title}`}
                trigger={["hover", "focus"]}
              >
                <Button
                  type="text"
                  className="table-title"
                  aria-label={`复制标题 ${title}`}
                  onClick={() => {
                    void copyBookTitle(
                      preferredBookTitle(b),
                      navigator.clipboard,
                    ).then((feedback) => message.open(feedback));
                  }}
                >
                  <span className="book-title-text">{title}</span>
                </Button>
              </Tooltip>
              <ReadingBadge state={b.reading_state} />
              {b.trashed_at && <Tag color="warning">回收站</Tag>}
              {b.library_disabled && <Tag>书库停用</Tag>}
            </div>
          </div>
        );
      },
    },
    {
      title: "文件",
      key: "state",
      width: 90,
      render: (_: unknown, b: Book) => <StateTag state={b.file_state} />,
    },
    {
      title: "元数据",
      key: "metadata",
      width: 100,
      render: (_: unknown, b: Book) => (
        <StateTag state={b.metadata?.state ?? "none"} />
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 145,
      fixed: "right" as const,
      render: (_: unknown, b: Book) => (
        <Space size={2}>
          <Button
            type="text"
            aria-label={`${b.user_state?.favorite ? "取消收藏" : "收藏"} ${preferredBookTitle(b)}`}
            icon={
              b.user_state?.favorite ? (
                <StarFilled aria-hidden="true" style={{ color: "#d89614" }} />
              ) : (
                <StarOutlined aria-hidden="true" />
              )
            }
            onClick={() =>
              void action.run(async () => {
                await organization.state(b, {
                  favorite: !b.user_state?.favorite,
                });
                listing.reload();
              })
            }
          />
          <Button
            type="text"
            icon={
              permissions.edit(b.library_id) ? (
                <EditOutlined aria-hidden="true" />
              ) : (
                <EyeOutlined aria-hidden="true" />
              )
            }
            aria-label={`${permissions.edit(b.library_id) ? "编辑" : "查看详情"} ${preferredBookTitle(b)}`}
            onClick={() => openBook(b.id)}
          />
          <Dropdown
            menu={{
              items: [
                {
                  key: "metadata",
                  label: "获取元数据",
                  disabled: !!b.trashed_at,
                },
                {
                  key: "hide",
                  label: b.user_state?.hidden ? "恢复显示" : "隐藏这本",
                },
                {
                  key: "trash",
                  label: "移入回收站",
                  danger: true,
                  disabled: !!b.trashed_at,
                },
              ].filter(
                (item) =>
                  item.key === "hide" ||
                  (item.key === "metadata" && permissions.edit(b.library_id)) ||
                  (item.key === "trash" && permissions.admin),
              ),
              onClick: ({ key }) => {
                if (key === "trash") setTrash([b]);
                else if (key === "hide")
                  void action.run(async () => {
                    await organization.state(b, {
                      hidden: !b.user_state?.hidden,
                    });
                    listing.reload();
                  });
                else
                  void action.run(async () => {
                    const j = await metadata.fetch([b.id]);
                    navigate(taskURL(j.job_id));
                  });
              },
            }}
          >
            <Button
              type="text"
              icon={<MoreOutlined aria-hidden="true" />}
              aria-label={`更多操作 ${preferredBookTitle(b)}`}
            />
          </Dropdown>
        </Space>
      ),
    },
  ];
  return (
    <>
      <PageTitle
        title="漫画"
        subtitle={
          review
            ? "集中核对未匹配、内容变化及需要人工确认的漫画。"
            : "检索、编辑和整理物理漫画索引。源文件保持只读。"
        }
        extra={
          <Button
            icon={<BookOutlined aria-hidden="true" />}
            onClick={() => setSaveOpen(true)}
          >
            已保存的搜索
          </Button>
        }
      />
      {!saveOpen && <ActionNotice action={action} />}
      <Card className="filter-card">
        <div className="book-search">
          <Input.Search
            aria-label="搜索标题或标签"
            placeholder="标题、原文标题或 namespace:标签"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onSearch={() => apply({ query })}
            enterButton="搜索"
            allowClear
          />
        </div>
        <div className="book-primary-filters">
          <Select
            aria-label="筛选文件状态"
            value={conditions.file_state}
            options={[
              { value: "", label: "文件：全部" },
              { value: "available", label: "可用" },
              { value: "missing", label: "缺失" },
              { value: "corrupt", label: "损坏" },
            ]}
            onChange={(file_state) => apply({ file_state })}
          />
          <Select
            aria-label="元数据状态"
            value={conditions.metadata_state ?? ""}
            options={[
              { value: "", label: "元数据：全部" },
              { value: "none", label: "未匹配来源" },
              { value: "matched", label: "已匹配来源" },
              { value: "needs_review", label: "匹配待复核" },
            ]}
            onChange={(metadata_state) => apply({ metadata_state })}
          />
          <Button
            icon={<FilterOutlined aria-hidden="true" />}
            aria-expanded={moreFilters}
            aria-controls="book-more-filters"
            onClick={() => setMoreFilters((value) => !value)}
          >
            更多筛选
            {conditions.scope !== "all" ||
            includeDisabled ||
            Object.keys(conditions.properties ?? {}).length > 0
              ? ` (${Number(conditions.scope !== "all") + Number(includeDisabled) + Object.keys(conditions.properties ?? {}).length})`
              : ""}
            <DownOutlined
              aria-hidden="true"
              className={
                moreFilters ? "filter-chevron expanded" : "filter-chevron"
              }
            />
          </Button>
        </div>
        <div
          id="book-more-filters"
          className="book-more-filters"
          hidden={!moreFilters}
        >
          <Select
            aria-label="查询范围"
            value={conditions.scope}
            options={[
              { value: "all", label: "全部漫画" },
              { value: "favorites", label: "收藏" },
              { value: "missing_metadata", label: "缺元数据" },
              { value: "hidden", label: "隐藏" },
              { value: "missing", label: "文件缺失" },
              { value: "corrupt", label: "损坏" },
            ]}
            onChange={(scope) => apply({ scope })}
          />
          <LibraryScopeControl />
          {scope.all && (
            <Checkbox
              checked={includeDisabled}
              onChange={(e) => apply({ include_disabled: e.target.checked })}
            >
              包括停用书库
            </Checkbox>
          )}
          <PropertyFilters
            key={filterReset}
            value={conditions.properties}
            onChange={(properties) => apply({ properties })}
          />
          <Typography.Text type="secondary" className="search-syntax">
            支持 AND / OR、括号、引号、排除 - 与标签前缀 *
          </Typography.Text>
        </div>
      </Card>
      <ErrorNotice
        error={parsed.error || listing.error}
        retry={() => {
          if (parsed.error) {
            setQueryDraft({ applied: "", value: "" });
            setFilterReset((value) => value + 1);
            apply(
              {
                ...base,
                metadata_state: review ? "none" : "",
                visibility: "active",
              },
              true,
            );
          } else if (conditions.page > 1) apply({ page: 1 }, true);
          else listing.reload();
        }}
      />
      {selected.length > 0 && (
        <div className="selection-toolbar">
          <Space wrap>
            <strong>已选择 {selected.length} 本</strong>
            {selected.every((b) => permissions.edit(b.library_id)) &&
              (permissions.admin ||
                new Set(selected.map((b) => b.library_id)).size === 1) && (
                <>
                  <Button
                    onClick={() => void batchMetadata()}
                    disabled={action.busy}
                  >
                    获取所选元数据
                  </Button>
                  <RecommendationLauncher
                    libraryID={
                      new Set(selected.map((b) => b.library_id)).size === 1
                        ? selected[0]?.library_id
                        : undefined
                    }
                  />
                  <Button onClick={() => setOrganize(true)}>
                    整理所选漫画
                  </Button>
                </>
              )}
            <AdminOnly>
              <Button danger onClick={() => setTrash(selected)}>
                移入回收站
              </Button>
            </AdminOnly>
            <Button type="text" onClick={() => setSelection({})}>
              清除选择
            </Button>
          </Space>
        </div>
      )}
      <Card className="table-card">
        <div className="book-results-toolbar">
          <Space wrap>
            <strong role="status">
              {parsed.error
                ? "筛选条件无效"
                : listing.data
                  ? `${listing.data.total} 本漫画`
                  : listing.loading
                    ? "正在加载…"
                    : "— 本漫画"}
            </strong>
            <Button
              type="text"
              icon={<SaveOutlined aria-hidden="true" />}
              onClick={() => setSaveOpen(true)}
            >
              保存条件
            </Button>
            <Button
              type="text"
              onClick={() => {
                setQueryDraft({ applied: "", value: "" });
                setFilterReset((value) => value + 1);
                apply({
                  ...base,
                  metadata_state: review ? "none" : "",
                  visibility: "active",
                });
              }}
            >
              重置筛选
            </Button>
          </Space>
          <Space wrap>
            <Select
              aria-label="排序方式"
              value={`${conditions.sort}:${conditions.order}`}
              options={[
                { value: "indexed_at:desc", label: "最近入库" },
                { value: "indexed_at:asc", label: "最早入库" },
                { value: "title:asc", label: "标题升序" },
                { value: "title:desc", label: "标题降序" },
              ]}
              onChange={(value) => {
                const [sort, order] = value.split(":");
                apply({ sort, order });
              }}
            />
            <Button
              icon={<ReloadOutlined aria-hidden="true" />}
              onClick={listing.reload}
            >
              刷新
            </Button>
          </Space>
        </div>
        <Table<Book>
          rowKey="id"
          size="small"
          loading={listing.loading}
          dataSource={parsed.error ? [] : (listing.data?.items ?? [])}
          columns={tableColumns}
          scroll={{ x: 620 }}
          tableLayout="fixed"
          locale={{ emptyText: <Empty description="没有符合条件的漫画" /> }}
          rowSelection={{
            selectedRowKeys: Object.keys(selection),
            preserveSelectedRowKeys: true,
            getCheckboxProps: (b) => ({
              "aria-label": `选择 ${preferredBookTitle(b)}`,
            }),
            onChange: (keys, rows) => {
              if (keys.length > 1000) {
                action.setError(
                  new Error("最多选择 1000 本，请先完成或清除当前批次。"),
                );
                return;
              }
              setSelection((previous) => {
                const next: Record<string, Book> = {};
                for (const id of keys) {
                  const b =
                    rows.find((v) => v?.id === id) ?? previous[String(id)];
                  if (b) next[b.id] = b;
                }
                return next;
              });
            },
          }}
          pagination={{
            current: conditions.page,
            pageSize: conditions.page_size,
            total: listing.data?.total ?? 0,
            showSizeChanger: true,
            pageSizeOptions: [25, 50, 100],
            onChange: (page, page_size) =>
              apply({
                page: page_size !== conditions.page_size ? 1 : page,
                page_size,
              }),
            showTotal: (total) => `共 ${total} 本`,
          }}
        />
      </Card>
      <BookEditor
        key={bookID}
        id={bookID}
        onClose={() => {
          const q = new URLSearchParams(location.search);
          q.delete("book");
          void routerNavigate(
            { search: q.toString() },
            { preventScrollReset: true },
          );
        }}
        onChanged={listing.reload}
        navigate={navigate}
      />
      {organize && (
        <OrganizeSelection
          books={selected}
          onClose={() => setOrganize(false)}
          onDone={() => {
            setOrganize(false);
            listing.reload();
          }}
        />
      )}
      {trash.length > 0 && (
        <MaintenanceDialog
          operation="trash"
          books={trash}
          onClose={() => setTrash([])}
          onDone={(id) => {
            setTrash([]);
            setSelection({});
            navigate(taskURL(id));
          }}
        />
      )}
      <Modal
        title="保存常用搜索"
        open={saveOpen}
        onCancel={() => setSaveOpen(false)}
        footer={null}
      >
        <Space orientation="vertical" className="full-width">
          <Select
            aria-label="已保存搜索"
            value={savedID}
            className="full-width"
            options={[
              { value: "", label: "新建保存搜索" },
              ...(saved.data?.items ?? []).map((s) => ({
                value: s.id,
                label: s.name,
              })),
            ]}
            onChange={(id) => {
              setSavedID(id);
              setSaveName(
                saved.data?.items.find((s) => s.id === id)?.name ?? "",
              );
            }}
          />
          <Input
            aria-label="搜索名称"
            placeholder="为当前搜索命名"
            value={saveName}
            maxLength={200}
            onChange={(e) => setSaveName(e.target.value)}
          />
          <Alert
            type="info"
            title="只保存已应用的条件，不保存页码、结果或未提交的搜索草稿。"
          />
          <Space wrap>
            <Button
              type="primary"
              disabled={!saveName.trim() || !!parsed.error}
              loading={action.busy}
              onClick={() =>
                void action.run(async () => {
                  await search.save(
                    saveName,
                    savedConditions(conditions),
                    saved.data?.items.find((s) => s.id === savedID),
                  );
                  saved.reload();
                }, "搜索已保存")
              }
            >
              {savedID ? "更新保存搜索" : "保存当前搜索"}
            </Button>
            <Button
              disabled={!savedID}
              onClick={() => {
                const s = saved.data?.items.find((s) => s.id === savedID);
                if (s) {
                  try {
                    const q = writeSearchParams(new URLSearchParams(), {
                      ...base,
                      ...s.conditions,
                      page: 1,
                      collapse: "none",
                    });
                    q.delete("library");
                    if (!s.conditions.library_id?.trim()) {
                      q.delete("library_id");
                      q.set("library_scope", "all");
                    }
                    void routerNavigate(
                      { pathname: location.pathname, search: q.toString() },
                      { preventScrollReset: true },
                    );
                    setFilterReset((value) => value + 1);
                    setSaveOpen(false);
                  } catch (error) {
                    action.setError(error);
                  }
                }
              }}
            >
              打开保存搜索
            </Button>
            <Button
              danger
              disabled={!savedID}
              loading={action.busy}
              onClick={() =>
                void action.run(async () => {
                  const s = saved.data?.items.find((s) => s.id === savedID);
                  if (s) await search.remove(s);
                  setSavedID("");
                  setSaveName("");
                  saved.reload();
                }, "保存搜索已删除")
              }
            >
              删除保存搜索
            </Button>
          </Space>
          <ActionNotice action={action} />
        </Space>
      </Modal>
    </>
  );
}

function BookCover({ book, onOpen }: { book: Book; onOpen: () => void }) {
  const [failed, setFailed] = useState(false);
  const available =
    !book.trashed_at && (book.page_count > 0 || (book.cover_revision ?? 0) > 0);
  return (
    <button
      type="button"
      className="book-cover-button"
      aria-label={`查看漫画详情 ${preferredBookTitle(book)}`}
      onClick={onOpen}
    >
      {available && !failed ? (
        <img
          className="table-cover"
          loading="lazy"
          src={`/api/v1/books/${book.id}/cover?content_revision=${book.content_revision}&cover_revision=${book.cover_revision ?? 0}`}
          alt=""
          onError={() => setFailed(true)}
        />
      ) : (
        <span className="cover-empty">
          <BookOutlined aria-hidden="true" />
          <span>暂无封面</span>
        </span>
      )}
    </button>
  );
}
