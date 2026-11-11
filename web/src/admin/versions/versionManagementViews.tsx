import { useEffect, useState } from "react";
import { App as AntApp, Image, Pagination } from "antd";
import { versionLayout } from "./versionLayout";
import type { RefObject } from "react";
import { useRemote as useSource } from "../common";
// Never display rows from a previous library or page while the next query loads.
export function useVersionRemote<T>(
  path: string,
  body?: unknown,
  method = "GET",
  enabled = true,
) {
  const result = useSource<T>(path, body, method, enabled);
  const identity = (value: string) => {
    const url = new URL(value, window.location.origin);
    url.searchParams.delete("refresh");
    return url.pathname + url.search;
  };
  // Refreshing the same list retains its DOM and scroll; different conditions
  // must never temporarily expose rows or revisions from another selection.
  return {
    ...result,
    data:
      enabled && identity(result.loadedPath) === identity(path)
        ? result.data
        : null,
  };
}
export function Cover({
  id,
  content = 0,
  revision = 0,
  preview = true,
}: {
  id: string;
  content?: number;
  revision?: number;
  preview?: boolean;
}) {
  return (
    <Image
      preview={preview}
      className="version-cover"
      alt="漫画封面"
      width={64}
      height={88}
      src={`/api/v1/editions/${encodeURIComponent(id)}/cover?content_revision=${content}&cover_revision=${revision}`}
      fallback="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='64' height='88'%3E%3Crect width='64' height='88' fill='%23888'/%3E%3C/svg%3E"
    />
  );
}
export function Pager({
  data,
  onChange,
}: {
  data: { page: number; page_size: number; total: number } | null;
  onChange: (p: number) => void;
}) {
  return data && data.total > data.page_size ? (
    <Pagination
      className="version-pager"
      size="small"
      current={data.page}
      pageSize={data.page_size}
      total={data.total}
      showSizeChanger={false}
      onChange={onChange}
    />
  ) : null;
}

export function useVersionFeedback(action: {
  notice: string;
  setNotice: (value: string) => void;
}) {
  const { message } = AntApp.useApp();
  const { notice, setNotice } = action;
  useEffect(() => {
    if (notice) {
      void message.success(notice);
      setNotice("");
    }
  }, [notice, setNotice, message]);
}
export function useWorkspaceFrame(
  page: RefObject<HTMLDivElement | null>,
  workspace: RefObject<HTMLDivElement | null>,
) {
  const [frame, setFrame] = useState({ mode: versionLayout(0), height: 600 });
  useEffect(() => {
    const update = () => {
      if (!page.current || !workspace.current) return;
      const next = {
        mode: versionLayout(page.current.getBoundingClientRect().width),
        height: Math.max(
          240,
          Math.floor(
            window.innerHeight -
              workspace.current.getBoundingClientRect().top -
              16,
          ),
        ),
      };
      setFrame((previous) =>
        previous.mode === next.mode && previous.height === next.height
          ? previous
          : next,
      );
    };
    const observer = new ResizeObserver(update);
    if (page.current) observer.observe(page.current);
    if (workspace.current) observer.observe(workspace.current);
    window.addEventListener("resize", update);
    update();
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", update);
    };
  }, [page, workspace]);
  return frame;
}
