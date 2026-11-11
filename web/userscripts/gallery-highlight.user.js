// ==UserScript==
// @name         NyaManga 本地画廊高亮
// @namespace    nyamangaserver.gallery-highlight
// @version      2.0.0
// @description  Chrome/Edge + Tampermonkey；只查询本页画廊ID，不读取管理资料。
// @match        https://e-hentai.org/*
// @match        https://exhentai.org/*
// @connect      __NYAMANGA_CONNECT__
// @grant        GM.getValue
// @grant        GM.setValue
// @grant        GM.deleteValue
// @grant        GM.registerMenuCommand
// @grant        GM.xmlHttpRequest
// @grant        GM_info
// @sandbox      DOM
// @run-at       document-idle
// @noframes
// ==/UserScript==
/* global GM, GM_info, document, window, location, MutationObserver, setTimeout, clearTimeout, URL, alert, prompt */

(() => {
  "use strict";
  const SERVICE_ORIGIN = /*__NYAMANGA_ORIGIN__*/ "";
  const STORAGE = "nyamanga.gallery-highlight.v1",
    TTL = 60000,
    LIMIT = 1000;
  const STATES = { collected: "已收录", needs_review: "记录待检查" };
  const cache = new Map(),
    targets = new Map(),
    inflight = new Set(),
    requests = new Set();
  let config = null,
    generation = 1,
    cycle = 0,
    queue = [],
    pageKey = location.href,
    validated = false,
    expires = 0,
    stopped = false,
    storageBusy = false,
    scanTimer,
    refreshTimer,
    retryAt = 0,
    failures = 0,
    diagnostic = "未配置连接。",
    budget = { started: Date.now(), requests: 0, ids: 0 };
  const capable =
    typeof GM === "object" &&
    [
      "getValue",
      "setValue",
      "deleteValue",
      "registerMenuCommand",
      "xmlHttpRequest",
    ].every((key) => typeof GM[key] === "function") &&
    typeof GM_info === "object" &&
    GM_info.scriptHandler === "Tampermonkey" &&
    GM_info.sandboxMode === "dom";
  function origin(value) {
    if (
      typeof value !== "string" ||
      [...value].some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127)
    )
      throw Error("origin");
    const url = new URL(value);
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.pathname !== "/" ||
      /[?#]/u.test(value) ||
      !/^(?:[a-z0-9.-]+|\[[a-f0-9:]+\])$/iu.test(url.hostname)
    )
      throw Error("origin");
    return url.origin;
  }
  function configuration(value) {
    if (
      !value ||
      value.protocol_version !== 2 ||
      origin(value.service_url) !== SERVICE_ORIGIN ||
      value.service_url !== SERVICE_ORIGIN ||
      typeof value.token !== "string" ||
      !/^ngh1_[A-Za-z0-9_-]{43}$/u.test(value.token)
    )
      throw Error("config");
    return {
      protocol_version: 2,
      service_url: SERVICE_ORIGIN,
      token: value.token,
    };
  }
  function gid(value) {
    if (typeof value !== "string" || !/^\d{1,19}$/u.test(value)) return null;
    const clean = value.replace(/^0+/u, "");
    return clean && (clean.length < 19 || clean <= "9223372036854775807")
      ? clean
      : null;
  }
  function galleryID(href) {
    try {
      const url = new URL(href, location.href);
      if (
        url.protocol !== "https:" ||
        !["e-hentai.org", "exhentai.org"].includes(url.hostname) ||
        url.username ||
        url.password
      )
        return null;
      const match = /^\/g\/(\d{1,19})\/[A-Za-z0-9]+\/?$/u.exec(url.pathname);
      return match ? gid(match[1]) : null;
    } catch {
      return null;
    }
  }
  function unmark(element) {
    element.classList.remove("ngh-collected", "ngh-needs-review", "ngh-card");
    for (const node of element.querySelectorAll('[data-ngh-owned="badge"]'))
      node.remove();
  }
  function clearMarks() {
    for (const element of targets.keys()) unmark(element);
  }
  function render() {
    const now = Date.now();
    for (const [element, id] of targets) {
      if (!element.isConnected) {
        targets.delete(element);
        continue;
      }
      const value = cache.get(id),
        state = value && value.until > now ? value.state : null;
      const name =
        state === "collected"
          ? "ngh-collected"
          : state === "needs_review"
            ? "ngh-needs-review"
            : "";
      if (!name) {
        unmark(element);
        continue;
      }
      element.classList.add("ngh-card");
      element.classList.toggle("ngh-collected", state === "collected");
      element.classList.toggle("ngh-needs-review", state === "needs_review");
      let badge = element.querySelector('[data-ngh-owned="badge"]');
      if (!badge) {
        badge = document.createElement("span");
        badge.dataset.nghOwned = "badge";
        const parent =
          element.tagName === "TR"
            ? element.querySelector("td:has(.glink)") ||
              element.querySelector("td:last-child")
            : element;
        (parent || element).append(badge);
      }
      if (badge.textContent !== STATES[state])
        badge.textContent = STATES[state];
    }
  }
  function collect() {
    const next = new Map(),
      ambiguous = new Set();
    for (const link of document.querySelectorAll('a[href*="/g/"]')) {
      const id = galleryID(link.href);
      if (
        !id ||
        (!link.querySelector(".glink, img") &&
          !link.classList.contains("glink"))
      )
        continue;
      const row = link.closest("table.itg > tbody > tr, table.itg > tr"),
        element = row || link.closest(".itg .gl1t, .itg .gl1e");
      if (!element) continue;
      if (next.has(element) && next.get(element) !== id) ambiguous.add(element);
      next.set(element, id);
    }
    const detailID = galleryID(location.href),
      detail = document.querySelector("#gd2") || document.querySelector("#gd1");
    if (detailID && detail) next.set(detail, detailID);
    for (const element of ambiguous) next.delete(element);
    for (const [element] of targets)
      if (!next.has(element)) {
        unmark(element);
        targets.delete(element);
      }
    for (const [element, id] of next) {
      if (targets.get(element) !== id) unmark(element);
      targets.set(element, id);
    }
    render();
    const ids = [...new Set(targets.values())].filter(
      (id) =>
        !inflight.has(id) &&
        (!cache.has(id) || cache.get(id).until <= Date.now()),
    );
    ids.sort((a, b) => Number(cache.has(a)) - Number(cache.has(b)));
    queue = ids;
  }
  function stopRequests() {
    generation++;
    cycle = 0;
    clearTimeout(scanTimer);
    clearTimeout(refreshTimer);
    scanTimer = undefined;
    for (const ticket of [...requests]) ticket.cancel();
    requests.clear();
    inflight.clear();
    queue = [];
  }
  function reset() {
    stopRequests();
    clearMarks();
    cache.clear();
    validated = false;
    expires = 0;
  }
  function wait() {
    clearTimeout(refreshTimer);
    if (!config || stopped || document.hidden) return;
    const now = Date.now(),
      fresh = [...cache.values()]
        .filter((v) => v.until > now)
        .map((v) => v.until),
      deadline = Math.max(
        now + 50,
        retryAt > now ? retryAt : Math.min(now + TTL, ...fresh),
      );
    refreshTimer = setTimeout(scan, Math.min(60000, deadline - now));
  }
  function rateWindow(count) {
    const now = Date.now();
    if (now - budget.started >= TTL)
      budget = { started: now, requests: 0, ids: 0 };
    if (budget.requests >= 60 || budget.ids + count > LIMIT) {
      retryAt = Math.max(retryAt, budget.started + TTL);
      throw { kind: "rate", until: retryAt };
    }
    budget.requests++;
    budget.ids += count;
  }
  function retryAfter(headers) {
    const value = /^retry-after:\s*(.+)$/imu.exec(headers || "")?.[1]?.trim();
    let until =
      value && /^\d+$/u.test(value)
        ? Date.now() + Number(value) * 1000
        : Date.parse(value || "");
    if (!Number.isFinite(until) || until <= Date.now())
      until = Date.now() + TTL;
    return until;
  }
  function request(path, ids, own) {
    rateWindow(ids?.length || 0);
    const captured = config,
      requestPage = pageKey,
      url = SERVICE_ORIGIN + "/api/v1/gallery-highlight/" + path;
    return new Promise((resolve, reject) => {
      let settled = false,
        handle,
        timer;
      const ticket = {
        cancel: () => finish({ kind: "cancelled" }, undefined, true),
      };
      function finish(error, value, abort = false) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        requests.delete(ticket);
        if (abort) {
          try {
            handle?.abort();
          } catch {
            /* No fallback transport. */
          }
        }
        if (error) reject(error);
        else resolve(value);
      }
      requests.add(ticket);
      timer = setTimeout(
        () => finish({ kind: "timeout" }, undefined, true),
        10000,
      );
      try {
        handle = GM.xmlHttpRequest({
          method: ids ? "POST" : "GET",
          url,
          anonymous: true,
          redirect: "error",
          timeout: 10000,
          headers: {
            Authorization: `Bearer ${captured.token}`,
            ...(ids ? { "Content-Type": "application/json" } : {}),
          },
          ...(ids
            ? {
                data: JSON.stringify({ provider: "ehentai", gallery_ids: ids }),
              }
            : {}),
          responseType: "json",
          onload: (response) => {
            if (own !== generation || captured !== config) {
              finish({ kind: "cancelled" });
              return;
            }
            if (requestPage !== location.href) {
              finish({ kind: "cancelled" });
              schedule();
              return;
            }
            if (response.finalUrl && response.finalUrl !== url) {
              finish({ kind: "redirect" });
              return;
            }
            if ([401, 403].includes(response.status)) {
              finish({ kind: String(response.status) });
              return;
            }
            if (response.status === 429) {
              finish({
                kind: "rate",
                until: retryAfter(response.responseHeaders),
              });
              return;
            }
            if ([400, 404, 405, 413, 415].includes(response.status)) {
              finish({ kind: "protocol" });
              return;
            }
            if (response.status !== 200) {
              finish({ kind: "network" });
              return;
            }
            try {
              if (
                typeof response.responseText === "string" &&
                response.responseText.length > 131072
              )
                throw Error();
              const value =
                typeof response.response === "object" &&
                response.response !== null
                  ? response.response
                  : JSON.parse(response.responseText || "null");
              finish(null, value);
            } catch {
              finish({ kind: "protocol" });
            }
          },
          onerror: () => finish({ kind: "network" }),
          ontimeout: () => finish({ kind: "timeout" }, undefined, true),
          onabort: () => finish({ kind: "cancelled" }),
        });
        if (!handle || typeof handle.abort !== "function")
          finish({ kind: "unsupported" }, undefined, true);
        // Modern GM requests may also expose a Promise; consume its rejection.
        if (typeof handle?.catch === "function") handle.catch(() => {});
      } catch {
        finish({ kind: "unsupported" });
      }
    });
  }
  function validateStatus(value) {
    if (
      !value ||
      value.protocol_version !== 2 ||
      value.provider !== "ehentai" ||
      value.max_batch_size !== 200 ||
      !Object.hasOwn(value, "connection_expires_at") ||
      (value.connection_expires_at !== null &&
        (!Number.isSafeInteger(value.connection_expires_at) ||
          !Number.isSafeInteger(value.connection_expires_at * 1000) ||
          value.connection_expires_at <= Date.now() / 1000))
    )
      throw { kind: "protocol" };
    expires =
      value.connection_expires_at === null
        ? null
        : value.connection_expires_at * 1000;
  }
  function validateLookup(value, ids) {
    if (
      !value ||
      value.protocol_version !== 2 ||
      value.provider !== "ehentai" ||
      typeof value.query_version !== "string" ||
      !value.query_version ||
      value.query_version.length > 128 ||
      !Array.isArray(value.items) ||
      value.items.length !== ids.length
    )
      throw { kind: "protocol" };
    const expected = new Set(ids),
      seen = new Set();
    for (const item of value.items) {
      if (
        !item ||
        gid(item.gallery_id) !== item.gallery_id ||
        !expected.has(item.gallery_id) ||
        seen.has(item.gallery_id) ||
        !["collected", "needs_review", "none"].includes(item.state)
      )
        throw { kind: "protocol" };
      seen.add(item.gallery_id);
    }
    return value.items;
  }
  function failure(error, own) {
    if (own !== generation || error?.kind === "cancelled") return;
    if (
      ["401", "403", "unsupported", "redirect", "protocol"].includes(
        error?.kind,
      )
    ) {
      const label = {
        401: "授权无效或已到期，请重新连接。",
        403: "本地画廊高亮未开启；开启后请明确刷新本页。",
        unsupported:
          "当前GM接口不支持可中止的匿名请求，请使用兼容的Tampermonkey。",
        redirect: "服务发生重定向，已停止请求；请重新配置直达服务origin。",
        protocol: "协议响应不完整或不兼容，请更新脚本或服务；已停止自动请求。",
      };
      stopped = true;
      reset();
      diagnostic = label[error.kind];
    } else {
      retryAt = Math.max(
        retryAt,
        error?.kind === "rate"
          ? error.until
          : Date.now() + Math.min(60000, 15000 * 2 ** Math.min(failures++, 2)),
      );
      diagnostic =
        error?.kind === "rate"
          ? "请求受限，正在等待服务指定的重试时间。"
          : "连接暂不可用，结果未知；稍后重试。";
      render();
    }
  }
  async function run() {
    if (
      !config ||
      stopped ||
      document.hidden ||
      cycle === generation ||
      !queue.length ||
      retryAt > Date.now()
    ) {
      wait();
      return;
    }
    const own = generation;
    cycle = own;
    try {
      if (!validated) {
        validateStatus(await request("status", null, own));
        if (own !== generation) return;
        validated = true;
      }
      if (expires !== null && expires <= Date.now()) throw { kind: "401" };
      async function worker() {
        while (
          own === generation &&
          pageKey === location.href &&
          !document.hidden &&
          !stopped &&
          retryAt <= Date.now() &&
          queue.length
        ) {
          const ids = queue.splice(0, 200).filter((id) => !inflight.has(id));
          if (!ids.length) continue;
          ids.forEach((id) => inflight.add(id));
          try {
            const result = validateLookup(
              await request("lookup", ids, own),
              ids,
            );
            if (own !== generation) return;
            for (const item of result) {
              cache.delete(item.gallery_id);
              cache.set(item.gallery_id, {
                state: item.state,
                until:
                  expires === null
                    ? Date.now() + TTL
                    : Math.min(Date.now() + TTL, expires),
              });
            }
            while (cache.size > LIMIT) cache.delete(cache.keys().next().value);
            failures = 0;
            diagnostic = "本页查询成功；标记仅表示服务保存的正式来源记录。";
            render();
          } catch (error) {
            failure(error, own);
          } finally {
            if (own === generation) ids.forEach((id) => inflight.delete(id));
          }
        }
      }
      await Promise.all([worker(), worker()]);
    } catch (error) {
      failure(error, own);
    } finally {
      if (own === generation) {
        cycle = 0;
        wait();
      }
    }
  }
  function scan() {
    clearTimeout(scanTimer);
    scanTimer = undefined;
    clearTimeout(refreshTimer);
    if (document.hidden || !config || stopped) return;
    if (pageKey !== location.href) {
      reset();
      pageKey = location.href;
    }
    collect();
    void run();
  }
  function schedule() {
    if (document.hidden || !config || stopped) return;
    if (scanTimer !== undefined) return;
    scanTimer = setTimeout(scan, 200);
  }
  function manualRefresh() {
    if (!capable || !config) {
      diagnostic = capable
        ? "未配置连接。"
        : "缺少必要GM能力；首轮仅支持Chrome/Edge + Tampermonkey。";
      return;
    }
    reset();
    stopped = false;
    failures = 0;
    pageKey = location.href;
    schedule();
  }
  async function importConfig() {
    if (!capable || storageBusy) return;
    const address = prompt(
      "输入完整NyaMangaServer地址（含协议和端口）。必须与下载脚本时的地址一致。",
      config?.service_url || SERVICE_ORIGIN,
    );
    if (address === null) return;
    let server;
    try {
      server = origin(address.trim());
      if (server !== SERVICE_ORIGIN) throw Error();
    } catch {
      alert(
        "地址无效或与脚本固定origin不一致。请回设置重新下载正确脚本；原配置保持。",
      );
      return;
    }
    const code = prompt("输入从NyaMangaServer复制的授权码。", "");
    if (code === null) return;
    let next;
    try {
      next = configuration({
        protocol_version: 2,
        service_url: server,
        token: code.trim(),
      });
    } catch {
      alert("授权码格式无效，原配置和标记保持。");
      return;
    }
    storageBusy = true;
    try {
      await GM.setValue(STORAGE, next);
      reset();
      config = next;
      stopped = false;
      retryAt = 0;
      budget = { started: Date.now(), requests: 0, ids: 0 };
      diagnostic = "配置已保存，正在查询本页。";
      schedule();
    } catch {
      alert("GM配置保存失败，原配置保持。");
    } finally {
      storageBusy = false;
    }
  }
  async function clear() {
    if (!capable || storageBusy) return;
    reset();
    config = null;
    stopped = false;
    diagnostic = "本页连接与标记已清除。";
    storageBusy = true;
    try {
      await GM.deleteValue(STORAGE);
    } catch {
      diagnostic = "本页已停用，但GM存储清除失败，请重试清除。";
    } finally {
      storageBusy = false;
    }
  }
  if (typeof GM !== "object" || typeof GM.registerMenuCommand !== "function") {
    alert("缺少GM菜单能力，请使用Tampermonkey安装此脚本。");
    return;
  }
  GM.registerMenuCommand("连接状态", () => alert(diagnostic));
  if (!capable) {
    diagnostic =
      "当前环境缺少隔离的DOM沙箱或必要GM能力。请在Tampermonkey启用Isolated Content Script上下文后重载；不支持raw/MAIN或未知模式。";
    return;
  }
  GM.registerMenuCommand("配置服务器", () => void importConfig());
  GM.registerMenuCommand("刷新本页", manualRefresh);
  GM.registerMenuCommand("清除配置与标记", () => void clear());
  try {
    if (origin(SERVICE_ORIGIN) !== SERVICE_ORIGIN) throw Error();
  } catch {
    diagnostic = "脚本尚未绑定有效服务origin，请从管理台重新下载。";
    return;
  }
  const style = document.createElement("style");
  style.dataset.nghOwned = "style";
  style.textContent = `.ngh-collected:not(:focus-within){outline:1px solid #3f8a56;outline-offset:1px}
.ngh-needs-review:not(:focus-within){outline:1px solid #a56b16;outline-offset:1px}
[data-ngh-owned="badge"]{display:inline-block;margin:4px 6px;padding:1px 5px;border:1px solid currentColor;border-radius:3px;font:500 12px/1.5 system-ui,sans-serif;color:#205c32;background:#e8f4ec;vertical-align:middle}
.ngh-needs-review [data-ngh-owned="badge"]{color:#754708;background:#fff3dc}
@media(prefers-color-scheme:dark){[data-ngh-owned="badge"]{color:#b3e5c2;background:#183824}.ngh-needs-review [data-ngh-owned="badge"]{color:#f3d397;background:#3e2f18}}`;
  document.head.append(style);
  const observer = new MutationObserver((mutations) => {
    const external = mutations.some((m) => {
      const element =
        m.target.nodeType === 1 ? m.target : m.target.parentElement;
      if (element?.closest("[data-ngh-owned]")) return false;
      const changed = [...m.addedNodes, ...m.removedNodes];
      return (
        m.type === "attributes" ||
        !changed.length ||
        changed.some(
          (n) => n.nodeType !== 1 || !n.hasAttribute("data-ngh-owned"),
        )
      );
    });
    if (external) schedule();
  });
  observer.observe(document.body, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["href"],
  });
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) stopRequests();
    else schedule();
  });
  window.addEventListener("pagehide", stopRequests);
  window.addEventListener("pageshow", schedule);
  window.addEventListener("popstate", schedule);
  void (async () => {
    const reading = generation;
    try {
      const stored = await GM.getValue(STORAGE, null);
      if (reading !== generation || storageBusy) return;
      if (stored?.protocol_version === 1) {
        diagnostic =
          "检测到旧版配置，请通过“配置服务器”重新配置协议2；旧配置未清除，尚未查询。";
        return;
      }
      if (stored) {
        config = configuration(stored);
        diagnostic = "已读取GM配置，正在查询本页。";
        schedule();
      }
    } catch {
      if (reading !== generation || storageBusy) return;
      diagnostic =
        "GM配置读取失败、协议不符或origin不一致，请更新脚本或服务并通过“配置服务器”重新配置。";
    }
  })();
})();
