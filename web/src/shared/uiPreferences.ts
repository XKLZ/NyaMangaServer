export const navigationPreferenceKey = "nyamanga.ui.navigation-collapsed.v1";
export function readNavigationCollapsed(
  storage: Pick<Storage, "getItem">,
): boolean {
  try {
    return storage.getItem(navigationPreferenceKey) !== "false";
  } catch {
    return true;
  }
}
export function writeNavigationCollapsed(
  storage: Pick<Storage, "setItem">,
  collapsed: boolean,
) {
  try {
    storage.setItem(navigationPreferenceKey, String(collapsed));
  } catch {
    /* UI stays usable when local storage is unavailable. */
  }
}
