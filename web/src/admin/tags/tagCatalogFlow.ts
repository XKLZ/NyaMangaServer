import type {
  CatalogInput,
  DirectoryInput,
  CatalogEdition,
  CatalogTag,
  CharacterRelation,
  EditionRelation,
  TagCategory,
} from "../../shared/tagCatalog";
export const tagCategories = {
  parody: "原作",
  character: "角色",
  artist: "作者",
  group: "社团",
  content: "内容标签",
} as const;
export const tagPartitions = {
  all: "全部共现",
  related: "已归属",
  unclassified: "未归类",
  evidence: "原始证据",
  inferred: "唯一剩余匹配",
  competing: "归属竞争",
  suppressed: "逐本否决",
} as const;
export function catalogInput(search: string, library: string): CatalogInput {
  const q = new URLSearchParams(search),
    c = q.get("category") || "parody";
  const category = (c in tagCategories ? c : "parody") as TagCategory;
  return {
    library_id: library,
    category,
    namespace: category === "content" ? q.get("namespace") || "" : "",
    q: q.get("q") || "",
    sort: q.get("sort") === "usage" ? "usage" : "name",
    order: q.get("order") === "desc" ? "desc" : "asc",
    page: Math.max(1, Number(q.get("page")) || 1),
    page_size: 50,
  };
}
/** Follow scope belongs only to the directory, never common detail inputs. */
export function tagDirectoryInput(
  search: string,
  library: string,
): DirectoryInput {
  return {
    ...catalogInput(search, library),
    ...(new URLSearchParams(search).get("followed_only") === "true"
      ? { followed_only: true as const }
      : {}),
  };
}
/** Legacy directory URLs never query unused identities before normalization. */
export function normalizeTagDirectoryURL(search: string): string | null {
  const q = new URLSearchParams(search);
  if (!q.has("include_unused")) return null;
  q.delete("include_unused");
  q.delete("page");
  q.delete("catalog_snapshot");
  return `/admin/tags?${q}`;
}
export function tagWorkspaceURL(
  search: string,
  change: Record<string, string | number | null>,
) {
  const q = new URLSearchParams(search);
  const different = (key: string) =>
    key in change && String(change[key] ?? "") !== (q.get(key) || "");
  if (["category"].some(different))
    for (const key of [
      "tag",
      "parent_tag",
      "relations",
      "images",
      "d_page",
      "d_q",
      "work",
      "details",
      "w_page",
      "w_q",
      "w_order",
      "v_page",
      "v_q",
      "related_tag",
      "partition",
      "book",
      "preview",
      "r_page",
      "e_page",
      "r_q",
      "e_q",
    ])
      q.delete(key);
  if (different("tag"))
    for (const key of [
      "parent_tag",
      "relations",
      "images",
      "d_page",
      "d_q",
      "work",
      "details",
      "w_page",
      "w_q",
      "w_order",
      "v_page",
      "v_q",
      "related_tag",
      "partition",
      "book",
      "preview",
      "r_page",
      "e_page",
      "r_q",
      "e_q",
    ])
      q.delete(key);
  if (
    [
      "category",
      "q",
      "namespace",
      "sort",
      "order",
      "include_unused",
      "followed_only",
    ].some(different)
  ) {
    q.delete("page");
    q.delete("catalog_snapshot");
  }
  if (different("relations"))
    for (const key of [
      "r_page",
      "r_q",
      "related_tag",
      "partition",
      "e_page",
      "e_q",
      "relation_snapshot",
      "display_snapshot",
      "edition_snapshot",
    ])
      q.delete(key);
  if (different("d_q")) q.delete("d_page");
  if (["w_q", "w_order"].some(different)) q.delete("w_page");
  if (different("work")) {
    q.delete("book");
    q.delete("preview");
    q.delete("details");
    q.delete("v_page");
    q.delete("v_q");
  }
  if (different("v_q")) q.delete("v_page");
  if (["related_tag", "partition", "e_q"].some(different)) {
    q.delete("e_page");
    q.delete("edition_snapshot");
  }
  if (["r_q", "r_sort", "r_order"].some(different)) {
    q.delete("r_page");
    q.delete("relation_snapshot");
  }
  for (const [key, value] of Object.entries(change)) {
    if (value === null || value === "" || value === 1) q.delete(key);
    else q.set(key, String(value));
  }
  return `/admin/tags?${q}`;
}
export const tagName = (tag: Pick<CatalogTag, "translation" | "name">) =>
  tag.translation || tag.name;
