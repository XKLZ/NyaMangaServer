export type MatchMode = "automatic" | "gallery";
export function matchingIntent(mode: MatchMode, url: string, force: boolean) {
  if (mode === "automatic") return { book_urls: {}, force_refresh: force };
  let parsed: URL;
  try {
    parsed = new URL(url.trim());
  } catch {
    throw new Error("请输入有效的画廊网址。");
  }
  if (
    parsed.protocol !== "https:" ||
    !["e-hentai.org", "exhentai.org"].includes(parsed.hostname) ||
    !/^\/g\/\d+\/[a-f0-9]+\/?$/i.test(parsed.pathname) ||
    parsed.username ||
    parsed.password ||
    parsed.port ||
    parsed.search ||
    parsed.hash
  )
    throw new Error("请输入 EH / EX 的 HTTPS 画廊网址。");
  return { gallery_url: url.trim(), force_refresh: false };
}
export function sourceDraftDirty(url: string, submittedURL: string) {
  return !!url && url !== submittedURL;
}
