# dataagent

Spreadsheet Agent core code — Multi-agent system based on ByteDance Eino, targeting SpreadsheetBench.

## Quick Start

```bash
# Environment check
make precheck

# Build
make build

# Download dataset
make download-dataset

# Run 400 sample tests
make bench-400
```

## Directory Structure

```
cmd/
  agent/       # Agent service entry
  bench/       # Benchmark CLI
  precheck/    # Environment precheck
internal/
  agent/       # Four core agents (Coordinator/Informer/Coder/Evaluator)
  model/       # LLM model layer (Claude/Gemini/OpenRouter)
  sheet/       # Excel parsing + SheetCompressor
  executor/    # Python code executor (embedded/docker)
  eval/        # OJ evaluation engine
  bench/       # Benchmark framework
  skills/      # v1.x.x skills system (reserved)
config/        # Config files
docker/        # Execution sandbox Dockerfile
scripts/       # Dataset download and other scripts
```

## Related Repositories

- [dataagent-design](../dataagent-design/) — Design docs, UI/UX, datasets
- [dataagent-tools](../dataagent-tools/) — Dataset collection/cleaning/Research Radar tools


## 🏆 Benchmark Scores

We continuously evaluate DataAgent against the **SpreadsheetBench** dataset.

| Version | Dataset | Pass@1 Score | Status |
| :---: | :---: | :---: | :---: |
| **v0.2.0** | `400 Verified` | **73.7 / 100** | ![73.7%](https://img.shields.io/badge/Pass@1-73.7%25-brightgreen?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.1.0** | `400 Verified` | **65.8 / 100** | ![65.8%](https://img.shields.io/badge/Pass@1-65.8%25-yellow?style=for-the-badge&logo=microsoftexcel&logoColor=white) |

> *Evaluated using Claude Sonnet 4.6 in the CodeAct Single-Agent architecture.*
