import { usePermissions } from "../shared/permissions";
/** §2.1/§3.6: one compact header scope control; real scope gates business lists.
 * Existing React/Ant tokens, 8/12/16 spacing, neutral labels and explicit all/disabled tags. */
import {
  createContext,
  Fragment,
  useContext,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  App,
  Button,
  ConfigProvider,
  Dropdown,
  Empty,
  Modal,
  Select,
  Space,
  Spin,
  Tag,
  Tooltip,
} from "antd";
import {
  FolderOutlined,
  MoreOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useLocation, useNavigate } from "react-router";
import { taskID } from "../shared/consoleRoutes.mjs";
import { BusinessReadContext } from "../shared/readBoundary";
import {
  ApiError,
  request,
  subscribeWrites,
  writesPending,
} from "../shared/api";
import type { Library } from "../shared/api";
import { resourceEvent } from "../shared/events";
import {
  canonicalLibraryURL,
  enabledLibrary,
  libraryPolicy,
  libraryTarget,
  libraryURL,
  readLibraryPreference,
  resolveLibrary,
  switchLibraryURL,
  writeLibraryPreference,
} from "../shared/libraryScope";
import type { LibraryTarget } from "../shared/libraryScope";
import type { WorkProposal } from "../shared/workDiscovery";
import { ErrorNotice } from "./common";
import { useRetainedRemote } from "./useRetainedRemote";
import "./currentLibrary.css";

