# Tagging Batching ONNX Profiling Note

Task 3 changed semantic tag ranking from separate `provider.Func(content)` plus `provider.Batch(labels)` calls to one `provider.Batch([content] + labels)` call.

## Supported Docker/ONNX evidence

### Before batching change

- Supported-path root-cause profiling showed that `add` performed multiple sequential embedded embedding calls and that `onnx_run` dominated each profiled embedding pass.
- Pre-change supported Docker/ONNX values from the original profiling session:
  - `add`
    - group 1: `onnx_run=224.017379ms`, `embed_total=224.343002ms`
    - group 2: `onnx_run=7.421131ms`, `embed_total=7.527792ms`
    - group 3: `onnx_run=2.870734ms`, `embed_total=2.922972ms`
  - `search`
    - group 1: `onnx_run=266.987898ms`, `embed_total=267.242818ms`
- The code path before this change guaranteed an extra semantic-tagging embed call on `add`: one `Func(content)` call plus one `Batch(labels)` call.

### After batching change

Fresh supported Docker/ONNX rerun on 2026-05-04 with `TAGMEM_EMBED_PROFILE=1`:

- `add`
  - pass 1: `onnx_run=385.171203ms`, `embed_total=385.496966ms`
  - pass 2: `onnx_run=4.601115ms`, `embed_total=4.674092ms`
- `search`
  - pass 1: `onnx_run=232.453266ms`, `embed_total=232.712845ms`

Earlier same-day supported-path rerun showed the same post-change shape:

- `add`
  - pass 1: `onnx_run=166.020564ms`, `embed_total≈166ms`
  - pass 2: `onnx_run≈2.214ms`, `embed_total≈2.214ms`
- `search`
  - pass 1: `onnx_run=164.009596ms`, `embed_total≈164ms`

Repeated Docker/ONNX timings vary between runs, so the important signal here is the profile shape and call count change rather than any single millisecond value.

## Interpretation

- The dominant cost is still the main embedding `onnx_run`.
- The batching change removed one semantic-tagging embedding request from the `add` path, reducing the post-change `add` profile shape to two embedding passes instead of the pre-change three-pass shape.
- The remaining small `add` pass is consistent with the batched semantic-tagging work.
