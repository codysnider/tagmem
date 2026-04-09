# Benchmark Report

Date: 2026-04-08

## Executive Summary

`tagmem` was benchmarked in Docker with GPU acceleration enabled across three local embedding models:

- `all-MiniLM-L6-v2`
- `bge-small-en-v1.5`
- `bge-base-en-v1.5`

All runs used the same benchmark corpora and the same Dockerized execution workflow.

Main conclusion:

- `bge-small-en-v1.5` is the best overall default for GPU-backed local operation.
- `bge-base-en-v1.5` improves `LongMemEval` and `LoCoMo` slightly, but costs materially more runtime and indexing overhead.
- `all-MiniLM-L6-v2` remains the strongest throughput-first fallback.

## Cross-Benchmark Comparison

| Model | LongMemEval R@5 | LongMemEval Time | LoCoMo Avg Recall | LoCoMo Time | MemBench R@5 | MemBench Time | ConvoMem Avg Recall | ConvoMem Time | Add Avg ms | Search Avg ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `all-MiniLM-L6-v2` | 0.982 | 14.4s | 0.915 | 896.1s | 0.778 | 995.8s | 0.931 | 10.2s | 1.148 | 0.618 |
| `bge-small-en-v1.5` | 0.990 | 22.4s | 0.941 | 1642.8s | 0.804 | 1816.9s | 0.898 | 18.9s | 1.161 | 0.583 |
| `bge-base-en-v1.5` | 0.992 | 44.1s | 0.949 | 1696.2s | 0.802 | 1877.9s | 0.920 | 19.3s | 2.369 | 0.635 |

## Best Model By Benchmark

| Benchmark | Best Quality | Fastest |
|---|---|---|
| LongMemEval | `bge-base-en-v1.5` | `all-MiniLM-L6-v2` |
| LoCoMo | `bge-base-en-v1.5` | `all-MiniLM-L6-v2` |
| MemBench | `bge-small-en-v1.5` | `all-MiniLM-L6-v2` |
| ConvoMem | `all-MiniLM-L6-v2` | `all-MiniLM-L6-v2` |
| Perf add/search | `n/a` | `all-MiniLM-L6-v2` |

## Recommendation

### Default GPU model

Use `bge-small-en-v1.5` as the default embedded model for Docker/GPU execution.

Why:

- strong `LongMemEval` improvement over MiniLM
- best `MemBench` score in the matrix
- much faster than `bge-base-en-v1.5`
- acceptable `LoCoMo` improvement while keeping runtime practical

### Alternate modes

- Use `all-MiniLM-L6-v2` for maximum throughput.
- Use `bge-base-en-v1.5` only when the slight gains on `LongMemEval` and `LoCoMo` are worth the extra runtime and memory.

## Published Baseline Comparison

The publicly cited MemPalace raw baseline on LongMemEval is:

- `Recall@5 = 0.966`
- `Recall@10 = 0.982`
- `NDCG@10 = 0.889`
- approximately `~300s` on Apple Silicon

All three `tagmem` GPU runs beat that quality baseline comfortably.

## Raw Data

Raw benchmark JSON outputs are included under:

- `raw/all-MiniLM-L6-v2/`
- `raw/bge-small-en-v1.5/`
- `raw/bge-base-en-v1.5/`

See also:

- `CHARTS.md`
- `MEMPALACE-COMPARISON.md`
