# 整实例备份与恢复

用于同一应用构建之间的故障恢复与跨机器迁移，不合并实例，不是漫画导入或数据库抢救。具体发行验证见[测试版说明](releases/v0.7.0-beta.1.md)。

## 包与保留边界

ZIP格式版本1，必要时自动写ZIP64。`manifest.json`记录应用／foundation-v1、版本及构建、schema、逐迁移账本摘要、标准schema摘要、对象数量、相对文件键、实际长度及SHA256。必须完全相同构建和账本；未知／dirty开发构建拒绝。包不加密，含密码摘要、凭据及解密密钥，必须按秘密保管；目录0700、包／清单／素材0600，部分归档不可下载。

完整一致性数据库保留所有账号、关系、来源原文、人工明确清空、组织、版本标签及所有权、关注、图片、例外、任务／检查点、审核候选、决定／撤销及健康报告。长期assets包括`recommendation_materials`；现有封面、标签图片、导入预览、译库原文及派生SQL、推荐特征／增量块一并保留。必要健康修复回退文件及其摘要引用必须完整。原本missing／corrupt／needs_review不被当作备份失败或自动修复；缺失必要长期素材或损坏vault配对则不能成功。

不含源漫画、模型权重、Python环境或程序、宿主配置、锁、普通临时文件／日志、旧备份／恢复暂存，及无引用历史升级快照。源根、模型、Python/venv及worker与数据托管路径重叠时安全拒绝，不为打包或目标枚举读取这些资源，也不安装覆盖它们。排除`instance-recovery`避免递归膨胀。此前文档“未来普通脱敏业务导出移除密码／凭据／派生缓存”不适用于本专项私有整实例包；TreeC转换和历史脱敏导出设计仍保留原边界。

## 备份

持久维护屏障拒绝业务与旧工作器最终写入；排队／运行任务提升代次并暂停，原暂停任务保持。收尾HTTP、扫描、元数据、译库、推荐和维护IO后持独占素材租约，使用SQLite Online Backup，冻结数据库、vault和文件集合，复制成独立私有材料。释放保护后才压缩，不因长压缩阻塞日常管理。不会自动恢复任何暂停任务；后台编排保持静止，用户通过全局后台开关显式解除，避免尚未创建的导入子任务或增强切换在解冻时自行运行。

操作使用持久ID、请求幂等键和CAS revision，`instance_operation` SSE只通知刷新。中断留下的屏障不自动解除；重启标记`interrupted`，显式取消会收尾IO后只释放同一操作的保护。成功归档在认证下载前再次核对摘要。

## 网页恢复契约

所有路由位于`/api/v1/maintenance`，登录会话、Origin及CSRF仍走原认证边界。上传用`application/zip`、`Idempotency-Key`，不支持服务端按远端URL拉取备份包；已完成备份仍通过认证下载路由获取。上传／下载及指定验证路径只在认证通过后，从原始请求建立有限30分钟context并调整该路由socket期限；保留客户端断开取消。普通请求30秒context及全站15秒读／30秒写超时不变。

| 路由 | 行为 |
|---|---|
| `GET/POST backups` | 分页历史，可选`request_key`精确查找／创建；创建JSON只有`request_key` |
| `GET backups/{id}` | 持久状态与revision |
| `POST backups/{id}/cancel` | `expected_revision`；取消或释放中断保护 |
| `GET backups/{id}/download` | 仅完整包；no-store、认证下载 |
| `GET/POST restores` | 分页历史，可选`request_key`精确查找／ZIP上传 |
| `GET restores/{id}` | operation、可空summary、目标可选根、已保存映射、冻结preview |
| `POST restores/{id}/validate` | CAS启动独立验证，持久状态轮询 |
| `POST restores/{id}/preview` | `expected_revision`、全量`root_mappings`、`acknowledge_stat_only:true` |
| `POST restores/{id}/confirm` | `expected_revision`、`request_key`；保全目标并安排正常重启激活 |
| `POST restores/{id}/abandon` | CAS放弃；无激活日志的中断准备可安全解除本操作保护，有日志按日志取消 |
| `POST restores/{id}/rollback` | 激活日志的`expected_revision`、`request_key`；安排受控回退 |
| `GET instance-activation` | 主库外激活日志状态、进度、revision |
| `GET instance-state` | 全局后台静止与业务写入代次 |
| `POST instance-state/release` | `expected_revision`、`allow_automatic_background:true`；显式解除后台静止 |

