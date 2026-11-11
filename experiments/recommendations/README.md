# 独立推荐算法试验

当前[V3阶段](../../docs/recommendation-v3.md)已完成120对用户标注、冻结及唯一留出评估，结果与命令见[V3分析](../../docs/recommendation-analysis-v3.md)。较强建议零误报但召回偏低，未证明全面优于固定V2，暂不接入应用。`study.py prepare-v3`默认输出run-v3，复用固定缓存、不运行模型，已有包拒绝覆盖；离线review.html每次导出完整120条`labels.user.v3.json`。标注线协议保留四类关系v2格式，通过独立pack_id区分新包；原300对和旧90对今后均作为开发数据，V2历史评估保留。analyze_v3已冻结，不重跑已完成evaluate；review_analysis_v3只重渲染既有报告。

本工程只评估版本/系列建议，不接入 Go、API、管理网页、迁移工具或业务库。真实输入、标题、标签、ID、图片、特征和逐条结果都在 `.runtime`，不得提交 Git。来源只允许用户提供的 `.runtime/tests/fixtures/treec-recommendations-input/`；数据库使用 `mode=ro + query_only` 和一个读事务，不读取凭据、源漫画、向量或旧绝对路径指向的目录。SQLite WAL 模式可能创建副本边车文件，不写业务记录，不使用 `immutable=1`。

[四类关系300对新样本](../../docs/recommendation-labeling-v2.md)的校准、冻结及一次留出评估已完成，结果见本文末节及[分析报告](../../docs/recommendation-analysis-v2.md)。旧same已确认混合作品／系列关系，下面v1的质量统计只作历史材料，不得据此判断模型效果。资源／转换证据仍有效。

新候选使用`study.py prepare-v2`，输出run-v2，按需复用旧缓存并补算。新页面分10批，每次导出完整300条`labels.user.v2.json`。明确指定`--output .runtime/tests/runs/recommendations/run-v2`才能校验新标注，默认validate-labels仍指向旧run-v1；v2包不使用旧`study.py score/evaluate`，独立分析器的留出评估已单次完成并禁止重复。

首次输入副本的复制是否在停写后完成仍未确认；`quick_check` 与外键检查仅证明本次读取的结构检查通过，不证明复制过程的一致性。源信息通过白名单导出冻结，封面仅将 `Covers/<两位小写摘要>/<40位摘要>.jpg` 映射到副本内部，拒绝路径穿越、符号链接和非普通文件。

## 历史v1候选册与标注

已交付 `.runtime/tests/runs/recommendations/run-v1/review.html`：版本/系列各40对，各20正候选、15困难负候选、5普通负候选。现有分组只用于选样，未作为真值。困难样本覆盖同画廊不同原组织、同作者不同显式卷和相同缓存封面；这些仍须人审。画廊ID从不计入同作品匹配分。系列正候选取不同逻辑条目，排除版本组内部两成员。

双击 HTML 即可离线核对，选择 `same` / `different` / `uncertain`、填写理由并点击“导出标注JSON”。浏览器本地草稿仅为便利功能；JSON导出/导入才是可移植续填材料。无远程脚本、图片或请求。建议将导出文件保存为同目录 `labels.user.json`；分批标注允许保留未填项。确认保存完成后再运行：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 experiments/recommendations/study.py validate-labels --labels .runtime/tests/runs/recommendations/run-v1/labels.user.json
```

`run-v1` 已在用户开始标注前固定，属于探索性 pilot：采样规则为旧 `title-structure-v1`、50/50族划分；不修改其 pack ID，也不据此宣布正式70/30评测或最终阈值。当前打分使用独立的规则v2重新计算，不复用旧采样分。新 `prepare --output .runtime/tests/runs/recommendations/<新运行名>` 使用70/30配额（每类28/12对）和版本化族划分；只在需要下一轮样本时显式执行。组织关系与精确题名核心构成保守已知族，按族固定分配，跨侧已知族交集必须为0；未知语义同族仍需人审，标注发现后应合并族并建立新版本划分，不能在测试侧调阈值。

## 独立环境与模型

基础准备只需 Python3.12 标准库：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 experiments/recommendations/study.py prepare
```

