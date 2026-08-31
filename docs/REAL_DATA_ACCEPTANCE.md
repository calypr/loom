# Real-data acceptance test

## Purpose

`make acceptance-real` proves the path an NCPI researcher uses. The command loads a fixed FHIR Aggregator cohort into ArangoDB, publishes an Explorer V2 workspace into ClickHouse, reads the Explorer viewer state, and queries the published dataframe through GraphQL. `make acceptance-performance` runs that proof for a Git base and the current worktree on the same services, then applies the regression gate.

Local runs use the existing Kubernetes database pods through port-forwards. GitHub runs use job service containers. Both modes build and start Loom from the checkout under test; neither uses the deployed Loom pod as evidence.

## Fixture contract

The fixture is a locked cohort of 100 TCGA-BRCA Patients from `ResearchStudy/638f6162-e000-5167-8216-962d86b74a98` at `https://google-fhir.fhir-aggregator.org`.

The lock records:

- the source endpoint and study;
- the deterministic patient selection rule;
- every resource type, ID, version, and canonical SHA-256 digest;
- exact per-type counts;
- the fixture format and lock digest.

The repository does not store raw FHIR payloads while redistribution terms remain unclear. A cold run fetches the locked resources into a content-addressed cache. A warm run performs no network requests. Upstream drift fails the test and never rewrites the lock.

The fetcher enforces hard limits before it commits the cache:

- 100 Patients;
- 50,000 resources;
- 512 MiB of uncompressed JSON;
- 250 FHIR result pages;
- 5 minutes of fetch time.

The fixture includes all Specimen, Observation, DocumentReference, and Condition resources whose subject points to a selected Patient. It also includes the ResearchStudy. ResearchSubject is deliberately excluded: the Aggregator exposes the R4 `individual` shape while Loom validates the R5 `subject` shape, and this recipe does not traverse ResearchSubject. Other references may point outside the fixture because the acceptance recipe does not traverse them.

`refresh-fixture` updates only the fixture lock. A separate explicit command produces candidate expected results. A maintainer reviews those results before changing the checked-in oracle.

## Researcher dataframe

The checked-in workspace publishes one Patient-grain output named `tcga_brca_cohort`. Each of its 100 rows combines Patient demographics with clinical and biospecimen facts reached through the FHIR graph:

- `Patient -> Condition` supplies distinct histology, pathological stage, and the condition count.
- `Patient -> Observation` supplies age at diagnosis, diagnostic method, and days to death by exact observation code.
- `Patient -> Specimen` supplies total, tumor, and normal specimen counts and proves paired tumor-normal availability.
- `Patient -> Specimen -> Observation` supplies the earliest and latest collection day.

The published columns are:

```text
patient_id                    submitter_id
birth_sex                     race
ethnicity                     primary_histology
pathological_stage            age_at_diagnosis_days
diagnostic_method             days_to_death
condition_count               specimen_count
tumor_specimen_count          normal_specimen_count
has_paired_tumor_normal       earliest_collection_day
latest_collection_day
```

`primary_histology` is a sorted distinct array because five fixture patients have more than one histology. Choosing the first Condition would discard real diagnoses. FHIR Aggregator supplies age-at-diagnosis intervals as negative days, following the source dataset convention; the acceptance dataframe preserves those values without an undocumented sign change.

The oracle makes coverage readable in addition to checking the exact normalized row digest. It requires stage, pairing, specimen counts, and collection range for all 100 patients; age at diagnosis for 98; diagnostic method for 88; days to death for 6; race for 89; and ethnicity for 84.

The acceptance command checks that every requested field and traversal exists in the generation catalog before publication. It does not weaken the workspace when a path is missing.

## Command contract

The public Go interface stays small.

