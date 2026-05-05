# Embedded ONNX Profiling and Batching Design

Date: 2026-05-04

## Summary

Measure and optimize the embedded ONNX embedding path itself by adding opt-in phase profiling around tokenization, session acquisition, inference, and post-processing, then use that evidence to tune batching behavior where it materially reduces end-to-end latency.

## Goals

- Identify which internal ONNX embedding phase dominates current command latency.
- Keep the profiling local, opt-in, and low-intrusion.
- Improve the real embedded provider path rather than guessing from high-level timing alone.
- Preserve correctness and current model outputs.

## Non-Goals

- Changing the embedding model in this phase.
- Replacing ONNX runtime in this phase.
- Broad caching redesign in this phase.

## Recommended Approach

Add a small profiling layer to the embedded provider and batch path, expose it only behind an opt-in environment variable, and use it to compare:

- tokenization cost
- tensor preparation cost
- session acquisition cost
- ONNX `Run(...)` cost
- pooling/normalization cost

Then apply small batching/path improvements only where the numbers show clear value.

## Architecture

### Profiling Mode

Recommended activation:

- `TAGMEM_EMBED_PROFILE=1`

When enabled, embedded-provider code records timing for key phases and prints a compact summary to `stderr` or another profiling sink already used by the CLI profiling pass.

### Instrumentation Points

For single embedding and batch embedding paths, capture:

- tokenizer load/init if it still occurs during warm calls
- tokenization
- flatten/tensor preparation
- session checkout from pool
- ONNX runtime `Run(...)`
- output pooling/normalization
- total embed time

### Batching Focus

The first optimization target should be batch behavior, because the provider already has a micro-batch path (`miniLMMicroBatchSize = 32`) and a session pool.

The likely high-value questions are:

- is `miniLMMicroBatchSize=32` actually optimal on current CPU/GPU path?
- is session-pool checkout materially contended?
- does sorting by text length help enough to keep?
- is there waste in token/tensor preparation that scales badly on small single-entry requests?

## Testing Strategy

- unit/integration tests for profiling-off behavior staying unchanged
- focused tests that profiling-on mode emits expected phase markers without altering output values
- real command validation on the supported Docker/ONNX path to capture phase breakdowns

## Recommendation

Proceed with opt-in embedded-provider profiling first, then use the measured dominant phase to choose the smallest real batching optimization rather than guessing.
