import { randomIdentity } from "../shared/randomIdentity";
import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  useMemo,
  useSyncExternalStore,
} from "react";
import {
  Alert,
  Button,
  ConfigProvider,
  Modal,
  Space,
  Tag,
  Typography,
} from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import {
  useBlocker,
  useBeforeUnload,
  useLocation,
  useNavigate,
  Link,
} from "react-router";
import { dropsTagSessions, preservedTagIDs } from "./tags/tagTrail";
import { libraryScopeChanged } from "../shared/libraryScope";
import { BusinessReadContext } from "../shared/readBoundary";
import {
  ApiError,
  request,
  subscribeWrites,
  writesPending,
} from "../shared/api";

export type DirtyOptions = { keepTagBook?: boolean; tagID?: string };
export type DirtyPreservation = boolean | readonly string[];
export const DirtyContext = createContext<
  (id: string, dirty: boolean, options?: DirtyOptions) => void
>(() => {});
export function useDirty(dirty: boolean, options?: DirtyOptions) {
  const register = useContext(DirtyContext);
  const id = useMemo(() => randomIdentity(), []);
  const keepTagBook = !!options?.keepTagBook;
  const tagID = options?.tagID;
  useEffect(() => {
    register(id, dirty, { keepTagBook, tagID });
    return () => register(id, false);
  }, [register, id, dirty, keepTagBook, tagID]);
  return useCallback(() => register(id, false), [register, id]);
}
export function NavigationGuard({
  dirty,
  authExpired,
}: {
  dirty: (preserve?: DirtyPreservation) => boolean;
  authExpired?: () => boolean;
}) {
  const busy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    // A rejected request may still be releasing its write lock when login is required.
    if (nextLocation.pathname === "/login" && authExpired?.()) return false;
    const preserveTag =
      currentLocation.pathname === "/admin/tags" &&
      nextLocation.pathname === "/admin/tags" &&
      !libraryScopeChanged(currentLocation, nextLocation)
        ? preservedTagIDs(currentLocation, nextLocation)
        : false;
    if (!dirty(preserveTag) && !writesPending()) return false;
    // Query-only filters and settings tabs preserve mounted draft forms.
    // Changing the selected book or page component would discard them.
    return (
      currentLocation.pathname !== nextLocation.pathname ||
      libraryScopeChanged(currentLocation, nextLocation) ||
      (["/admin/settings", "/admin/maintenance"].includes(
        currentLocation.pathname,
      ) &&
        ["section", "task"].some(
          (key) =>
            new URLSearchParams(currentLocation.search).get(key) !==
            new URLSearchParams(nextLocation.search).get(key),
        )) ||
      (currentLocation.pathname === "/admin/tags" &&
        ["tag", "category", "work", "images"].some(
          (key) =>
            new URLSearchParams(currentLocation.search).get(key) !==
            new URLSearchParams(nextLocation.search).get(key),
        )) ||
      (currentLocation.pathname === "/admin/tags" &&
        dropsTagSessions(currentLocation, nextLocation)) ||
      (currentLocation.pathname === "/admin/series" &&
        ["series", "proposal", "tab"].some(
          (key) =>
            new URLSearchParams(currentLocation.search).get(key) !==
            new URLSearchParams(nextLocation.search).get(key),
        )) ||
      (currentLocation.pathname === "/admin/imports" &&
        new URLSearchParams(currentLocation.search).get("batch") !==
          new URLSearchParams(nextLocation.search).get("batch")) ||
      new URLSearchParams(currentLocation.search).get("book") !==
        new URLSearchParams(nextLocation.search).get("book") ||
      (currentLocation.pathname === "/admin/versions" &&
        ["work", "proposal", "tab"].some(
          (key) =>
            new URLSearchParams(currentLocation.search).get(key) !==
            new URLSearchParams(nextLocation.search).get(key),
        ))
    );
  });
  useBeforeUnload(
    useCallback(
      (event) => {
        if (dirty() || writesPending()) {
          event.preventDefault();
          event.returnValue = "";
        }
      },
      [dirty],
    ),
  );
  return (
    <Modal
      open={blocker.state === "blocked"}
      title={
        busy
          ? "正在保存，请稍候"
          : dirty()
            ? "有尚未保存的修改"
            : "操作已完成，继续切换？"
      }
      okText={busy ? "等待保存完成" : dirty() ? "放弃修改并离开" : "继续切换"}
      zIndex={5000}
      okButtonProps={{ disabled: busy }}
      cancelText="继续编辑"
      onOk={() =>
        !writesPending() && blocker.state === "blocked" && blocker.proceed()
      }
      onCancel={() => blocker.state === "blocked" && blocker.reset()}
    >
      <p>离开会丢失当前草稿。你也可以取消，保存后再继续。</p>
    </Modal>
  );
}
export function useRemote<T>(
  path: string,
  body?: unknown,
  method = "GET",
  enabled = true,
  onLoaded?: (value: T) => void,
) {
  const readable = useContext(BusinessReadContext);
  const [data, setData] = useState<T | null>(null);
  const [loadedPath, setLoadedPath] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const key = body === undefined ? undefined : JSON.stringify(body);
  const reload = useCallback(() => setRevision((v) => v + 1), []);
  useEffect(() => {
    if (!enabled || !readable) return;
    const controller = new AbortController();
    void Promise.resolve()
      .then(() => {
        if (controller.signal.aborted) return;
        setLoading(true);
        setError(null);
        return request<T>(
          path,
          method,
          key === undefined ? undefined : JSON.parse(key),
          false,
          controller.signal,
        ).then((v) => {
          if (!controller.signal.aborted) {
            setData(v);
            setLoadedPath(path);
            onLoaded?.(v);
          }
        });
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [path, method, key, enabled, readable, revision, onLoaded]);
  return { data, setData, error, loading, reload, loadedPath };
}
export function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  async function run(action: () => Promise<void>, success = "操作已完成") {
    setBusy(true);
    setError(null);
    setNotice("");
    try {
      await action();
      setNotice(success);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, notice, setError, setNotice, run };
}
export function ErrorNotice({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  if (!error) return null;
  const value = error instanceof Error ? error.message : "请求失败，请重试";
  const api = error instanceof ApiError ? error : null;
  return (
    <Alert
      type="error"
      showIcon
      title={
        api?.code === "enhancement_disabled" ? "所需增强功能未启用" : value
      }
      description={
        <Space orientation="vertical">
          <span>
            {api?.code === "enhancement_disabled" && (
              <Link to="/admin/settings?section=enhancements">
                前往增强设置
              </Link>
            )}
            {api?.status === 409 && api.code !== "enhancement_disabled"
              ? "数据已变化。你的草稿仍保留，请查看当前值并重新确认版本。"
              : ""}
          </span>
          {api?.details !== undefined && (
            <pre className="error-details">
              {JSON.stringify(api.details, null, 2)}
            </pre>
          )}
          {api?.requestId && (
            <Typography.Text type="secondary" copyable>
              请求编号：{api.requestId}
            </Typography.Text>
          )}
          {retry && (
            <Button size="small" onClick={retry}>
              重新加载
            </Button>
          )}
        </Space>
      }
    />
  );
}
export function ActionNotice({
  action,
}: {
  action: ReturnType<typeof useAction>;
}) {
  return (
    <>
      <ErrorNotice error={action.error} />
      {action.notice && (
        <Alert
          type="success"
          showIcon
          title={<span role="status">{action.notice}</span>}
          closable
          onClose={() => action.setNotice("")}
        />
      )}
    </>
  );
}
export function PageTitle({
  title,
  extra,
  level = 1,
}: {
  title: string;
  subtitle?: string;
  extra?: React.ReactNode;
  level?: 1 | 2;
}) {
  const heading =
    level === 1 ? (
      <h1 className="sr-only">{title}</h1>
    ) : (
      <h2 className="sr-only">{title}</h2>
    );
  if (!extra) return heading;
  return (
    <header className="admin-page-title">
      {heading}
      {extra && <Space wrap>{extra}</Space>}
    </header>
  );
}
export function RefreshButton({
  reload,
  disabled = false,
}: {
  reload: () => void;
  disabled?: boolean;
}) {
  return (
    <Button
      icon={<ReloadOutlined aria-hidden="true" />}
      disabled={disabled}
      onClick={reload}
    >
      刷新
    </Button>
  );
}
export function StateTag({ state }: { state: string }) {
  const names: Record<string, string> = {
    available: "可用",
    ready: "可用",
    parsing_changed: "页序待核对",
    content_changed: "内容已变化",
    missing: "文件缺失",
    corrupt: "损坏",
    queued: "排队中",
    running: "执行中",
    paused: "已暂停",
    completed: "已完成",
    failed: "失败",
    cancelled: "已取消",
    none: "未匹配",
    matched: "已匹配",
    needs_review: "待复核",
    pending: "待处理",
    processing: "处理中",
    review: "等待审核",
    succeeded: "成功",
  };
  return (
    <Tag
      color={
        state === "available" ||
        state === "ready" ||
        state === "completed" ||
        state === "matched"
          ? "success"
          : ["failed", "corrupt"].includes(state)
            ? "error"
            : [
                  "missing",
                  "needs_review",
                  "paused",
                  "content_changed",
                  "parsing_changed",
                ].includes(state)
              ? "warning"
              : "default"
      }
    >
      {names[state] ?? state}
    </Tag>
  );
}
export function query(
  values: Record<string, string | number | boolean | undefined>,
) {
  const q = new URLSearchParams();
  Object.entries(values).forEach(([k, v]) => {
    if (v !== undefined && v !== "") q.set(k, String(v));
  });
  return q.toString();
}
export function time(value: number | string | null | undefined) {
  if (!value) return "—";
  return new Date(
    typeof value === "number" ? value * 1000 : value,
  ).toLocaleString("zh-CN");
}

export function useURLValue<T extends string | number>(
  key: string,
  fallback: T,
): [
  T extends string ? string : number,
  (value: T extends string ? string : number) => void,
] {
  type Value = T extends string ? string : number;
  const location = useLocation();
  const navigate = useNavigate();
  const raw = new URLSearchParams(location.search).get(key);
  const value = (
    typeof fallback === "number"
      ? raw && Number.isFinite(Number(raw)) && Number(raw) >= 1
        ? Number(raw)
        : fallback
      : (raw ?? fallback)
  ) as Value;
  const set = (next: Value) => {
    const params = new URLSearchParams(window.location.search);
    if (String(next) === String(fallback)) params.delete(key);
    else params.set(key, String(next));
    void navigate({ search: params.toString() }, { preventScrollReset: true });
  };
  return [value, set];
}

export function FormLock({
  busy,
  children,
}: {
  busy: boolean;
  children: React.ReactNode;
}) {
  const { componentDisabled } = ConfigProvider.useConfig();
  return (
    <ConfigProvider componentDisabled={busy || componentDisabled}>
      {children}
    </ConfigProvider>
  );
}
