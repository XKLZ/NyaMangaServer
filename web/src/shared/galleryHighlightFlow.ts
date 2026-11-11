export type HighlightConfig = {
  protocol_version: 2;
  service_url: string;
  token: string;
};
export type GalleryConnection = {
  id: string;
  name: string;
  created_at: number;
  expires_at: number | null;
  revoked_at: number | null;
  revision: number;
  state: "active" | "expired" | "revoked" | "invalidated";
};
export type GalleryConnections = {
  items: GalleryConnection[];
  total: number;
  page: number;
  page_size: number;
};
export type GalleryConnectionSecret = {
  protocol_version: 2;
  connection: GalleryConnection;
  token: string;
};
export function serviceOrigin(value: string) {
  if ([...value].some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127))
    throw Error("服务地址不能包含控制字符");
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw Error("请输入完整的 http:// 或 https:// 服务根地址");
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.pathname !== "/" ||
    /[?#]/u.test(value) ||
    !/^(?:[a-z0-9.-]+|\[[a-f0-9:]+\])$/iu.test(url.hostname)
  )
    throw Error("仅支持无凭据、路径、查询或片段的HTTP(S)服务根地址");
  return url.origin;
}
export function insecureRemote(origin: string) {
  const url = new URL(serviceOrigin(origin));
  return (
    url.protocol === "http:" &&
    !(
      url.hostname === "localhost" ||
      url.hostname === "[::1]" ||
      /^127\./u.test(url.hostname)
    )
  );
}
export function oneTimeAuthorization(value: GalleryConnectionSecret): string {
  if (
    value?.protocol_version !== 2 ||
    !/^ngh1_[A-Za-z0-9_-]{43}$/u.test(value.token) ||
    !value.connection?.id ||
    value.connection.expires_at !== null
  )
    throw Error("服务未返回完整的协议2授权码，请核对授权列表并撤销后重建");
  return value.token;
}
export function buildGalleryScript(template: string, origin: string) {
  const service = serviceOrigin(origin),
    host = new URL(service).hostname;
  const connect = "__NYAMANGA_CONNECT__",
    source = '/*__NYAMANGA_ORIGIN__*/ ""';
  if (
    template.split(connect).length !== 2 ||
    template.split(source).length !== 2
  )
    throw Error("脚本模板不完整，无法下载");
  return template
    .replace(connect, host)
    .replace(source, JSON.stringify(service));
}
export const connectionStateNames = {
  active: "授权有效",
  expired: "已到期",
  revoked: "已撤销",
  invalidated: "已失效",
};
export const connectionTime = (value: number) =>
  Number.isFinite(value) &&
  value > 0 &&
  Number.isFinite(new Date(value * 1000).getTime())
    ? new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(value * 1000))
    : "未知";
