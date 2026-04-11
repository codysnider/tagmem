# Benchmarks

This folder contains publishable benchmark results for `tagmem`.

Contents:

- `REPORT.md`: executive summary and cross-model comparison tables
- `MACHINE.md`: hardware and software environment details
- `METHODOLOGY.md`: exact commands, dataset sources, hashes, and reproducibility notes
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
  - `FalseMemBench`

Measured systems and source-reported reference values are intentionally separated in the detailed report.

Recommended default after these runs:

- GPU default: `bge-small-en-v1.5`
- CPU fallback: `all-MiniLM-L6-v2`
