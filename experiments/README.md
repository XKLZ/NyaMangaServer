# 推荐算法冻结实验

本目录保留推荐算法的独立冻结实现、策略、依赖和许可信息，不属于正式应用运行模块。现有匿名一致性测试会在独立进程引用其中纯函数，不能按“实验”二字整组删除，也不能让正式工作器导入实验命令。

正式离线工作器在[runtime/recommendations](../runtime/recommendations/README.md)，实验说明见[推荐评测](../docs/recommendation-study.md)。私人输入、标注、缓存、候选册和逐条结果不在Git中，实验通过不代表真实推荐质量已验收。

早期通用可行性实验已退役，不是构建或当前开发入口；原文件随Git历史保留。模块边界见[仓库结构](../docs/repository-layout.md)，执行规则见[Agent指导](../AGENTS.md)。
