import type { Book } from "../shared/api";
import type {
  Field,
  EditableField,
  PropertyField,
  Override,
  TagName,
} from "../shared/metadata";

export const fieldLabels: Record<Field | PropertyField, string> = {
  title: "其他标题",
  title_original: "原文标题",
  category: "分类",
  rating: "评分",
  published_at: "发布时间",
  description: "描述",
  language: "语言",
  languages: "语言",
  translated: "翻译状态",
  color_mode: "颜色",
  censorship: "修正",
  extraneous_ads: "额外广告",
  tags: "标签",
};
export type Draft = Partial<Record<EditableField, Override>>;
export type EditorState = {
  base: Book;
  draft: Draft;
  pending: Record<string, string>;
};
export const tagKey = (tag: TagName) =>
  `${tag.namespace.trim().toLowerCase()}:${tag.name.trim().replace(/\s+/g, " ").toLowerCase()}`;
export function sameValue(key: EditableField, a: unknown, b: unknown) {
  if (key === "tags")
    return (
      JSON.stringify(((a as TagName[]) ?? []).map(tagKey).sort()) ===
      JSON.stringify(((b as TagName[]) ?? []).map(tagKey).sort())
    );
  if (key === "languages")
    return (
      JSON.stringify(Array.isArray(a) && !a.length ? null : (a ?? null)) ===
      JSON.stringify(Array.isArray(b) && !b.length ? null : (b ?? null))
    );
  return (a ?? "") === (b ?? "");
}
export function isDirty(state: EditorState) {
  return (
    Object.keys(state.draft).length > 0 ||
    Object.values(state.pending).some(Boolean)
  );
}
export const propertyFields: PropertyField[] = [
  "languages",
  "translated",
  "color_mode",
  "censorship",
  "extraneous_ads",
];
export function isPropertyField(key: string): key is PropertyField {
  return propertyFields.includes(key as PropertyField);
}
export function effectiveValue(book: Book, key: EditableField) {
  return isPropertyField(key)
    ? book.metadata?.properties?.[key].value
    : book.metadata?.fields[key];
}
export function inheritedValue(book: Book, key: EditableField) {
  if (isPropertyField(key))
    return book.metadata?.properties?.[key].automatic.value ?? null;
  return book.metadata?.source?.fields[key] ?? (key === "tags" ? [] : null);
}
export function displayedValue(
  state: EditorState,
  key: EditableField,
): unknown {
  const edit = state.draft[key];
  if (!edit) return effectiveValue(state.base, key);
  return edit.op === "inherit"
    ? inheritedValue(state.base, key)
    : edit.op === "empty"
      ? key === "tags"
        ? []
        : null
      : edit.value;
}
export type EditorAction =
  | { type: "change"; key: EditableField; value: unknown }
  | { type: "restore" | "undo"; key: EditableField }
  | { type: "pending"; namespace: string; value: string }
  | { type: "receive" | "saved" | "rebase" | "discard"; book: Book };
