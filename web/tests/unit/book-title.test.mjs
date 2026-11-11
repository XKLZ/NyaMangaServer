import assert from "node:assert/strict";
import test from "node:test";
import {
  cleanBookTitle,
  copyBookTitle,
} from "../../src/admin/books/bookTitle.ts";

test("removes every supported pair and nested annotations", () => {
  for (const [open, close] of [
    ["(", ")"],
    ["（", "）"],
    ["[", "]"],
    ["［", "］"],
    ["【", "】"],
    ["{", "}"],
    ["｛", "｝"],
    ["「", "」"],
    ["『", "』"],
    ["〈", "〉"],
    ["《", "》"],
    ["〔", "〕"],
  ]) {
    assert.equal(cleanBookTitle(`${open}annotation${close}Title`), "Title");
  }
  assert.equal(
    cleanBookTitle("(event [circle（name）]) 真正的标题 【译注】"),
    "真正的标题",
  );
  assert.equal(cleanBookTitle("A[one]B(two)C"), "ABC");
});

test("preserves unmatched text and falls back when no title remains", () => {
  assert.equal(cleanBookTitle("标题 (未完成"), "标题 (未完成");
  assert.equal(cleanBookTitle("标题 ] 尾部"), "标题 ] 尾部");
  assert.equal(
    cleanBookTitle("标题 (未配对 [可移除] 尾部"),
    "标题 (未配对 尾部",
  );
  assert.equal(cleanBookTitle("[全部注释]"), "[全部注释]");
  assert.equal(cleanBookTitle(""), "");
});

test("normalizes whitespace without mutating the original", () => {
  const original = "  标题\n [注释] \t 第二部分  ";
  assert.equal(cleanBookTitle(original), "标题 第二部分");
  assert.equal(original, "  标题\n [注释] \t 第二部分  ");
});

test("copies the full cleaned title and reports success only after completion", async () => {
  let copied;
  let complete;
  const pending = new Promise((resolve) => {
    complete = resolve;
  });
  let reported = false;
  const result = copyBookTitle("[circle] Full Title", {
    writeText: async (text) => {
      copied = text;
      await pending;
    },
  }).then((feedback) => {
    reported = true;
    return feedback;
  });
  await Promise.resolve();
  assert.equal(reported, false);
  assert.equal(copied, "Full Title");
  complete();
  assert.deepEqual(await result, { type: "success", content: "标题已复制" });
});

test("clipboard denial or missing support reports failure", async () => {
  for (const clipboard of [
    undefined,
    {
      writeText: async () => {
        throw new Error("denied");
      },
    },
  ]) {
    const result = await copyBookTitle("Title", clipboard);
    assert.equal(result.type, "error");
    assert.match(result.content, /复制失败/);
  }
});

test("workspace display prefers effective original titles without changing Work names", async () => {
  const { originalTitle } = await import("../../src/admin/books/bookTitle.ts");
  const work = {
    name: "Manual Work name",
    title: "Translation",
    title_original: " [Circle] 原文 ",
  };
  assert.equal(originalTitle(work), "[Circle] 原文");
  assert.equal(cleanBookTitle(originalTitle(work)), "原文");
  assert.equal(work.name, "Manual Work name");
  assert.equal(
    originalTitle({ ...work, title_original: "  " }),
    "Manual Work name",
  );
  assert.equal(originalTitle({ name: "Legacy name" }), "Legacy name");
});

test("original title fallback respects explicit empty live values and frozen summaries", async () => {
  const { originalTitle } = await import("../../src/admin/books/bookTitle.ts");
  const live = {
    title: "其他标题",
    metadata: { fields: { title_original: "日本語" } },
  };
  assert.equal(originalTitle(live), "日本語");
  assert.equal(originalTitle({ ...live, title_original: "" }), "其他标题");
  assert.equal(originalTitle({ ...live, title_original: null }), "其他标题");
  assert.equal(originalTitle({ ...live, title_original: undefined }), "日本語");
  assert.equal(
    originalTitle({ name_override: "人工名", name: "继承名" }),
    "人工名",
  );
  assert.equal(
    originalTitle({ title: "  ", relative_path: "folder/旧记录.cbz" }),
    "旧记录.cbz",
  );
  assert.equal(originalTitle({ filename: "folder\\file.cbz" }), "file.cbz");
  assert.equal(originalTitle({}), "未命名作品");
  const frozen = Object.freeze({ title: "旧任务已保存标题" });
  assert.equal(originalTitle(frozen), "旧任务已保存标题");
  assert.deepEqual(frozen, { title: "旧任务已保存标题" });
});
