# Multilingual Retrieval

`tagmem` stores original source text and retrieves against it with hybrid semantic and lexical ranking.

The current tested multilingual retrieval coverage is focused on document search, not answer generation or general translation.

## Verified Coverage

Current ONNX-backed integration coverage verifies:

- Russian document, Russian inquiry
- Russian document, English inquiry
- Chinese document, English inquiry
- Chinese document, Chinese query with literal key phrases from the document

These checks run against the real embedded ONNX model (`bge-small-en-v1.5`) in:

- `internal/vector/multilingual_integration_test.go`

Importer-level full-document source retention is also covered in:

- `internal/importer/importer_integration_test.go`

## Examples

Expected supported behavior includes cases like:

- Russian document:

```text
В мастерской на Садовой улице хранится синий чемодан с зимними письмами и фотографиями моря.
```

Matching queries:

```text
В какой мастерской лежат зимние письма и фотографии моря?
Which workshop keeps the winter letters and sea photographs?
```

- Chinese document:

```text
在海边的小图书馆里，林把借书卡放在木窗旁边，并在目录里记下晒过太阳的页码。
```

Matching queries:

```text
借书卡放在木窗旁边
Who stores the cards by the wooden window in the seaside library?
```

- Chinese document:

```text
在旧天文台旁边，明把铜灯和潮汐地图锁进石柜里，等夜潮退去再来查看。
```

Matching queries:

```text
谁把铜灯和潮汐地图锁进石柜里？
Who locks the brass lantern and tide map in the stone cabinet?
```

## What This Does Not Claim

This document does not claim:

- arbitrary translation quality across all languages
- broad multilingual answer synthesis
- strong retrieval for every paraphrased Chinese natural-language question form

The current honest claim is narrower: the release checks cover the exact multilingual retrieval patterns listed above.

## Verification Commands

```bash
go test ./internal/importer
go test -tags tagmem_onnx ./internal/vector
```
