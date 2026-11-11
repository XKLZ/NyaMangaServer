import { useLayoutEffect, useRef } from "react";
import type { RefObject } from "react";
/** Fit the actual remaining viewport below the shell, including library notices
 * and the mobile visual viewport. Only the directory body scrolls. */
export function useTagWorkspace(
  page: RefObject<HTMLElement | null>,
  scroller: RefObject<HTMLDivElement | null>,
  identity: string,
) {
  const positions = useRef<Record<string, number>>({});
  useLayoutEffect(() => {
    const node = page.current;
    if (!node) return;
    const measure = () => {
      const parent =
        node.closest<HTMLElement>(".admin-content") || node.parentElement;
      const bottom = parent
        ? parseFloat(getComputedStyle(parent).paddingBottom) || 0
        : 0;
      const viewport = window.visualViewport;
      const height =
        (viewport?.height ?? window.innerHeight) + (viewport?.offsetTop ?? 0);
      node.style.setProperty(
        "--tag-workspace-height",
        `${Math.max(0, height - node.getBoundingClientRect().top - bottom)}px`,
      );
    };
    measure();
    const observer = new ResizeObserver(measure);
    const parent =
      node.closest<HTMLElement>(".admin-content") || node.parentElement;
    if (parent) observer.observe(parent);
    const header = document.querySelector(".admin-header");
    if (header) observer.observe(header);
    window.addEventListener("resize", measure);
    window.visualViewport?.addEventListener("resize", measure);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
      window.visualViewport?.removeEventListener("resize", measure);
    };
  }, [page]);
  useLayoutEffect(() => {
    const node = scroller.current;
    node?.scrollTo({ top: positions.current[identity] || 0 });
    const saved = positions.current;
    return () => {
      if (node) saved[identity] = node.scrollTop;
    };
  }, [identity, scroller]);
}
