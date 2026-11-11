export type Actor = {
  id: string;
  username: string;
  role: "admin" | "user";
  enabled: boolean;
  must_change_password: boolean;
  acl_revision: number;
  library_grants: Record<string, "viewer" | "editor">;
  capabilities: {
    manage_accounts: boolean;
    global_maintenance: boolean;
    manage_global_tags: boolean;
    personal_state: boolean;
  };
};
export function actorAccess(): {
  actor: Actor | null;
  ready: boolean;
  verified: boolean;
  epoch: number;
};
export function subscribeActor(fn: () => void): () => void;
export function actorEpoch(): number;
export function setActor(actor: Actor | null): void;
export function legacyActor(id: string): Actor;
export function registerActorAbort(abort: () => void): () => void;
export function isAdministrator(actor?: Actor | null): boolean;
export function canViewLibrary(id: string, actor?: Actor | null): boolean;
export function canEditLibrary(id: string, actor?: Actor | null): boolean;
export function rememberResources(data: unknown): void;
export function clientAccess(
  path: string,
  method: string,
  body?: unknown,
): boolean;

export function setActorVerified(verified: boolean): void;
