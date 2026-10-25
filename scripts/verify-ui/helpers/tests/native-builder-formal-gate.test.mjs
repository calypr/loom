import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { currentFormalJourneyEvidence } from '../../../generate-builder-browser-evidence.mjs';
import { validateNativeBracketEnvelope, validateNativeBracketSummary, validateNativeTlaReceipt } from '../../../run-builder-journey.mjs';

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const canonicalJson = (value) => Array.isArray(value)
  ? '[' + value.map(canonicalJson).join(',') + ']'
  : value && typeof value === 'object'
    ? '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}'
    : JSON.stringify(value);
const copy = (value) => structuredClone(value);

const makeBracketFixture = (root) => {
  const selection = {
    scenarioID: 'standalone-reshape-group-edit-before-related-column',
    caseName: 'group-edit-before-related-column',
    expectedIdentity: { project: 'loom_dev_cda_fhir' },
    requiredChecks: ['native lifecycle assertion'],
    traceKind: 'native-lifecycle',
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    model: 'BuilderJourney',
  };
  const fingerprint = { sha256: 'a'.repeat(64), files: 3 };
  const controls = [{ name: 'wrong-related-binding', status: 'pass' }];
  const traceProof = {
    status: 'pass',
    replayStatus: 'pass',
    selfTestStatus: 'pass',
    traceName: selection.traceName,
    nativeCaseKey: selection.nativeCaseKey,
    requestCoverage: { observed: 2, modeled: 2, unmapped: 0, duplicates: 0 },
    requiredActionCountsByTrace: { mismatches: {} },
    requiredActionMinimumsByTrace: { mismatches: {} },
    unmodeledEvents: { unclassified: { total: 0 } },
    mutationControls: controls,
  };
  const domainReport = {
    status: 'passed',
    scenario: selection.scenarioID,
    case: selection.caseName,
    target: {
      project: 'loom_dev_cda_fhir',
      generation: 'cda-fhir-v1',
      sourceFingerprint: fingerprint,
      apiBuildIdentity: 'api-build-17',
      builderJourneyTrace: traceProof,
    },
    requiredChecks: selection.requiredChecks,
    missingRequiredChecks: [],
    assertions: [{ name: selection.requiredChecks[0], status: 'passed' }],
  };
  const domainReportPath = join(root, 'domain-report.json');
  writeFileSync(domainReportPath, JSON.stringify(domainReport));
  const summary = {
    status: 'passed',
    nativeStatus: 'passed',
    scenario: selection.scenarioID,
    case: selection.caseName,
    integrity: {
      status: 'PASS',
      dimensions: {
        source: { status: 'PASS', before: fingerprint, after: fingerprint, changedPaths: [] },
        docs: { status: 'PASS', before: fingerprint, after: fingerprint, changedPaths: [] },
        apiBuildIdentity: { status: 'PASS', before: 'api-build-17', after: 'api-build-17' },
        ownedMounts: { status: 'PASS' },
        healthBefore: { status: 'PASS' },
        healthAfter: { status: 'PASS' },
      },
    },
    targetValidation: { registryBinding: 'bound', configPath: join(root, 'owned-target.json') },
    targetIdentity: { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' },
    lifecycle: {
      reportStatus: 'passed',
      reportIdentityMatchesSelection: true,
      requiredCheckCount: 1,
      passedCheckCount: 1,
      failedCheckNames: [],
      missingCheckNames: [],
      dimensions: { usability: 'passed', correctness: 'passed', persistence: 'passed', performance: 'passed' },
    },
    evidence: { domainReport: domainReportPath },
  };
  const summaryPath = join(root, 'summary.json');
  summary.evidence.summary = summaryPath;
  writeFileSync(summaryPath, JSON.stringify(summary));
  return { selection, fingerprint, controls, domainReport, domainReportPath, summary };
};

const makeTlaFixture = (root) => {
  const selection = {
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    model: 'BuilderJourney',
  };
  const sourceCommit = 'b'.repeat(40);
  const sourceFingerprint = 'c'.repeat(64);
  const contractSourceCommit = 'd'.repeat(40);
  const trace = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traces: [{ name: selection.traceName, model: selection.model, actions: [{ label: 'Reload' }] }],
  };
  const tracePath = join(root, 'trace.json');
  writeFileSync(tracePath, JSON.stringify(trace));
  const tla = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traceCount: 1, uniqueTraceCount: 1, validatedCount: 1, invalidCount: 0,
    coverage: {
      requiredModels: { missing: [] }, requiredActions: { missing: [] },
      requiredActionsByTrace: { missing: {} }, requiredEvents: { missing: [] },
    },
    unmodeledEvents: { unclassified: { total: 0 } },
    traces: [{ name: selection.traceName, model: selection.model, status: 'pass', actionCount: 1 }],
    builderJourneyTrace: {
      traceName: selection.traceName, nativeCaseKey: selection.nativeCaseKey,
      replayStatus: 'pass', status: 'pending-self-test', requestCoverage: { observed: 2, modeled: 2, unmapped: 0, duplicates: 0 },
      requiredActionCountsByTrace: { mismatches: {} }, requiredActionMinimumsByTrace: { mismatches: {} },
      unmodeledEvents: { unclassified: { total: 0 } },
    },
  };
  return { selection, sourceCommit, sourceFingerprint, contractSourceCommit, tracePath, tla };
};