上传／创建响应丢失时，用同一请求键只读找回持久身份：`GET /api/v1/maintenance/restores?request_key=anonymous-upload-key`；备份使用`backups`路径。参数可省略以列普通历史；提供时必须单值、长度8–128（与创建相同校验），空值、重复值、非法编码或越界返回422。SQL在分页前按当前端点kind及request_key完整匹配，大小写／前后缀不推断，未找到返回现有array格式`[]`；不创建操作、不改变状态或后台保护、不向历史响应增加request_key字段。现有跨kind全局幂等键唯一约束不变。

响应丢失不表示服务端没有接收：首次`[]`只代表该次查询快照尚未找到，不能据此POST空文件试探，也不能改用时间或最新记录猜测。若旧上传仍在途，后续按原key查到pending／uploaded，ID保持同一个；前端已切走的旧请求可按原key找回ID及最新revision，通过原abandon接口显式回收，再查询确认终态，不把旧响应写回当前草稿。该GET沿用原登录认证，不新增Origin／CSRF写入例外。

`summary`在完整验证后提供源书库ID／名称／root_key、对象数、构建、schema和文件规模；前端先读取它及目标允许根，再提交映射，不猜ID。预览保存映射、目标指纹、对象替换／账号重登影响及空间预算。路径、秘密和私有journal不进入API。稳定错误及完整schema见[OpenAPI](../api/openapi.yaml)。

```json
{
  "expected_revision": 4,
  "acknowledge_stat_only": true,
  "root_mappings": [
    {"library_id": "fixture-library", "target_root_key": "fixture"}
  ]
}
```

把`target_root_key`设为null表示暂不关联：恢复元数据和缓存，扫描／源读取被根映射保护阻止，不批量标missing。保持原一根一库及root_key唯一约束，不扩大领域。Docker映射应指向容器允许根，例如`/library/comics`对应的配置键，不接收NAS宿主路径。

## 验证、映射与空间

先检查ZIP目录和清单，再创建独立解包对象：拒绝越界、符号链接、重复名称、非法类型／加密条目及文件和目录冲突。压缩256GiB、展开1TiB、单文件128GiB、最多100万文件、清单128MiB的上限同时约束声明与实际读取，CRC及SHA256均核对。数据库只读检查integrity、外键、身份、完整账本；schema与独立匿名标准库比较，不能仅伪造账本夹带触发器。

根只能来自目标显式配置，拒绝越界符号链接。只对已有File对应路径stat，核对普通文件及大小，登记目标目录身份和mtime；不读漫画内容、不推理、不重扫。**这不能证明源内容相同**；同尺寸的不同内容不能通过stat识别，用户必须确认复制的是对应源文件。库ID、Work／Edition／File、内容revision和来源匹配revision保持；终态和暂停任务冻结输入不因迁移mtime改写。

源文件相对路径与恢复包成员键分别校验。在POSIX文件系统上，`CHAPTER 01: EXAMPLE.zip`、`Re:Example.cbz`等普通文件名中的冒号允许保留；仍拒绝绝对路径、点段、空路径段、反斜杠、NUL和任一形如`C:`的盘符路径段。Windows端继续拒绝冒号，防止备用数据流。包内`assets/`等成员键保持原有可移植限制，不随源文件规则放宽。映射预览、准备及激活复核使用同一规则；缺文件、大小不符和符号链接仍拒绝。

空间探针在解包前、预览及确认时执行。预览列上传／已验证／准备副本、两份目标保全、最大临时安装和64MiB安全余量；不足返回507。明确配置的外置秘密父目录另检查写入与空间，不能改只读配置或沿用源机器绝对路径。实际写入仍可能遇到并发磁盘消耗／ENOSPC，日志保留可继续或回退的边界，不宣称预估能锁住磁盘。

## 激活、静止与回退

