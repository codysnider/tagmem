# Remove Embedded Hash Fallback Design

Date: 2026-04-30

## Summary

Remove the embedded hash provider as a supported runtime path and make the embedded model fail hard whenever the real ONNX-backed embedding runtime cannot initialize.

The current fallback behavior lets unsupported or broken environments silently degrade into a synthetic hashing-based embedding mode. That makes benchmark output misleading, weakens runtime guarantees, and creates confusion in tests, scripts, and release validation. The new design makes one rule explicit: if the real embedded model is unavailable, `tagmem` fails clearly instead of pretending to work.

## Goals

- Eliminate the embedded hash provider from supported runtime behavior.
- Ensure the embedded model path always means the real ONNX-backed model path.
- Make runtime failures explicit and early when the embedded model cannot initialize.
- Keep tests fast by replacing hash-provider usage with explicit test doubles where appropriate.
- Ensure benchmarks and release validation only measure or validate the real model path.

## Non-Goals

- Replacing the current embedded ONNX model implementation.
- Redesigning the OpenAI-compatible provider path.
- Reworking benchmark methodology beyond removing synthetic hash embeddings from supported runs.
- Changing the Docker-first deployment model.

## Problem Statement

The repo currently supports or exposes an embedded hash path in several places:

- provider resolution can explicitly select `embedded-hash`
- embedded model setup can fall back to hash when ONNX is unsupported
- tests and benchmarks use `EmbeddedHashProvider()` directly
- scripts and validation logic detect fallback by string matching instead of treating it as a hard failure

This creates three core problems:

1. **Misleading runtime behavior**
   An environment that cannot run the real model can still appear to work.

2. **Misleading benchmark output**
   People can accidentally report or inspect results from a mode that is not intended to be supported.

3. **Confused testing model**
   The codebase currently mixes a product fallback mode with what should instead be explicit unit-test fakes.

## Recommended Approach

Remove the embedded hash provider from runtime provider resolution and embedded model fallback logic. Replace it with fail-hard behavior and explicit test-only fakes.

This is preferred over:

- keeping hash as an undocumented or hidden escape hatch
- keeping hash only for benchmarks
- continuing to detect fallback strings in scripts after runtime setup

The design principle is simple: supported model or explicit failure.

## Architecture

### Runtime Provider Resolution

The runtime provider layer should only expose supported runtime paths:

- `embedded`
- `openai` or equivalent OpenAI-compatible path

It should no longer expose:

- `embedded-hash`
- `hash`

Any environment variable or provider resolution path that currently selects the hash provider should become an explicit configuration error.

### Embedded Model Initialization

The embedded model path should no longer degrade into a synthetic fallback.

If the ONNX runtime or supported execution path is unavailable, embedded model setup should return an error that clearly states:

- what failed
- whether the environment is unsupported
- what supported path the user should use instead

That error should surface through normal command execution rather than being downgraded into a fake embedding implementation.

### Doctor Behavior

`tagmem doctor` should fail if the embedded provider cannot initialize the real model/runtime.

It should not report a fallback success state. This keeps diagnostics honest and makes release validation simpler.

## Runtime Behavior

### Embedded Provider

When `TAGMEM_EMBED_PROVIDER=embedded` or the embedded provider is selected by default:

- if the ONNX model/runtime is available and supported, continue normally
- if it is not available or supported, return an error and stop

### OpenAI-Compatible Provider

The OpenAI-compatible provider remains unchanged by this design. It is still a supported alternative provider path.

### Unsupported Environments

Unsupported environments should fail immediately rather than silently degrading.

Examples include:

- source builds on unsupported host architectures for the embedded provider
- missing or incompatible ONNX runtime assets
- embedded model initialization failures during doctor or normal command execution

## Testing Strategy

### Unit Tests

Unit tests that do not care about real embedding quality should stop using `EmbeddedHashProvider()`.

Instead, they should use explicit test doubles that live in test code only, for example:

- a deterministic fake embedding function
- a small fake provider constructor used only in tests

This preserves fast tests without pretending the hash mode is a supported product configuration.

### Integration Tests

Integration tests for the embedded model path should run only on the actual ONNX-backed runtime path.

That means:

- real embedded model tests stay tagged or environment-scoped as needed
- unsupported environments fail or skip explicitly
- no integration path should pass by silently dropping to hash embeddings

### Benchmarking

Benchmarks should only run on the real model path.

Any benchmark workflow that currently uses or allows the hash provider should be removed or converted to use:

- the real embedded model path
- or an explicitly named mock benchmark path that is clearly not a product benchmark

The preferred direction is to remove hash-backed benchmark runs entirely.

## Cleanup Scope

The implementation should remove or replace the following categories of code:

### Provider Layer

- `ProviderEmbeddedHash`
- `EmbeddedHashProvider()`
- explicit `hash` or `embedded-hash` provider selection in env resolution

### Embedded Model Fallbacks

- fallback-to-hash branches in embedded/local model setup
- descriptions or doctor output that mention hash fallback as a normal outcome

### Tests

- direct use of `EmbeddedHashProvider()` in store tests
- direct use of `EmbeddedHashProvider()` in importer tests
- direct use of `EmbeddedHashProvider()` in MCP or benchmark tests

These should be replaced with explicit local test doubles where the real model is not required.

### Scripts and Validation

- installer checks that grep for fallback strings
- release checks that treat fallback as a post-hoc validation condition

These should move to straightforward command-failure behavior: if the embedded model cannot initialize, the command should fail.

## Rollout Plan

### Phase 1

- remove runtime selection of embedded hash provider
- remove embedded fallback-to-hash behavior
- make doctor and runtime fail hard on embedded model initialization failure

### Phase 2

- replace test usage of `EmbeddedHashProvider()` with explicit test doubles or real tagged integration paths

### Phase 3

- remove fallback detection logic from installer and release scripts
- simplify benchmark commands to only use supported real-model paths

### Phase 4

- delete the now-unused hash provider implementation and related constants/helpers

## Risks

### Risk: Many tests currently rely on hash provider speed

Mitigation:

- replace product fallback with explicit test-only doubles
- keep unit tests deterministic and fast without preserving hash as a supported runtime mode

### Risk: Some unsupported local workflows will now fail immediately

Mitigation:

- this is intentional
- errors should clearly explain that the embedded runtime is unsupported or unavailable
- users can switch to the supported Docker path or another supported provider

### Risk: Hidden references remain in scripts or benchmarks

Mitigation:

- search and remove all runtime references to `EmbeddedHashProvider`, `embedded-hash`, and `hash`
- update validation logic so success is based on real model startup, not string matching

## Success Criteria

The design is successful when all of the following are true:

- no supported runtime path can silently fall back to hash embeddings
- `tagmem doctor` fails on unsupported embedded-model environments instead of reporting fallback success
- release validation only passes on real embedded model execution
- benchmarks no longer use the hash provider as a product-mode path
- unit tests still run quickly using explicit test doubles
- integration tests continue to validate the real ONNX-backed embedded model path

## Recommendation

Proceed with strict fail-hard behavior and remove the embedded hash provider from supported runtime behavior entirely.

This is the cleanest model, best matches the Docker-first supported path, and prevents future confusion in performance claims, diagnostics, and test design.
