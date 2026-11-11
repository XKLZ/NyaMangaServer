import { useLocation } from "react-router";
import { useEffect, useSyncExternalStore } from "react";
import { Alert, Button, Space } from "antd";
import { request } from "../../shared/api";
import { resourceEvent } from "../../shared/events";
import {
  healthAccess,
  resetHealth,
  subscribeHealth,
  updateHealth,
} from "../../shared/healthAccess.mjs";
import { healthPhases, healthURL } from "../../shared/healthFlow";
import type { InstanceState } from "../../shared/instanceFlow";
import type { HealthState } from "../../shared/healthFlow";

export function HealthProtection({
  navigate,
}: {
  navigate: (url: string) => void;
}) {
  const location = useLocation();
  const onRecovery =
    location.pathname === "/admin/maintenance" &&
    new URLSearchParams(location.search).get("section") === "recovery";
  const value = useSyncExternalStore(
    subscribeHealth,
    healthAccess,
    healthAccess,
  );
  useEffect(() => {
    let live = true,
      controller: AbortController | undefined,
      reading = false,
      queued = false,
      epoch = 0,
      refreshTimer: ReturnType<typeof setTimeout> | undefined;
    function schedule() {
      if (document.hidden || !live || refreshTimer !== undefined) return;
      refreshTimer = setTimeout(() => {
        refreshTimer = undefined;
        void read();
      }, 200);
    }
    async function read() {
      if (document.hidden) return;
      if (reading) {
        queued = true;
        return;
      }
      reading = true;
      const captured = epoch;
      const own = new AbortController();
      controller = own;
      try {
        const service = await request<{ database_schema: number }>(
          "/settings/service",
          "GET",
          undefined,
          false,
          own.signal,
        );
        if (!live || own.signal.aborted || captured !== epoch) return;
        if (service.database_schema < 23) {
          updateHealth({
            supported: false,
            state: null,
            error: false,
            instanceSupported: false,
            instanceState: null,
            instanceError: false,
          });
          return;
        }
        updateHealth({ supported: true });
        const state = await request<HealthState>(
          "/maintenance/health-state",
          "GET",
          undefined,
          false,
          own.signal,
        );
        if (live && !own.signal.aborted && captured === epoch)
          updateHealth({ state, error: false });
        if (!live || own.signal.aborted || captured !== epoch) return;
        if (service.database_schema >= 24) {
          updateHealth({ instanceSupported: true });
          try {
            const instanceState = await request<InstanceState>(
              "/maintenance/instance-state",
              "GET",
              undefined,
              false,
              own.signal,
            );
            if (live && !own.signal.aborted && captured === epoch)
              updateHealth({ instanceState, instanceError: false });
          } catch {
            if (live && !own.signal.aborted && captured === epoch)
              updateHealth({ instanceError: true });
          }
        } else
          updateHealth({
            instanceSupported: false,
            instanceState: null,
            instanceError: false,
          });
      } catch {
        if (live && !own.signal.aborted && captured === epoch)
          updateHealth({ error: true });
      } finally {
        reading = false;
        if (queued && live) {
          queued = false;
          schedule();
        }
      }
    }
    function event(e: Event) {
      const type = (e as CustomEvent<{ type?: string }>).detail?.type;
      if (
        !type ||
        type === "health_state" ||
        type === "health_repair" ||
        type === "instance_operation"
      ) {
        epoch++;
        schedule();
      }
    }
    function visible() {
      if (document.hidden) {
        epoch++;
        controller?.abort();
        clearTimeout(refreshTimer);
        refreshTimer = undefined;
        queued = false;
      } else void read();
    }
    void read();
    const timer = setInterval(() => void read(), 30000);
    window.addEventListener(resourceEvent, event);
    window.addEventListener("health-state-refresh", event);
    document.addEventListener("visibilitychange", visible);
    return () => {
      live = false;
      controller?.abort();
      clearTimeout(refreshTimer);
      clearInterval(timer);
      window.removeEventListener(resourceEvent, event);
      window.removeEventListener("health-state-refresh", event);
      document.removeEventListener("visibilitychange", visible);
      resetHealth();
    };
  }, []);
  const protectedNow =
    value.supported === true &&
    (value.error || !value.state || value.state.phase !== "idle");
  const instance = value.state?.phase === "instance_maintenance";
  const recoveryURL = "/admin/maintenance?section=recovery";
  return (
    <>
      {protectedNow && (
        <Alert
          className="health-global-status"
          type="warning"
          showIcon
          title={
            value.error
              ? "维护状态暂不可读，继续保留写入保护"
              : instance
                ? "整实例维护保护中"
                : `全局维护保护 · ${healthPhases[value.state?.phase || ""] || "正在维护"}`
          }
          description={
            <Space wrap>
              <span>浏览和草稿保留，业务写入暂时只读。</span>
              {!(instance && onRecovery) && (
                <Button
                  size="small"
                  onClick={() =>
                    navigate(
                      instance
                        ? recoveryURL
                        : healthURL(value.state?.repair_id || ""),
                    )
                  }
                >
                  {instance ? "查看备份与恢复" : "查看维护与恢复"}
                </Button>
              )}
              <Button
                size="small"
                type="text"
                onClick={() =>
                  window.dispatchEvent(new Event("health-state-refresh"))
                }
              >
                刷新状态
              </Button>
            </Space>
          }
        />
      )}
      {value.instanceSupported &&
        !onRecovery &&
        !protectedNow &&
        (value.instanceState?.background_hold || value.instanceError) && (
          <Alert
            className="health-global-status"
            type="info"
            showIcon
            title={
              value.instanceError
                ? "后台调度状态暂不可读"
                : "后台自动处理已暂停"
            }
            description={
              <Space wrap>
                <span>业务编辑可用；已暂停的任务仍需手动恢复。</span>
                <Button size="small" onClick={() => navigate(recoveryURL)}>
                  查看后台恢复入口
                </Button>
              </Space>
            }
          />
        )}
    </>
  );
}
