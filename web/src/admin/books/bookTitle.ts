const brackets = new Map([
  ["(", ")"],
  ["（", "）"],
  ["[", "]"],
  ["［", "］"],
  ["【", "】"],
  ["{", "}"],
  ["｛", "｝"],
  ["「", "」"],
  ["『", "』"],
  ["〈", "〉"],
  ["《", "》"],
  ["〔", "〕"],
]);

/** Display only: preserve the stored title and unmatched bracket text. */
export function cleanBookTitle(title: string): string {
  const stack: { start: number; close: string }[] = [];
  const ranges: [number, number][] = [];
  for (let i = 0; i < title.length; i++) {
    const close = brackets.get(title[i]);
    if (close) stack.push({ start: i, close });
    else if (stack.at(-1)?.close === title[i]) {
      ranges.push([stack.pop()!.start, i + 1]);
    }
  }
  ranges.sort((a, b) => a[0] - b[0]);
  let result = "";
  let end = 0;
  for (const [start, next] of ranges) {
    if (start >= end) result += title.slice(end, start);
    end = Math.max(end, next);
  }
  result = (result + title.slice(end)).replace(/\s+/g, " ").trim();
  return result || title;
}

export async function copyBookTitle(
  title: string,
  clipboard: Pick<Clipboard, "writeText"> | undefined,
): Promise<{ type: "success" | "error"; content: string }> {
  try {
    if (!clipboard) throw new Error("Clipboard unavailable");
    await clipboard.writeText(cleanBookTitle(title));
    return { type: "success", content: "标题已复制" };
  } catch {
    return { type: "error", content: "复制失败，请检查浏览器剪贴板权限后重试" };
  }
}

export type ComicTitle = {
  title_original?: string | null;
  metadata?: { fields?: { title_original?: string | null } };
  name_override?: string | null;
  name?: string | null;
  title?: string | null;
  filename?: string | null;
  relative_path?: string | null;
};
/** Identity display only. An explicitly empty original title is authoritative;
 * never borrow another Edition or today's metadata for frozen historical rows. */
export function originalTitle(value: ComicTitle): string {
  const original =
    value.title_original !== undefined
      ? value.title_original
      : value.metadata?.fields?.title_original;
  for (const text of [original, value.name_override, value.name, value.title])
    if (typeof text === "string" && text.trim()) return text.trim();
  const path = value.filename || value.relative_path || "";
  return path.split(/[\\/]/).at(-1) || "未命名作品";
}
export const preferredBookTitle = originalTitle;

/** Bounded query draft only; preserves unmatched marks and never changes files. */
export function sourceTitleQuery(filename: string) {
  return [...cleanBookTitle(filename.replace(/\.[a-z0-9]{1,8}$/i, ""))]
    .slice(0, 256)
    .join("");
}