确认先保全一致性目标数据库和素材，写入主库外`instance-recovery/activation.json`并保持屏障。由用户正常重启；应用取得原实例锁后、普通Store.Open／迁移／任务恢复之前执行激活。目标停止后的DB／WAL／SHM再次完整保全，再同文件系统临时复制和逐文件原子安装；不重命名数据卷根。逐步journal支持替换前后中断重放，不能启动半恢复实例。

目标listen/public_url/data_dir/允许根/Python/模型等配置保持；源业务设置和vault配对安装到目标明确配置的可写位置。源密码和auth_revision保持，所有旧会话删除，重新登录；高亮授权有效／撤销／改密失效状态不复活。原queued/running暂停，原paused和review保持。

恢复后持久`background_hold`覆盖定时扫描、导入待建子任务、来源查询、增强切换、推荐重验、健康启动恢复及启动投影／译库准备。用户明确解除后这些自动编排才可运行；解除不自动resume原暂停job。

激活后无新业务写入才允许整实例回退。重新登录、SQLite正常checkpoint和可重建缓存读取不等于业务修改：停止持锁后核验恢复身份及业务epoch，再保全实际当前物理文件集合，避免用prepared DB旧字节误判；已有新业务写入拒绝覆盖。回退材料受存储统计及清理保护，不随普通tmp／快照清理删除。

## 停服离线入口

独立命令不调用Store.Open、迁移或任务恢复，支持空目标、旧库和损坏目标。普通模式拒绝未解决健康屏障／unsafe标记；明确故障替换使用`--replace-unhealthy-target`，仍必须先预览，再`--prepare`再次确认。该模式保全旧DB/WAL/SHM、unsafe标记及完整健康日志；成功切换后旧unsafe隔离在回退材料，回退时恢复。任何有效未完成恢复激活journal都不能绕过；开发reset中断标记也不绕过。

标准`make build`会配对构建server及instance-restore；本批验证未调用make build，以免触发生产网页构建。若已有网页嵌入产物，只编译这两个Go入口可使用相同标识：

```sh
BUILD_ID=$(python3 scripts/check/build-identity.py)
go build -trimpath -ldflags "-X main.commit=$BUILD_ID" -o .runtime/bin/server ./cmd/server
go build -trimpath -ldflags "-X main.commit=$BUILD_ID" -o .runtime/bin/instance-restore ./cmd/instance-restore
```

干净源码返回完整Git SHA；dirty／unknown只允许普通管理，整实例操作拒绝，不用不一致的短／长标识混编。Docker镜像内同样包含两个命令，离线同卷entrypoint示例见[部署材料](../deploy/README.md)。请求JSON必须0600，包含`request_key`、`acknowledge_stat_only:true`、全量`root_mappings`。命令只安排重启切换，不替用户启动服务：

```sh
instance-restore --config /explicit/target.json --archive /private/instance.zip --request /private/request.json
instance-restore --config /explicit/target.json --archive /private/instance.zip --request /private/request.json --prepare
instance-restore --config /explicit/target.json --status
instance-restore --config /explicit/target.json --rollback 12
```

故障替换在前两条都加`--replace-unhealthy-target`；改变包／映射／目标需重新预览，不能复用已确认旧目标指纹。首次`--prepare`没有先前预览会冲突。恢复的旧坏目标在回退后仍可能无法启动，这表示恢复了故障前保全状态，不是抢救或修复成功。

## 独立匿名前端入口与实际验证

不构建生产网页，先单独编译`cmd/fixtures`，使用全新`.runtime/tests/...`目录：

```sh
go build -o .runtime/bin/fixtures ./cmd/fixtures
.runtime/bin/fixtures --recovery-instance .runtime/tests/recovery-ui-anonymous
.runtime/bin/fixtures --serve .runtime/tests/recovery-ui-anonymous/config.json
```

账号密码为此**匿名夹具专用**`anonymous recovery password`。端口仅18081／18082，根目录必须通过固定匿名归档指纹检查。装配真实NewInstance／HTTP／重启激活，使用真实SQLite、素材与vault；网络provider、推荐runner仍是离线匿名替身，不能冒充真实模型或真实联网验收。夹具有非空组织／来源／人工empty、标签策略／关注／图片、导入审核、决定／撤销、健康依赖及有效／撤销高亮授权；可用null根映射体验暂不关联。

