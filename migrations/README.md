# 数据库迁移

现行代码迁移为001–022，代码状态统一见[README](../README.md)。本页按迁移版本描述结构变化；各版本的实例、端口和验收叙述只表示当时状态，不是执行升级或重置的授权。所有既有SQL保持原字节与校验和。


当前未发布基线为 `nyamangaserver`／`foundation-v1`，初始迁移 [001_foundation.sql](001_foundation.sql) 仅建立认证、书库索引与扫描任务。迁移执行器另建 application_identity 与 schema_migrations，共十表。书籍内部文件名标题为 filename_title，HTTP 继续返回 title。

启动先核验身份，再启用 WAL 和校验迁移版本、连续前缀与 SHA256；拒绝旧 M1／M2 实验库、未知身份、较新版本和校验和异常，不自动覆盖或替换。所有待执行迁移及账本写入在同一事务中；同基线升级前使用 VACUUM INTO 保存权限 0600 的一致性私有快照。

首个未发布基线获准重新整理；正式使用后的同基线变更必须新增迁移，不能修改已应用 SQL。Mac 开发库通过显式开发重置重新建立，不提供旧实验库转换。TreeC 转换仍未实现，见 [迁移兼容性](../docs/migration-compatibility.md)。

M3 追加 [002_metadata.sql](002_metadata.sql)，共 23 表。来源／覆盖／标签／有效投影、人工和远端翻译、发行版本、SHA1 查找缓存、通用资源与事件独立；任务表在事务内扩展并保留原 ID、成员与检查点。001 固定不变，M3 不清库。迁移回滚和旧基线升级测试见 internal/store/metadata_test.go。

003_organization.sql 为M4增量：稳定组织锚点、系列／版本组、账号状态、保存搜索与搜索投影；不改001／002。迁移使用应用注册的v1确定性Unicode函数，请通过服务／测试迁移器升级，不用独立sqlite命令直接执行该SQL。

004_administration.sql为管理台增量，扩展任务类型并保留检查点，新增停用/回收站、扫描排除、封面选择/长期素材、维护计划和来源预览。001/002/003校验和不变，升级前仍创建一致性私有快照。相关验收见[管理台记录](../docs/admin-console-verification.md)，不能直接用sqlite命令绕过应用身份、注册函数、迁移账本或数据锁。

[005_properties_dictionary.sql](005_properties_dictionary.sql)新增六张属性与词库派生表，schema5共46表，不修改001–004。SQL升级仍事务化和私有一致性快照；属性Go回填每500本独立提交检查点，完成前不能启动HTTP/任务，失败保留schema5供同版本重试。完整词库每1000项提交未激活索引，发布时验证任务代次和数量。详见[属性与联想](../docs/properties-tag-suggestions.md)。

[006_archive_manifest.sql](006_archive_manifest.sql)保留人工选页条目身份后删除book_pages；新增摘要算法/旧封面兼容字段及version_member_labels，schema6仍46表。旧单标签不猜测拆分，001–005保持不变。[专项约定](../docs/archive-manifest.md)。

[007_work_edition_file.sql](007_work_edition_file.sql)真实拆分Work／Edition／File并重绑来源、投影、标签、用户状态和任务外键；EditionID=旧书籍ID，WorkID=旧逻辑条目ID，Series保留。旧运行表退役，schema7共44表，001–006保持原校验和。旧组→Work映射写私有报告，先快照再原位升级，不清库。见[迁移与验证](../docs/work-edition-model.md)。

008_work_recommendations.sql 增加可选本地Work推荐任务、冻结输入、特征登记、候选及人工反馈；完整重绑既有任务引用，001–007保持原校验和。向量保存在私有可重建索引，主库仅登记指纹；验证快照封面属于长期素材。本轮代码schema8，现有开发实例仍保持schema7不操作，隔离试用另行初始化。未提供正式TreeC转换或自动归并。


009_work_discovery.sql新增library任务模式及配对检查点、方案／成员、持久决策和撤销快照。旧任务默认edition，不改001–008。方案／决策独立于任务清理，旧008采纳不伪造Undo。当前只完成匿名升级验证，真实pilot升级由主Agent收尾执行。

010_series_discovery.sql 扩展系列发现任务，新增冻结输入/标题特征/召回行、方案成员、待核对备选及决策快照，共62表。所有任务引用先重建后回填，保持旧行及001–009校验和；失败整体回滚。采纳只改Series，Work及Edition身份不变。代码schema10，真实隔离升级状态以[系列试用记录](../docs/series-recommendation-pilot.md)为准；日常实例不随试用升级。


011_version_management.sql 分离人工标签所有权与自动预设，登记完整配对证据的增量复用；历史人工清空和未知归属受保护，不能开页自动补标签。见[版本管理](../docs/version-management.md)。

012_import_workflow.sql 建立导入批次、固定成员、子任务请求／关联、候选与三阶段审核；普通扫描仍离线，候选不提前发布。013_recommendation_checkpoints.sql 增加系列增量证据、压缩检查点和引用保护。见[完整导入](../docs/import-workflow.md)。

014_import_stage_decisions.sql 为批次增加阶段处置JSON；明确跳过与失败历史分别保存，未成功重试或明确跳过不能冒充零建议。见[第二轮改进](../docs/import-round2.md)。

015_series_proposal_identity.sql 将系列匹配身份／作用域与存储方案身份分离，保留决定、撤销快照及跨作用域忽略／恢复投影。见[系列发布修复](../docs/series-publication-fix.md)。

016_source_searches.sql 增加来源查询、分页结果、幂等请求和metadata_search任务；重建任务约束时保留旧关联、检查点和触发器。查询结果、审核候选及正式资料独立，选用只暂存。见[来源查询](../docs/import-source-search.md)。


