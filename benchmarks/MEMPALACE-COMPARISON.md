# MemPalace Comparison Appendix

This appendix compares `tagmem` only against MemPalace metrics that are directly comparable from public materials.

## Directly Comparable Published Metrics

Published MemPalace raw baseline:

- LongMemEval `Recall@5 = 0.966`
- LongMemEval `Recall@10 = 0.982`
- LongMemEval `NDCG@10 = 0.889`
- Claimed runtime: `~300s` on Apple Silicon

Current `tagmem` GPU results:

| Model | Recall@5 | Recall@10 | NDCG@10 | Time |
|---|---:|---:|---:|---:|
| `all-MiniLM-L6-v2` | 0.982 | 0.994 | 0.933 | 14.4s |
| `bge-small-en-v1.5` | 0.990 | 0.996 | 0.951 | 23.0s |
| `bge-base-en-v1.5` | 0.992 | 0.994 | 0.950 | 44.1s |

## Adversarial Retrieval Comparison

Using the standalone adversarial distractor benchmark:

| System | Cases | Recall@1 | Recall@5 | MRR |
|---|---:|---:|---:|---:|
| `tagmem` | 500 | 0.8880 | 1.0000 | 0.9440 |
| MemPalace raw-style | 500 | 0.6740 | 1.0000 | 0.8272 |

This benchmark stresses ranking under conflicting or near-miss memories. On the current `500`-case adversarial dataset, both systems saturate top-5 recall, but `tagmem` keeps a large top-1 and MRR advantage.

## Takeaways

- All three GPU-backed embedded models beat MemPalace's published raw LongMemEval quality numbers.
- All three GPU-backed embedded models are much faster than MemPalace's published raw timing claim.
- `bge-small-en-v1.5` is the best practical default because it materially improves quality over MiniLM without the large runtime increase of `bge-base-en-v1.5`.

## Caveats

- MemPalace's published runtime is described for Apple Silicon; these `tagmem` results were produced on a different machine and in Docker.
- This appendix only compares publicly published MemPalace raw metrics, not undocumented or non-reproducible variants.
