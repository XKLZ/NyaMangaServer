import { resetHealth, readOnlyPost } from "./healthAccess.mjs";
let value = { actor: null, ready: false, verified: true, epoch: 0 };
const listeners = new Set(),
  aborters = new Set(),
  resources = new Map();
export const actorAccess = () => value;
export const subscribeActor = (fn) => {
  listeners.add(fn);
  return () => listeners.delete(fn);
};
export const actorEpoch = () => value.epoch;
export function registerActorAbort(abort) {
  aborters.add(abort);
  return () => aborters.delete(abort);
}
export function setActor(actor) {
  const before = value.actor;
  const identity = (a) =>
    a
      ? JSON.stringify([
          a.id,
          a.acl_revision,
          a.role,
          a.enabled,
          a.must_change_password,
          a.library_grants,
        ])
      : "none";
  const changed = identity(before) !== identity(actor);
  if (changed) {
    resetHealth();
    for (const abort of aborters) abort();
    aborters.clear();
    resources.clear();
    if (before && typeof window !== "undefined") {
      // Transient private drafts and caches never pass to another login or ACL generation.
      try {
        const storage = window.sessionStorage;
        for (const key of Object.keys(storage))
          if (key.startsWith("instance-ui-") || key.startsWith("tag-"))
            storage.removeItem(key);
      } catch {
        /* Storage may be unavailable. */
      }
      window.dispatchEvent(new Event("actor-invalidated"));
    }
  }
  value = {
    actor,
    ready: true,
    verified: true,
    epoch: value.epoch + (changed ? 1 : 0),
  };
  listeners.forEach((fn) => fn());
}
export function setActorVerified(verified) {
  value = { ...value, verified };
  listeners.forEach((fn) => fn());
}
export function legacyActor(id) {
  return {
    id,
    username: "admin",
    role: "admin",
    enabled: true,
    must_change_password: false,
    acl_revision: 0,
    library_grants: {},
    capabilities: {
      manage_accounts: false,
      global_maintenance: true,
      manage_global_tags: true,
      personal_state: true,
    },
  };
}
export const isAdministrator = (actor = value.actor) =>
  !!actor?.enabled && !actor.must_change_password && actor.role === "admin";
export function canViewLibrary(id, actor = value.actor) {
  return (
    isAdministrator(actor) ||
    (!!id &&
      !!actor?.enabled &&
      !actor.must_change_password &&
      ["viewer", "editor"].includes(actor.library_grants?.[id]))
  );
}
export function canEditLibrary(id, actor = value.actor) {
  return (
    isAdministrator(actor) ||
    (!!id &&
      !!actor?.enabled &&
      !actor.must_change_password &&
      actor.library_grants?.[id] === "editor")
  );
}
export function rememberResources(data) {
  if (!data || typeof data !== "object") return;
  const visit = (v, depth) => {
    if (!v || typeof v !== "object" || depth > 10) return;
    if (
      typeof v.id === "string" &&
      typeof v.library_id === "string" &&
      v.library_id
    )
      resources.set(v.id, v.library_id);
    for (const x of Object.values(v))
      if (typeof x === "object") visit(x, depth + 1);
  };
  visit(data, 0);
}
export function clientAccess(path, method, body) {
  const actor = value.actor;
  if (path.startsWith("/auth/"))
    return (
      !actor?.must_change_password ||
      ["/auth/session", "/auth/password", "/auth/logout"].includes(path)
    );
  if (!value.ready) return true;
  if (!actor || !actor.enabled || actor.must_change_password) return false;
  if (isAdministrator(actor))
    return (
      value.verified ||
      ["GET", "HEAD"].includes(method) ||
      readOnlyPost(path.split("?")[0], method)
    );
  const route = path.split("?")[0];
  if (
    /^\/(accounts(?:\/|$)|library-roots$|diagnostics$|maintenance(?:\/|$))/.test(
      route,
    ) ||
    (/^\/settings\//.test(route) && route !== "/settings/work-recommendations")
  )
    return false;
  if (["GET", "HEAD"].includes(method) || readOnlyPost(route, method))
    return true;
  if (!value.verified) return false;
  if (
    /^\/(?:books|editions)\/[^/]+\/(?:user-state|reading-state|reading-sessions)$/.test(
      route,
    ) ||
    /^\/works\/[^/]+\/favorite$/.test(route) ||
    /^\/tags\/[^/]+\/follow$/.test(route) ||
    /^\/saved-searches(?:\/|$)/.test(route)
  )
    return true;
  if (
    /^\/tags\/[^/]+\/translations\//.test(route) ||
    /^\/tag-catalog\/[^/]+\/images/.test(route) ||
    /^\/(?:parody-character-relations|version-groups)(?:\/|$)/.test(route) ||
    /\/character-suppressions\//.test(route) ||
    route === "/libraries" ||
    /^\/libraries\/[^/]+(?:\/status)?$/.test(route)
  )
    return false;
  const ids = [];
  const match =
    /^\/(?:books|editions|works|series|jobs|import-batches|work-proposals|series-proposals|work-recommendations)\/([^/]+)/.exec(
      route,
    );
  if (match) ids.push(decodeURIComponent(match[1]));
  if (body && typeof body === "object") {
    if (typeof body.library_id === "string")
      return canEditLibrary(body.library_id, actor);
    for (const key of ["book_ids", "edition_ids", "work_ids"])
      if (Array.isArray(body[key])) ids.push(...body[key]);
  }
  const libraries = new Set();
  for (const id of ids) {
    if (resources.has(id)) {
      const library = resources.get(id);
      if (!canEditLibrary(library, actor)) return false;
      libraries.add(library);
    }
  }
  if (libraries.size > 1) return false;
  // Unknown object ownership is resolved and enforced by the server, never inferred as all-library authority.
  return Object.values(actor.library_grants || {}).includes("editor");
}
