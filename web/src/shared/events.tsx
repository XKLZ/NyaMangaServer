import { useEffect } from "react";
export const resourceEvent = "resources-changed";
export function useResourceChanges(refresh: () => void, interval = 5000) {
  useEffect(() => {
    window.addEventListener(resourceEvent, refresh);
    const timer = window.setInterval(refresh, interval);
    return () => {
      window.removeEventListener(resourceEvent, refresh);
      clearInterval(timer);
    };
  }, [refresh, interval]);
}
// Fetch permits an explicit replay cursor and bounded exponential reconnects.
// Notifications carry IDs only; each view still queries its normal JSON API.
export function Notifications() {
  useEffect(() => {
    let stopped = false;
    let controller = new AbortController();
    let retry: ReturnType<typeof setTimeout>;
    let watchdog: ReturnType<typeof setTimeout>;
    let last = "";
    let delay = 1000;
    const notify = (detail?: { type?: string }) =>
      window.dispatchEvent(new CustomEvent(resourceEvent, { detail }));
    async function connect() {
      controller = new AbortController();
      try {
        const response = await fetch("/api/v1/events", {
          credentials: "same-origin",
          headers: last ? { "Last-Event-ID": last } : {},
          signal: controller.signal,
        });
        if (response.status === 401) {
          window.dispatchEvent(new Event("session-expired"));
          return;
        }
        if (!response.ok || !response.body)
          throw new Error("stream unavailable");
        if (last) notify();
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (!stopped) {
          clearTimeout(watchdog);
          watchdog = setTimeout(() => controller.abort(), 40000);
          const { done, value } = await reader.read();
          if (done) break;
          delay = 1000;
          buffer += decoder.decode(value, { stream: true });
          if (buffer.length > 65536) throw new Error("invalid event stream");
          let end: number;
          while ((end = buffer.indexOf("\n\n")) >= 0) {
            const block = buffer.slice(0, end);
            buffer = buffer.slice(end + 2);
            for (const line of block.split("\n"))
              if (line.startsWith("id: ")) last = line.slice(4);
            if (
              block.includes("event: change") ||
              block.includes("event: resync_required")
            ) {
              const data = block
                .split("\n")
                .find((line) => line.startsWith("data: "));
              try {
                notify(data ? JSON.parse(data.slice(6)) : undefined);
              } catch {
                notify();
              }
            }
          }
        }
      } catch {
        /* Periodic JSON polling remains available while disconnected. */
      } finally {
        clearTimeout(watchdog);
        controller.abort();
      }
      if (!stopped) {
        retry = setTimeout(() => void connect(), delay);
        delay = Math.min(delay * 2, 30000);
      }
    }
    void connect();
    return () => {
      stopped = true;
      controller.abort();
      clearTimeout(retry);
      clearTimeout(watchdog);
    };
  }, []);
  return null;
}
