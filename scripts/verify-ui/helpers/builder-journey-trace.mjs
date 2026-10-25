import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { scenarioCaseFor } from '../registry.mjs';
import {
  builderFormalAdapterForBinding,
  builderFormalAdapterForContract,
  isNativeBuilderFormalCaptureKind,
} from './builder-formal-adapters.mjs';

const sourceManifestPath = 'verification/builder-interactions/builder-preview-contract.json';

const canonicalJson = value => Array.isArray(value)
  ? `[${value.map(canonicalJson).join(',')}]`
  : value && typeof value === 'object'
    ? `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(',')}}`
    : JSON.stringify(value);

const assertNativeFormalProofRecordingReady = (report, binding) => {
  const assertionName = binding.formalProofAssertion;
  assert(typeof assertionName === 'string' && assertionName.length > 0 && assertionName.trim() === assertionName,
    'Native Builder journey binding must declare its exact formalProofAssertion.');
  assert(Array.isArray(report.requiredChecks) &&
    report.requiredChecks.filter(name => name === assertionName).length === 1,
  'Registered formalProofAssertion must name exactly one required check.');
  assert(Array.isArray(report.assertions), 'Native Builder report must initialize its assertions list.');
  const existing = report.assertions.filter(assertion => assertion?.name === assertionName);
  assert.equal(existing.length, 0,
    existing.length > 1
      ? `Native Builder report already contains duplicate registered formal proof assertions: ${assertionName}.`
      : `Native Builder report already contains the registered formal proof assertion before the finalized receipt: ${assertionName}.`);
  assert(report.target && typeof report.target === 'object' && !Array.isArray(report.target),
    'Native Builder report must initialize its target object.');
  assert(report.target.builderJourneyTrace === undefined || report.target.builderJourneyTrace === null,
    'Native Builder report must not pre-populate a partial formal proof receipt.');
  assert(report.dimensions?.correctness && typeof report.dimensions.correctness === 'object' &&
    Array.isArray(report.dimensions.correctness.evidence),
  'Native Builder report must initialize correctness evidence before recording its formal proof.');
  return assertionName;
};

const recordNativeFormalProof = (report, assertionName, receipt) => {
  assert.equal(receipt?.status, 'pass', 'Only a completed native formal receipt can be recorded as a passing assertion.');
  assert.equal(receipt.replayStatus, 'pass', 'Native formal receipt replay must pass before assertion recording.');
  assert.equal(receipt.selfTestStatus, 'pass', 'Native formal receipt self-test must pass before assertion recording.');
  assert(Array.isArray(report.assertions), 'Native Builder report assertions must remain initialized.');
  assert.equal(report.assertions.filter(assertion => assertion?.name === assertionName).length, 0,
    `Native Builder report must not duplicate registered formal proof assertion ${assertionName}.`);
  assert(Array.isArray(report.requiredChecks) &&
    report.requiredChecks.filter(name => name === assertionName).length === 1,
  'Registered formalProofAssertion must remain exactly one required check at recording time.');
  const correctness = report.dimensions?.correctness;
  assert(correctness && Array.isArray(correctness.evidence),
    'Native Builder correctness evidence must remain initialized at proof recording time.');

  const status = 'passed';
  const nextAssertions = [...report.assertions, {
    dimension: 'correctness',
    name: assertionName,
    status,
    evidence: receipt,
  }];
  const nextCorrectness = {
    ...correctness,
    status: correctness.status === 'untested' ? status : correctness.status,
    evidence: [...correctness.evidence, { name: assertionName, status, ...receipt }],
  };
  const nextDimensions = { ...report.dimensions, correctness: nextCorrectness };
  const nextTarget = { ...report.target, builderJourneyTrace: receipt };
  Object.assign(report, {
    assertions: nextAssertions,
    dimensions: nextDimensions,
    target: nextTarget,
  });
  return receipt;
};

const repositoryPath = (repositoryRoot, path) => {
  const absolutePath = resolve(repositoryRoot, path);
  const repoRelative = relative(repositoryRoot, absolutePath);
  if (!repoRelative || repoRelative.startsWith('..') || resolve(repositoryRoot, repoRelative) !== absolutePath) {
    throw new Error(`Builder journey path escapes repository root: ${path}`);
  }
  return absolutePath;
};

export const createBuilderReadMetadataRegistry = () => {
  const metadataByBuilder = new WeakMap();
  return {
    record(builder, metadata) {
      assert(builder && typeof builder === 'object' && !Array.isArray(builder),
        'Builder read metadata must be bound to the exact object returned by /builder.');
      assert(metadata && typeof metadata === 'object' && !Array.isArray(metadata),
        'Builder read metadata must be an actual request record.');
      metadataByBuilder.set(builder, structuredClone(metadata));
      return builder;
    },
    forBuilder(builder) {
      const metadata = builder && typeof builder === 'object' ? metadataByBuilder.get(builder) : undefined;
      assert(metadata, 'Builder object must retain metadata from its own /builder response.');
      return structuredClone(metadata);
    },
  };
};

