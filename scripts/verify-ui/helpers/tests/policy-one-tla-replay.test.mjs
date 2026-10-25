import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  compilePolicyOneJourneyTrace,
  policyOneTlaReplayModelPath,
  validatePolicyOneJourneyTrace,
} from '../policy-one-tla-replay.mjs';
import { makeSyntheticPolicyOneJourneyTrace } from './fixtures/policy-one-journey.mjs';

const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';
const DEFAULT_TLC_JAR = '/private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-builder-interactions-tools/tla2tools-1.7.4.jar';
const TLC_JAR = process.env.LOOM_TLA_TOOLS_JAR ?? DEFAULT_TLC_JAR;
const STANDALONE_CFG = fileURLToPath(new URL('../../../../verification/builder-interactions/PolicyOneDisagreement.cfg', import.meta.url));

function summary(output) {
  return output.split('\n').filter(line => /Error:|Invariant .*violated|Parse Error|Encountered|Was expecting|Attempted|nonexistent field|Semantic processing|Could not parse|Model checking completed|states generated|complete state graph/.test(line))
    .slice(0, 12).join('\n');
}

function checkWithTLC(trace, name) {
  assert(existsSync(TLC_JAR), `TLC 1.7.4 jar must exist at ${TLC_JAR}`);
  const digest = createHash('sha256').update(readFileSync(TLC_JAR)).digest('hex');
  assert.equal(digest, TLC_SHA256, 'TLC jar must match the pinned 1.7.4 digest');
  const compiled = compilePolicyOneJourneyTrace(trace, { moduleName: name });
  const runDir = mkdtempSync(join(tmpdir(), 'loom-policy-one-tlc-'));
  const statesDir = join(runDir, 'states');
  mkdirSync(statesDir);
  writeFileSync(join(runDir, 'PolicyOneDisagreement.tla'), compiled.baseModelText);
  writeFileSync(join(runDir, `${compiled.moduleName}.tla`), compiled.modelText);
  writeFileSync(join(runDir, `${compiled.moduleName}.cfg`), compiled.configText);
  const result = spawnSync('java', [
    '-XX:+UseParallelGC', '-cp', TLC_JAR, 'tlc2.TLC', '-workers', '1',
    '-metadir', statesDir, '-config', `${compiled.moduleName}.cfg`, `${compiled.moduleName}.tla`,
  ], { cwd: runDir, encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
  const status = result.status;
  rmSync(runDir, { recursive: true, force: true });
  return { status, output, compiled, error: result.error };
}

function checkStandaloneModelWithTLC() {
  assert(existsSync(TLC_JAR), `TLC 1.7.4 jar must exist at ${TLC_JAR}`);
  const digest = createHash('sha256').update(readFileSync(TLC_JAR)).digest('hex');
  assert.equal(digest, TLC_SHA256, 'TLC jar must match the pinned 1.7.4 digest');
  const runDir = mkdtempSync(join(tmpdir(), 'loom-policy-one-standalone-tlc-'));
  const statesDir = join(runDir, 'states');
  mkdirSync(statesDir);
  writeFileSync(join(runDir, 'PolicyOneDisagreement.tla'), readFileSync(policyOneTlaReplayModelPath, 'utf8'));
  writeFileSync(join(runDir, 'PolicyOneDisagreement.cfg'), readFileSync(STANDALONE_CFG, 'utf8'));
  const result = spawnSync('java', [
    '-XX:+UseParallelGC', '-cp', TLC_JAR, 'tlc2.TLC', '-workers', '1',
    '-metadir', statesDir, '-config', 'PolicyOneDisagreement.cfg', 'PolicyOneDisagreement.tla',
  ], { cwd: runDir, encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
  rmSync(runDir, { recursive: true, force: true });
  return { status: result.status, output, error: result.error };
}

test('compiler specializes the base witness to actual trace constants and emits replay checks', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  assert.equal(validatePolicyOneJourneyTrace(trace), trace);
  const compiled = compilePolicyOneJourneyTrace(trace);
  const groupApply = trace.actions.find(entry => entry.label === 'GroupApplySubmitted');
  const groupApplied = trace.actions.find(entry => entry.label === 'GroupApplied');
  const allApplied = trace.actions.find(entry => entry.label === 'AllApplied').observation.saved;
  const removalApplied = trace.actions.find(entry => entry.label === 'RemovalApplied');
  assert.equal(Object.hasOwn(groupApply.request.body, 'outputId'), false,
    'commands API outputId belongs on the command DTO');
  assert.equal(groupApply.request.body.commands[0].outputId, 'output-1');
  assert.equal(groupApplied.savedBefore.draftDigest, 'draft-root');
  assert.equal(groupApplied.savedAfter.draftDigest, 'draft-before');
  assert.deepEqual(groupApplied.savedAfter.group.rowValues, [],
    'saved Group row values come from the top-level ConstructionStep.rowValues DTO');
  assert.equal(removalApplied.savedBefore.group.rowValues[0].policy, 'ALL');
  assert.equal(allApplied.group.inputColumnBinding.columnId, 'patient-id-values');
  assert.equal(allApplied.group.outputColumnBinding.columnId, 'row_value_d953ae59403369dab2d0011c');
  assert.notEqual(allApplied.group.inputColumnBinding.columnId, allApplied.group.outputColumnBinding.columnId);
  assert.equal(allApplied.group.outputColumnBinding.type, 'array');
  assert.equal(removalApplied.savedAfter.group, null,
    'removal mutates the saved Group while retaining the added source column');
  assert(removalApplied.savedAfter.sourceColumns.some(column => column.id === 'patient-id-values'));
  assert.match(compiled.modelText, /ObservationMatches\(traceIndex \+ 1, model, model'\)/);
  assert.match(compiled.modelText, /ObservedResponseOwnership/);
  assert.match(compiled.modelText, /ObservedPolicyCAS/);
  assert.match(compiled.configText, /OneCaptureId = "capture-one-random"/);
  assert.match(compiled.configText, /AllCaptureId = "capture-all-random"/);
  assert.match(compiled.configText, /AddedPatientIdOutputColumnId = "row_value_d953ae59403369dab2d0011c"/);
  assert.match(compiled.baseModelText, /SourcePatientIds == \{"dev-patient-001", "dev-patient-002"\}/);
  assert.match(compiled.claim, /no concurrency or exhaustive interleaving claim/);
});

test('the standalone policy witness requires eventual restoration under weak fairness', t => {
  const result = checkStandaloneModelWithTLC();
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(result.status, 0, summary(result.output));
  assert.match(result.output, /Model checking completed\. No error has been found\./);
});

test('TLC replays the complete synthetic lifecycle against captured workspace and protocol facts', t => {
  const result = checkWithTLC(makeSyntheticPolicyOneJourneyTrace(), 'PolicyOneReplayPositive');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(result.status, 0, summary(result.output));
  assert.match(result.output, /Model checking completed\. No error has been found\./);
});

test('TLC rejects a response whose captured owner differs from the submitted request', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  trace.actions.find(entry => entry.label === 'OneRejected').response.requestObjectId = 'wrong-owner';
  assert.equal(validatePolicyOneJourneyTrace(trace), trace,
    'semantic owner mutations must reach TLC instead of being rejected by the shape validator');
  const result = checkWithTLC(trace, 'PolicyOneReplayWrongOwner');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.notEqual(result.status, 0, result.output);
  assert.match(result.output, /ObservedResponseOwnership/);
});

test('TLC rejects an ALL proposal carrying a stale draft digest', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  trace.actions.find(entry => entry.label === 'AllSubmitted').request.body.expectedDraftDigest = 'stale-draft';
  assert.equal(validatePolicyOneJourneyTrace(trace), trace,
    'semantic CAS mutations must reach TLC instead of being rejected by the shape validator');
  const result = checkWithTLC(trace, 'PolicyOneReplayStaleCas');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.notEqual(result.status, 0, result.output);
  assert.match(result.output, /ObservedPolicyCAS/);
});

test('TLC rejects an out-of-order repair response', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const submitted = trace.actions.findIndex(entry => entry.label === 'AllSubmitted');
  const ready = trace.actions.findIndex(entry => entry.label === 'AllReady');
  [trace.actions[submitted], trace.actions[ready]] = [trace.actions[ready], trace.actions[submitted]];
  assert.equal(validatePolicyOneJourneyTrace(trace), trace,
    'chronology mutations must reach TLC instead of being rejected by the shape validator');
  const result = checkWithTLC(trace, 'PolicyOneReplayOutOfOrder');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.notEqual(result.status, 0, result.output);
  assert.match(result.output, /ObservedChronology|TraceProgress/);
});

test('TLC rejects a post-removal saved snapshot that disagrees with the observed terminal workspace', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  trace.actions.find(entry => entry.label === 'RemovalApplied').savedAfter.draftDigest = 'wrong-restored-draft';
  assert.equal(validatePolicyOneJourneyTrace(trace), trace,
    'semantic saved-state mutations must reach TLC instead of being rejected by the shape validator');
  const result = checkWithTLC(trace, 'PolicyOneReplayWrongSavedAfter');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.notEqual(result.status, 0, result.output);
  assert.match(result.output, /TraceProgress/);
});

test('TLC rejects a saved row value whose output differs from its declared Group output', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const savedAllGroup = trace.actions.find(entry => entry.label === 'RemovalApplied').savedBefore.group;
  savedAllGroup.rowValues[0].outputColumnId = 'row_value_unlinked';
  assert.equal(validatePolicyOneJourneyTrace(trace), trace,
    'semantic Group output mutations must reach TLC instead of being rejected by the shape validator');
  const result = checkWithTLC(trace, 'PolicyOneReplayWrongRowValueOutput');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.notEqual(result.status, 0, result.output);
  assert.match(result.output, /TraceProgress/);
});

test('TLC rejects an ALL row-value output declaration that is not an array', t => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const savedAllGroup = trace.actions.find(entry => entry.label === 'RemovalApplied').savedBefore.group;
  savedAllGroup.outputs.find(output => output.id === savedAllGroup.rowValues[0].outputColumnId).type = 'string';
  assert.equal(validatePolicyOneJourneyTrace(trace), trace,
    'semantic output type mutations must reach TLC instead of being rejected by the shape validator');
  const result = checkWithTLC(trace, 'PolicyOneReplayScalarAllOutput');
  t.diagnostic(summary(result.output));
  assert.equal(result.error, undefined, result.error?.message);
  assert.notEqual(result.status, 0, result.output);
  assert.match(result.output, /TraceProgress/);
});
