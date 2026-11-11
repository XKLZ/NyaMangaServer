import { taskURL } from "../shared/consoleRoutes.mjs";
import { useEffect, useState, useRef } from "react";
import {
  Alert,
  App as AntApp,
  Button,
  Checkbox,
  Collapse,
  Dropdown,
  Input,
  Popover,
  Segmented,
  Space,
  Spin,
  Table,
  Typography,
} from "antd";
import {
  MoreOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
  LinkOutlined,
} from "@ant-design/icons";
import { catalog, request } from "../shared/api";
import type { Book, Listing, JobItem, Job } from "../shared/api";
import { metadata } from "../shared/metadata";
import type { Field } from "../shared/metadata";
import type { SourcePreview } from "./types";
import { fieldLabels, valueText } from "./metadataForm";
import {
  ActionNotice,
  ErrorNotice,
  FormLock,
  StateTag,
  time,
  useAction,
  useRemote,
} from "./common";
import { matchingIntent, sourceDraftDirty } from "./sourceFlow";
import type { MatchMode } from "./sourceFlow";
import { namespaces } from "./tagInput";
const remoteLabels: Record<string, string> = {
  remote_page_count: "远端页数",
  remote_size_bytes: "远端大小（字节）",
};
const sourcePreviewValue = (key: string, value: unknown) =>
  remoteLabels[key] && value == null ? "未知" : valueText(value);
