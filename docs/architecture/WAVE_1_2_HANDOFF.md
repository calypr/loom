# Executor handoff for Waves 1 and 2

Start on `architecture-cleanup` at the code snapshot `cd64a6456`. The [decision register](WAVE_1_2_DECISIONS.tsv) covers all 849 Wave 1 and 2 rows from the 1,127-row audit. Use the row ID and symbol to locate work. The original scanner’s line numbers refer to `036c340e7`; several symbols have since moved or disappeared. A decision authorizes a particular change. Only a verified implementation commit counts as completed work.

The decision register is separate from the execution ledger. The full plan has seven waves. This handoff covers the two in bold.

| Wave | Scope |
| --- | --- |
| 0 | Baseline tests, coverage, render and HTTP captures |
| **1** | **Test volume and ownership. 498 rows.** |
| **2** | **Dead code, signatures, imports and static analysis. 351 rows.** |
| 3 | Shared state vocabulary and primitives |
| 4 | Duplicated production responsibilities |
| 5 | Structural changes, beginning with the publication statechart replays |
| 6 | Query-text assertions |

## Queue for Sol Low

| Decision | Wave 1 | Wave 2 | Executor action |
| --- | ---: | ---: | --- |
| implement | 119 | 295 | Apply the named edit and verify its unit. |
| retain | 326 | 37 | Keep the named behavior or contract. Close the scanner suggestion with its recorded evidence. |
| complete | 52 | 17 | The register identifies the prior implementation commit. Skip. |
| covered | 0 | 2 | Credit the coupled row when its owning edit passes. |
| protected | 1 | 0 | Preserve the operator-owned file. |

From the implementation worktree, list open Wave 1 edits by ID and owner using this filter.

```sh
awk -F '\t' '$2 == 1 && $6 == "implement" {print $1, $4, $7}' docs/architecture/WAVE_1_2_DECISIONS.tsv
```

These numbers count **audit rows**, not commits or hours. The 304 statement-coverage suggestions name 1,052 test cases. A retain decision means the evidence does not justify discarding that test’s recorded assertions. It does not claim every case is globally unique. The register lists all candidate names, including those truncated in the original worklist.

## Start here

1. Read the register alongside the runtime ledger in the original checkout’s `.cache/archexec/ledger.tsv`. Skip completed commits, and preserve all operator-owned untracked files. In particular, row 828 names `internal/explorer/rowsource/filter_test.go`. Its absence from Git is **never** authority to remove the local file or its lifecycle counterpart.
2. Finish the pending generation unit first, following the coverage decision below. Then process other Wave 1 units by owning package. Where tests use an otherwise dead helper, change the tests and remove the helper in the **same commit**, crediting both waves’ row IDs. Complete the remaining Wave 2 rows after the Wave 1 rescan. Keep independent packages moving if one unit fails a gate.
3. Scope each edit to the named symbol and case. A `keep` decision for a coverage candidate preserves its input and expected outcome. A separately authorized clone row can still share its fixture or move that exact case into a table. When a test is named by the integration runner, preserve its entry point and verify its execution. Each removal gets one commit of record even if several rows identify it.
4. Use the decisions below and the row-specific checks as the executor’s brief. If the frozen premise fails on the implementation branch, record the symbol, caller or failing output and proceed with a different unit. Do not invent a replacement design or broaden a deletion to get the build green.

The four prior commits are `5055ded54` (row-source test preparation), `2783629be` (storage and ClickHouse tests), `56a1eb4e3` (Explorer route fixtures), and `cd64a6456` (ingest row loading). Draft patches and other worktrees are implementation aids. Compare them with the register and the current source before using them. They may be partial and have overlapping edits.

### Decisions that override earlier scratch patches

