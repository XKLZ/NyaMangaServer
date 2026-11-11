import { useTagNavigation } from "./TagNavigation";
import { originalTitle } from "../books/bookTitle";
/** §2.1/§3.4/§7.1: real Work identity leads. Small equal-ratio covers support
 * the name; no Edition counts or relationship diagnostics on these cards. */
import { useEffect, useRef, useState } from "react";
import { PictureOutlined } from "@ant-design/icons";
import { Empty, Input, Pagination, Skeleton } from "antd";
import { Link } from "react-router";
import type { TagWork } from "../../shared/tagCatalog";
import type { WorkCoverSummary } from "../../shared/organization";
import { resourceEvent } from "../../shared/events";
import { tagWorkInput, tagWorkspaceURL } from "./tagCatalogFlow";
import { TagReadError } from "./tagViews";
import { useTagPage } from "./useTagPage";
export function TagWorkCover({ cover }: { cover?: WorkCoverSummary | null }) {
  const [failed, setFailed] = useState("");
  const src = cover?.edition_id
    ? `/api/v1/editions/${encodeURIComponent(cover.edition_id)}/cover?content_revision=${cover.content_revision}&cover_revision=${cover.cover_revision}`
    : "";
  return (
    <span className="tag-work-cover">
      {src && failed !== src ? (
        <img
          src={src}
          width={64}
          height={88}
          alt=""
          loading="lazy"
          onError={() => setFailed(src)}
        />
      ) : (
        <span role="img" aria-label="暂无作品封面">
          <PictureOutlined aria-hidden />
        </span>
      )}
    </span>
  );
}
export function TagWorkCards({
  tag,
  search,
  library,
  active,
  refreshEpoch,
  onRoute,
  onSelect,
  onRows,
}: {
  tag: string;
  search: string;
  library: string;
  active: boolean;
  refreshEpoch: number;
  onRoute: (v: Record<string, string | number | null>) => void;
  onSelect: (work: TagWork) => void;
  onRows: (items: TagWork[]) => void;
}) {
  const input = tagWorkInput(search, library);
  const navigation = useTagNavigation();
  const list = useTagPage<TagWork>(
    `/tag-catalog/${encodeURIComponent(tag)}/works`,
    input,
  );
  useEffect(() => {
    if (list.data) onRows(list.data.items);
  }, [list.data, onRows]);
  const reload = list.reload,
    restart = list.restart;
  const previousActive = useRef(active);
  useEffect(() => {
    if (active && !previousActive.current) reload();
    previousActive.current = active;
  }, [active, reload]);
  useEffect(() => {
    if (!active) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const change = (event: Event) => {
      if (
        (event as CustomEvent<{ type?: string }>).detail?.type === "tag_follow"
      )
        return;
      clearTimeout(timer);
      timer = setTimeout(reload, 200);
    };
    window.addEventListener(resourceEvent, change);
    return () => {
      clearTimeout(timer);
      window.removeEventListener(resourceEvent, change);
    };
  }, [active, reload]);
  useEffect(() => {
    if (refreshEpoch) restart();
  }, [refreshEpoch, restart]);
  return (
    <section aria-label="关联作品" className="tag-works-section">
      <div className="tag-section-heading">
        <h3>关联作品</h3>
        <Input.Search
          key={input.q}
          aria-label="搜索关联作品"
          defaultValue={input.q}
          placeholder="搜索作品…"
          allowClear
          maxLength={512}
          onSearch={(w_q) => onRoute({ w_q })}
        />
      </div>
      <TagReadError error={list.error} restart={list.restart} />
      <div className="tag-work-grid" aria-busy={list.pending}>
        {list.pending && !list.data
          ? Array.from({ length: 6 }, (_, i) => (
              <div className="tag-work-card" key={i}>
                <Skeleton.Avatar active shape="square" size={64} />
                <Skeleton active title paragraph={{ rows: 1 }} />
              </div>
            ))
          : list.data?.items.map((work) => (
              <Link
                key={work.id}
                className="tag-work-card"
                aria-label={originalTitle(work)}
                data-work-id={work.id}
                state={navigation.stateFor(
                  tagWorkspaceURL(search, { work: work.id }),
                )}
                to={tagWorkspaceURL(search, { work: work.id })}
                onClick={() => onSelect(work)}
              >
                <TagWorkCover cover={work.cover} />
                <span className="tag-work-name">{originalTitle(work)}</span>
              </Link>
            ))}
      </div>
      {list.data?.items.length === 0 && <Empty description="暂无关联作品" />}
      <div className="tag-pager">
        <span>{list.data?.total ?? "—"} 个作品</span>
        <Pagination
          size="small"
          current={input.page}
          pageSize={24}
          total={list.data?.total || 0}
          showSizeChanger={false}
          onChange={(w_page) => onRoute({ w_page })}
        />
      </div>
    </section>
  );
}