```go
type FixtureDigest string
type RunID string

type Namespace struct {
	Run             RunID
	Project         string
	Generation      string
	ArangoDatabase  string
	ClickHouseDatabase string
}

type ValidatedFixture struct {
	Digest FixtureDigest
	MetaDir string
	Counts map[string]int
}

type Lease interface {
	Connections() Connections
	Namespace() Namespace
	Close(context.Context) error
}

type Target interface {
	Acquire(context.Context, RunSpec) (Lease, error)
}

type Runner struct{}

func New(Config) (*Runner, error)
func (r *Runner) Run(context.Context) (Report, error)
```

`Runner.Run` owns the acceptance transaction. Callers do not coordinate load, publication, verification, or cleanup stages.

## Execution targets

The Kubernetes target resolves services by label or fixed-name fallback. It port-forwards only ArangoDB and ClickHouse. It builds the loader and Loom server from the current worktree, creates guarded run-specific database names, starts the local server with `--no-auth`, and waits for `/readyz`.

The GitHub target receives ArangoDB and ClickHouse service URLs from the workflow. It builds and starts the same checkout with the same run-specific database policy.

Database names must match `^loom_acceptance_[a-f0-9]{16}$`. Cleanup drops only the two captured databases after it verifies the names. It does not delete records from shared application databases. Cleanup then lists Arango databases and queries ClickHouse `system.databases` to prove that both names are absent.

## Assertions

The command performs these checks:

1. Upload the per-type NDJSON files through the generation HTTP API.
2. Compare the load response and direct Arango counts with the fixture lock.
3. Reject dangling recipe-path edges and missing catalog fields.
4. Publish the checked-in workspace through the repository Explorer route.
5. Resolve the ClickHouse table through the returned execution ID and the Arango publication registry.
6. Compare the physical schema, row count, and unique Patient count with the checked-in oracle.
7. Require a non-empty Explorer viewer state containing the publication recipe.
8. Query GraphQL dataset metadata, sorted rows, counts, and facets. Require numeric `rowCount` and `totalCount`, array-shaped `rows`, and reject any GraphQL `errors` value. Compare per-column coverage and boolean truth counts with the readable oracle, then compare the normalized rows with the exact `row_digest`.
9. Publish the same workspace again and prove that its execution and selector are unchanged.

The command writes partial evidence before cleanup. A cleanup failure is joined with the original scenario failure.

## Performance comparison

Correctness always blocks a change. Performance compares the base and the current checkout on the same machine, against the same fixture and service versions. Local dirty-tree runs use `HEAD` as the base. Pull requests use the pull request base SHA. Pushes use the previous commit when GitHub provides it.

Each variant receives empty run-specific databases. The runner warms and validates the fixture before either variant and alternates execution order from the head digest. It records:

- generation upload time;
- Explorer publication time;
- Explorer viewer latency;
- the combined GraphQL metadata, rows, count, and facet probe latency.

A metric is a suspected regression when the head is at least twice as slow and exceeds the absolute floor. The floor is 5 seconds for ingestion and publication, and 100 milliseconds for API probes. The runner repeats a suspected regression in reverse order. CI fails only when the same metric crosses both limits twice.

If the base commit cannot build or does not support the protocol, the report uses `BASE_UNAVAILABLE`. That status fails unless the explicit `LOOM_ACCEPTANCE_ALLOW_BASE_UNAVAILABLE=true` rollout flag is present. Remove the workflow flag once the acceptance protocol exists on every supported base branch.

## Files

```text
cmd/loom-acceptance/                 Command boundary
internal/acceptance/                 Fixture, target, scenario, evidence, and comparison logic
testdata/acceptance/ncpi-tcga-brca/ Fixture lock, provenance, workspace, GraphQL document, and oracle
scripts/acceptance-real.sh          Stable local and CI entry point
scripts/acceptance-performance.sh   Same-machine base/current regression driver
.github/workflows/acceptance.yaml   GitHub service and artifact wiring
.codex/skills/verify-loom/          Agent-facing launch, drive, evidence, and cleanup instructions
```

The commands write `report.json`, normalized responses, timings, cleanup proofs, service logs, and `performance.json` under `.artifacts/acceptance/`. Reports exclude credentials and raw FHIR payloads.
