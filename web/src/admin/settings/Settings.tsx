import { usePermissions } from "../../shared/permissions";
import { AccountsSettings } from "./AccountsSettings";
/** §2.1/§3.3/§3.6: a quiet category rail and one readable content column.
 * React / Ant / native CSS, existing theme tokens and 8/16/24 spacing. Service
 * facts precede optional diagnostics; switches are permissions, never probes. */
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useLocation } from "react-router";
import { Button, Collapse, Descriptions, Select, Skeleton, theme } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { subscribeWrites, writesPending } from "../../shared/api";
import type { ProviderSettings } from "../../shared/metadata";
import type { ServiceInfo } from "../../shared/enhancementFlow";
import { settingsSection, sectionURL } from "../../shared/consoleRoutes.mjs";
import { Security } from "../../features/auth/Auth";
import { useRetainedRemote } from "../useRetainedRemote";
import { ErrorNotice, FormLock, useRemote } from "../common";
import { ProviderEditor, SecretsEditor } from "./SettingsEditors";
import { EnhancementSettings } from "./EnhancementSettings";
import "./settings.css";
const sections = {
  overview: "概览",
  enhancements: "增强功能",
  metadata: "元数据与网络",
  security: "账号与安全",
};
export function SettingsPage({
  navigate,
  onSecurityChanged,
}: {
  navigate: (path: string) => void;
  onSecurityChanged: () => void;
}) {
  const permissions = usePermissions();
  const entries = Object.entries(sections).filter(
    ([key]) => permissions.admin || key === "security",
  );
  const location = useLocation(),
    view = permissions.admin ? settingsSection(location.search) : "security",
    { token } = theme.useToken();
  const busy = useSyncExternalStore(
    subscribeWrites,
    writesPending,
    writesPending,
  );
  const requestedFocus =
    new URLSearchParams(location.search).get("focus") || "";
  const focus = [
    "provider",
    "credentials",
    "work_recommendations",
    "series_recommendations",
    "chinese_dictionary",
    "local_gallery_highlight",
  ].includes(requestedFocus)
    ? requestedFocus
    : "";
  const content = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!focus) return;
    const target = content.current?.querySelector<HTMLElement>(
      `[id="settings-${focus}"]`,
    );
    target?.scrollIntoView({ block: "start" });
    target?.focus({ preventScroll: true });
  }, [view, focus]);
  const change = (section: string) =>
    navigate(sectionURL("/admin/settings", location.search, section));
  return (
    <section
      className="settings-page settings-workspace"
      style={
        {
          "--settings-border": token.colorBorderSecondary,
          "--settings-muted": token.colorTextSecondary,
          "--settings-active": token.controlItemBgActive,
          "--settings-hover": token.colorBgTextHover,
        } as React.CSSProperties
      }
    >
      <h1 className="sr-only">设置</h1>
      <nav className="settings-navigation" aria-label="设置分类">
        {entries.map(([key, label]) => (
          <Button
            key={key}
            type="text"
            className={view === key ? "selected" : ""}
            aria-current={view === key ? "page" : undefined}
            disabled={busy}
            onClick={() => change(key)}
          >
            {label}
          </Button>
        ))}
      </nav>
      <Select
        className="settings-mobile-navigation"
        aria-label="设置分类"
        value={view}
        disabled={busy}
        onChange={change}
        options={entries.map(([value, label]) => ({
          value,
          label,
        }))}
      />
      <div className="settings-content" ref={content}>
        <FormLock busy={busy}>
          {view === "overview" && <ServiceOverview />}
          {view === "enhancements" && (
            <EnhancementSettings focus={focus} navigate={navigate} />
          )}
          {view === "metadata" && <MetadataSettings focus={focus} />}
          {view === "security" && (
            <div className="settings-form">
              <p>
                当前账号：{permissions.actor?.username} ·{" "}
                {permissions.admin ? "管理员" : "普通用户"}
              </p>
              <Security onChanged={onSecurityChanged} />
              {permissions.actor?.capabilities.manage_accounts && (
                <AccountsSettings />
              )}
            </div>
          )}
        </FormLock>
      </div>
    </section>
  );
}
function ServiceOverview() {
  const service = useRetainedRemote<ServiceInfo>("/settings/service");
  const [diagnostics, setDiagnostics] = useState(false);
  const data = service.data;
  return (
    <>
      <div className="settings-section-actions">
        <Button
          aria-label="刷新服务信息"
          type="text"
          icon={<ReloadOutlined />}
          onClick={service.reload}
        />
      </div>
      <ErrorNotice error={service.error} retry={service.reload} />
      {!data && !service.error && <Skeleton active paragraph={{ rows: 6 }} />}
      {data && (
        <>
          <section className="settings-group">
            <h2>服务</h2>
            <Descriptions
              size="small"
              column={1}
              items={[
                {
                  key: "version",
                  label: "服务端版本",
                  children: data.version || "unknown",
                },
                {
                  key: "commit",
                  label: "构建提交",
                  children: (
                    <span className="settings-value">
                      {data.commit || "unknown"}
                    </span>
                  ),
                },
                {
                  key: "state",
                  label: "运行状态",
                  children:
                    data.state === "ready" ? "就绪" : data.state || "未知",
                },
                {
                  key: "start",
                  label: "启动时间",
                  children:
                    data.started_at &&
                    Number.isFinite(Date.parse(data.started_at))
                      ? new Date(data.started_at).toLocaleString()
                      : "未知",
                },
                {
                  key: "uptime",
                  label: "运行时长",
                  children: Number.isFinite(data.uptime_seconds)
                    ? `${Math.floor(data.uptime_seconds / 86400)}天 ${Math.floor((data.uptime_seconds % 86400) / 3600)}小时 ${Math.floor((data.uptime_seconds % 3600) / 60)}分钟`
                    : "未知",
                },
                {
                  key: "system",
                  label: "系统与架构",
                  children: `${data.os || "unknown"} / ${data.architecture || "unknown"}`,
                },
              ]}
            />
          </section>
          <section className="settings-group">
            <h2>数据库</h2>
            <Descriptions
              size="small"
              column={1}
              items={[
                {
                  key: "schema",
                  label: "Schema",
                  children: data.database_schema || "未知",
                },
                {
                  key: "baseline",
                  label: "基线",
                  children: data.database_baseline || "未知",
                },
              ]}
            />
          </section>
          <Collapse
            ghost
            activeKey={diagnostics ? ["diagnostics"] : []}
            onChange={(keys) => setDiagnostics(keys.includes("diagnostics"))}
            items={[
              {
                key: "diagnostics",
                label: "诊断信息",
                children: diagnostics ? <ServiceDiagnostics /> : null,
              },
            ]}
          />
        </>
      )}
    </>
  );
}
function ServiceDiagnostics() {
  const diagnostics = useRetainedRemote<{
    version: string;
    commit: string;
    schema_version: number;
    database_baseline: string;
  }>("/diagnostics");
  return (
    <>
      <ErrorNotice error={diagnostics.error} retry={diagnostics.reload} />
      {diagnostics.data ? (
        <Descriptions
          size="small"
          column={1}
          items={Object.entries({
            version: diagnostics.data.version,
            commit: diagnostics.data.commit,
            schema_version: diagnostics.data.schema_version,
            database_baseline: diagnostics.data.database_baseline,
          }).map(([key, children]) => ({
            key,
            label:
              (
                {
                  version: "构建版本",
                  commit: "构建提交",
                  schema_version: "Schema",
                  database_baseline: "基线",
                } as Record<string, string>
              )[key] || key,
            children,
          }))}
        />
      ) : (
        !diagnostics.error && <Skeleton active paragraph={{ rows: 2 }} />
      )}
    </>
  );
}
function MetadataSettings({ focus }: { focus: string }) {
  const settings = useRemote<ProviderSettings>("/settings/metadata-provider");
  const ready = !!settings.data;
  useEffect(() => {
    if (!ready || !["provider", "credentials"].includes(focus)) return;
    const target = document.getElementById(`settings-${focus}`);
    target?.scrollIntoView({ block: "start" });
    target?.focus({ preventScroll: true });
  }, [ready, focus]);
  return (
    <>
      <div className="settings-section-actions">
        <Button
          type="text"
          aria-label="刷新元数据设置"
          icon={<ReloadOutlined />}
          onClick={settings.reload}
        />
      </div>
      <ErrorNotice error={settings.error} retry={settings.reload} />
      {settings.data ? (
        <>
          <ProviderEditor initial={settings.data} onSaved={settings.setData} />
          <SecretsEditor initial={settings.data} onSaved={settings.setData} />
        </>
      ) : (
        !settings.error && <Skeleton active />
      )}
    </>
  );
}
