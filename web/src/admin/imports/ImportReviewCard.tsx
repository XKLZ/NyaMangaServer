/** §2.1/§2.8/§3.6: filename and best matching source title lead; state/cover/URL support.
 * Row actions are secondary; the workspace footer owns the single primary action.
 * Dimensions/type/spacing map to existing Ant tokens, with 56×80 cover identity. */
import { useMemo, useState } from "react";
import {
  Button,
  Checkbox,
  Dropdown,
  Input,
  Tag,
  Tooltip,
  App,
  Space,
} from "antd";
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClockCircleOutlined,
  WarningOutlined,
  MoreOutlined,
  FileOutlined,
  GlobalOutlined,
  ReadOutlined,
  SnippetsOutlined,
  EditOutlined,
  ExportOutlined,
  DownOutlined,
  UpOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import type { ImportMember } from "../../shared/imports";
import { categories, languages } from "../FieldsEditor";
import { namespaces } from "../tagInput";
import { reviewStatus, reviewTitle } from "./importReviewFlow";
import { Cover } from "../versions/versionManagementViews";
import { ImportCoverCompare } from "./ImportCoverCompare";

export function ImportReviewCard({
  batch,
  member: v,
  checked,
  disabled,
  url,
  onURL,
  onSelect,
  onReview,
  onRefetch,
  onEdit,
  onRetry,
  onCompare,
  showURLInput = false,
}: {
  batch: string;
  member: ImportMember;
  checked: boolean;
  disabled: boolean;
  url?: string;
  onURL: (value: string | undefined) => void;
  onSelect: (value: boolean) => void;
  onReview: (decision: string) => void;
  onRefetch: (paste: boolean) => Promise<boolean>;
  onEdit: () => void;
  onRetry: () => void;
  onCompare: (mode?: "matches" | "title", trigger?: HTMLElement) => void;
  showURLInput?: boolean;
}) {
  const [expanded, setExpanded] = useState(false),
    [input, setInput] = useState(url !== undefined);
  const { message } = App.useApp();
  const comparison = useMemo(
    () =>
      reviewTitle(
        v.filename,
        v.candidate?.fields.title ?? "",
        v.candidate?.fields.title_original ?? "",
      ),
    [v.filename, v.candidate?.fields.title, v.candidate?.fields.title_original],
  );
  const text = [v.filename, comparison.value];
  const parts = comparison.parts;
  const [state, color] = reviewStatus(v.status),
    mismatch = url !== undefined && url.trim() !== (v.candidate?.url ?? "");
  const bad = ["file_error", "stale", "fetching"].includes(v.status),
    canConfirm = !!v.candidate && !bad && !mismatch;
  const copy = async (value: string, label: string) => {
    try {
      await navigator.clipboard.writeText(value);
      void message.success(`${label}已复制`);
    } catch {
      void message.error("复制失败，请手动选择文字复制");
    }
  };
  const fields = v.candidate?.fields;
  const grouped: {
    namespace: string;
    label: string;
    tags: { namespace: string; name: string; translation: string }[];
  }[] = namespaces.map(([ns, label]) => ({
    namespace: ns,
    label,
    tags:
      v.candidate?.tags?.filter((t) => t.namespace === ns) ??
      fields?.tags
        ?.filter((t) => t.namespace === ns)
        .map((t) => ({ ...t, translation: t.name })) ??
      [],
  }));
  const extra = [
    ...new Set(fields?.tags?.map((t) => t.namespace) ?? []),
  ].filter((ns) => !namespaces.some(([n]) => n === ns));
  for (const ns of extra)
    grouped.push({
      namespace: ns,
      label: ns,
      tags:
        v.candidate?.tags?.filter((t) => t.namespace === ns) ??
        fields?.tags
          .filter((t) => t.namespace === ns)
          .map((t) => ({ ...t, translation: t.name })) ??
        [],
    });
  return (
    <article
      className={`import-review-row ${checked ? "is-selected" : ""}`}
      data-status={v.decision ? "reviewed" : v.status}
      data-testid={`import-member-${v.id}`}
      aria-label={`审核 ${v.filename}`}
    >
      <div className="import-review-main">
        <Checkbox
          aria-label={`选择 ${v.filename}`}
          checked={checked}
          disabled={disabled || !!v.decision || v.status === "fetching"}
          onChange={(e) => onSelect(e.target.checked)}
        />
        <ImportCoverCompare batch={batch} member={v}>
          <Cover
            id={v.edition_id}
            content={v.content_revision}
            revision={v.cover_revision}
            preview={false}
          />
        </ImportCoverCompare>
        <div className="import-review-titles">
          <div className="import-title-comparison">
            {text.map((value, i) => (
              <div className="import-title-line" key={i}>
                <span
                  className="import-field-label"
                  role="img"
                  aria-label={
                    i === 0
                      ? "文件名"
                      : comparison.original
                        ? "原文标题"
                        : "站点标题"
                  }
                >
                  {i === 0 ? (
                    <FileOutlined />
                  ) : comparison.original ? (
                    <ReadOutlined />
                  ) : (
                    <GlobalOutlined />
                  )}
                </span>
                {value ? (
                  <button
                    type="button"
                    className={`import-title-copy ${i === 0 ? "file-title" : ""}`}
                    onClick={() =>
                      void copy(value, i === 0 ? "文件名" : "标题")
                    }
                    aria-label={`${i === 0 ? "复制文件名" : "复制标题"} ${value}`}
                  >
                    {parts[i].map((p, j) =>
                      p.matched ? <mark key={j}>{p.text}</mark> : p.text,
                    )}
                  </button>
                ) : (
                  <span className="import-missing">
                    {v.candidate ? "未提供" : "尚无候选"}
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
        <div className="import-review-actions">
          <Tag
            color={color}
            icon={
              v.status === "ready" || v.decision ? (
                <CheckCircleOutlined aria-hidden="true" />
              ) : v.status === "fetching" ? (
                <ClockCircleOutlined aria-hidden="true" />
              ) : v.status === "fetch_error" || v.status === "file_error" ? (
                <CloseCircleOutlined aria-hidden="true" />
              ) : (
                <WarningOutlined aria-hidden="true" />
              )
            }
          >
            {v.decision
              ? ((
                  {
                    accept: "已确认匹配",
                    manual: "已人工确认",
                    keep: "已保留现有资料",
                    skip: "本次未更新元数据",
                  } as Record<string, string>
                )[v.decision] ?? state)
              : v.status === "multiple_matches"
                ? `发现${v.match_count || "多"}个候选·请选择`
                : state}
          </Tag>
        </div>
      </div>
      <div className="import-card-toolbar">
        <div className="import-toggle-slot">
          {v.candidate && (
            <Tooltip
              title={expanded ? "收起资料" : "展开资料"}
              trigger={["hover", "focus"]}
            >
              <Button
                className="import-details-toggle"
                type="text"
                aria-label="查看资料"
                aria-expanded={expanded}
                icon={
                  expanded ? (
                    <UpOutlined aria-hidden="true" />
                  ) : (
                    <DownOutlined aria-hidden="true" />
                  )
                }
                aria-controls={`import-details-${v.id}`}
                onClick={() => setExpanded(!expanded)}
              />
            </Tooltip>
          )}
        </div>
        <div className="import-gallery-row">
          {(v.status === "multiple_matches" || v.source_search_id) && (
            <Button
              size="small"
              disabled={disabled || !!v.decision}
              onClick={(event) => onCompare(undefined, event.currentTarget)}
            >
              比较候选
            </Button>
          )}
          <Tooltip title="按标题查找来源" trigger={["hover", "focus"]}>
            <Button
              type="text"
              aria-label="按标题查找来源"
              icon={<SearchOutlined aria-hidden="true" />}
              disabled={disabled || !!v.decision || v.status === "file_error"}
              onClick={(event) => onCompare("title", event.currentTarget)}
            />
          </Tooltip>
          {v.candidate && (
            <>
              <Tooltip title="复制完整画廊网址" trigger={["hover", "focus"]}>
                <button
                  type="button"
                  className="import-gallery-copy"
                  onClick={() => void copy(v.candidate!.url, "网址")}
                  aria-label="复制当前候选网址"
                >
                  {new URL(v.candidate.url).pathname.replace(/^\/|\/$/g, "")}
                </button>
              </Tooltip>
              <Tooltip title="打开画廊" trigger={["hover", "focus"]}>
                <Button
                  type="text"
                  href={v.candidate.url}
                  target="_blank"
                  rel="noreferrer"
                  aria-label="打开当前候选画廊"
                >
                  <ExportOutlined aria-hidden="true" />
                </Button>
              </Tooltip>
            </>
          )}
          <Tooltip title="粘贴网址并重新匹配" trigger={["hover", "focus"]}>
            <Button
              type="text"
              aria-label="粘贴网址重新匹配"
              icon={<SnippetsOutlined aria-hidden="true" />}
              onClick={() => {
                void onRefetch(true).then((success) => setInput(!success));
              }}
              disabled={disabled || !!v.decision || v.status === "file_error"}
            />
          </Tooltip>
        </div>
        <div className="import-card-edits">
          {!v.decision && (
            <Space size={8}>
              {canConfirm && v.status !== "ready" && (
                <Button disabled={disabled} onClick={() => onReview("accept")}>
                  确认旧匹配
                </Button>
              )}
              <Tooltip
                title={v.candidate ? "编辑候选资料" : "人工录入"}
                trigger={["hover", "focus"]}
              >
                <Button
                  type="text"
                  aria-label={v.candidate ? "编辑候选资料" : "人工录入"}
                  icon={<EditOutlined aria-hidden="true" />}
                  disabled={
                    disabled ||
                    v.status === "fetching" ||
                    v.status === "file_error"
                  }
                  onClick={onEdit}
                />
              </Tooltip>
              <Dropdown
                disabled={disabled}
                trigger={["click"]}
                menu={{
                  items: [
                    { key: "refetch", label: "重新获取匹配", disabled: bad },
                    { key: "skip", label: "本次不更新元数据" },
                    ...(v.scan_error
                      ? [{ key: "retry", label: "修复文件后重试扫描" }]
                      : []),
                  ],
                  onClick: ({ key }) => {
                    if (key === "refetch") onRefetch(false);
                    else if (key === "retry") onRetry();
                    else onReview(key);
                  },
                }}
              >
                <Tooltip title="更多操作" trigger={["hover", "focus"]}>
                  <Button
                    aria-label="该项更多操作"
                    type="text"
                    icon={<MoreOutlined aria-hidden="true" />}
                  />
                </Tooltip>
              </Dropdown>
            </Space>
          )}
        </div>
      </div>
      <div className="import-card-feedback">
        {mismatch && (
          <div className="import-row-warning" role="status">
            网址尚未获取；当前结果仍来自上方网址。
          </div>
        )}
        {(input || showURLInput || url !== undefined) && (
          <div className="import-url-editor">
            <label htmlFor={`gallery-${v.id}`}>手工填写画廊网址</label>
            <div>
              <Input
                id={`gallery-${v.id}`}
                name="gallery_url"
                type="url"
                autoComplete="off"
                spellCheck={false}
                value={url ?? ""}
                disabled={disabled}
                placeholder="https://e-hentai.org/g/…"
                onChange={(e) => onURL(e.target.value)}
              />
              <Button
                disabled={disabled || !url?.trim()}
                onClick={() => onRefetch(false)}
              >
                获取匹配
              </Button>
              <Button
                disabled={disabled}
                onClick={() => {
                  onURL(undefined);
                  setInput(false);
                }}
              >
                取消
              </Button>
            </div>
          </div>
        )}
      </div>
      {v.error_code && !["no_match", "multiple_matches"].includes(v.status) && (
        <div className="import-row-error">
          {v.candidate &&
            v.requested_url &&
            v.requested_url !== v.candidate.url && (
              <span>
                新请求 {v.requested_url} 未获取成功；当前仍为原网址的候选。{" "}
              </span>
            )}

          {v.status === "no_match"
            ? "可粘贴画廊网址，或人工录入后确认。"
            : v.status === "multiple_matches"
              ? "请指定正确画廊，或人工录入。"
              : v.status === "file_error"
                ? "请先检查源文件；修复后可重试扫描，也可跳过本次元数据。"
                : v.candidate
                  ? "重新获取失败；保留上方原网址对应的候选，可重试或确认旧匹配。"
                  : "获取失败，可重新匹配或人工录入。"}
        </div>
      )}
      {expanded && fields && (
        <section
          id={`import-details-${v.id}`}
          className="import-expanded-metadata"
          aria-label="候选完整资料"
        >
          <dl>
            <div>
              <dt>站点标题</dt>
              <dd>{fields.title || "未提供"}</dd>
            </div>
            <div>
              <dt>原文标题</dt>
              <dd>{fields.title_original || "未提供"}</dd>
            </div>
            <div>
              <dt>分类</dt>
              <dd>
                {categories.find(([k]) => k === fields.category)?.[1] ??
                  fields.category ??
                  "未知"}
              </dd>
            </div>
            <div>
              <dt>评分</dt>
              <dd>{fields.rating ?? "未知"}</dd>
            </div>
            <div>
              <dt>语言</dt>
              <dd>
                {languages.find(([k]) => k === fields.language)?.[1] ??
                  fields.language ??
                  "未知"}
              </dd>
            </div>
            <div>
              <dt>发布时间</dt>
              <dd>
                {fields.published_at
                  ? new Intl.DateTimeFormat("zh-CN", {
                      dateStyle: "medium",
                      timeStyle: "short",
                    }).format(new Date(fields.published_at))
                  : "未知"}
              </dd>
            </div>
          </dl>
          {grouped
            .filter((g) => g.tags.length)
            .map((g) => (
              <div className="import-tag-group" key={g.namespace}>
                <strong>
                  {g.label} <span>{g.namespace}</span>
                </strong>
                <div>
                  {g.tags.map((t) => (
                    <Tooltip
                      key={`${t.namespace}:${t.name}`}
                      title={`${t.namespace}:${t.name}`}
                      trigger={["hover", "focus"]}
                    >
                      <Tag tabIndex={0}>{t.translation || t.name}</Tag>
                    </Tooltip>
                  ))}
                </div>
              </div>
            ))}
          {fields.description && <p>{fields.description}</p>}
          {v.candidate!.shared_editions > 0 && (
            <p className="import-row-warning">
              采纳会刷新另外{v.candidate!.shared_editions}
              个版本共享的来源，人工覆盖保留。
            </p>
          )}
        </section>
      )}
    </article>
  );
}