export function relationPair(anchor: CatalogTag, other: CatalogTag) {
  if (anchor.placeholder || other.placeholder || !anchor.id || !other.id)
    throw Error("原创占位标签不能设置角色归属");
  if (anchor.namespace === "parody" && other.namespace === "character")
    return { parody: anchor, character: other };
  if (anchor.namespace === "character" && other.namespace === "parody")
    return { parody: other, character: anchor };
  throw Error("请选择一个原作和一个角色");
}
export const otherTag = (r: CharacterRelation, anchor: string) =>
  r.parody.id === anchor ? r.character : r.parody;
export const canSuppress = (r: EditionRelation) =>
  !!r.parody_id && !!r.character_id && (r.suppressed || r.decision === "auto");
export const translationOrigins: Record<string, string> = {
  manual: "人工译名",
  remote: "继承译库",
  original: "原文",
};
export const relationStates: Record<string, string> = {
  auto: "自动匹配",
  confirmed: "人工关联",
  excluded: "全局排除",
  single_parody: "单原作证据",
  evidence: "本库可靠证据",
  inferred: "本版本唯一剩余匹配",
  suppressed: "此版本已取消自动匹配",
  competing_parodies: "原作归属竞争",
  known_elsewhere: "可靠归属与本书矛盾",
  unclassified: "未归类",
};
export function relationLabel(state: string, supplied = "") {
  return relationStates[state] || supplied || "未知状态";
}

export type TagIntent = {
  kind: "global" | "edition";
  parody: string;
  character: string;
  parodyLabel: string;
  characterLabel: string;
  expected: number;
  decision: "auto" | "confirmed" | "excluded";
  edition?: string;
  editionLabel?: string;
  editionQuery?: string;
  suppressed?: boolean;
};
function revision(value: number) {
  if (!Number.isSafeInteger(value) || value < 0)
    throw Error("缺少可提交的版本，请重新核对");
  return value;
}
export function globalIntent(
  anchor: CatalogTag,
  target: CatalogTag,
  relation: CharacterRelation | undefined,
  decision: TagIntent["decision"],
): TagIntent {
  const pair = relationPair(anchor, target);
  if (
    relation &&
    (relation.parody.id !== pair.parody.id ||
      relation.character.id !== pair.character.id)
  )
    throw Error("关系目标已变化，请重新选择");
  return {
    kind: "global",
    parody: pair.parody.id,
    character: pair.character.id,
    parodyLabel: tagName(pair.parody),
    characterLabel: tagName(pair.character),
    expected: revision(relation?.revision ?? 0),
    decision,
  };
}
export function suppressionIntent(
  edition: CatalogEdition,
  row: EditionRelation,
  label = edition.title,
): TagIntent {
  if (!canSuppress(row)) throw Error("此项没有可提交的原作目标");
  return {
    kind: "edition",
    parody: row.parody_id,
    character: row.character_id,
    parodyLabel: row.parody_translation || row.parody_name,
    characterLabel: row.character_translation || row.character_name,
    expected: revision(row.suppression_revision),
    decision: row.decision,
    edition: edition.id,
    editionLabel: label,
    editionQuery: edition.title,
    suppressed: !row.suppressed,
  };
}

/** Explicit whitelist: a Work query must never inherit catalog/relationship filters. */
export function tagWorkInput(
  search: string,
  library: string,
): Pick<
  CatalogInput,
  "library_id" | "q" | "sort" | "order" | "page" | "page_size"
> {
  const q = new URLSearchParams(search),
    page = Number(q.get("w_page") || 1);
  return {
    library_id: library,
    q: q.get("w_q") || "",
    sort: "name",
    order: q.get("w_order") === "desc" ? "desc" : "asc",
    page: Number.isSafeInteger(page) && page > 0 ? page : 1,
    page_size: 24,
  };
}
