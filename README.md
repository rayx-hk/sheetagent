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

## RAG (Few-Shot Injection) Policy

SheetAgent includes a RAG module (`internal/rag/`) that accumulates successful task solutions and injects them as few-shot examples into future prompts.

**RAG is a product-mode feature only and MUST be disabled during benchmark evaluation.**

| Mode | `rag.enabled` | Behavior |
| :--- | :---: | :--- |
| **Benchmark** (`cmd/bench`) | `false` (enforced) | Bench binary **refuses to start** if `rag.enabled=true`. Startup log prints `RAG status: DISABLED (benchmark mode)`. |
| **Production** (`cmd/agent`) | `true` | RAG loads the store, retrieves few-shot examples, and accumulates new solutions. |

### Why?

RAG solutions are indirectly validated by golden answers (OJ Judge AC verdict). Injecting them back into the same benchmark constitutes **test-set contamination** — the agent would be answering questions it has already "seen the answer to". This makes benchmark scores non-credible.

### Enforcement layers

1. **Config**: `config.yaml` has `rag.enabled: false` with inline documentation.
2. **Hard gate**: `cmd/bench/main.go` checks `cfg.RAG.Enabled` and returns a fatal error if true.
3. **Store lock**: `rag.NewBenchLockedStore()` returns a no-op store that rejects `Add()`, `Save()`, and returns empty from `Retrieve()`.
4. **Reporting**: When publishing benchmark results, always report **cold-start** (no RAG) scores. If warm-start (with RAG) scores are reported, they must be clearly labeled as such.

## Related Repositories

- [sheetagent-design](../sheetagent-design/) — Design docs, UI/UX, datasets
- [sheetagent-tools](../sheetagent-tools/) — Dataset collection/cleaning/Research Radar tools


## 🏆 Benchmark Scores

We continuously evaluate SheetAgent against the **SpreadsheetBench** dataset.

| Version | Dataset | Pass@1 Score | Model | Status |
| :---: | :---: | :---: | :---: | :---: |
| **v0.5.0** | `400 Verified` | **87.6 / 100** | Claude Sonnet 4.5 | ![87.6%](https://img.shields.io/badge/Pass@1-87.6%25-brightgreen?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.3.1** | `400 Verified` | **96.7 / 100** | Claude Sonnet 4.6 | ![96.7%](https://img.shields.io/badge/Pass@1-96.7%25-brightgreen?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.3.0** | `400 Verified` | **94.7 / 100** | Claude Sonnet 4.6 | ![94.7%](https://img.shields.io/badge/Pass@1-94.7%25-brightgreen?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.2.0** | `400 Verified` | **73.7 / 100** | Claude Sonnet 4.6 | ![73.7%](https://img.shields.io/badge/Pass@1-73.7%25-green?style=for-the-badge&logo=microsoftexcel&logoColor=white) |
| **v0.1.0** | `400 Verified` | **65.8 / 100** | Claude Sonnet 4.6 | ![65.8%](https://img.shields.io/badge/Pass@1-65.8%25-yellow?style=for-the-badge&logo=microsoftexcel&logoColor=white) |

> *CodeAct Single-Agent with MCTS + Adaptive SOP architecture. v0.5.0 scored on Claude Sonnet 4.5. Current SOTA on SpreadsheetBench 400 Verified (prev. Nobie Agent 91%).*
