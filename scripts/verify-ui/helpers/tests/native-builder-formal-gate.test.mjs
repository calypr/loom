import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  currentFormalJourneyEvidence,
  nativeJourneyRequestCoverageProblem,
} from '../../../generate-builder-browser-evidence.mjs';
import {
  bracketSummaryFromOutput,
  nativeEnvironmentTargetProblems,
  resolveNativeEvidenceParent,
  retainNativeDomainReportArtifact,
  validateNativeBracketEnvelope,
  validateNativeJourneyTraceBinding,
  validateNativeTlaReceipt,
} from '../../../run-builder-journey.mjs';
import { nativeBuilderJourneyReceiptFromReport, resolveNativeBuilderJourneyArtifactPath } from '../builder-journey-trace.mjs';
import { registry, scenarioCaseFor } from '../../registry.mjs';
import { cdaMutatingRequestCoverageProblem, cdaRequestCoverageReceipt } from '../../../verify-builder-traces.mjs';

const REPOSITORY_ROOT = resolve(fileURLToPath(new URL('../../../../', import.meta.url)));
const makeRepositoryArtifactDirectory = (prefix) => {
  const artifacts = join(REPOSITORY_ROOT, '.artifacts');
  mkdirSync(artifacts, { recursive: true });
  return mkdtempSync(join(artifacts, prefix));
};

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const canonicalJson = (value) => Array.isArray(value)
  ? '[' + value.map(canonicalJson).join(',') + ']'
  : value && typeof value === 'object'
    ? '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}'
    : JSON.stringify(value);
const copy = (value) => structuredClone(value);

test('native request coverage accounts for setup mutations without action credit or double counting', () => {
  const valid = {
    observed: 7,
    modeled: 7,
    unmapped: 0,
    duplicates: 0,
    modeledActions: 2,
    reconcileReads: 2,
    supportedReadOnly: 2,
    setupMutations: 1,
  };
  assert.equal(nativeJourneyRequestCoverageProblem(valid), null);
  assert.match(nativeJourneyRequestCoverageProblem({
    ...valid,
    setupMutations: undefined,
  }), /setup counts/u);
  assert.match(nativeJourneyRequestCoverageProblem({
    ...valid,
    setupMutations: 2,
  }), /exactly once/u);
  assert.match(nativeJourneyRequestCoverageProblem({
    ...valid,
    setupMutations: -1,
  }), /non-negative/u);
});
const retainedBasicTargetFixture = {
  kind: 'isolated',
  project: 'loom_dev_verify_mvnpkod5-d7536dd',
  generation: 'fixture-v1',
  qaIsolation: {
    project: 'loom_dev_verify_mvnpkod5-d7536dd',
    generation: 'fixture-v1',
    fixtureDir: '$CHECKOUT/testdata/devloop-fixture',
    fixtureOracle: {
      source: 'fresh project Patient.ndjson',
      path: '$CHECKOUT/testdata/devloop-fixture/Patient.ndjson',
      project: 'loom_dev_verify_mvnpkod5-d7536dd',
      generation: 'fixture-v1',
      resourceType: 'Patient',
      patientIDs: ['dev-patient-001', 'dev-patient-002'],
      expectedCountRows: 2,
    },
  },
};

