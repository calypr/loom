import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { currentFormalJourneyEvidence } from '../../../generate-builder-browser-evidence.mjs';
import {
  resolveNativeEvidenceParent,
  retainNativeDomainReportArtifact,
  validateNativeBracketEnvelope,
  validateNativeBracketSummary,
  validateNativeTlaReceipt,
} from '../../../run-builder-journey.mjs';
import { cdaMutatingRequestCoverageProblem, cdaRequestCoverageReceipt } from '../../../verify-builder-traces.mjs';

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const canonicalJson = (value) => Array.isArray(value)
  ? '[' + value.map(canonicalJson).join(',') + ']'
  : value && typeof value === 'object'
    ? '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}'
    : JSON.stringify(value);
const copy = (value) => structuredClone(value);

const makeBracketFixture = (root) => {
  const contract = JSON.parse(readFileSync(new URL('../../../../verification/builder-interactions/builder-cda-group-related-contract.json', import.meta.url), 'utf8'));
  const selection = {
    scenarioID: 'standalone-reshape-group-edit-before-related-column',
    caseName: 'group-edit-before-related-column',
    contract: 'verification/builder-interactions/builder-cda-group-related-contract.json',
    expectedIdentity: { project: 'loom_dev_cda_fhir' },
    requiredChecks: ['native lifecycle assertion'],
    traceKind: 'native-lifecycle',
    traceName: 'cda-builder-group-related-source',
    nativeCaseKey: 'standalone-reshape-group-edit-before-related-column/group-edit-before-related-column',
    model: 'BuilderJourney',
  };
  const fingerprint = { sha256: 'a'.repeat(64), files: 3 };
  const owner = { kind: 'proposal', browserRequestId: 'native-request-1', mountEpoch: 0 };
  const nativeTrace = {
    traces: [{
      actions: [{ label: 'Preview', groupLifecycle: { owner } }],
      nativeRequests: [{
        index: 0, browserRequestId: owner.browserRequestId, requestId: 'request-1', method: 'POST',
        path: '/authoring/v2/construction-proposals', startedAt: 1, isMutation: true,
      }],
    }],
  };
  const nativeTraceBytes = Buffer.from(JSON.stringify(nativeTrace));
  const tracePath = join(root, 'native-trace.json');
  writeFileSync(tracePath, nativeTraceBytes);
  const traceSha256 = sha256(nativeTraceBytes);
  const controls = [
    ...contract.journeyCase.requiredMutationControlNames,
    contract.journeyCase.observedOwnerEpochControlPrefix + owner.mountEpoch,
  ].map(name => ({ name, status: 'pass' }));
  const traceProof = {
    status: 'pass',
    replayStatus: 'pass',
    selfTestStatus: 'pass',
    traceName: selection.traceName,
    nativeCaseKey: selection.nativeCaseKey,
    tracePath,
    traceSha256,
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
  return { selection, fingerprint, controls, domainReport, domainReportPath, summary, tracePath, traceSha256 };
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
  const traceBytes = Buffer.from(JSON.stringify(trace));
  writeFileSync(tracePath, traceBytes);
  const traceSha256 = sha256(traceBytes);
  const tla = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traceSha256,
    traceCount: 1, uniqueTraceCount: 1, validatedCount: 1, invalidCount: 0,
    coverage: {
      requiredModels: { missing: [] }, requiredActions: { missing: [] },
      requiredActionsByTrace: { missing: {} }, requiredEvents: { missing: [] },
    },
    unmodeledEvents: { unclassified: { total: 0 } },
    traces: [{ name: selection.traceName, model: selection.model, status: 'pass', actionCount: 1 }],
    builderJourneyTrace: {
      traceName: selection.traceName, nativeCaseKey: selection.nativeCaseKey,
      traceSha256,
      replayStatus: 'pass', status: 'pending-self-test', requestCoverage: { observed: 2, modeled: 2, unmapped: 0, duplicates: 0 },
      requiredActionCountsByTrace: { mismatches: {} }, requiredActionMinimumsByTrace: { mismatches: {} },
      unmodeledEvents: { unclassified: { total: 0 } },
    },
  };
  return { selection, sourceCommit, sourceFingerprint, contractSourceCommit, tracePath, traceBytes, traceSha256, tla };
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
  const changedTrace = JSON.parse(readFileSync(tla.tracePath, 'utf8'));
  changedTrace.traces[0].actions[0].label = 'Preview';
  writeFileSync(tla.tracePath, JSON.stringify(changedTrace));
  assert.equal(validateNativeTlaReceipt(tla).status, 'failed');
  writeFileSync(tla.tracePath, tla.traceBytes);
  const wrongTlaFingerprint = copy(tla);
  wrongTlaFingerprint.tla.sourceFingerprint = 'f'.repeat(64);
  assert.equal(validateNativeTlaReceipt(wrongTlaFingerprint).status, 'failed');
});

