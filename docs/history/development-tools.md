# 历史准备与显式回退工具

这些入口为旧试用实例和2026-10-22实例提升保留，不属于日常启动步骤。日常使用make dev-api／dev-web／status-dev／stop-dev，现行约束见[根指导](../../AGENTS.md)。本轮只整理说明，不执行这些历史工具或读取其私人输入。

| 入口 | 保留原因与边界 |
|---|---|
| make pilot-prepare／pilot-import | 固定隔离空目标准备及白名单导入；不是TreeC转换器，不能重新导入日常实例 |
| make pilot-reuse-cache | 核验固定输入和模型后离线迁入缓存，必须持有原隔离实例锁，不推理、不下载 |
| make pilot-build | 构建旧命名的隔离程序；含生产网页构建，本轮不执行 |
| make pilot-api／pilot-web 与 npm run dev:pilot | 已退役入口，只明确提示改用日常命令并失败退出 |
| make promote-pilot 与 promote-pilot.py --rollback | 已完成提升的可续做／显式回退工具，有私有阶段记录和数据一致性保护；不是通用备份恢复 |
| web/vite.rollback.config.mjs | 仅在显式回退完成后恢复旧端口，不参与日常Vite启动 |

提升工具、回退配置及匿名测试原路径保留；dev-config和dev-services仍检查提升阶段记录，防止中断状态下创建空实例或错误启动。具体协议和实际提升证据见[维护记录](../project-maintenance.md)。历史工具不能因名字包含pilot就整组删除。
