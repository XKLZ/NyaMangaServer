import { useSyncExternalStore } from "react";
import {
  actorAccess,
  subscribeActor,
  canEditLibrary,
  canViewLibrary,
  isAdministrator,
} from "./actorAccess.mjs";
export function usePermissions() {
  const { actor, epoch, verified } = useSyncExternalStore(
    subscribeActor,
    actorAccess,
    actorAccess,
  );
  return {
    actor,
    verified,
    epoch,
    admin: isAdministrator(actor),
    edit: (id: string) => canEditLibrary(id, actor),
    view: (id: string) => canViewLibrary(id, actor),
    anyEditor:
      isAdministrator(actor) ||
      (!!actor?.enabled &&
        !actor.must_change_password &&
        Object.values(actor.library_grants || {}).includes("editor")),
  };
}
export function AdminOnly({ children }: { children: React.ReactNode }) {
  return usePermissions().admin ? <>{children}</> : null;
}
export function EditOnly({
  library,
  children,
}: {
  library: string;
  children: React.ReactNode;
}) {
  return usePermissions().edit(library) ? <>{children}</> : null;
}
