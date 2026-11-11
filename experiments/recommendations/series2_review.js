"use strict";
const D = JSON.parse(document.getElementById("payload").textContent),
  R = D.report;
const $ = (id) => document.getElementById(id),
  el = (tag, text, cls) => {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = String(text);
    if (cls) n.className = cls;
    return n;
  };
const names = {
  complete_legacy: "完全连接＋旧主体",
  average_legacy: "平均连接＋旧主体",
  complete_profile: "完全连接＋主体画像",
  average_profile: "平均连接＋主体画像",
};
const risks = {
  explicit_creator_conflict: "创作者资料明确不一致",
  creator_support_missing: "缺少共同创作者资料",
  semantic_feature_missing: "缺少语义特征",
  ordinal_type_ambiguity: "卷／话序号类型不一致",
  same_position_needs_work_or_part_review:
    "相同序号，请核对是否为版本或不同篇章",
  distinct_work_relationship_needs_review: "尚不能明确区分为不同作品",
  different_title_subject: "题名主体冲突",
};
const pct = (v) =>
  v === null || v === undefined ? "—" : (100 * v).toFixed(1) + "%";
function work(id) {
  const box = el("section", undefined, "work"),
    w = D.works[id];
  if (!w) {
    box.append(el("p", "资料未提供"));
    return box;
  }
  for (const [index, i] of w.edition_ids.entries()) {
    const b = D.books[String(i)];
    if (!b) continue;
    const part = el("div");
    if (b.image) {
      const image = el("img");
      image.src = b.image;
      image.alt = "版本封面";
      image.loading = "lazy";
      part.append(image);
    } else part.append(el("p", "封面缺失", "muted"));
    part.append(el("p", b.title_original || b.title, "title"));
    if (b.title_original && b.title_original !== b.title)
      part.append(el("p", b.title, "muted"));
    part.append(
      el(
        "p",
        (b.language || "语言未知") + " · " + (b.pages ?? "未知") + " 页",
        "muted",
      ),
    );
    if (w.edition_ids.length > 1)
      part.append(el("span", "同一作品 · 版本 " + (index + 1), "chip"));
    const tags = el("details"),
      heading = el("summary", "标签");
    tags.append(
      heading,
      el("p", (b.tags || []).map((t) => t.join(":")).join(" · ")),
    );
    part.append(tags);
    box.append(part);
  }
  return box;
}
function members(ids) {
  const wrapper = el("div"),
    grid = el("div", undefined, "members"),
    tools = el("div", undefined, "member-tools"),
    prev = el("button", "上一批成员"),
    next = el("button", "下一批成员"),
    label = el("span", undefined, "muted");
  let offset = 0;
  function draw() {
    grid.replaceChildren();
    for (const i of ids.slice(offset, offset + 12)) grid.append(work(i));
    label.textContent = `成员 ${offset + 1}–${Math.min(ids.length, offset + 12)} / ${ids.length}`;
    prev.disabled = offset === 0;
    next.disabled = offset + 12 >= ids.length;
  }
  prev.onclick = () => {
    offset -= 12;
    draw();
  };
  next.onclick = () => {
    offset += 12;
    draw();
  };
  if (ids.length > 12) tools.append(prev, next, label);
  else tools.append(el("p", ids.length + " 个作品", "muted"));
  wrapper.append(tools, grid);
  draw();
  return wrapper;
}
function notes(plan) {
  const box = el("div");
  if ((plan.known_negative_pairs || []).length)
    box.append(el("p", "含已标注为不相关的关系，请重点核对。", "risk"));
  const list = [
    ...new Set(
      (plan.risks || []).map((r) => risks[r] || "资料存在歧义，请核对"),
    ),
  ];
  if (list.length) box.append(el("p", list.join("；"), "risk"));
  box.append(
    el(
      "p",
      plan.order?.suggested_order
        ? "有明确序号顺序建议，请核对。"
        : "顺序尚不能可靠判断，需要人工确认。",
      "muted",
    ),
  );
  return box;
}
function planCard(p) {
  const card = el("article");
  card.append(
    el("h2", p.suggested_name || "待确认系列名称"),
    el("span", p.tier === "strong" ? "较强建议 · 仍需核对" : "需核对", "chip"),
    notes(p),
    members(p.order?.suggested_order || p.work_ids),
  );
  const details = el("details");
  details.append(el("summary", "题名依据与范围"));
  if (p.profile) {
    const rep = p.profile.representative;
    details.append(
      el("p", "以组内代表作品的题名检查各成员，未沿相似链扩展。", "muted"),
    );
    if (rep) details.append(work(rep));
  }
  details.append(
    el("p", "未经完整整组标注；未知成员关系不计为正确。", "muted"),
  );
  card.append(details);
  return card;
}
function pendingCard(p) {
  const card = el("article");
  card.append(
    el("h2", "待核对的作品"),
    el(
      "p",
      p.reason === "multiple_series_targets_similarly_supported"
        ? "多个去向的支持程度接近，暂未放入主方案。"
        : "移除歧义成员后，原组需要重新确认。",
      "risk",
    ),
    work(p.work_id),
  );
  if (!p.options.length)
    card.append(el("p", "重验后暂时没有可用去向；不强行归组。", "muted"));
  for (const [n, o] of p.options.entries()) {
    const section = el("section", undefined, "panel");
    section.append(
      el(
        "h3",
        "备选 " +
          (n + 1) +
          " · " +
          (o.kind === "join_series" ? "加入已有系列" : "与以下作品组成系列"),
      ),
      el("p", "仅为待确认去向，不与其他备选同时成立。", "muted"),
      notes(o),
      members(o.target_work_ids),
    );
    card.append(section);
  }
  if (p.qualified_targets > p.options.length)
    card.append(el("p", "另有其他合格去向，当前只展示最优的三个。", "muted"));
  return card;
}
function comparison() {
  const box = el("section", undefined, "panel"),
    wrap = el("div", undefined, "table-wrap"),
    table = el("table"),
    head = el("tr");
  for (const h of [
    "方法",
    "主方案",
    "已知正例：移出前→后",
    "已知误配：移出前→后",
    "待核对作品",
    "备选",
    "重复归属",
    "OOF P / R",
  ])
    head.append(el("th", h));
  table.append(head);
  for (const [key, v] of Object.entries(R.fullfit)) {
    const a = v.audit,
      b = v.before_boundary_audit,
      m = R.oof[key].metrics.combined,
      row = el("tr");
    for (const x of [
      names[key],
      a.primary_groups,
      `${b.pair_metrics.combined.tp} → ${a.pair_metrics.combined.tp} / ${a.pair_metrics.combined.tp + a.pair_metrics.combined.fn}`,
      `${b.pair_metrics.combined.fp} → ${a.pair_metrics.combined.fp}`,
      a.pending_works,
      a.alternative_count,
      a.duplicate_memberships,
      pct(m.precision) + " / " + pct(m.recall),
    ])
      row.append(el("td", x));
    table.append(row);
  }
  wrap.append(table);
  box.append(
    el("h2", "成组对照"),
    el("p", "完整数据策略与五折结果分开；配对精确率不是整组正确率。", "risk"),
    wrap,
    el(
      "p",
      `V1：${R.prior_v1.raw_proposals} 个重叠方案，完整方案覆盖 ${R.prior_v1.fullfit_group_pair_metrics.tp} / ${R.prior_v1.fullfit_group_pair_metrics.tp + R.prior_v1.fullfit_group_pair_metrics.fn} 个已知系列正例。`,
    ),
    el(
      "p",
      "聚类评分、主体规则和歧义移出都可能造成拆组；方案变少本身不算改进。",
      "muted",
    ),
  );
  return box;
}
let pageIndex = 0;
function current() {
  return R.fullfit[$("variant").value];
}
function rows() {
  const mode = $("mode").value,
    v = current();
  if (mode === "primary") return v.proposals;
  if (mode === "pending") return v.pending;
  if (mode === "join") return R.virtual_series;
  if (mode === "comparison") return [null];
  return D.rows.filter((r) => {
    if (
      r.mapping_conflict ||
      r.collection_or_partial_overlap ||
      r.label === "uncertain"
    )
      return false;
    const yes = v.audit.predictions[r.pair_id] !== "none";
    return r.label === "same_series" ? !yes : yes;
  });
}
function draw() {
  const list = rows(),
    mode = $("mode").value,
    content = $("content");
  content.replaceChildren();
  for (const item of list.slice(pageIndex * 8, pageIndex * 8 + 8)) {
    if (mode === "primary") content.append(planCard(item));
    else if (mode === "pending") content.append(pendingCard(item));
    else if (mode === "comparison") content.append(comparison());
    else if (mode === "join") {
      const card = el("article");
      card.append(
        el("h2", "加入已有系列 · 虚拟示范"),
        el("p", "只由三条明确人工关系构造，不代表真实系列验收。", "muted"),
        el("p", "本区固定使用平均连接＋主体画像，不随对照切换。", "muted"),
        members(item.series.work_ids),
        el("h3", "待加入作品"),
        work(item.query_work_id),
      );
      for (const p of item.proposals) card.append(planCard(p));
      for (const p of item.pending) card.append(pendingCard(p));
      content.append(card);
    } else {
      const card = el("article");
      card.append(
        el(
          "h2",
          item.label === "same_series"
            ? "已知同系列，未进入同一主方案"
            : "已知不同关系，被放入同一主方案",
        ),
        members(item.work_ids),
      );
      content.append(card);
    }
  }
  if (!list.length) content.append(el("p", "此视图暂无项目。", "empty"));
  $("progress").textContent =
    `共 ${list.length} 项 · 第 ${pageIndex + 1} / ${Math.max(1, Math.ceil(list.length / 8))} 页`;
  $("previous").disabled = pageIndex === 0;
  $("next").disabled = (pageIndex + 1) * 8 >= list.length;
  const a = current().audit;
  const oof = R.oof[$("variant").value].metrics;
  $("summary").replaceChildren(
    el(
      "p",
      `${names[$("variant").value]}：${a.primary_groups} 个主方案、${a.pending_works} 个待核对作品、${a.alternative_count} 个备选；主方案重复归属 ${a.duplicate_memberships}。`,
    ),
    el(
      "p",
      `主方案覆盖已知系列正例 ${a.pair_metrics.combined.tp} / ${a.pair_metrics.combined.tp + a.pair_metrics.combined.fn}；覆盖与混组情况请查看四项对照。`,
      "muted",
    ),
    el(
      "p",
      `五折已见数据检查：较强层配对精确率 ${pct(oof.strong.precision)}，合计 ${pct(oof.combined.precision)}、召回 ${pct(oof.combined.recall)}。这些不是整组精确率。`,
      "risk",
    ),
  );
}
$("previous").onclick = () => {
  pageIndex--;
  draw();
};
$("next").onclick = () => {
  pageIndex++;
  draw();
};
$("mode").onchange = $("variant").onchange = () => {
  pageIndex = 0;
  draw();
};
draw();
