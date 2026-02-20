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
