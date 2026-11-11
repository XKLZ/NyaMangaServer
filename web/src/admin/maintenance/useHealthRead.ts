import { useEffect, useState } from "react";
import { request } from "../../shared/api";
import { resourceEvent } from "../../shared/events";
/** Health views are global; they deliberately do not inherit the library read boundary. */
export function useHealthRead<T>(path: string, enabled: boolean, poll = false) {
  const [version, setVersion] = useState(0),
    [snapshot, setSnapshot] = useState<{ path: string; data: T } | null>(null),
    [failure, setFailure] = useState<{ path: string; error: unknown } | null>(
      null,
    );
  useEffect(() => {
    if (!enabled) return;
    let controller: AbortController | undefined,
      live = true,
      pollNeeded = true,
      reading = false,
      queued = false,
      epoch = 0,
      refreshTimer: ReturnType<typeof setTimeout> | undefined;
    function schedule() {
      if (document.hidden || !live || refreshTimer !== undefined) return;
      refreshTimer = setTimeout(() => {
        refreshTimer = undefined;
        void load();
      }, 200);
    }
    async function load() {
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
        const data = await request<T>(
          path,
          "GET",
          undefined,
          false,
          own.signal,
        );
        if (live && !own.signal.aborted && captured === epoch) {
          pollNeeded =
            !data ||
            typeof data !== "object" ||
            ![
              "completed",
              "rolled_back",
              "paused",
              "failed",
              "cancelled",
              "uploaded",
              "validated",
              "previewed",
              "awaiting_restart",
              "abandoned",
            ].includes(
              String(
                (data as { state?: string; operation?: { state?: string } })
                  .state ||
                  (data as { operation?: { state?: string } }).operation?.state,
              ),
            );
          setSnapshot({ path, data });
          setFailure(null);
        }
      } catch (error) {
        if (live && !own.signal.aborted && captured === epoch)
          setFailure({ path, error });
      } finally {
        reading = false;
        if (queued && live) {
          queued = false;
          schedule();
        }
      }
    }
    function visible() {
      if (document.hidden) {
        epoch++;
        controller?.abort();
        clearTimeout(refreshTimer);
        refreshTimer = undefined;
        queued = false;
      } else void load();
    }
    function change(e: Event) {
      const t = (e as CustomEvent<{ type?: string }>).detail?.type;
      if (
        !t ||
        (path === "/libraries" && t === "library") ||
        [
          "health_check",
          "health_repair",
          "health_state",
          "instance_operation",
        ].includes(t)
      ) {
        epoch++;
        schedule();
      }
    }
    void load();
    const timer = poll
      ? setInterval(() => {
          if (pollNeeded) void load();
        }, 3000)
      : undefined;
    document.addEventListener("visibilitychange", visible);
    window.addEventListener(resourceEvent, change);
    return () => {
      live = false;
      controller?.abort();
      clearTimeout(refreshTimer);
      clearInterval(timer);
      document.removeEventListener("visibilitychange", visible);
      window.removeEventListener(resourceEvent, change);
    };
  }, [path, enabled, poll, version]);
  return {
    data: enabled && snapshot?.path === path ? snapshot.data : null,
    error: failure?.path === path ? failure.error : null,
    reload: () => setVersion((v) => v + 1),
  };
}
