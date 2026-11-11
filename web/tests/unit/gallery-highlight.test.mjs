import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";
import {
  serviceOrigin,
  insecureRemote,
  oneTimeAuthorization,
  buildGalleryScript,
} from "../../src/shared/galleryHighlightFlow.ts";
import {
  togglePatch,
  resourceLabel,
} from "../../src/shared/enhancementFlow.ts";
const template = await readFile(
  new URL("../../userscripts/gallery-highlight.user.js", import.meta.url),
  "utf8",
);
const script = buildGalleryScript(template, "https://nas.example.test:8443"),
  token = "ngh1_" + "a".repeat(43);
test("service origin validation prevents credentials, paths, metadata and code injection", () => {
  assert.equal(
    serviceOrigin(" https://NAS.example.test:443/ "),
    "https://nas.example.test",
  );
  for (const value of [
    "ftp://host",
    "https://user:pass@host",
    "http://host/path",
    "http://host/?",
    "https://host/#",
    "https://host/\n// @connect *",
    "http://host/;alert(1)",
    "javascript:alert(1)",
    "http://host/\u0000",
  ])
    assert.throws(() => serviceOrigin(value));
  assert.equal(insecureRemote("http://nas.example.test"), true);
  for (const value of [
    "https://nas.example.test",
    "http://127.0.0.1:8080",
    "http://localhost",
    "http://[::1]",
  ])
    assert.equal(insecureRemote(value), false);
});
test("protocol2 authorization is a raw one-time code; script output contains only the fixed origin", () => {
  const result = {
    protocol_version: 2,
    connection: { id: "anonymous", expires_at: null },
    token,
  };
  assert.equal(oneTimeAuthorization(result), token);
  for (const value of [
    { ...result, protocol_version: 1 },
    { ...result, token: "wrong" },
    { ...result, connection: null },
    { ...result, connection: { id: "anonymous" } },
    { ...result, connection: { id: "anonymous", expires_at: 1 } },
  ])
    assert.throws(() => oneTimeAuthorization(value));
  assert.match(script, /\/\/ @connect\s+nas\.example\.test\n/u);
  assert.match(script, /\/\/ @sandbox\s+DOM\n/u);
  assert.match(script, /GM_info\.sandboxMode === "dom"/u);
  assert.equal(script.includes(token), false);
  assert.equal(script.includes("__NYAMANGA"), false);
  for (const forbidden of [
    /\/\/ @connect\s+\*/u,
    /\/\/ @require/u,
    /unsafeWindow/u,
    /\bfetch\(/u,
    /localStorage/u,
    /sessionStorage/u,
  ])
    assert.doesNotMatch(script, forbidden);
  assert.doesNotThrow(() => new vm.Script(script));
  assert.throws(() => buildGalleryScript("missing", "http://localhost"));
});
test("new feature is immediate without pause or transition payload; old services remain unsupported", () => {
  const f = {
    enabled: true,
    effective_enabled: true,
    state: "stable",
    resource_state: "ready",
    affected_tasks: 9,
    impact_token: "not-applicable",
    transition: null,
  };
  assert.deepEqual(
    togglePatch(
      { revision: 7, features: { local_gallery_highlight: f } },
      "local_gallery_highlight",
      false,
    ),
    {
      expected_revision: 7,
      feature: "local_gallery_highlight",
      enabled: false,
    },
  );
  assert.throws(
    () =>
      togglePatch(
        { revision: 7, features: {} },
        "local_gallery_highlight",
        true,
      ),
    /升级/u,
  );
  assert.equal(
    resourceLabel("ready", "local_gallery_highlight"),
    "无需额外资源",
  );
  assert.equal(resourceLabel("ready", "chinese_dictionary"), "译库可用");
});
test("raw, js, missing or non-Tampermonkey environments never read GM secrets or expose a paste menu", async () => {
  for (const sandboxMode of ["raw", "js", "MAIN", undefined]) {
    const menus = new Map(),
      alerts = [];
    let storage = 0,
      prompt = 0,
      http = 0;
    const context = {
      location: { href: "https://e-hentai.org/" },
      GM_info: { scriptHandler: "Tampermonkey", sandboxMode },
      GM: {
        registerMenuCommand: (name, fn) => menus.set(name, fn),
        getValue: () => {
          storage++;
        },
        setValue: () => {
          storage++;
        },
        deleteValue: () => {
          storage++;
        },
        xmlHttpRequest: () => {
          http++;
        },
      },
      alert: (s) => alerts.push(s),
      prompt: () => {
        prompt++;
        return null;
      },
      URL,
    };
    vm.runInNewContext(script, context);
    assert.deepEqual([...menus.keys()], ["连接状态"]);
    menus.get("连接状态")();
    assert.match(alerts[0], /Isolated Content Script/u);
    assert.equal(storage + prompt + http, 0);
  }
});
