import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import type { ReactNode, PointerEvent, KeyboardEvent } from "react";
import { App as AntApp, Button, Tooltip } from "antd";
import { HolderOutlined } from "@ant-design/icons";
import type { SeriesProposal } from "../../shared/seriesDiscovery";
import type { SeriesDrop } from "./seriesDragFlow";
export type SeriesDragItem = {
  kind: "member" | "proposal" | "recommendation";
  id: string;
  scope: string;
  label: string;
  order: string[];
  expected?: Record<string, number>;
  proposal?: SeriesProposal;
};
type Handler = (item: SeriesDragItem, drop: SeriesDrop) => Promise<void> | void;
type Active = {
  item: SeriesDragItem;
  target: SeriesDrop;
  keyboard: boolean;
  x: number;
  y: number;
};
type DragAPI = {
  active: Active | null;
  busy: boolean;
  register: (scope: string, handler: Handler) => () => void;
  pointer: (e: PointerEvent, item: SeriesDragItem) => void;
  key: (e: KeyboardEvent, item: SeriesDragItem) => void;
};
const Context = createContext<DragAPI | null>(null);
function remaining(item: SeriesDragItem) {
  const moving =
    item.kind === "recommendation"
      ? (item.proposal?.new_work_ids ?? [])
      : [item.id];
  return item.order.filter((id) => !moving.includes(id));
}
function useDrag() {
  const value = useContext(Context);
  if (!value) throw Error("Missing series drag context");
  return value;
}
export function SeriesDragProvider({
  children,
  onActivity,
}: {
  children: ReactNode;
  onActivity: (busy: boolean) => void;
}) {
  const { message } = AntApp.useApp();
  const [active, setActive] = useState<Active | null>(null),
    [busy, setBusy] = useState(false);
  const current = useRef<Active | null>(null),
    handlers = useRef(new Map<string, Handler>());
  const gesture = useRef<{
    item: SeriesDragItem;
    x: number;
    y: number;
    id: number;
  } | null>(null);
  const set = useCallback((value: Active | null) => {
    current.current = value;
    setActive(value);
  }, []);
  const register = useCallback((scope: string, handler: Handler) => {
    handlers.current.set(scope, handler);
    return () => {
      if (handlers.current.get(scope) === handler)
        handlers.current.delete(scope);
    };
  }, []);
  const finish = useCallback(() => {
    const value = current.current;
    gesture.current = null;
    set(null);
    if (!value) return;
    const handler = handlers.current.get(value.target.scope);
    if (!handler) return;
    setBusy(true);
    void Promise.resolve()
      .then(() => handler(value.item, value.target))
      .finally(() => setBusy(false))
      .catch((error) =>
        message.error(
          error instanceof Error ? error.message : "拖动未保存，请重试",
        ),
      );
  }, [set, message]);
  const activity = !!active || busy;
  useEffect(() => {
    onActivity(activity);
    return () => onActivity(false);
  }, [activity, onActivity]);
  useEffect(() => {
    let frame = 0;
    function hit(x: number, y: number): SeriesDrop | null {
      const node = document
        .elementFromPoint(x, y)
        ?.closest<HTMLElement>("[data-series-drop-scope]");
      if (!node) return null;
      const scope = node.dataset.seriesDropScope!;
      if (!handlers.current.has(scope)) return null;
      const row = node.dataset.seriesDropRow ?? "";
      const rect = node.getBoundingClientRect();
      return {
        scope,
        row,
        edge: row && y > rect.top + rect.height / 2 ? "after" : "before",
      };
    }
    function move(e: globalThis.PointerEvent) {
      const g = gesture.current;
      if (!g || g.id !== e.pointerId) return;
      if (!current.current && Math.hypot(e.clientX - g.x, e.clientY - g.y) < 6)
        return;
      const target = hit(e.clientX, e.clientY) ?? {
        scope: "",
        row: "",
        edge: "before" as const,
      };
      set({
        item: g.item,
        target,
        keyboard: false,
        x: e.clientX,
        y: e.clientY,
      });
      if (!frame) frame = requestAnimationFrame(scroll);
    }
    function cancel() {
      gesture.current = null;
      set(null);
    }
    function up(e: globalThis.PointerEvent) {
      if (gesture.current?.id === e.pointerId) finish();
    }
    function escape(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape" && current.current) {
        e.preventDefault();
        cancel();
      }
    }
    function scroll() {
      frame = 0;
      const a = current.current;
      if (!a || a.keyboard) return;
      if (a && !a.keyboard) {
        let el = document.elementFromPoint(a.x, a.y) as HTMLElement | null;
        while (el && el !== document.documentElement) {
          const rect = el.getBoundingClientRect();
          const style = getComputedStyle(el);
          if (
            el.scrollHeight > el.clientHeight &&
            /auto|scroll/.test(style.overflowY)
          ) {
            const delta =
              a.y < rect.top + 36 ? -12 : a.y > rect.bottom - 36 ? 12 : 0;
            if (delta) {
              el.scrollTop += delta;
              const target = hit(a.x, a.y);
              if (target) set({ ...a, target });
            }
            break;
          }
          el = el.parentElement;
        }
      }
      frame = requestAnimationFrame(scroll);
    }
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
    window.addEventListener("blur", cancel);
    window.addEventListener("keydown", escape);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      window.removeEventListener("blur", cancel);
      window.removeEventListener("keydown", escape);
    };
  }, [finish, set]);
  function pointer(e: PointerEvent, item: SeriesDragItem) {
    if (busy || e.button !== 0) return;
    e.preventDefault();
    if (e.currentTarget instanceof HTMLElement) e.currentTarget.focus();
    gesture.current = { item, x: e.clientX, y: e.clientY, id: e.pointerId };
    document.body.setPointerCapture?.(e.pointerId);
  }
  function key(e: KeyboardEvent, item: SeriesDragItem) {
    if (busy) return;
    const a = current.current;
    if (!a && [" ", "Enter"].includes(e.key)) {
      e.preventDefault();
      const rest = remaining(item);
      const index =
        item.kind === "recommendation"
          ? rest.length
          : Math.max(0, item.order.indexOf(item.id));
      set({
        item,
        target: { scope: item.scope, row: rest[index] ?? "", edge: "before" },
        keyboard: true,
        x: 0,
        y: 0,
      });
      return;
    }
    if (!a || !a.keyboard || a.item.id !== item.id) return;
    if ([" ", "Enter"].includes(e.key)) {
      e.preventDefault();
      finish();
      return;
    }
    if (["ArrowUp", "ArrowDown", "Home", "End"].includes(e.key)) {
      e.preventDefault();
      const rest = remaining(a.item);
      let index = a.target.row ? rest.indexOf(a.target.row) : rest.length;
      index =
        e.key === "Home"
          ? 0
          : e.key === "End"
            ? rest.length
            : Math.max(
                0,
                Math.min(rest.length, index + (e.key === "ArrowUp" ? -1 : 1)),
              );
      set({
        ...a,
        target: { scope: a.item.scope, row: rest[index] ?? "", edge: "before" },
      });
    }
  }
  return (
    <Context.Provider value={{ active, busy, register, pointer, key }}>
      {children}
      <span className="sr-only" role="status" aria-live="polite">
        {active
          ? `正在移动${active.item.label}，${active.target.row ? "放置到目标作品旁" : "放置到末尾"}；回车放下，Esc取消。`
          : busy
            ? "正在保存顺序"
            : ""}
      </span>
      {active && !active.keyboard && (
        <div
          className="series-drag-ghost"
          style={{
            left: Math.min(active.x + 12, window.innerWidth - 220),
            top: Math.max(0, active.y + 12),
          }}
        >
          {active.item.label}
        </div>
      )}
    </Context.Provider>
  );
}
export function SeriesDropZone({
  scope,
  onDrop,
  children,
}: {
  scope: string;
  onDrop: Handler;
  children: ReactNode;
}) {
  const { register, active } = useDrag(),
    handler = useRef(onDrop);
  useEffect(() => {
    handler.current = onDrop;
  }, [onDrop]);
  useEffect(
    () => register(scope, (item, drop) => handler.current(item, drop)),
    [register, scope],
  );
  return (
    <div
      className={`series-drop-zone ${active?.item.scope === scope ? "is-dragging" : ""}`}
      data-series-drop-scope={scope}
      data-series-drop-row=""
    >
      {children}
      <div
        className="series-drop-end"
        data-series-drop-scope={scope}
        data-series-drop-row=""
      >
        拖到此处追加到末尾
      </div>
    </div>
  );
}
export function SeriesDragRow({
  scope,
  id,
  children,
  className = "",
}: {
  scope: string;
  id: string;
  children: ReactNode;
  className?: string;
}) {
  const { active } = useDrag();
  const over =
    active?.target.scope === scope && active.target.row === id
      ? active.target.edge
      : undefined;
  return (
    <div
      className={`${className} ${active?.item.id === id ? "series-drag-source" : ""}`}
      data-series-drop-scope={scope}
      data-series-drop-row={id}
      data-series-drag-over={over}
    >
      {children}
    </div>
  );
}
export function SeriesDragHandle({
  item,
  disabled = false,
}: {
  item: SeriesDragItem;
  disabled?: boolean;
}) {
  const { pointer, key, busy, active } = useDrag();
  const lifted =
    active?.item.id === item.id && active.item.scope === item.scope;
  return (
    <Tooltip
      title="拖动排序；也可按空格提起、方向键移动、回车放下，Esc取消"
      trigger={["hover", "focus"]}
    >
      <Button
        size="small"
        className="series-drag-handle"
        disabled={(disabled && !lifted) || busy || (!!active && !lifted)}
        icon={<HolderOutlined />}
        aria-label={`拖动 ${item.label}`}
        onPointerDown={(e) => pointer(e, item)}
        onKeyDown={(e) => key(e, item)}
      />
    </Tooltip>
  );
}

export function useSeriesInteractionBusy() {
  const { active, busy } = useDrag();
  return !!active || busy;
}
