# dataagent

Spreadsheet Agent 核心代码 — 基于 ByteDance Eino 的多 Agent 系统，冲榜 SpreadsheetBench。

## 快速开始

```bash
# 环境检查
make precheck

# 编译
make build

# 下载数据集
make download-dataset

# 跑 200 条样本测试
make bench-200
```

## 目录结构

```
cmd/
  agent/       # Agent 服务入口
  bench/       # 基准测试 CLI
  precheck/    # 环境预检
internal/
  agent/       # 四个核心 Agent (Coordinator/Informer/Coder/Evaluator)
  model/       # LLM 模型层 (Claude/Gemini/OpenRouter)
  sheet/       # Excel 解析 + SheetCompressor
  executor/    # Python 代码执行器 (embedded/docker)
  eval/        # OJ 评测引擎
  bench/       # 基准测试框架
  skills/      # v1.x.x 技能系统 (reserved)
config/        # 配置文件
docker/        # 执行沙箱 Dockerfile
scripts/       # 数据集下载等脚本
```

## 关联仓库

- [dataagent-design](../dataagent-design/) — 设计文档、UI/UX、数据集
- [dataagent-tools](../dataagent-tools/) — 数据集收集/清洗/Research Radar 等工具
