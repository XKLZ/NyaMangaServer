import type { InstancePreview, RootMapping } from "../../shared/instanceFlow";
export type RestoreDraft = {
  revision: number;
  mappings: RootMapping[];
  acknowledge: boolean;
  preview: InstancePreview | null;
  blocked: boolean;
  dirty: boolean;
};
export type PendingInstance = { kind: "backups" | "restores"; key: string };
// Only operation identities, configured root keys and frozen revisions; no file data or credentials.
export function readInstanceMemory<T>(key: string, fallback: T): T {
  try {
    return (
      JSON.parse(sessionStorage.getItem("instance-ui-" + key) || "null") ??
      fallback
    );
  } catch {
    return fallback;
  }
}
export function writeInstanceMemory(key: string, value: unknown) {
  try {
    if (value === null) sessionStorage.removeItem("instance-ui-" + key);
    else sessionStorage.setItem("instance-ui-" + key, JSON.stringify(value));
  } catch {
    /* In-memory drafts still work if storage is unavailable. */
  }
}
