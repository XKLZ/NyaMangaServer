import { useTagNavigation } from "./TagNavigation";
import { originalTitle } from "../books/bookTitle";
import { useEffect, useState } from "react";
import { PlusOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Empty,
  Input,
  Pagination,
  Select,
  Space,
  Spin,
  Table,
} from "antd";
import { Link } from "react-router";
import { resourceEvent } from "../../shared/events";
import { tagCatalogPath } from "../../shared/tagCatalog";
import type {
  CatalogDetail,
  CatalogEdition,
  CharacterRelation,
  EditionPage,
  RelationPage,
  TagPartition,
} from "../../shared/tagCatalog";
import { ErrorNotice, StateTag, useDirty } from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
import {
  canSuppress,
  otherTag,
  tagName,
  tagPartitions,
  tagWorkspaceURL,
} from "./tagCatalogFlow";
import { globalIntent, suppressionIntent } from "./tagCatalogFlow";
import type { TagIntent } from "./tagCatalogFlow";
import { TagIntentDialog } from "./TagIntentDialog";
import { TagRelationPicker } from "./TagRelationPicker";
import { RelationState, TagReadError } from "./tagViews";
import { useTagPage } from "./useTagPage";
type Route = (values: Record<string, string | number | null>) => void;
export function TagRelations({
  id,
  search,
  library,
  detail,
  active,
  onOverlay,
  onOpenEdition,
  onChanged,
  onRoute,
  refreshEpoch,
}: {
  id: string;
  search: string;
  library: string;
  detail: CatalogDetail;
  active: boolean;
  onOverlay: (value: boolean) => void;
  onOpenEdition: () => void;
  onChanged: () => void;
  onRoute: Route;
  refreshEpoch: number;
}) {
  const params = new URLSearchParams(search);
  const navigation = useTagNavigation();
  const relational =
    !!detail && ["parody", "character"].includes(detail.namespace);
  const [intent, setIntent] = useState<TagIntent | null>(null),
    [picker, setPicker] = useState(false);
  useDirty(!!intent || picker, { keepTagBook: true, tagID: id });
  useEffect(() => {
    onOverlay(!!intent || picker);
    return () => onOverlay(false);
  }, [intent, picker, onOverlay]);
  const related = params.get("related_tag") || "",
    partition = (params.get("partition") || "all") as TagPartition;
  const other = useRetainedRemote<CatalogDetail>(
    tagCatalogPath(`/tag-catalog/${encodeURIComponent(related)}`, {
      library_id: library,
    }),
    !!related && relational,
  );
  const validRelated =
    !!related &&
    !!other.data &&
    ((detail?.namespace === "parody" && other.data.namespace === "character") ||
      (detail?.namespace === "character" && other.data.namespace === "parody"));
  const validPartition =
    partition in tagPartitions &&
    (["all", "unclassified"].includes(partition) || validRelated);
  const rPage = Math.max(1, Number(params.get("r_page")) || 1),
    ePage = Math.max(1, Number(params.get("e_page")) || 1);
  const relations = useTagPage<CharacterRelation, RelationPage>(
    `/tag-catalog/${encodeURIComponent(id)}/relations`,
    {
      library_id: library,
      view: "all",
      q: params.get("r_q") || "",
      sort: params.get("r_sort") === "usage" ? "usage" : "name",
      order: params.get("r_order") === "desc" ? "desc" : "asc",
      page: rPage,
      page_size: 25,
    },
    relational,
  );
  const editions = useTagPage<CatalogEdition, EditionPage>(
    `/tag-catalog/${encodeURIComponent(id)}/editions`,
    {
      library_id: library,
      q: params.get("e_q") || "",
      sort: "name",
      order: "asc",
      page: ePage,
      page_size: 25,
      ...(relational
        ? { partition, ...(validRelated ? { related_tag_id: related } : {}) }
        : {}),
    },
    !!detail && validPartition && (!related || validRelated),
  );
  const reloadRelations = relations.reload,
    reloadEditions = editions.reload,
    reloadOther = other.reload,
    restartRelations = relations.restart,
    restartEditions = editions.restart;
  useEffect(() => {
    if (!active) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const listener = (event: Event) => {
      const follow =
        (event as CustomEvent<{ type?: string }>).detail?.type === "tag_follow";
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (follow) restartRelations();
        else {
          reloadRelations();
          reloadEditions();
        }
        reloadOther();
      }, 200);
    };
    window.addEventListener(resourceEvent, listener);
    return () => {
      clearTimeout(timer);
      window.removeEventListener(resourceEvent, listener);
    };
  }, [active, reloadRelations, restartRelations, reloadEditions, reloadOther]);
  useEffect(() => {
    if (refreshEpoch) {
      restartRelations();
      restartEditions();
      reloadOther();
    }
  }, [refreshEpoch, restartRelations, restartEditions, reloadOther]);
  const changed = () => {
    restartRelations();
    restartEditions();
    reloadOther();
    onChanged();
  };

  return (
    <div className="tag-details">
      {relational && (
        <section aria-label="原作角色关系" className="tag-relations">
          <div className="tag-section-heading">
            <h3>{detail.namespace === "parody" ? "关联角色" : "关联原作"}</h3>
            <Button
              aria-label="添加关联"
              icon={<PlusOutlined aria-hidden />}
              disabled={detail.placeholder}
              onClick={() => setPicker(true)}
            >
              添加关联
            </Button>
          </div>
          {detail.placeholder && (
            <Alert type="info" title="原创占位标签只读，不参与角色归属。" />
          )}
          <div className="tag-relation-filters">
            <Input.Search
              key={`r:${params.get("r_q") || ""}`}
              aria-label="搜索关系原文或译名"
              placeholder="搜索关系原文或译名…"
              defaultValue={params.get("r_q") || ""}
              allowClear
              maxLength={512}
              onSearch={(r_q) => onRoute({ r_q })}
            />
            <Select
              aria-label="关系排序"
              value={`${params.get("r_sort") === "usage" ? "usage" : "name"}:${params.get("r_order") === "desc" ? "desc" : "asc"}`}
              options={[
                { value: "name:asc", label: "名称 · 升序" },
                { value: "usage:desc", label: "使用数 · 降序" },
              ]}
              onChange={(v) => {
                const [r_sort, r_order] = v.split(":");
                onRoute({ r_sort, r_order });
              }}
            />
          </div>
          <TagReadError error={relations.error} restart={relations.restart} />
          <div className="tag-relation-list">
            {relations.data?.items.map((r) => {
              const target = otherTag(r, id);
              return (
                <article key={target.id} className="tag-relation-row">
                  <div className="tag-section-heading">
                    <div className="tag-identity">
                      <Link
                        className="tag-name"
                        to={navigation.related(target.id).to}
                        state={navigation.related(target.id).state}
                        replace
                        onClick={(event) => {
                          if (target.id === id) event.preventDefault();
                          else onOpenEdition();
                        }}
                      >
                        {tagName(target)}
                      </Link>
                      <span className="tag-original">
                        {target.namespace}:{target.name}
                      </span>
                    </div>
                    <RelationState state={r.state} label={r.state_label} />
                  </div>
                  <div className="tag-relation-counts">
                    <span>
                      已归属 {r.work_count} 作品 / {r.edition_count} 版本
                    </span>
                    <span>
                      原始证据 {r.evidence_edition_count} · 可用证据{" "}
                      {r.seed_edition_count}
                    </span>
                    <span>
                      唯一剩余匹配 {r.inferred_edition_count} · 竞争{" "}
                      {r.competing_edition_count} · 逐本否决{" "}
                      {r.suppressed_edition_count}
                    </span>
                  </div>
                  <Space wrap size={8}>
                    <Button
                      size="small"
                      onClick={() =>
                        onRoute({
                          related_tag: target.id,
                          partition: "related",
                        })
                      }
                    >
                      查看漫画
                    </Button>
                    <Button
                      size="small"
                      disabled={
                        relations.pending ||
                        !!relations.error ||
                        detail.placeholder ||
                        target.placeholder ||
                        r.decision === "confirmed"
                      }
                      onClick={() =>
                        setIntent(globalIntent(detail, target, r, "confirmed"))
                      }
                    >
                      将此角色关联到该原作
                    </Button>
                    <Button
                      size="small"
                      disabled={
                        relations.pending ||
                        !!relations.error ||
                        detail.placeholder ||
                        target.placeholder ||
                        r.decision === "excluded"
                      }
                      onClick={() =>
                        setIntent(globalIntent(detail, target, r, "excluded"))
                      }
                    >
                      排除此关联
                    </Button>
                    <Button
                      size="small"
                      disabled={
                        relations.pending ||
                        !!relations.error ||
                        detail.placeholder ||
                        target.placeholder ||
                        r.decision === "auto"
                      }
                      onClick={() =>
                        setIntent(globalIntent(detail, target, r, "auto"))
                      }
                    >
                      恢复自动匹配
                    </Button>
                  </Space>
                </article>
              );
            })}
            {relations.pending && !relations.data && <Spin />}
            {relations.data?.items.length === 0 && (
              <Empty description="暂无关联关系" />
            )}
          </div>
          <div className="tag-pager">
            <span>{relations.data?.total ?? 0} 个关系</span>
            <Pagination
              size="small"
              current={rPage}
              total={relations.data?.total || 0}
              pageSize={25}
              showSizeChanger={false}
              onChange={(r_page) => onRoute({ r_page })}
            />
          </div>
        </section>
      )}
      <section aria-label="关联漫画" className="tag-editions">
        <h3>关联漫画</h3>
        {relational && (
          <>
            <div className="tag-related-scope">
              {related ? (
                <>
                  <span>
                    关系范围：{other.data ? tagName(other.data) : "读取中…"}
                  </span>
                  <Button
                    size="small"
                    onClick={() =>
                      onRoute({ related_tag: null, partition: null })
                    }
                  >
                    清除关系范围
                  </Button>
                </>
              ) : (
                <span>当前标签的全部版本</span>
              )}
            </div>
            <ErrorNotice error={other.error} retry={other.reload} />
            <Select
              aria-label="漫画关系分区"
              value={partition}
              options={Object.entries(tagPartitions).map(([value, label]) => ({
                value,
                label,
                disabled:
                  !["all", "unclassified"].includes(value) && !validRelated,
              }))}
              onChange={(partition) => onRoute({ partition })}
            />
            {!validPartition && (
              <Alert
                type="warning"
                title="此分区需要先选择有效的原作与角色关系。"
              />
            )}
            <Button
              type="link"
              onClick={() =>
                onRoute({ related_tag: null, partition: "unclassified" })
              }
            >
              查看未归类（{detail.unclassified_edition_count}）
            </Button>
          </>
        )}
        <Input.Search
          key={`e:${params.get("e_q") || ""}`}
          aria-label="搜索关联漫画"
          placeholder="搜索漫画标题…"
          defaultValue={params.get("e_q") || ""}
          allowClear
          maxLength={512}
          onSearch={(e_q) => onRoute({ e_q })}
        />
        <TagReadError error={editions.error} restart={editions.restart} />
        <Table<CatalogEdition>
          rowKey="id"
          size="small"
          pagination={false}
          dataSource={editions.data?.items || []}
          loading={editions.pending && !editions.data}
          columns={[
            {
              title: "漫画",
              render: (_, edition) => (
                <div className="tag-edition-info">
                  <Link
                    state={navigation.stateFor(
                      tagWorkspaceURL(search, { book: edition.id }),
                    )}
                    onClick={onOpenEdition}
                    to={tagWorkspaceURL(search, {
                      book: edition.id,
                    })}
                  >
                    {originalTitle(edition)}
                  </Link>
                  <StateTag state={edition.file_state} />
                  {edition.relations.map((r, i) => (
                    <div
                      className="tag-edition-relation"
                      key={`${r.parody_id}:${r.character_id}:${i}`}
                    >
                      <span>
                        {r.parody_translation ||
                          r.parody_name ||
                          "无可提交的原作目标"}{" "}
                        · {r.character_translation || r.character_name}
                      </span>
                      <RelationState state={r.state} label={r.state_label} />
                      {canSuppress(r) && (
                        <Button
                          size="small"
                          disabled={
                            detail.library_disabled ||
                            editions.pending ||
                            !!editions.error
                          }
                          onClick={() =>
                            setIntent(
                              suppressionIntent(
                                edition,
                                r,
                                originalTitle(edition),
                              ),
                            )
                          }
                        >
                          {r.suppressed
                            ? "恢复此版本自动匹配"
                            : "仅此版本取消自动匹配"}
                        </Button>
                      )}
                    </div>
                  ))}
                </div>
              ),
            },
          ]}
          locale={{ emptyText: <Empty description="此分区暂无漫画" /> }}
        />
        <div className="tag-pager">
          <span>
            {editions.data?.work_count ?? 0} 个作品 ·{" "}
            {editions.data?.total ?? 0} 个版本
          </span>
          <Pagination
            size="small"
            current={ePage}
            total={editions.data?.total || 0}
            pageSize={25}
            showSizeChanger={false}
            onChange={(e_page) => onRoute({ e_page })}
          />
        </div>
      </section>
      {picker && (
        <TagRelationPicker
          anchor={detail}
          library={library}
          onClose={() => setPicker(false)}
          onIntent={(v) => {
            setIntent(v);
            setPicker(false);
          }}
        />
      )}
      {intent && (
        <TagIntentDialog
          initial={intent}
          anchor={id}
          library={library}
          onClose={() => setIntent(null)}
          onChanged={changed}
        />
      )}
    </div>
  );
}
