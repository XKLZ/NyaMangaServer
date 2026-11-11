# 结构化漫画属性与完整译库联想

基线 `codex/admin-polish / 50de68d`。本轮继续当前分支，后端完成并验证后，再由前端专项对话串行接入。保留源漫画、账号、会话、来源和人工覆盖；不执行联网、浏览器或部署验收。

## 数据与规则

005新增六张表：effective_book_properties、effective_book_languages、projection_state、dictionary_generations、dictionary_entries、dictionary_state。基础身份仍为foundation-v1；001–004不修改，当前目标schema5，共46表。

属性是派生投影，人工值仍归field_overrides所有。推断纯函数位于model，store在业务事务内取最新输入、更新投影。扫描、来源发布/确认/解绑、人工字段/标签及重新关联使用同一规则；永久清除通过外键级联清理属性关系。原始来源和标签不被属性编辑反写。

- languages：语言数组或null。新人工值最多200项，每项64个Unicode字符/256字节；NFKC、大小写折叠及空白规范化后去重，保留人工顺序。自动语言按规范化名称排序。
- translated、extraneous_ads：boolean或null；color_mode：monochrome/color/null；censorship：uncensored/mosaic/full/null。
- 人工set/empty/inherit分别为填写、明确未知、恢复自动识别。empty阻止自动回填，继承删除覆盖；人工值与标签不一致仅展示差异。
- 语言优先级：languages人工覆盖、旧language人工覆盖、有效language标签、合法旧来源language、未知。translated/rewrite不是语言，不从标题推测语言。旧人工自由文本保留，不猜测拆分。
- 其他属性先有效标签、再完整有效标题/原文标题的括号明确标记；没有来源标题时使用完整文件名标题。不使用网页净化标题。标签冲突不由标题覆盖，多个语言不是冲突。
- 固定映射：language:translated；other:full color；other:uncensored、mosaic censorship、full censorship；other:extraneous ads。标题括号映射沿用先前网页有限词表：翻译/翻訳/translated，彩色/カラー/full color，黑白/白黒/monochrome，无修正/無修正/uncensored，马赛克修正/モザイク修正/mosaic censorship，完全修正/full censorship，额外广告/extraneous ads，无额外广告/no extraneous ads。
- 缺失不推断false、黑白或无修正；自动冲突为null且state=conflict。人工明确值仍为normal，自动冲突保留在automatic中。

## 属性API与前端约定

书籍详情的metadata.properties：

```json
{
  "rule_version": 1,
  "languages": {
    "value": ["english", "japanese"],
    "origin": "tags",
    "state": "normal",
    "evidence": ["language:english", "language:japanese"],
    "automatic": {
      "value": ["english", "japanese"],
      "origin": "tags",
      "state": "normal",
      "evidence": ["language:english", "language:japanese"]
    }
  }
}
```

示例省略其余四项，实际详情五项齐全。每项都包含value/origin/state/evidence/automatic。origin为none/tags/title/source/manual/legacy_manual；state为normal/unknown/conflict。管理列表摘要不返回完整properties，详情加载后再使用。

PATCH /api/v1/books/{id}/overrides在原fields对象增加languages/translated/color_mode/censorship/extraneous_ads，与基础字段/标签一次保存；沿用expected_revision，不增加独立保存接口。设置使用`{"op":"set","value":...}`；明确未知和恢复继承仅传op=empty/inherit。新语言空数组无效，应发送empty；布尔false不是空值。

metadata.fields.language保留string|null兼容视图，为有效语言第一项。当前多语言时，旧language写入409 multiple_languages_require_array；同请求language/languages并存400 conflicting_language_fields。用户明确编辑languages时，同事务移除旧language覆盖；回填不修改旧覆盖。

前端使用后端automatic显示恢复识别结果；标签草稿尚未保存时提示“保存后按最终标签重新识别”，不在网页重建推断规则。新的数组比较必须按语言顺序比较，不能复用标签排序/TagName转换逻辑。

## 属性筛选

普通POST /books/search、管理POST /admin/books/search和GET命中明细的查询JSON均扩展properties；保存搜索格式仍为1，旧记录不变。

```json
{
  "properties": {
    "languages": {"any_of": ["english", "japanese"], "include_unknown": true},
    "translated": ["yes", "unknown"],
    "color_mode": ["color"],
    "censorship": ["uncensored", "mosaic", "full", "unknown"],
    "extraneous_ads": ["no"]
  }
}
```

未选属性省略；同属性OR、跨属性AND；未知包括冲突产生的null。语言条件不能既没有any_of又不包含未知。前端必须显式JSON编码/解析URL中的properties，保存/恢复/重置和snapshot条件键同步适配，不把对象转为字符串[object Object]。SQL先筛选物理书籍再折叠分页，管理端沿用独立物理分页。GET命中明细将properties作为JSON字符串查询参数传递，非法JSON或未知字段明确失败。

## 完整词库与标签联想

完整纯文本词库保存在独立SQL派生表，不创建业务tags。空译名词条仍保留成员身份，规范化同名而内容不同明确拒绝。下载/解析上限仍32MiB、50万词条；人工译文始终优先，不执行或渲染远端HTML。

