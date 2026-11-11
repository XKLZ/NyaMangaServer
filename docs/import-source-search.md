# 导入来源查询与候选比较

## 语义与数据所有权

搜索结果、比较快照、待审核候选、正式资料四者分离。查询任务类型metadata_search，复用元数据单工作器和站点串行限速；它不属于自动阶段推进的import_children。查询保存内容版本、摘要及算法、来源选择版本；所有结果发布检查批次仍在元数据阶段、成员未审核、维护状态和任务代次。

创建／补全不会修改人工网址、正式来源、人工值或既有待审核候选。select原子替换一份待审核候选、递增成员revision与请求代次、清除该项待处理错误；不联网、不采纳、不确认审核。之后继续使用现有reviews／manual-review事务；保留最新人工覆盖和完整分类／评分等资料。原自动元数据任务未结束时选用返回资源忙，避免另一个工作器覆盖选择。

同一成员新查询取消旧查询活动任务并停止旧IO；不会取消普通元数据任务。旧完成查询可读取，但只有最新查询可首次选用，防止晚到的旧选用请求覆盖新操作。重复选用只在同一结果版本、同一待审核候选revision仍有效时幂等。关闭抽屉仅停止轮询，不默认取消任务。

批次暂停／取消／重新审核同时保护查询任务。只有被批次暂停的查询随批次恢复，用户自己暂停的查询不会自动恢复。终态任务清理保留被查询请求引用的任务；本轮不自动清理查询历史。元数据审核期间无30分钟过期，不因为等待审核而重复联网。进入后续阶段后旧查询禁止选用。

## API

公共前缀：`/api/v1/import-batches/{id}/members/{edition}/source-searches`。所有写入仍需Origin和CSRF；所有读取需会话。OpenAPI为机器契约。

| 方法与后缀 | 输入／输出 |
|---|---|
| POST 空后缀 | mode=matches或title、q、request_key、expected_revision（当前成员revision）；202 SourceSearch |
| GET /{search} | 查询状态，不联网 |
| GET /{search}/results?page=1&page_size=10 | 本地结果分页，不联网；page1–20，固定每页10 |
| POST /{search}/load-page | page、expected_revision（查询revision）、request_key；202，按需继续站点分页并补全该页 |
| GET /{search}/results/{result} | 完整规范化字段、中文标签、远端页数；不返回远端原文 |
| GET /{search}/results/{result}/cover?result_revision=… | 按需同源PNG预览；不传任意远端网址 |
| POST /{search}/select | result_id、expected_revision（查询）、result_revision、member_revision、edition_revision；200 selected=true |
| POST /{search}/control/{action} | action=pause／resume／cancel／retry；expected_revision（查询）；返回查询最新状态。retry保留失败操作页码 |

SourceSearch包含id、batch_id、edition_id、mode、q、site、revision、job_id、state、error_code、active_page、loaded_count、continuation，以及冻结的content_revision、selection_revision、edition_revision、member_revision。页面提交成员／Edition版本取编辑起点的实际值；冻结值与当前不同需明确重载，不能跟随后台刷新无提示升级。

continuation为unknown／more／end／limited。loaded_count只是已保存数量，不是站点总数；unknown专供旧SHA1缓存等无法证明完整的结果，more表示站点可继续，end表示查询完成，limited表示资源上限。初次matches优先读取当前任务保存的候选，只补全已有首屏；不声称旧10条就是全部。用户“查询更多”时提交load-page：当前页不足10项则仍加载当前页；完整页则加载下一页。服务端重新检查站点并去重；若无新结果留在原页提示已查完，不跳到空页。

结果包含id、url、title、position、revision、state（pending／ready／failed）、error_code、可选fields、tags（译文及来源，未收录回退原文）、可空page_count。远端页数不覆盖File页数。只有ready可选用。搜索任务部分画廊失败不会阻止同页其他画廊，单项失败可再次load-page；全局认证／访问限制暂停任务，恢复需用户明确点击。重试使用新幂等键，保留已准备成功的资料，不重复获取ready条目。

导入成员列表批量增量返回match_count、match_list_complete、source_search_id。不逐卡查详情。source_search SSE仅携带查询ID／revision；原import_batch通知继续表示批次版本，不能用查询revision冒充批次revision。

## 网络与资源边界

标题输入最多256字符／1024字节，服务端构造title限定的普通文本查询，不接受任意站点高级语法。只点击搜索／打开比较／加载页触发已明确授权的远端操作。站点分页信息只在服务端保存，校验主机并提取数值next，重建请求而不跟随HTML中的任意网址。

搜索页最多4MiB，不能解析的页面报invalid_response，与零结果区分。每页最多10个gdata请求项，按画廊身份匹配响应，不依赖响应数组顺序。每会话200个画廊／32MiB完整资料，达到限额明确limited或search_limit_reached，不隐瞒截断。旧候选、已读页和正式资料不因新查询失败被清空。Cookie仅发送至配置站点，API及图片不接收站点Cookie；设置站点切换后旧查询需重新创建，返回provider_settings_changed。