const makeEvidenceFixture = (root) => {
  const traceName = 'cda-builder-group-related-source';
  const nativeCaseKey = 'scenario/case';
  const model = 'BuilderJourney';
  const sourceCommit = '1'.repeat(40);
  const sourcePath = 'proof-source.txt';
  const sourceBytes = Buffer.from('retained proof source');
  writeFileSync(join(root, sourcePath), sourceBytes);
  const sourceHash = sha256(sourceBytes);
  mkdirSync(join(root, 'verification/builder-interactions'), { recursive: true });
  writeFileSync(join(root, 'verification/builder-interactions/builder-preview-contract.json'), JSON.stringify({
    sources: [{ path: sourcePath, sha256: sourceHash }],
  }));
  const contractPath = 'verification/builder-interactions/cda-contract.json';
  const contract = JSON.parse(readFileSync(new URL('../../../../verification/builder-interactions/builder-cda-group-related-contract.json', import.meta.url), 'utf8'));
  const contractSourceCommit = contract.sourceCommit;
  writeFileSync(join(root, contractPath), JSON.stringify(contract));
  const sourceFingerprint = sha256(canonicalJson([{ path: sourcePath, sha256: sourceHash }]));
  const observedEpoch = 1;
  const controls = [
    ...contract.journeyCase.requiredMutationControlNames,
    contract.journeyCase.observedOwnerEpochControlPrefix + observedEpoch,
  ].map(name => ({ name, status: 'pass' }));
  const requestCoverage = { observed: 2, modeled: 2, unmapped: 0, duplicates: 0 };
  const counts = { mismatches: {} };
  const minimums = { mismatches: {} };
  const unmodeled = { unclassified: { total: 0 } };
  const tracePath = join(root, 'trace.json');
  const replayReportPath = join(root, 'tla.json');
  const selfTestReportPath = join(root, 'self-test.json');
  const owner = { kind: 'proposal', browserRequestId: 'native-request-1', mountEpoch: observedEpoch };
  const evidence = {
    status: 'pass', traceName, nativeCaseKey, replayStatus: 'pass', selfTestStatus: 'pass',
    tracePath, replayReportPath, selfTestReportPath,
    requestCoverage, requiredActionCountsByTrace: counts, requiredActionMinimumsByTrace: minimums,
    unmodeledEvents: unmodeled, mutationControls: controls, actionLabels: ['Preview', 'Reload'],
  };
  const trace = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traces: [{ name: traceName, model, status: 'pass', actions: [
      { label: 'Preview', groupLifecycle: { owner } },
      { label: 'Reload' },
    ], nativeRequests: [{
      index: 0, browserRequestId: owner.browserRequestId, requestId: 'request-1', method: 'POST',
      path: '/authoring/v2/construction-proposals', startedAt: 1, isMutation: true,
    }] }],
  };
  const traceBytes = Buffer.from(JSON.stringify(trace));
  const traceSha256 = sha256(traceBytes);
  evidence.traceSha256 = traceSha256;
  const replay = {
    sourceProfile: 'journey', sourceCommit, sourceFingerprint, contractSourceCommit,
    traceSha256,
    traceCount: 1, uniqueTraceCount: 1, validatedCount: 1, invalidCount: 0,
    coverage: {
      requiredModels: { missing: [] }, requiredActions: { missing: [] },
      requiredActionsByTrace: { missing: {} }, requiredEvents: { missing: [] },
    },
    unmodeledEvents: unmodeled,
    traces: [{ name: traceName, model, status: 'pass', actionCount: 2 }],
    builderJourneyTrace: {
      traceName, nativeCaseKey, replayStatus: 'pass', status: 'pending-self-test',
      traceSha256,
      tracePath: evidence.tracePath, replayReportPath: evidence.replayReportPath,
      actionLabels: ['Preview', 'Reload'], requestCoverage, requiredActionCountsByTrace: counts,
      requiredActionMinimumsByTrace: minimums, unmodeledEvents: unmodeled,
    },
  };
  const selfTest = {
    status: 'pass', traceName, nativeCaseKey, tracePath: evidence.tracePath,
    traceSha256, selfTestReportPath: evidence.selfTestReportPath, mutationControls: controls,
  };
  writeFileSync(evidence.tracePath, traceBytes);
  writeFileSync(evidence.replayReportPath, JSON.stringify(replay));
  writeFileSync(evidence.selfTestReportPath, JSON.stringify(selfTest));
  const reportPath = 'native-report.json';
  const report = { scenario: 'scenario', case: 'case', target: { builderJourneyTrace: evidence } };
  writeFileSync(join(root, reportPath), JSON.stringify(report));
  const registeredCase = {
    journeyTrace: {
      kind: 'native-lifecycle', name: traceName, model, contract: contractPath,
      requiredActions: ['Preview', 'Reload'], requiredActionCounts: { Reload: 1 },
      minimumActionCounts: { Preview: 1 }, optionalActions: [],
    },
  };
  const sourceStatus = { status: 'current' };
  return { root, evidence, report, reportPath, registeredCase, sourceStatus, selfTest, replay, traceBytes };
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

  const substitutedControl = copy(fixture.selfTest);
  substitutedControl.mutationControls[0].name = 'invented-control';
  writeFileSync(fixture.evidence.selfTestReportPath, JSON.stringify(substitutedControl));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /mutation controls differ from the contract inventory/);

  const duplicateControl = copy(fixture.selfTest);
  duplicateControl.mutationControls[1].name = duplicateControl.mutationControls[0].name;
  writeFileSync(fixture.evidence.selfTestReportPath, JSON.stringify(duplicateControl));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /mutation controls differ from the contract inventory/);

  const missingReplayCoverage = copy(fixture.replay);
  missingReplayCoverage.builderJourneyTrace.requestCoverage.unmapped = 1;
  writeFileSync(fixture.evidence.replayReportPath, JSON.stringify(missingReplayCoverage));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /incomplete or duplicated native request coverage/);

  writeFileSync(fixture.evidence.replayReportPath, JSON.stringify(fixture.replay));
  writeFileSync(fixture.evidence.selfTestReportPath, JSON.stringify(fixture.selfTest));
  const tamperedTrace = JSON.parse(fixture.traceBytes.toString('utf8'));
  tamperedTrace.traces[0].actions[0].label = 'PreviewError';
  writeFileSync(fixture.evidence.tracePath, JSON.stringify(tamperedTrace));
  assert.throws(() => currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath), /do not bind the exact retained trace bytes/);
});