test('native Builder proof gates accept a complete retained receipt and reject lifecycle, source, or mutation drift', (t) => {
  const root = mkdtempSync(join(tmpdir(), 'loom-native-formal-gate-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bracket = makeBracketFixture(root);
  assert.equal(validateNativeBracketSummary(bracket.summary, bracket.selection, bracket.summary.targetValidation.configPath).status, 'passed');
  const envelope = {
    status: 'passed',
    scenario: bracket.selection.scenarioID,
    case: bracket.selection.caseName,
    summary: bracket.summary.evidence.summary,
    report: bracket.domainReportPath,
  };
  assert.equal(validateNativeBracketEnvelope(envelope, bracket.selection, bracket.summary.targetValidation.configPath).status, 'passed');
  assert.equal(validateNativeBracketEnvelope({ ...envelope, summary: join(root, 'missing-summary.json') }, bracket.selection,
    bracket.summary.targetValidation.configPath).status, 'failed');
  assert.equal(validateNativeBracketEnvelope({ ...envelope, scenario: 'wrong-scenario' }, bracket.selection,
    bracket.summary.targetValidation.configPath).status, 'failed');

  const failedCheck = copy(bracket.summary);
  failedCheck.lifecycle.passedCheckCount = 0;
  failedCheck.lifecycle.failedCheckNames = ['native lifecycle assertion'];
  assert.equal(validateNativeBracketSummary(failedCheck, bracket.selection, failedCheck.targetValidation.configPath).status, 'failed');

  const missingAssertion = copy(bracket.domainReport);
  missingAssertion.assertions = [];
  writeFileSync(bracket.domainReportPath, JSON.stringify(missingAssertion));
  assert.equal(validateNativeBracketSummary(bracket.summary, bracket.selection, bracket.summary.targetValidation.configPath).status, 'failed');

  const wrongFingerprint = copy(bracket.domainReport);
  wrongFingerprint.target.sourceFingerprint.sha256 = 'e'.repeat(64);
  writeFileSync(bracket.domainReportPath, JSON.stringify(wrongFingerprint));
  assert.equal(validateNativeBracketSummary(bracket.summary, bracket.selection, bracket.summary.targetValidation.configPath).status, 'failed');

  const missingMutationReceipt = copy(bracket.domainReport);
  missingMutationReceipt.target.builderJourneyTrace.mutationControls = [];
  writeFileSync(bracket.domainReportPath, JSON.stringify(missingMutationReceipt));
  assert.equal(validateNativeBracketSummary(bracket.summary, bracket.selection, bracket.summary.targetValidation.configPath).status, 'failed');

  const tla = makeTlaFixture(root);
  assert.equal(validateNativeTlaReceipt(tla).status, 'passed');
  const wrongTlaFingerprint = copy(tla);
  wrongTlaFingerprint.tla.sourceFingerprint = 'f'.repeat(64);
  assert.equal(validateNativeTlaReceipt(wrongTlaFingerprint).status, 'failed');
});

const makeEvidenceFixture = (root) => {
  const traceName = 'cda-builder-group-related-source';
  const nativeCaseKey = 'scenario/case';
  const model = 'BuilderJourney';
  const sourceCommit = '1'.repeat(40);
  const contractSourceCommit = '2'.repeat(40);
  const sourcePath = 'proof-source.txt';
  const sourceBytes = Buffer.from('retained proof source');
  writeFileSync(join(root, sourcePath), sourceBytes);
  const sourceHash = sha256(sourceBytes);
  mkdirSync(join(root, 'verification/builder-interactions'), { recursive: true });
  writeFileSync(join(root, 'verification/builder-interactions/builder-preview-contract.json'), JSON.stringify({
    sources: [{ path: sourcePath, sha256: sourceHash }],
  }));
  const contractPath = 'verification/builder-interactions/cda-contract.json';
  writeFileSync(join(root, contractPath), JSON.stringify({ sourceCommit: contractSourceCommit }));
  const sourceFingerprint = sha256(canonicalJson([{ path: sourcePath, sha256: sourceHash }]));
  const controls = [{ name: 'wrong-related-binding', status: 'pass' }];
  const requestCoverage = { observed: 2, modeled: 2, unmapped: 0, duplicates: 0 };
  const counts = { mismatches: {} };
  const minimums = { mismatches: {} };
  const unmodeled = { unclassified: { total: 0 } };
  const tracePath = join(root, 'trace.json');
  const replayReportPath = join(root, 'tla.json');
  const selfTestReportPath = join(root, 'self-test.json');
  const evidence = {
    status: 'pass', traceName, nativeCaseKey, replayStatus: 'pass', selfTestStatus: 'pass',
    tracePath, replayReportPath, selfTestReportPath,
    requestCoverage, requiredActionCountsByTrace: counts, requiredActionMinimumsByTrace: minimums,
    unmodeledEvents: unmodeled, mutationControls: controls, actionLabels: ['Reload'],
  };
  const trace = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traces: [{ name: traceName, model, status: 'pass', actions: [{ label: 'Reload' }] }],
  };
  const replay = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traceCount: 1, uniqueTraceCount: 1, validatedCount: 1, invalidCount: 0,
    coverage: {
      requiredModels: { missing: [] }, requiredActions: { missing: [] },
      requiredActionsByTrace: { missing: {} }, requiredEvents: { missing: [] },
    },
    unmodeledEvents: unmodeled,
    traces: [{ name: traceName, model, status: 'pass', actionCount: 1 }],
    builderJourneyTrace: {
      traceName, nativeCaseKey, replayStatus: 'pass', status: 'pending-self-test',
      tracePath: evidence.tracePath, replayReportPath: evidence.replayReportPath,
      actionLabels: ['Reload'], requestCoverage, requiredActionCountsByTrace: counts,
      requiredActionMinimumsByTrace: minimums, unmodeledEvents: unmodeled,
    },
  };
  const selfTest = {
    status: 'pass', traceName, nativeCaseKey, tracePath: evidence.tracePath,
    selfTestReportPath: evidence.selfTestReportPath, mutationControls: controls,
  };
  writeFileSync(evidence.tracePath, JSON.stringify(trace));
  writeFileSync(evidence.replayReportPath, JSON.stringify(replay));
  writeFileSync(evidence.selfTestReportPath, JSON.stringify(selfTest));
  const reportPath = 'native-report.json';
  const report = { scenario: 'scenario', case: 'case', target: { builderJourneyTrace: evidence } };
  writeFileSync(join(root, reportPath), JSON.stringify(report));
  const registeredCase = {
    journeyTrace: {
      kind: 'native-lifecycle', name: traceName, model, contract: contractPath,
      requiredActions: ['Reload'], requiredActionCounts: { Reload: 1 },
      minimumActionCounts: {}, optionalActions: [],
    },
  };
  const sourceStatus = { status: 'current' };
  return { root, evidence, report, reportPath, registeredCase, sourceStatus, selfTest, replay };
};

test('current browser evidence requires the retained replay receipt and passing mutation controls', (t) => {
  const root = mkdtempSync(join(tmpdir(), 'loom-current-formal-evidence-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const fixture = makeEvidenceFixture(root);
  assert.equal(currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath).status, 'current');

  const missingControls = copy(fixture.selfTest);
  missingControls.mutationControls = [];
  writeFileSync(fixture.evidence.selfTestReportPath, JSON.stringify(missingControls));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /mutation receipt is missing passing negative controls/);

  const failedControl = copy(fixture.selfTest);
  failedControl.mutationControls[0].status = 'fail';
  writeFileSync(fixture.evidence.selfTestReportPath, JSON.stringify(failedControl));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /mutation receipt is missing passing negative controls/);

  const missingReplayCoverage = copy(fixture.replay);
  missingReplayCoverage.builderJourneyTrace.requestCoverage.unmapped = 1;
  writeFileSync(fixture.evidence.replayReportPath, JSON.stringify(missingReplayCoverage));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /incomplete or duplicated native request coverage/);
});
