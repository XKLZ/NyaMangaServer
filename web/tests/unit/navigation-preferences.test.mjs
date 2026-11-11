import assert from "node:assert/strict";
import test from "node:test";
import {
  navigationPreferenceKey,
  readNavigationCollapsed,
  writeNavigationCollapsed,
} from "../../src/shared/uiPreferences.ts";
test("navigation defaults collapsed and persists an explicit preference", () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  };
  assert.equal(readNavigationCollapsed(storage), true);
  writeNavigationCollapsed(storage, false);
  assert.equal(values.get(navigationPreferenceKey), "false");
  assert.equal(readNavigationCollapsed(storage), false);
  writeNavigationCollapsed(storage, true);
  assert.equal(readNavigationCollapsed(storage), true);
});
test("unavailable local storage does not block navigation", () => {
  const storage = {
    getItem: () => {
      throw new Error("denied");
    },
    setItem: () => {
      throw new Error("denied");
    },
  };
  assert.equal(readNavigationCollapsed(storage), true);
  assert.doesNotThrow(() => writeNavigationCollapsed(storage, false));
});
