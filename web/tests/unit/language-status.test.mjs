import assert from "node:assert/strict";
import test from "node:test";
import { savePayload } from "../../src/admin/metadataForm.ts";
import { validateProperties } from "../../src/shared/propertyFilters.ts";
test("language edits and filters reject normalized status words without deleting input", () => {
  for (const name of ["translated", " REWRITE ", "ＴＲＡＮＳＬＡＴＥＤ"]) {
    const value = ["english", name];
    const state = {
      base: { id: "anonymous", revision: 1, metadata: { properties: {} } },
      draft: { languages: { op: "set", value } },
      pending: {},
    };
    assert.throws(() => savePayload(state), /状态/);
    assert.throws(
      () => validateProperties({ languages: { any_of: value } }),
      /状态/,
    );
    assert.deepEqual(state.draft.languages.value, ["english", name]);
    assert.deepEqual(value, ["english", name]);
  }
  assert.deepEqual(
    validateProperties({ languages: { any_of: ["english", "japanese"] } }),
    { languages: { any_of: ["english", "japanese"] } },
  );
});