当前已实际通过的定向匿名检查包含一致性备份、vault配对、恶意包、屏障暂停、全业务非空逻辑逐表比较、原文件状态、重新登录／旧会话失效、正常关闭后回退、新业务写入拒绝、首次数据库切换后中断续做、离线空／坏／锁定目标、显式不健康替换及回退、丢失SQL确认收据重放、准备中断放弃、映射stat与空间不足、API认证／Origin／CSRF／schema及慢上传的有限路由超时。完整相关门禁及本地提交结果见下节；没有真实实例、生产网页、容器、NAS或模型执行证据。


## 2026-11-03后端收尾记录

内核增量`66979cf`、固定运行环境`1c0d66d`，后续收尾提交补入以下实际验证与保护。新增024为106表；23→24失败回滚、重复打开及逐项001–023字节／账本保留通过，原macOS锁和models.lock.json保持原字节。日常7353486／schema23、8080／5173、生产网页及全部私人材料未操作。

- Go定向`TestInstance`通过：store／maintenance／httpapi；包含8个素材／vault／DB安装前后中断子场景、完整非空逻辑逐表往返、必要素材摘要、坏包／错版本、空间与权限预检、旧会话及回退后会话不复活、显式不健康目标、停止锁、准备中断放弃、确认收据丢失重放。
- 直接相关的旧健康屏障／023兼容、扫描取消重启、元数据旧IO收尾与陈旧发布、增强恢复／只读状态门禁通过。只更新服务状态测试的当前schema断言为24，没有把历史23升级改写为本次升级。
- 定向race通过：store保护／完成窗口；maintenance异步快照／屏障／准备中断、取消CAS与脱敏错误分类；httpapi认证传输／bulk context／请求断开。文件替换矩阵以普通测试为证；最初扩展race把矩阵重复建库也纳入，SQLite插桩耗时后在408秒主动中止，**不算通过**，随后按并发边界收窄并通过。
- 大包验证同时覆盖真实HTTP下350ms慢上传超过注入100ms普通context／150ms socket期限；通过Handler后的认证bulk截止约30分钟，普通请求仍30秒；未认证不进入bulk，原始客户端取消会传播。不是仅调整socket而保留30秒父context。
- `scripts/test/instance-recovery-smoke.py`真实匿名进程通过：ZIP上传下载、非空源摘要／映射、确认、正常停止重启激活、旧会话401、重新登录、实际后台tick后epoch仍0、定时扫描时间不变、导入review保持、正常checkpoint后同构建CLI回退及原根恢复。仅18082和全新`.runtime/tests`，所有进程已正常退出。
- Python平台锁／未知平台／错误Python／缺模型共3项匿名检查通过，无权重读取或推理。Linux两架构各23个wheel实际下载、摘要及ELF检查；官方固定镜像元数据核验方法与动态库静态覆盖见[运行锁](../runtime/recommendations/README.md)。`--check-local`、Python语法、OpenAPI lint／响应契约、Go vet（cmd/internal/migrations）、依赖边界、文档链接、gofmt及diff检查通过。

异步Operation保留失败phase和脱敏稳定error_code：`instance_build_mismatch`、`invalid_instance_package`、`instance_mapping_invalid`、`storage_full`、`permission_denied`、`instance_material_missing`、`instance_secrets_unavailable`、`cancelled`、`request_timeout`、`maintenance_active`、`instance_recovery_required`、`revision_conflict`、`invalid_instance_request`及兜底`instance_operation_failed`。不持久化原始错误或私有路径。验证中的放弃先返回`abandon_requested`，旧验证退出后CAS结算`abandoned`，不会发布迟到的成功。

已完成的激活／回退日志属于历史，不永久锁死日后升级或改监听／公开地址；未完成日志仍严格限制构建／目标配置。受控回退另核对原构建和目标配置，不能把旧回退跨构建使用。普通读取完成历史不触碰旧部署秘密路径。

前端接入依据本页路由、已保存summary／preview、OpenAPI和上述全新匿名夹具即可，不需要修改Go来伪造响应。未运行前端流程／生产构建、真实备份恢复／健康修复、日常024、Docker镜像构建执行、Linux／NAS实机或真实模型。没有推送、发布、新分支／工作树／Agent／对话。