test('environment-only formal bracket accepts its unbound scope and binds the isolated fixture identity', (t) => {
  const { project, generation } = retainedBasicTargetFixture;
  const root = makeRepositoryArtifactDirectory('native-environment-target-');
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bracket = makeBracketFixture(root);
  const selection = { ...bracket.selection, targetMode: 'environment' };
  delete selection.expectedIdentity;
  const summary = copy(bracket.summary);
  summary.targetIdentity = null;
  summary.targetValidation = {
    scope: 'environment-only',
    registryBinding: 'unbound',
    runtimeDatasetIdentity: 'not-checked',
    configPath: null,
    project: null,
    generation: null,
  };
  const domainReport = copy(bracket.domainReport);
  domainReport.target = {
    ...domainReport.target,
    ...copy(retainedBasicTargetFixture),
  };
  writeFileSync(bracket.summaryPath, JSON.stringify(summary));
  writeFileSync(bracket.domainReportPath, JSON.stringify(domainReport));
  const tracePaths = {
    tracePath: bracket.tracePath,
    replayReportPath: bracket.replayReportPath,
    selfTestReportPath: bracket.selfTestReportPath,
  };
  const envelope = bracketSummaryFromOutput(JSON.stringify(bracket.outputEnvelope));
  const validate = (target = null) => validateNativeBracketEnvelope(envelope, selection, target, tracePaths);

  assert.equal(validate().status, 'passed');
  assert.equal(validate(bracket.targetPath).status, 'failed');
  assert(nativeEnvironmentTargetProblems({ ...summary, targetIdentity: { project, generation } }, domainReport)
    .some(problem => problem.includes('must not invent a registered targetIdentity')));
  assert(nativeEnvironmentTargetProblems({
    ...summary,
    targetValidation: { ...summary.targetValidation, scope: 'owned-config', registryBinding: 'bound' },
  }, domainReport).some(problem => problem.includes('unbound target validation scope')));

  writeFileSync(bracket.summaryPath, JSON.stringify({ ...summary, targetIdentity: { project, generation } }));
  assert.equal(validate().status, 'failed');
  writeFileSync(bracket.summaryPath, JSON.stringify(summary));
  writeFileSync(bracket.summaryPath, JSON.stringify({
    ...summary,
    targetValidation: { ...summary.targetValidation, scope: 'owned-config', registryBinding: 'bound' },
  }));
  assert.equal(validate().status, 'failed');
  writeFileSync(bracket.summaryPath, JSON.stringify(summary));
  writeFileSync(bracket.domainReportPath, JSON.stringify({
    ...domainReport,
    target: { ...domainReport.target, qaIsolation: { ...domainReport.target.qaIsolation, project: 'other-project' } },
  }));
  assert.equal(validate().status, 'failed');
  writeFileSync(bracket.domainReportPath, JSON.stringify(domainReport));
  writeFileSync(bracket.domainReportPath, JSON.stringify({
    ...domainReport,
    target: { ...domainReport.target, qaIsolation: { ...domainReport.target.qaIsolation,
      fixtureOracle: { project, generation: 'other-generation' } } },
  }));
  assert.equal(validate().status, 'failed');
});