export const nativeRequestLedgerFrom = entries => entries.map(entry => ({
  index: entry.index,
  browserRequestId: entry.browserRequestId,
  requestId: entry.requestId ?? null,
  method: entry.method,
  path: entry.path,
  startedAt: entry.startedAt,
  isMutation: entry.isMutation,
}));

const nativeProposalOwnerEpochsFromTrace = trace => {
  assert(Array.isArray(trace?.nativeRequests), 'Native journey trace must retain its complete nativeRequests ledger.');
  assert(Array.isArray(trace?.actions), 'Native journey trace must retain its observed actions.');
  assert(trace.nativeRequests.every(request => request && typeof request.method === 'string' &&
    typeof request.path === 'string' && typeof request.browserRequestId === 'string' &&
    Number.isSafeInteger(request.index) && (request.requestId === null || typeof request.requestId === 'string') &&
    Number.isFinite(request.startedAt) && typeof request.isMutation === 'boolean'),
  'Native request ledger entries must retain their actual index, IDs, method, path, start time, and mutation flag.');
  const ledgerProposalRequestIds = trace.nativeRequests
    .filter(request => request.method === 'POST' && request.path.endsWith('/construction-proposals'))
    .map(request => request.browserRequestId);
  assert(ledgerProposalRequestIds.length > 0,
    'Native journey trace must retain at least one actual construction-proposal request.');
  assert(ledgerProposalRequestIds.every(requestId => typeof requestId === 'string' && requestId.length > 0),
    'Native proposal ledger entries must retain their actual browserRequestId.');
  assert.equal(new Set(ledgerProposalRequestIds).size, ledgerProposalRequestIds.length,
    'Native proposal ledger must not repeat browserRequestId values.');
  const ownerEpochByRequestId = new Map();
  const addProposalOwner = owner => {
    if (owner?.kind !== 'proposal') return;
    assert(typeof owner.browserRequestId === 'string' && owner.browserRequestId.length > 0,
      'Every modeled proposal owner must retain its actual browserRequestId.');
    assert(Number.isSafeInteger(owner.mountEpoch) && owner.mountEpoch >= 0,
      `Proposal owner ${owner.browserRequestId} must retain its actual mountEpoch.`);
    const priorEpoch = ownerEpochByRequestId.get(owner.browserRequestId);
    assert(priorEpoch === undefined || priorEpoch === owner.mountEpoch,
      `Proposal owner ${owner.browserRequestId} must not change mountEpoch across actions.`);
    ownerEpochByRequestId.set(owner.browserRequestId, owner.mountEpoch);
  };
  for (const action of trace.actions) {
    addProposalOwner(action.owner);
    addProposalOwner(action.groupLifecycle?.owner);
    addProposalOwner(action.groupLifecycle?.candidateOwner);
    addProposalOwner(action.rowExpansionLifecycle?.owner);
    addProposalOwner(action.relatedSourceLifecycle?.owner);
  }
  assert.deepEqual([...ownerEpochByRequestId.keys()].sort(), [...ledgerProposalRequestIds].sort(),
    'Every native construction-proposal request must have exactly one retained proposal owner identity.');
  return [...new Set(ownerEpochByRequestId.values())].sort((left, right) => left - right);
};