图片复用cache/import-previews/v1、128MiB／1000项、并发2；原图4MiB、8百万像素、最大边8192，重新编码后最大2MiB。预览需结果revision，并在IO后复核。缩略图只在hover／聚焦／点击比较时请求，不批量预取。未知目标、重定向、凭据隔离、DNS及解码限制沿用已有受限图片客户端。

## 前端交接要求

1. 批次父页面统一轮询：自动处理每2秒，SSE合并触发，恢复可见立即查询；扫描结束自动进入下一界面。人工关卡仍需用户放行。删除子页面重复批次轮询，保留成员／方案各自按需更新。
2. 同阶段更新不卸载卡片、不全区遮罩；保持勾选、草稿、冻结revision、焦点和滚动。失败就地展示，不卡在扫描文案。
3. 多结果显示“发现N个候选·请选择”，不能默认选第一项或自动勾选。与标题搜索共用比较抽屉，固定本地封面／文件名／标题／页数／语言，候选列表与完整详情分层展示。
4. 标题与文件名高亮共同文本，不声称置信度。候选字段包括两种标题、分类、评分、语言、页数；标签按中文分组。网址点击复制，小图标打开，保留粘贴网址路径。
5. 打开抽屉创建或恢复查询；POST授权准备、GET只读取。查询／翻页按search ID隔离，局部加载，旧响应不能串页。搜索草稿不随后台通知覆盖。关闭后下次保留查询词、页码、选择和位置。
6. select成功后回原卡片、定位并更新待审核候选，继续原单项／批量确认，无额外确认弹窗；不可把select当作正式采纳。原有人工编辑抽屉和批量保护保留。
7. 标题检索预填有限清洗文件题名，可编辑；只按钮／回车联网，不随输入搜索。三种状态“加载中”“该候选不可用”“认证／网络问题”分别呈现；图片失败仍可审核文字。
8. 前端使用refactoring-ui和web-design-guidelines，保留Ant Design与主题；先结构稿、再5173热更新。仅web与专项说明；后端缺口回传协调，不自行改Go／SQL／OpenAPI。

## 验证边界

匿名测试涵盖分页超过10条、旧缓存完整性未知、标题语法隔离、错误页面／合法零结果、批量响应乱序／部分失败、选用不采纳、后续确认事务、并发旧工作器拒绝、文件变化与Edition版本冲突、批次取消、认证暂停恢复、schema15→16全旧表逻辑行保留、坏迁移回滚、快照及重复启动。HTTP包含会话／CSRF、跨成员访问、PNG成功与版本冲突、OpenAPI请求响应校验。

前端交接后补60项匿名刷新、无需手动刷新过扫描、搜索／比较／选用／批量确认、分页草稿、键盘与焦点、1280／1440桌面与390手机明暗截图。后端测试不能替代这些交互验收。真实站点／分页变体、用户Cookie和代理组合未自动访问；生产数据未搜索、未改动。

### 后端定向验证记录

`source-search-backend.log`：provider、metadata、importflow、store、httpapi、library相关查询／导入／迁移／兼容测试通过；其中store约6.7秒、HTTP约10.1秒。`source-search-final-focused.log`补充旧SHA1输入校验及选用冲突回滚通过；`source-search-race.log`相关查询并发测试通过，`source-search-vet.log`受影响包vet通过。Redocly OpenAPI、Go依赖边界、346个本地文档链接与diff检查通过。日志仅在.runtime/tests/results，匿名样本没有真实查询。

本轮不修改web、不执行前端构建／静态／浏览器测试；这些由前端接续时完成。后端API必要编译及真实实例更新结果另记于下方，不自动运行任何用户查找。

77张旧非账本表逐列规范逻辑行摘要全部一致，001–015账本原行不变，仅追加016；12,363个配置、密钥、人工素材、缓存及索引文件摘要一致，Vite PID保持。quick_check=ok、外键错误0，升级后仍无活动／暂停任务及待重验。8080与5173代理的readiness和auth/status均返回200 JSON。没有创建查询、搜索漫画、下载封面、处理候选或修改历史失败任务。

私有逐表／文件摘要、快照位置及旧程序server.before-search保存在.runtime/maintenance/import-source-search，目录0700、报告0600；不进入Git。回退须正常停API后将本次一致性快照恢复至独立目录并校验，不能用旧schema15程序直接打开schema16或覆盖较新用户资料。

网页仍由前端专项对话接续；本节只证明后端编译、匿名行为和真实保留升级，不代表扫描自动切换与比较抽屉已完成，更不代表真实站点分页和推荐质量已验收。