const makeBracketFixture = (root) => {
  const contract = JSON.parse(readFileSync(new URL('../../../../verification/builder-interactions/builder-cda-group-related-contract.json', import.meta.url), 'utf8'));
  const scenarioID = 'standalone-reshape-group-edit-before-related-column';
  const caseName = 'group-edit-before-related-column';
  const registeredScenario = registry.find((scenario) => scenario.id === scenarioID);
  const registeredCase = scenarioCaseFor(registeredScenario, caseName);
  const binding = registeredCase.journeyTrace;
  const selection = {
    scenarioID,
    caseName,
    nativeCaseKey: `${scenarioID}/${caseName}`,
    contract: 'verification/builder-interactions/builder-cda-group-related-contract.json',
    expectedIdentity: registeredCase.expectedIdentity,
    requiredChecks: registeredCase.requiredChecks,
    formalProofAssertion: binding.formalProofAssertion,
    traceKind: 'native-lifecycle',
    traceName: binding.name,
    model: binding.model,
  };
  const fingerprint = { sha256: 'a'.repeat(64), files: 3 };
  const owners = Array.from({ length: 5 }, (_, mountEpoch) => ({
    kind: 'proposal', browserRequestId: `native-proposal-${mountEpoch}`, mountEpoch,
  }));
  const actionLabels = [
    'Preview', 'PreviewComplete', 'ExpandRows', 'Preview', 'PreviewComplete', 'ExpandRows',
    'Preview', 'PreviewComplete', 'Group', 'Reload', 'Preview', 'PreviewComplete', 'GroupEditCancel',
    'Preview', 'PreviewComplete', 'EditGroup', 'Reload', 'RelatedSourcePreview', 'RelatedSourceCancel',
    'RelatedSourcePreview', 'RelatedSourceApply', 'Reload', 'Preview', 'PreviewComplete', 'GroupEditCancel',
    'Preview', 'PreviewComplete', 'EditGroupAgain', 'Reload', 'RelatedSourcePreview', 'RelatedSourceCancel',
    'RelatedSourcePreview', 'RelatedSourceApply', 'Reload',
  ];
  const actions = actionLabels.map((label, index) => ({
    label,
    ...(index < owners.length ? { groupLifecycle: { owner: owners[index] } } : {}),
  }));
  const nativeRequests = [
    ...owners.map((owner, index) => ({
      index,
      browserRequestId: owner.browserRequestId,
      requestId: null,
      method: 'POST',
      path: '/api/v1/projects/p/explorers/e/authoring/v2/construction-proposals',
      startedAt: 1_000 + index,
      isMutation: false,
    })),
    ...Array.from({ length: 13 }, (_, offset) => {
      const index = owners.length + offset;
      return {
        index,
        browserRequestId: `native-command-${offset}`,
        requestId: null,
        method: 'POST',
        path: '/api/v1/projects/p/explorers/e/authoring/v2/commands',
        startedAt: 1_000 + index,
        isMutation: true,
      };
    }),
    ...Array.from({ length: 11 }, (_, offset) => {
      const index = owners.length + 13 + offset;
      return {
        index,
        browserRequestId: `native-support-${offset}`,
        requestId: null,
        method: 'GET',
        path: '/api/v1/projects/p/explorers/e/authoring/v2/builder',
        startedAt: 1_000 + index,
        isMutation: false,
      };
    }),
  ];
  const nativeTrace = {
    schemaVersion: 1,
    sourceProfile: 'journey',
    sourceCommit: 'b'.repeat(40),
    sourceFingerprint: fingerprint.sha256,
    contractSourceCommit: contract.sourceCommit,
    fixture: {},
    traces: [{ name: selection.traceName, model: selection.model, actions, nativeRequests }],
  };
  const nativeTraceBytes = Buffer.from(JSON.stringify(nativeTrace));
  const tracePath = join(root, 'native-trace.json');
  writeFileSync(tracePath, nativeTraceBytes);
  const traceSha256 = sha256(nativeTraceBytes);
  const replayReportPath = join(root, 'native-replay.json');
  const selfTestReportPath = join(root, 'native-self-test.json');
  const requiredMutationControlNames = [
    ...contract.journeyCase.requiredMutationControlNames,
    ...owners.map((owner) => `${contract.journeyCase.observedOwnerEpochControlPrefix}${owner.mountEpoch}`),
  ];
  const mutationControls = requiredMutationControlNames.map((name) => ({ name, status: 'pass' }));
  const requestCoverage = { observed: 18, modeled: 18, unmapped: 0, duplicates: 0 };
  const actionMismatches = { mismatches: {} };
  const unmodeledEvents = { total: 11, unclassified: { total: 0 } };
  const verifierReceipt = {
    traceName: selection.traceName,
    nativeCaseKey: selection.nativeCaseKey,
    tracePath,
    traceSha256,
    replayReportPath,
    actionLabels,
    requiredMutationControlNames,
    status: 'pending-self-test',
    replayStatus: 'pass',
    selfTestStatus: 'not-run',
    selfTestReportPath: null,
    requestCoverage,
    requiredActionCountsByTrace: actionMismatches,
    requiredActionMinimumsByTrace: actionMismatches,
    unmodeledEvents,
  };
  writeFileSync(replayReportPath, JSON.stringify({
    sourceProfile: 'journey',
    sourceCommit: nativeTrace.sourceCommit,
    sourceFingerprint: nativeTrace.sourceFingerprint,
    contractSourceCommit: nativeTrace.contractSourceCommit,
    traceSha256,
    traceCount: 1,
    uniqueTraceCount: 1,
    validatedCount: 1,
    invalidCount: 0,
    coverage: {
      requiredModels: { missing: [] },
      requiredActions: { missing: [] },
      requiredActionsByTrace: { missing: {} },
      requiredEvents: { missing: [] },
    },
    unmodeledEvents,
    traces: [{ name: selection.traceName, model: selection.model, status: 'pass', actionCount: actions.length }],
    builderJourneyTrace: verifierReceipt,
  }));
  const selfTestReceipt = {
    traceName: selection.traceName,
    nativeCaseKey: selection.nativeCaseKey,
    tracePath,
    traceSha256,
    selfTestReportPath,
    status: 'pass',
    requiredMutationControlNames,
    mutationControls,
  };
  writeFileSync(selfTestReportPath, JSON.stringify(selfTestReceipt));
  const finalProof = {
    ...verifierReceipt,
    mutationControls,
    status: 'pass',
    replayStatus: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath,
  };
  const checkoutPath = (path) => '$CHECKOUT/' + relative(REPOSITORY_ROOT, path).split('\\').join('/');
  const domainReport = {
    status: 'passed',
    scenario: selection.scenarioID,
    case: selection.caseName,
    target: {
      project: 'loom_dev_cda_fhir',
      generation: 'cda-fhir-v1',
      sourceFingerprint: fingerprint,
      apiBuildIdentity: 'api-build-17',
    },
    requiredChecks: selection.requiredChecks,
    missingRequiredChecks: [],
    assertions: selection.requiredChecks.map((name) => ({
      name,
      status: 'passed',
      ...(name === selection.formalProofAssertion ? {
        evidence: {
          ...finalProof,
          tracePath: checkoutPath(tracePath),
          replayReportPath: checkoutPath(replayReportPath),
          selfTestReportPath: checkoutPath(selfTestReportPath),
        },
      } : {}),
    })),
  };
  const domainReportPath = join(root, 'domain-report.json');
  writeFileSync(domainReportPath, JSON.stringify(domainReport));
  const targetPath = join(root, 'owned-target.json');
  writeFileSync(targetPath, '{}\n');
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
    targetValidation: { registryBinding: 'bound', configPath: targetPath },
    targetIdentity: { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' },
    lifecycle: {
      reportStatus: 'passed',
      reportIdentityMatchesSelection: true,
      requiredCheckCount: selection.requiredChecks.length,
      passedCheckCount: selection.requiredChecks.length,
      failedCheckNames: [],
      missingCheckNames: [],
      dimensions: { usability: 'passed', correctness: 'passed', persistence: 'passed', performance: 'passed' },
    },
    evidence: { domainReport: domainReportPath },
  };
  const summaryPath = join(root, 'summary.json');
  summary.evidence.summary = summaryPath;
  writeFileSync(summaryPath, JSON.stringify(summary));
  const outputEnvelope = {
    status: 'passed',
    scenario: selection.scenarioID,
    case: selection.caseName,
    summary: summaryPath,
    report: domainReportPath,
  };
  return {
    selection, fingerprint, controls: mutationControls, domainReport, domainReportPath, summary, summaryPath,
    tracePath, traceSha256, replayReportPath, selfTestReportPath, targetPath, actionLabels, outputEnvelope,
  };
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

test('native formal receipt selection requires the exact registered assertion and confines retained paths', (t) => {
  const receipt = { traceName: 'trace', nativeCaseKey: 'scenario/case', status: 'pass' };
  const binding = { name: 'trace', formalProofAssertion: 'exact formal assertion' };
  const requiredChecks = [binding.formalProofAssertion];
  const report = { assertions: [{ name: binding.formalProofAssertion, status: 'passed', evidence: receipt }] };
  assert.equal(nativeBuilderJourneyReceiptFromReport(report, { binding, scenarioID: 'scenario', caseName: 'case', requiredChecks }), receipt);
  assert.throws(() => nativeBuilderJourneyReceiptFromReport({ assertions: [] }, {
    binding, scenarioID: 'scenario', caseName: 'case', requiredChecks,
  }), /exactly one registered formal proof assertion/);
  assert.throws(() => nativeBuilderJourneyReceiptFromReport({
    assertions: [report.assertions[0], report.assertions[0]],
  }, { binding, scenarioID: 'scenario', caseName: 'case', requiredChecks }), /exactly one registered formal proof assertion/);
  assert.throws(() => nativeBuilderJourneyReceiptFromReport({
    assertions: [{ name: binding.formalProofAssertion, status: 'passed', evidence: { ...receipt, nativeCaseKey: 'wrong/case' } }],
  }, { binding, scenarioID: 'scenario', caseName: 'case', requiredChecks }), /nativeCaseKey/);
  const legacyReceipt = { traceName: 'trace', nativeCaseKey: 'scenario/case' };
  assert.equal(nativeBuilderJourneyReceiptFromReport({ target: { builderJourneyTrace: legacyReceipt } }, {
    binding: { name: 'trace' }, scenarioID: 'scenario', caseName: 'case',
  }), legacyReceipt);

  const root = makeRepositoryArtifactDirectory('native-proof-path-');
  const outside = mkdtempSync(join(tmpdir(), 'loom-native-proof-outside-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  t.after(() => rmSync(outside, { recursive: true, force: true }));
  const artifact = join(root, 'trace.json');
  const outsideArtifact = join(outside, 'trace.json');
  writeFileSync(artifact, '{}\n');
  writeFileSync(outsideArtifact, '{}\n');
  const checkoutPath = '$CHECKOUT/' + relative(REPOSITORY_ROOT, artifact).split('\\').join('/');
  assert.equal(resolveNativeBuilderJourneyArtifactPath(checkoutPath, REPOSITORY_ROOT), realpathSync(artifact));
  assert.throws(() => resolveNativeBuilderJourneyArtifactPath('$CHECKOUT/../../outside.json', REPOSITORY_ROOT), /escapes the repository root/);
  assert.throws(() => resolveNativeBuilderJourneyArtifactPath('$CHECKOUTED/.artifacts/trace.json', REPOSITORY_ROOT), /only the literal \$CHECKOUT\//);
  const symlink = join(root, 'linked-trace.json');
  symlinkSync(outsideArtifact, symlink);
  assert.throws(() => resolveNativeBuilderJourneyArtifactPath(symlink, REPOSITORY_ROOT), /symlink escapes the repository root/);
});

test('native Builder proof gates accept a complete retained receipt and reject lifecycle, source, or mutation drift', (t) => {
  const root = makeRepositoryArtifactDirectory('native-formal-gate-');
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bracket = makeBracketFixture(root);
  const tracePaths = {
    tracePath: bracket.tracePath,
    replayReportPath: bracket.replayReportPath,
    selfTestReportPath: bracket.selfTestReportPath,
  };
  const envelope = bracketSummaryFromOutput(`native runner output\n${JSON.stringify(bracket.outputEnvelope)}\n`);
  const validate = () => validateNativeBracketEnvelope(
    envelope, bracket.selection, bracket.targetPath, tracePaths,
  );
  const accepted = validate();
  assert.equal(accepted.status, 'passed', JSON.stringify(accepted.issues));
  assert.equal(accepted.formalTraceProof.mutationControls.length, 42);
  assert.equal(validateNativeJourneyTraceBinding(accepted, bracket.selection, bracket.tracePath, bracket.actionLabels).status, 'passed');
  const optionalDuplicateDiagnostics = copy(bracket.domainReport);
  optionalDuplicateDiagnostics.assertions.push(
    { name: 'Change starting records for Specimen completed', status: 'passed' },
    { name: 'Change starting records for Specimen completed', status: 'passed' },
    { name: 'Make a row for each related record Follow a relationship; make one row for each match. completed', status: 'passed' },
    { name: 'Make a row for each related record Follow a relationship; make one row for each match. completed', status: 'passed' },
  );
  writeFileSync(bracket.domainReportPath, JSON.stringify(optionalDuplicateDiagnostics));
  assert.equal(validate().status, 'passed');

  const duplicateRequiredAssertion = copy(optionalDuplicateDiagnostics);
  duplicateRequiredAssertion.assertions.push({
    name: bracket.selection.formalProofAssertion,
    status: 'passed',
    evidence: copy(duplicateRequiredAssertion.assertions.find((assertion) => assertion.name === bracket.selection.formalProofAssertion).evidence),
  });
  writeFileSync(bracket.domainReportPath, JSON.stringify(duplicateRequiredAssertion));
  assert.equal(validate().status, 'failed');
  writeFileSync(bracket.domainReportPath, JSON.stringify(bracket.domainReport));

  assert.equal(validateNativeBracketEnvelope({ ...envelope, summary: join(root, 'missing-summary.json') }, bracket.selection,
    bracket.targetPath, tracePaths).status, 'failed');
  assert.equal(validateNativeBracketEnvelope({ ...envelope, scenario: 'wrong-scenario' }, bracket.selection,
    bracket.targetPath, tracePaths).status, 'failed');

  const failedCheck = copy(bracket.summary);
  failedCheck.lifecycle.passedCheckCount = 0;
  failedCheck.lifecycle.failedCheckNames = [bracket.selection.formalProofAssertion];
  writeFileSync(bracket.summaryPath, JSON.stringify(failedCheck));
  assert.equal(validate().status, 'failed');
  writeFileSync(bracket.summaryPath, JSON.stringify(bracket.summary));

  const missingAssertion = copy(bracket.domainReport);
  missingAssertion.assertions = missingAssertion.assertions.filter((assertion) => assertion.name !== bracket.selection.formalProofAssertion);
  writeFileSync(bracket.domainReportPath, JSON.stringify(missingAssertion));
  assert.equal(validate().status, 'failed');

  const wrongCaseReceipt = copy(bracket.domainReport);
  wrongCaseReceipt.assertions.find((assertion) => assertion.name === bracket.selection.formalProofAssertion).evidence.nativeCaseKey = 'wrong/case';
  writeFileSync(bracket.domainReportPath, JSON.stringify(wrongCaseReceipt));
  assert.equal(validate().status, 'failed');

  const wrongTraceDigest = copy(bracket.domainReport);
  wrongTraceDigest.assertions.find((assertion) => assertion.name === bracket.selection.formalProofAssertion).evidence.traceSha256 = 'e'.repeat(64);
  writeFileSync(bracket.domainReportPath, JSON.stringify(wrongTraceDigest));
  assert.equal(validate().status, 'failed');

  const escapedArtifactPath = copy(bracket.domainReport);
  escapedArtifactPath.assertions.find((assertion) => assertion.name === bracket.selection.formalProofAssertion).evidence.tracePath = '$CHECKOUT/../../outside-trace.json';
  writeFileSync(bracket.domainReportPath, JSON.stringify(escapedArtifactPath));
  assert.equal(validate().status, 'failed');

  const wrongFingerprint = copy(bracket.domainReport);
  wrongFingerprint.target.sourceFingerprint.sha256 = 'e'.repeat(64);
  writeFileSync(bracket.domainReportPath, JSON.stringify(wrongFingerprint));
  assert.equal(validate().status, 'failed');

  const missingMutationReceipt = copy(bracket.domainReport);
  missingMutationReceipt.assertions.find((assertion) => assertion.name === bracket.selection.formalProofAssertion).evidence.mutationControls = [];
  writeFileSync(bracket.domainReportPath, JSON.stringify(missingMutationReceipt));
  assert.equal(validate().status, 'failed');

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
      index: 0, browserRequestId: owner.browserRequestId, requestId: null, method: 'POST',
      path: '/authoring/v2/construction-proposals', startedAt: 1, isMutation: false,
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
  const report = {
    scenario: 'scenario',
    case: 'case',
    target: {
      project: 'test-project',
      generation: 'fixture-v1',
      sourceFingerprint: { sha256: sourceHash, files: 1 },
      apiBuildIdentity: 'api-build-fixture',
    },
    assertions: [{ name: 'formal proof assertion', status: 'passed', evidence }],
  };
  writeFileSync(join(root, reportPath), JSON.stringify(report));
  const registeredCase = {
    journeyTrace: {
      kind: 'native-lifecycle', name: traceName, model, contract: contractPath,
      formalProofAssertion: 'formal proof assertion',
      requiredActions: ['Preview', 'Reload'], requiredActionCounts: { Reload: 1 },
      minimumActionCounts: { Preview: 1 }, optionalActions: [],
    },
  };
  registeredCase.requiredChecks = ['formal proof assertion'];
  const sourceStatus = { status: 'current' };
  return { root, evidence, report, reportPath, registeredCase, sourceStatus, selfTest, replay, traceBytes };
};

test('current browser evidence requires the retained replay receipt and passing mutation controls', (t) => {
  const root = mkdtempSync(join(tmpdir(), 'loom-current-formal-evidence-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const fixture = makeEvidenceFixture(root);
  assert.equal(currentFormalJourneyEvidence(root, fixture.report, fixture.registeredCase, fixture.sourceStatus, fixture.reportPath).status, 'current');

  const legacyCase = copy(fixture.registeredCase);
  delete legacyCase.journeyTrace.formalProofAssertion;
  const legacyReport = copy(fixture.report);
  legacyReport.target.builderJourneyTrace = fixture.evidence;
  writeFileSync(join(root, fixture.reportPath), JSON.stringify(legacyReport));
  assert.equal(currentFormalJourneyEvidence(root, legacyReport, legacyCase, fixture.sourceStatus, fixture.reportPath).status, 'current');
  writeFileSync(join(root, fixture.reportPath), JSON.stringify(fixture.report));

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
