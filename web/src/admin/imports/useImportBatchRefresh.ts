import { useEffect } from "react";
import { resourceEvent } from "../../shared/events";
/** One owner for batch polling; events are hints, never stage transitions. */
export function useImportBatchRefresh(
  id: string,
  state: string | undefined,
  reload: () => void,
  jobIDs = "",
) {
  useEffect(() => {
    let merged: ReturnType<typeof setTimeout> | undefined;
    const visible = () => {
      if (document.visibilityState === "visible") reload();
    };
    const notify = (event: Event) => {
      const detail = (
        event as CustomEvent<{ type?: string; resource_id?: string }>
      ).detail;
      if (
        detail?.type &&
        detail.type !== "enhancements" &&
        !(detail.type === "import_batch" && detail.resource_id === id) &&
        !(
          detail.type === "job" &&
          jobIDs.split(",").includes(detail.resource_id ?? "")
        )
      )
        return;
      if (!merged)
        merged = setTimeout(() => {
          merged = undefined;
          visible();
        }, 250);
    };
    const timer =
      state === "processing" ? setInterval(visible, 2000) : undefined;
    document.addEventListener("visibilitychange", visible);
    window.addEventListener(resourceEvent, notify);
    return () => {
      clearInterval(timer);
      clearTimeout(merged);
      document.removeEventListener("visibilitychange", visible);
      window.removeEventListener(resourceEvent, notify);
    };
  }, [id, state, reload, jobIDs]);
}
