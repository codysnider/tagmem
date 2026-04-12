# Verbatim Source Verification

`tagmem` stores searchable body text separately from retrievable verbatim source material.

The claim in `README.md` is verified by the importer integration test:

- `internal/importer/importer_integration_test.go`
- test name: `TestRunFilesModeSearchReturnsFullDocumentSource`

## What The Test Checks

The test ingests five long text fixtures under `internal/importer/testdata/library/`.

For each file, it searches using three exact sentences selected from different parts of the document:

- beginning
- middle
- end

For every matching result, it verifies:

- the correct document is returned by `origin`
- `source` matches the full original file content exactly after trimming outer whitespace
- `source` still contains the searched sentence
- `source` also contains the other expected sentences from the same document

This proves the retrieved memory is not only the matching chunk. The full original document remains available in `source` after ingest and search.

## How To Reproduce

Run the importer test package:

```bash
rtk go test ./internal/importer
```

Or run the release guardrail, which includes the focused test packages used to protect this contract:

```bash
just release-check
```

## Relevant Storage Contract

- `body`: searchable chunk or memory body used for ranking and retrieval relevance
- `source`: full verbatim source material returned with the selected memory
- `origin`: provenance such as file path or manual source label

That separation is why search can rank on compact memory text while still returning the original source material unchanged when a memory is selected.
