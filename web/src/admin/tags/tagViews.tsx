import { Button, Tag } from "antd";
import { ErrorNotice } from "../common";
import { relationLabel } from "./tagCatalogFlow";
export function TagReadError({
  error,
  restart,
}: {
  error: unknown;
  restart: () => void;
}) {
  return error ? (
    <div className="tag-read-error">
      <ErrorNotice error={error} />
      <Button onClick={restart}>重新载入此列表</Button>
    </div>
  ) : null;
}
export function RelationState({
  state,
  label,
}: {
  state: string;
  label?: string;
}) {
  const color =
    state === "confirmed"
      ? "blue"
      : [
            "excluded",
            "suppressed",
            "competing_parodies",
            "known_elsewhere",
          ].includes(state)
        ? "warning"
        : undefined;
  return <Tag color={color}>{relationLabel(state, label)}</Tag>;
}
