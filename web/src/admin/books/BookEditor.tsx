import { usePermissions, AdminOnly } from "../../shared/permissions";
import { MetadataReadOnly } from "./MetadataReadOnly";
import { ReadingControls } from "./ReadingState";
import { healthBytes } from "../../shared/healthFlow";
import { useEffect } from "react";
import { resourceEvent } from "../../shared/events";
import type { Work } from "../../shared/organization";
import { BookVersions } from "./BookVersions";
import { originalTitle } from "./bookTitle";
import { taskURL } from "../../shared/consoleRoutes.mjs";
import { useContext, useState } from "react";
import {
  Alert,
  App as AntApp,
  Button,
  Descriptions,
  Modal,
  Space,
  Spin,
  Tabs,
  Tag,
  Typography,
} from "antd";
import { BusinessReadContext } from "../../shared/readBoundary";
import { useLocation } from "react-router";
import { catalog } from "../../shared/api";
import type { Book } from "../../shared/api";
import { RecommendationLauncher } from "../versions/RecommendationLauncher";
import { useRecommendationCapability } from "../../shared/recommendations";
import { FieldsEditor } from "../FieldsEditor";
import { versionStatus } from "../metadataForm";
import { organization } from "../../shared/organization";
import {
  ErrorNotice,
  RefreshButton,
  useAction,
  ActionNotice,
  useRemote,
  useDirty,
  StateTag,
  time,
} from "../common";
import { MaintenanceDialog } from "../maintenance/Maintenance";
import { CoverEditor } from "../CoverEditor";
import { SourceEditor } from "../SourceEditor";
import { BookOrganization } from "./BookOrganization";

