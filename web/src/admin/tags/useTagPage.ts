import { useCallback, useContext, useEffect, useRef, useState } from "react";
import { BusinessReadContext } from "../../shared/readBoundary";
import { tagCatalog } from "../../shared/tagCatalog";
import type { CatalogInput, CatalogPage } from "../../shared/tagCatalog";
/** Each mounted endpoint owns its own pagination token. A deep page first reads
 * page one for that exact query; stale tokens require an explicit restart. */
export function useTagPage<T, P extends CatalogPage<T> = CatalogPage<T>>(
  endpoint: string,
  input: CatalogInput,
  enabled = true,
) {
  const readable = useContext(BusinessReadContext);
  const { page = 1, snapshot: supplied, ...conditions } = input;
  const identity = JSON.stringify([endpoint, conditions]);
  const key = `${identity}:${page}`;
  const token = useRef<{ identity: string; value: string } | null>(null);
  const [value, setValue] = useState<{ key: string; data: P } | null>(null);
  const [failure, setFailure] = useState<{
    key: string;
    error: unknown;
  } | null>(null);
  const [pending, setPending] = useState(false),
    [revision, setRevision] = useState(0);
  const reload = useCallback(() => setRevision((v) => v + 1), []);
  const restart = useCallback(() => {
    token.current = null;
    setRevision((v) => v + 1);
  }, []);
  useEffect(() => {
    if (!enabled || !readable || !JSON.parse(identity)[1].library_id) return;
    const abort = new AbortController();
    void Promise.resolve().then(async () => {
      if (abort.signal.aborted) return;
      setPending(true);
      try {
        const [path, query] = JSON.parse(identity) as [string, CatalogInput];
        let snapshot =
          supplied ||
          (token.current?.identity === identity
            ? token.current.value
            : undefined);
        if (page > 1 && !snapshot) {
          const first = await tagCatalog.page<T>(
            path,
            { ...query, page: 1 },
            abort.signal,
          );
          snapshot = first.snapshot;
        }
        const data = (await tagCatalog.page<T>(
          path,
          { ...query, page, ...(page > 1 ? { snapshot } : {}) },
          abort.signal,
        )) as P;
        if (!abort.signal.aborted) {
          token.current = { identity, value: data.snapshot };
          setValue({ key, data });
          setFailure(null);
        }
      } catch (error) {
        if (!abort.signal.aborted) setFailure({ key, error });
      } finally {
        if (!abort.signal.aborted) setPending(false);
      }
    });
    return () => abort.abort();
  }, [identity, key, page, supplied, enabled, readable, revision]);
  return {
    data: enabled && value?.key === key ? value.data : null,
    error: failure?.key === key ? failure.error : null,
    pending: enabled && readable && pending,
    reload,
    restart,
  };
}
