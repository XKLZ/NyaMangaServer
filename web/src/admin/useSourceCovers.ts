import { useCallback, useEffect, useRef, useState } from "react";
import { loadCoverImages } from "./coverFlow";
import { readSourceCover } from "./sourceCoverFlow";

type ImageState = { url?: string; error?: string };
/** Automatically read only the displayed page, with two cancellable workers. */
export function useSourceCovers(scope: string, paths: string[]) {
  const signature = JSON.stringify(paths);
  const cache = useRef(new Map<string, ImageState>());
  const previousScope = useRef(scope);
  const [revision, setRevision] = useState(0);
  const [snapshot, setSnapshot] = useState<{
    scope: string;
    images: Record<string, ImageState>;
  }>({ scope, images: {} });
  useEffect(() => {
    const controller = new AbortController();
    const current = JSON.parse(signature) as string[];
    const keep = new Set(current);
    for (const [path, image] of cache.current) {
      if (previousScope.current !== scope || !keep.has(path)) {
        if (image.url) URL.revokeObjectURL(image.url);
        cache.current.delete(path);
      }
    }
    previousScope.current = scope;
    const publish = () =>
      setSnapshot({ scope, images: Object.fromEntries(cache.current) });
    void Promise.resolve().then(async () => {
      if (controller.signal.aborted) return;
      publish();
      await loadCoverImages(
        current.filter((path) => !cache.current.has(path)),
        controller.signal,
        readSourceCover,
        (path, image) => {
          cache.current.set(path, image);
          publish();
        },
        URL.revokeObjectURL,
      );
    });
    return () => controller.abort();
  }, [scope, signature, revision]);
  useEffect(() => {
    const resources = cache.current;
    return () => {
      for (const image of resources.values())
        if (image.url) URL.revokeObjectURL(image.url);
      resources.clear();
    };
  }, []);
  const retry = useCallback((path: string) => {
    const image = cache.current.get(path);
    if (image?.url) URL.revokeObjectURL(image.url);
    cache.current.delete(path);
    setSnapshot((previous) => ({
      ...previous,
      images: { ...previous.images, [path]: {} },
    }));
    setRevision((v) => v + 1);
  }, []);
  const fail = useCallback((path: string) => {
    const image = cache.current.get(path);
    if (image?.url) URL.revokeObjectURL(image.url);
    const error = { error: "封面图片无法显示，请重试。" };
    cache.current.set(path, error);
    setSnapshot((previous) => ({
      ...previous,
      images: { ...previous.images, [path]: error },
    }));
  }, []);
  return {
    images: snapshot.scope === scope ? snapshot.images : {},
    retry,
    fail,
  };
}