- **Store index tests (343–345, 435, 1099).** Keep all five original tests. The free functions forward to the live index-definition functions. Switch the calls, preserving stored-value arguments, exact index matching and migration behavior.
- **Compiler fixtures (653, 670, 727, 730, 745).** Place pure reshape inputs in `testkit/recipefixtures` and agreement-query helpers in `testkit/recipetest`. Putting trace compilation into `compilerfixtures` introduced an AQL test import cycle. Keep the existing package boundaries.
- **Research and compatibility (208–210, 212, 312–318, 332, 364–367, 425–428, 434).** Keep the documented compiler research entry points, byte-walker behavior and private-prefix validation witnesses. The compiler redesign plan explicitly invokes the translator fuzz target and benchmarks. A binary-only scan misses those consumers.
- **Stored semantic coverage (429–433).** Seed independent legacy envelope fixtures and exercise `LookupSemanticCoverage`. The write helper found in `coverage_test.go` is test-only; it cannot serve as a live production substitute.
- **Diagnostic text (547–568).** Retain the existing terminology. All 135 flagged messages start with Arango, Builder, Explorer, Group, Pivot or Coding. Rewording these names for a capitalization warning buys no simplification and changes observable diagnostics.

## The first unit has a specific coverage gate

Rows **288–298 and 418** retire the old `semantic.Contribution` spill path. Both the CLI and server ingestion paths use `EntryAggregator`. Preserve the omitted-field reset case against `EntryPartial`. At handoff the integration worktree already had a two-file draft in `internal/ingest/generation/runs.go` and `runs_test.go` (patch SHA-256 `8953c675ca423adbe354bd0138245b85ee9e25a5be75198d1cd5c2711683cd51`). That draft passed build, direct tests, lint and disposable integration. Its cross-package comparison still reported **ten unmatched blocks**. A passing package suite does not close this unit.

- Seven blocks are in `semantic/aggregate.go` (sort-key framing, reset, order-key and merge), and one is in `semantic/inventory.go` (`ContributionKey`). Apply the register’s decisions for each legacy semantic symbol and its consumers. Retain the original coverage for any implementation that survives. Preserve persisted key and ordering witnesses independently of the old spill writer. For a persisted-key witness, use project `p`, generation `g`, auth path `/p`, type `Observation`, build `b`, source `s`, ordinal `7`. The old length-prefixed SHA-256 key is `e2477481e5e105f363825ae64302e0fd53fcb900cf7717744db6e9d6237acf65`. Seed a legacy record with this literal key and verify the supported reader/materialization path. Record genuinely deleted statements separately.
- One block is the equal-length lexical less-than case in `semantic/entries.go`. Cover that live ordering rule directly with two equal-length, distinct values. Exercise both comparison directions so map iteration cannot select the only covered direction.
- One block is cancellation after receiving a job in `ingest/generation/schedule.go`. The retired tests did not call the scheduler directly. Compare identical pre/post scheduling cases, including `TestRunGenerationFilesFailFastCancelsSiblings`, with the same source, instrumentation and other-package union. Use one worker and two queued files: the first load cancels the parent context before it returns. Assert the second load never starts. The outcome is deterministic; the select can exit through either cancellation branch. Record branch coverage separately from that behavioral assertion. If the surviving statement set still shrinks, keep the affected test under the original rollback rule.

The mismatches are in `.cache/archexec/logs/generation-coverage-map.log`. Use a fixed package baseline for the comparison. Coverage profiles whose source coordinates have moved require a same-source comparison or an explicit mapping of surviving statements.

## Verification on implementation

Run `go build ./...`, tests for each touched package and its direct importers, and `make lint-go-comments` for every unit. Use the row-specific check **in addition** to these gates. Test deletions require the per-test covered-statement union with other packages and preservation of unique assertions. Keep database-skipped tests independent of unit coverage. For compiler, recipe, spec and Explorer compilation changes, compare the render records. For API and Explorer changes, compare the HTTP captures using the established volatile-field checks.

Run the package audit before and after package moves and its full mode afterward. Use the repository’s adversarial review procedure for changes to production logic. Run `make test-integration` on disposable services for storage, publication, ingest, ClickHouse or Arango changes and at each wave boundary. Record commands, source identity, logs and failures. Rescan each completed wave for newly dead code, duplicates and unused declarations. Commit locally with explicit paths after the gates pass. The local cache under `.cache/archexec/` contains prior evidence and tools; a previous run against a different implementation does not verify a new edit.
