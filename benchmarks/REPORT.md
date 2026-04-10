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
| `bge-small-en-v1.5` | 0.990 | 23.0s | 0.941 | 1633.6s | 0.804 | 1775.2s | 0.898 | 18.1s | 1.120 | 2.220 |
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

## Adversarial Comparison

`tagmem` was also compared against a standalone adversarial distractor benchmark designed to stress ranking under conflicting or near-miss memories.

| System | Cases | Recall@1 | Recall@5 | MRR |
|---|---:|---:|---:|---:|
| `tagmem` | 500 | 0.8860 | 1.0000 | 0.9430 |
| MemPalace raw-style | 500 | 0.6600 | 1.0000 | 0.8193 |

Interpretation:

- Both systems now saturate top-5 recall on the current adversarial dataset.
- `tagmem` maintains a large advantage on top-1 ranking quality and mean reciprocal rank.
- For agent memory, this indicates `tagmem` is more likely to surface the right answer near the top even when distractors are semantically close.

## Raw Data

Raw benchmark JSON outputs are included under:

- `raw/all-MiniLM-L6-v2/`
- `raw/bge-small-en-v1.5/`
- `raw/bge-base-en-v1.5/`
- `raw/adversarial/`

See also:

- `CHARTS.md`
- `MEMPALACE-COMPARISON.md`
