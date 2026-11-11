import { AdminOnly } from "../../shared/permissions";
/** §2.1/§2.5: stable read-only relationships. SQL display view owns both count
 * and paging; evidence, exceptions and writes belong to the secondary editor. */
import { useEffect, useLayoutEffect, useRef } from "react";
import { Button, Empty, Input, Pagination, Skeleton } from "antd";
import type {
  CatalogDetail,
  CharacterRelation,
  RelationPage,
} from "../../shared/tagCatalog";
import { resourceEvent } from "../../shared/events";
import { useTagNavigation } from "./TagNavigation";
import { otherTag } from "./tagCatalogFlow";
import { TagCard } from "./TagCard";
import { TagReadError } from "./tagViews";
import { useTagPage } from "./useTagPage";
export function TagRelationDisplay({
  tag,
  library,
  search,
  active,
  refreshEpoch,
  onRoute,
  onOpen,
}: {
  tag: CatalogDetail;
  library: string;
  search: string;
  active: boolean;
  refreshEpoch: number;
  onRoute: (v: Record<string, string | number | null>) => void;
  onOpen: () => void;
}) {
  const navigation = useTagNavigation();
  const character = tag.namespace === "character";
  const params = new URLSearchParams(search),
    page = Math.max(1, Number(params.get("d_page")) || 1),
    q = character ? "" : params.get("d_q") || "";
  const obsolete =
    character &&
    ["d_q", "d_page", "d_sort", "d_order", "display_snapshot"].some((key) =>
      params.has(key),
    );
  const list = useTagPage<CharacterRelation, RelationPage>(
    `/tag-catalog/${encodeURIComponent(tag.id)}/relations`,
    {
      library_id: library,
      view: "display",
      page,
      page_size: 24,
      q,
      sort: "name",
      order: "asc",
    },
    !obsolete,
  );
  const reload = list.reload,
    restart = list.restart;
  useEffect(() => {
    if (!active) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = (event: Event) => {
      const follow =
        (event as CustomEvent<{ type?: string }>).detail?.type === "tag_follow";
      clearTimeout(timer);
      timer = setTimeout(follow ? restart : reload, 200);
    };
    window.addEventListener(resourceEvent, refresh);
    return () => {
      clearTimeout(timer);
      window.removeEventListener(resourceEvent, refresh);
    };
  }, [active, reload, restart]);
  useEffect(() => {
    if (refreshEpoch) restart();
  }, [refreshEpoch, restart]);
  const row = useRef<HTMLDivElement>(null),
    positions = useRef(new Map<string, number>()),
    scrollKey = `${page}:${q}`;
  useLayoutEffect(() => {
    const element = row.current;
    if (!active || !element || !list.data) return;
    element.scrollTo({ left: positions.current.get(scrollKey) || 0 });
  }, [active, scrollKey, list.data]);
  return (
    <section
      className="tag-display-relations"
      aria-label={tag.namespace === "parody" ? "关联角色" : "关联原作"}
    >
      <div className="tag-section-heading">
        <h3>{tag.namespace === "parody" ? "关联角色" : "关联原作"}</h3>
        <AdminOnly>
          <div>
            <Button
              type="link"
              size="small"
              onClick={() => onRoute({ relations: "edit" })}
            >
              编辑角色归属
            </Button>
            {(list.data?.unclassified_edition_count || 0) > 0 && (
              <Button
                type="link"
                size="small"
                onClick={() =>
                  onRoute({ relations: "edit", partition: "unclassified" })
                }
              >
                待核对
              </Button>
            )}
          </div>
        </AdminOnly>
      </div>
      {!character && (
        <Input.Search
          key={q}
          aria-label="搜索关联标签"
          placeholder="搜索关联标签…"
          defaultValue={q}
          allowClear
          maxLength={512}
          onSearch={(d_q) => onRoute({ d_q, d_page: 1 })}
        />
      )}
      <TagReadError error={list.error} restart={list.restart} />
      <div
        className={`tag-relation-cards ${character ? "tag-parody-wrap" : "tag-character-row"}`}
        ref={row}
        role="region"
        aria-label={character ? "关联原作卡片" : "关联角色卡片"}
        tabIndex={character ? undefined : 0}
        onScroll={(event) => {
          if (active)
            positions.current.set(scrollKey, event.currentTarget.scrollLeft);
        }}
        onKeyDown={(event) => {
          if (
            !character &&
            event.target === event.currentTarget &&
            ["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)
          ) {
            event.preventDefault();
            const element = event.currentTarget;
            const left =
              event.key === "Home"
                ? 0
                : event.key === "End"
                  ? element.scrollWidth
                  : element.scrollLeft +
                    (event.key === "ArrowLeft" ? -160 : 160);
            element.scrollTo({ left });
          }
        }}
        aria-busy={list.pending}
      >
        <div className="tag-relation-track">
          {list.pending && !list.data ? (
            <Skeleton active />
          ) : (
            list.data?.items.map((r) => {
              const target = otherTag(r, tag.id);
              return (
                <TagCard
                  key={target.id}
                  tag={target}
                  count={r.work_count}
                  onOpen={onOpen}
                  {...navigation.related(target.id)}
                />
              );
            })
          )}
        </div>
      </div>
      {list.data?.items.length === 0 && (
        <Empty
          description={
            tag.namespace === "parody" ? "暂无关联角色" : "暂无关联原作"
          }
        />
      )}
      <div className="tag-pager">
        <span>{list.data?.total ?? "—"} 个关联</span>
        <Pagination
          size="small"
          current={page}
          pageSize={24}
          total={list.data?.total || 0}
          showSizeChanger={false}
          onChange={(d_page) => onRoute({ d_page })}
        />
      </div>
    </section>
  );
}
