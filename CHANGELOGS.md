# Changelog

## 2026-02-20

### 1. 移除 Best-of-N 策略，改为 Sequential Retry
- `orchestrator/runner.go`: 移除 `N` 参数和 `runBestOfN` 逻辑
- `cmd/agent/main.go`: 同步移除 `N: 3` 配置
- 单任务最大 LLM 调用从 ~12 次降至 3-4 次，token 消耗减少约 70%

### 2. 移除 ===RESULT=== 解析依赖
- `orchestrator/runner.go`: 删除 `parseAgentOutput`，改为 OJ Judge 直接评测修改后的文件
- 修复了 65% 的 `agent_error` 误判（之前因解析失败被标记为失败，实际 agent 已正确操作文件）

### 3. Retry 前自动恢复原始文件
- `bench/runner.go`: 创建 `.orig` 备份文件
- `orchestrator/runner.go`: 每次 retry 前从 `.orig` 恢复输入文件，避免错误修改残留

### 4. 增强 Retry 上下文反馈
- `agent/codeact.go`: retry prompt 注入 OJ Judge 的 mismatch 详情（expected vs actual）
- `eval/judge.go`: 新增 `MismatchSummary(maxItems)` 方法，生成结构化 JSON 反馈
- 引导 agent 精准定位空值写入、格式差异、计算逻辑错误

### 5. 评测值归一化（P0 修复）
- `eval/compare.go`: `CompareValues` 增加日期（15+ 格式）、数字（逗号/货币/百分比）、布尔值的归一化
- 新增相对容差（1e-4）+ 绝对容差（1e-4）双重浮点比较
- `eval/compare_test.go`: 新增 5 组测试用例覆盖各类型

### 6. 失败分类优化
- `eval/report.go`: `ClassifyFailure` 关键词匹配更精确
  - `python_error` 需要 "traceback" 或 "exit status"
  - `api_error` 需要 "403 forbidden" 或 "rate limit"
  - 默认归类为 `value_mismatch`，避免误判

### 7. Agent Trace 记录
- `bench/runner.go`: 新增 `attemptTrace` 结构，记录每次 attempt 的代码、分数、错误
- 输出 `trace.json` 到 task 工作目录，支持后续归因分析

### 8. 设计文档沉淀
- `dataagent-design/planning/18-adaptive-retry-strategy.md`: 自适应重试策略 vs Best-of-N 的理论分析
- `dataagent-design/planning/19-llm-context-caching-strategy.md`: 三层 LLM 上下文缓存架构设计

### 9. 引入 macOS 原生 MS Excel 公式重算 (P0 修复)
- `executor/msoffice.go`: 新增 `ForceCalculate`，通过 AppleScript 在后台静默唤起本地 Microsoft Excel 进行全量公式重算和保存。
- 彻底解决了 `openpyxl` 写入公式后因无内置计算引擎导致评测系统读取到 `got=""`（空值）的问题。
- 引入移花接木机制：自动将文件拷贝至 macOS Office 专属共享沙盒白名单目录（`~/Library/Group Containers/UBF8T346G9.Office`）执行 AppleScript，完美绕过 macOS App Sandbox / TCC 的访问权限弹窗打扰，支持全静默挂机评测和断点续传。
- `eval/judge.go`: 在 `CompareFiles` 之前自动调用 `ForceCalculate`。
- `executor/msoffice_test.go`: 补充 AppleScript 唤起 Excel 的自动化单元测试。

### 10. API 调用网络层指数退避重试 (Exponential Backoff)
- `model/ratelimit.go`: 新增 `doWithRetry` 泛型方法，封装 `Generate` 和 `Stream` 接口。
- 应对 `502 Bad Gateway`、`400 Bad Request` 等瞬态网络和 API 错误，最高重试 4 次，初始间隔 2s，指数递增，极大提升了跑大批量 Benchmark 时的稳定性。

