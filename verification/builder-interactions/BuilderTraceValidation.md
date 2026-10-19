# Builder trace validation

`scripts/verify-builder-traces.mjs` replays captured Builder interaction traces
through the TLA+ models. It is a conformance check for the traces emitted by the
real-controller schedule suites; it does not prove the React or Go
implementations.

## Usage

```sh
node scripts/verify-builder-traces.mjs \
  --traces /tmp/traces.json \
  --output /tmp/trace-report.json
```

Options:

- `--traces <file>`: required trace JSON.
- `--output <file>`: write the JSON report. Without it, the summary still prints.
- `--contract <file>`: use a local contract fixture. The default is
  `verification/builder-interactions/builder-trace-contract.json`.
- `--allow-partial`: local iteration only. Counts and required coverage are
  reported but not enforced. The default contract requires 61 schedule
  traces and zero expected-failure finding traces.
- `--quiet`: print only the summary and failures, not one line per trace.

The exit status is 0 only when shape, contract, count, coverage, and every
unique trace check pass.

## Checks

The runner validates:

- `schemaVersion`, `sourceCommit`, the trace shape, and observation fields.
- Every action label against the selected model's contract action list, and
  every event against the contract bindings/required events. If a label has a
  binding, its event must match the binding.
- Required models, actions, and events from `contract.phase1`.
- Duplicate `(model, initialObservation, actions)` traces are validated once
  with TLC and reported for every original trace.

## TLC replay

For each unique trace, the runner writes a temporary module and config:

- `EXTENDS BuilderPreview` or `EXTENDS ConstructionLifecycle`, plus `Naturals`,
  `Sequences`, and `TLC`.
- `TraceActions` is the action-label sequence with a leading `"Init"`.
  `TraceObservations` is the matching observation sequence, starting with the
  initial observation.
- `TraceNext` takes one model `Next` step, requires `lastAction'` to match the
  next trace label, requires the next observation mapping to hold, and
  increments `traceIndex`.
- The config checks `TypeOK` and `TraceProgress`.

`TraceProgress` is a trace-completion check, not a model safety invariant. The
usual `[][TraceNext]_vars` specification permits stuttering, so a trace could
otherwise stop before a bad later action and still pass. `TraceProgress`
requires `ENABLED TraceNext` until `traceIndex = Len(TraceActions)`. Retained
finding paths remain legal because this check only concerns trace progress.

The generated specification is named `TraceSpec`, not `Spec`, because each
extended model already defines `Spec`; TLA+ modules cannot redefine an inherited
operator. The config therefore uses `SPECIFICATION TraceSpec`.

Observation mapping is evaluated twice: `ObsMatches` against the initial state,
and `ObsMatchesNext` against the next state after the action. `idle` adds no
constraint. For the construction model, the next-state `canApply` check uses a
primed copy of `ResourceIsApplicable`.

TLC 1.7.4 is required. `TLA2TOOLS_JAR` is honored; otherwise the runner uses the
same temporary cache and SHA-256 as `verify-builder-formal.mjs`:
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`.

On a TLC failure the report and console include the trace name, failing action
index, TLC output tail, and the generated module path. Full TLC output is not
printed. `unmodeledEvents` from the capture are summarized, not replayed.
