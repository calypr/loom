import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import { makeTemporalFilterJourneyCaptureFixture } from './fixtures/temporal-filter-journey-schema-fixture.mjs';
import { compileTemporalFilterJourneyTrace, validateTemporalFilterJourneyTrace } from '../temporal-filter-tla-adapter.mjs';

const testDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(testDirectory, '../../../../');
const modelPath = resolve(repositoryRoot, 'verification/builder-interactions/TemporalFilterJourney.tla');
const tlcJar = process.env.TLA2TOOLS_JAR;

const makeJourney = caseName => adaptTemporalFilterJourney(makeTemporalFilterJourneyCaptureFixture(caseName));

const runTlc = (trace, moduleSuffix) => {
  assert(tlcJar, 'Set TLA2TOOLS_JAR to the verified TLC 1.7.4 jar for this focused check.');
  const compiled = compileTemporalFilterJourneyTrace(trace, { moduleName: `TemporalFilter${moduleSuffix}` });
  const directory = mkdtempSync(join(tmpdir(), 'loom-temporal-filter-tla-'));
  const metadataDirectory = join(directory, 'states');
  mkdirSync(metadataDirectory);
  try {
    copyFileSync(modelPath, join(directory, 'TemporalFilterJourney.tla'));
    writeFileSync(join(directory, `${compiled.moduleName}.tla`), compiled.moduleText);
    writeFileSync(join(directory, `${compiled.moduleName}.cfg`), compiled.configText);
    const result = spawnSync('java', [
      '-XX:+UseParallelGC', '-cp', tlcJar, 'tlc2.TLC', '-workers', '1',
      '-metadir', metadataDirectory, '-config', `${compiled.moduleName}.cfg`,
      `${compiled.moduleName}.tla`,
    ], { cwd: directory, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
    return { ...result, output: `${result.stdout ?? ''}${result.stderr ?? ''}`, compiled };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
};

const rawRequestFor = (trace, event) => trace.temporalFilterLifecycle.requestEvents.find(request =>
  request.browserRequestId === event.request.captureId);

test('compiler binds synthetic adapter-produced DATE events, snapshots, DOM references, and request ledger', () => {
  const trace = makeJourney('date');
  const validated = validateTemporalFilterJourneyTrace(trace);
  const compiled = compileTemporalFilterJourneyTrace(trace);
  assert.equal(validated.eventCount, 16);
  assert.equal(validated.mutationCount, 6);
  assert.equal(validated.proposalCount, 6);
  assert.equal(validated.initialMountEpoch, 1);
  assert.equal(compiled.moduleName, 'TemporalFilterJourneyDateTrace');
  assert.match(compiled.moduleText, /EXTENDS TemporalFilterJourney/u);
  assert.match(compiled.moduleText, /TraceInitialMountEpoch == 1/u);
  assert.match(compiled.moduleText, /TraceEvents == <<\[action \|-> "ExplorerCreated"/u);
  assert.match(compiled.configText, /CONSTANT InitialMountEpoch <- TraceInitialMountEpoch/u);
  assert.match(compiled.configText, /CONSTANT ObservedRequests <- TraceObservedRequests/u);
  assert.match(compiled.configText, /INVARIANT InvObservedMutationsConsumed/u);
  assert.match(compiled.configText, /PROPERTY EventuallyComplete/u);
  assert.equal(compiled.eventCount, trace.actions.length);
});

test('compiler binds synthetic DATE_TIME typed instants and the adapter-captured native input', () => {
  const trace = makeJourney('date-time');
  const input = validateTemporalFilterJourneyTrace(trace);
  assert.equal(input.profile, 'DATE_TIME');
  assert.equal(input.caseValue.inputValue, trace.actions.find(event =>
    event.action === 'FilterProposed(create)').ui.inputValue);
  assert.equal(input.caseValue.initialRequestValue.dateTime, '2026-01-02T00:00:00.000Z');
  assert.equal(input.caseValue.initialResponseValue.dateTime, '2026-01-02T00:00:00Z');
  assert.deepEqual(input.caseValue.initialRowIds, []);
  assert.deepEqual(input.caseValue.editedRowIds, [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
  ]);
});

test('compiler rejects missing actions, unlinked mutations, unknown read-only POSTs, and raw-reference tampering', () => {
  const missingAction = makeJourney('date');
  missingAction.actions.pop();
  assert.throws(() => compileTemporalFilterJourneyTrace(missingAction), /Every semantic lifecycle action/u);

  const missingMutation = makeJourney('date');
  const tableAction = missingMutation.actions.find(event => event.action === 'SourceTableCreated');
  tableAction.request.isMutation = false;
  assert.throws(() => compileTemporalFilterJourneyTrace(missingMutation), /preserve the observer's mutation classification/u);

  const unknownFalsePost = makeJourney('date');
  unknownFalsePost.temporalFilterLifecycle.requestEvents.push({
    browserRequestId: 'unclassified-false-post',
    requestId: 'raw-unclassified-false-post',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/unclassified-read',
    status: 200,
    request: {},
    response: {},
    startedAt: 10,
    completedAt: 11,
    isMutation: false,
    atMonotonicMs: 10,
    atEpochMs: 10,
    mountEpoch: 1,
  });
  assert.throws(() => compileTemporalFilterJourneyTrace(unknownFalsePost), /no temporal-filter path allowlist entry/u);

  const rawReadTamper = makeJourney('date');
  rawReadTamper.temporalFilterLifecycle.builderReads[5].draftDigest = 'altered-without-normalized-update';
  assert.throws(() => compileTemporalFilterJourneyTrace(rawReadTamper), /before snapshot must resolve from its actual raw Builder read index/u);

  const rawDOMTamper = makeJourney('date');
  rawDOMTamper.temporalFilterLifecycle.domObservations[3].rowIds = ['dev-patient-001'];
  assert.throws(() => compileTemporalFilterJourneyTrace(rawDOMTamper), /DOM observation must resolve from its actual raw DOM index/u);
});

test('synthetic DATE and DATE_TIME adapter captures pass their generated TLA trace instances', { timeout: 30_000 }, () => {
  for (const [caseName, suffix] of [['date', 'DatePositive'], ['date-time', 'DateTimePositive']]) {
    const result = runTlc(makeJourney(caseName), suffix);
    assert.equal(result.status, 0, result.output);
    assert.match(result.output, /Model checking completed\. No error has been found\./u, result.output);
    const stateCount = /([0-9,]+) states generated, ([0-9,]+) distinct states found/u.exec(result.output);
    assert(stateCount, result.output);
    console.log(`synthetic ${caseName} trace: ${stateCount[1]} states, ${stateCount[2]} distinct; all semantic actions reached completion.`);
  }
});

test('TLA rejects stale proposal ownership, stale draft CAS, and reordered adapter events', { timeout: 30_000 }, () => {
  const wrongOwner = makeJourney('date');
  const ownerApply = wrongOwner.actions.find(event => event.action === 'FilterApplied(create)');
  ownerApply.request.body.commands[0].proposalId = 'stale-proposal';
  rawRequestFor(wrongOwner, ownerApply).request.commands[0].proposalId = 'stale-proposal';
  const ownerResult = runTlc(wrongOwner, 'WrongOwner');
  assert.notEqual(ownerResult.status, 0, ownerResult.output);
  assert.match(ownerResult.output, /Temporal property EventuallyComplete violated|Deadlock reached/u, ownerResult.output);
  assert.equal([...ownerResult.output.matchAll(/cursor \|-> (\d+)/gu)].at(-1)?.[1], '7', ownerResult.output);

  const staleCAS = makeJourney('date');
  const casApply = staleCAS.actions.find(event => event.action === 'FilterApplied(create)');
  casApply.request.body.expectedDraftVersion += 1;
  rawRequestFor(staleCAS, casApply).request.expectedDraftVersion += 1;
  const casResult = runTlc(staleCAS, 'WrongCas');
  assert.notEqual(casResult.status, 0, casResult.output);
  assert.match(casResult.output, /Temporal property EventuallyComplete violated|Deadlock reached/u, casResult.output);
  assert.equal([...casResult.output.matchAll(/cursor \|-> (\d+)/gu)].at(-1)?.[1], '7', casResult.output);

  const wrongOrder = makeJourney('date');
  [wrongOrder.actions[3], wrongOrder.actions[4]] = [wrongOrder.actions[4], wrongOrder.actions[3]];
  [wrongOrder.temporalFilterLifecycle.steps[3], wrongOrder.temporalFilterLifecycle.steps[4]] =
    [wrongOrder.temporalFilterLifecycle.steps[4], wrongOrder.temporalFilterLifecycle.steps[3]];
  const orderResult = runTlc(wrongOrder, 'WrongOrder');
  assert.notEqual(orderResult.status, 0, orderResult.output);
  assert.match(orderResult.output, /InvSemanticActionOrder is equal to FALSE/u, orderResult.output);
});
