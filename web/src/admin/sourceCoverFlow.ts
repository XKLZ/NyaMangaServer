/** Same-origin provider proxy only; keep the existing 2 MiB PNG boundary. */
export async function readSourceCover(
  path: string,
  signal: AbortSignal,
  fetcher: typeof fetch = fetch,
) {
  const response = await fetcher(path, {
    credentials: "same-origin",
    signal,
  });
  if (!response.ok)
    throw Error(
      response.status === 409
        ? "候选已变化，请核对后重新加载封面。"
        : "封面暂不可用，仍可核对文字资料。",
    );
  const blob = await response.blob();
  if (signal.aborted) throw new DOMException("Aborted", "AbortError");
  if (blob.size > 2 * 1024 * 1024 || blob.type !== "image/png")
    throw Error("封面响应无效。");
  return URL.createObjectURL(blob);
}
