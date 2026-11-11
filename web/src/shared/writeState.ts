export { healthWriteAllowed, updateHealth } from "./healthAccess.mjs";
import { readOnlyPost } from "./healthAccess.mjs";
let activeWrites = 0;
const listeners = new Set<() => void>();
export const writesPending = () => activeWrites > 0;
export function subscribeWrites(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
export function isBusinessWrite(path: string, method: string) {
  return !["GET", "HEAD"].includes(method) && !readOnlyPost(path, method);
}
export async function trackWrite<T>(
  work: () => Promise<T>,
  writing = true,
): Promise<T> {
  if (writing) {
    activeWrites++;
    listeners.forEach((notify) => notify());
  }
  try {
    return await work();
  } finally {
    if (writing) {
      activeWrites--;
      listeners.forEach((notify) => notify());
    }
  }
}

export {
  actorAccess,
  actorEpoch,
  registerActorAbort,
  setActor,
  legacyActor,
  clientAccess,
  rememberResources,
} from "./actorAccess.mjs";
