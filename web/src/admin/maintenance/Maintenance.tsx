import { originalTitle } from "../books/bookTitle";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import { useState } from "react";
import {
  Alert,
  Breadcrumb,
  Button,
  Card,
  Checkbox,
  Descriptions,
  Empty,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import {
  FolderOutlined,
  FileZipOutlined,
  ArrowLeftOutlined,
} from "@ant-design/icons";
import type { Book, Library, Listing, Root } from "../../shared/api";
import { request } from "../../shared/api";
import {
  RefreshButton,
  useURLValue,
  useRemote,
  useAction,
  useDirty,
  ActionNotice,
  ErrorNotice,
  time,
  query,
} from "../common";
import type { MaintenancePlan, RootEntry, TrashEntry } from "../types";
const operationNames: Record<string, string> = {
  trash: "移入回收站",
  restore: "恢复索引",
  purge: "永久清除应用记录",
  relink: "重新关联源文件",
  rebind: "重新关联漫画目录",
  cleanup: "清理存储",
  reinstate: "恢复已排除文件",
};
const categoryNames: Record<string, string> = {
  covers: "封面缓存",
  tag_images: "标签图片缓存",
  orphan_assets: "无引用人工素材",
  assets: "人工素材",
  tmp: "临时文件",
  terminal_jobs: "终态任务",
  snapshots: "升级快照",
  tag_translations: "标签译库",
  translation_cache: "译库缓存",
  pages: "页面缓存",
  database: "业务数据库",
};
const summaryNames: Record<string, string> = {
  books: "漫画",
  libraries: "书库",
  groups: "受影响版本组",
  series: "受影响系列",
  count: "数量",
  bytes: "空间（字节）",
  files: "文件数",
  book_count: "漫画数",
  library_count: "书库数",
  source_files_deleted: "是否删除源漫画",
  operation: "操作",
  group_count: "版本组数",
  series_count: "系列数",
  preserved: "保留项",
  removed: "移除项",
  members: "成员",
  before: "变更前",
  after: "变更后",
  primary_changes: "主版本变化",
  dissolved_groups: "退化解散的组",
  series_changes: "系列顺序变化",
  exclusions: "扫描排除记录",
  history: "保留的任务历史",
  targets: "本次处理对象",
  targets_truncated: "处理对象列表已截断",
  remaining_order: "变更后系列顺序",
  remaining_order_truncated: "系列顺序列表已截断",
  title: "漫画标题",
  name: "组织名称",
  relative_path: "源文件相对路径",
  display_name: "书库名称",
  library_name: "所属书库",
  organization_effects: "组织关系影响",
  relationship_preserved: "保留组织关系",
  remaining_members: "剩余物理版本数",
  remaining_items: "剩余逻辑条目数",
  remaining_titles: "剩余系列顺序",
  primary_book_title: "变更后主版本",
  dissolved: "版本组将解散",
  revision: "校验版本",
  storage: "存储清理预估",
  requires_rescan: "完成后需要手动重扫",
  content_identity_verified: "已确认文件内容一致",
  kind: "对象类型",
  truncated: "列表已截断",
  source_archives: "源漫画文件：始终只读",
  referenced_assets: "仍被引用的人工素材",
  latest_upgrade_snapshot: "最新的升级回退快照",
  active_tasks: "运行中的任务及其资源",
};
function summary(value: unknown): React.ReactNode {
  if (value === null || value === undefined) return "无";
  if (typeof value === "boolean") return value ? "是" : "否";
  if (Array.isArray(value))
    return value.length ? (
      <ul>
        {value.map((v, i) => (
          <li key={i}>{summary(v)}</li>
        ))}
      </ul>
    ) : (
      "无"
    );
  if (typeof value === "object")
    return (
      <Descriptions
        size="small"
        column={1}
        bordered
        items={Object.entries(value)
          .filter(
            ([key]) =>
              ![
                "id",
                "book_id",
                "library_id",
                "primary_book_id",
                "resource",
              ].includes(key),
          )
          .map(([key, v]) => ({
            key,
            label: summaryNames[key] ?? categoryNames[key] ?? key,
            children:
              key === "bytes" && typeof v === "number"
                ? `${(v / 1024 / 1024).toFixed(2)} MiB`
                : summary(v),
          }))}
      />
    );
  return (
    summaryNames[String(value)] ??
    categoryNames[String(value)] ??
    operationNames[String(value)] ??
    { book: "漫画", library: "书库", group: "版本组", standalone: "独立漫画" }[
      String(value)
    ] ??
    String(value)
  );
}
export function MaintenanceDialog({
  operation,
  books = [],
  library,
  onClose,
  onDone,
  initialOptions,
}: {
  operation: string;
  books?: Book[];
  library?: Library;
  onClose: () => void;
  onDone: (job: string) => void;
  initialOptions?: Record<string, unknown>;
}) {
  const [plan, setPlan] = useState<MaintenancePlan | null>(null);
  const [root, setRoot] = useState(
    operation === "reinstate" ? (library?.root_key ?? "") : "",
  );
  const [relative, setRelative] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [browser, setBrowser] = useState(false);
  const [purgeAck, setPurgeAck] = useState(false);
  const action = useAction();
  const roots = useRemote<{ items: Root[] }>(
    "/library-roots",
    undefined,
    "GET",
    operation === "relink" ||
      operation === "rebind" ||
      operation === "reinstate",
  );
  const expected = Object.fromEntries([
    ...books.map((b) => [`book:${b.id}`, b.revision] as const),
    ...(library ? [[`library:${library.id}`, library.revision] as const] : []),
  ]);
  const options =
    initialOptions ??
    (operation === "relink"
      ? {
          root_key: root,
          relative_path: relative,
          confirm_replacement: confirmed,
        }
      : operation === "reinstate"
        ? { relative_path: relative }
        : operation === "rebind"
          ? { root_key: root }
          : {});
  const eligible =
    operation === "relink"
      ? !!root && !!relative && confirmed
      : operation === "reinstate"
        ? !!relative
        : operation === "rebind"
          ? !!root
          : true;
  async function preview() {
    await action.run(async () => {
      setPlan(
        await request<MaintenancePlan>("/maintenance/plans", "POST", {
          operation,
          book_ids: books.map((b) => b.id),
          ...(library ? { library_id: library.id } : {}),
          expected_revisions: expected,
          options,
        }),
      );
    }, "");
  }
  return (
    <>
      <Modal
        open
        title={operationNames[operation] ?? operation}
        width={720}
        onCancel={onClose}
        footer={
          <Space>
            <Button onClick={onClose}>取消</Button>
            {!plan ? (
              <Button
                type="primary"
                disabled={!eligible}
                loading={action.busy}
                onClick={() => void preview()}
              >
                查看影响预览
              </Button>
            ) : (
              <>
                <Button
                  onClick={() => {
                    setPlan(null);
                    setPurgeAck(false);
                  }}
                >
                  返回修改
                </Button>
                <Button
                  type="primary"
                  danger={operation === "purge"}
                  disabled={operation === "purge" && !purgeAck}
                  loading={action.busy}
                  onClick={() =>
                    void action.run(async () => {
                      const result = await request<{ job_id: string }>(
                        `/maintenance/plans/${plan.id}/execute`,
                        "POST",
                        {},
                      );
                      onDone(result.job_id);
                    }, "已加入任务中心")
                  }
                >
                  确认执行{operationNames[operation]}
                </Button>
              </>
            )}
          </Space>
        }
      >
        <Space orientation="vertical" className="full-width" size={16}>
          <ActionNotice action={action} />
          <Alert
            showIcon
            type={operation === "purge" ? "warning" : "info"}
            title={
              operation === "purge"
                ? "永久清除不可恢复，但不会删除源漫画文件。"
                : operation === "trash"
                  ? "索引进入回收站，人工整理和组织关系保留。扫描不会自动重新纳入。"
                  : "操作只影响明确选择的应用数据。"
            }
          />
          {books.length > 0 && (
            <div className="selected-books">
              {books.map((b) => (
                <Tag key={b.id}>{originalTitle(b)}</Tag>
              ))}
            </div>
          )}
          {library && (
            <p>
              书库：<strong>{library.display_name}</strong>
            </p>
          )}
          {!plan &&
            (operation === "relink" ||
              operation === "rebind" ||
              operation === "reinstate") && (
              <>
                <Select
                  aria-label="目标漫画目录"
                  disabled={operation === "reinstate"}
                  placeholder="选择漫画目录"
                  className="full-width"
                  value={root || undefined}
                  options={(roots.data?.items ?? []).map((r) => ({
                    value: r.root_key,
                    label: r.display_name,
                  }))}
                  onChange={(v) => {
                    setRoot(v);
                    setRelative("");
                  }}
                />
                {(operation === "relink" || operation === "reinstate") && (
                  <>
                    <Button disabled={!root} onClick={() => setBrowser(true)}>
                      浏览并选择漫画归档
                    </Button>
                    <Typography.Text>
                      已选择：{relative || "尚未选择"}
                    </Typography.Text>
                    {operation === "relink" && (
                      <Checkbox
                        checked={confirmed}
                        onChange={(e) => setConfirmed(e.target.checked)}
                      >
                        确认可能替换文件内容；书籍 ID
                        与人工整理保留，内容变化时来源待复核
                      </Checkbox>
                    )}
                  </>
                )}
              </>
            )}
          {plan && (
            <>
              <Typography.Text type="secondary">
                预览有效至 {time(plan.expires_at)}。执行时会再次核对版本。
              </Typography.Text>
              <Typography.Title level={5}>影响摘要</Typography.Title>
              {summary(plan.summary)}
              <Typography.Text type="secondary">
                涉及 {Array.isArray(plan.affected) ? plan.affected.length : 0}{" "}
                条漫画记录；以上方书名、路径和组织影响为准。
              </Typography.Text>
              <Typography.Title level={5}>受保护项目</Typography.Title>
              {summary(plan.protected)}
              {operation === "purge" && (
                <Checkbox
                  checked={purgeAck}
                  onChange={(e) => setPurgeAck(e.target.checked)}
                >
                  我已核对组织关系和永久清除范围，确认清除应用记录
                </Checkbox>
              )}
            </>
          )}
        </Space>
      </Modal>
      {browser && (
        <RootBrowser
          root={root}
          onClose={() => setBrowser(false)}
          onSelect={(entry) => {
            setRelative(entry.relative_path);
            setBrowser(false);
          }}
        />
      )}
    </>
  );
}
export function RootBrowser({
  root,
  onClose,
  onSelect,
}: {
  root: string;
  onClose: () => void;
  onSelect: (entry: RootEntry) => void;
}) {
  const [path, setPath] = useState("");
  const [page, setPage] = useState(1);
  const data = useRemote<Listing<RootEntry>>(
    `/maintenance/roots/${encodeURIComponent(root)}/entries?${query({ relative_path: path, page, page_size: 50 })}`,
  );
  function go(path: string) {
    setPath(path);
    setPage(1);
  }
  return (
    <Modal
      open
      title="选择允许目录中的漫画归档"
      width={680}
      footer={<Button onClick={onClose}>取消选择</Button>}
      onCancel={onClose}
    >
      <Space orientation="vertical" className="full-width">
        <Alert
          type="info"
          title="只显示允许目录中的子目录及 ZIP / CBZ，不跟随符号链接。"
        />
        <Breadcrumb
          items={[
            {
              title: (
                <Button type="link" onClick={() => go("")}>
                  漫画目录
                </Button>
              ),
            },
            ...path
              .split("/")
              .filter(Boolean)
              .map((part, index, array) => ({
                title: (
                  <Button
                    type="link"
                    onClick={() => go(array.slice(0, index + 1).join("/"))}
                  >
                    {part}
                  </Button>
                ),
              })),
          ]}
        />
        <Button
          icon={<ArrowLeftOutlined aria-hidden="true" />}
          disabled={!path}
          onClick={() => go(path.split("/").slice(0, -1).join("/"))}
        >
          上一级
        </Button>
        <ErrorNotice error={data.error} />
        <Table
          size="small"
          rowKey="relative_path"
          loading={data.loading}
          dataSource={data.data?.items ?? []}
          columns={[
            {
              title: "名称",
              render: (_: unknown, e: RootEntry) => (
                <Space>
                  {e.kind === "directory" ? (
                    <FolderOutlined aria-hidden="true" />
                  ) : (
                    <FileZipOutlined aria-hidden="true" />
                  )}
                  {e.name}
                </Space>
              ),
            },
            {
              title: "操作",
              render: (_: unknown, e: RootEntry) => (
                <Button
                  onClick={() =>
                    e.kind === "directory" ? go(e.relative_path) : onSelect(e)
                  }
                >
                  {e.kind === "directory" ? "打开目录" : "选择归档"}
                </Button>
              ),
            },
          ]}
          pagination={{
            current: page,
            pageSize: 50,
            total: data.data?.total ?? 0,
            showSizeChanger: false,
            onChange: setPage,
          }}
        />
      </Space>
    </Modal>
  );
}
export function MaintenancePanel({
  navigate,
  view,
}: {
  navigate: (path: string) => void;
  view: "trash";
}) {
  const [kind, setKind] = useURLValue("kind", "book");
  const [page, setPage] = useURLValue("trash_page", 1);
  const trash = useRemote<Listing<TrashEntry>>(
    `/maintenance/trash?${query({ kind, page, page_size: 50 })}`,
    undefined,
    "GET",
    view === "trash",
  );
  const [operation, setOperation] = useState("");
  const [selected, setSelected] = useState<TrashEntry | null>(null);
  const [book, setBook] = useState<Book | null>(null);
  const [library, setLibrary] = useState<Library | null>(null);
  const action = useAction();
  const releaseDraft = useDirty(!!operation);
  async function prepare(row: TrashEntry, op: string) {
    await action.run(async () => {
      setSelected(row);
      if (kind === "book") setBook(await request<Book>(`/books/${row.id}`));
      else
        setLibrary(
          (await request<{ items: Library[] }>("/libraries")).items.find(
            (l) => l.id === row.id,
          ) ?? null,
        );
      setOperation(op);
    }, "");
  }
  const close = () => {
    releaseDraft();
    setOperation("");
    setSelected(null);
    setBook(null);
    setLibrary(null);
  };
  return (
    <section>
      <div className="section-toolbar">
        <Tag>全局</Tag>
        <RefreshButton reload={trash.reload} />
      </div>
      <ActionNotice action={action} />
      {
        <Card>
          <Space orientation="vertical" className="full-width">
            <Select
              aria-label="回收站类型"
              value={kind}
              onChange={(v) => {
                setKind(v);
                setPage(1);
              }}
              options={[
                { value: "book", label: "漫画索引" },
                { value: "library", label: "书库" },
              ]}
            />
            <ErrorNotice error={trash.error} />
            <Table
              rowKey="id"
              size="small"
              dataSource={trash.data?.items ?? []}
              loading={trash.loading}
              locale={{ emptyText: <Empty description="回收站为空" /> }}
              columns={[
                {
                  title: "名称",
                  render: (_: unknown, r: TrashEntry) =>
                    r.title ??
                    r.display_name ??
                    r.relative_path ??
                    "已移除对象",
                },
                {
                  title: "移除时间",
                  dataIndex: "trashed_at",
                  render: time,
                },
                {
                  title: "操作",
                  render: (_: unknown, r: TrashEntry) => (
                    <Space wrap>
                      <Button onClick={() => void prepare(r, "restore")}>
                        恢复
                      </Button>
                      <Button danger onClick={() => void prepare(r, "purge")}>
                        永久清除
                      </Button>
                    </Space>
                  ),
                },
              ]}
              pagination={{
                current: page,
                pageSize: 50,
                total: trash.data?.total ?? 0,
                showSizeChanger: false,
                onChange: setPage,
              }}
            />
          </Space>
        </Card>
      }
      {operation && (
        <MaintenanceDialog
          operation={operation}
          books={book ? [book] : []}
          library={library ?? undefined}
          initialOptions={selected ? undefined : undefined}
          onClose={close}
          onDone={(job) => {
            close();
            navigate(taskURL(job));
          }}
        />
      )}
    </section>
  );
}
