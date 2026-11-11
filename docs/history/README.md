# 保留的专项记录

本目录及下列链接用于追溯设计、实验和验收。旧文中的“当前／最新授权”、分支、端口、人员分工和已完成操作仅属于当时阶段，不能直接执行。现行规则见[根指导](../../AGENTS.md)，现行能力见[README](../../README.md)。

## 仍有用途的维护与功能记录

- [历史准备与回退工具](development-tools.md)及[实例维护](../project-maintenance.md)：启动保护仍引用提升记录，保留显式回退边界。
- [管理台验收](../admin-console-verification.md)、[Work模型](../work-edition-model.md)、[页面目录](../archive-manifest.md)和[属性译库](../properties-tag-suggestions.md)：保留专题约束及相应范围内的证据。
- 当前规则按[安全](../security.md)、[扫描](../ingestion.md)、[元数据](../metadata.md)和[搜索](../search.md)查阅，不以编号里程碑作为开发顺序。退役规划、验收和旧交接不再保存仓库副本，早期执行事实由维护者本地归档，未随首次公开快照发布。

## 冻结评测与试用（原路径保留）

- [推荐初始研究](../recommendation-study.md)、[标注v2](../recommendation-labeling-v2.md)、[v2分析](../recommendation-analysis-v2.md)、[V3候选](../recommendation-v3.md)、[V3分析](../recommendation-analysis-v3.md)、[V4](../recommendation-v4.md)。
- [系列V1](../series-recommendation-v1.md)、[系列V2](../series-recommendation-v2.md)、[同作品试用](../work-recommendation-pilot.md)、[系列试用](../series-recommendation-pilot.md)和[全库发现](../work-discovery.md)。冻结策略、输入与质量限制仍须遵守，不能把已见数据或配对指标当作新留出或整组质量。
- 独立源码保留在[实验目录](../../experiments/README.md)，正式工作器位于[runtime/recommendations](../../runtime/recommendations/README.md)，私人输入与输出不在Git中。

## Git追溯

退役文件从当前目录直接移除，不留下重定向或归档副本。私人旧历史不包含在公开main中，不提供依赖旧提交的运行命令。原始验收仅说明对应提交／环境的结果，不能据此宣称当前代码通过。