export const assertNativeBuilderJourneyMutationControls = (controls, { contract, traceDocument, where = 'Self-test output' }) => {
  if (contract?.nativeJourney) {
    const registration = contract.nativeJourney;
    const adapter = builderFormalAdapterForContract(contract);
    assert(adapter, `${where} validation needs a registered nativeJourney adapter.`);
    const requiredMutationControlNames = registration.requiredMutationControlNames;
    assert(Array.isArray(requiredMutationControlNames) && requiredMutationControlNames.length > 0 &&
      requiredMutationControlNames.every(name => typeof name === 'string' && name.length > 0),
    'Native journey contract must declare requiredMutationControlNames.');
    assert.equal(new Set(requiredMutationControlNames).size, requiredMutationControlNames.length,
      'Native journey contract must not repeat requiredMutationControlNames.');
    assert(Array.isArray(traceDocument?.traces) && traceDocument.traces.length === 1,
      `${where} validation needs the exact single-trace input document.`);
    const trace = traceDocument.traces[0];
    assert.equal(trace?.name, registration.traceName,
      `${where} trace name must match the static nativeJourney contract.`);
    assert.equal(trace?.model, registration.model,
      `${where} trace model must match the static nativeJourney contract.`);
    adapter.validateTrace(trace);
    const expectedControls = adapter.buildMutationControls(trace);
    assert(Array.isArray(expectedControls) && expectedControls.length > 0 &&
      expectedControls.every(control => control && typeof control.name === 'string' &&
        control.expectedFailure?.stage === 'TLC' && typeof control.expectedFailure.invariant === 'string'),
    'Native adapter must produce named TLC mutation controls with an exact expected outcome.');
    assert.deepEqual([...expectedControls.map(control => control.name)].sort(), [...requiredMutationControlNames].sort(),
      'Native journey contract mutation names must match the selected adapter controls.');
    assert(Array.isArray(controls) && controls.length > 0,
      `${where} must contain mutation control results.`);
    const actualNames = controls.map(control => control?.name);
    assert(actualNames.every(name => typeof name === 'string' && name.length > 0),
      `${where} mutation controls must retain their actual names.`);
    assert.equal(new Set(actualNames).size, actualNames.length,
      `${where} must not repeat mutation-control names.`);
    assert.deepEqual([...actualNames].sort(), [...requiredMutationControlNames].sort(),
      `${where} mutation-control names must exactly match the nativeJourney contract.`);
    for (const [index, control] of controls.entries()) {
      assert(control && control.name === actualNames[index] &&
        (control.status === 'pass' || control.status === 'fail'),
      `${where} mutation control ${index} must retain its actual name and status.`);
      assert.equal(control.status, 'pass', `${where} mutation control ${index} (${control.name}) must pass.`);
    }
    return controls;
  }
  assert(contract?.journeyCase && typeof contract.journeyCase === 'object',
    `${where} validation needs the registered journeyCase contract.`);
  const { requiredMutationControlNames, observedOwnerEpochControlPrefix } = contract.journeyCase;
  assert(Array.isArray(requiredMutationControlNames) && requiredMutationControlNames.length > 0 &&
    requiredMutationControlNames.every(name => typeof name === 'string' && name.length > 0),
  'CDA journey contract must declare requiredMutationControlNames.');
  assert.equal(new Set(requiredMutationControlNames).size, requiredMutationControlNames.length,
    'CDA journey contract must not repeat requiredMutationControlNames.');
  assert(typeof observedOwnerEpochControlPrefix === 'string' && observedOwnerEpochControlPrefix.length > 0,
    'CDA journey contract must declare observedOwnerEpochControlPrefix.');
  assert(Array.isArray(traceDocument?.traces) && traceDocument.traces.length === 1,
    `${where} validation needs the exact single-trace input document.`);
  const trace = traceDocument.traces[0];
  const observedEpochs = nativeProposalOwnerEpochsFromTrace(trace);
  const expectedNames = [
    ...requiredMutationControlNames,
    ...observedEpochs.map(epoch => `${observedOwnerEpochControlPrefix}${epoch}`),
  ];
  assert.equal(new Set(expectedNames).size, expectedNames.length,
    'CDA journey contract mutation controls must not overlap owner-epoch controls.');
  assert(Array.isArray(controls) && controls.length > 0,
    `${where} must contain mutation control results.`);
  const actualNames = controls.map(control => control?.name);
  assert(actualNames.every(name => typeof name === 'string' && name.length > 0),
    `${where} mutation controls must retain their actual names.`);
  assert.equal(new Set(actualNames).size, actualNames.length,
    `${where} must not repeat mutation-control names.`);
  assert.deepEqual([...actualNames].sort(), [...expectedNames].sort(),
    `${where} mutation-control names must exactly match the contract and observed proposal-owner epochs.`);
  for (const [index, control] of controls.entries()) {
    assert(control && control.name === actualNames[index] &&
      (control.status === 'pass' || control.status === 'fail'),
    `${where} mutation control ${index} must retain its actual name and status.`);
    assert.equal(control.status, 'pass', `${where} mutation control ${index} (${control.name}) must pass.`);
  }
  return controls;
};

