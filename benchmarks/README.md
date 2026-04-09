# Benchmarks

This folder contains publishable benchmark results for `tagmem`.

Contents:

- `REPORT.md`: executive summary and cross-model comparison tables
- `CHARTS.md`: GitHub-friendly charts for the benchmark matrix
- `MACHINE.md`: hardware and software environment details
- `METHODOLOGY.md`: exact commands, dataset sources, hashes, and reproducibility notes
- `MEMPALACE-COMPARISON.md`: direct comparison against MemPalace on comparable published metrics
- `raw/`: raw JSON outputs for each model and benchmark set

Current benchmark matrix:

- Models:
  - `all-MiniLM-L6-v2`
  - `bge-small-en-v1.5`
  - `bge-base-en-v1.5`
- Benchmarks:
  - `perf`
  - `longmemeval`
  - `locomo`
  - `membench`
  - `convomem`

Recommended default after these runs:

- GPU default: `bge-small-en-v1.5`
- CPU fallback: `all-MiniLM-L6-v2`
