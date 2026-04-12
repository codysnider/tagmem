# Methodology

## Scope

These benchmarks were executed for the local embedded vector path only, using Docker with GPU acceleration.

Models tested:

- `all-MiniLM-L6-v2`
- `bge-small-en-v1.5`
- `bge-base-en-v1.5`

Benchmark sets:

- `perf`
- `longmemeval`
- `locomo`
- `membench`
- `convomem`
- `FalseMemBench`

## Execution Environment

- All runs executed inside `docker/docker-compose.yml`
- GPU exposed via NVIDIA container runtime
- Persistent XDG and dataset directories mounted from `TAGMEM_DATA_ROOT` on the host.
- Commands invoked through `just` wrappers and scripts in `scripts/cmd/`

## Commands Used

### Prepare datasets

```bash
cd /path/to/tagmem
just datasets
```

### Full suite per model

```bash
TAGMEM_EMBED_MODEL=all-MiniLM-L6-v2 just bench-suite
TAGMEM_EMBED_MODEL=bge-small-en-v1.5 just bench-suite
TAGMEM_EMBED_MODEL=bge-base-en-v1.5 just bench-suite
```

### LongMemEval only per model

```bash
TAGMEM_EMBED_MODEL=all-MiniLM-L6-v2 just bench-longmemeval
TAGMEM_EMBED_MODEL=bge-small-en-v1.5 just bench-longmemeval
TAGMEM_EMBED_MODEL=bge-base-en-v1.5 just bench-longmemeval
```

### Release guardrail

```bash
just release-check
```

This command runs focused Go tests and a guarded `LongMemEval` rerun for `bge-small-en-v1.5`, then compares the result against `benchmarks/guards/longmemeval-bge-small-en-v1.5.json` with a `0.01` tolerance on the tracked quality metrics.

## Dataset Sources

### LongMemEval

- Source URL:
  - `https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_s_cleaned.json`
- SHA256:
  - `d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442`

### LoCoMo

- Source repo:
  - `https://github.com/snap-research/locomo.git`
- File used:
  - `data/locomo10.json`
- SHA256:
  - `79fa87e90f04081343b8c8debecb80a9a6842b76a7aa537dc9fdf651ea698ff4`

### MemBench

- Source repo:
  - `https://github.com/import-myself/Membench.git`
- Dataset path:
  - `MemData/FirstAgent`
- Commit:
  - `f66d8d1028d3f68627d00f77a967b93fbb8694b6`

### ConvoMem

- Source dataset:
  - HuggingFace `Salesforce/ConvoMem`
- Retrieval during run:
  - downloaded and cached automatically to `${TAGMEM_DATA_ROOT}/datasets/convomem_cache`

### FalseMemBench

- Source project:
  - standalone benchmark project maintained outside the main repo
- Published artifacts:
  - copied into `benchmarks/raw/adversarial/`
- Compared measured systems currently include:
  - `tagmem`
  - `BM25`
  - `MemPalace raw-style`
  - `Contriever`
  - `Stella`

## Embedded Runtime Details

- Embedded provider: `embedded`
- Default GPU model after evaluation: `bge-small-en-v1.5`
- Execution provider: `CUDA`
- Runtime library path pattern:
  - `${TAGMEM_DATA_ROOT}/xdg/data/tagmem/models/<model>/runtime-cuda/libonnxruntime.so.1.24.1`

## Notes on Repeatability

- Raw outputs in `raw/` are copied verbatim from the benchmark run artifacts.
- Docker image definition is versioned in the repo.
- The host GPU workload may affect exact timing numbers.
- The `ConvoMem` benchmark downloads cached files from HuggingFace; the cache directory should be preserved for exact reruns.