export const builderJourneyTraceBindingFor = (scenarioID, caseName) => {
  const registeredCase = scenarioCaseFor(scenarioID, caseName);
  const binding = registeredCase.journeyTrace;
  if (!binding || typeof binding !== 'object') {
    throw new Error(`Missing Builder journey trace binding for ${scenarioID}/${caseName}`);
  }
  for (const field of ['name', 'model', 'contract']) {
    if (typeof binding[field] !== 'string' || binding[field].trim() === '') {
      throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} needs ${field}`);
    }
  }
  if (binding.kind === 'native-lifecycle') {
    builderFormalAdapterForBinding(binding);
  } else if (binding.captureKind !== 'builder-group-legacy') {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} needs a recognized captureKind`);
  }
  if (!Array.isArray(binding.requiredActions) || binding.requiredActions.length === 0 ||
      binding.requiredActions.some(action => typeof action !== 'string' || action.length === 0)) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} needs requiredActions`);
  }
  if (binding.requiredActionCounts !== undefined &&
      (!binding.requiredActionCounts || typeof binding.requiredActionCounts !== 'object' ||
       Object.entries(binding.requiredActionCounts).some(([action, count]) =>
         typeof action !== 'string' || !Number.isInteger(count) || count < 1))) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} has invalid requiredActionCounts`);
  }
  if (binding.minimumActionCounts !== undefined &&
      (!binding.minimumActionCounts || typeof binding.minimumActionCounts !== 'object' ||
       Object.entries(binding.minimumActionCounts).some(([action, count]) =>
         typeof action !== 'string' || !Number.isInteger(count) || count < 1))) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} has invalid minimumActionCounts`);
  }
  return binding;
};

export const builderJourneyTraceReceiptIdentity = (binding, traceName, scenarioID, caseName) => {
  if (binding.kind === 'native-lifecycle') {
    builderFormalAdapterForBinding(binding);
    return { traceName, nativeCaseKey: `${scenarioID}/${caseName}` };
  }
  if (binding.captureKind === 'builder-group-legacy') return { caseKey: traceName };
  throw new Error(`Unknown Builder journey receipt captureKind: ${String(binding.captureKind)}`);
};

export const builderJourneyReportPaths = ({
  captureKind,
  tracePath,
  evidenceDirectory,
  artifactStem,
  repositoryRoot,
  nativeReportPath,
  retainedNativeReportPath,
}) => {
  if (isNativeBuilderFormalCaptureKind(captureKind)) {
    const traceDirectory = dirname(tracePath);
    const traceFileStem = basename(tracePath).replace(/\.json$/i, '');
    assert(traceFileStem.length > 0, 'Native journey trace path must have a file stem.');
    const paths = {
      replayReportPath: join(traceDirectory, `${traceFileStem}-tla.json`),
      selfTestReportPath: join(traceDirectory, `${traceFileStem}-self-test.json`),
    };
    let outerTlaReportPath = null;
    const retainedPaths = [];
    if (nativeReportPath !== undefined) {
      assert(typeof nativeReportPath === 'string' && nativeReportPath.length > 0 && /\.json$/i.test(nativeReportPath),
        'Native outer report path must be a nonempty JSON path.');
      assert(typeof retainedNativeReportPath === 'string' && retainedNativeReportPath.length > 0,
        'Native retained report path is required with the outer report path.');
      outerTlaReportPath = nativeReportPath.replace(/\.json$/i, '.tla.json');
      retainedPaths.push(nativeReportPath, outerTlaReportPath, retainedNativeReportPath);
    } else {
      assert(retainedNativeReportPath === undefined,
        'Native retained report path requires the outer report path.');
    }
    const outputs = [tracePath, paths.replayReportPath, paths.selfTestReportPath, ...retainedPaths];
    const canonicalOutputs = outputs.map(path => {
      const absolute = resolve(path);
      return join(realpathSync(dirname(absolute)), basename(absolute));
    });
    assert.equal(new Set(canonicalOutputs).size, canonicalOutputs.length,
      'Native journey trace, proof, report, and retained-report output paths must be unique.');
    const existing = outputs.filter(existsSync);
    assert.equal(existing.length, 0,
      `Native journey trace and proof outputs must be fresh; choose a fresh trace path (existing: ${existing.join(', ')}).`);
    return { ...paths, ...(outerTlaReportPath === null ? {} : { outerTlaReportPath }) };
  }
  if (captureKind === 'builder-group-legacy') {
    return {
      replayReportPath: join(evidenceDirectory, `${artifactStem}-tla.json`),
      selfTestReportPath: resolve(repositoryRoot, evidenceDirectory, `${artifactStem}-self-test.json`),
    };
  }
  throw new Error(`Unknown Builder journey report path captureKind: ${String(captureKind)}`);
};

const assertNativeReceiptIdentity = (receipt, traceName, nativeCaseKey, stage) => {
  assert(receipt && typeof receipt === 'object' && !Array.isArray(receipt),
    `${stage} must contain a Builder journey receipt object.`);
  assert.equal(receipt.traceName, traceName, `${stage} traceName must match the registered trace.`);
  assert.equal(receipt.nativeCaseKey, nativeCaseKey,
    `${stage} nativeCaseKey must match the selected registered case.`);
  assert(!Object.hasOwn(receipt, 'caseKey'), `${stage} must not include a legacy caseKey alias.`);
};

const pathWithin = (parent, candidate) => {
  const local = relative(parent, candidate);
  return local !== '..' && !local.startsWith('..' + sep) && !isAbsolute(local);
};

export const resolveNativeBuilderJourneyArtifactPath = (reference, repositoryRoot) => {
  assert(typeof reference === 'string' && reference.length > 0 && reference.trim() === reference,
    'Native Builder journey artifact path must be an exact non-empty string.');
  assert(typeof repositoryRoot === 'string' && repositoryRoot.length > 0,
    'Native Builder journey artifact path needs the repository root.');
  const root = realpathSync(resolve(repositoryRoot));
  let localReference = reference;
  if (localReference.startsWith('$CHECKOUT/')) localReference = localReference.slice('$CHECKOUT/'.length);
  else assert(!localReference.includes('$CHECKOUT'),
    'Native Builder journey artifact path may use only the literal $CHECKOUT/ root prefix.');
  const candidate = isAbsolute(localReference) ? resolve(localReference) : resolve(root, localReference);
  if (!isAbsolute(localReference)) {
    assert(candidate !== root && pathWithin(root, candidate),
      'Native Builder journey artifact path escapes the repository root: ' + reference);
  }
  const realCandidate = realpathSync(candidate);
  assert(realCandidate !== root && pathWithin(root, realCandidate),
    'Native Builder journey artifact symlink escapes the repository root: ' + reference);
  return realCandidate;
};

export const nativeBuilderJourneyReceiptFromReport = (report, { binding, scenarioID, caseName, requiredChecks }) => {
  assert(binding && typeof binding === 'object' && !Array.isArray(binding),
    'Native Builder journey report selection needs the registered trace binding.');
  assert(typeof binding.name === 'string' && binding.name.length > 0,
    'Native Builder journey report selection needs the registered traceName.');
  assert(typeof scenarioID === 'string' && scenarioID.length > 0 &&
    typeof caseName === 'string' && caseName.length > 0,
  'Native Builder journey report selection needs the registered scenario and case.');
  const nativeCaseKey = `${scenarioID}/${caseName}`;
  if (!Object.hasOwn(binding, 'formalProofAssertion')) {
    const legacyReceipt = report?.target?.builderJourneyTrace ?? null;
    if (legacyReceipt) assertNativeReceiptIdentity(legacyReceipt, binding.name, nativeCaseKey, 'Legacy target receipt');
    return legacyReceipt;
  }
  assert(typeof binding.formalProofAssertion === 'string' && binding.formalProofAssertion.length > 0 &&
    binding.formalProofAssertion.trim() === binding.formalProofAssertion,
  'Registered formalProofAssertion must be an exact non-empty assertion name.');
  assert(Array.isArray(requiredChecks) &&
    requiredChecks.filter((name) => name === binding.formalProofAssertion).length === 1,
  'Registered formalProofAssertion must name exactly one required check.');
  const assertions = Array.isArray(report?.assertions) ? report.assertions : [];
  const matches = assertions.filter((assertion) => assertion?.name === binding.formalProofAssertion);
  assert.equal(matches.length, 1,
    `Native Builder journey report must contain exactly one registered formal proof assertion: ${binding.formalProofAssertion}.`);
  const assertion = matches[0];
  assert.equal(assertion.status, 'passed',
    `Registered formal proof assertion did not pass: ${binding.formalProofAssertion}.`);
  const receipt = assertion.evidence;
  assertNativeReceiptIdentity(receipt, binding.name, nativeCaseKey,
    `Registered formal proof assertion ${binding.formalProofAssertion}`);
  const targetReceipt = report?.target?.builderJourneyTrace;
  if (targetReceipt) {
    for (const field of [
      'traceName', 'nativeCaseKey', 'status', 'replayStatus', 'selfTestStatus', 'traceSha256',
      'actionLabels', 'requiredMutationControlNames', 'requestCoverage', 'requiredActionCountsByTrace',
      'requiredActionMinimumsByTrace', 'unmodeledEvents', 'mutationControls', 'requestCoverageBasis',
    ]) {
      assert.equal(canonicalJson(targetReceipt[field]), canonicalJson(receipt[field]),
        `Target and registered assertion formal receipts differ at ${field}.`);
    }
  }
  return receipt;
};

const assertNativeReceiptTraceDigest = (receipt, tracePath, stage) => {
  assert(typeof tracePath === 'string' && tracePath.length > 0,
    `${stage} validation needs the actual trace file path.`);
  assert.equal(receipt.tracePath, tracePath, `${stage} tracePath must match the captured trace file.`);
  const actualTraceSha256 = createHash('sha256').update(readFileSync(tracePath)).digest('hex');
  assert.match(receipt.traceSha256 ?? '', /^[0-9a-f]{64}$/, `${stage} must retain a SHA-256 traceSha256.`);
  assert.equal(receipt.traceSha256, actualTraceSha256,
    `${stage} traceSha256 must match the exact raw trace file bytes.`);
  return actualTraceSha256;
};

export const assertNativeBuilderJourneyReplayReceipt = (receipt, {
  traceName,
  nativeCaseKey,
  actionLabels,
  tracePath,
  requestCoverageFields = ['modeledActions', 'reconcileReads', 'supportedReadOnly'],
}) => {
  assertNativeReceiptIdentity(receipt, traceName, nativeCaseKey, 'Verifier output');
  assertNativeReceiptTraceDigest(receipt, tracePath, 'Verifier output');
  assert.equal(receipt.status, 'pending-self-test', 'Verifier receipt must await the mutation self-test.');
  assert.equal(receipt.replayStatus, 'pass', 'Verifier receipt must record a passing replay.');
  assert.equal(receipt.selfTestStatus, 'not-run', 'Verifier receipt must record that the self-test has not run.');
  assert.equal(receipt.selfTestReportPath, null, 'Verifier receipt must not claim a self-test report before it runs.');
  assert.deepEqual(receipt.actionLabels, actionLabels, 'Verifier receipt must retain the captured action chronology.');
  const coverage = receipt.requestCoverage;
  assert(coverage && typeof coverage === 'object' && !Array.isArray(coverage),
    'Verifier receipt must include requestCoverage.');
  for (const field of ['observed', 'modeled', 'unmapped', 'duplicates']) {
    assert(Number.isInteger(coverage[field]) && coverage[field] >= 0,
      `Verifier requestCoverage.${field} must be a non-negative integer.`);
  }
  assert.equal(coverage.modeled, coverage.observed,
    'Verifier request coverage must model every observed request.');
  assert.equal(coverage.unmapped, 0, 'Verifier request coverage must have no unmapped requests.');
  assert.equal(coverage.duplicates, 0, 'Verifier request coverage must have no duplicate bindings.');
  if (Object.hasOwn(receipt, 'requestCoverageBasis')) {
    assert(typeof receipt.requestCoverageBasis === 'string' && receipt.requestCoverageBasis.trim().length > 0,
      'Verifier requestCoverageBasis must state the adapter coverage claim.');
    const detailFields = requestCoverageFields;
    assert(detailFields.every(field => Number.isInteger(coverage[field]) && coverage[field] >= 0),
      `Adapter request coverage must retain non-negative counts for ${detailFields.join(', ')}.`);
    assert.equal(detailFields.reduce((sum, field) => sum + coverage[field], 0), coverage.observed,
      'Adapter request coverage detail counts must account for every retained transport row.');
  }
  for (const field of ['requiredActionCountsByTrace', 'requiredActionMinimumsByTrace']) {
    assert(receipt[field] && typeof receipt[field] === 'object' && !Array.isArray(receipt[field]) &&
      receipt[field].mismatches && typeof receipt[field].mismatches === 'object' &&
      !Array.isArray(receipt[field].mismatches),
    `Verifier receipt must include ${field}.mismatches.`);
    assert.equal(Object.keys(receipt[field].mismatches).length, 0,
      `Verifier receipt must have no ${field} mismatches.`);
  }
  assert(receipt.unmodeledEvents && typeof receipt.unmodeledEvents === 'object' &&
    Number.isInteger(receipt.unmodeledEvents.total) && receipt.unmodeledEvents.total >= 0 &&
    receipt.unmodeledEvents.unclassified &&
    Number.isInteger(receipt.unmodeledEvents.unclassified.total) && receipt.unmodeledEvents.unclassified.total >= 0,
  'Verifier receipt must include unmodeled event totals.');
  assert.equal(receipt.unmodeledEvents.unclassified.total, 0,
    'Verifier receipt must have no unclassified unmodeled events.');
  return receipt;
};

export const assertNativeBuilderJourneySelfTestReceipt = (receipt, {
  traceName,
  nativeCaseKey,
  tracePath,
  traceSha256,
  selfTestReportPath,
  contract,
  traceDocument,
}) => {
  assertNativeReceiptIdentity(receipt, traceName, nativeCaseKey, 'Self-test output');
  const actualTraceSha256 = assertNativeReceiptTraceDigest(receipt, tracePath, 'Self-test output');
  assert.equal(actualTraceSha256, traceSha256, 'Verifier and self-test must bind the same exact trace bytes.');
  assert.equal(receipt.status, 'pass', 'Self-test receipt status must be pass.');
  assert(!Object.hasOwn(receipt, 'selfTestStatus'), 'Self-test receipt must use one authoritative status field.');
  assert.equal(typeof receipt.selfTestReportPath, 'string', 'Self-test receipt must retain its output path.');
  assert.equal(resolve(receipt.selfTestReportPath), resolve(selfTestReportPath),
    'Self-test receipt must identify its actual output path.');
  for (const field of ['replayStatus', 'replayReportPath', 'requestCoverage']) {
    assert(!Object.hasOwn(receipt, field), `Self-test receipt must not echo verifier field ${field}.`);
  }
  assertNativeBuilderJourneyMutationControls(receipt.mutationControls, {
    contract,
    traceDocument,
    where: 'Self-test output',
  });
  return receipt;
};

export const finalizeNativeBuilderJourneyReceipt = (verifierReceipt, selfTestReceipt, identity) => {
  const adapter = identity.contract ? builderFormalAdapterForContract(identity.contract) : null;
  const registeredCoverageFields = adapter?.requestCoverageFields;
  if (registeredCoverageFields && identity.requestCoverageFields !== undefined) {
    assert.deepEqual(identity.requestCoverageFields, registeredCoverageFields,
      'Caller request coverage fields must match the registered adapter contract.');
  }
  const replayIdentity = {
    ...identity,
    requestCoverageFields: registeredCoverageFields ?? identity.requestCoverageFields,
  };
  assertNativeBuilderJourneyReplayReceipt(verifierReceipt, replayIdentity);
  assertNativeBuilderJourneySelfTestReceipt(selfTestReceipt, identity);
  return {
    ...verifierReceipt,
    traceSha256: verifierReceipt.traceSha256,
    mutationControls: selfTestReceipt.mutationControls,
    status: 'pass',
    replayStatus: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: identity.selfTestReportPath,
  };
};

export const captureBuilderJourneyTraceProvenance = ({
  repositoryRoot,
  contractPath,
  reviewedSourceManifestPath = sourceManifestPath,
}) => {
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: repositoryRoot, encoding: 'utf8' });
  if (head.error || head.status !== 0) {
    throw new Error(`Cannot resolve Builder trace source HEAD: ${head.error?.message ?? head.stderr ?? head.stdout}`);
  }

  const manifest = JSON.parse(readFileSync(resolve(repositoryRoot, reviewedSourceManifestPath), 'utf8'));
  assert(Array.isArray(manifest.sources) && manifest.sources.length > 0,
    'Builder journey source manifest must contain source pins.');
  const seenPaths = new Set();
  const sourceEntries = manifest.sources.map((source, index) => {
    assert(source && typeof source.path === 'string' && /^[0-9a-f]{64}$/.test(source.sha256),
      `Builder journey source manifest entry ${index} needs a path and SHA-256.`);
    assert(!seenPaths.has(source.path), `Builder journey source manifest repeats ${source.path}.`);
    seenPaths.add(source.path);
    const actual = createHash('sha256').update(readFileSync(repositoryPath(repositoryRoot, source.path))).digest('hex');
    if (actual !== source.sha256) {
      throw new Error(`Builder trace source pin drift at ${source.path}: expected ${source.sha256}, found ${actual}`);
    }
    return { path: source.path, sha256: actual };
  }).sort((left, right) => left.path.localeCompare(right.path));
  const contract = JSON.parse(readFileSync(repositoryPath(repositoryRoot, contractPath), 'utf8'));
  assert.equal(typeof contract.sourceProfile, 'string', 'Builder journey contract needs sourceProfile.');
  assert.match(contract.sourceCommit, /^[0-9a-f]{40}$/, 'Builder journey contract needs its sourceCommit.');
  return {
    sourceProfile: contract.sourceProfile,
    sourceCommit: head.stdout.trim(),
    contractSourceCommit: contract.sourceCommit,
    sourceFingerprint: createHash('sha256').update(canonicalJson(sourceEntries)).digest('hex'),
  };
};

export const writeAndReplayBuilderJourneyTrace = ({
  scenarioID,
  caseName,
  journeyTrace,
  report,
  fixture,
  repositoryRoot,
  reviewedSourceManifestPath,
}) => {
  const binding = builderJourneyTraceBindingFor(scenarioID, caseName);
  assert.equal(journeyTrace.name, binding.name,
    `Captured Builder journey name must match the ${scenarioID}/${caseName} registry binding.`);
  assert.equal(journeyTrace.model, binding.model,
    `Captured Builder journey model must match the ${scenarioID}/${caseName} registry binding.`);
  const contractPath = binding.contract;
  const isNativeLifecycle = binding.kind === 'native-lifecycle' && isNativeBuilderFormalCaptureKind(binding.captureKind);
  if (binding.kind === 'native-lifecycle') builderFormalAdapterForBinding(binding);
  const formalProofAssertion = isNativeLifecycle
    ? assertNativeFormalProofRecordingReady(report, binding)
    : null;
  const receiptIdentity = builderJourneyTraceReceiptIdentity(binding, journeyTrace.name, scenarioID, caseName);
  const actions = journeyTrace.actions ?? [];
  const labels = actions.map(action => action.label);
  for (const requiredAction of binding.requiredActions) {
    assert(labels.includes(requiredAction),
      `Captured Builder journey ${journeyTrace.name} is missing registered action ${requiredAction}.`);
  }
  for (const [requiredAction, expectedCount] of Object.entries(binding.requiredActionCounts ?? {})) {
    assert.equal(labels.filter(label => label === requiredAction).length, expectedCount,
      `Captured Builder journey ${journeyTrace.name} needs ${expectedCount} ${requiredAction} actions.`);
  }
  for (const [requiredAction, minimumCount] of Object.entries(binding.minimumActionCounts ?? {})) {
    assert(labels.filter(label => label === requiredAction).length >= minimumCount,
      `Captured Builder journey ${journeyTrace.name} needs at least ${minimumCount} ${requiredAction} actions.`);
  }

  const artifactStem = journeyTrace.name.replaceAll(':', '-');
  const tracePath = process.env.LOOM_JOURNEY_TRACE
    ? resolve(repositoryRoot, process.env.LOOM_JOURNEY_TRACE)
    : join(report.evidenceDirectory, `${artifactStem}-trace.json`);
  const { replayReportPath, selfTestReportPath } = builderJourneyReportPaths({
    captureKind: binding.captureKind,
    tracePath,
    evidenceDirectory: report.evidenceDirectory,
    artifactStem,
    repositoryRoot,
  });
  const selectedContract = isNativeLifecycle
    ? JSON.parse(readFileSync(repositoryPath(repositoryRoot, contractPath), 'utf8'))
    : null;
  const tracePayload = {
    schemaVersion: 1,
    ...captureBuilderJourneyTraceProvenance({ repositoryRoot, contractPath, reviewedSourceManifestPath }),
    fixture,
    traces: [journeyTrace],
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(tracePayload, null, 2)}\n`);
  const traceSha256 = createHash('sha256').update(readFileSync(tracePath)).digest('hex');
  const replayArgs = [
    repositoryPath(repositoryRoot, 'scripts/verify-builder-traces.mjs'),
    '--traces', tracePath,
    '--contract', contractPath,
    '--output', replayReportPath,
    '--quiet',
  ];
  if (!isNativeLifecycle) rmSync(replayReportPath, { force: true });
  const replay = spawnSync(process.execPath, replayArgs, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (replay.error) throw replay.error;
  const nativeCaseKey = `${scenarioID}/${caseName}`;
  report.target.builderJourneyTrace = {
    ...receiptIdentity,
    tracePath,
    replayReportPath,
    actionLabels: labels,
    status: 'fail',
    ...(isNativeLifecycle ? {
      traceSha256,
      replayStatus: 'fail',
      selfTestStatus: 'not-run',
      selfTestReportPath: null,
    } : {}),
  };
  if (replay.status !== 0) {
    const output = `${replay.stdout ?? ''}${replay.stderr ?? ''}`.trim();
    throw new Error(`Builder journey trace replay failed for ${journeyTrace.name}: ${output.split('\n').slice(-20).join('\n')}`);
  }

  let verifierReceipt;
  if (isNativeLifecycle) {
    const replayReport = JSON.parse(readFileSync(replayReportPath, 'utf8'));
    const nativeJourneyAdapter = selectedContract?.nativeJourney
      ? builderFormalAdapterForBinding(binding)
      : null;
    verifierReceipt = assertNativeBuilderJourneyReplayReceipt(replayReport.builderJourneyTrace, {
      traceName: journeyTrace.name,
      nativeCaseKey,
      actionLabels: labels,
      tracePath,
      requestCoverageFields: nativeJourneyAdapter?.requestCoverageFields,
    });
    if (selectedContract.nativeJourney) {
      assert.equal(verifierReceipt.nativeCaseKey, selectedContract.nativeJourney.nativeCaseKey,
        'Native replay receipt case key must match the static nativeJourney contract.');
      assert.equal(verifierReceipt.requestCoverageBasis, selectedContract.nativeJourney.coverageBasis,
        'Native replay receipt must retain the contract coverage basis.');
      assert.deepEqual(verifierReceipt.requestCoverage, nativeJourneyAdapter.requestCoverage(journeyTrace),
        'Native replay request coverage must be derived from the captured transport ledger.');
    }
    report.target.builderJourneyTrace = { ...verifierReceipt };
  } else {
    report.target.builderJourneyTrace = {
      ...report.target.builderJourneyTrace,
      status: 'pass',
      replayStatus: 'pass',
    };
  }

  const selfTestArgs = [
    repositoryPath(repositoryRoot, 'scripts/self-test-builder-traces.mjs'),
    '--journey-only',
    '--traces', tracePath,
    '--contract', contractPath,
  ];
  if (isNativeLifecycle) selfTestArgs.push('--output', selfTestReportPath);
  const selfTest = spawnSync(process.execPath, selfTestArgs, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const selfTestOutput = `${selfTest.stdout ?? ''}${selfTest.stderr ?? ''}`.trim();
  let selfTestReceipt;
  let selfTestValidationError;
  if (isNativeLifecycle && existsSync(selfTestReportPath)) {
    try {
      selfTestReceipt = JSON.parse(readFileSync(selfTestReportPath, 'utf8'));
      assertNativeBuilderJourneySelfTestReceipt(selfTestReceipt, {
        traceName: journeyTrace.name,
        nativeCaseKey,
        tracePath,
        traceSha256,
        selfTestReportPath,
        contract: selectedContract,
        traceDocument: tracePayload,
      });
    } catch (error) {
      selfTestValidationError = error;
    }
  }
  const selfTestStatus = !selfTest.error && selfTest.status === 0 &&
    (!isNativeLifecycle || (selfTestReceipt && !selfTestValidationError)) ? 'pass' : 'fail';
  if (!isNativeLifecycle) {
    writeFileSync(selfTestReportPath, `${JSON.stringify({
      ...receiptIdentity,
      tracePath,
      contractPath,
      command: [process.execPath, ...selfTestArgs],
      status: selfTestStatus,
      exitCode: selfTest.status,
      error: selfTest.error?.message ?? null,
      output: selfTestOutput,
    }, null, 2)}\n`);
  }
  if (isNativeLifecycle) {
    if (selfTestStatus === 'pass') {
      const finalizedReceipt = finalizeNativeBuilderJourneyReceipt(verifierReceipt, selfTestReceipt, {
        traceName: journeyTrace.name,
        nativeCaseKey,
        actionLabels: labels,
        tracePath,
        traceSha256,
        selfTestReportPath,
        contract: selectedContract,
        traceDocument: tracePayload,
      });
      recordNativeFormalProof(report, formalProofAssertion, finalizedReceipt);
    } else {
      report.target.builderJourneyTrace = {
        ...verifierReceipt,
        ...(Array.isArray(selfTestReceipt?.mutationControls)
          ? { mutationControls: selfTestReceipt.mutationControls }
          : {}),
        status: selfTestStatus,
        replayStatus: 'pass',
        selfTestStatus,
        selfTestReportPath,
      };
    }
  } else {
    report.target.builderJourneyTrace = {
      ...report.target.builderJourneyTrace,
      replayStatus: 'pass',
      status: selfTestStatus,
      selfTestStatus,
      selfTestReportPath,
    };
  }
  if (selfTestStatus !== 'pass') {
    const validationDetail = selfTestValidationError ? `\n${selfTestValidationError.message}` : '';
    throw new Error(`Builder journey trace self-test failed for ${journeyTrace.name}: ${selfTestOutput.split('\n').slice(-30).join('\n')}${validationDetail}`);
  }
  return report.target.builderJourneyTrace;
};
