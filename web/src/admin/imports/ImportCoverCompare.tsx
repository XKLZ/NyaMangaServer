import { useEffect, useRef, useState } from "react";
import type { ReactNode, KeyboardEvent } from "react";
import { Button, Popover, Spin } from "antd";
import type { ImportMember } from "../../shared/imports";
import { Cover } from "../versions/versionManagementViews";

export function ImportCoverCompare({
  batch,
  member,
  children,
  remote,
  label,
  disabled = false,
}: {
  batch: string;
  member: ImportMember;
  children: ReactNode;
  remote?: { key: string; path: string };
  label?: string;
  disabled?: boolean;
}) {
  const trigger = useRef<HTMLButtonElement | null>(null);
  const popup = useRef<HTMLDivElement | null>(null);
  const key =
    remote?.key ?? `${batch}:${member.id}:${member.candidate?.revision ?? 0}`;
  const [open, setOpen] = useState(false),
    [result, setResult] = useState<{
      key: string;
      url?: string;
      error?: string;
      loading?: boolean;
    } | null>(null);
  const reading = useRef<{ key: string; controller: AbortController } | null>(
      null,
    ),
    resource = useRef<{ key: string; url: string } | null>(null);
  useEffect(
    () => () => {
      if (reading.current?.key === key) {
        reading.current.controller.abort();
        reading.current = null;
      }
      if (resource.current?.key === key) {
        URL.revokeObjectURL(resource.current.url);
        resource.current = null;
      }
    },
    [key],
  );
  function escape(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      trigger.current?.focus({ preventScroll: true });
      setOpen(false);
      reading.current?.controller.abort();
      reading.current = null;
    }
  }
  const visible = result?.key === key ? result : null;
  async function load() {
    if (
      (!remote && !member.candidate) ||
      visible?.url ||
      resource.current?.key === key ||
      reading.current?.key === key
    )
      return;
    reading.current?.controller.abort();
    const controller = new AbortController();
    reading.current = { key, controller };
    setResult({ key, loading: true });
    try {
      const response = await fetch(
        remote?.path ??
          `/api/v1/import-batches/${encodeURIComponent(batch)}/members/${encodeURIComponent(member.id)}/candidate-cover?candidate_revision=${member.candidate!.revision}`,
        { credentials: "same-origin", signal: controller.signal },
      );
      if (!response.ok)
        throw Error(
          response.status === 409
            ? "候选已变化，请刷新核对后查看。"
            : "远端封面暂不可用，仍可审核文字资料。",
        );
      const blob = await response.blob();
      if (
        controller.signal.aborted ||
        reading.current?.controller !== controller
      )
        return;
      if (blob.size > 2 * 1024 * 1024 || blob.type !== "image/png")
        throw Error("远端封面响应无效。");
      const url = URL.createObjectURL(blob);
      if (resource.current) URL.revokeObjectURL(resource.current.url);
      resource.current = { key, url };
      setResult({ key, url });
    } catch (error) {
      if (!controller.signal.aborted)
        setResult({
          key,
          error: error instanceof Error ? error.message : "封面读取失败",
        });
    } finally {
      if (reading.current?.controller === controller) reading.current = null;
    }
  }
  return (
    <Popover
      open={open && !disabled}
      onOpenChange={(value) => {
        if (disabled) return;
        setOpen(value);
        if (value) {
          if (!visible?.error) void load();
        } else {
          reading.current?.controller.abort();
          reading.current = null;
        }
      }}
      trigger={disabled ? [] : ["hover", "click"]}
      mouseEnterDelay={0.3}
      title="封面对比"
      content={
        <div
          ref={popup}
          className="import-cover-compare"
          onKeyDown={escape}
          onBlur={(event) => {
            if (
              !popup.current?.contains(event.relatedTarget) &&
              event.relatedTarget !== trigger.current
            ) {
              setOpen(false);
              reading.current?.controller.abort();
              reading.current = null;
            }
          }}
        >
          <div>
            <strong>本地封面</strong>
            <Cover
              id={member.edition_id}
              content={member.content_revision}
              revision={member.cover_revision}
              preview={false}
            />
          </div>
          <div>
            <strong>候选封面</strong>
            {visible?.url ? (
              <img
                src={visible.url}
                width={144}
                height={200}
                alt="远端候选封面"
              />
            ) : visible?.loading ? (
              <Spin aria-label="按需加载候选封面" />
            ) : (
              <span>{visible?.error ?? "没有可用的候选封面"}</span>
            )}
            {visible?.error && (
              <Button size="small" onClick={() => void load()}>
                重试封面
              </Button>
            )}
          </div>
        </div>
      }
    >
      <button
        type="button"
        disabled={disabled}
        ref={trigger}
        onKeyDown={escape}
        onFocus={() => {
          setOpen(true);
          if (!visible?.error) void load();
        }}
        onBlur={(event) => {
          if (!popup.current?.contains(event.relatedTarget)) {
            setOpen(false);
            reading.current?.controller.abort();
            reading.current = null;
          }
        }}
        className="import-local-cover"
        aria-label={label ?? `查看封面对比 ${member.filename}`}
        aria-expanded={open}
      >
        {children}
      </button>
    </Popover>
  );
}
