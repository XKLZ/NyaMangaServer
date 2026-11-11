import { usePermissions } from "../shared/permissions";
import {
  actorAccess,
  setActor,
  setActorVerified,
} from "../shared/actorAccess.mjs";
import { Security } from "../features/auth/Auth";
import {
  rememberRecoveryReturn,
  takeRecoveryReturn,
} from "../shared/instanceFlow";
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useState,
  useRef,
  useSyncExternalStore,
} from "react";
import {
  App as AntApp,
  Alert,
  Breadcrumb,
  Button,
  ConfigProvider,
  Drawer,
  Layout,
  Menu,
  Modal,
  Space,
  Spin,
  Switch,
  Typography,
  Tooltip,
  theme,
} from "antd";
import { HealthProtection } from "../admin/maintenance/HealthProtection";
import { EnhancementsProvider } from "../shared/enhancements";
import zhCN from "antd/locale/zh_CN";
import {
  BookOutlined,
  FolderOutlined,
  ApartmentOutlined,
  GroupOutlined,
  TagsOutlined,
  ToolOutlined,
  SettingOutlined,
  MenuOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  LogoutOutlined,
  MoonOutlined,
  LoadingOutlined,
} from "@ant-design/icons";
import {
  createBrowserRouter,
  RouterProvider,
  useLocation,
  useNavigate,
  Link,
} from "react-router";
import { api, ApiError, subscribeWrites, writesPending } from "../shared/api";
import type { Session } from "../shared/api";
import { AuthForm } from "../features/auth/Auth";
import {
  readNavigationCollapsed,
  writeNavigationCollapsed,
} from "../shared/uiPreferences";
import { Notifications } from "../shared/events";
import { DirtyContext, NavigationGuard, ErrorNotice } from "../admin/common";
import type { DirtyOptions, DirtyPreservation } from "../admin/common";
const BooksPage = lazy(() =>
  import("../admin/books/Books").then((module) => ({
    default: module.BooksPage,
  })),
);
const ImportsPage = lazy(() =>
  import("../admin/imports/Imports").then((module) => ({
    default: module.ImportsPage,
  })),
);
const LibrariesPage = lazy(() =>
  import("../admin/libraries/Libraries").then((module) => ({
    default: module.LibrariesPage,
  })),
);
const MaintenanceCenter = lazy(() =>
  import("../admin/maintenance/MaintenanceCenter").then((module) => ({
    default: module.MaintenanceCenter,
  })),
);
const SettingsPage = lazy(() =>
  import("../admin/settings/Settings").then((module) => ({
    default: module.SettingsPage,
  })),
);
const VersionManagementPage = lazy(() =>
  import("../admin/versions/VersionManagement").then((module) => ({
    default: module.VersionManagementPage,
  })),
);
const TagsPage = lazy(() =>
  import("../admin/tags/TagsPage").then((module) => ({
    default: module.TagsPage,
  })),
);
const SeriesManagementPage = lazy(() =>
  import("../admin/series/SeriesManagement").then((module) => ({
    default: module.SeriesManagementPage,
  })),
);
import {
  CurrentLibraryProvider,
  CurrentLibraryHeader,
  CurrentLibraryGate,
} from "../admin/CurrentLibrary";
import { consoleURL } from "../shared/consoleRoutes.mjs";
import "../shared/styles.css";

const entries = [
  {
    key: "/admin/books",
    label: "漫画",
    icon: <BookOutlined aria-hidden="true" />,
  },
  {
    key: "/admin/libraries",
    label: "书库",
    icon: <FolderOutlined aria-hidden="true" />,
  },
  {
    key: "/admin/series",
    label: "系列",
    icon: <ApartmentOutlined aria-hidden="true" />,
  },
  {
    key: "/admin/versions",
    label: "版本",
    icon: <GroupOutlined aria-hidden="true" />,
  },
  {
    key: "/admin/tags",
    label: "标签",
    icon: <TagsOutlined aria-hidden="true" />,
  },
  {
    key: "/admin/maintenance",
    label: "维护中心",
    icon: <ToolOutlined aria-hidden="true" />,
  },
  {
    key: "/admin/settings",
    label: "设置",
    icon: <SettingOutlined aria-hidden="true" />,
  },
];
function legacy(value: string) {
  return consoleURL(value, window.location.origin);
}