## 上传响应丢失后的只读身份补充

在前端暂停写入时，仅补两个历史GET的可选精确request_key参数，不新增迁移、不修改POST幂等语义。定向仓储／匿名HTTP测试覆盖不存在无副作用、8／128边界、空／过长／重复／非法编码、SQL参数化与kind／前缀隔离、SQL过滤早于分页、认证、真正丢弃上传响应后找回，以及仍在途上传的pending→uploaded同ID、显式放弃迟到结果和pending已放弃后旧写入409、不复活终态。两项定向测试普通／race通过；相关store／maintenance／httpapi vet、OpenAPI lint与请求响应契约、gofmt、367条本地链接及diff检查通过。不重跑完整往返或模型套件，前端未提交改动原样保留。日常仍7353486／schema23，未操作日常实例、8080或5173。


## 2026-11-03前端接入：结构与保护

维护中心新增全局“备份与恢复”页签，不加主导航、不受当前书库筛选。桌面为240px分页记录和当前操作；手机默认收起记录。顶部提供创建、上传和固定刷新，完整备份单独认证下载。恢复依次展示上传与校验、目录映射、替换确认、正常重启与实际激活结果；主库外日志优先于回退后历史SQL收据，不把已准备当作已恢复。

网页沿用React／Ant Design、原生CSS及明暗主题。依Refactoring UI §2.1／§3.3／§3.6区分主操作和记录，技术指纹按需展开；可访问图标、原生折叠、表单标签和URL身份沿用Web Interface Guidelines约定。没有联网获取设计资料或引入依赖。

上传以原File为XHR请求体，携带application/zip、CSRF和固定Idempotency-Key，共用全局提交锁；只显示实际上传字节进度，不冒充服务器校验进度。取消传输后仍核对持久操作。创建或上传结果不明时，用后端精确request_key＋kind查询第一页；空数组仅代表该时点未存在，保留请求身份，不自动新建或重传。下载先用单字节Range请求核对可下载及材料错误、取消探测响应体，再由浏览器认证原生流式下载，不将整个ZIP读入JS内存。

每个源书库默认显式暂不关联，或只选目标允许的root_key友好名，不接收宿主路径。映射、确认选项和起始revision按操作保存在会话草稿中，刷新／分页／SSE不覆盖；不存文件、密码或凭据。目录预览使用起始CAS，409保留输入并要求明确重新核对。最终只有一次整实例影响确认，提示账号替换／重登、暂停任务、材料保全及新业务写入后的回退边界，不提供虚假网页重启按钮。

全局health_state.phase=instance_maintenance导航到备份恢复；instance-state.background_hold只暂停自动编排，不将业务编辑判为只读。后台恢复入口必须显式确认，不自动调用release，不混同增强开关，原paused任务仍手动恢复。schema23只显示未支持提示，不请求新端点或因404锁死业务。重新登录后仅恢复白名单操作身份URL；不把密码、路径或任意重定向放入URL。回退冻结激活日志revision而非操作revision，目标有新编辑时如实拒绝。

第一增量03e4b58提交源码和本节契约；下一增量补保护收敛、定向测试、实际截图及下列证据。日常8080／5173和.runtime/dev未操作；不构建生产网页、不改Go／API／迁移／依赖。


## 前端最终验证与交接（2026-11-03）

最初尝试只读复用5173源模块时，已授权匿名浏览器连接得到`ECONNREFUSED 127.0.0.1:5173`，同时`lsof -nP -iTCP:5173 -sTCP:LISTEN`无监听输出；这不是沙箱EPERM。没有据此启停或排查日常服务，改用专用5174，不以历史PID或匿名预览宣称日常可用。源码提供`vite.instance.config.mjs`及`playwright.instance.config.mjs`，匿名套件需预先编译`.runtime/bin/recovery-ui-fixtures`，然后使用`NYAMANGA_E2E_VITE=http://127.0.0.1:5174`执行。

### 核对中收敛的边界

