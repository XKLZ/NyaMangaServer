export const coverPageSize = 8;
export type Candidate = {
  mode: "page" | "asset";
  url: string;
  expected_revision: number;
  content_revision: number;
  parsing_revision?: number;
  page_index?: number;
  name: string;
  file?: File;
};
export function canLoadPages(
  book: {
    library_disabled?: boolean;
    trashed_at?: number | null;
    page_count: number;
  },
  active: boolean,
  requested: boolean,
) {
  return (
    active &&
    requested &&
    !book.library_disabled &&
    !book.trashed_at &&
    book.page_count > 0
  );
}
export function validateCoverFile(file: Pick<File, "size" | "type">) {
  if (
    ![
      "image/jpeg",
      "image/png",
      "image/webp",
      "image/bmp",
      "image/x-ms-bmp",
    ].includes(file.type)
  )
    throw new Error("仅支持 JPEG、PNG、WebP 或 BMP 图片。");
  if (file.size > 10 * 1024 * 1024) throw new Error("封面不能超过 10 MiB。");
}
export function coverCommand(
  candidate: Candidate,
  contentRevision: number,
  parsingRevision = 1,
) {
  if (candidate.content_revision !== contentRevision)
    throw new Error("漫画内容已变化，请重新加载并选择候选封面。");
  if (
    candidate.mode === "page" &&
    (candidate.parsing_revision ?? 1) !== parsingRevision
  )
    throw new Error("页序已变化，请重新加载并核对候选封面。");
  return {
    expected_revision: candidate.expected_revision,
    mode: candidate.mode,
    ...(candidate.mode === "page"
      ? {
          page_index: candidate.page_index,
          content_revision: candidate.content_revision,
          ...(candidate.parsing_revision !== undefined
            ? { parsing_revision: candidate.parsing_revision }
            : {}),
        }
      : {}),
  };
}
export async function loadCoverImages<T>(
  items: T[],
  signal: AbortSignal,
  load: (item: T, signal: AbortSignal) => Promise<string>,
  publish: (item: T, result: { url?: string; error?: string }) => void,
  discard: (url: string) => void,
) {
  let next = 0;
  const worker = async () => {
    while (!signal.aborted && next < items.length) {
      const item = items[next++];
      try {
        const url = await load(item, signal);
        if (signal.aborted) discard(url);
        else publish(item, { url });
      } catch (error) {
        if (!signal.aborted)
          publish(item, {
            error: error instanceof Error ? error.message : "图片加载失败",
          });
      }
    }
  };
  await Promise.all([worker(), worker()]);
}

export function coverPageReady(
  items: { page_index: number }[] | undefined,
  images: Record<string, { url?: string; error?: string }>,
  revision: number | string,
) {
  return (
    !!items?.length &&
    items.every((p) => !!images[`${revision}:${p.page_index}`]?.url)
  );
}