function ConsoleScope({
  account,
  routingReady,
  children,
}: {
  account: string;
  routingReady: boolean;
  children: React.ReactNode;
}) {
  return (
    <EnhancementsProvider>
      <CurrentLibraryProvider account={account} routingReady={routingReady}>
        {children}
      </CurrentLibraryProvider>
    </EnhancementsProvider>
  );
}
function Console() {
  const location = useLocation();
  const navigate = useNavigate();
  const permissions = usePermissions();
  useLayoutEffect(() => {
    window.scrollTo({ top: 0, left: 0, behavior: "auto" });
  }, [location.pathname]);
  const [loading, setLoading] = useState(true);
  const [initialized, setInitialized] = useState(true);
  const authExpired = useRef(false);
  const [session, setSession] = useState<Session | null>(null);
  const [startupError, setStartupError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [logoutBusy, setLogoutBusy] = useState(false);
  const writing = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const [dark, setDark] = useState(
    () => window.matchMedia("(prefers-color-scheme: dark)").matches,
  );
  const [navigationCollapsed, setNavigationCollapsed] = useState(() =>
    (() => {
      try {
        return readNavigationCollapsed(window.localStorage);
      } catch {
        return true;
      }
    })(),
  );
  function toggleNavigation() {
    const next = !navigationCollapsed;
    setNavigationCollapsed(next);
    try {
      writeNavigationCollapsed(window.localStorage, next);
    } catch {
      /* Keep the in-memory preference. */
    }
  }
  const [mobileMenu, setMobileMenu] = useState(false);
  const [restoreMenuFocus, setRestoreMenuFocus] = useState(true);
  const dirty = useRef(new Map<string, DirtyOptions | undefined>());
  const register = useCallback(
    (id: string, value: boolean, options?: DirtyOptions) => {
      if (value) dirty.current.set(id, options);
      else dirty.current.delete(id);
    },
    [],
  );
  const hasDirty = useCallback(
    (preserve: DirtyPreservation = false) =>
      [...dirty.current.values()].some(
        (options) =>
          !preserve ||
          !options?.keepTagBook ||
          (Array.isArray(preserve) &&
            (!options.tagID || !preserve.includes(options.tagID))),
      ),
    [],
  );
  const [logoutPrompt, setLogoutPrompt] = useState(false);
  const mobileMenuButton = useRef<HTMLButtonElement>(null);
  const logoutFromMobile = useRef(false);
  const go = useCallback(
    (path: string) => {
      setRestoreMenuFocus(false);
      setMobileMenu(false);
      void navigate(legacy(path));
    },
    [navigate],
  );
  useEffect(() => {
    const match = window.matchMedia("(prefers-color-scheme: dark)");
    const listener = () => setDark(match.matches);
    match.addEventListener("change", listener);
    return () => match.removeEventListener("change", listener);
  }, []);
  useEffect(() => {
    const expired = () => {
      authExpired.current = true;
      rememberRecoveryReturn(window.location.pathname + window.location.search);
      setSession(null);
      setActor(null);
      dirty.current.clear();
      setNotice("会话已过期，请重新登录。未保存的修改请重新确认。");
      void navigate("/login", { replace: true });
    };
    const invalidated = () => dirty.current.clear();
    window.addEventListener("session-expired", expired);
    window.addEventListener("actor-invalidated", invalidated);
    return () => {
      window.removeEventListener("session-expired", expired);
      window.removeEventListener("actor-invalidated", invalidated);
    };
  }, [navigate]);
  useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const state = await api.status();
        if (!live) return;
        setInitialized(state.initialized);
        if (!state.initialized) {
          void navigate("/setup", { replace: true });
          return;
        }
        try {
          const user = await api.session();
          if (live) {
            setSession(user);
            const path = window.location.pathname;
            if (path === "/login" || path === "/setup")
              void navigate("/admin/books", { replace: true });
            else {
              const target = legacy(path + window.location.search);
              if (target !== path + window.location.search)
                void navigate(target, {
                  replace: true,
                });
            }
          }
        } catch (e) {
          if (!(e instanceof ApiError && e.status === 401)) throw e;
          if (live) {
            rememberRecoveryReturn(
              window.location.pathname + window.location.search,
            );
            void navigate("/login", { replace: true });
          }
        }
      } catch (e) {
        if (live) setStartupError(e);
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
    };
  }, [navigate]);
  useEffect(() => {
    if (session) {
      const next = legacy(location.pathname + location.search);
      if (next !== location.pathname + location.search)
        void navigate(next, { replace: true });
    }
  }, [location.pathname, location.search, navigate, session]);
  const [checkingACL, setCheckingACL] = useState(false);
  const aclFocus = useRef<HTMLElement | null>(null);
  const aclStartEpoch = useRef(0);
  const checkingFocus = useRef(false);
  useLayoutEffect(() => {
    if (
      !checkingACL &&
      permissions.verified &&
      aclStartEpoch.current === permissions.epoch &&
      aclFocus.current?.isConnected
    ) {
      aclFocus.current.focus({ preventScroll: true });
      aclFocus.current = null;
    }
  }, [checkingACL, permissions.verified, permissions.epoch]);
  const [aclError, setACLError] = useState<unknown>(null);
  const accountID = session?.account_id;
  useEffect(() => {
    if (!accountID) return;
    let live = true,
      reading = false,
      queued = false,
      timer: ReturnType<typeof setTimeout> | undefined;
    async function refreshActor() {
      if (reading) {
        queued = true;
        return;
      }
      reading = true;
      if (
        document.activeElement instanceof HTMLElement &&
        !checkingFocus.current &&
        !aclFocus.current?.isConnected
      ) {
        aclFocus.current = document.activeElement;
        aclStartEpoch.current = actorAccess().epoch;
      }
      checkingFocus.current = true;
      setCheckingACL(true);
      setActorVerified(false);
      setACLError(null);
      try {
        const next = await api.session();
        if (live) {
          setSession(next);
        }
      } catch (error) {
        if (live && error instanceof ApiError && error.status === 401) {
          authExpired.current = true;
          setActor(null);
          setSession(null);
          dirty.current.clear();
          void navigate("/login", { replace: true });
        } else if (
          live &&
          !(error instanceof DOMException && error.name === "AbortError")
        )
          setACLError(error);
      } finally {
        reading = false;
        if (live) {
          checkingFocus.current = false;
          setCheckingACL(false);
          if (queued) {
            queued = false;
            void refreshActor();
          }
        }
      }
    }
    function schedule(event: Event) {
      const type = (event as CustomEvent<{ type?: string }>).detail?.type;
      if (
        event.type === "actor-session-refresh" ||
        type === "account_permissions"
      ) {
        setActorVerified(false);
        clearTimeout(timer);
        timer = setTimeout(() => void refreshActor(), 100);
      }
    }
    function visible() {
      if (!document.hidden) void refreshActor();
    }
    window.addEventListener("resources-changed", schedule);
    window.addEventListener("actor-session-refresh", schedule);
    document.addEventListener("visibilitychange", visible);
    return () => {
      live = false;
      clearTimeout(timer);
      window.removeEventListener("resources-changed", schedule);
      window.removeEventListener("actor-session-refresh", schedule);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [accountID, navigate]);
  function logout() {
    if (writesPending() || logoutBusy) return;
    if (dirty.current.size) {
      logoutFromMobile.current = mobileMenu;
      if (mobileMenu) {
        setRestoreMenuFocus(false);
        setMobileMenu(false);
      }
      setLogoutPrompt(true);
      return;
    }
    void logoutConfirmed();
  }
  async function logoutConfirmed() {
    if (writesPending() || logoutBusy) return;
    setLogoutPrompt(false);
    setLogoutBusy(true);
    try {
      await api.logout();
      setSession(null);
      setActor(null);
      dirty.current.clear();
      setNotice("你已安全退出。");
      setRestoreMenuFocus(false);
      setMobileMenu(false);
      void navigate("/login");
    } catch (e) {
      setNotice(e instanceof Error ? e.message : "退出失败");
    } finally {
      setLogoutBusy(false);
    }
  }
  const path = location.pathname;
  const routingReady =
    legacy(path + location.search) === path + location.search;
  const navigationEntries = entries;
  const selected =
    navigationEntries.find(
      (e) => path === e.key || path.startsWith(e.key + "/"),
    )?.key ??
    (path.startsWith("/admin/imports") ? "/admin/libraries" : "/admin/books");
  const nonce = document.querySelector<HTMLMetaElement>(
    'meta[name="csp-nonce"]',
  )?.content;
  const navigationItems = navigationEntries.flatMap((e, index) => [
    ...(index === 5
      ? [{ type: "divider" as const, key: "management-divider" }]
      : []),
    {
      ...e,
      title: e.label,
      label: (
        <Link
          to={legacy(e.key)}
          onClick={() => {
            setRestoreMenuFocus(false);
            setMobileMenu(false);
          }}
        >
          {e.label}
        </Link>
      ),
    },
  ]);
  const menu = (
    <Menu mode="inline" selectedKeys={[selected]} items={navigationItems} />
  );
  const exitControl = (compact = false) => (
    <div className="navigation-exit">
      <Tooltip
        title={compact ? "退出登录" : undefined}
        trigger={["hover", "focus"]}
        placement="right"
      >
        <Button
          type="text"
          block
          className="logout-button"
          aria-label="退出登录"
          disabled={writing}
          loading={logoutBusy}
          icon={<LogoutOutlined aria-hidden="true" />}
          onClick={logout}
        >
          {!compact && "退出登录"}
        </Button>
      </Tooltip>
    </div>
  );
  let content: React.ReactNode;
  if (path.startsWith("/admin/series")) content = <SeriesManagementPage />;
  else if (path === "/admin/versions") content = <VersionManagementPage />;
  else if (path === "/admin/tags") content = <TagsPage />;
  else if (path === "/admin/imports") content = <ImportsPage />;
  else if (path === "/admin/libraries")
    content = <LibrariesPage navigate={go} />;
  else if (path === "/admin/maintenance")
    content = <MaintenanceCenter navigate={go} />;
  else if (path === "/admin/settings")
    content = (
      <SettingsPage
        navigate={go}
        onSecurityChanged={() => {
          authExpired.current = true;
          setActor(null);
          setSession(null);
          setNotice("密码已更新，请使用新密码登录。");
          void navigate("/login", { replace: true });
        }}
      />
    );
  else content = <BooksPage key="books" review={false} navigate={go} />;
  return (
    <ConfigProvider
      virtual={false}
      button={{
        autoInsertSpace: false,
        loadingIcon: <LoadingOutlined aria-hidden="true" />,
      }}
      getPopupContainer={(trigger) =>
        trigger?.closest<HTMLElement>(
          ".ant-modal-container, .ant-modal-content, .ant-drawer-content, .ant-drawer-container",
        ) ?? document.body
      }
      locale={zhCN}
      csp={nonce ? { nonce } : undefined}
      theme={{
        algorithm: dark ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: { colorPrimary: "#3458d4", borderRadius: 6, fontSize: 14 },
        components: {
          Table: { cellPaddingBlockSM: 10 },
          Layout: {
            headerBg: dark ? "#141414" : "#ffffff",
            siderBg: dark ? "#141414" : "#ffffff",
          },
        },
      }}
      componentSize="middle"
    >
      <AntApp>
        <DirtyContext.Provider value={register}>
          <NavigationGuard
            dirty={hasDirty}
            authExpired={() => authExpired.current}
          />
          <Modal
            open={logoutPrompt}
            zIndex={5000}
            title="放弃未保存修改并退出？"
            okText="放弃并退出"
            cancelText="继续编辑"
            okButtonProps={{ disabled: writing || logoutBusy }}
            onOk={() => void logoutConfirmed()}
            onCancel={() => setLogoutPrompt(false)}
            afterClose={() => {
              if (logoutFromMobile.current) mobileMenuButton.current?.focus();
              logoutFromMobile.current = false;
            }}
          >
            退出会丢失当前编辑草稿。
          </Modal>
          {loading ? (
            <div className="boot-state">
              <Spin size="large" description="正在连接管理服务…" />
            </div>
          ) : startupError ? (
            <div className="auth-shell">
              <ErrorNotice
                error={startupError}
                retry={() => window.location.reload()}
              />
            </div>
          ) : !session ? (
            <div className="auth-shell">
              <div className="auth-brand">
                <BookOutlined aria-hidden="true" />
                <Typography.Title level={2}>NyaMangaServer</Typography.Title>
                <Typography.Text type="secondary">
                  漫画资料管理后台
                </Typography.Text>
              </div>
              {notice && (
                <Alert
                  title={<span role="status">{notice}</span>}
                  type="info"
                  showIcon
                />
              )}
              <AuthForm
                setup={!initialized}
                onSignedIn={(user) => {
                  authExpired.current = false;
                  setSession(user);
                  setInitialized(true);
                  setNotice("");
                  void navigate(takeRecoveryReturn() || "/admin/books", {
                    replace: true,
                  });
                }}
                onInitializedElsewhere={() => {
                  setInitialized(true);
                  setNotice("管理员已完成初始化，请登录。");
                  void navigate("/login");
                }}
              />
            </div>
          ) : permissions.actor?.must_change_password ? (
            <div className="auth-shell">
              <Security
                required
                onChanged={() => {
                  authExpired.current = true;
                  setActor(null);
                  setSession(null);
                  dirty.current.clear();
                  setNotice("密码已更新，请重新登录。");
                  void navigate("/login", { replace: true });
                }}
              />
              <Button onClick={() => void logoutConfirmed()} disabled={writing}>
                退出登录
              </Button>
            </div>
          ) : (
            <>
              <Notifications key={`events:${permissions.epoch}`} />

              {aclError && (
                <Alert
                  type="warning"
                  showIcon
                  title="账号权限暂未确认，草稿保留；提交已暂停"
                  description={
                    <Button
                      onClick={() =>
                        window.dispatchEvent(new Event("actor-session-refresh"))
                      }
                    >
                      重试权限读取
                    </Button>
                  }
                />
              )}
              <div inert={checkingACL}>
                <ConfigProvider componentDisabled={!permissions.verified}>
                  <ConsoleScope
                    key={`${session.account_id}:${permissions.epoch}`}
                    account={session.account_id}
                    routingReady={routingReady}
                  >
                    <Layout
                      className={`admin-shell ${dark ? "dark" : "light"} ${navigationCollapsed ? "navigation-collapsed" : ""}`}
                    >
                      <Layout.Sider
                        width={218}
                        collapsedWidth={64}
                        collapsed={navigationCollapsed}
                        trigger={null}
                        className="desktop-sidebar"
                      >
                        <div className="navigation-toggle">
                          <Button
                            type="text"
                            aria-label={
                              navigationCollapsed
                                ? "展开管理导航"
                                : "收起管理导航"
                            }
                            aria-expanded={!navigationCollapsed}
                            title={
                              navigationCollapsed
                                ? "展开管理导航"
                                : "收起管理导航"
                            }
                            icon={
                              navigationCollapsed ? (
                                <MenuUnfoldOutlined />
                              ) : (
                                <MenuFoldOutlined />
                              )
                            }
                            onClick={toggleNavigation}
                          />
                        </div>
                        <div className="admin-logo">
                          <BookOutlined aria-hidden="true" />
                          <div>
                            NyaManga<span>管理控制台</span>
                          </div>
                        </div>
                        <div className="navigation-menu">
                          <Menu
                            mode="inline"
                            inlineCollapsed={navigationCollapsed}
                            tooltip={{ trigger: ["hover", "focus"] }}
                            selectedKeys={[selected]}
                            items={navigationItems}
                          />
                        </div>
                        {exitControl(navigationCollapsed)}
                      </Layout.Sider>
                      <Layout>
                        <Layout.Header className="admin-header">
                          <Space>
                            <Button
                              ref={mobileMenuButton}
                              className="mobile-menu-button"
                              icon={<MenuOutlined aria-hidden="true" />}
                              aria-label="打开管理导航"
                              onClick={() => {
                                setRestoreMenuFocus(true);
                                setMobileMenu(true);
                              }}
                            />
                            <Breadcrumb
                              items={[
                                { title: "管理控制台" },
                                {
                                  title: navigationEntries.find(
                                    (e) => e.key === selected,
                                  )?.label,
                                },
                              ]}
                            />
                          </Space>
                          <CurrentLibraryHeader />
                          <Space>
                            <span className="acl-status" role="status">
                              {checkingACL ? "核对权限…" : ""}
                            </span>
                            <Switch
                              checked={dark}
                              onChange={setDark}
                              checkedChildren={
                                <MoonOutlined aria-hidden="true" />
                              }
                              unCheckedChildren="亮"
                              aria-label="深色主题"
                            />
                          </Space>
                        </Layout.Header>
                        <Layout.Content
                          className={`admin-content ${["/admin/versions", "/admin/series"].includes(path) ? "version-page-content" : ""}`}
                        >
                          {notice && (
                            <Alert
                              title={<span role="status">{notice}</span>}
                              type="info"
                              closable
                              onClose={() => setNotice("")}
                            />
                          )}{" "}
                          {permissions.admin && (
                            <HealthProtection navigate={go} />
                          )}
                          <Suspense
                            fallback={<Spin description="正在载入管理页面…" />}
                          >
                            {routingReady ? (
                              path === "/admin/maintenance" ? (
                                content
                              ) : (
                                <CurrentLibraryGate>
                                  {content}
                                </CurrentLibraryGate>
                              )
                            ) : (
                              <Spin description="正在规范管理链接…" />
                            )}
                          </Suspense>
                        </Layout.Content>
                      </Layout>
                    </Layout>
                  </ConsoleScope>
                </ConfigProvider>
              </div>
            </>
          )}
          <Drawer
            title="管理导航"
            focusable={{ focusTriggerAfterClose: restoreMenuFocus }}
            placement="left"
            open={mobileMenu}
            onClose={() => setMobileMenu(false)}
            size={270}
            className="navigation-drawer"
          >
            <div className="navigation-menu">{menu}</div>
            {exitControl()}
          </Drawer>
        </DirtyContext.Provider>
      </AntApp>
    </ConfigProvider>
  );
}
const router = createBrowserRouter([{ path: "*", Component: Console }]);
export default function App() {
  return <RouterProvider router={router} />;
}
