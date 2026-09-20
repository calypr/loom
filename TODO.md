### Feature

1. `how` over the affected subsystem.
   skip: parent froze the design and named the source files to inspect.
2. `architect` for parallel design exploration. Skipping stays as `architect skipped: <reason>`; do not fold the design decision silently into implementation.
   architect skipped: the parent froze the design and this is a single-owner implementation.
3. Write the throughput checkpoint as four todo items. A dimension that genuinely does not apply (single file, no fan-out) keeps its item with `n/a: <reason>` rather than being dropped:
   - Blocking first steps. Gates run before fan-out.
   - Independent workstreams. Disjoint files, services, or layers parallelize. Shared writes serialize.
   - Shared mutable state. Default to splitting the target (the separate-before-serializing-shared-state principle skill). Serialize only for real invariants.
   - Smallest safe decomposition. If one worker is best, name why.
4. Delegate code-writing to a subagent using your configured feature model (default in poteto-mode's Models section) with a specific scope (file paths, named data shape and its organizing structure per principle-model-the-domain, a state machine over scattered booleans, a table/registry over branching, a typed model over repeated shape assumptions, chosen before the delegate writes logic, and success criteria); review its diff yourself. When the implementation admits multiple valid shapes (error handling, abstraction layer, test structure), delegate via the arena skill instead so the runners surface the alternatives and the cross-judge guards the pick. Mandatory: no skip-with-reason escape, and Laziness Protocol does not override it (the gain is review separation, not lines saved). You can spawn a subagent even though you are one; "the app is small" and "a subagent cannot spawn one" are both wrong. A subagent forbidden to spawn satisfies this by owning the diff directly with the same review separation; no "standing by" reply that waits on a nested agent. Give every file-writing delegate its own worktree (spawn it with isolation: "worktree", or hand it an exclusive branch), and do not write files or run a suite in a worktree a delegate still holds; fencing a file in the brief's prose is not a lock (principle-separate-before-serializing-shared-state). Comments per Comments. Surgical edits, re-ground against the source for upstream-derived files. Port shared-primitive improvements to all consumers and verify each. Commit liberally.
   n/a: one implementation owner was explicitly assigned; this integration worktree is exclusive.
5. Verify on the matching surface. "Inconclusive" or wrong-surface is not a pass; flag it.
6. Rebase into small, ordered commits; stack follow-ups. Use the sequence-verifiable-units principle skill, building, verifying, and committing each small unit before the next.
   skip: commits are explicitly prohibited; verify each unit without committing.
7. If the design is contested, `interrogate` before shipping.
   skip: the design is frozen and no competing implementation emerged from direct inspection.
8. Run Opening a PR.
   skip: parent requested an in-repo implementation only; no PR or commit.

### Throughput checkpoint

- Blocking first steps. The frozen S02 boundary is recorded, J02 passes live, and the shared Go resolver now passes all 64 compilation-package tests.
- Independent workstreams. Implement the lifecycle, OpenAPI, generated server adapter, and client contract as one serialized server-contract unit. Migrate the React callers only after that unit is reviewed and green.
- Shared mutable state. One implementation owner has exclusive use of the integration worktree while changing the generated contract and its adapters. The root reviews and runs checks only after that owner returns.
- Smallest safe decomposition. Use one Luna owner for the server-contract unit because OpenAPI generation couples the specification, generated Go, server adapter, and TypeScript client types. Use a fresh owner for the later React migration.

### Task

- [x] Trace verifier APIs and DOM controls; identify the five-edge schema-backed chain.
- [x] Add `verify-j02` through the isolated fresh-fixture flow and record DOM/API/literal evidence.
- [x] Add and verify the shared closed-state Go interpretation resolver.
- [x] Add the bounded lifecycle configured-column context and create-from-saved-column operation.
- [x] Add the OpenAPI routes, generated bindings, direct server adapters, and generic client DTOs.
- [x] Migrate Builder callers to opaque server results and delete `authoring/interpretationCandidate.ts`.
- [x] Extend the fixture and focused verifier tests only where required.
- [x] Run syntax, focused unit tests, fixture/readme validation, and diff check. The root also ran the live Docker/Chrome acceptance journey after the focused checks.

### S02/UI02 client migration bracket

- Blocking first steps. Complete. The malformed trailing type declaration was repaired before the caller migration.
- Independent workstreams. One owner will repair the API boundary, migrate `BuilderWorkspace`, `ColumnSelector`, and `InterpretationPanel`, update focused tests, and delete the client matcher as one caller-migration unit.
- Shared mutable state. Complete. The implementation owner released the tree before root review and verification.
- Smallest safe decomposition. Keep the client contract and both React callers together because deleting the matcher is safe only after every caller consumes the server-owned context.

### S02/UI02 closure

- [x] Full Loom UI suite: 28 files, 198 tests.
- [x] Production Loom UI build.
- [x] Compilation, lifecycle, and server Go suites.
- [x] OpenAPI ownership check, verifier syntax, and 18 verifier tests.
- [x] Live `verify-fast` journey with mapping create/review/cancel/apply/reload plus stale context/create non-mutation evidence.
- [x] Evidence: `.artifacts/loom-dev/6d7df93d6a37/mua5aet3-0489f5d8`.

### S03/UI03 feature workflow

1. `how` over the affected subsystem.
2. `architect` for the durable row-definition and membership model.
3. Write the throughput checkpoint as four todo items.
4. Delegate each code-writing unit to one Luna owner with an isolated worktree or exclusive integration-tree lease; root reviews the diff and runs the checks after release.
5. Verify each unit on its matching compiler, API, UI, and live-browser surface.
6. Sequence independently verifiable units; do not batch failures into the final journey.
7. `interrogate` only if the selected design remains contested after executable evidence.
8. Opening a PR is skipped because the user requested local implementation and did not request a PR.

### S03/UI03 architecture phases

- [x] Ground the existing authoring, population, compiler, recipe expansion, and UI row-change flow.
- [x] Sketch at least two structurally distinct row-definition designs.
- [x] Agree by rubric and independent cross-judge; record the selected design in `.audit/s03-row-definition-design.md`.
- [ ] Implement the selected design in verified units.
- [ ] Scrap and redesign only if repeated implementation deviations invalidate the selected ownership boundary.

### S03/UI03 throughput checkpoint

- Blocking first steps. Freeze one explicit row-definition model and its ownership before editing OpenAPI, compiler, or UI contracts. Expansion must reuse the existing typed UNNEST path; grouping must preserve member identity rather than only a count.
- Independent workstreams. After the contract unit is green, compiler lowering and generic row-choice discovery can proceed in separate isolated trees. OpenAPI, generated bindings, client types, and `BuilderWorkspace.tsx` remain serialized under root ownership.
- Shared mutable state. The integration tree has many accepted uncommitted changes. Only one writer may hold it. Delegated writers use isolated worktrees or receive an explicit exclusive lease; root does not edit or run suites there until release.
- Smallest safe decomposition. Start with the durable sum type, validation, canonicalization, and migration tests. Then add expansion compilation, grouping compilation, lifecycle preview/apply, generic UI controls, and J03 in that order.

### S03/UI03 task units

- [x] S03-01a. Add explicit resource, grouped, and expanded row-definition variants with validation, canonicalization, cloning, and migration from existing documents. Accepted after 135 authoring tests and 447 Explorer tests.
- [x] S03-01b-schema. Resolve field-group keys and repeated scopes from generated schema facts and bind each opaque choice to one authorized route occurrence and capability snapshot.
- [x] S03-01b-groups-domain. Add immutable explicit-group revision types, deterministic definition and membership digests, empty groups, overlapping membership, and stale-source validation.
- [ ] S03-01b-groups-persistence. Persist staged explicit-group revisions, definitions, and memberships in Arango with idempotent completion and cleanup.
- [x] S03-02a-semantic. Replace the recipe-only semantic UNNEST facade with an occurrence-bound row-expansion boundary and preserve explicit empty-value policy through lowering.
- [ ] S03-02a-execution. Lower root and arbitrary-depth expansion owners, derive stable item identity, implement ERROR at the renderer boundary, and execute the resulting physical plan.
- [ ] S03-02b. Add a typed grouped-row semantic and physical operation that returns group identity plus exact member witnesses.
- [ ] S03-02c. Prove independent repeated collections remain independent unless an explicit combination operation is authored.
- [x] S03-03a-contract. Define the receipt-backed proposal, generic preview evidence, cancel-without-mutation, and exact draft CAS apply boundary.
- [x] S03-03a-reducer. Add a closed proposal command whose private resolved row definition can change only `Document.Rows` and cannot leak onto the wire.
- [ ] S03-03a-lifecycle. Resolve receipt-backed proposals and apply them through the existing draft compare-and-swap boundary.
- [ ] S03-03a-preview. Execute and compare before/after rows, memberships, counts, and affected columns.
- [ ] S03-03b. Add generic Records, Groups, and Expand repeated values controls without FHIR resource-specific branches.
- [ ] S03-04. Add two schema-shape fixtures and J03 live verification for memberships, cancel, apply, reload, export, and non-Cartesian expansion.