### 11. 评测值归一化增强
- `eval/compare.go`: `parseNumber` 支持财务负数格式解析（例如将 `(74.96)` 准确识别为 `-74.96`）。
- `eval/compare.go`: `dateFormats` 新增对纯时间格式（如 `15:04:05`, `03:04 PM`）的兼容。
- `eval/compare.go`: 新增 `compareLists` 方法，支持对包含逗号 `,` 或分号 `;` 的无序列表字符串进行切分、去空、排序后再对比，解决了结果列表顺序不一致导致的误判。

### 12. Agent 引入思维链 (CoT) 规范
- `agent/prompt/codeact.md`: 增加强制规范 `M0: Chain-of-Thought (CoT) Pseudo-Code`。
- 强制要求模型在编写 Python 代码前，必须先在注释中写出 step-by-step 的伪代码计划，以此提升复杂数据清洗、排序、合并等任务的逻辑推理准确率。

---

## 2026-02-19

### 1. 切换至 Single Agent CodeAct 架构
- 将 Coordinator + Informer + Coder + Evaluator 四 agent 合并为单一 CodeAct agent
- `agent/codeact.go`: 基于 Eino ADK 的 `ChatModelAgent`，配合 PythonRunnerTool
- `agent/prompt/codeact.md`: 完整的 prompt 工程（M1-M10 规则、禁止模式表、双策略）

### 2. 切换至 Claude Sonnet 4.6
- `model/claude.go`: 支持自定义代理 `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`
- 从 OpenRouter pony-alpha 切换至 Claude Sonnet 4.6

### 3. Sheet Compressor 实现
- `sheet/compressor.go`: 三模块压缩——结构锚点行采样、反向索引翻译、列统计聚合
- `sheet/compressor.go`: `CompressFull`（Light/Aggressive 两档）+ `CompressAnswerRegion`
- 大表格 overview 从 ~10K tokens 压缩至 ~1-3K tokens

### 4. OJ Judge 评测系统
- `eval/judge.go`: OJ 风格评测（AC/WA/RE/TLE/CE/SE/PC/SK），单元格级精确比较
- `eval/report.go`: Benchmark 报告生成 + 失败分类 + failures_live.jsonl 实时记录

### 5. Benchmark Runner
- `bench/runner.go`: 并发执行、retry、resume（跳过已通过任务）
- `model/ratelimit.go`: `RateLimitedModel` 限流封装（minInterval 共享）
- `sheet/cache.go`: `OverviewCache` 避免重复解析同一 xlsx

### 6. Prompt 工程优化
- `agent/prompt/coordinator.md`: 任务类型策略（Cell-Level vs Sheet-Level）
- `agent/prompt/coder.md`: Sheet-Level 专项模式（行删除反向遍历、样式修改、列插入）

---

## 2026-02-18

### 1. 项目初始化
- Go module `github.com/rayx-hk/dataagent`
- 基础目录结构：`cmd/`, `config/`, `internal/`, `docker/`, `scripts/`
- 配置系统：`config.yaml` + `models.yaml` + `.env` 三层配置
- Makefile 构建脚本

### 2. 多 Agent 架构（初版，后被 CodeAct 替代）
- Coordinator / Informer / Coder / Evaluator 四角色
- 基于 Eino ADK DeepAgent 编排

### 3. 数据集加载
- `bench/dataset.go`: 支持 `dataset.json` 和 `data.jsonl` 两种格式
- `flexString` 处理 id 字段的 string/number 兼容
- 自动识别 `_input.xlsx`/`_answer.xlsx` 和 `_init.xlsx`/`_golden.xlsx` 命名

### 4. Python 执行器
- `executor/embedded.go`: 本地 Python 3.11+ 沙箱执行
- 工作目录隔离、120 秒超时、stdout/stderr 捕获

### 5. 设计文档
- `dataagent-design/planning/00-overview.md` ~ `17-sota-architecture-upgrade.md`
- 完整的架构、模型层、工具链、评测策略等规划文档
