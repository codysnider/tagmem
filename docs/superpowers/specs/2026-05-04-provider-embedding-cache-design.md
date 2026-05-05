# Provider Embedding Cache Design

Date: 2026-05-04

## Summary

Add a bounded in-process embedding result cache inside the embedded provider so identical texts can reuse previously computed vectors instead of re-running ONNX inference.

## Goals

- Reduce repeated ONNX runs for identical texts within one hot process lifetime.
- Improve both direct and daemon-backed command latency where repeated texts recur.
- Keep the cache local, bounded, and opt-in-safe for the embedded provider path.

## Non-Goals

- Cross-process persistence in this phase.
- Cache sharing across different providers/models.
- Semantic or approximate cache matching; exact text identity only.

## Recommended Approach

Add a bounded exact-text embedding cache inside the embedded provider instance.

Key design points:

- key: exact input text
- value: cloned embedding vector
- scope: one provider/process lifetime
- bounded size with simple eviction
- used by both single-text and batch embedding paths

## Architecture

### Cache Scope

The cache lives inside the embedded provider / embedder, not in the repository.

That keeps it close to the actual ONNX cost and lets multiple higher-level callers reuse it naturally.

### Cache Behavior

- exact-match only
- if all requested texts in a batch are cached, skip ONNX entirely
- if some texts are cached and some are missing, run ONNX only for the misses, then merge results back into batch order

### Bounding

Use a simple bounded cache first.

Recommended shape:

- fixed capacity
- LRU or oldest-eviction is fine

## Recommendation

Proceed with a bounded exact-text cache inside the embedded provider and make `EmbedBatch` cache-aware first. That gives the broadest reuse benefit with minimal behavior risk.