输出目录默认 `.runtime/tests/runs/recommendations/run-v1`，已有候选包拒绝覆盖。私有输出目录0700、文件0600。环境固定在 `.runtime/cache/recommendations/venv`，模型在 `.runtime/cache/models/recommendations`，不安装系统/全局包。

`requirements-runtime.txt` 固定 NumPy/Pillow/ONNX Runtime/tokenizers；`requirements-reference.txt` 的 PyTorch/ONNX 仅用于转换比对，NAS特征工作器不需要它们。`requirements-lock-macos-arm64.txt` 包含本次 Python3.12/macOS ARM64 实际解析的全部轮子版本及SHA256，只适用于该平台。Linux/x86部署前需为目标平台另锁轮子，不能把本锁当成已验证的Linux环境，也不能在Linux直接安装默认CUDA版PyTorch。

本次实际建立环境的命令如下；首次依赖下载需访问官方 PyPI，后续安装无网并验证hash：

```sh
python3 -m venv .runtime/cache/recommendations/venv
.runtime/cache/recommendations/venv/bin/python -m pip --isolated download --cert /etc/ssl/cert.pem --index-url https://pypi.org/simple --only-binary=:all: --dest .runtime/cache/recommendations/wheels --cache-dir .runtime/cache/recommendations/pip-cache --requirement experiments/recommendations/requirements-runtime.txt --requirement experiments/recommendations/requirements-reference.txt
.runtime/cache/recommendations/venv/bin/python -m pip --isolated install --no-index --find-links .runtime/cache/recommendations/wheels --require-hashes --requirement experiments/recommendations/requirements-lock-macos-arm64.txt --cache-dir .runtime/cache/recommendations/pip-cache
PYTHONDONTWRITEBYTECODE=1 python3 experiments/recommendations/study.py fetch-models
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/study.py convert-sscd
```

下载只使用官方公开地址、正常TLS和不加载用户curl配置的匿名请求，不读取HF token，不上传私人输入。只有 `fetch-models` 可以联网；特征工作器直接加载本地文件，禁用隐式token/遥测，强制Hub/Transformers离线；缓存缺失拒绝执行，不自动补下载。下载支持信号取消并终止子进程，不持续重试失败请求。

固定模型与预处理：