test('CDA request coverage binds false-flag proposal previews and true-flag commands, but rejects unknown false POSTs', () => {
  const proposal = {
    method: 'POST', path: '/construction-proposals', browserRequestId: 'browser-proposal-1',
    captureId: 'capture-proposal-1', requestId: 'wire-proposal-1', index: 0, startedAt: 10, isMutation: false,
  };
  const command = {
    method: 'POST', path: '/commands', browserRequestId: 'browser-command-1',
    captureId: 'capture-command-1', requestId: 'wire-command-1', index: 1, startedAt: 20, isMutation: true,
  };
  const supportRead = {
    method: 'GET', path: '/builder', browserRequestId: 'browser-builder-1',
    requestId: null, index: 2, startedAt: 30, isMutation: false,
  };
  const trace = {
    actions: [
      { label: 'Preview', request: proposal },
      { label: 'PreviewComplete', request: proposal, settlement: {
        browserRequestId: proposal.browserRequestId, captureId: proposal.captureId,
        requestId: proposal.requestId, startedAt: 10, completedAt: 11,
      } },
      { label: 'Group', request: command, settlement: { startedAt: 20, completedAt: 21 } },
    ],
    unmodeledEvents: [{ kind: 'builder-read', reason: 'Reload evidence read', requests: [supportRead] }],
    nativeRequests: [
      { ...proposal, path: '/api/v1/projects/p/explorers/e/authoring/v2/construction-proposals' },
      { ...command, path: '/api/v1/projects/p/explorers/e/authoring/v2/commands' },
      { ...supportRead, path: '/api/v1/projects/p/explorers/e/authoring/v2/builder' },
    ],
  };
  const journeyCase = { readOnlySupportKinds: ['builder-read'] };
  const contract = {
    readOnlySupportRoutes: { 'builder-read': { method: 'GET', pathSuffix: '/builder' } },
    unmodeledEventPolicy: { 'builder-read': true },
  };
  assert.equal(cdaMutatingRequestCoverageProblem(trace, 'trace', journeyCase, contract), null);
  assert.deepEqual(cdaRequestCoverageReceipt(trace), { observed: 2, modeled: 2, unmapped: 0, duplicates: 0 });

  const unknownPost = copy(trace);
  unknownPost.nativeRequests.push({
    method: 'POST', path: '/api/v1/projects/p/explorers/e/authoring/v2/unknown',
    browserRequestId: 'browser-unknown-1', requestId: 'wire-unknown-1', index: 3, startedAt: 40, isMutation: false,
  });
  assert.match(cdaMutatingRequestCoverageProblem(unknownPost, 'trace', journeyCase, contract)?.message ?? '', /unknown read-only method\/path route outside the support allowlist/);
});