export function BookEditor({
  id,
  onClose,
  onChanged,
  navigate,
  workContext,
  versionsOpen = false,
  onVersionsOpen,
  onEditionChange,
  onVersionQuery,
}: {
  id: string;
  onClose: () => void;
  onChanged: () => void;
  navigate: (path: string) => void;
  workContext?: Work;
  versionsOpen?: boolean;
  onVersionsOpen?: (open: boolean) => void;
  onEditionChange?: (id: string) => void;
  onVersionQuery?: (v: Record<string, string | number | null>) => void;
}) {
  const permissions = usePermissions();
  const readable = useContext(BusinessReadContext);
  const load = useRemote<Book>(`/books/${id}`, undefined, "GET", !!id);
  const reloadBook = load.reload;
  useEffect(() => {
    if (!id) return;
    const changed = (event: Event) => {
      if (
        (event as CustomEvent<{ type?: string }>).detail?.type ===
        "enhancements"
      )
        reloadBook();
    };
    window.addEventListener(resourceEvent, changed);
    return () => window.removeEventListener(resourceEvent, changed);
  }, [id, reloadBook]);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editorFooter, setEditorFooter] = useState<HTMLDivElement | null>(null);
  const { capability: recommendationCapability } = useRecommendationCapability(
    permissions.edit(load.data?.library_id || ""),
  );
  const [sourceDirty, setSourceDirty] = useState(false);
  const [coverDirty, setCoverDirty] = useState(false);
  const [organizationDirty, setOrganizationDirty] = useState(false);
  const [maintenance, setMaintenance] = useState("");
  const [tab, setTab] = useState(
    new URLSearchParams(window.location.search).has("preview")
      ? "source"
      : "overview",
  );
  const location = useLocation();
  const preview = new URLSearchParams(location.search).get("preview") ?? "";
  const releaseDirty = useDirty(
    dirty || sourceDirty || coverDirty || organizationDirty,
  );
  const { modal } = AntApp.useApp();
  function close() {
    if (saving) return;
    if (dirty || sourceDirty || coverDirty || organizationDirty)
      modal.confirm({
        title: "放弃尚未保存的编辑？",
        content: "当前草稿还未提交。",
        okText: "放弃并关闭",
        cancelText: "继续编辑",
        onOk: () => {
          setDirty(false);
          releaseDirty();
          onClose();
        },
      });
    else onClose();
  }
  function switchEdition(next: string) {
    if (saving || next === id) return;
    const apply = () => {
      releaseDirty();
      onEditionChange?.(next);
    };
    if (dirty || sourceDirty || coverDirty || organizationDirty)
      modal.confirm({
        title: "放弃当前版本未保存的修改？",
        content: "切换后将打开所选版本的独立资料。",
        okText: "放弃并切换",
        cancelText: "继续编辑",
        onOk: apply,
      });
    else apply();
  }
  const b = load.data;
  return (
    <>
      <Modal
        open={!!id && readable}
        onCancel={close}
        title={
          <div className="book-dialog-heading">
            <span>漫画详情{b ? ` · ${originalTitle(b)}` : ""}</span>
            <Space>
              {!permissions.verified && (
                <Button
                  disabled={false}
                  onClick={() =>
                    window.dispatchEvent(new Event("actor-session-refresh"))
                  }
                >
                  重试权限读取
                </Button>
              )}
              <RefreshButton disabled={saving} reload={load.reload} />
              <Button disabled={saving} onClick={close}>
                关闭
              </Button>
            </Space>
          </div>
        }
        width={1100}
        centered
        closable={false}
        className="book-dialog"
        wrapClassName="book-dialog-wrap"
        footer={tab === "fields" ? <div ref={setEditorFooter} /> : null}
      >
        <ErrorNotice error={load.error} retry={load.reload} />
        {load.loading && !b && <Spin />}
        {b && (
          <>
            {workContext && onEditionChange && (
              <>
                <div className="book-version-toolbar">
                  <span className="small-text">
                    当前资料属于这一版本
                    {workContext.name_override
                      ? ` · 作品名：${workContext.name_override}`
                      : ""}
                  </span>
                  <Button
                    disabled={saving}
                    onClick={() => onVersionsOpen?.(!versionsOpen)}
                  >
                    {versionsOpen ? "收起其他版本" : "其他版本"}
                  </Button>
                </div>
                {versionsOpen && (
                  <BookVersions
                    work={workContext}
                    current={id}
                    busy={saving}
                    onSelect={switchEdition}
                    onQuery={onVersionQuery || (() => {})}
                  />
                )}
              </>
            )}
            <Tabs
              activeKey={
                !b ||
                permissions.edit(b.library_id) ||
                ["overview", "fields", "file"].includes(tab)
                  ? tab
                  : "overview"
              }
              onChange={(next) => {
                if (!saving) setTab(next);
              }}
              items={[
                {
                  key: "overview",
                  label: "概览",
                  children: (
                    <Overview
                      book={b}
                      navigate={navigate}
                      onChanged={() => {
                        load.reload();
                        onChanged();
                      }}
                      onMaintenance={setMaintenance}
                    />
                  ),
                },
                {
                  key: "fields",
                  label: "元数据",
                  children: !permissions.edit(b.library_id) ? (
                    <MetadataReadOnly book={b} />
                  ) : (
                    <FieldsEditor
                      book={b}
                      dirty={setDirty}
                      onBusy={setSaving}
                      footer={editorFooter}
                      onSaved={(v) => {
                        load.setData((previous) =>
                          previous
                            ? {
                                ...previous,
                                ...v,
                                user_state: v.user_state ?? previous.user_state,
                                organization:
                                  v.organization ?? previous.organization,
                              }
                            : v,
                        );
                        onChanged();
                      }}
                    />
                  ),
                },
                {
                  key: "source",
                  label: "来源匹配",
                  children: (
                    <SourceEditor
                      book={b}
                      previewID={preview}
                      onDirty={setSourceDirty}
                      onBusy={setSaving}
                      navigate={navigate}
                      onSaved={() => {
                        load.reload();
                        onChanged();
                      }}
                    />
                  ),
                },
                {
                  key: "cover",
                  label: "封面",
                  children: (
                    <CoverEditor
                      book={b}
                      active={tab === "cover"}
                      onDirty={setCoverDirty}
                      onBusy={setSaving}
                      onChanged={() => {
                        load.reload();
                        onChanged();
                      }}
                    />
                  ),
                },
                {
                  key: "organization",
                  label: "组织",
                  children: (
                    <BookOrganization
                      book={b}
                      navigate={navigate}
                      onDirty={setOrganizationDirty}
                      onBusy={setSaving}
                      onChanged={() => {
                        load.reload();
                        onChanged();
                      }}
                    />
                  ),
                },
                ...(recommendationCapability?.enabled ||
                recommendationCapability?.discovery
                  ? [
                      {
                        key: "recommendations",
                        label: "同作品整理",
                        children: (
                          <Space orientation="vertical" className="full-width">
                            <Typography.Paragraph type="secondary">
                              同作品整理现在按整个书库查找，不需要逐本选择或填写关系反馈。
                            </Typography.Paragraph>
                            <RecommendationLauncher libraryID={b.library_id} />
                          </Space>
                        ),
                      },
                    ]
                  : []),
                {
                  key: "file",
                  label: "文件诊断",
                  children: (
                    <FileDetails
                      book={b}
                      onRelink={() => setMaintenance("relink")}
                    />
                  ),
                },
              ].filter(
                (item) =>
                  permissions.edit(b.library_id) ||
                  ["overview", "fields", "file"].includes(item.key),
              )}
            />
          </>
        )}
      </Modal>
      {b && maintenance && (
        <MaintenanceDialog
          operation={maintenance}
          books={[b]}
          onClose={() => setMaintenance("")}
          onDone={(job) => {
            setMaintenance("");
            navigate(taskURL(job));
          }}
        />
      )}
    </>
  );
}
function Overview({
  book,
  navigate,
  onChanged,
  onMaintenance,
}: {
  book: Book;
  navigate: (path: string) => void;
  onChanged: () => void;
  onMaintenance: (op: string) => void;
}) {
  const action = useAction();
  return (
    <Space orientation="vertical" className="full-width" size={20}>
      <ActionNotice action={action} />
      <div className="book-overview">
        {!book.trashed_at &&
        (book.page_count > 0 || (book.cover_revision ?? 0) > 0) ? (
          <img
            className="overview-cover"
            src={
              catalog.cover(book) +
              `&cover_revision=${book.cover_revision ?? 0}`
            }
            alt={`${originalTitle(book)}封面`}
          />
        ) : (
          <div className="overview-cover cover-empty">暂无可用封面</div>
        )}
        <Descriptions
          column={1}
          size="small"
          items={[
            {
              key: "original",
              label: "原文标题",
              children:
                (book.title_original ?? book.metadata?.fields.title_original) ||
                "—",
            },
            { key: "title", label: "其他标题", children: book.title },
            {
              key: "state",
              label: "文件状态",
              children: <StateTag state={book.file_state} />,
            },
            {
              key: "meta",
              label: "元数据",
              children: <StateTag state={book.metadata?.state ?? "none"} />,
            },
            ...versionStatus(book).map((version) => ({
              key: `version-${version.id}`,
              label: "版本状态",
              children: (
                <Space>
                  <Tag color="blue">{version.status}</Tag>
                  {version.label}
                </Space>
              ),
            })),
            ...(book.organization?.series ?? []).map((series) => ({
              key: `series-${series.id}`,
              label: "所属系列",
              children: (
                <Button
                  type="link"
                  className="inline-link"
                  onClick={() => navigate(`/admin/series/${series.id}`)}
                >
                  {series.name}
                </Button>
              ),
            })),
            {
              key: "local",
              label: "本地文件",
              children: `${book.page_count > 0 ? `${book.page_count} 页` : "页数未知"} · ${healthBytes(book.size)}`,
            },
            {
              key: "remote",
              label: "来源画廊",
              children: `${book.metadata?.source?.fields.remote_page_count == null ? "页数未知" : `${book.metadata.source.fields.remote_page_count} 页`} · ${book.metadata?.source?.fields.remote_size_bytes == null ? "大小未知" : healthBytes(book.metadata.source.fields.remote_size_bytes)}`,
            },
            {
              key: "indexed",
              label: "索引时间",
              children: time(book.indexed_at),
            },
          ]}
        />
      </div>
      {book.reading_state && (
        <ReadingControls state={book.reading_state} onChanged={onChanged} />
      )}
      <Space wrap>
        <Button
          onClick={() =>
            void action.run(async () => {
              await organization.state(book, {
                favorite: !book.user_state?.favorite,
              });
              onChanged();
            })
          }
        >
          {book.user_state?.favorite ? "取消收藏" : "收藏这本"}
        </Button>
        <Button
          onClick={() =>
            void action.run(async () => {
              await organization.state(book, {
                hidden: !book.user_state?.hidden,
              });
              onChanged();
            })
          }
        >
          {book.user_state?.hidden ? "恢复显示" : "隐藏这本"}
        </Button>
        <AdminOnly>
          <Button
            danger
            disabled={!!book.trashed_at}
            onClick={() => onMaintenance("trash")}
          >
            移入回收站
          </Button>
        </AdminOnly>
      </Space>
    </Space>
  );
}
function FileDetails({ book, onRelink }: { book: Book; onRelink: () => void }) {
  return (
    <Space orientation="vertical" className="full-width">
      <Descriptions
        column={1}
        bordered
        size="small"
        items={[
          { key: "path", label: "相对路径", children: book.relative_path },
          {
            key: "size",
            label: "文件大小",
            children: `${(book.size / 1024 / 1024).toFixed(2)} MiB`,
          },
          {
            key: "version",
            label: "内容版本",
            children: book.content_revision,
          },
          { key: "error", label: "诊断码", children: book.error_code || "无" },
          {
            key: "sha",
            label: "代表图 SHA1",
            children: (
              <Typography.Text copyable>
                {book.representative_sha1 ?? "无"}
              </Typography.Text>
            ),
          },
        ]}
      />
      <AdminOnly>
        <Button onClick={onRelink}>重新关联源文件</Button>
      </AdminOnly>
      <Alert
        type="info"
        title="重新关联保留书籍身份和人工整理。内容不同时需明确确认，原来源会标记为待复核。"
      />
    </Space>
  );
}
