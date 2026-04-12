# Benchmark Report

Date: 2026-04-11

## Executive Summary

`tagmem` was benchmarked in Docker with GPU acceleration enabled across three local embedding models:

- `all-MiniLM-L6-v2`
- `bge-small-en-v1.5`
- `bge-base-en-v1.5`

All runs used the same benchmark corpora and the same Dockerized execution workflow.

Release note:

- `just release-check` now reruns `LongMemEval` for `bge-small-en-v1.5` on the ONNX/CUDA path and enforces a `0.01` regression tolerance against a checked-in baseline.
- Latest release-check confirmation: `Recall@1 0.924`, `Recall@5 0.990`, `Recall@10 0.996`, `MRR 0.955`, `NDCG@10 0.951`, `Time 23.4s`.

Main conclusion:

- `bge-small-en-v1.5` is the best overall default for GPU-backed local operation.
- `bge-base-en-v1.5` improves `LongMemEval` and `LoCoMo` slightly, but costs materially more runtime and indexing overhead.
- `all-MiniLM-L6-v2` remains the strongest throughput-first fallback.

This report is organized around two evidence classes:

- **Measured by us**: results produced directly from the benchmark harnesses and raw artifacts in this repository
- **Source-reported references**: external values we did not independently reproduce

## Measured By Us

### Cross-Benchmark Comparison

These table values are the published suite snapshot from the raw artifacts in `benchmarks/raw/`. The release-check rerun above is a targeted guardrail pass rather than a full matrix refresh.

| Model | LongMemEval R@5 | LongMemEval Time | LoCoMo Avg Recall | LoCoMo Time | MemBench R@5 | MemBench Time | ConvoMem Avg Recall | ConvoMem Time | Add Avg ms | Search Avg ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `all-MiniLM-L6-v2` | 0.982 | 14.4s | 0.915 | 896.1s | 0.778 | 995.8s | 0.931 | 10.2s | 1.148 | 0.618 |
| `bge-small-en-v1.5` | 0.990 | 23.0s | 0.941 | 1633.6s | 0.804 | 1775.2s | 0.898 | 18.1s | 1.120 | 2.220 |
| `bge-base-en-v1.5` | 0.992 | 44.1s | 0.949 | 1696.2s | 0.802 | 1877.9s | 0.920 | 19.3s | 2.369 | 0.635 |

### Best Model By Benchmark

| Benchmark | Best Quality | Fastest |
|---|---|---|
| LongMemEval | `bge-base-en-v1.5` | `all-MiniLM-L6-v2` |
| LoCoMo | `bge-base-en-v1.5` | `all-MiniLM-L6-v2` |
| MemBench | `bge-small-en-v1.5` | `all-MiniLM-L6-v2` |
| ConvoMem | `all-MiniLM-L6-v2` | `all-MiniLM-L6-v2` |
| Perf add/search | `n/a` | `all-MiniLM-L6-v2` |

### Recommendation

#### Default GPU model

Use `bge-small-en-v1.5` as the default embedded model for Docker/GPU execution.

Why:

- strong `LongMemEval` improvement over MiniLM
- best `MemBench` score in the matrix
- much faster than `bge-base-en-v1.5`
- acceptable `LoCoMo` improvement while keeping runtime practical

#### Alternate modes

- Use `all-MiniLM-L6-v2` for maximum throughput.
- Use `bge-base-en-v1.5` only when the slight gains on `LongMemEval` and `LoCoMo` are worth the extra runtime and memory.

### FalseMemBench

`FalseMemBench` is a standalone adversarial distractor benchmark designed to stress ranking under conflicting, stale, or near-miss memories.

| System | Cases | Recall@1 | Recall@5 | MRR |
|---|---:|---:|---:|---:|
| `tagmem` | 573 | 0.8674 | 0.9983 | 0.9288 |
| BM25 | 573 | 0.6946 | 0.9930 | 0.8278 |
| MemPalace raw-style | 573 | 0.6632 | 0.9948 | 0.8154 |
| Contriever | 573 | 0.6527 | 0.9843 | 0.8049 |
| Stella | 573 | 0.4258 | 0.9791 | 0.6465 |

Interpretation:

- `tagmem` is the best measured system in this comparison on `Recall@1` and `MRR`, and remains strongest overall.
- BM25 is a serious baseline and outperforms the dense academic baselines tested here.
- MemPalace raw-style remains a useful measured reference point, but is materially weaker than `tagmem` on top-of-list ranking.
- This benchmark suggests that claim-aware reranking and value precision matter more than dense retrieval strength alone.

## Source-Reported Reference Values

These values are included for context only. They are not produced by the benchmark harnesses in this repository.

### LongMemEval reference values

| System | Recall@5 | Source status |
|---|---:|---|
| Mastra | 0.9487 | Project-reported |
| Hindsight | 0.914 | Project-reported |
| MemPalace raw baseline | 0.966 | Project-reported |

When possible, prefer the measured results in this repository over reference values.

## Raw Data

Raw benchmark JSON outputs are included under:

- `raw/all-MiniLM-L6-v2/`
- `raw/bge-small-en-v1.5/`
- `raw/bge-base-en-v1.5/`
- `raw/adversarial/`

See also:

- `MACHINE.md`
- `METHODOLOGY.md`
