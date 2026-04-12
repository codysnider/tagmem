# Knowledge Graph

`tagmem` uses the knowledge graph for narrow, canonical facts.

Use it when you want deterministic answers to exact questions such as:

- what does staging use?
- what is the current default branch?
- where does this person live right now?
- what was true on a specific date?

## Model

Each fact is stored as:

- `subject`
- `predicate`
- `object`
- `valid_from`
- `valid_to`
- `source`

This is intentionally small. The graph is not meant to replace entries. It exists to hold canonical facts that benefit from exact lookup and timeline queries.

## Query Semantics

- `kg_query` without `as_of` returns current facts only
- `kg_query` with `as_of` returns facts valid on that date
- `kg_timeline` returns the full chronological history, including expired facts
- `source` can point back to the entry or source material that justified the fact

## When To Use It

Promote text to the graph when all of these are true:

- it fits one stable `subject -> predicate -> object` statement
- you expect exact lookup, current-state lookup, or timeline lookup later
- a reusable predicate name is obvious
- the canonical fact matters more than the original wording

Keep the original entry too when timing, qualifiers, or wording matter.

Examples:

- fact only: `Repo default branch is main.`
- fact only: `Staging uses postgres.internal.example.com.`
- fact plus entry: `Caroline currently lives in New York.`
- entry only: `We discussed maybe moving staging to Postgres next quarter.`

For the lightweight promotion rule set, see [`FACT_RUBRIC.md`](FACT_RUBRIC.md).

## What The Graph Is Not

- not a full ontology system
- not automatic extraction from every entry
- not a replacement for verbatim source retrieval
- not the right place for plans, suggestions, or nuanced discussion

The default remains simple: store memory as entries first, and promote only the small subset of statements that clearly benefit from canonical structure.
