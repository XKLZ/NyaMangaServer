let snapshot = {
  supported: null,
  state: null,
  error: false,
  instanceSupported: false,
  instanceState: null,
  instanceError: false,
};
const listeners = new Set(),
  jobs = new Set();
export const healthAccess = () => snapshot;
export const subscribeHealth = (fn) => {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
};
export function updateHealth(value) {
  snapshot = {
    ...snapshot,
    ...value,
  };
  listeners.forEach((fn) => fn());
}
export function resetHealth() {
  jobs.clear();
  updateHealth({
    supported: null,
    state: null,
    error: false,
    instanceSupported: false,
    instanceState: null,
    instanceError: false,
  });
}
export function registerHealthJob(id) {
  if (id) jobs.add(id);
}
export const maintenanceProtected = () =>
  snapshot.supported === true &&
  (snapshot.error || !snapshot.state || snapshot.state.phase !== "idle");
export const readOnlyPost = (path, method) =>
  method === "POST" &&
  [
    "/books/search",
    "/editions/search",
    "/admin/books/search",
    "/admin/editions/search",
    "/tag-suggestions",
  ].includes(path);
export function healthControl(path, method) {
  if (method !== "POST") return false;
  if (
    [
      "/maintenance/health-checks",
      "/maintenance/health-repair-plans",
      "/maintenance/health-state/recover",
      "/maintenance/backups",
      "/maintenance/restores",
      "/maintenance/instance-state/release",
    ].includes(path)
  )
    return true;
  if (
    /^\/maintenance\/health-repair-plans\/[^/?#]+\/execute$/.test(path) ||
    /^\/maintenance\/health-repairs\/[^/?#]+\/rollback$/.test(path)
  )
    return true;
  if (
    /^\/maintenance\/backups\/[^/?#]+\/cancel$/.test(path) ||
    /^\/maintenance\/restores\/[^/?#]+\/(validate|preview|confirm|abandon|rollback)$/.test(
      path,
    )
  )
    return true;
  const job = /^\/jobs\/([^/?#]+)\/(pause|cancel|resume)$/.exec(path);
  return !!job && jobs.has(job[1]);
}
export const healthWriteAllowed = (path, method) =>
  !maintenanceProtected() ||
  ["GET", "HEAD"].includes(method) ||
  (["/auth/login", "/auth/setup", "/auth/logout"].includes(path) &&
    method === "POST") ||
  readOnlyPost(path, method) ||
  healthControl(path, method);
