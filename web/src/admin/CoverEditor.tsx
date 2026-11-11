import { pageIdentity, parsingRevision } from "../shared/scanFlow";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Dropdown,
  Pagination,
  Segmented,
  Spin,
  Popover,
  Typography,
} from "antd";
import {
  CheckCircleOutlined,
  MoreOutlined,
  PictureOutlined,
  QuestionCircleOutlined,
} from "@ant-design/icons";
import { ApiError, catalog, request, upload } from "../shared/api";
import type { Book, Listing, Page } from "../shared/api";
import type { CoverSelection } from "./types";
import {
  ActionNotice,
  ErrorNotice,
  FormLock,
  useAction,
  useRemote,
  StateTag,
} from "./common";
import {
  canLoadPages,
  coverCommand,
  coverPageSize,
  coverPageReady,
  loadCoverImages,
  validateCoverFile,
} from "./coverFlow";
import type { Candidate } from "./coverFlow";

export function CoverEditor({
  book,
  active,
  onChanged,
  onDirty,
  onBusy,
}: {
  book: Book;
  active: boolean;
  onChanged: () => void;
  onDirty: (dirty: boolean) => void;
  onBusy: (busy: boolean) => void;
}) {
  const selected = useRemote<CoverSelection>(
    `/books/${book.id}/cover-selection`,
  );
  const parsing = parsingRevision(book);
  const imageVersion = `${book.content_revision}:${parsing}`;
  const [mode, setMode] = useState("pages");
  const [loadedPages, setLoadedPages] = useState<{
    content: number;
    parsing: number;
    data: Listing<Page>;
  } | null>(null);
  const listing =
    loadedPages?.content === book.content_revision &&
    loadedPages.parsing === parsing
      ? loadedPages.data
      : null;
  const [images, setImages] = useState<
    Record<string, { url?: string; error?: string }>
  >({});
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [candidate, setCandidate] = useState<Candidate | null>(null);
  const [conflict, setConflict] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const resources = useRef(new Set<string>());
  const cache = useRef(new Map<string, string>());
  const candidateRef = useRef<Candidate | null>(null);
  const currentKeys = useRef(new Set<string>());
  const serial = useRef({ value: 0 });
  const applying = useRef(false);
  const action = useAction();
  const selection = selected.data;
  const allowed = canLoadPages(book, active, true);
  const currentSrc =
    catalog.cover(book) +
    `&cover_revision=${selection?.revision ?? book.cover_revision ?? 0}`;
  function prune() {
    const keep = new Set(
      [...currentKeys.current].map((key) => cache.current.get(key)),
    );
    keep.add(candidateRef.current?.url);
    for (const url of resources.current)
      if (!keep.has(url)) {
        URL.revokeObjectURL(url);
        resources.current.delete(url);
      }
    for (const [key, url] of cache.current)
      if (!resources.current.has(url)) cache.current.delete(key);
  }
  function choose(value: Candidate | null) {
    candidateRef.current = value;
    setCandidate(value);
    setConflict(false);
    prune();
  }
  useEffect(() => {
    onDirty(!!candidate);
    return () => onDirty(false);
  }, [candidate, onDirty]);
  useEffect(() => {
    if (!active || book.library_disabled || book.trashed_at) {
      controller.current?.abort();
      serial.current.value++;
    }
    void Promise.resolve().then(() => {
      if (controller.current?.signal.aborted) setLoading(false);
    });
    const counter = serial.current;
    return () => {
      controller.current?.abort();
      counter.value++;
    };
  }, [
    active,
    book.content_revision,
    parsing,
    book.library_disabled,
    book.trashed_at,
  ]);
  useEffect(() => {
    const prefix = `${imageVersion}:`;
    for (const [key, url] of cache.current) {
      if (!key.startsWith(prefix)) {
        cache.current.delete(key);
        if (url !== candidateRef.current?.url) {
          URL.revokeObjectURL(url);
          resources.current.delete(url);
        }
      }
    }
  }, [imageVersion]);
  useEffect(() => {
    const urls = resources.current;
    return () => {
      controller.current?.abort();
      for (const url of urls) URL.revokeObjectURL(url);
      urls.clear();
    };
  }, []);
  async function loadPage(page: number, only?: Page) {
    if (!canLoadPages(book, active, true)) return;
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    const generation = ++serial.current.value;
    setLoading(true);
    setLoadError(null);
    try {
      const data =
        only && listing
          ? listing
          : await request<Listing<Page>>(
              `/books/${book.id}/pages?${pageIdentity(book)}&page=${page}&page_size=${coverPageSize}`,
              "GET",
              undefined,
              false,
              abort.signal,
            );
      if (abort.signal.aborted || generation !== serial.current.value) return;
      setLoadedPages({ content: book.content_revision, parsing, data });
      const key = (p: Page) => `${imageVersion}:${p.page_index}`;
      currentKeys.current = new Set(data.items.map(key));
      prune();
      setImages((previous) =>
        Object.fromEntries(
          data.items.map((p) => {
            const name = key(p);
            return [
              name,
              cache.current.has(name)
                ? { url: cache.current.get(name) }
                : (previous[name] ?? {}),
            ];
          }),
        ),
      );
      await loadCoverImages(
        (only ? [only] : data.items).filter((p) => !cache.current.has(key(p))),
        abort.signal,
        async (p, signal) => {
          const response = await fetch(
            `/api/v1/books/${book.id}/pages/${p.page_index}/image?${pageIdentity(book)}`,
            { credentials: "same-origin", signal },
          );
          if (!response.ok) {
            const details = await response.json().catch(() => null);
            throw new Error(
              details?.error?.code === "parsing_changed"
                ? "页序已变化，请刷新漫画资料后重新加载并核对。"
                : `内页图片加载失败（${response.status}）`,
            );
          }
          const blob = await response.blob();
          return URL.createObjectURL(blob);
        },
        (p, result) => {
          if (generation !== serial.current.value) {
            if (result.url) URL.revokeObjectURL(result.url);
            return;
          }
          if (result.url) {
            resources.current.add(result.url);
            cache.current.set(key(p), result.url);
          }
          setImages((previous) => ({ ...previous, [key(p)]: result }));
        },
        URL.revokeObjectURL,
      );
    } catch (error) {
      if (!abort.signal.aborted) setLoadError(error);
    } finally {
      if (generation === serial.current.value) setLoading(false);
    }
  }
  async function mutate(work: () => Promise<void>, notice: string) {
    if (applying.current) return;
    applying.current = true;
    onBusy(true);
    try {
      await action.run(work, notice);
    } finally {
      applying.current = false;
      onBusy(false);
    }
  }
  async function apply() {
    if (!candidate) return;
    await mutate(
      async () => {
        const command = coverCommand(candidate, book.content_revision, parsing);
        try {
          if (candidate.mode === "asset")
            await upload(
              `/books/${book.id}/cover-assets`,
              candidate.file!,
              candidate.expected_revision,
            );
          else
            await request(`/books/${book.id}/cover-selection`, "PUT", command);
          choose(null);
          selected.reload();
          onChanged();
        } catch (error) {
          if (error instanceof ApiError && error.status === 409) {
            setConflict(true);
            selected.reload();
          }
          throw error;
        }
      },
      candidate.mode === "asset" ? "人工封面已保存" : "已选择归档页作为封面",
    );
  }
  return (
    <FormLock busy={action.busy}>
      <div className="cover-manager">
        <div className="detail-section-heading">
          <StateTag state={selection?.state ?? "加载中"} />
          {selection?.state === "parsing_changed" && (
            <Alert
              type="warning"
              showIcon
              title="页序已变化，原人工封面选择仍保留"
              description="加载当前内页并核对后，重新选择封面；不会自动套用旧页码。"
            />
          )}
        </div>
        <ActionNotice action={action} />
        <ErrorNotice error={selected.error} retry={selected.reload} />
        <div className="cover-columns">
          <section>
            <Typography.Title level={5}>当前封面</Typography.Title>
            {!book.trashed_at &&
            (book.page_count > 0 || selection?.mode === "asset") ? (
              <img
                className="cover-current-image"
                src={currentSrc}
                alt="当前封面"
              />
            ) : (
              <div className="cover-current-image cover-empty">
                暂无可用封面
              </div>
            )}
            <p>
              来源：
              {selection?.mode === "asset"
                ? "人工上传"
                : selection?.mode === "page"
                  ? `归档第 ${selection.page_index} 页`
                  : "自动选择"}
            </p>
            <Typography.Title level={5}>
              待选封面预览{" "}
              <Popover
                trigger={["hover", "focus", "click"]}
                content="选择候选后在此预览，应用前不修改当前封面。"
              >
                <Button
                  type="text"
                  size="small"
                  aria-label="待选封面预览说明"
                  icon={<QuestionCircleOutlined />}
                />
              </Popover>
            </Typography.Title>
            {candidate ? (
              <>
                <img
                  className="cover-candidate-image"
                  src={candidate.url}
                  alt="待选封面"
                />
                <p>
                  {candidate.mode === "page"
                    ? `第 ${candidate.page_index} 页 · `
                    : "人工上传 · "}
                  {candidate.name}
                </p>
              </>
            ) : (
              <div className="cover-candidate-image cover-empty">
                暂无待选封面
              </div>
            )}
            {conflict && (
              <Alert
                type="warning"
                title="封面已变化，待选预览已保留"
                description="核对当前封面后重新确认，或重置预览。"
                action={
                  <Button
                    disabled={
                      !selection ||
                      candidate?.content_revision !== book.content_revision ||
                      (candidate?.mode === "page" &&
                        (candidate.parsing_revision ?? 1) !== parsing)
                    }
                    onClick={() => {
                      if (candidate && selection) {
                        choose({
                          ...candidate,
                          expected_revision: selection.revision,
                        });
                        action.setError(null);
                      }
                    }}
                  >
                    核对后保留候选
                  </Button>
                }
              />
            )}
            {candidate &&
              (candidate.content_revision !== book.content_revision ||
                (candidate.mode === "page" &&
                  (candidate.parsing_revision ?? 1) !== parsing)) && (
                <Alert
                  type="warning"
                  title="漫画内容或页序已变化，请重新加载并核对候选封面。"
                />
              )}
          </section>
          <section>
            <div className="detail-section-heading">
              <Typography.Title level={5}>选择封面</Typography.Title>
              <Button
                type="text"
                disabled={!candidate}
                onClick={() => choose(null)}
              >
                重置预览
              </Button>
            </div>
            <Segmented
              block
              aria-label="封面来源"
              options={[
                { label: "漫画内页", value: "pages" },
                { label: "人工上传", value: "upload" },
              ]}
              value={mode}
              onChange={(value) => {
                controller.current?.abort();
                serial.current.value++;
                setLoading(false);
                setMode(String(value));
              }}
            />
            {mode === "pages" ? (
              <>
                {!allowed && (
                  <Alert
                    type="warning"
                    title="书库停用、漫画在回收站或无内页，不能加载源页面。"
                  />
                )}
                {!coverPageReady(listing?.items, images, imageVersion) && (
                  <div className="cover-load">
                    <Button
                      disabled={!allowed || loading}
                      loading={loading && active}
                      onClick={() => void loadPage(listing?.page ?? 1)}
                    >
                      {listing ? "继续加载" : "加载图片"}
                    </Button>
                  </div>
                )}
                <ErrorNotice
                  error={loadError}
                  retry={() => void loadPage(listing?.page ?? 1)}
                />
                <div className="cover-page-grid">
                  {listing?.items.map((p) => {
                    const image = images[`${imageVersion}:${p.page_index}`];
                    const chosen =
                      candidate?.mode === "page" &&
                      candidate.content_revision === book.content_revision &&
                      (candidate.parsing_revision ?? 1) === parsing &&
                      candidate.page_index === p.page_index;
                    const applied =
                      selection?.mode === "page" &&
                      selection.content_revision === book.content_revision &&
                      (selection.parsing_revision ?? 1) === parsing &&
                      selection.page_index === p.page_index;
                    return (
                      <div key={p.page_index}>
                        <button
                          type="button"
                          className={
                            chosen ? "cover-page chosen" : "cover-page"
                          }
                          disabled={!image?.url || !selection || action.busy}
                          aria-label={`选择第 ${p.page_index} 页封面`}
                          aria-pressed={chosen}
                          onClick={() =>
                            choose({
                              mode: "page",
                              page_index: p.page_index,
                              url: image!.url!,
                              name: p.display_name,
                              expected_revision: selection!.revision,
                              content_revision: book.content_revision,
                              parsing_revision: parsing,
                            })
                          }
                        >
                          {image?.url ? (
                            <img
                              src={image.url}
                              alt={`第 ${p.page_index} 页`}
                              onError={() => {
                                const key = `${imageVersion}:${p.page_index}`;
                                cache.current.delete(key);
                                setImages((previous) => ({
                                  ...previous,
                                  [key]: { error: "图片无法显示，请重试" },
                                }));
                                prune();
                              }}
                            />
                          ) : (
                            <PictureOutlined aria-hidden="true" />
                          )}
                        </button>
                        <div className="cover-page-label">
                          {p.display_name}
                          {applied && (
                            <CheckCircleOutlined aria-label="当前已应用" />
                          )}
                        </div>
                        {image?.error && (
                          <Button
                            size="small"
                            disabled={loading}
                            onClick={() => void loadPage(listing.page, p)}
                          >
                            重试第 {p.page_index} 页
                          </Button>
                        )}
                      </div>
                    );
                  })}
                </div>
                {listing && (
                  <Pagination
                    className="cover-pagination"
                    current={listing.page}
                    pageSize={coverPageSize}
                    total={listing.total}
                    showSizeChanger={false}
                    onChange={(page) => void loadPage(page)}
                    disabled={!allowed || loading || action.busy}
                  />
                )}
                {loading && active && <Spin size="small" />}
              </>
            ) : (
              <label className="upload-field">
                上传人工封面
                <input
                  aria-label="上传人工封面"
                  type="file"
                  accept="image/jpeg,image/png,image/webp,image/bmp,image/x-ms-bmp"
                  disabled={!selection || action.busy}
                  onChange={(e) => {
                    const file = e.currentTarget.files?.[0];
                    e.currentTarget.value = "";
                    if (!file || !selection) return;
                    try {
                      validateCoverFile(file);
                      const url = URL.createObjectURL(file);
                      resources.current.add(url);
                      choose({
                        mode: "asset",
                        file,
                        url,
                        name: file.name,
                        expected_revision: selection.revision,
                        content_revision: book.content_revision,
                      });
                    } catch (error) {
                      action.setError(error);
                    }
                  }}
                />
                <Typography.Text type="secondary">
                  JPEG / PNG / WebP / BMP，最多10 MiB；点击应用后才上传。
                </Typography.Text>
              </label>
            )}
            <div className="cover-apply-actions">
              <Button
                disabled={!selection}
                onClick={() =>
                  void mutate(async () => {
                    await request(`/books/${book.id}/cover-selection`, "PUT", {
                      expected_revision: selection!.revision,
                      mode: "auto",
                    });
                    selected.reload();
                    onChanged();
                  }, "已恢复自动封面")
                }
              >
                恢复自动
              </Button>
              <Button
                type="primary"
                disabled={
                  !candidate ||
                  conflict ||
                  candidate.content_revision !== book.content_revision ||
                  (candidate.mode === "page" &&
                    (candidate.parsing_revision ?? 1) !== parsing)
                }
                loading={action.busy}
                onClick={() => void apply()}
              >
                应用封面
              </Button>
              <Dropdown
                trigger={["click"]}
                menu={{
                  items: [
                    {
                      key: "rebuild",
                      label: "重建封面（创建任务）",
                      disabled: !selection,
                    },
                  ],
                  onClick: () =>
                    void mutate(async () => {
                      await request(
                        `/books/${book.id}/cover-rebuilds`,
                        "POST",
                        { expected_revision: selection!.revision },
                      );
                    }, "封面重建已加入任务中心"),
                }}
              >
                <Button
                  aria-label="更多封面操作"
                  icon={<MoreOutlined aria-hidden="true" />}
                />
              </Dropdown>
            </div>
          </section>
        </div>
      </div>
    </FormLock>
  );
}
