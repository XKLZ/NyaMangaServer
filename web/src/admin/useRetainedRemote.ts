import { useCallback, useContext, useEffect, useState } from "react";
import { BusinessReadContext } from "../shared/readBoundary";
import { request } from "../shared/api";
/** A GET refresh updates a value in place; only a changed identity starts a new
 * loading view. Aborted and late reads can never replace a newer path's data. */
export function useRetainedRemote<T>(path: string, enabled = true) {
  const readable = useContext(BusinessReadContext);
  const [snapshot, setSnapshot] = useState<{ path: string; value: T } | null>(
      null,
    ),
    [failure, setFailure] = useState<{ path: string; error: unknown } | null>(
      null,
    );
  const [pending, setPending] = useState(false),
    [revision, setRevision] = useState(0);
  const reload = useCallback(() => setRevision((v) => v + 1), []);
  useEffect(() => {
    if (!enabled || !readable) return;
    const controller = new AbortController();
    void Promise.resolve().then(async () => {
      if (controller.signal.aborted) return;
      setPending(true);
      try {
        const value = await request<T>(
          path,
          "GET",
          undefined,
          false,
          controller.signal,
        );
        if (!controller.signal.aborted) {
          setSnapshot({ path, value });
          setFailure(null);
        }
      } catch (error) {
        if (!controller.signal.aborted) setFailure({ path, error });
      } finally {
        if (!controller.signal.aborted) setPending(false);
      }
    });
    return () => controller.abort();
  }, [path, enabled, readable, revision]);
  const data = enabled && snapshot?.path === path ? snapshot.value : null,
    error = failure?.path === path ? failure.error : null;
  return {
    data,
    error,
    reload,
    loading: enabled && !data && !error,
    refreshing: pending && !!data,
    loadedPath: snapshot?.path ?? "",
  };
}
