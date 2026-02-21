# sheetagent

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

- [sheetagent-design](../sheetagent-design/) — Design docs, UI/UX, datasets
- [sheetagent-tools](../sheetagent-tools/) — Dataset collection/cleaning/Research Radar tools


## 🏆 Benchmark Scores

We continuously evaluate SheetAgent against the **SpreadsheetBench** dataset.

| Version | Dataset | Pass@1 Score | Model | Status |
| :---: | :---: | :---: | :---: | :---: |
| **v0.3.1** | `400 Verified` | **96.7 / 100** | Claude Sonnet 4.6 | ![96.7%](https://img.shields.io/badge/Pass@1-96.7%25-brightgreen?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.3.0** | `400 Verified` | **94.7 / 100** | Claude Sonnet 4.6 | ![94.7%](https://img.shields.io/badge/Pass@1-94.7%25-brightgreen?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.2.0** | `400 Verified` | **73.7 / 100** | Claude Sonnet 4.6 | ![73.7%](https://img.shields.io/badge/Pass@1-73.7%25-green?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.1.0** | `400 Verified` | **65.8 / 100** | Claude Sonnet 4.6 | ![65.8%](https://img.shields.io/badge/Pass@1-65.8%25-yellow?style=for-the-badge&logo=microsoftexcel&logoColor=white) |

> *CodeAct Single-Agent architecture. Current SOTA on SpreadsheetBench 400 Verified (prev. Nobie Agent 91%).*