- 结果不明保留原key，精确查询始终使用page=1；真正空列表也不推断请求永远不会落库。保留“该读取时点”的提示，查到持久ID后才允许取消／放弃，不提供虚假的取消成功。
- 只有已证明操作尚未创建的稳定配对拒绝清理本次待查身份：401 unauthenticated、403 csrf_rejected／origin_rejected，以及409 instance_build_mismatch的构建前置拒绝。网络失败、其他403、普通409、内部失败及未知结果不按此清理。请求未获准时可重新登录或刷新CSRF后由用户明确重新发起，绝不自动重发。
- 真实401测试发现请求锁仍在finally释放时会阻止登录导航。现仅会话明确失效时允许跳转登录；普通离开／切库／页签草稿保护仍在，恢复映射会话草稿和待查身份不因认证页面清空。
- 同一已选owner的等待重启状态只保留全局只读提示和当前步骤的一次重启说明；不重复owner入口或独立activation等待块，已核对映射收为只读折叠摘要。已激活结果及回退入口保持。schema放到技术标识；summary.files标为“包内文件”，不误称全部是素材。
- 回退409只在当前确认框显示一处对应说明：当前条件已变化、返回核对、新业务写入不可回退；请求编号按需展开。不再堆叠黄条／红条或提示重新预览、输入revision。映射冲突仍保留明确重验入口及原草稿。

### 实际结果

四类场景各覆盖桌面和手机，共8项；重跑不重复计数。前端没有重跑已通过的Go／race／模型／部署套件。

| 范围 | 实际证据 |
|---|---|
| 相关单元20项 | instance6项、health5项及既有路由／写锁相关回归；覆盖默认null映射、白名单登录返回、只读hold区分、精确控制路径、原File／CSRF／请求键／提交锁／取消、无Blob完整下载探测、确定前置拒绝与未知结果分类。`instance-ui-unit-delivery.log` |
| 主流程桌面／手机2项 | 真实创建及浏览器原生下载、上传校验、null映射、刷新／重载保留、单次确认、正常停止重启激活、旧会话401、重新登录返回原ID、实际受控回退。桌面13.4秒、手机13.0秒，`instance-ui-desktop-fifth.log`、`instance-ui-mobile.log`；随后只复核受影响交互，没有重跑无变化主流程 |
| 异常与冻结桌面／手机2项 | 原键尚不存在→同key迟到落库找回；实际已上传响应丢失；真实坏包失败及放弃；历史包丢失提示；已配置目录映射、Space勾选、冻结预览后目标实际编辑409、刷新保留并明确重验；保护导航返回owner；正常重启后新编辑实际拒绝回退且人工标题保留；首次显式release后hold解除、原paused job仍暂停。最终收敛截图复核日志`instance-ui-visual-final.log` |
| 中断桌面／手机2项 | 旧23不访问新端点；停止上传后保留原key，真实HTTP分段迟到上传找到pending；UI明确放弃后旧上传CAS返回409且不复活；离开页签一个完整轮询周期及通知后不再读取该操作。`instance-ui-cancel-desktop.log`及`instance-ui-boundaries-mobile.log`的cancel场景 |
| 前置拒绝桌面／手机2项 | 真实未登录401、错误CSRF403、错误Origin403均未创建记录、不遗留待查身份，重新登录／刷新后明确创建和上传可用。Origin通过匿名API转发保留错误头，不伪造错误JSON、不访问外部站点。`instance-ui-final-boundaries.log`的auth场景 |
| 静态／范围 | TypeScript、仓库ESLint规则覆盖的src、所有变更web文件Prettier、文档本地链接和diff；测试模块按仓库既有规则由Node／Playwright与Prettier检查。最终没有Go／SQL／API／依赖／运行材料改动 |

12张正常viewport匿名截图：1280／1440桌面和390手机的明暗映射6张，等待正常重启2张、重登后实际已激活与回退入口2张、新业务编辑拒绝回退2张。映射截图核对无横向溢出，手机步骤条紧凑横排；等待／激活截图等弹窗关闭再回到顶部，不把滚动恢复时的中间态当作布局验收。图形层级按Refactoring UI §2.1／§2.4／§3.6复核，键盘与可访问名称覆盖目录展开、Space勾选和明确按钮名称；未宣称完整屏幕阅读器或真实触屏设备验收。