| 模型 | 来源与固定方式 | 输入和输出 |
|---|---|---|
| SSCD `sscd_disc_mixup` | [官方仓库](https://github.com/facebookresearch/sscd-copy-detection)、MIT；源码参考提交 `95902662f2217a5f4aa45f2a3fc70a01dfd3b66a`。官方TorchScript静态URL没有权重revision，固定首次下载本地SHA256 `9f26bd4c848cc19b73d2ae92eea6e04886f61a7b764ceb7a13aeee62e6a6db56`，不是官方签名。 | RGB/Pillow bilinear直接缩放320×320（不裁剪），ImageNet mean/std；512维L2。本地导出opset17，三个匿名输入与官方TorchScript比对。 |
| 多语言MiniLM | [官方模型](https://huggingface.co/sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2)，Apache-2.0；revision `e8f8c211226b894fcb81acc59f3b34ba3efd5f42`；官方FP32 ONNX SHA256 `10f7a088420252b26caf819236ca2c9d2987afd0fc06fec7553b542a5655a05a`（匹配官方LFS元数据）。 | 优先日文原题名，否则原题名；本地tokenizer，128 token截断、attention-mask均值池化、L2；384维。启动校验官方sequence/pooling配置并将其指纹纳入缓存。 |

完整下载文件的来源、大小、精确SHA256和摘要来源保存在私有 `manifest.json` 及公开的 `model-artifacts.lock.json`。SSCD转换产物只有参考比对通过才原子发布；转换误差不是漫画识别准确率。输入中的现有封面是缩略图，不能证明整本内容相同。

## 特征、比较与资源

两模型顺序加载，每个命令为独立进程；CPU provider、batch1、线程1，无GPU/Apple Vision/云服务。部署候选是通用FP32 ONNX，不选绑定AVX2的预设量化权重；N5105/SSE4.2真实运行尚未验证。

```sh
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/study.py features --algorithm phash
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/study.py features --algorithm sscd
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/study.py features --algorithm minilm
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/study.py score
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/study.py evaluate
```

默认只计算80对涉及的去重书籍/封面；`--limit N` 用于独立小批验证。缓存以输入SHA256、算法/预处理版本和模型文件指纹共同寻址，每项原子落盘并校验feature摘要；同输入复用，变化失效。SIGINT/SIGTERM在单项推理结束后检查，已发布缓存保留，重复同命令续算。独占工作器锁拒绝并行运行。图片上限10MiB/2000万像素，坏图、超限和损坏缓存记录类别，不伪装有效特征；RSS初始目标2GiB，在加载后及每项后检查，属于软预算而非操作系统硬限制。

版本比较题名结构规则、DCT感知hash和SSCD；系列比较结构规则与MiniLM。规则v2保留固有数字和未识别括号内容，仅去已识别版本修饰；显式volume/chapter区分类型，跨卷/话阻断版本建议。映射仍有限，未知表达不能假装已正确理解。打分为可解释匹配信号，不是概率；规则/图像/语义各自top5候选取并集，保留冲突及理由，不沿相似边传递扩组。系列建议不把同版本组两成员当成两个逻辑条目。

`scores.json`、`descriptive-summary.json` 仅描述当前候选样本。`evaluate` 没有显式导入的有效人工标注时拒绝执行；uncertain/未填不是真值。当前pilot有标注后也只报告描述性分布，不调最终阈值、不声称全库召回率。正式评测另需70/30族隔离及人工族审查。Mac时间/RSS不替代N5105实测；下载、冷启动、推理和缓存耗时分别记录。

## 本轮实际结果（Mac ARM64，2026-10-16）

80对涉及148本书，去重144封面和136题名。两任务分别收到34 same / 5 different / 1 uncertain，78条确定标注。原册和原 `labels.user.json` 保留；只读 `review-outliers.html` 展示21对排名遗漏、低分或规则冲突及当前人工判断，不是已确认误判。该子集不提供标注导出；如需修正，回原完整候选册导入原JSON，修正后导出完整80条。

| 算法 | 冷启动 | 去重输入推理 | 冷跑总计 | 冷跑峰值RSS | 全部148项缓存重跑总计 |
|---|---:|---:|---:|---:|---:|
| pHash，144封面 | 0.0996s | 0.2745s | 0.5822s | 99,106,816B | 0.2252s |
| SSCD，144封面 | 0.2405s | 25.2938s | 28.6498s | 332,873,728B | 2.9970s |
| MiniLM，136题名 | 1.1266s | 2.2020s | 5.6814s | 1,056,768,000B | 3.0436s |

三个算法错误0；缓存重跑各148命中、0项重新推理。缓存重跑仍验证模型文件、加载工作器和逐项输入，因此耗时不是纯内存查找。MiniLM缓存重跑峰值RSS为1,243,348,992B，仍低于2GiB软预算。冷跑证据独立保存在 `resources-*-cold.json`，重跑保存在 `resources-*.json`。SSCD转换参考进程峰值893,190,144B，最大匿名数值误差3.184e-6，低于2e-4容差；转换不是部署推理或识别准确率。

当前148本候选池中，34个已标same版本对的top5覆盖：规则33、pHash23、SSCD27、多路并集33；漏掉的1对被卷/话规则保护阻断，需核对解析依据。34个已标same系列对：规则34、MiniLM31、并集34。这仅是已知标注样本在小候选池中的排名覆盖，不能外推为全库召回率。

困难候选按规则分优先选取，对规则基线有利；这些数字不能证明规则优于SSCD或MiniLM，也没有全面覆盖图像相近而题名不同的例子。后续应从实际算法多路候选补入真正困难的人工负例。

图像和语义分数均存在重叠：版本same的SSCD最低0.0402、different最高0.2215；版本pHash对应0.3968/0.5238；系列MiniLM对应0.6321/0.6512。规则在本轮分数上分开了两类，但全部different恰来自普通负候选：版本15个困难负候选全部标same，系列14个same/1个uncertain。因此不能用这批简单负例宣布95%可靠，也没有据此确定最终阈值。下一轮需真正困难的人工different例与族审查，正式样本至少100对/任务、固定70/30划分后再校准和留出评估。

`study.py benchmark` 使用固定种子的3000项匿名随机特征，分块32查询、top5；512/384维检索分别0.0752s/0.0737s，峰值RSS50,397,184B/55,050,240B，植入的精确相邻项均找回。这只是NumPy分块向量检索，不是 `features.score` 的Python逐对规则/冲突流水线，也不含模型推理、真实书籍或全库召回评测；不能视为3000本完整处理时间，不能替代N5105运行。

## 匿名验证

```sh
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python -m unittest discover -s experiments/recommendations -p 'test_*.py'
```

测试只构造匿名元数据/图片，覆盖题名数字与序数类型、同画廊非证据、逻辑条目取样、70/30无已知族泄漏、路径/符号链接、取消原子发布和锁、标注schema及空标注拒绝、HTML转义、图片异常/上限和特征缓存。没有运行应用迁移、生产构建、浏览器、Go/web测试、Docker、Ubuntu或NAS；不触碰日常实例与业务服务。

## v2校准与单次留出分析

用户完成四类标注后，使用独立的`analyze_v2.py`；不复用v1的混合same标签，也不运行旧score/evaluate。协调者先只开放210对校准记录、2823本白名单书籍和现成特征，测试配对及答案保持封存。详见[v2分析契约](../../docs/recommendation-analysis-v2.md)。

```sh
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/analyze_v2.py calibrate
```

本命令只读取私有`run-v2/analysis/calibration.json`、`analysis-books.json`、`input-audit.json`及上层三个features JSON；不读源数据库、图片、原候选册、完整标注或组织关系。标题解析保留未知括号和固有数字，只清理明确修饰及标签文字直接匹配的署名，或重复、共同创作者标签支持的`[Group (Artist)]`结构。单纯“同作者、重复方括号”不能推断署名。卷／话／上中下有明确类型；裸数字、有限罗马数字和`(2)`只提供歧义序号信号，不作为同作品的硬冲突。未知格式继续复核。

版本与系列为互斥动作；同作者不是同系列，封面相似不是整本相同。标题为主要信号，SSCD／pHash／MiniLM为辅助；另有严格的SSCD、语义、共同创作者、页数兼容联合路线，允许找回部分题名损坏的版本。明确作者冲突、卷话类型歧义、疑似合集／部分重叠、竞争关系或证据不足进入defer；缺标签不当作冲突，语言／颜色不作否定。不会沿相似边自动扩组。

现有规则／pHash／SSCD／MiniLM分别报告固定约定阈值基线；补充在校准集有限6阈值中选择的单信号对照，不能据此声称优于最优单模型。融合仅比较36组参数，两动作都须达到校准点precision≥95%且各至少10个预测正例，再最大化两动作recall之和；没有可行配置时使用预先固定的保守fallback并明确未达目标。校准结果不能当留出或总体质量。

主指标中same_work仅为版本正例、same_series仅为系列正例；uncertain及独立部分重叠标记排除，原记录与预测保留单列。defer正例仍计入FN，同时报告自动覆盖率。Wilson95%按配对计数，未校正同族相关性，不能证明总体或独立家族的95%精确率。top5／10／20在全部2823本池中检索，任一端找到另一端算覆盖，只表示已标正例覆盖，不能冒充全库真实召回；候选排名与最终动作保护分开报告。多路union表示每条路线各取topK后并集，本轮5条路线每query最多5K个候选，不能称为总量限制为K的最终推荐列表。

命令输出0600私有`calibration-report.json`和`frozen-policy.json`；冻结包含全部参数、代码／输入SHA256、模型及特征版本、运行时、排除政策和defer定义。源码、参数、输入或运行时变化会拒绝留出评估，已冻结时拒绝再次校准。协调者审查冻结后，才提供同目录`heldout.json`（`recommendation-heldout-v2`），顶层含`pack_id`、`feature_input_id`、`frozen_policy_sha256`及与校准相同字段的`rows`。然后仅运行一次：

```sh
PYTHONDONTWRITEBYTECODE=1 .runtime/cache/recommendations/venv/bin/python experiments/recommendations/analyze_v2.py evaluate
```

校验释放指纹、所有冻结输入及族／物理书籍隔离通过后，建立排他的一次性评估标记；缺文件或不匹配释放不消耗机会。测试结果不反向修改算法／阈值。逐项误差、defer、缺特征和标签矛盾只进入私有JSON，不覆盖完整用户标注。所有分析单进程、请求单线程、零模型加载／推理，记录缓存分析及单对决策开销；Mac结果不替代N5105运行验收。

## v2本次结果：留出目标未达（2026-10-16）

协调者认可的冻结SHA256为`45e27a79a7d945e7d830d6b557156b5b914a550e0a6066ed1e5f9025d15caeb9`，版本／系列阈值`.90/.72`、标题／图像权重`.86/.04`。协调者完成唯一一次留出评估；评分代码、参数和标签没有依据测试结果再调整。此前未认可的候选冻结及审查修复记录在私有`analysis/candidate-v1/`，只修复测试实际命中、负图像支持截断和候选并集解释，参数及校准指标不变。

| 阶段／动作 | 主样本／正例 | TP／FP／FN | precision | recall（含defer遗漏） | precision Wilson95% |
|---|---:|---:|---:|---:|---:|
| 校准版本 | 202／60 | 56／0／4 | 100.00% | 93.33% | 93.58–100.00% |
| 校准系列 | 202／63 | 39／2／24 | 95.12% | 61.90% | 83.86–98.65% |
| 留出版本 | 88／24 | 20／2／4 | 90.91% | 83.33% | 72.19–97.47% |
| 留出系列 | 88／28 | 18／2／10 | 90.00% | 64.29% | 69.90–97.21% |

冻结融合策略在留出集的两个动作均未达到95%点精确率目标。校准100／202（49.50%）、留出45／88（51.14%）进入defer，自动覆盖率分别50.50%／48.86%；不能只在未defer记录上计算主recall。区间按配对计数，未校正族内相关性；已知族、书籍及完全相同封面隔离通过，未知语义族仍未全部人工审查，选样也不是总体随机抽样。

留出集固定约定阈值对照如下；每项是独立二分类参考，未施加融合的互斥／完整冲突保护，不能当作已交付自动整理策略：

| 方法／阈值 | 版本TP／FP／FN | 版本precision／recall | 系列TP／FP／FN | 系列precision／recall |
|---|---:|---:|---:|---:|
| 旧规则 .90 | 16／4／8 | 80.00%／66.67% | 7／16／21 | 30.43%／25.00% |
| pHash .90 | 19／0／5 | 100.00%／79.17% | 0／19／28 | 0.00%／0.00% |
| SSCD .70 | 22／1／2 | 95.65%／91.67% | 1／22／27 | 4.35%／3.57% |
| MiniLM .90 | 22／35／2 | 38.60%／91.67% | 20／37／8 | 35.09%／71.43% |
| 冻结融合 | 20／2／4 | 90.91%／83.33% | 18／2／10 | 90.00%／64.29% |

本次版本融合没有胜过SSCD：SSCD的留出点precision与recall均更高，pHash固定阈值也以较低recall得到0个误报。有限校准阈值对照仅将版本pHash选为`.95`，留出14／0／10；其余单信号没有满足校准目标的可行阈值，按预定规则保留固定fallback。本轮不能据测试集改选参数后重新报告一个主策略，也不能把内部字段名`improved`理解为效果更优。系列融合比旧规则／纯语义误报少，但仍有误报、漏报和较高复核比例。

全部2823本检索池中，冻结融合与每路线候选并集的已标正例覆盖为：

| 阶段／动作 | 融合top5／10／20 | 多路各取top5／10／20后并集 |
|---|---:|---:|
| 校准版本，60正例 | 59／59／59 | 60／60／60 |
| 校准系列，63正例 | 44／51／59 | 47／55／61 |
| 留出版本，24正例 | 24／24／24 | 24／24／24 |
| 留出系列，28正例 | 27／28／28 | 28／28／28 |

并集每query最多25／50／100个候选，不是总top5／10／20推荐。候选覆盖没有套用最终动作阈值和冲突保护，不能用全覆盖掩盖最终召回率，也不代表全库真实召回。完整规则、图像、语义各路对照均保存在私有报告。

留出版本2个FP都是真实same_series；4个FN中3个defer、1个误推系列。留出系列2个FP分别是真实unrelated和same_work；10个FN中8个defer、2个误推版本。剩余风险集中于作品／卷篇界线、署名冲突及相近标题结构。校准5个uncertain全部被预测为same_work，仍按原标注单列，不视作正确；校准4个部分重叠标记与其中1个uncertain交叠，排除记录共8条。留出另外2条部分重叠same_series均defer，排除记录共2条。不能将primary结果扩大为这些模糊关系已解决。

私有`posthoc-summary.json`只聚合误差类型、defer原因和候选覆盖；多条原因可同属一条记录，原因数不等于defer条数。逐项标签、预测、分数、冲突和复核依据仍在两个原报告，未改写300条用户标注。后续可研究以SSCD为版本候选主线、标题／页数作为冲突提示，以及更分层的系列标题结构；任何改动都需要新版本及新留出验证，本轮尚未实施。

本次48项实验匿名测试通过（23项为新分析定向测试）。校准完整缓存分析27.00s、峰值RSS279,281,664B；留出12.09s、290,816,000B，均为Mac ARM64、零模型加载／推理，低于2GiB软目标。报告中的`cached_pair_decision_median_proxy_us`实际为100轮决策的均摊时间，校准／留出约1.45／1.33µs，既不是实测中位数，也不含信号提取、特征生成或完整检索成本。未做N5105／AMD64／NAS部署验证；未改正式应用、schema、API、网页或服务状态，未运行Go/web构建、启动、网页UI、迁移或容器验收。

## V4同作品与预设版本标签（离线开发）

已完成的[V4约定与阶段记录](../../docs/recommendation-v4.md)复用420条已见标注，按作品族五折开发，先审计历史漏报，再验证新Edition寻找Work及保守预设标签。不改V2／V3冻结材料、不联网、不接正式应用。`v4_data.py prepare`仅校验已有白名单和特征并在新的`run-v4/data-reviewed`写入审计，命令和针对性测试见专项文档。

V4最终已见开发五折：较强103TP/3FP/8FN，合计108TP/8FP/3FN，补充单独5TP/5FP。前向382查询总top5/10/20已知正例覆盖108/111，未知候选不计对错；不是新留出或真实Work全库精度。`v4_run.py run`使用已审查stage2策略并原子发布新的final，`verify`只校验；`prepare-policy --policy-dir <新的run-v4子目录>`可显式复建已见开发策略，不覆盖旧产物。完整命令、标签保守表现、候选工作量和资源边界见专项文档，默认不再重复评分。
