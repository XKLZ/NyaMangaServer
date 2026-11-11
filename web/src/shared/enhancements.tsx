import { usePermissions } from "./permissions";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import { request } from "./api";
import { resourceEvent } from "./events";
import type {
  EnhancementPatch,
  Enhancements,
  FeatureKey,
  ServiceInfo,
} from "./enhancementFlow";
export const enhancementAPI = {
  read: (signal?: AbortSignal) =>
    request<Enhancements>(
      "/settings/enhancements",
      "GET",
      undefined,
      false,
      signal,
    ),
  change: (patch: EnhancementPatch) =>
    request<Enhancements>("/settings/enhancements", "PATCH", patch),
  service: (signal?: AbortSignal) =>
    request<ServiceInfo>("/settings/service", "GET", undefined, false, signal),
};
type Shared = {
  data: Enhancements | null;
  error: unknown;
  loading: boolean;
  reload: () => void;
  publish: (value: Enhancements) => void;
  retain: () => () => void;
};
const Context = createContext<Shared | null>(null);
/** A single account-scoped read owner. Consumers opt in; settings for a hidden
 * group have no leases, no reads and no timers. No library parameter is used. */
export function EnhancementsProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [data, setData] = useState<Enhancements | null>(null),
    [error, setError] = useState<unknown>(null),
    [loading, setLoading] = useState(false),
    [leases, setLeases] = useState(0),
    [epoch, setEpoch] = useState(0);
  const sequence = useRef(0);
  const active = leases > 0;
  const reload = useCallback(() => setEpoch((v) => v + 1), []);
  const retain = useCallback(() => {
    setLeases((v) => v + 1);
    return () => setLeases((v) => v - 1);
  }, []);
  const publish = useCallback((next: Enhancements) => {
    sequence.current++;
    setData((old) => (!old || next.revision >= old.revision ? next : old));
    setError(null);
    setLoading(false);
  }, []);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController(),
      generation = ++sequence.current;
    void Promise.resolve().then(async () => {
      if (controller.signal.aborted) return;
      setLoading(true);
      try {
        const next = await enhancementAPI.read(controller.signal);
        if (!controller.signal.aborted && generation === sequence.current) {
          setData((old) =>
            !old || next.revision >= old.revision ? next : old,
          );
          setError(null);
        }
      } catch (e) {
        if (!controller.signal.aborted && generation === sequence.current)
          setError(e);
      } finally {
        if (!controller.signal.aborted && generation === sequence.current)
          setLoading(false);
      }
    });
    return () => controller.abort();
  }, [active, epoch]);
  useEffect(() => {
    if (!active) return;
    const changed = (event: Event) => {
      const detail = (event as CustomEvent<{ type?: string }>).detail;
      if (!detail?.type || detail.type === "enhancements") reload();
    };
    window.addEventListener(resourceEvent, changed);
    return () => window.removeEventListener(resourceEvent, changed);
  }, [active, reload]);
  return (
    <Context.Provider value={{ data, error, loading, reload, publish, retain }}>
      {children}
    </Context.Provider>
  );
}
export function useEnhancements(enabled = true) {
  const admin = usePermissions().admin;
  enabled = enabled && admin;
  const shared = useContext(Context);
  if (!shared) throw Error("EnhancementsProvider is missing");
  const retain = shared.retain;
  useEffect(() => (enabled ? retain() : undefined), [enabled, retain]);
  return { ...shared, data: enabled ? shared.data : null };
}
export function useFeature(feature?: FeatureKey) {
  const state = useEnhancements(!!feature);
  return {
    ...state,
    feature: feature ? state.data?.features[feature] : undefined,
  };
}