记录位于被忽略的`.runtime/tests/results/instance-ui-*.log`；源码指纹为`instance-ui-source.sha256`，截图及4份浏览器真实下载产物的索引／尺寸／SHA256为`instance-ui-artifacts-final.json`，截图目录`.runtime/tests/instance-ui/screenshots/`。中间失败日志保留：匿名配置listen_addr键、未监听的5173、图标和重复状态定位、Origin头注入方式均已纠正；真实401写锁导航问题及已渲染的重复信息也在本批修正。只有最终通过日志计作证据。


## 2026-11-03上传失败结算后端收尾

在前端0da0ce5交付并停写后，仅收尾UploadRestore创建记录后的失败持久化，不改上传协议、迁移、前端或日常实例。修复前匿名注入证明：暂存父目录被普通文件阻断、已有partial冲突、发布rename遇到目录冲突三项返回错误但记录仍为pending；ENOSPC／权限reader错误虽为failed，error_code却统一丢为restore_upload_failed。该次红测仅作为复现证据。

创建成功后的所有错误出口现由同一defer结算：使用独立于请求取消、最多3秒的settle context，写入failed、phase=upload与既有instanceFailureCode脱敏分类。SQL同时限定原ID、restore kind、pending状态和原revision，不能覆盖已放弃、已uploaded或任何更新世代；不保存原始错误／路径，不移除冲突的既有partial。创建已提交但读回被取消时保留原请求ID，仍能按原request_key找回。最终uploaded SQL已提交而返回取消／响应不明时只读回其成功状态，不误降failed；请求原错误仍交调用方处理，不伪造传输成功。

后端内核66979cf、固定材料1c0d66d、生命周期4e44a81、原键查询2a3a5be；前端03e4b58／0da0ce5；上传故障a7c7d82。主对话审查契约、屏障、失败结算及启动路径，独立复验必要维护／HTTP边界后，核对前端23份源码指纹、12张截图及4份实际下载文件摘要与提交一致，复核1280／1440桌面、390手机的映射、重启、激活和回退冲突画面。20项相关单元和8项匿名桌面手机流程有实际记录；上传新增12子场景、7个结果不明／CAS子场景窄race及原键HTTP回归通过。001–023、原macOS依赖锁及models.lock.json逐字节保持；不重复已通过且未变化的广泛测试。

升级后schema24／106表，102张旧业务表原列与行逻辑摘要、001–023迁移账本及12454个配置／秘密／素材／缓存／索引文件摘要保持。SQLite quick_check为ok，foreign_key_check为空；instance_operations和instance_root_links为空，instance_state.owner为NULL、background_hold=0、write_epoch=0。快照可读、仍为schema23。没有创建真实备份、恢复、健康检查或修复，也没有登录或改写私人资料。

7项直接回环HTTP检查通过：API存活／就绪、已初始化状态、备份未认证401、Vite页面／API代理／新源模块；标准进程身份匹配。首次使用默认urllib环境代理的回环检查连接拒绝，未作为应用故障或通过证据；改用不经过环境代理的直接HTTPConnection后全部通过，未修改系统网络。配置和秘密未改动。私有核对、快照定位和旧程序保全位于`.runtime/maintenance/backup-restore-20261103/`，不进入Git。

入口为维护中心“备份与恢复”。创建后的任务不会自动恢复，后台自动处理须显式解除；恢复需正常停止重启并使用备份账号重新登录。源目录选择目标配置中的允许根，Docker选择容器内根；暂不关联可先保留资料和缓存。仅相同应用构建／格式／账本支持，整实例回退只允许尚无新业务写入。完整包未加密且含秘密，应私有保存。

未验证：真实私人全量备份／恢复、跨机器物理迁移、Linux／Docker运行与NAS性能、真实模型、完整屏幕阅读器及触屏设备。生产网页仍待本轮界面确认后集中构建，当前5173提供新界面；没有构建或启动Docker、自动下载模型、推送或扩展自动备份／远端存储／选择性恢复。完成后停在整实例备份恢复阶段。

新增账号集合／ACL与阅读三表完整进入相同build／账本的私有整实例包。独立结构核验仍从可信迁移构造匿名canonical库，其构造明确不携带请求Actor，避免以外来包中的账号ID判断基础设施权限；不对源包迁移、修复或放宽同版本策略。账号完整性校验不再把singleton=1当作总账号数限制。