test('native lifecycle evidence defaults to bracket-managed external retention and rejects in-repo parents', () => {
  assert.equal(resolveNativeEvidenceParent(undefined), null);
  const externalEvidencePath = join(tmpdir(), 'loom-native-evidence-policy-test');
  assert.equal(resolveNativeEvidenceParent(externalEvidencePath), externalEvidencePath);
  assert.throws(() => resolveNativeEvidenceParent('.artifacts/builder-journey/native-case-bracket'),
    /must stay outside the watched repository root/);
});

test('native report retention copies exact bytes only from its bracket run into repository artifacts', (t) => {
  const root = mkdtempSync(join(tmpdir(), 'loom-native-report-retention-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const repositoryRoot = join(root, 'repository');
  const artifactRoot = join(repositoryRoot, '.artifacts');
  const bracketRun = join(root, 'bracket-run');
  const attachmentDirectory = join(bracketRun, 'attachments');
  mkdirSync(artifactRoot, { recursive: true });
  mkdirSync(attachmentDirectory, { recursive: true });
  const summaryPath = join(bracketRun, 'summary.json');
  const sourcePath = join(attachmentDirectory, 'native-report.json');
  const destinationPath = join(artifactRoot, 'native-report.json');
  const bytes = Buffer.from('{"status":"passed","target":{"builderJourneyTrace":{"status":"pass"}}}\n');
  writeFileSync(summaryPath, '{}\n');
  writeFileSync(sourcePath, bytes);

  const retained = retainNativeDomainReportArtifact({
    sourcePath,
    destinationPath,
    summaryPath,
    repositoryRoot,
    artifactRoot,
  });

  assert.equal(retained.path, '.artifacts/native-report.json');
  assert.equal(retained.sha256, sha256(bytes));
  assert.deepEqual(readFileSync(destinationPath), bytes);
  assert.throws(() => retainNativeDomainReportArtifact({
    sourcePath,
    destinationPath,
    summaryPath,
    repositoryRoot,
    artifactRoot,
  }), /EEXIST/);

  const outsideSource = join(root, 'unrelated-report.json');
  writeFileSync(outsideSource, bytes);
  assert.throws(() => retainNativeDomainReportArtifact({
    sourcePath: outsideSource,
    destinationPath: join(artifactRoot, 'unrelated-copy.json'),
    summaryPath,
    repositoryRoot,
    artifactRoot,
  }), /exact bracket run directory/);
  assert.throws(() => retainNativeDomainReportArtifact({
    sourcePath,
    destinationPath: join(root, 'external-copy.json'),
    summaryPath,
    repositoryRoot,
    artifactRoot,
  }), /repository \.artifacts/);
  const externalArtifactRoot = join(root, 'external-artifacts');
  mkdirSync(externalArtifactRoot);
  assert.throws(() => retainNativeDomainReportArtifact({
    sourcePath,
    destinationPath: join(externalArtifactRoot, 'external-copy.json'),
    summaryPath,
    repositoryRoot,
    artifactRoot: externalArtifactRoot,
  }), /resolve inside the repository root/);
});
