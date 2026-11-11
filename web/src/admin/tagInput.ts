import type { Tag, TagName } from "../shared/metadata";

export const namespaces = [
  ["language", "语言"],
  ["parody", "原作"],
  ["character", "角色"],
  ["group", "社团"],
  ["artist", "作者"],
  ["male", "男性"],
  ["female", "女性"],
  ["mixed", "混合"],
  ["cosplayer", "Cosplayer"],
  ["other", "其他"],
  ["reclass", "重新分类"],
  ["temp", "临时标签"],
] as const;
const key = (tag: TagName) => `${tag.namespace}:${tag.name}`;
export function canonicalTag(tag: TagName): TagName {
  return {
    namespace: tag.namespace.trim().toLowerCase(),
    name: tag.name.trim().replace(/\s+/g, " ").toLowerCase(),
  };
}
export function validateTag(tag: TagName) {
  if (!/^[a-z][a-z0-9_]{0,31}$/.test(tag.namespace))
    throw new Error("标签类型应为小写字母开头，最多32个字母、数字或下划线。");
  if (!tag.name || [...tag.name].length > 128 || tag.name.includes("\0"))
    throw new Error("标签名称应为1–128个字符，且不能包含空字符。");
}
/** Commas/newlines and a following namespace start a token; ordinary spaces do not. */
export function parseTags(text: string, namespace: string): TagName[] {
  const parts: string[] = [];
  let part = "";
  let quote = "";
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quote) {
      if (c === "\\" && text[i + 1] === quote) {
        part += text[++i];
        continue;
      }
      if (c === quote) quote = "";
      else part += c;
    } else if (
      (c === '"' || c === "'") &&
      (!part.trim() || /:$/.test(part.trim()))
    )
      quote = c;
    else if (
      /[\n\r,;，；\t]/.test(c) ||
      (/\s/.test(c) && /^\s+["']?[a-z][a-z0-9_]{0,31}:/i.test(text.slice(i)))
    ) {
      if (part.trim()) parts.push(part.trim());
      part = "";
    } else part += c;
  }
  if (quote) throw new Error("标签引号未闭合，请修正后再添加。");
  if (part.trim()) parts.push(part.trim());
  if (!parts.length) throw new Error("请输入标签名称。");
  const tags = parts.map((part) => {
    const at = part.indexOf(":");
    const tag = canonicalTag(
      at < 0
        ? { namespace, name: part }
        : { namespace: part.slice(0, at), name: part.slice(at + 1) },
    );
    validateTag(tag);
    return tag;
  });
  return [...new Map(tags.map((tag) => [key(tag), tag])).values()];
}
export function mergeTags(existing: TagName[], added: TagName[]) {
  const tags = [
    ...new Map(
      [...existing, ...added].map((t) => {
        const tag = canonicalTag(t);
        validateTag(tag);
        return [key(tag), tag] as const;
      }),
    ).values(),
  ];
  if (tags.length > 200)
    throw new Error("每本漫画最多200个标签，请减少后再添加。");
  return tags;
}
export function availableTags<T extends TagName>(
  items: T[],
  selected: TagName[],
) {
  const used = new Set(selected.map((t) => key(canonicalTag(t))));
  return items.filter((t) => !used.has(key(canonicalTag(t))));
}
export function tagKeyAction(
  key: string,
  composing: boolean,
  input: string,
  open: boolean,
  index: number,
  count: number,
) {
  if (composing) return { action: "none", index };
  if (key === "ArrowDown")
    return { action: "navigate", index: count ? (index + 1) % count : -1 };
  if (key === "ArrowUp")
    return {
      action: "navigate",
      index: count ? (index <= 0 ? count - 1 : index - 1) : -1,
    };
  if (key === "Escape") return { action: "close", index: -1 };
  if (key === "Backspace" && !input) return { action: "remove", index: -1 };
  if (key === "Enter")
    return {
      action:
        open && index >= 0 && index < count
          ? "select"
          : input.trim()
            ? "create"
            : "none",
      index,
    };
  return { action: "none", index };
}

/** A transport can finish despite cancellation; never publish that response. */
export async function loadSuggestions(
  load: () => Promise<{ items: Tag[] }>,
  signal: AbortSignal,
) {
  const result = await load();
  return signal.aborted ? null : result;
}
