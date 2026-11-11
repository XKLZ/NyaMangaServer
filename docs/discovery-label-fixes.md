# 同作品查找与版本标签修复

## 调查与证据边界

用户报告的历史任务在2823个Edition中完成565行、1,435,100对比较后失败。配对复用0，旧完整任务没有新版执行标识／逐Edition匹配指纹，不能直接伪造其兼容性。资源记录为8459次缓存命中、0次新增推理、0次模型加载；资料准备和配对重算不等于重跑模型。

旧失败记录只有discovery_failed，工作器stderr与内部错误已丢弃。仅用已有缓存只读重放停止行能够通过，未推理、未重跑全库、未改旧任务。本次不能追溯证明该次失败的精确触发原因。

匿名复现确认一个独立的设计缺口：工作器重启后内存配对池消失，旧执行器仍认为已准备好。现在缺池／池身份变化具有有限脱敏错误码，执行器重建配对池后只重试未提交行一次；再次失败暂停并保留检查点。运行中网页能力查询复用启动前已校验的能力信息，不触碰共享Python进程，避免HTTP取消破坏活动配对池。未知工作器原文、标题、路径或凭据均不转发到日志／网页。

另外修复了成员属性读取的RawMessage别名问题：不能先推断共享有效／自动字节数组，再把已保存JSON覆盖解码到该对象，否则自动值可能污染有效值。现在完整投影解码至新对象，仅没有投影时推断。匿名标签落库及成员属性回归覆盖此问题。

## 增量与诊断

- 基线选择允许completed／failed／cancelled任务，要求同书库及执行、证据、算法版本一致，优先已提交行数，其次新旧时间。每对仍用稳定Edition ID及完整特征指纹验证；缺失行、变化输入或未知结果不能复用。
- 失败／取消留下的完整行块可复用，负结果和硬冲突一并复用。当前任务写出独立完整检查点，执行代次与旧基线外键保护保留。没有可验证指纹的历史任务仍需补建。
- `GET /api/v1/work-discovery-jobs/{id}`增加baseline_state、failure_stage、failure_reason、failure_row及累积resources。无兼容基线、基线复用与用户主动完整重算明确区分。诊断复用现有summary_json，不新增SQL迁移。
- 网页分别展示资料登记／准备、特征缓存命中、实际推理、模型加载和复用／新增配对。未知旧错误明确标为未记录具体原因；新错误显示脱敏阶段和已提交行。

## 标签规则

当前规则为edition-preset-labels-evidence-v2，预设为原版、译本、无修、修正、彩色、黑白、DL、特别，自定义单独保留。每个维度最多一个可信结果；共同标签、语言差异或对方属性未知均不再阻止添加。

译本依据有效翻译属性、language:translated或明确翻译标记；原版仅接受明确未翻译或人工属性。颜色与修正依赖有效属性／明确标签及有限标题修饰；无标记不反推黑白、原版或修正。DL只接受独立DL／Digital／電子版等电子发行标记，DLsite、普通下载、ZIP及画廊URL不构成依据。特别版仅接受有限特别／增补版标记，“修订版”不等于审查修正。标签与完整标题修饰先规范化，人工明确未知及冲突不回填。

自动“其他”兜底取消。多版本组没有可靠建议时不更新现有标签；单版本Work仍清除组织区分用自动标签。人工模式、明确空数组和自定义文字不被自动覆盖。旧自动“其他”保留历史身份，主动生成可靠结果后仅替换自动集合；升级不批量处理。

新生成与待采纳方案的标签显示和实际落库使用同一Go规则。待采纳列表／详情批量读取当前属性来更新显示建议，保留原有输入指纹、组织身份、提交revision和命令；读请求不写回标签或方案，不因规则变化解除冲突校验。Python兼容推荐入口采用相同保守预设策略，实验历史源码和报告不修改。

## 验证与运行

定向Go测试覆盖配对池丢失／重复失败暂停、部分检查点复用、无变化全部复用、单本变化与新增、全量证据一致、取消／立即重启及陈旧写入、旧基线拒绝、标签保护与组织回归、HTTP契约和鉴权。相关并发测试使用race；相关包vet、依赖边界、OpenAPI、前端类型／ESLint／格式与定向单元检查执行。Python runtime匿名测试24项通过，没有调用模型或联网。

没有执行真实全库发现、真实标签生成、模型冷推理、规模、浏览器全套或部署验收。旧任务仍保留原失败状态；下次查找及已有组标签生成由用户点击启动。精确运行更新与数据审计结果在交付时补记，不把匿名测试当作本次真实任务已经恢复完成。

### 本次实际交付

源码提交为`b476983`，定向Go／HTTP契约检查通过；并发子集race及四个相关包vet通过。Python runtime24项匿名测试、前端2项标签／脱敏提示单元、TypeScript、修改文件ESLint／格式、OpenAPI及依赖边界通过。首次边界检查因未设置本地Go缓存而被沙箱拒写，指定仓库`.runtime/cache/go`和`go-build`后通过，没有修改全局缓存权限。

匿名四本场景：首次计算6对，无变化复用6对／新增0对；修改单本复用3对／新增3对；新增一本复用6对／新增4对。增量与完整计算证据相等。配对池丢失后重新准备一次，仅计算未提交行；重复失败暂停，取消后新任务复用已提交3对／新增3对。取消／恢复的旧执行代次不能提交。标签场景覆盖预览与实际落库同为“译本、DL”、共同彩色不再省略、人工清空／自定义保护、无可靠结果保留旧标签，以及有效／自动属性解码隔离。

仅构建Go服务一次，使用已有嵌入网页资产；运行标识为b476983。确认无queued／running／paused任务、两类发现任务无待重验后，正常SIGTERM停止并更新API8080，Vite5173 PID保持。停服前及恢复后66张表计数与行摘要全部一致，配置／秘密摘要一致；用户报告的旧失败任务保持原状态与565行检查点。未创建新会话、未生成标签或启动真实发现。私有审计及HTTP结果在被忽略的`.runtime/tests/results/discovery-label-fixes-20261023/`（目录0700、文件0600）。

API与Vite代理readiness200、Vite版本管理入口200、未认证任务接口401；这些启动检查不等于真实业务全流程验证。生产网页未重新构建，5173使用热更新源码；真实查找、真实标签生成及新页面浏览器手工体验等待用户执行。NAS、Docker、源文件和模型质量均未在本次重新验证。

复现定向检查（在仓库根目录，使用本地已安装依赖）：

```sh
GOTOOLCHAIN=local GOPATH="$PWD/.runtime/cache/go" GOCACHE="$PWD/.runtime/cache/go-build" go test ./internal/recommendation ./internal/store ./internal/httpapi -run 'Test(Preset|Worker|Capabilities|Discovery|Incremental|VersionManagement|VersionLabels|EditionLabels|Proposal|ResolvedSelection|WorkDiscovery|RecommendationHTTP)'
GOTOOLCHAIN=local GOPATH="$PWD/.runtime/cache/go" GOCACHE="$PWD/.runtime/cache/go-build" go test -race ./internal/recommendation ./internal/store -run 'TestCapabilities|TestDiscoveryRebuilds|TestDiscoveryRepeated|TestDiscoveryRetry|TestIncremental|TestRecommendationPause'
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python -B -m unittest discover -s runtime/recommendations -p 'test_*.py'
node --test web/tests/unit/version-labels.test.mjs
```