export function SourceEditor({
  book,
  previewID,
  onDirty,
  onBusy,
  onSaved,
  navigate,
}: {
  book: Book;
  previewID: string;
  onDirty: (dirty: boolean) => void;
  onBusy: (busy: boolean) => void;
  onSaved: () => void;
  navigate: (path: string) => void;
}) {
  const [url, setURL] = useState("");
  const [force, setForce] = useState(false);
  const [expanded, setExpanded] = useState(!!previewID);
  const [mode, setMode] = useState<MatchMode>(
    previewID ? "gallery" : "automatic",
  );
  const [submittedURL, setSubmittedURL] = useState("");
  const [job, setJob] = useState("");
  const [preview, setPreview] = useState(previewID);
  const action = useAction();
  const [confirmationAttempt, setConfirmationAttempt] = useState(false);
  const confirmationRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (confirmationAttempt && action.error) {
      confirmationRef.current?.focus({ preventScroll: true });
      confirmationRef.current?.scrollIntoView({ block: "nearest" });
    }
  }, [confirmationAttempt, action.error]);
  const details = book.metadata;
  const { modal } = AntApp.useApp();
  useEffect(() => {
    onDirty(sourceDraftDirty(url, submittedURL));
    return () => onDirty(false);
  }, [url, submittedURL, onDirty]);
  useEffect(() => {
    onBusy(action.busy);
    return () => onBusy(false);
  }, [action.busy, onBusy]);
  const result = useRemote<SourcePreview>(
    `/books/${book.id}/source-previews/${preview}`,
    undefined,
    "GET",
    !!preview,
  );
  const [previewFailure, setPreviewFailure] = useState("");
  const previewState = result.data?.state;
  const previewJob = result.data?.job_id;
  const reloadPreview = result.reload;
  useEffect(() => {
    if (!preview || previewState !== "pending") return;
    const controller = new AbortController();
    let stopped = false;
    const poll = async () => {
      if (stopped) return;
      try {
        if (previewJob) {
          const task = await request<Job>(
            `/jobs/${previewJob}`,
            "GET",
            undefined,
            false,
            controller.signal,
          );
          if (controller.signal.aborted) return;
          if (task.state === "failed" || task.state === "cancelled") {
            setPreviewFailure(
              task.state === "failed"
                ? "来源预览任务失败，请查看任务原因后重新获取。原来源保持不变。"
                : "来源预览任务已取消，原来源保持不变。",
            );
            stopped = true;
            return;
          }
        }
        reloadPreview();
      } catch (error) {
        if (!controller.signal.aborted)
          setPreviewFailure(
            error instanceof Error ? error.message : "预览状态无法读取",
          );
      }
    };
    const timer = setInterval(() => void poll(), 1500);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [preview, previewState, previewJob, reloadPreview]);
  const setActionError = action.setError;
  useEffect(() => {
    if (!job) return;
    let live = true;
    const controller = new AbortController();
    const poll = async () => {
      try {
        const data = await request<Listing<JobItem>>(
          `/jobs/${job}/items?page=1&page_size=50`,
          "GET",
          undefined,
          false,
          controller.signal,
        );
        const item = data.items.find(
          (i) => i.preview_id && i.state === "completed",
        );
        if (live && item?.preview_id) {
          setPreview(item.preview_id);
          setJob("");
        } else if (live && data.items.some((i) => i.state === "failed")) {
          setActionError(
            new Error("来源预览失败，请到任务中心查看原因。原来源保持不变。"),
          );
          setJob("");
        }
      } catch (e) {
        if (live) setActionError(e);
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), 1500);
    return () => {
      live = false;
      controller.abort();
      clearInterval(timer);
    };
  }, [job, book.id, setActionError]);
  if (!details) return null;
  return (
    <FormLock busy={action.busy}>
      <Space orientation="vertical" className="full-width">
        <ActionNotice
          action={{
            ...action,
            error: confirmationAttempt ? null : action.error,
          }}
        />
        <div className="detail-section-heading">
          <StateTag state={details.state} />
        </div>
        <div className="source-summary">
          <img
            className="source-representative"
            src={
              catalog.cover(book) +
              `&cover_revision=${book.cover_revision ?? 0}`
            }
            alt="本地代表图"
          />
          <div>
            <Typography.Text type="secondary">
              当前关联来源 · EH / EX
            </Typography.Text>
            <Typography.Title level={3}>
              {details.source
                ? `画廊 #${details.source.external_id}`
                : "尚未关联来源"}
            </Typography.Title>
            <Typography.Paragraph type="secondary">
              匹配方式：
              {({
                manual: "人工网址",
                webinfo: "归档提示文件",
                hash: "代表图 SHA1",
              }[details.source?.match_method ?? ""] ??
                details.source?.match_method) ||
                "—"}
            </Typography.Paragraph>
            {details.source && (
              <a href={details.source.url} target="_blank" rel="noreferrer">
                <LinkOutlined aria-hidden="true" /> 查看原始画廊
              </a>
            )}
          </div>
        </div>
        <div className="source-actions">
          <Button
            type="primary"
            icon={<ReloadOutlined aria-hidden="true" />}
            aria-expanded={expanded}
            onClick={() => setExpanded((value) => !value)}
          >
            重新匹配
          </Button>
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                {
                  key: "unlink",
                  label: "解绑来源",
                  danger: true,
                  disabled: !details.source,
                },
                {
                  key: "clear",
                  label: "仅清除人工网址",
                  disabled: !details.manual_url,
                },
              ],
              onClick: ({ key }) =>
                modal.confirm({
                  title:
                    key === "unlink"
                      ? "解绑已采纳来源？"
                      : "仅清除保存的人工网址？",
                  content:
                    key === "unlink"
                      ? "人工编辑保留；未覆盖字段将回退到文件或默认值。"
                      : "只清除保存的网址，不解绑已采纳来源或修改人工字段。",
                  okText: "确认",
                  cancelText: "取消",
                  onOk: () =>
                    action.run(
                      async () => {
                        await request(
                          `/books/${book.id}/${key === "unlink" ? "source" : "manual-source"}`,
                          "DELETE",
                          key === "unlink"
                            ? {
                                expected_revision: book.revision,
                                selection_revision: details.selection_revision,
                              }
                            : {
                                selection_revision: details.selection_revision,
                              },
                        );
                        onSaved();
                      },
                      key === "unlink" ? "来源已解绑" : "人工网址已清除",
                    ),
                }),
            }}
          >
            <Button
              aria-label="更多来源操作"
              icon={<MoreOutlined aria-hidden="true" />}
            />
          </Dropdown>
        </div>
        <Collapse
          items={[
            {
              key: "source-data",
              label: "查看来源数据",
              children: details.source ? (
                <>
                  <Table
                    size="small"
                    pagination={false}
                    rowKey="key"
                    dataSource={(Object.keys(details.source.fields) as Field[])
                      .filter((key) => key !== "tags")
                      .map((key) => ({
                        key,
                        label:
                          fieldLabels[key] ||
                          (
                            {
                              remote_page_count: "远端页数",
                              remote_size_bytes: "远端大小（字节）",
                            } as Record<string, string>
                          )[key] ||
                          key,
                        value:
                          key.startsWith("remote_") &&
                          details.source!.fields[key] == null
                            ? "未知"
                            : valueText(details.source!.fields[key]),
                      }))}
                    columns={[
                      { title: "字段", dataIndex: "label" },
                      { title: "来源值", dataIndex: "value" },
                    ]}
                  />
                  {[
                    ...new Set(
                      (details.source.fields.tags ?? []).map(
                        (t) => t.namespace,
                      ),
                    ),
                  ].map((ns) => (
                    <div key={ns}>
                      <strong>
                        {namespaces.find(([key]) => key === ns)?.[1] ?? ns} ·{" "}
                        {ns}
                      </strong>
                      <p>
                        {(details.source!.fields.tags ?? [])
                          .filter((t) => t.namespace === ns)
                          .map((t) => t.name)
                          .join("、")}
                      </p>
                    </div>
                  ))}
                  <Typography.Text type="secondary">
                    保存的人工网址：{details.manual_url || "无"}
                  </Typography.Text>
                </>
              ) : (
                "暂无已采纳来源数据"
              ),
            },
          ]}
        />
        {expanded && (
          <div className="source-rematch">
            <Typography.Title level={5}>重新匹配来源</Typography.Title>
            <Segmented
              block
              aria-label="重新匹配方式"
              value={mode}
              options={[
                { label: "自动识别", value: "automatic" },
                { label: "指定画廊", value: "gallery" },
              ]}
              onChange={(value) => setMode(value as MatchMode)}
            />
            {mode === "automatic" ? (
              <>
                <Typography.Title level={5}>
                  通过已有匹配依据查找
                </Typography.Title>
                <Typography.Paragraph type="secondary">
                  沿用当前来源识别规则，成功结果按任务规则采纳；多候选到任务中心处理。
                </Typography.Paragraph>
                <Collapse
                  items={[
                    {
                      key: "advanced",
                      label: "高级选项",
                      children: (
                        <Space>
                          <Checkbox
                            checked={force}
                            onChange={(e) => setForce(e.target.checked)}
                          >
                            从网络重新获取数据
                          </Checkbox>
                          <Popover
                            trigger={["hover", "focus", "click"]}
                            content="跳过本地 SHA1 查找缓存，重新联网查找。未勾选时，缓存未命中仍可能联网。"
                          >
                            <Button
                              type="text"
                              size="small"
                              aria-label="重新获取数据说明"
                              icon={
                                <QuestionCircleOutlined aria-hidden="true" />
                              }
                            />
                          </Popover>
                        </Space>
                      ),
                    },
                  ]}
                />
                <Button
                  type="primary"
                  disabled={!!job}
                  onClick={() =>
                    void action.run(async () => {
                      setConfirmationAttempt(false);
                      const intent = matchingIntent("automatic", url, force);
                      const task = await metadata.fetch(
                        [book.id],
                        {},
                        intent.force_refresh,
                      );
                      navigate(taskURL(task.job_id));
                    }, "已启动自动识别")
                  }
                >
                  开始自动识别
                </Button>
              </>
            ) : (
              <div className="source-preview-form">
                <label htmlFor="gallery-preview-url">手动指定画廊网址</label>
                <Input
                  id="gallery-preview-url"
                  disabled={action.busy || !!job}
                  type="url"
                  placeholder="https://e-hentai.org/g/…/…/"
                  value={url}
                  onChange={(e) => setURL(e.target.value)}
                />
                <Typography.Text type="secondary">
                  指定画廊将直接联网获取；确认采纳前保留当前来源。
                </Typography.Text>
                <Button
                  type="primary"
                  loading={action.busy}
                  disabled={!url || !!job}
                  onClick={() =>
                    void action.run(async () => {
                      setConfirmationAttempt(false);
                      const intent = matchingIntent("gallery", url, force);
                      const task = await request<{ job_id: string }>(
                        `/books/${book.id}/source-previews`,
                        "POST",
                        {
                          ...intent,
                          expected_revision: book.revision,
                          selection_revision: details.selection_revision,
                          content_revision: book.content_revision,
                        },
                      );
                      setSubmittedURL(url);
                      setPreview("");
                      setPreviewFailure("");
                      setJob(task.job_id);
                    }, "正在获取来源预览；当前来源保持不变")
                  }
                >
                  获取来源预览
                </Button>
              </div>
            )}
            {job && (
              <Space>
                <Spin size="small" />
                <Button type="link" onClick={() => navigate(taskURL(job))}>
                  查看预览任务
                </Button>
              </Space>
            )}
          </div>
        )}
        <ErrorNotice error={result.error} />
        {preview && result.data && result.data.state !== "ready" && (
          <Alert
            type={
              previewFailure || result.data.state === "expired"
                ? "warning"
                : "info"
            }
            title={
              previewFailure ||
              (result.data.state === "expired"
                ? "来源预览已过期，请重新获取。"
                : result.data.state === "confirmed"
                  ? "此预览已经采纳。"
                  : "来源预览尚未完成，完成后可核对并采纳。")
            }
            action={
              result.data.job_id ? (
                <Button onClick={() => navigate(taskURL(result.data!.job_id))}>
                  查看任务
                </Button>
              ) : undefined
            }
          />
        )}
        {result.data && preview && result.data.state === "ready" && (
          <>
            <Alert
              type="info"
              title={`预览保留至 ${time(result.data.expires_at)}`}
              description={`共享来源关联 ${result.data.shared_book_count} 本。人工覆盖优先，确认前不会替换已采纳来源。`}
            />
            <Table
              rowKey="key"
              size="small"
              pagination={false}
              tableLayout="fixed"
              scroll={{ x: 720 }}
              dataSource={(Object.keys(details.fields) as Field[]).map(
                (key) => ({
                  key,
                  label: fieldLabels[key] || remoteLabels[key] || key,
                  old: sourcePreviewValue(key, details.fields[key]),
                  next: sourcePreviewValue(
                    key,
                    result.data!.fields?.[key] ??
                      result.data!.source?.fields?.[key],
                  ),
                  override: remoteLabels[key]
                    ? "来源统计 · 只读"
                    : details.overrides[key]
                      ? "人工值保留"
                      : "继承新来源",
                }),
              )}
              columns={[
                { title: "字段", dataIndex: "label", width: 100 },
                { title: "当前", dataIndex: "old", width: 240 },
                { title: "新来源", dataIndex: "next", width: 240 },
                { title: "采纳规则", dataIndex: "override", width: 140 },
              ]}
            />
            <div
              ref={confirmationRef}
              tabIndex={-1}
              role="group"
              aria-label="来源采纳结果"
            >
              <ActionNotice
                action={{
                  ...action,
                  notice: "",
                  error: confirmationAttempt ? action.error : null,
                }}
              />
              <Button
                type="primary"
                loading={action.busy}
                onClick={() =>
                  void action.run(async () => {
                    setConfirmationAttempt(true);
                    await request(
                      `/books/${book.id}/source-previews/${preview}/confirm`,
                      "POST",
                      {
                        expected_revision: result.data!.expected_revision,
                        selection_revision: result.data!.selection_revision,
                        content_revision: result.data!.content_revision,
                      },
                    );
                    setPreview("");
                    setURL("");
                    setSubmittedURL("");
                    onSaved();
                  }, "已采纳来源，人工编辑保持不变")
                }
              >
                确认采纳此来源
              </Button>
            </div>
          </>
        )}
      </Space>
    </FormLock>
  );
}
