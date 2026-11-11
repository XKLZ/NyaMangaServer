import { randomIdentity } from "../../shared/randomIdentity";
import { usePermissions } from "../../shared/permissions";
/** §2.1/§3.6/§7.1: two independent, centered image frames. The frame is the
 * picker; information and delete remain separate native buttons. Ant tokens,
 * 8/16/24 spacing, contain images, no optimistic replacement or extra modal. */
import {
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Alert, Button, Popover, Spin } from "antd";
import { DeleteOutlined, InfoCircleOutlined } from "@ant-design/icons";
import { resourceEvent } from "../../shared/events";
import { writesPending } from "../../shared/api";
import { BusinessReadContext } from "../../shared/readBoundary";
import { tagImages } from "../../shared/tagImages";
import type { ImageRole, TagImage, TagImages } from "../../shared/tagImages";
import type { CatalogDetail } from "../../shared/tagCatalog";
import { DirtyContext, ErrorNotice } from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
import {
  imageFailureKind,
  imageRoleNames,
  imageRoles,
  tagImageContent,
  tagImageMedia,
  tagImagePreview,
} from "./tagImageInput";
type Pending = {
  role: ImageRole;
  expected: number;
  generation: number;
  controller: AbortController;
  file?: File;
  media?: string;
  preview?: string;
  removing?: boolean;
  phase: "reading" | "writing" | "failed";
  error?: unknown;
  reviewed?: TagImage;
  reviewing?: boolean;
};
export function TagImageEditor({
  tag,
  onChanged,
}: {
  tag: CatalogDetail;
  onChanged: () => void;
}) {
  const { admin, verified } = usePermissions();
  const id = tag.id,
    readable = useContext(BusinessReadContext),
    register = useContext(DirtyContext);
  const guardKey = useMemo(() => randomIdentity(), []);
  const state = useRetainedRemote<TagImages>(
    `/tag-catalog/${encodeURIComponent(id)}/images`,
  );
  const [pending, setPending] = useState<Partial<Record<ImageRole, Pending>>>(
      {},
    ),
    [committed, setCommitted] = useState<Partial<Record<ImageRole, TagImage>>>(
      {},
    ),
    [failed, setFailed] = useState<Partial<Record<ImageRole, string>>>({}),
    [notice, setNotice] = useState<Partial<Record<ImageRole, string>>>({}),
    [infoOpen, setInfoOpen] = useState<Partial<Record<ImageRole, boolean>>>({});
  const operations = useRef<Partial<Record<ImageRole, Pending>>>({}),
    gate = useRef({ active: false, readable: false }),
    sequence = useRef(0),
    latest = useRef<Partial<Record<ImageRole, TagImage>>>({}),
    inputs = useRef<Partial<Record<ImageRole, HTMLInputElement | null>>>({}),
    frames = useRef<Partial<Record<ImageRole, HTMLButtonElement | null>>>({}),
    timers = useRef<Partial<Record<ImageRole, ReturnType<typeof setTimeout>>>>(
      {},
    );
  const newest = (role: ImageRole) =>
    [state.data?.[role], tag.images?.[role], committed[role]]
      .filter((v): v is TagImage => !!v)
      .sort((a, b) => b.revision - a.revision)[0];
  const foreground = newest("foreground"),
    background = newest("background");
  useLayoutEffect(() => {
    latest.current = { foreground, background };
  }, [foreground, background]);
  useLayoutEffect(() => {
    const boundary = gate.current;
    boundary.active = true;
    const work = operations.current,
      feedback = timers.current;
    return () => {
      boundary.active = false;
      imageRoles.forEach((role) => {
        work[role]?.controller.abort();
        clearTimeout(feedback[role]);
      });
      register(guardKey, false);
    };
  }, [register, guardKey]);
  const owned = (op: Pending) =>
    gate.current.active &&
    operations.current[op.role]?.generation === op.generation;
  const publish = useCallback(
    (op: Pending) => {
      if (!gate.current.active) return;
      operations.current[op.role] = op;
      // Register synchronously, before the first file read can yield to navigation.
      register(guardKey, true);
      setPending((old) => ({ ...old, [op.role]: op }));
    },
    [register, guardKey, setPending],
  );
  function discard(role: ImageRole) {
    operations.current[role]?.controller.abort();
    delete operations.current[role];
    register(
      guardKey,
      imageRoles.some((key) => !!operations.current[key]),
    );
    setPending((old) => {
      const next = { ...old };
      delete next[role];
      return next;
    });
  }
  useLayoutEffect(() => {
    gate.current.readable = readable;
    if (readable) return;
    imageRoles.forEach((role) => {
      const op = operations.current[role];
      if (op?.phase === "reading") {
        op.controller.abort();
        void Promise.resolve().then(() => {
          if (owned(op))
            publish({
              ...op,
              phase: "failed",
              error: new Error("图片读取已中断，请重试或取消"),
            });
        });
      }
    });
  }, [readable, publish]);
  const reload = state.reload;
  useEffect(() => {
    const update = (event: Event) => {
      const change = (
        event as CustomEvent<{ type?: string; resource_id?: string }>
      ).detail;
      if (
        !change?.type ||
        (change.type === "tag_image" &&
          imageRoles.some((role) => change.resource_id === `${id}:${role}`))
      )
        reload();
    };
    window.addEventListener(resourceEvent, update);
    return () => window.removeEventListener(resourceEvent, update);
  }, [id, reload]);
  async function review(op: Pending) {
    if (!owned(op) || !gate.current.readable) return;
    const controller = new AbortController();
    op.controller.abort();
    const next = { ...op, controller, reviewing: true };
    publish(next);
    try {
      const current = (await tagImages.state(id, controller.signal))[op.role];
      if (!owned(next) || controller.signal.aborted) return;
      setCommitted((old) => ({ ...old, [op.role]: current }));
      publish({ ...next, reviewed: current, reviewing: false });
    } catch (error) {
      if (owned(next) && !controller.signal.aborted)
        publish({ ...next, error, reviewing: false });
    }
  }
  async function write(op: Pending) {
    if (!owned(op) || op.controller.signal.aborted) return;
    if (!gate.current.readable) {
      publish({
        ...op,
        phase: "failed",
        error: new Error("请在当前标签恢复显示后重试"),
      });
      return;
    }
    if (latest.current[op.role]?.revision !== op.expected) {
      const conflict = {
        ...op,
        phase: "failed" as const,
        error: { status: 409 },
        reviewed: latest.current[op.role],
      };
      publish(conflict);
      return;
    }
    const saving = {
      ...op,
      phase: "writing" as const,
      error: undefined,
      reviewed: undefined,
      reviewing: false,
    };
    publish(saving);
    try {
      // The global write lock begins synchronously when this call starts.
      const result = op.removing
        ? await tagImages.remove(id, op.role, op.expected)
        : await tagImages.save(id, op.role, op.expected, op.file!, op.media!);
      if (!owned(saving)) return;
      setCommitted((old) => ({ ...old, [op.role]: result }));
      discard(op.role);
      setNotice((old) => ({
        ...old,
        [op.role]: op.removing ? "已删除" : "已保存",
      }));
      clearTimeout(timers.current[op.role]);
      timers.current[op.role] = setTimeout(() => {
        if (gate.current.active)
          setNotice((old) => ({ ...old, [op.role]: "" }));
      }, 2400);
      reload();
      onChanged();
    } catch (error) {
      if (!owned(saving)) return;
      const failed = { ...saving, phase: "failed" as const, error };
      publish(failed);
      if (imageFailureKind(error) === "conflict") void review(failed);
    } finally {
      requestAnimationFrame(() => {
        if (
          gate.current.active &&
          gate.current.readable &&
          document.activeElement === document.body
        )
          frames.current[op.role]?.focus({ preventScroll: true });
      });
    }
  }
  function operation(
    role: ImageRole,
    file?: File,
    removing = false,
    expected?: number,
  ): Pending | null {
    if (
      !verified ||
      !state.data ||
      !gate.current.active ||
      !gate.current.readable ||
      writesPending() ||
      imageRoles.some((key) => operations.current[key]?.phase === "writing")
    )
      return null;
    const old = operations.current[role],
      base = latest.current[role];
    if (!base) return null;
    old?.controller.abort();
    clearTimeout(timers.current[role]);
    setNotice((value) => ({ ...value, [role]: "" }));
    const op: Pending = {
      role,
      generation: ++sequence.current,
      controller: new AbortController(),
      expected: expected ?? old?.expected ?? base.revision,
      file,
      removing,
      phase: "reading",
    };
    publish(op);
    return op;
  }
  async function choose(role: ImageRole, file: File) {
    const op = operation(role, file);
    if (!op) return;
    try {
      const media = await tagImageMedia(file);
      if (!owned(op) || op.controller.signal.aborted) return;
      const preview = await tagImagePreview(file, media, op.controller.signal);
      if (!owned(op) || op.controller.signal.aborted) return;
      const ready = { ...op, media, preview };
      publish(ready);
      await write(ready);
    } catch (error) {
      if (owned(op) && !op.controller.signal.aborted)
        publish({ ...op, phase: "failed", error });
    }
  }
  function retry(op: Pending, reviewed?: TagImage) {
    const next = operation(
      op.role,
      op.file,
      !!op.removing,
      reviewed?.revision ?? op.expected,
    );
    if (!next) return;
    if (op.file && !op.media) {
      void choose(op.role, op.file);
      return;
    }
    void write({ ...next, media: op.media, preview: op.preview });
  }
  const writing = imageRoles.some((role) => pending[role]?.phase === "writing");
  return (
    <section className="tag-image-editor" aria-label="标签图片">
      <ErrorNotice error={state.error} retry={reload} />
      <div className="tag-image-role-grid">
        {imageRoles.map((role) => {
          const image = role === "foreground" ? foreground : background,
            op = pending[role],
            name = imageRoleNames[role];
          const src = tagImageContent(id, image),
            conflict =
              !!op &&
              (imageFailureKind(op.error) === "conflict" ||
                image?.revision !== op.expected),
            canRetry =
              !!op &&
              (imageFailureKind(op.error) === "retryable" ||
                (!op.media && op.error instanceof Error)),
            tall = role === "foreground" && tag.namespace === "character";
          return (
            <section
              key={role}
              className={`tag-image-role ${tall ? "tag-image-role-character" : ""}`}
              aria-label={name}
              aria-busy={op?.phase === "writing" || op?.phase === "reading"}
            >
              <h3>{name}</h3>
              <div
                className="tag-image-frame"
                data-info-open={infoOpen[role] || undefined}
              >
                <button
                  ref={(node) => {
                    frames.current[role] = node;
                  }}
                  type="button"
                  className={`tag-role-preview tag-role-${role}`}
                  aria-label={
                    admin
                      ? `${image?.asset_id ? "替换" : "添加"}${name}`
                      : `${name}图片`
                  }
                  disabled={
                    !verified || !admin || !state.data || writing || !readable
                  }
                  onClick={() => inputs.current[role]?.click()}
                >
                  {src && failed[role] !== src && (
                    <img
                      src={src}
                      alt={name}
                      width={tall ? 512 : 1024}
                      height={tall ? 768 : 512}
                      onError={() =>
                        setFailed((old) => ({ ...old, [role]: src }))
                      }
                    />
                  )}
                  {admin && (
                    <span className="tag-image-picker-hint" aria-hidden="true">
                      <span>{image?.asset_id ? "点击更换" : "点击添加"}</span>
                    </span>
                  )}
                </button>
                <input
                  ref={(node) => {
                    inputs.current[role] = node;
                  }}
                  className="sr-only"
                  tabIndex={-1}
                  type="file"
                  aria-label={`选择${name}`}
                  accept="image/jpeg,image/png,image/webp,image/bmp,.jpg,.jpeg,.png,.webp,.bmp"
                  disabled={
                    !verified || !admin || !state.data || writing || !readable
                  }
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = "";
                    if (file) void choose(role, file);
                  }}
                />
                <div className="tag-image-corners">
                  <Popover
                    trigger={["hover", "focus", "click"]}
                    open={!!infoOpen[role]}
                    onOpenChange={(open) =>
                      setInfoOpen((old) => ({ ...old, [role]: open }))
                    }
                    content={
                      <div className="tag-image-info">
                        <p>
                          {image?.asset_id
                            ? `${image.width} × ${image.height} · ${image.format.toUpperCase()}`
                            : admin
                              ? "点击导入JPEG、PNG、WebP、BMP 格式图片 · 最多10 MiB"
                              : "未设置图片"}
                        </p>
                      </div>
                    }
                  >
                    <Button
                      type="text"
                      className="tag-image-info-button"
                      icon={<InfoCircleOutlined aria-hidden />}
                      aria-label={`${name}信息`}
                      onKeyDown={(event) => {
                        if (event.key === "Escape" && infoOpen[role]) {
                          event.stopPropagation();
                          setInfoOpen((old) => ({ ...old, [role]: false }));
                        }
                      }}
                    />
                  </Popover>
                  {admin && image?.asset_id && (
                    <Button
                      type="text"
                      className="tag-image-delete"
                      icon={<DeleteOutlined aria-hidden />}
                      aria-label={`删除${name}`}
                      disabled={!verified || writing || !readable}
                      onClick={(event) => {
                        event.stopPropagation();
                        const next = operation(role, undefined, true);
                        if (next) void write(next);
                      }}
                    />
                  )}
                </div>
                {(op?.phase === "reading" || op?.phase === "writing") && (
                  <span className="tag-image-progress" role="status">
                    <Spin size="small" />
                    {op.phase === "reading" ? "读取中…" : "保存中…"}
                  </span>
                )}
                {notice[role] && (
                  <span className="tag-image-feedback" role="status">
                    {notice[role]}
                  </span>
                )}
              </div>
              {src && failed[role] === src && (
                <Button
                  type="link"
                  size="small"
                  onClick={() => setFailed((old) => ({ ...old, [role]: "" }))}
                >
                  重试{name}显示
                </Button>
              )}
              {op?.phase === "failed" && (
                <div className="tag-image-failure">
                  {conflict ? (
                    <Alert
                      type="warning"
                      title="当前图片已变化，请核对后重试"
                    />
                  ) : (
                    <ErrorNotice error={op.error} />
                  )}
                  {op.file && (
                    <div className="tag-image-pending">
                      {op.preview && (
                        <img
                          src={op.preview}
                          alt={`待上传${name}`}
                          width={96}
                          height={64}
                        />
                      )}
                      <span>{op.file.name}</span>
                    </div>
                  )}
                  {conflict ? (
                    <>
                      <Button
                        size="small"
                        loading={op.reviewing}
                        onClick={() => void review(op)}
                      >
                        读取当前{name}
                      </Button>
                      {op.reviewed && (
                        <>
                          {op.reviewed.asset_id ? (
                            <img
                              className="tag-reviewed-image"
                              src={tagImageContent(id, op.reviewed)}
                              alt={`当前${name}`}
                              width={160}
                              height={80}
                            />
                          ) : (
                            <span>当前未设置{name}</span>
                          )}
                          <Button
                            size="small"
                            disabled={
                              op.reviewed.revision !== image?.revision ||
                              op.reviewing
                            }
                            onClick={() => retry(op, op.reviewed)}
                          >
                            核对后重试{name}
                          </Button>
                        </>
                      )}
                    </>
                  ) : (
                    canRetry && (
                      <Button size="small" onClick={() => retry(op)}>
                        重试{op.removing ? "删除" : "上传"}
                        {name}
                      </Button>
                    )
                  )}
                  <Button size="small" onClick={() => discard(role)}>
                    取消{name}操作
                  </Button>
                </div>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}