索引按词库版本+摘要定位代次，每1000词条提交检查点，在线批次/发布/淘汰都校验任务执行代次。全部词条和数量校验完成后，与活动版本切换、已用标签翻译和任务成员完成一起原子提交。活动、上一完整和当前构建代次受保护，旧派生代次分批淘汰。原始缓存和人工翻译不随索引淘汰删除。任务缓存落盘后持久化摘要；暂停/中断恢复可复用已校验原始缓存，不重复下载。

启动在HTTP/后台任务前尝试离线准备：有效SQL索引无需原始缓存；缺索引时使用校验通过的已有缓存。缺失/损坏/构建失败降级本地查询，普通业务继续启动，不联网补取。编辑后只从活动SQL索引物化本地使用到的翻译，不反复读取完整缓存。

新增POST /api/v1/tag-suggestions，保留GET /tags：

```json
{"namespace":"artist","q":"匿名","exclude":[{"namespace":"artist","name":"selected"}],"page":1,"page_size":50}
```

- q最多512 UTF-8字节，namespace省略或空表示全部；exclude最多200项，规范化去重。
- page默认1，每页默认50、最大100；后续页必须带snapshot_revision并保持条件/每页数量。
- 返回items,total,page,page_size,snapshot_revision,dictionary_state。
- items包含namespace/name、translation、translation_origin(manual/remote/original)、usage_count、local_exists、可空tag_id、三态dictionary_match。稳定身份是namespace/name，零使用词库项的tag_id为null，不能当业务tag ID提交翻译。
- dictionary_state为ready/unavailable/not_downloaded。只有ready时dictionary_match为true或false，否则null；空译文不等于未收录。
- 使用次数为启用且未回收书籍中的有效标签数，包含个人隐藏，每本至多一次。按频次降序、本地存在优先、规范化名称/稳定键排序；排除发生在SQL计数及分页前。
- SQL匹配原文、人工译文及活动译库译文，不全量加载到网页/Go后筛选分页。快照绑定目录版本、索引代次、人工译文版本及规范化条件；过期409 tag_suggestions_snapshot_expired，重新查第一页。
- metadata.tags同时返回dictionary_match，前端保留200ms防抖、请求取消/陈旧响应隔离、已选排除、键盘和原子粘贴。

## 升级与验证状态

005结构和版本账本仍由现有迁移事务提交，升级前一致性快照。随后在实例锁内、对外服务/后台任务前，属性按稳定ID每500本提交投影与检查点。SQL失败回滚；回填失败保留schema5及检查点并拒绝启动，重启继续。回填不修改认证/会话、来源原文、人工覆盖或书籍编辑版本，不使用替代空库。

已通过属性推断/校验、原子保存/语言兼容、502本匿名回填中断续建、schema4升级与失败回滚、属性筛选/保存搜索/快照，以及词库解析、1001词条索引中断、暂停恢复、词库版本保留、零使用/无译名词条、频次/中文检索/排除/分页、人工译文保护和缓存恢复测试。HTTP契约新增属性和联想流程通过；受影响包race通过，缓存检查点新增测试另做定向race。命中明细GET筛选编码与非法字段拒绝、共享来源刷新/解绑/永久清除、属性版本折叠均已定向race通过；OpenAPI、依赖边界、文档链接和diff检查通过。

前端已在6df39d5串行接入，父对话随后补充不存在书籍编辑的404处理（2dc3fad）。前端一次31项单元整体通过，修正后相关14项及追加语言状态词测试通过；格式/ESLint/类型检查通过。没有运行浏览器、生产网页构建、真实上游下载或部署验收。用户此前对列表/侧面板的手工确认不扩大为本轮UI已通过。

### 开发实例实际升级（2026-10-15）

- 已核实项目API进程与配置路径，确认无queued/running任务及周期扫描后正常SIGTERM停止；Vite保持运行。
- 编译Go程序0.6.0-dev，构建提交2dc3fad；通过原vite-config.json启动，继续http://localhost:5173/admin/，不切回8080同源网页。
- 应用创建新的0600一致性快照后执行005与属性回填；快照为schema4并与升级前摘要一致。
- schema5已就绪，17本索引对应17行属性投影。原37表的业务字段/关系及人工译文摘要一致；核验排除事件、目录版本、迁移账本和可重建远端译文，原001–004账本另行逐项一致。账号1、会话2、书库1、书籍17；远端译文仍19条。
- 开发配置摘要不变；quick_check/foreign_key_check通过，无活动任务及周期扫描。原来源、人工覆盖、组织、收藏和会话没有被回填改写。
- 现有缓存已离线建立44,590条词库索引，dictionary状态ready；未下载真实词库或发起站点请求。没有读取漫画源文件。
- 私有核验报告仅位于被忽略的.runtime/tests/results/properties-dev-upgrade.json，未输出标题、凭据或原始数据。

启动证明与界面/识别准确性分开：新增属性编辑、筛选和联想的真实页面操作待用户体验，生产网页产物未更新。本轮Go受影响包/race/vet、HTTP/OpenAPI、前端定向单元与静态、链接/diff检查通过，不沿用旧浏览器或部署结果宣称本轮通过。
