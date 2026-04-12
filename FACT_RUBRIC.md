# Fact Promotion Rubric

Default to storing memory as an entry.

Promote a statement to the knowledge graph when all of these are true:

- it fits one clear `subject -> predicate -> object` statement
- you expect exact lookup, current-state lookup, or timeline lookup later
- a stable predicate name is easy to reuse
- losing some wording nuance is acceptable

Store both an entry and a fact when the canonical value matters but the original phrasing, timing, or source still matters.

Keep text as an entry only when any of these are true:

- it is a plan, suggestion, discussion, or uncertainty
- it has multiple clauses or multiple facts packed together
- the wording itself matters
- the predicate would be ad hoc or inconsistent

Good fact candidates:

- `Staging uses postgres.internal.example.com.`
- `Repo default branch is main.`
- `Caroline attended LGBTQ support group.`
- `API gateway port is 8080.`

Good dual-storage candidates:

- `Caroline currently lives in New York.`
- `The billing service used Stripe before the migration.`

Keep as entry:

- `We discussed maybe moving staging to Postgres next quarter.`
- `You suggested using Postgres for staging because it simplifies backups.`
- `The migration worked because staging finally stabilized.`
