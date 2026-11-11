# 后端实现

app 装配服务；appdata 管理实例锁与开发重置；config 校验配置；model 保存不依赖 HTTP／SQL 的业务类型；auth／library／metadata／organization／search／userstate／maintenance／recommendation／importflow拥有业务规则与仓储接口；comic 返回自己的归档条目并处理受限图片；provider 管理有界网络、加密设置和译库；imagecache 独立管理分片封面与容量；store 实现分领域 SQL 和事务；httpapi 映射输入输出及错误；webassets 嵌入生成的网页。

testfixture 仅供测试及匿名 fixtures 命令，正式服务不依赖它。textkey提供v1规范化和自然排序键。阅读等按后续需求建立，不预建空包。整体导航见 [仓库结构](../docs/repository-layout.md)，执行约束见 [AGENTS.md](../AGENTS.md)。

推荐Go适配通过有界协议调用runtime/recommendations的正式离线Python工作器，运行代码不导入experiments。recommendationpilot只支持历史隔离准备，不进入正式服务装配；入口及保留原因见[历史工具](../docs/history/development-tools.md)。