export function editorReducer(
  state: EditorState,
  action: EditorAction,
): EditorState {
  if (action.type === "receive")
    return isDirty(state)
      ? state
      : { base: action.book, draft: {}, pending: {} };
  if (action.type === "saved" || action.type === "discard")
    return { base: action.book, draft: {}, pending: {} };
  if (action.type === "rebase") return { ...state, base: action.book };
  if (action.type === "pending")
    return {
      ...state,
      pending: { ...state.pending, [action.namespace]: action.value },
    };
  const draft = { ...state.draft };
  if (action.type === "undo") delete draft[action.key];
  if (action.type === "restore") {
    if (
      state.base.metadata?.overrides[action.key] ||
      (isPropertyField(action.key) &&
        ["manual", "legacy_manual"].includes(
          state.base.metadata?.properties?.[action.key].origin ?? "",
        ))
    )
      draft[action.key] = { op: "inherit" };
    else delete draft[action.key];
  }
  if (action.type === "change") {
    if (
      sameValue(
        action.key,
        action.value,
        effectiveValue(state.base, action.key),
      )
    )
      delete draft[action.key];
    else {
      const empty =
        action.value == null ||
        (typeof action.value === "string" && !action.value.trim()) ||
        (Array.isArray(action.value) && !action.value.length);
      draft[action.key] = empty
        ? { op: "empty" }
        : { op: "set", value: action.value };
    }
  }
  return {
    ...state,
    draft,
    pending:
      (action.type === "undo" || action.type === "restore") &&
      action.key === "tags"
        ? {}
        : state.pending,
  };
}
export function savePayload(state: EditorState) {
  if (
    Object.keys(state.draft).some(isPropertyField) &&
    !state.base.metadata?.properties
  )
    throw new Error("结构化属性接口尚未就绪，请等待 API 升级后重新加载。");
  if (Object.values(state.pending).some(Boolean))
    throw new Error("请先添加或清除标签输入框中的文字，再保存修改。");
  if (!Object.keys(state.draft).length) throw new Error("没有需要保存的修改。");
  const limits: Partial<Record<EditableField, number>> = {
    title: 512,
    title_original: 512,
    category: 128,
    description: 8192,
  };
  for (const [key, edit] of Object.entries(state.draft)) {
    if (edit?.op !== "set") continue;
    if (key === "languages") {
      if (
        Array.isArray(edit.value) &&
        edit.value.some(
          (v) =>
            typeof v === "string" &&
            ["translated", "rewrite"].includes(
              v.normalize("NFKC").trim().replace(/\s+/g, " ").toLowerCase(),
            ),
        )
      )
        throw new Error(
          "translated / rewrite 是翻译或改写状态，不能作为语言；请修正语言输入。",
        );
      if (
        !Array.isArray(edit.value) ||
        !edit.value.length ||
        edit.value.length > 200 ||
        edit.value.some(
          (v) =>
            typeof v !== "string" ||
            !v.trim() ||
            [...v].length > 64 ||
            new TextEncoder().encode(v).length > 256 ||
            v.includes("\0"),
        )
      )
        throw new Error(
          "语言最多200项，每项1–64个字符/256字节；未知请清除所有语言。",
        );
    }
    if (
      ["translated", "extraneous_ads"].includes(key) &&
      typeof edit.value !== "boolean"
    )
      throw new Error("布尔属性必须为是、否或未知。");
    if (
      key === "color_mode" &&
      !["monochrome", "color"].includes(String(edit.value))
    )
      throw new Error("颜色值无效。");
    if (
      key === "censorship" &&
      !["uncensored", "mosaic", "full"].includes(String(edit.value))
    )
      throw new Error("修正值无效。");
    const limit = limits[key as EditableField];
    if (
      limit &&
      (typeof edit.value !== "string" ||
        [...edit.value].length > limit ||
        edit.value.includes("\0"))
    )
      throw new Error(
        `${fieldLabels[key as EditableField]}最多 ${limit} 个字符，且不能包含空字符。`,
      );
    if (
      key === "rating" &&
      (typeof edit.value !== "number" ||
        !Number.isFinite(edit.value) ||
        edit.value < 0 ||
        edit.value > 5)
    )
      throw new Error("评分应在 0 到 5 之间。");
    if (
      key === "published_at" &&
      (typeof edit.value !== "string" || Number.isNaN(Date.parse(edit.value)))
    )
      throw new Error("请选择有效的发布时间。");
  }
  return { expected_revision: state.base.revision, fields: state.draft };
}
export function valueText(value: unknown) {
  if (Array.isArray(value))
    return (
      value.map((v) => (typeof v === "string" ? v : tagKey(v))).join("、") ||
      "未知"
    );
  if (typeof value === "boolean") return value ? "是" : "否";
  return value == null || value === "" ? "未填写" : String(value);
}

export function versionStatus(book: Book) {
  const work = book.organization?.works.find((w) => w.id === book.work_id);
  const edition = book.organization?.editions.find((e) => e.id === book.id);
  return work
    ? [
        {
          id: work.id,
          status:
            work.preferred_edition_id === book.id ? "首选版本" : "其他版本",
          label: edition?.version_labels.join(" · ") ?? "",
        },
      ]
    : [];
}

export async function submitDraft(
  state: EditorState,
  edit: (book: string, revision: number, fields: Draft) => Promise<Book>,
) {
  const payload = savePayload(state);
  return edit(state.base.id, payload.expected_revision, payload.fields);
}