[017_tag_catalog.sql](017_tag_catalog.sql)新增四张标签关系表，schema17共85表；001–016不改。单原作证据在有效标签事务内维护，初次每500条离线回填，checkpoint与批次同事务，未就绪不能服务。全局人工决定与逐本自动否决分别CAS，DELETE保留递增墓碑。与迁移账本独立，回填失败保留schema17供同版本重启续做；不能用schema16程序回退打开。真实升级未执行，详见[标签管理契约与匿名验证](../docs/tag-management.md)。

018_tag_images.sql为原作／角色新增tag_images（独立role、素材外键、尺寸／格式和单调CAS墓碑），复用assets；schema18共86表，不修改001–017、不回填图片、不改变旧资料和任务。图片缓存不进入数据库。匿名17→18保留、外键、事务失败回滚及重复打开验证见[标签图片](../docs/tag-images.md)；日常升级尚待主对话审查，不以源码测试代替真实升级。

019_enhancements.sql新增四张全局增强／切换／导入阶段冻结表，schema19共90表；001–018保持。新实例两个推荐关闭、译库开启；迁移前已有合法foundation账本的实例保留两个推荐开启，判据由迁移器使用事务前账本，不按资料数量推断。旧批次缺少冻结行时按两个推荐原本都需要处理，不重写旧完成历史。匿名18→19完整旧表／账本／素材保留、失败回滚及重开证据见[增强设置](../docs/enhancements.md)。本批没有执行日常实例升级。

020_tag_follows.sql新增tag_follows／tag_follow_epochs，events追加可空owner_account_id和索引；schema20共92表。001–019保持，旧全局事件原列与ID不变、owner为NULL，原retention触发器保持。关注初始无记录，不回填或自动关注。账号模型不改，迁移仍应用一致性私有快照和事务回滚；源码与匿名验证见[标签关注](../docs/tag-follows.md)，日常实例仍schema19，本批未执行真实升级。

021_gallery_highlight.sql新增默认关闭的独立高亮设置及只读连接表、来源metadata_id和账号列表索引，schema21共94表。001–020、旧三功能CHECK、transition外键与历史不改，不DROP／RENAME旧表、不生成连接或明文秘密。新feature通过原全局增强revision CAS映射即时切换。匿名20→21全旧表／账本／素材／设置与未完成transition保留、FK、失败回滚及重复打开见[本地画廊高亮](../docs/local-gallery-highlight.md)；本批不操作日常实例。

022_gallery_permanent_connections.sql仅重建连接叶表，让expires_at可NULL（长期）。复制所有旧列原值、保留索引和账号FK，001–021保持；旧期限／撤销／改密失效不复活，不对旧记录做期限回填。新创建由应用写NULL和独立短编号，协议2；schema22仍94表。日常于2026-11-01完成022升级，见[高亮交接](../docs/local-gallery-highlight.md)。

023_data_health.sql新增九张检查／冻结对象／问题／计划／修复步骤／全局维护／暂停任务／材料表及健康状态通知，schema23共103表。001–022原字节和checksum不变，不重建旧表或jobs，不自动检查、修复、关注或启用任何功能。023在首次应用前的最终定义包含冻结object_revision和source_bytes_read；2026-11-02日常实例已由应用创建私有一致性快照后升级该最终定义，不能只应用中间提交。契约、离线入口、匿名保留、回退及实际升级证据见[数据健康](../docs/data-health.md)。

024_instance_recovery.sql新增instance_operations／instance_state／instance_root_links三表及操作通知，源码schema24／106表。001–023保持原字节及账本；无既有业务回填，不恢复或执行任务。主库外恢复日志在普通Store.Open前持锁激活，空／旧／坏目标不先迁移。日常024未应用，见[备份恢复](../docs/backup-restore.md)。

025_accounts_authorization.sql扩展账号集合、逐库ACL／无秘密审计及持久后台身份，保留原管理员ID／摘要／session和原列；原singleton仅为历史槽位。父表重建在固定迁移连接暂关FK，事务内foreign_key_check及账号集合校验，结束恢复并核验FK。旧jobs／批次actor回填不改状态，不产生迁移通知；事件追加库归属及个人状态owner。周期扫描及增强transition绑定操作者。026_reading_state.sql新增个人阅读状态／会话／幂等请求，永久删除Edition级联清除。001–024字节与checksum保持；最终源码schema26／112表，日常仍schema24，首次实际升级由主对话协调。匿名24→25→26保留／回滚／重开及功能证据见[数据兼容](../docs/data-compatibility.md)。


[028_client_foundation.sql](028_client_foundation.sql)新增设备会话与逻辑实例身份／缓存世代。保持001–027原字节和账本，现有账号、网页会话、书库和个人资料不回填、不重建。设备凭证从新登录产生。恢复和回退激活清除设备会话并刷新缓存世代。源码升级验证使用匿名27→28数据库，已发布schema27的恢复包继续要求原构建与账本。见[客户端协议](../docs/client-foundation.md)。

[029_reading_session_intent.sql](029_reading_session_intent.sql)为阅读会话增加受约束的 `intent`，旧行默认 `start`，保留会话身份、幂等键和阅读历史。新 `restart` 由服务在同一事务创建会话并重置位置与完成状态。001–028 原字节和账本保持。参见[阅读同步契约](../docs/client-foundation.md#阅读写入继续与重新开始)。


[030_offline_client.sql](030_offline_client.sql)增加原文件下载准备、离线清单身份及个人操作回执。回执保留请求摘要、前置关系、收件时间与有效阅读事件时间。001–029 保持原字节和账本，原阅读状态和会话沿用。三张表随一致性备份保留，激活后旧世代记录隔离。见[下载与离线协议](../docs/client-offline.md)。