type ScopeContext = {
  id: string;
  library?: Library;
  libraries: Library[];
  all: boolean;
  ready: boolean;
  error?: unknown;
  directoryError?: unknown;
  key: string;
  change: (id: string) => void;
  setAll: (all: boolean) => void;
  reload: () => void;
  pending: boolean;
};
const CurrentLibraryContext = createContext<ScopeContext | null>(null);
export function useCurrentLibrary() {
  const value = useContext(CurrentLibraryContext);
  if (!value) throw Error("Current library provider is missing");
  return value;
}
function readPreference(account: string, tab = false) {
  try {
    return readLibraryPreference(
      tab ? window.sessionStorage : window.localStorage,
      account,
      tab,
    );
  } catch {
    return "";
  }
}
async function objectLibrary(
  target: LibraryTarget,
  signal: AbortSignal,
): Promise<string | undefined> {
  const paths = {
    book: "books",
    work: "works",
    series: "series",
    "work-proposal": "work-proposals",
    "series-proposal": "series-proposals",
    batch: "import-batches",
    job: "jobs",
  };
  const object = await request<{ library_id?: string } & Partial<WorkProposal>>(
    `/${paths[target.kind]}/${encodeURIComponent(target.id)}`,
    "GET",
    undefined,
    false,
    signal,
  );
  if (object.library_id) return object.library_id;
  if (target.kind === "job") {
    try {
      const batch = await request<{
        library_id?: string;
        batch_id?: string;
        id?: string;
      }>(
        `/jobs/${encodeURIComponent(target.id)}/import-batch`,
        "GET",
        undefined,
        false,
        signal,
      );
      if (batch.library_id) return batch.library_id;
      const id = batch.batch_id || batch.id;
      if (id)
        return (
          await request<{ library_id: string }>(
            `/import-batches/${encodeURIComponent(id)}`,
            "GET",
            undefined,
            false,
            signal,
          )
        ).library_id;
    } catch (error) {
      if (!(error instanceof ApiError && error.status === 404)) throw error;
    }
    return undefined;
  }
  if (target.kind === "work-proposal") {
    const candidates = [
      ...(object.job_id ? [`/jobs/${encodeURIComponent(object.job_id)}`] : []),
      ...(object.target_work_id
        ? [`/works/${encodeURIComponent(object.target_work_id)}`]
        : []),
      ...(object.members?.[0]?.id
        ? [`/books/${encodeURIComponent(object.members[0].id)}`]
        : []),
    ];
    for (const path of candidates) {
      try {
        const value = await request<{ library_id?: string }>(
          path,
          "GET",
          undefined,
          false,
          signal,
        );
        if (value.library_id) return value.library_id;
      } catch (error) {
        if (!(error instanceof ApiError && error.status === 404)) throw error;
      }
    }
  }
  throw Error("无法确认此对象所属书库，请重试或返回列表。");
}
export function CurrentLibraryProvider({
  account,
  children,
  routingReady = true,
}: {
  account: string;
  routingReady?: boolean;
  children: React.ReactNode;
}) {
  const admin = usePermissions().admin;
  const location = useLocation(),
    navigate = useNavigate(),
    { message } = App.useApp();
  const catalog = useRetainedRemote<{ items: Library[] }>("/libraries");
  const [current, setCurrent] = useState("");
  const [owner, setOwner] = useState<{
    key: string;
    id?: string;
    error?: unknown;
  } | null>(null);
  const [ownerRetry, setOwnerRetry] = useState(0);
  const target = libraryTarget(location.pathname, location.search);
  const targetKey = target ? `${target.kind}:${target.id}` : "";
  useEffect(() => {
    if (!targetKey) return;
    const abort = new AbortController();
    const [kind, ...id] = targetKey.split(":");
    void objectLibrary(
      { kind: kind as LibraryTarget["kind"], id: id.join(":") },
      abort.signal,
    )
      .then((value) => {
        if (!abort.signal.aborted) setOwner({ key: targetKey, id: value });
      })
      .catch((error) => {
        if (!abort.signal.aborted) setOwner({ key: targetKey, error });
      });
    return () => abort.abort();
  }, [targetKey, ownerRetry]);
  const reload = catalog.reload;
  useEffect(() => {
    const update = (event: Event) => {
      const detail = (event as CustomEvent<{ type?: string }>).detail;
      if (!detail?.type || ["library", "libraries"].includes(detail.type))
        reload();
    };
    const visible = () => {
      if (document.visibilityState === "visible") reload();
    };
    window.addEventListener(resourceEvent, update);
    document.addEventListener("visibilitychange", visible);
    return () => {
      window.removeEventListener(resourceEvent, update);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [reload]);
  const libraries = catalog.data?.items ?? [];
  const policy = libraryPolicy(location.pathname, location.search);
  const resolvedOwner = !targetKey || owner?.key === targetKey;
  const resolution = resolveLibrary(libraries, {
    search: location.search,
    policy,
    owner: resolvedOwner
      ? owner?.key === targetKey
        ? owner.id
        : undefined
      : undefined,
    current,
    tab: readPreference(account, true),
    remembered: readPreference(account),
  });
  const objectError =
    targetKey && owner?.key === targetKey
      ? owner.error ||
        (target?.kind === "job" &&
        !taskID(location.pathname, location.search) &&
        !owner.id
          ? Error("无法确认此任务所属书库，请返回任务详情核对。")
          : null)
      : null;
  const error =
    libraryURL(location.search).error ||
    objectError ||
    (catalog.data
      ? resolution.error ||
        (catalog.error && current && resolution.library?.id !== current
          ? catalog.error
          : null)
      : catalog.error);
  const canonical = canonicalLibraryURL(
    location.pathname,
    location.search,
    resolution.library?.id,
    resolution.all,
  );
  const needsCanonical =
    routingReady &&
    !error &&
    resolvedOwner &&
    !!catalog.data &&
    !!(resolution.library || resolution.all) &&
    canonical !== location.pathname + location.search &&
    (policy !== "global" || !!libraryURL(location.search).id) &&
    !(target?.kind === "job" && !owner?.id);
  const ready =
    (admin || libraries.length > 0) &&
    routingReady &&
    !!catalog.data &&
    resolvedOwner &&
    !error &&
    !needsCanonical &&
    (!!resolution.library ||
      resolution.all ||
      (target?.kind === "job" &&
        !!taskID(location.pathname, location.search) &&
        owner?.key === targetKey &&
        !owner.error));
  const lastNotice = useRef("");
  useEffect(() => {
    if (needsCanonical) {
      if (resolution.fallback && lastNotice.current !== "fallback") {
        lastNotice.current = "fallback";
        void message.info("上次书库已不可用，已选择首个启用书库");
      }
      const explicit = libraryURL(location.search).id;
      if (
        owner?.id &&
        explicit &&
        owner.id !== explicit &&
        lastNotice.current !== targetKey
      ) {
        lastNotice.current = targetKey;
        void message.info("已切换到此对象所属书库");
      }
      void navigate(canonical, {
        replace: true,
        preventScrollReset: true,
        // Reordering an already-scoped URL must retain its detail navigation
        // state. A real scope change still starts a new workspace.
        state:
          location.pathname === "/admin/tags" &&
          explicit &&
          explicit === resolution.library?.id
            ? location.state
            : null,
      });
    }
  }, [
    needsCanonical,
    resolution.fallback,
    canonical,
    navigate,
    owner,
    location.pathname,
    location.search,
    location.state,
    resolution.library?.id,
    targetKey,
    message,
  ]);
  useEffect(() => {
    if (!ready || !resolution.library || (resolution.all && targetKey)) return;
    const library = resolution.library;
    let active = true;
    void Promise.resolve().then(() => {
      if (active) setCurrent(library.id);
    });
    if (enabledLibrary(library)) {
      try {
        writeLibraryPreference(
          window.sessionStorage,
          account,
          library.id,
          true,
        );
        writeLibraryPreference(window.localStorage, account, library.id);
      } catch {
        /* Keep the resolved URL and in-memory scope. */
      }
    }
    if (resolution.fallback && lastNotice.current !== "fallback") {
      lastNotice.current = "fallback";
      void message.info("上次书库已不可用，已选择首个启用书库");
    }
    return () => {
      active = false;
    };
  }, [
    ready,
    resolution.library,
    resolution.fallback,
    resolution.all,
    targetKey,
    account,
    message,
  ]);
  const change = (id: string) => {
    if (!catalog.error && !writesPending())
      void navigate(switchLibraryURL(location.pathname, location.search, id), {
        preventScrollReset: true,
      });
  };
  const setAll = (all: boolean) => {
    if (!catalog.error && !writesPending())
      void navigate(
        switchLibraryURL(
          location.pathname,
          location.search,
          resolution.library?.id || current || undefined,
          all,
        ),
        { preventScrollReset: true },
      );
  };
  const scopeID =
    resolution.library?.id || libraryURL(location.search).id || current;
  const value: ScopeContext = {
    id: scopeID,
    library: resolution.library,
    libraries,
    all: resolution.all && (admin || libraries.length > 0),
    ready,
    error,
    directoryError: catalog.error,
    key: `${resolution.all ? "all" : scopeID || "none"}`,
    change,
    setAll,
    reload: () => {
      reload();
      setOwnerRetry((v) => v + 1);
    },
    pending:
      !routingReady || catalog.loading || !resolvedOwner || needsCanonical,
  };
  return (
    <CurrentLibraryContext.Provider value={value}>
      {children}
    </CurrentLibraryContext.Provider>
  );
}
export function CurrentLibraryGate({
  children,
}: {
  children: React.ReactNode;
}) {
  const scope = useCurrentLibrary(),
    location = useLocation(),
    navigate = useNavigate();
  const global = libraryPolicy(location.pathname, location.search) === "global";
  const admin = usePermissions().admin;
  const noGrant = !admin && !scope.libraries.length;
  const allowed =
    (global || scope.ready) &&
    (!noGrant || location.pathname === "/admin/settings");
  const contentKey = global
    ? `global:${libraryURL(location.search).id}`
    : scope.key;
  const [mountedKey, setMountedKey] = useState("");
  if (allowed && mountedKey !== contentKey) setMountedKey(contentKey);
  const retained = mountedKey === contentKey;
  const visible = allowed || (retained && scope.pending && !scope.error);
  const status =
    scope.error && !noGrant ? (
      <>
        <ErrorNotice
          error={
            typeof scope.error === "string" ? Error(scope.error) : scope.error
          }
          retry={scope.reload}
        />
        <Space>
          {scope.library && !scope.library.trashed_at && (
            <Button onClick={() => scope.change(scope.id)}>返回当前书库</Button>
          )}
          <Button onClick={() => void navigate("/admin/libraries")}>
            {admin ? "管理书库" : "查看授权书库"}
          </Button>
          {admin && (
            <Button
              onClick={() => void navigate("/admin/maintenance?section=trash")}
            >
              打开回收站
            </Button>
          )}
        </Space>
      </>
    ) : scope.pending ? (
      <Spin description="正在确认书库范围…" />
    ) : (
      <Empty
        description={
          noGrant
            ? "尚未获得书库授权，请联系管理员"
            : !scope.libraries.length
              ? "还没有书库"
              : "暂无启用书库"
        }
      >
        {admin && (
          <Button
            type="primary"
            onClick={() => void navigate("/admin/libraries")}
          >
            {!scope.libraries.length ? "添加书库" : "管理书库"}
          </Button>
        )}
      </Empty>
    );
  return (
    <BusinessReadContext.Provider value={allowed}>
      {!visible && status}
      <div
        className="library-scoped-content"
        hidden={!visible}
        inert={!allowed}
      >
        <ConfigProvider
          componentDisabled={!allowed}
          modal={{
            styles: { root: { display: allowed ? undefined : "none" } },
          }}
          drawer={{
            styles: { root: { display: allowed ? undefined : "none" } },
          }}
        >
          {(allowed || retained) && (
            <Fragment key={contentKey}>{children}</Fragment>
          )}
        </ConfigProvider>
      </div>
    </BusinessReadContext.Provider>
  );
}
export function CurrentLibraryHeader() {
  const scope = useCurrentLibrary(),
    location = useLocation();
  const [advanced, setAdvanced] = useState(false);
  const busy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const disabled = scope.libraries.filter((l) => l.disabled && !l.trashed_at);
  return (
    <div className="current-library-header">
      <FolderOutlined aria-hidden="true" />
      <Select<string | null>
        aria-label="当前书库"
        className="current-library-select"
        placeholder={
          scope.pending ? "确认书库…" : scope.all ? "全部书库" : "选择书库"
        }
        value={scope.all ? null : (scope.library?.id ?? null)}
        labelRender={({ label }) => (scope.all ? "全部书库" : label)}
        loading={scope.pending}
        disabled={
          busy ||
          !!scope.directoryError ||
          scope.pending ||
          (!!scope.error && !scope.libraries.length)
        }
        options={scope.libraries
          .filter(
            (l) =>
              enabledLibrary(l) || (!scope.all && l.id === scope.library?.id),
          )
          .map((l) => ({
            value: l.id,
            label: `${l.display_name}${l.trashed_at ? " · 已回收" : l.disabled ? " · 已停用" : ""}`,
            disabled: !!l.trashed_at,
          }))}
        onChange={(id) => {
          if (id) scope.change(id);
        }}
      />
      {scope.library?.trashed_at ? (
        <Tag className="library-state-tag" color="error">
          已回收
        </Tag>
      ) : (
        scope.library?.disabled && (
          <Tag className="library-state-tag" color="warning">
            已停用
          </Tag>
        )
      )}
      {scope.all ? (
        <Tag className="library-state-tag" color="blue">
          全部书库
        </Tag>
      ) : libraryPolicy(location.pathname, location.search) === "global" ? (
        <Tag>全局</Tag>
      ) : null}
      {scope.directoryError ? (
        <Tooltip
          title="书库目录读取失败，点击重试"
          trigger={["hover", "focus"]}
        >
          <Button
            type="text"
            danger
            disabled={busy}
            aria-label="重试书库目录"
            icon={<ReloadOutlined aria-hidden="true" />}
            onClick={scope.reload}
          />
        </Tooltip>
      ) : (
        <Dropdown
          menu={{
            items: [
              {
                key: "disabled",
                label: "查看停用书库",
                disabled: !disabled.length,
                onClick: () => setAdvanced(true),
              },
              {
                key: "refresh",
                label: "刷新书库目录",
                icon: <ReloadOutlined />,
                onClick: scope.reload,
              },
            ],
          }}
          trigger={["click"]}
        >
          <Tooltip title="书库高级选项">
            <Button
              type="text"
              aria-label="书库高级选项"
              disabled={busy}
              icon={<MoreOutlined aria-hidden="true" />}
            />
          </Tooltip>
        </Dropdown>
      )}
      <Modal
        open={advanced}
        title="查看停用书库"
        footer={null}
        onCancel={() => setAdvanced(false)}
      >
        <Select<string | null>
          aria-label="停用书库"
          value={null}
          disabled={busy}
          placeholder="选择停用书库…"
          className="full-width"
          options={disabled.map((l) => ({
            value: l.id,
            label: `${l.display_name}${l.disabled ? " · 已停用" : ""}`,
          }))}
          onChange={(id) => {
            if (id) {
              scope.change(id);
              setAdvanced(false);
            }
          }}
        />
      </Modal>
    </div>
  );
}
export function LibraryScopeControl() {
  const scope = useCurrentLibrary();
  const busy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  return (
    <Select
      aria-label="书库查看范围"
      disabled={busy}
      value={scope.all ? "all" : "current"}
      options={[
        { value: "current", label: "当前书库" },
        { value: "all", label: "全部书库" },
      ]}
      onChange={(value) => scope.setAll(value === "all")}
    />
  );
}
