#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const args = process.argv.slice(2);
const valueFor = (name) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
const journeyOnly = args.includes('--journey-only');
const formalOnly = args.includes('--formal-only');
const tracesPath = valueFor('--traces');
const selfTestOutputPath = valueFor('--output');
const contractPath = valueFor('--contract') ?? (journeyOnly
  ? 'verification/builder-interactions/builder-journey-contract.json'
  : 'verification/builder-interactions/builder-trace-contract.json');
if (!tracesPath && !journeyOnly) {
  console.error('usage: node scripts/self-test-builder-traces.mjs --traces <traces.json> [--contract <contract.json>] [--formal-only|--journey-only]');
  process.exit(1);
}

const rawTraceBytes = tracesPath ? readFileSync(resolve(ROOT, tracesPath)) : null;
const traceSha256 = rawTraceBytes === null ? null : createHash('sha256').update(rawTraceBytes).digest('hex');
let payload = rawTraceBytes === null ? null : JSON.parse(rawTraceBytes.toString('utf8'));
const contract = JSON.parse(readFileSync(resolve(ROOT, contractPath), 'utf8'));
const mutationControlNamesMatch = (controls, requiredNames) => {
  if (!Array.isArray(controls) || !Array.isArray(requiredNames)) return false;
  const names = controls.map(control => control?.name);
  return names.length === requiredNames.length &&
    names.every(name => typeof name === 'string') &&
    new Set(names).size === names.length &&
    names.every(name => requiredNames.includes(name)) &&
    requiredNames.every(name => names.includes(name)) &&
    controls.every(control => control.status === 'pass');
};
const observedCdaOwnerEpochs = trace => [...new Set((trace.actions ?? [])
  .filter(action => ['Preview', 'RelatedSourcePreview'].includes(action.label))
  .map(action => (action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner ??
    action.relatedSourceLifecycle?.owner)?.mountEpoch)
  .filter(Number.isInteger))].sort((left, right) => left - right);
const expectedCdaMutationControlNames = (journeyCase, trace) => [
  ...(journeyCase.requiredMutationControlNames ?? []),
  ...observedCdaOwnerEpochs(trace).map(epoch => journeyCase.observedOwnerEpochControlPrefix + epoch),
];
const selfTestReceiptProblem = (receipt, expected) => {
  if (!receipt || receipt.traceName !== expected.traceName || receipt.nativeCaseKey !== expected.nativeCaseKey ||
      receipt.tracePath !== expected.tracePath || !/^[0-9a-f]{64}$/.test(receipt.traceSha256 ?? '') ||
      receipt.selfTestReportPath !== expected.selfTestReportPath || !['pass', 'fail'].includes(receipt.status) ||
      !Array.isArray(receipt.requiredMutationControlNames) || !Array.isArray(receipt.mutationControls)) {
    return 'self-test receipt identity, byte digest, status, control set, and resolved output path are required';
  }
  if (['selfTestStatus', 'replayStatus', 'replayReportPath', 'requestCoverage', 'actionLabels', 'unmodeledEvents']
    .some(field => Object.hasOwn(receipt, field))) {
    return 'self-test receipt must not duplicate verifier or replay fields';
  }
  return null;
};
const selfTestReceiptWriterTest = () => {
  const fixture = {
    traceName: 'trace-fixture',
    nativeCaseKey: 'scenario/case-fixture',
    tracePath: '/tmp/trace-fixture.json',
    traceSha256: 'a'.repeat(64),
    selfTestReportPath: '/tmp/self-test-fixture.json',
    status: 'pass',
    requiredMutationControlNames: [],
    mutationControls: [],
  };
  const expected = {
    traceName: fixture.traceName,
    nativeCaseKey: fixture.nativeCaseKey,
    tracePath: fixture.tracePath,
    selfTestReportPath: fixture.selfTestReportPath,
  };
  const validAccepted = selfTestReceiptProblem(fixture, expected) === null;
  const missingOutputRejected = selfTestReceiptProblem({ ...fixture, selfTestReportPath: undefined }, expected) !== null;
  const wrongOutputRejected = selfTestReceiptProblem({ ...fixture, selfTestReportPath: '/tmp/other.json' }, expected) !== null;
  const duplicateStatusRejected = selfTestReceiptProblem({ ...fixture, selfTestStatus: 'pass' }, expected) !== null;
  return validAccepted && missingOutputRejected && wrongOutputRejected && duplicateStatusRejected;
};
if (!selfTestReceiptWriterTest()) {
  console.error('FAIL self-test receipt compatibility and output-path checks');
  process.exit(1);
}
console.log('PASS self-test receipt compatibility and output-path checks');
if (!journeyOnly && (!Array.isArray(payload.traces) || payload.traces.length === 0)) {
  console.error('FAIL trace corpus is empty');
  process.exit(1);
}
const requiredEvent = journeyOnly ? null : (contract.phase1?.requiredEvents ?? []).find((event) =>
  payload.traces.some((trace) => trace.actions?.some((action) => action.event === event)),
);
if (!journeyOnly && !requiredEvent) {
  console.error('FAIL no required event is present in the trace corpus');
  process.exit(1);
}

const workDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-trace-self-test-'));
const runMutation = (label, mutate, expectedText, { allowPartial = false, semanticActionLabel = null, expectNoInitialState = false } = {}) => {
  if (expectedText?.semanticActionLabel) {
    semanticActionLabel = expectedText.semanticActionLabel;
    expectedText = null;
  }
  const mutated = JSON.parse(JSON.stringify(payload));
  mutate(mutated);
  if (JSON.stringify(mutated) === JSON.stringify(payload)) {
    console.error('FAIL ' + label + ': mutation did not change the trace corpus');
    return false;
  }
  const path = join(workDirectory, label + '.json');
  writeFileSync(path, JSON.stringify(mutated, null, 2) + '\n');
  const verifierArgs = [
    'scripts/verify-builder-traces.mjs',
    '--traces',
    path,
    '--contract',
    contractPath,
  ];
  if (allowPartial) verifierArgs.push('--allow-partial');
  const reportPath = join(workDirectory, label + '.report.json');
  if (semanticActionLabel || expectNoInitialState) verifierArgs.push('--output', reportPath);
  const result = spawnSync(process.execPath, verifierArgs, {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const output = (result.stdout || '') + (result.stderr || '');
  if (result.status === 0) {
    console.error('FAIL ' + label + ': validator accepted the mutation');
    return false;
  }
  if (semanticActionLabel) {
    let report;
    try {
      report = JSON.parse(readFileSync(reportPath, 'utf8'));
    } catch (error) {
      console.error('FAIL ' + label + ': validator did not write a structured TLC failure report');
      console.error(error instanceof Error ? error.message : 'unknown report error');
      return false;
    }
    const trace = report.traces?.[0];
    const failure = trace?.failure;
    if (trace?.status !== 'fail' ||
        failure?.actionLabel !== semanticActionLabel ||
        failure?.message !== 'TLC did not complete the trace' ||
        !(trace.tlc?.stateCount > 0) ||
        !(trace.tlc?.distinctStateCount > 0)) {
      console.error('FAIL ' + label + ': structured report does not identify the expected TLC transition failure');
      console.error(JSON.stringify({ status: trace?.status, failure, tlc: trace?.tlc }, null, 2));
      return false;
    }
  }
  if (expectNoInitialState) {
    let report;
    try {
      report = JSON.parse(readFileSync(reportPath, 'utf8'));
    } catch (error) {
      console.error('FAIL ' + label + ': validator did not write a structured TLC report');
      console.error(error instanceof Error ? error.message : 'unknown failure');
      return false;
    }
    const trace = report.traces?.[0];
    if (trace?.status !== 'fail' || trace.failure?.message !== 'TLC found no initial state' ||
        trace.tlc?.stateCount !== 0 || trace.tlc?.distinctStateCount !== 0) {
      console.error('FAIL ' + label + ': rejection did not come from the mutated initial observation');
      console.error(JSON.stringify({ status: trace?.status, failure: trace?.failure, tlc: trace?.tlc }, null, 2));
      return false;
    }
  }
  const expectedTexts = Array.isArray(expectedText) ? expectedText : expectedText ? [expectedText] : [];
  const missingExpectedText = expectedTexts.find((text) => !output.includes(text));
  if (missingExpectedText) {
    console.error('FAIL ' + label + ': validator failed for an unexpected reason');
    console.error('Expected validator output to include: ' + missingExpectedText);
    console.error(output.split('\n').slice(-20).join('\n'));
    return false;
  }
  console.log('PASS ' + label + ': validator rejected the mutation');
  return true;
};

const findTraceAction = (traces, predicate) => {
  for (const trace of traces) {
    for (let actionIndex = 0; actionIndex < (trace.actions ?? []).length; actionIndex += 1) {
      const action = trace.actions[actionIndex];
      if (predicate(trace, action, actionIndex)) return { trace, action, actionIndex };
    }
  }
  return null;
};

const retainOnlyTrace = (mutated, hit) => {
  if (hit) mutated.traces = [hit.trace];
  return hit;
};

const hasAcceptedApplyBefore = (trace, actionIndex) => trace.actions
  .slice(0, actionIndex)
  .some((action) => action.label === 'BeginApply(accepted)');

const semanticFailureFor = (actionLabel) => ({ semanticActionLabel: actionLabel });

const isPassingTrace = (trace) =>
  trace.expectedFailure !== true &&
  trace.kind !== 'finding' &&
  trace.kind !== 'expected-failure' &&
  !/\bfinding\b|^finding:/i.test(trace.name);

const runPositiveBaseline = () => {
  const trace = payload.traces.find(isPassingTrace);
  if (!trace) {
    console.error('FAIL positive trace baseline: no passing trace is available');
    return false;
  }
  const path = join(workDirectory, 'positive-baseline.json');
  writeFileSync(path, JSON.stringify({ ...payload, traces: [trace] }, null, 2) + '\n');
  const result = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces',
    path,
    '--contract',
    contractPath,
    '--allow-partial',
    '--quiet',
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.status !== 0) {
    console.error('FAIL positive trace baseline: current validator rejected an unmutated trace');
    console.error(((result.stdout || '') + (result.stderr || '')).split('\n').slice(-20).join('\n'));
    return false;
  }
  console.log('PASS positive trace baseline: current validator accepted an unmutated ' + trace.model + ' trace');
  return true;
};

const runJourneyCrossEvidenceMutation = () => {
  const journeyContractPath = 'verification/builder-interactions/builder-journey-contract.json';
  const journeyContract = JSON.parse(readFileSync(resolve(ROOT, journeyContractPath), 'utf8'));
  const sourceTablePreview = {
    status: 'ready',
    receiptId: 'source-receipt-self-test',
    outputId: 'source-output-self-test',
    proposalId: null,
    rowsPresent: true,
    rowCount: 3,
    renderedRowCount: 3,
  };
  const initialObservation = {
    status: 'idle',
    tablePreviewStatus: 'ready',
    canApply: false,
    proposalIdPresent: false,
    proposalId: null,
    proposalOutputId: null,
    proposalSnapshotToken: null,
    proposalDraftVersion: null,
    proposalDraftDigest: null,
    previewReceiptPresent: false,
    rowsPresent: false,
    receiptPresent: false,
    previewReceiptId: null,
    previewOutputId: null,
    proposalPreviewSurface: 'unknown',
    sourceTablePreview,
    embeddedProposalPreview: null,
    apply: { available: false, enabled: false, applying: false },
  };
  const owner = { kind: 'proposal', attempt: 'P1' };
  const captureId = 'capture-proposal-self-test';
  const request = {
    method: 'POST',
    path: '/api/v1/projects/self-test/preview',
    requestId: 'request-proposal-self-test',
    captureId,
  };
  const readyResponse = {
    status: 200,
    errorCode: null,
    requestId: request.requestId,
    captureId,
    proposalId: 'proposal-receipt-self-test',
    proposalOutputId: 'output-self-test',
    proposalSnapshotToken: 'snapshot-self-test',
    proposalDraftVersion: 1,
    proposalDraftDigest: 'draft-digest-self-test',
    proposalPreviewStatus: 'ready',
    preview: {
      receiptId: 'proposal-receipt-self-test',
      outputId: 'output-self-test',
      rowsPresent: true,
      rowCount: 4,
    },
  };
  const journeyTrace = {
    name: 'self-test-only synthetic BuilderJourney capture-shaped proposal preview',
    model: 'BuilderJourney',
    initialObservation,
    initialPreview: {
      owner: { kind: 'preview', attempt: 'P1' },
      request: {
        method: 'POST',
        path: '/api/v1/projects/self-test/source-preview',
        requestId: 'request-source-self-test',
        captureId: 'capture-source-self-test',
      },
      response: {
        status: 200,
        errorCode: null,
        requestId: 'request-source-self-test',
        captureId: 'capture-source-self-test',
        preview: {
          receiptId: sourceTablePreview.receiptId,
          outputId: sourceTablePreview.outputId,
          rowsPresent: true,
          rowCount: sourceTablePreview.rowCount,
        },
      },
      settlement: { kind: 'http-response', captureId: 'capture-source-self-test' },
    },
    actions: [
      {
        label: 'Preview',
        event: 'preview',
        owner,
        request,
        observation: {
          status: 'previewing',
          tablePreviewStatus: 'ready',
          canApply: false,
          proposalIdPresent: false,
          previewReceiptPresent: false,
          rowsPresent: false,
          receiptPresent: false,
          proposalId: null,
          proposalOutputId: null,
          proposalSnapshotToken: null,
          proposalDraftVersion: null,
          proposalDraftDigest: null,
          previewReceiptId: null,
          previewOutputId: null,
          proposalPreviewSurface: 'unknown',
          sourceTablePreview,
          embeddedProposalPreview: null,
          apply: { available: false, enabled: false, applying: false },
        },
      },
      {
        label: 'PreviewComplete',
        event: 'preview-response',
        owner,
        request,
        response: readyResponse,
        settlement: { kind: 'http-response', captureId },
        observation: {
          status: 'ready',
          tablePreviewStatus: 'ready',
          canApply: true,
          proposalIdPresent: true,
          previewReceiptPresent: true,
          rowsPresent: true,
          receiptPresent: true,
          proposalId: readyResponse.proposalId,
          proposalOutputId: readyResponse.proposalOutputId,
          proposalSnapshotToken: readyResponse.proposalSnapshotToken,
          proposalDraftVersion: readyResponse.proposalDraftVersion,
          proposalDraftDigest: readyResponse.proposalDraftDigest,
          previewReceiptId: readyResponse.preview.receiptId,
          previewOutputId: readyResponse.preview.outputId,
          proposalPreviewSurface: 'row-operation',
          sourceTablePreview,
          embeddedProposalPreview: {
            status: 'ready',
            receiptId: readyResponse.preview.receiptId,
            outputId: readyResponse.preview.outputId,
            proposalId: readyResponse.proposalId,
            draftVersion: readyResponse.proposalDraftVersion,
            draftDigest: readyResponse.proposalDraftDigest,
            rowsPresent: true,
            rowCount: readyResponse.preview.rowCount,
            renderedRowCount: readyResponse.preview.rowCount,
          },
          apply: { available: true, enabled: true, applying: false },
        },
      },
    ],
  };
  const journeyPayload = {
    ...payload,
    sourceProfile: 'journey',
    contractSourceCommit: journeyContract.sourceCommit,
    traces: [journeyTrace],
  };
  const baselinePath = join(workDirectory, 'journey-positive-baseline.json');
  const baselineReportPath = join(workDirectory, 'journey-positive-baseline.report.json');
  writeFileSync(baselinePath, JSON.stringify(journeyPayload, null, 2) + '\n');
  const baseline = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces', baselinePath,
    '--contract', journeyContractPath,
    '--allow-partial',
    '--quiet',
    '--output', baselineReportPath,
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (baseline.status !== 0) {
    console.error('FAIL journey positive baseline: current validator rejected the self-test-only capture-shaped trace');
    console.error(JSON.stringify(JSON.parse(readFileSync(baselineReportPath, 'utf8')).traces?.[0] ?? null, null, 2));
    console.error(((baseline.stdout || '') + (baseline.stderr || '')).split('\n').slice(-12).join('\n'));
    return false;
  }
  const baselineReport = JSON.parse(readFileSync(baselineReportPath, 'utf8'));
  if (baselineReport.traces?.[0]?.status !== 'pass') {
    console.error('FAIL journey positive baseline: report did not mark the unmutated trace as passing');
    console.error(JSON.stringify(baselineReport.traces?.[0] ?? null, null, 2));
    return false;
  }
  console.log('PASS generic BuilderJourney trace without groupLifecycle: validator accepted the capture-shaped preview trace under builder-journey-contract');

  const mainTableJourneyPayload = JSON.parse(JSON.stringify(journeyPayload));
  const mainTableObservation = mainTableJourneyPayload.traces[0].actions[1].observation;
  const mainTablePreview = {
    ...sourceTablePreview,
    status: 'ready',
    receiptId: readyResponse.preview.receiptId,
    outputId: readyResponse.preview.outputId,
    proposalId: readyResponse.proposalId,
    draftVersion: readyResponse.proposalDraftVersion,
    draftDigest: readyResponse.proposalDraftDigest,
    rowsPresent: readyResponse.preview.rowsPresent,
    rowCount: readyResponse.preview.rowCount,
    renderedRowCount: readyResponse.preview.rowCount,
  };
  mainTableObservation.proposalPreviewSurface = 'main-table';
  mainTableObservation.sourceTablePreview = mainTablePreview;
  mainTableObservation.embeddedProposalPreview = {
    status: mainTablePreview.status,
    receiptId: mainTablePreview.receiptId,
    outputId: mainTablePreview.outputId,
    proposalId: mainTablePreview.proposalId,
    rowsPresent: mainTablePreview.rowsPresent,
    rowCount: mainTablePreview.rowCount,
    renderedRowCount: mainTablePreview.renderedRowCount,
  };
  const mainTablePath = join(workDirectory, 'journey-main-table-positive-baseline.json');
  const mainTableReportPath = join(workDirectory, 'journey-main-table-positive-baseline.report.json');
  writeFileSync(mainTablePath, JSON.stringify(mainTableJourneyPayload, null, 2) + '\n');
  const mainTableBaseline = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces', mainTablePath,
    '--contract', journeyContractPath,
    '--allow-partial',
    '--quiet',
    '--output', mainTableReportPath,
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const mainTableReport = JSON.parse(readFileSync(mainTableReportPath, 'utf8'));
  if (mainTableBaseline.status !== 0 || mainTableReport.traces?.[0]?.status !== 'pass') {
    console.error('FAIL journey main-table positive baseline: validator rejected matching raw and selected main-table preview evidence');
    console.error(JSON.stringify(mainTableReport.traces?.[0] ?? null, null, 2));
    console.error(((mainTableBaseline.stdout || '') + (mainTableBaseline.stderr || '')).split('\n').slice(-12).join('\n'));
    return false;
  }
  console.log('PASS journey main-table positive baseline: validator accepted matching raw and selected preview evidence');

  const mutated = JSON.parse(JSON.stringify(journeyPayload));
  mutated.traces[0].actions[1].response.proposalId = 'forged-response-proposal-id';
  const mutatedPath = join(workDirectory, 'journey-response-proposal-id-mismatch.json');
  const mutatedReportPath = join(workDirectory, 'journey-response-proposal-id-mismatch.report.json');
  writeFileSync(mutatedPath, JSON.stringify(mutated, null, 2) + '\n');
  const result = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces', mutatedPath,
    '--contract', journeyContractPath,
    '--allow-partial',
    '--quiet',
    '--output', mutatedReportPath,
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.status === 0) {
    console.error('FAIL journey-response-proposal-id-mismatch: validator accepted a response/rendered proposal ID mismatch');
    return false;
  }
  const mutatedReport = JSON.parse(readFileSync(mutatedReportPath, 'utf8'));
  const failedTrace = mutatedReport.traces?.[0];
  const diagnostic = 'traces[0].actions[1] response.proposalId does not match displayed observation.proposalId';
  if (failedTrace?.status !== 'fail' ||
      !failedTrace.failure?.message?.includes(diagnostic) ||
      failedTrace.tlc !== undefined) {
    console.error('FAIL journey-response-proposal-id-mismatch: rejection did not come from the intended pre-TLC cross-evidence check');
    console.error(JSON.stringify(failedTrace ?? null, null, 2));
    return false;
  }
  console.log('PASS journey-response-proposal-id-mismatch: validator rejected the forged response ID before TLC');

  const missingObservationIdentity = JSON.parse(JSON.stringify(journeyPayload));
  missingObservationIdentity.traces[0].actions[1].observation.proposalOutputId = null;
  const missingObservationPath = join(workDirectory, 'journey-missing-observed-output-id.json');
  const missingObservationReportPath = join(workDirectory, 'journey-missing-observed-output-id.report.json');
  writeFileSync(missingObservationPath, JSON.stringify(missingObservationIdentity, null, 2) + '\n');
  const missingObservationResult = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces', missingObservationPath,
    '--contract', journeyContractPath,
    '--allow-partial',
    '--quiet',
    '--output', missingObservationReportPath,
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (missingObservationResult.status === 0) {
    console.error('FAIL journey-missing-observed-output-id: validator accepted a missing rendered output ID');
    return false;
  }
  const missingObservationReport = JSON.parse(readFileSync(missingObservationReportPath, 'utf8'));
  const missingObservationFailure = missingObservationReport.traces?.[0];
  const missingObservationDiagnostic = 'traces[0].actions[1] response.proposalOutputId does not match displayed observation.proposalOutputId';
  if (missingObservationFailure?.status !== 'fail' ||
      !missingObservationFailure.failure?.message?.includes(missingObservationDiagnostic) ||
      missingObservationFailure.tlc !== undefined) {
    console.error('FAIL journey-missing-observed-output-id: rejection did not come from the intended pre-TLC cross-evidence check');
    console.error(JSON.stringify(missingObservationFailure ?? null, null, 2));
    return false;
  }
  console.log('PASS journey-missing-observed-output-id: validator rejected the absent rendered output ID before TLC');

  const runJourneySurfaceMutation = (label, mutateObservation, diagnostic) => {
    const surfaceMutation = JSON.parse(JSON.stringify(journeyPayload));
    mutateObservation(surfaceMutation.traces[0].actions[1].observation);
    const surfacePath = join(workDirectory, label + '.json');
    const surfaceReportPath = join(workDirectory, label + '.report.json');
    writeFileSync(surfacePath, JSON.stringify(surfaceMutation, null, 2) + '\n');
    const surfaceResult = spawnSync(process.execPath, [
      'scripts/verify-builder-traces.mjs',
      '--traces', surfacePath,
      '--contract', journeyContractPath,
      '--allow-partial',
      '--quiet',
      '--output', surfaceReportPath,
    ], {
      cwd: ROOT,
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
    });
    if (surfaceResult.status === 0) {
      console.error('FAIL ' + label + ': validator accepted source preview data as proposal preview evidence');
      return false;
    }
    const surfaceReport = JSON.parse(readFileSync(surfaceReportPath, 'utf8'));
    const surfaceFailure = surfaceReport.traces?.[0];
    if (surfaceFailure?.status !== 'fail' ||
        !surfaceFailure.failure?.message?.includes(diagnostic) ||
        surfaceFailure.tlc !== undefined) {
      console.error('FAIL ' + label + ': rejection did not come from the intended pre-TLC preview-surface check');
      console.error(JSON.stringify(surfaceFailure ?? null, null, 2));
      return false;
    }
    console.log('PASS ' + label + ': validator rejected source preview data as proposal preview evidence');
    return true;
  };

  const absentProposalId = runJourneySurfaceMutation(
    'journey-source-preview-without-proposal-id',
    (observation) => {
      observation.proposalPreviewSurface = 'main-table';
      observation.embeddedProposalPreview = { ...observation.sourceTablePreview };
      observation.previewReceiptPresent = true;
      observation.rowsPresent = sourceTablePreview.rowsPresent;
      observation.receiptPresent = true;
      observation.previewReceiptId = sourceTablePreview.receiptId;
      observation.previewOutputId = sourceTablePreview.outputId;
    },
    'traces[0].actions[1].observation.main-table proposal preview requires sourceTablePreview.proposalId',
  );
  const mismatchedProposalId = runJourneySurfaceMutation(
    'journey-source-preview-with-mismatched-proposal-id',
    (observation) => {
      const staleProposalId = 'stale-source-proposal-self-test';
      const sourcePreviewAsCandidate = { ...observation.sourceTablePreview, proposalId: staleProposalId };
      observation.proposalPreviewSurface = 'main-table';
      observation.sourceTablePreview = sourcePreviewAsCandidate;
      observation.embeddedProposalPreview = sourcePreviewAsCandidate;
      observation.previewReceiptPresent = true;
      observation.rowsPresent = sourcePreviewAsCandidate.rowsPresent;
      observation.receiptPresent = true;
      observation.previewReceiptId = sourcePreviewAsCandidate.receiptId;
      observation.previewOutputId = sourcePreviewAsCandidate.outputId;
    },
    'traces[0].actions[1].observation.main-table sourceTablePreview.proposalId does not match displayed proposalId',
  );
  const mismatchedReceiptId = runJourneySurfaceMutation(
    'journey-main-table-matching-proposal-with-stale-receipt',
    (observation) => {
      const stalePreview = {
        ...observation.sourceTablePreview,
        proposalId: observation.proposalId,
        receiptId: 'stale-source-receipt-self-test',
      };
      observation.proposalPreviewSurface = 'main-table';
      observation.sourceTablePreview = stalePreview;
      observation.embeddedProposalPreview = { ...stalePreview };
      observation.previewReceiptPresent = true;
      observation.rowsPresent = stalePreview.rowsPresent;
      observation.receiptPresent = true;
      observation.previewReceiptId = stalePreview.receiptId;
      observation.previewOutputId = stalePreview.outputId;
    },
    'traces[0].actions[1].observation.main-table sourceTablePreview.receiptId does not match displayed proposalId',
  );
  if (!absentProposalId || !mismatchedProposalId || !mismatchedReceiptId) return false;
  return true;
};

const runJourneyChoiceProofMutations = () => {
  const addColumnContractPath = 'verification/builder-interactions/builder-add-column-contract.json';
  const addColumnContract = JSON.parse(readFileSync(resolve(ROOT, addColumnContractPath), 'utf8'));
  const commandId = 'choice-command-self-test';
  const choiceCaptureId = 'choice-capture-self-test';
  const commandCaptureId = 'command-capture-self-test';
  const choicePreview = {
    receiptId: 'choice-receipt-self-test',
    outputId: 'choice-output-self-test',
    rowsPresent: true,
    rowCount: 2,
  };
  const sourcePreview = {
    status: 'ready',
    receiptId: 'source-receipt-before-choice-self-test',
    outputId: 'source-output-before-choice-self-test',
    proposalId: null,
    draftVersion: 1,
    draftDigest: 'source-draft-self-test',
    rowsPresent: true,
    rowCount: 2,
    renderedRowCount: 2,
  };
  const observed = (published) => ({
    status: 'absent',
    tablePreviewStatus: 'ready',
    canApply: false,
    proposalIdPresent: false,
    proposalId: null,
    proposalOutputId: null,
    proposalSnapshotToken: null,
    proposalDraftVersion: null,
    proposalDraftDigest: null,
    previewReceiptPresent: false,
    rowsPresent: false,
    receiptPresent: false,
    previewReceiptId: null,
    previewOutputId: null,
    proposalPreviewSurface: 'unknown',
    sourceTablePreview: published ? {
      ...sourcePreview,
      receiptId: choicePreview.receiptId,
      outputId: choicePreview.outputId,
    } : sourcePreview,
    embeddedProposalPreview: null,
    apply: { available: false, enabled: false, applying: false },
  });
  const proposalOwner = { kind: 'proposal', attempt: 'P1' };
  const applyOwner = { kind: 'apply', attempt: 'P1' };
  const choiceRequest = {
    method: 'POST',
    path: 'construction-choice-proposals',
    requestId: 'choice-request-self-test',
    captureId: choiceCaptureId,
    commandId,
  };
  const choiceResponse = {
    status: 200,
    requestId: choiceRequest.requestId,
    captureId: choiceCaptureId,
    commandId,
    proposalId: null,
    proposalOutputId: null,
    proposalSnapshotToken: null,
    proposalDraftVersion: null,
    proposalDraftDigest: null,
    proposalPreviewStatus: 'READY',
    preview: choicePreview,
    errorCode: null,
  };
  const commandRequest = {
    method: 'POST',
    path: 'commands',
    requestId: 'command-request-self-test',
    captureId: commandCaptureId,
    commandId,
  };
  const commandResponse = {
    status: 200,
    requestId: commandRequest.requestId,
    captureId: commandCaptureId,
    commandId,
    errorCode: null,
  };
  const choiceTrace = {
    name: 'self-test-only synthetic BuilderJourney choice-preview and AddColumn capture',
    model: 'BuilderJourney',
    initialObservation: observed(false),
    initialPreview: {
      owner: { kind: 'preview', attempt: 'P1' },
      request: {
        method: 'POST',
        path: 'preview',
        requestId: 'source-request-self-test',
        captureId: 'source-capture-self-test',
      },
      response: {
        status: 200,
        requestId: 'source-request-self-test',
        captureId: 'source-capture-self-test',
        errorCode: null,
        preview: {
          receiptId: sourcePreview.receiptId,
          outputId: sourcePreview.outputId,
          rowsPresent: true,
          rowCount: sourcePreview.rowCount,
        },
      },
      settlement: { kind: 'http-response', captureId: 'source-capture-self-test' },
    },
    actions: [
      {
        label: 'ChoicePreview',
        event: 'choice-preview',
        owner: proposalOwner,
        request: choiceRequest,
        observation: observed(false),
      },
      {
        label: 'ChoicePreviewComplete',
        event: 'choice-preview-response',
        owner: proposalOwner,
        request: choiceRequest,
        response: choiceResponse,
        settlement: { kind: 'http-response', captureId: choiceCaptureId },
        // This snapshot is intentionally still the old table. Publication is
        // proved only by the later AddColumn command observation.
        observation: observed(false),
      },
      {
        label: 'AddColumn',
        event: 'add-column',
        owner: applyOwner,
        request: commandRequest,
        response: commandResponse,
        settlement: { kind: 'http-response', captureId: commandCaptureId },
        observation: observed(true),
      },
    ],
  };
  const choicePayload = {
    ...payload,
    sourceProfile: 'journey',
    contractSourceCommit: addColumnContract.sourceCommit,
    traces: [choiceTrace],
  };
  const invoke = (label, candidate, expectedText, semanticActionLabel = null) => {
    const tracePath = join(workDirectory, label + '.json');
    const reportPath = join(workDirectory, label + '.report.json');
    writeFileSync(tracePath, JSON.stringify(candidate, null, 2) + '\n');
    const verifierArgs = [
      'scripts/verify-builder-traces.mjs',
      '--traces', tracePath,
      '--contract', addColumnContractPath,
      '--allow-partial',
      '--quiet',
      '--output', reportPath,
    ];
    const result = spawnSync(process.execPath, verifierArgs, {
      cwd: ROOT,
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
    });
    if (semanticActionLabel) {
      let report;
      try {
        report = JSON.parse(readFileSync(reportPath, 'utf8'));
      } catch (error) {
        console.error('FAIL ' + label + ': verifier did not write a structured TLC failure report');
        console.error(error instanceof Error ? error.message : 'unknown failure');
        return false;
      }
      const trace = report.traces?.[0];
      if (result.status === 0 || trace?.status !== 'fail' ||
          trace.failure?.actionLabel !== semanticActionLabel ||
          trace.failure?.message !== 'TLC did not complete the trace' ||
          !(trace.tlc?.stateCount > 0) || !(trace.tlc?.distinctStateCount > 0)) {
        console.error('FAIL ' + label + ': rejection did not identify the expected missing choice transition');
        console.error(JSON.stringify(trace ?? null, null, 2));
        return false;
      }
      console.log('PASS ' + label + ': TLC rejected the out-of-order choice proof at ' + semanticActionLabel);
      return true;
    }
    const output = (result.stdout || '') + (result.stderr || '');
    if (result.status === 0 || !output.includes(expectedText)) {
      console.error('FAIL ' + label + ': validator did not reject the choice mutation for the intended reason');
      console.error('Expected diagnostic: ' + expectedText);
      console.error(output.split('\n').slice(-16).join('\n'));
      return false;
    }
    console.log('PASS ' + label + ': validator rejected the choice mutation before TLC');
    return true;
  };

  const baselinePath = join(workDirectory, 'journey-choice-positive-baseline.json');
  const baselineReportPath = join(workDirectory, 'journey-choice-positive-baseline.report.json');
  writeFileSync(baselinePath, JSON.stringify(choicePayload, null, 2) + '\n');
  const baseline = spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces', baselinePath,
    '--contract', addColumnContractPath,
    '--allow-partial',
    '--quiet',
    '--output', baselineReportPath,
  ], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (baseline.status !== 0) {
    console.error('FAIL journey choice positive baseline: validator rejected the complete choice-to-publication proof');
    console.error(((baseline.stdout || '') + (baseline.stderr || '')).split('\n').slice(-18).join('\n'));
    try {
      console.error(JSON.stringify(JSON.parse(readFileSync(baselineReportPath, 'utf8')).traces?.[0] ?? null, null, 2));
    } catch {}
    return false;
  }
  console.log('PASS journey choice positive baseline: all four command IDs and later published preview matched');

  const wrongCommandId = JSON.parse(JSON.stringify(choicePayload));
  wrongCommandId.traces[0].actions[1].response.commandId = 'wrong-choice-command-self-test';
  const wrongCommandPass = invoke(
    'journey-choice-wrong-command-id',
    wrongCommandId,
    'ChoicePreviewComplete request/response commandId values must match the ChoicePreview request commandId',
  );

  const outOfOrder = JSON.parse(JSON.stringify(choicePayload));
  outOfOrder.traces[0].actions = [
    outOfOrder.traces[0].actions[0],
    outOfOrder.traces[0].actions[2],
    outOfOrder.traces[0].actions[1],
  ];
  const orderPass = invoke('journey-choice-proof-out-of-order', outOfOrder, null, 'AddColumn');

  const wrongPublishedPreview = JSON.parse(JSON.stringify(choicePayload));
  wrongPublishedPreview.traces[0].actions[2].observation.sourceTablePreview.outputId = 'wrong-published-output-self-test';
  const publishedPass = invoke(
    'journey-choice-wrong-published-source-output',
    wrongPublishedPreview,
    'AddColumn sourceTablePreview receipt/output does not match the ChoicePreviewComplete response.preview',
  );

  return wrongCommandPass && orderPass && publishedPass;
};

const runJourneyRequestCancellationMutation = () => {
  const journeyContractPath = 'verification/builder-interactions/builder-journey-contract.json';
  const journeyContract = JSON.parse(readFileSync(resolve(ROOT, journeyContractPath), 'utf8'));
  const sourceTablePreview = {
    status: 'ready',
    receiptId: 'source-receipt-cancel-self-test',
    outputId: 'source-output-cancel-self-test',
    proposalId: null,
    draftVersion: 1,
    draftDigest: 'source-draft-cancel-self-test',
    rowsPresent: true,
    rowCount: 2,
    renderedRowCount: 2,
  };
  const observation = (status) => ({
    status,
    proposalPanelPresent: status !== 'absent',
    tablePreviewStatus: 'ready',
    canApply: status === 'ready',
    proposalIdPresent: status === 'ready',
    proposalId: status === 'ready' ? 'proposal-cancel-p2-self-test' : null,
    proposalOutputId: status === 'ready' ? 'output-cancel-p2-self-test' : null,
    proposalSnapshotToken: status === 'ready' ? 'snapshot-cancel-p2-self-test' : null,
    proposalDraftVersion: status === 'ready' ? 2 : null,
    proposalDraftDigest: status === 'ready' ? 'draft-cancel-p2-self-test' : null,
    proposalPreviewStatus: status === 'ready' ? 'READY' : null,
    previewReceiptPresent: status === 'ready',
    rowsPresent: status === 'ready',
    receiptPresent: status === 'ready',
    previewReceiptId: status === 'ready' ? 'proposal-cancel-p2-self-test' : null,
    previewOutputId: status === 'ready' ? 'output-cancel-p2-self-test' : null,
    proposalPreviewSurface: status === 'ready' ? 'row-operation' : 'unknown',
    sourceTablePreview,
    embeddedProposalPreview: status === 'ready' ? {
      status: 'ready',
      receiptId: 'proposal-cancel-p2-self-test',
      outputId: 'output-cancel-p2-self-test',
      proposalId: 'proposal-cancel-p2-self-test',
      draftVersion: 2,
      draftDigest: 'draft-cancel-p2-self-test',
      rowsPresent: true,
      rowCount: 2,
      renderedRowCount: 2,
    } : null,
    apply: { available: status === 'ready', enabled: status === 'ready', applying: false },
  });
  const pendingObservation = observation('previewing');
  const p1Owner = { kind: 'proposal', attempt: 'P1' };
  const p2Owner = { kind: 'proposal', attempt: 'P2' };
  const p1CaptureId = 'preview-cancel-p1-capture';
  const p2CaptureId = 'preview-cancel-p2-capture';
  const p1Request = {
    method: 'POST',
    path: 'construction-proposals',
    requestId: 'preview-cancel-p1-wire',
    captureId: p1CaptureId,
  };
  const p2Request = {
    method: 'POST',
    path: 'construction-proposals',
    requestId: 'preview-cancel-p2-wire',
    captureId: p2CaptureId,
  };
  const p2Response = {
    status: 200,
    requestId: p2Request.requestId,
    captureId: p2CaptureId,
    errorCode: null,
    proposalId: 'proposal-cancel-p2-self-test',
    proposalOutputId: 'output-cancel-p2-self-test',
    proposalSnapshotToken: 'snapshot-cancel-p2-self-test',
    proposalDraftVersion: 2,
    proposalDraftDigest: 'draft-cancel-p2-self-test',
    proposalPreviewStatus: 'READY',
    preview: {
      receiptId: 'proposal-cancel-p2-self-test',
      outputId: 'output-cancel-p2-self-test',
      rowsPresent: true,
      rowCount: 2,
    },
  };
  const trace = {
    name: 'self-test-only BuilderJourney current-owner aborted preview before replacement',
    model: 'BuilderJourney',
    initialObservation: observation('absent'),
    initialPreview: {
      owner: { kind: 'preview', attempt: 'P1' },
      request: {
        method: 'POST',
        path: 'preview',
        requestId: 'preview-cancel-source-wire',
        captureId: 'preview-cancel-source-capture',
      },
      response: {
        status: 200,
        requestId: 'preview-cancel-source-wire',
        captureId: 'preview-cancel-source-capture',
        errorCode: null,
        preview: {
          receiptId: sourceTablePreview.receiptId,
          outputId: sourceTablePreview.outputId,
          rowsPresent: true,
          rowCount: sourceTablePreview.rowCount,
        },
      },
      settlement: { kind: 'http-response', captureId: 'preview-cancel-source-capture' },
    },
    actions: [
      {
        label: 'Preview',
        event: 'preview',
        owner: p1Owner,
        request: p1Request,
        observation: pendingObservation,
      },
      {
        label: 'PreviewRequestCancelled',
        event: 'preview-request-cancelled',
        owner: p1Owner,
        request: p1Request,
        response: null,
        settlement: { kind: 'request-failed', captureId: p1CaptureId, errorText: 'net::ERR_ABORTED' },
        observation: structuredClone(pendingObservation),
      },
      {
        label: 'Preview',
        event: 'preview',
        owner: p2Owner,
        request: p2Request,
        observation: pendingObservation,
      },
      {
        label: 'PreviewComplete',
        event: 'preview-response',
        owner: p2Owner,
        request: p2Request,
        response: p2Response,
        settlement: { kind: 'http-response', captureId: p2CaptureId },
        observation: observation('ready'),
      },
    ],
  };
  const cancellationPayload = {
    ...payload,
    sourceProfile: 'journey',
    contractSourceCommit: journeyContract.sourceCommit,
    traces: [trace],
  };
  const run = (label, candidate, reportPath) => {
    const tracePath = join(workDirectory, label + '.json');
    writeFileSync(tracePath, JSON.stringify(candidate, null, 2) + '\n');
    const result = spawnSync(process.execPath, [
      'scripts/verify-builder-traces.mjs',
      '--traces', tracePath,
      '--contract', journeyContractPath,
      '--allow-partial',
      '--quiet',
      '--output', reportPath,
    ], {
      cwd: ROOT,
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
    });
    return { result, output: (result.stdout || '') + (result.stderr || '') };
  };

  const baselineReportPath = join(workDirectory, 'journey-request-cancel-positive.report.json');
  const baseline = run('journey-request-cancel-positive', cancellationPayload, baselineReportPath);
  if (baseline.result.status !== 0) {
    console.error('FAIL journey request-cancellation positive baseline: validator rejected the current-owner abort stutter');
    console.error(baseline.output.split('\n').slice(-18).join('\n'));
    try {
      console.error(JSON.stringify(JSON.parse(readFileSync(baselineReportPath, 'utf8')).traces?.[0] ?? null, null, 2));
    } catch {}
    return false;
  }
  console.log('PASS journey request-cancellation positive baseline: P1 abort stutter precedes P2 preview');

  const mutation = JSON.parse(JSON.stringify(cancellationPayload));
  mutation.traces[0].actions[1].observation.sourceTablePreview.receiptId = 'changed-source-receipt-cancel-self-test';
  const mutationReportPath = join(workDirectory, 'journey-request-cancel-observation-mutation.report.json');
  const mutated = run('journey-request-cancel-observation-mutation', mutation, mutationReportPath);
  let report;
  try {
    report = JSON.parse(readFileSync(mutationReportPath, 'utf8'));
  } catch (error) {
    console.error('FAIL journey request-cancel-observation-mutation: verifier did not write a structured TLC report');
    console.error(error instanceof Error ? error.message : 'unknown failure');
    return false;
  }
  const failedTrace = report.traces?.[0];
  if (mutated.result.status === 0 || failedTrace?.status !== 'fail' ||
      failedTrace.failure?.actionLabel !== 'PreviewRequestCancelled' ||
      failedTrace.failure?.message !== 'TLC did not complete the trace' ||
      !(failedTrace.tlc?.stateCount > 0) || !(failedTrace.tlc?.distinctStateCount > 0)) {
    console.error('FAIL journey request-cancel-observation-mutation: rejection did not come from the cancellation stutter guard');
    console.error(JSON.stringify(failedTrace ?? null, null, 2));
    return false;
  }
  console.log('PASS journey request-cancel-observation-mutation: ' + failedTrace.failure.message +
    ' at ' + failedTrace.failure.actionLabel + ' (' + failedTrace.tlc.stateCount + ' states)');
  return true;
};

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const fingerprintFor = (sources, root) => sha256(JSON.stringify(sources
  .map(({ path }) => ({ path, sha256: sha256(readFileSync(resolve(root, path))) }))
  .sort((left, right) => left.path.localeCompare(right.path))));

if (journeyOnly) {
  const profilePath = 'verification/builder-interactions/builder-preview-contract.json';
  const profile = JSON.parse(readFileSync(resolve(ROOT, profilePath), 'utf8'));
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' });
  if (head.status !== 0 || !['formal', 'journey'].includes(contract.sourceProfile)) {
    console.error('FAIL journey-only: cannot establish selected source profile provenance');
    process.exit(1);
  }
  if (!tracesPath) {
    payload = {
      schemaVersion: 1,
      sourceProfile: contract.sourceProfile,
      sourceCommit: (head.stdout || '').trim(),
      contractSourceCommit: contract.sourceCommit,
      sourceFingerprint: fingerprintFor(profile.sources, ROOT),
      traces: [],
    };
  }
}

const runSourceProfileMutation = () => {
  const selectedContract = JSON.parse(readFileSync(resolve(ROOT, contractPath), 'utf8'));
  const profilePath = 'verification/builder-interactions/builder-preview-contract.json';
  if (!['formal', 'journey'].includes(selectedContract.sourceProfile)) {
    console.error('FAIL source-profile-byte-mutation: selected contract must declare sourceProfile formal or journey');
    return false;
  }
  const profile = JSON.parse(readFileSync(resolve(ROOT, profilePath), 'utf8'));
  if (!Array.isArray(profile.sources) || profile.sources.length === 0) {
    console.error('FAIL source-profile-byte-mutation: reviewed profile has no source pins');
    return false;
  }
  const selectedTrace = payload.traces.find(isPassingTrace);
  if (!selectedTrace) {
    console.error('FAIL source-profile-byte-mutation: no passing trace is available for the fixture baseline');
    return false;
  }
  const rootHead = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' });
  if (rootHead.status !== 0) {
    console.error('FAIL source-profile-byte-mutation: cannot read source checkout Git HEAD');
    return false;
  }
  const contractSourceCommit = selectedContract.sourceCommit;
  if (payload.sourceCommit !== (rootHead.stdout || '').trim() ||
      payload.contractSourceCommit !== contractSourceCommit ||
      payload.sourceProfile !== selectedContract.sourceProfile ||
      payload.sourceFingerprint !== fingerprintFor(profile.sources, ROOT)) {
    console.error('FAIL source-profile-byte-mutation: trace provenance does not match the source checkout and reviewed profile');
    return false;
  }

  const fixtureDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-trace-source-self-test-'));
  const fixtureRoot = join(fixtureDirectory, 'repo');
  const fixtureContractPath = 'verification/builder-interactions/trace-self-test-contract.json';
  const fixtureTracePath = 'trace-source-self-test.json';
  mkdirSync(fixtureRoot, { recursive: true });
  const runGit = (args) => {
    const result = spawnSync('git', args, { cwd: fixtureRoot, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
    if (result.status !== 0) {
      throw new Error((result.stderr || result.stdout || 'git ' + args.join(' ') + ' failed').trim());
    }
    return (result.stdout || '').trim();
  };
  const runVerifier = () => spawnSync(process.execPath, [
    'scripts/verify-builder-traces.mjs',
    '--traces',
    fixtureTracePath,
    '--contract',
    fixtureContractPath,
    '--allow-partial',
    '--quiet',
  ], {
    cwd: fixtureRoot,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });

  try {
    const profilePaths = new Set([
      ...profile.sources.map((source) => source.path),
      'scripts/verify-builder-traces.mjs',
      profilePath,
      'verification/builder-interactions/' + selectedTrace.model + '.tla',
      fixtureContractPath,
    ]);
    for (const path of profilePaths) {
      const target = resolve(fixtureRoot, path);
      mkdirSync(dirname(target), { recursive: true });
      if (path === fixtureContractPath) {
        writeFileSync(target, JSON.stringify(selectedContract, null, 2) + '\n');
      } else {
        copyFileSync(resolve(ROOT, path), target);
      }
    }
    runGit(['init', '--quiet', '--object-format=sha1']);
    runGit(['add', '--', ...profilePaths]);
    runGit([
      '-c', 'user.name=Codex trace self-test',
      '-c', 'user.email=trace-self-test@example.invalid',
      'commit', '--quiet', '-m', 'trace source provenance self-test',
    ]);

    const fixtureHead = runGit(['rev-parse', 'HEAD']);
    const fixtureContract = JSON.parse(readFileSync(resolve(fixtureRoot, fixtureContractPath), 'utf8'));
    const fixtureProfile = JSON.parse(readFileSync(resolve(fixtureRoot, profilePath), 'utf8'));
    if (fixtureContract.sourceCommit !== contractSourceCommit ||
        fixtureContract.sourceProfile !== selectedContract.sourceProfile ||
        fingerprintFor(fixtureProfile.sources, fixtureRoot) !== payload.sourceFingerprint) {
      throw new Error('fixture contract or reviewed source fingerprint differs from the trace provenance');
    }

    const fixturePayload = {
      ...payload,
      sourceCommit: fixtureHead,
      contractSourceCommit: fixtureContract.sourceCommit,
      sourceProfile: fixtureContract.sourceProfile,
      sourceFingerprint: fingerprintFor(fixtureProfile.sources, fixtureRoot),
      traces: [selectedTrace],
    };
    writeFileSync(resolve(fixtureRoot, fixtureTracePath), JSON.stringify(fixturePayload, null, 2) + '\n');
    const baseline = runVerifier();
    const baselineOutput = (baseline.stdout || '') + (baseline.stderr || '');
    if (baseline.status !== 0) {
      console.error('FAIL source-profile-byte-mutation: matching source profile was rejected before mutation');
      console.error(baselineOutput.split('\n').slice(-20).join('\n'));
      return false;
    }

    const sourcePath = profile.sources[0].path;
    appendFileSync(resolve(fixtureRoot, sourcePath), Buffer.from('\n'));
    const headAfterMutation = runGit(['rev-parse', 'HEAD']);
    if (headAfterMutation !== fixtureHead) {
      console.error('FAIL source-profile-byte-mutation: source mutation changed Git HEAD');
      return false;
    }

    const mutated = runVerifier();
    const mutatedOutput = (mutated.stdout || '') + (mutated.stderr || '');
    if (mutated.status === 0) {
      console.error('FAIL source-profile-byte-mutation: validator accepted changed source bytes at unchanged HEAD');
      return false;
    }
    const expectedDiagnostic = 'reviewed source SHA mismatch at ' + sourcePath +
      ': expected ' + profile.sources[0].sha256 + ', found ';
    const diagnosticIndex = mutatedOutput.indexOf(expectedDiagnostic);
    const actualHash = diagnosticIndex < 0
      ? ''
      : mutatedOutput.slice(diagnosticIndex + expectedDiagnostic.length, diagnosticIndex + expectedDiagnostic.length + 64);
    if (!/^[0-9a-f]{64}$/.test(actualHash)) {
      console.error('FAIL source-profile-byte-mutation: validator failed for an unexpected reason');
      console.error('Expected validator output to include: ' + expectedDiagnostic + '<64-character SHA-256>');
      console.error(mutatedOutput.split('\n').slice(-20).join('\n'));
      return false;
    }
    console.log('PASS source-profile-byte-mutation: validator rejected changed source bytes at unchanged Git HEAD');
    return true;
  } catch (error) {
    console.error('FAIL source-profile-byte-mutation: ' + (error instanceof Error ? error.message : 'unknown failure'));
    return false;
  } finally {
    rmSync(fixtureDirectory, { recursive: true, force: true });
  }
};

if (journeyOnly) {
  const crossEvidencePassed = runJourneyCrossEvidenceMutation();
  const choiceProofPassed = runJourneyChoiceProofMutations();
  const requestCancellationPassed = runJourneyRequestCancellationMutation();
  let repeatEditOwnerPassed = true;
  let repeatEditRequiredPassed = true;
  let lateAbortOwnerPassed = true;
  let groupLifecycleControlsPassed = true;
  if (tracesPath && contractPath.endsWith('builder-edit-remove-contract.json')) {
    const groupTraceNames = new Set([
      'builder-authoring:group-entry',
      'builder-authoring:group-entry-raw-basic',
    ]);
    const groupTrace = payload.traces?.find(trace => groupTraceNames.has(trace.name));
    if (groupTrace && !groupTrace.actions?.some(action => action.label === 'EditGroupAgain')) {
      console.error(`FAIL BuilderJourney repeat-edit owner control: ${groupTrace.name} lacks its second applied Group edit.`);
      repeatEditOwnerPassed = false;
      repeatEditRequiredPassed = false;
    } else if (groupTrace && !runPositiveBaseline()) {
      repeatEditOwnerPassed = false;
    } else if (groupTrace) {
      const groupTraceName = groupTrace.name;
      const mutatedTracePassed = runMutation('edit-group-again-noncurrent-owner', mutated => {
        const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
          trace.name === groupTraceName && action.label === 'EditGroupAgain'));
        if (hit) hit.action.owner = {
          ...hit.action.owner,
          attempt: hit.action.owner.attempt === 'P1' ? 'P2' : 'P1',
        };
      }, semanticFailureFor('EditGroupAgain'), { allowPartial: true });
      repeatEditOwnerPassed = mutatedTracePassed;
      const requiredActionControl = groupTraceName.endsWith('-raw-basic')
        ? 'group-entry-raw-basic-repeat-edit-required'
        : 'group-entry-repeat-edit-required';
      repeatEditRequiredPassed = runMutation(requiredActionControl, mutated => {
        const trace = mutated.traces?.find(candidate => candidate.name === groupTraceName);
        if (trace) trace.actions = trace.actions.filter(action => action.label !== 'EditGroupAgain');
      }, `trace "${groupTraceName}" missing required actions: EditGroupAgain`);
      const lifecycleContractEnabled = Object.keys(contract.phase1?.requiredActionCountsByTrace ?? {})
        .some(traceName => groupTraceNames.has(traceName));
      if (lifecycleContractEnabled) {
        const findSameAttemptLateAbortPair = trace => {
          for (let lateIndex = 0; lateIndex < trace.actions.length; lateIndex += 1) {
            const late = trace.actions[lateIndex];
            if (late.label !== 'LatePreviewErrorIgnored' || late.response !== null ||
                late.settlement?.kind !== 'request-failed' || late.settlement.errorText !== 'net::ERR_ABORTED') continue;
            const oldCaptureId = late.request?.captureId;
            const oldStartIndex = trace.actions.findIndex(action => action.label === 'Preview' &&
              action.request?.captureId === oldCaptureId);
            if (oldStartIndex < 0) continue;
            const oldStart = trace.actions[oldStartIndex];
            const replacement = trace.actions.slice(oldStartIndex + 1, lateIndex)
              .filter(action => action.label === 'Preview' &&
                typeof action.request?.captureId === 'string' && action.request.captureId !== oldCaptureId)
              .at(-1);
            if (replacement && oldStart.owner?.attempt === late.owner?.attempt &&
                replacement.owner?.attempt === oldStart.owner.attempt) {
              return { oldCaptureId, replacementCaptureId: replacement.request.captureId };
            }
          }
          return null;
        };
        const sameAttemptReplacement = findSameAttemptLateAbortPair(groupTrace);
        if (!sameAttemptReplacement) {
          console.log('SKIP late-aborted-preview-current-error-control: captured Group trace has no LatePreviewErrorIgnored paired with a distinct later Preview using the same P1/P2 attempt; this stale-current error behavior remains model-only and needs observation.');
        } else {
          lateAbortOwnerPassed = runMutation('late-aborted-preview-as-current-error-must-reject-old-capture', mutated => {
            const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
              trace.name === groupTraceName
              && action.label === 'LatePreviewErrorIgnored'
              && action.request?.captureId === sameAttemptReplacement.oldCaptureId
              && action.response === null
              && action.settlement?.kind === 'request-failed'
              && action.settlement.errorText === 'net::ERR_ABORTED'));
            if (!hit) throw new Error('captured late-abort request disappeared before the stale-capture control');
            const oldStartIndex = hit.trace.actions.findIndex(action => action.label === 'Preview' &&
              action.request?.captureId === sameAttemptReplacement.oldCaptureId);
            const replacement = hit.trace.actions.slice(oldStartIndex + 1, hit.actionIndex)
              .filter(action => action.label === 'Preview' &&
                action.request?.captureId === sameAttemptReplacement.replacementCaptureId)
              .at(-1);
            if (!replacement || replacement.owner?.attempt !== hit.action.owner?.attempt) {
              throw new Error('same-attempt replacement Preview is absent from the selected late-abort schedule');
            }
            hit.action.label = 'PreviewError';
            hit.action.observation = {
              ...hit.action.observation,
              status: 'error',
              proposalPreviewStatus: 'error',
              proposalIdPresent: false,
              proposalId: null,
              proposalOutputId: null,
              proposalSnapshotToken: null,
              proposalDraftVersion: null,
              proposalDraftDigest: null,
              proposalPreviewSurface: 'unknown',
              previewReceiptPresent: false,
              rowsPresent: false,
              receiptPresent: false,
              previewReceiptId: null,
              previewOutputId: null,
              embeddedProposalPreview: null,
              canApply: false,
              apply: { available: false, enabled: false, applying: false },
            };
          }, semanticFailureFor('PreviewError'), { allowPartial: true });
        }
      } else if (findTraceAction(payload.traces, (trace, action) =>
        trace.name === groupTraceName
        && action.label === 'LatePreviewErrorIgnored'
        && action.response === null
        && action.settlement?.kind === 'request-failed'
        && action.settlement.errorText === 'net::ERR_ABORTED')) {
        lateAbortOwnerPassed = runMutation('late-aborted-preview-must-be-noncurrent-owner', mutated => {
          const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
            trace.name === groupTraceName
            && action.label === 'LatePreviewErrorIgnored'
            && action.response === null
            && action.settlement?.kind === 'request-failed'
            && action.settlement.errorText === 'net::ERR_ABORTED'));
          if (!hit) return;
          const requestCaptureId = hit.action.request.captureId;
          const start = hit.trace.actions.find(action => action.label === 'Preview'
            && action.request?.captureId === requestCaptureId);
          const currentAttempt = hit.action.owner.attempt === 'P1' ? 'P2' : 'P1';
          hit.action.owner = { ...hit.action.owner, attempt: currentAttempt };
          if (start) start.owner = { ...start.owner, attempt: currentAttempt };
        }, semanticFailureFor('LatePreviewErrorIgnored'), { allowPartial: true });
      }

      if (lifecycleContractEnabled) {
        const runGroupLifecycleMutation = (label, actionLabel, predicate, mutate) => {
          const hit = findTraceAction(payload.traces, (trace, action) =>
            trace.name === groupTraceName && action.label === actionLabel && predicate(trace, action));
          if (!hit) {
            console.error(`FAIL ${label}: ${groupTraceName} has no captured ${actionLabel} observation for this control.`);
            return false;
          }
          return runMutation(label, mutated => {
            const retained = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
              trace.name === groupTraceName && action.label === actionLabel && predicate(trace, action)));
            if (retained) mutate(retained.trace, retained.action, retained.actionIndex);
          }, semanticFailureFor(actionLabel), { allowPartial: true });
        };
        const selectedDocumentFor = saved => {
          const documents = saved.workspace?.documents ?? [];
          const selected = documents.filter(document => document.output?.id === saved.outputId);
          if (selected.length !== 1) throw new Error('saved workspace evidence does not identify one selected Builder document');
          return selected[0];
        };
        const alternateDocumentKey = (saved, currentKeys) => {
          const column = (selectedDocumentFor(saved).columns ?? []).find(candidate =>
            !currentKeys.includes(candidate.columnId ?? candidate.id));
          return column?.columnId ?? column?.id ?? null;
        };
        const rewriteSavedGroupKeys = (saved, keys) => {
          saved.group.keys = [...keys];
          const steps = selectedDocumentFor(saved).construction?.steps ?? [];
          const groupSteps = steps.filter(step => step.operation?.kind === 'GROUP' && step.id === saved.group.stepId);
          if (groupSteps.length !== 1) throw new Error('saved Group projection does not identify one raw GROUP step');
          groupSteps[0].operation.group.keys = keys.map(inputColumnId => ({ inputColumnId }));
        };
        const rewriteSavedGroupStepId = (saved, stepId) => {
          const previousStepId = saved.group.stepId;
          const steps = selectedDocumentFor(saved).construction?.steps ?? [];
          const groupSteps = steps.filter(step => step.operation?.kind === 'GROUP' && step.id === previousStepId);
          if (groupSteps.length !== 1) throw new Error('saved Group projection does not identify one raw GROUP step');
          saved.group.stepId = stepId;
          groupSteps[0].id = stepId;
          if (Object.hasOwn(groupSteps[0].operation.group, 'constructionId')) {
            groupSteps[0].operation.group.constructionId = stepId;
          }
        };
        const rewriteCandidateKeys = (intent, keys) => {
          intent.proposed.keys = [...keys];
          const steps = intent.rawProposalBody?.candidateConstruction?.steps ?? [];
          const groupSteps = steps.filter(step => step.id === intent.proposed.stepId && step.operation?.kind === 'GROUP');
          if (groupSteps.length !== 1) throw new Error('candidate Group projection does not identify one raw GROUP step');
          groupSteps[0].operation.group.keys = keys.map(inputColumnId => ({ inputColumnId }));
        };

        const editCancelControl = runGroupLifecycleMutation(
          'group-edit-cancel-must-preserve-saved-group',
          'GroupEditCancel',
          (_trace, action) => action.groupLifecycle?.candidateIntent !== null,
          (trace, action) => {
            const before = action.groupLifecycle.savedBefore.group;
            const after = action.groupLifecycle.savedAfter.group;
            const anotherKey = alternateDocumentKey(action.groupLifecycle.savedAfter, before.keys);
            const changedKeys = before.keys.length === 0
              ? (anotherKey ? [anotherKey] : [])
              : [];
            if (before.keys.length === 0 && !anotherKey) throw new Error('trace has no captured alternate Group key for the Cancel control');
            rewriteSavedGroupKeys(action.groupLifecycle.savedAfter, changedKeys);
          },
        );
        const removalCancelControl = runGroupLifecycleMutation(
          'group-removal-cancel-must-preserve-draft-version',
          'GroupRemovalCancel',
          () => true,
          (_trace, action) => {
            const version = action.groupLifecycle.savedAfter.draftVersion;
            action.groupLifecycle.savedAfter.draftVersion = typeof version === 'number' ? version + 1 : version + '-changed';
          },
        );
        const reloadKeyControl = runGroupLifecycleMutation(
          'reload-must-restore-same-cardinality-group-keys',
          'Reload',
          (_trace, action) => action.groupLifecycle?.savedAfter?.group !== null &&
            action.groupLifecycle?.mountedGroup?.keys?.length === 1,
          (_trace, action) => {
            const lifecycle = action.groupLifecycle;
            const group = lifecycle.mountedGroup;
            const anotherKey = alternateDocumentKey(lifecycle.savedAfter, group.keys);
            if (!anotherKey) throw new Error('trace has no captured alternate Group key for the same-cardinality Reload control');
            group.keys = [anotherKey];
            const column = selectedDocumentFor(lifecycle.savedAfter).columns.find(candidate =>
              (candidate.columnId ?? candidate.id) === anotherKey);
            if (!column) throw new Error('alternate Reload key has no selected document column label');
            group.selectedKeyLabels = ['Group by ' + column.label];
          },
        );
        const reloadStepControl = runGroupLifecycleMutation(
          'reload-must-restore-saved-group-step',
          'Reload',
          (_trace, action) => action.groupLifecycle?.savedAfter?.group !== null,
          (_trace, action) => {
            const lifecycle = action.groupLifecycle;
            const changedStepId = lifecycle.savedAfter.group.stepId + '-self-test-wrong-step';
            rewriteSavedGroupStepId(lifecycle.savedAfter, changedStepId);
            lifecycle.mountedGroup.stepId = changedStepId;
            lifecycle.clickedRowTarget.stepId = changedStepId;
            lifecycle.clickedRowTarget.testId = 'construction-row-edit-' + changedStepId;
          },
        );
        const staleCandidateControl = runGroupLifecycleMutation(
          'group-apply-must-consume-current-candidate',
          'EditGroup',
          (_trace, action) => action.groupLifecycle?.candidateIntent?.proposed !== null,
          (_trace, action) => {
            const intent = action.groupLifecycle.candidateIntent;
            const keys = intent.proposed.keys.length === 0
              ? [alternateDocumentKey(action.groupLifecycle.savedAfter, intent.proposed.keys)]
              : [];
            if (keys.length === 1 && !keys[0]) throw new Error('trace has no captured alternate Group key for the stale-candidate control');
            rewriteCandidateKeys(intent, keys);
          },
        );
        const staleCandidateOwnerControl = runGroupLifecycleMutation(
          'group-apply-must-consume-current-preview-owner',
          'EditGroup',
          (_trace, action) => action.groupLifecycle?.candidateOwner !== null,
          (trace, action, actionIndex) => {
            const current = action.groupLifecycle.candidateOwner;
            const earlierPreview = trace.actions.slice(0, actionIndex).find(candidate => candidate.label === 'Preview' &&
              candidate.groupLifecycle?.owner?.captureId !== current.captureId);
            if (!earlierPreview) throw new Error('captured trace has no distinct prior Preview owner');
            action.groupLifecycle.candidateOwner = {
              mountEpoch: earlierPreview.groupLifecycle.owner.mountEpoch,
              captureId: earlierPreview.groupLifecycle.owner.captureId,
            };
          },
        );
        const reloadCountControl = runMutation(
          'group-trace-requires-exactly-two-reloads',
          mutated => {
            const trace = mutated.traces?.find(candidate => candidate.name === groupTraceName);
            const reloadIndex = trace?.actions?.findLastIndex(action => action.label === 'Reload') ?? -1;
            if (reloadIndex >= 0) trace.actions.splice(reloadIndex, 1);
          },
          `trace "${groupTraceName}" action Reload count expected 2, found 1`,
        );

        groupLifecycleControlsPassed = editCancelControl && removalCancelControl && reloadKeyControl &&
          reloadStepControl &&
          staleCandidateControl && staleCandidateOwnerControl && reloadCountControl;
      }
    }
  }

  let cdaControlsPassed = true;
  const cdaControlResults = [];
  const cdaJourneyCase = contract.journeyCase;
  if (cdaJourneyCase?.traceName === 'cda-builder-group-related-source') {
    if (!Array.isArray(cdaJourneyCase.requiredMutationControlNames) ||
        cdaJourneyCase.requiredMutationControlNames.length === 0 ||
        cdaJourneyCase.requiredMutationControlNames.some(name => typeof name !== 'string' || name.trim().length === 0) ||
        new Set(cdaJourneyCase.requiredMutationControlNames).size !== cdaJourneyCase.requiredMutationControlNames.length ||
        cdaJourneyCase.requiredMutationControlNames.some(name => name.startsWith('request-owner-mount-epoch-')) ||
        cdaJourneyCase.observedOwnerEpochControlPrefix !== 'request-owner-mount-epoch-') {
      console.error('FAIL CDA mutation-control contract: expected unique static names and the observed-owner epoch prefix');
      cdaControlsPassed = false;
    }
    const cdaTrace = payload.traces?.find(trace => trace.name === cdaJourneyCase.traceName);
    if (!tracesPath || !cdaTrace) {
      console.error('FAIL CDA structural fixture: --traces must contain the registered cda-builder-group-related-source trace');
      cdaControlsPassed = false;
    } else {
      const runCdaShape = (label, fixture, expectedDiagnostic = null) => {
        const path = join(workDirectory, 'cda-' + label + '.json');
        writeFileSync(path, JSON.stringify(fixture, null, 2) + '\n');
        const result = spawnSync(process.execPath, [
          'scripts/verify-builder-traces.mjs',
          '--traces', path,
          '--contract', contractPath,
          '--allow-partial',
          '--shape-only',
          '--quiet',
        ], { cwd: ROOT, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
        const output = (result.stdout || '') + (result.stderr || '');
        const passed = expectedDiagnostic === null
          ? result.status === 0
          : result.status !== 0 && output.includes(expectedDiagnostic) && output.includes('Builder trace validation failed');
        cdaControlResults.push({ name: label, status: passed ? 'pass' : 'fail' });
        if (!passed) {
          console.error((expectedDiagnostic === null ? 'FAIL ' : 'FAIL ') + 'CDA structural fixture ' + label +
            (expectedDiagnostic ? ': expected rejection containing ' + expectedDiagnostic : ': unmutated trace did not satisfy the static projection contract'));
          console.error(output.split('\n').slice(-16).join('\n'));
          cdaControlsPassed = false;
        } else {
          console.log('PASS CDA structural fixture ' + label + (expectedDiagnostic ? ': rejected intended mutation' : ': unmutated lifecycle projections accepted'));
        }
      };
      const baseline = { ...payload, traces: [cdaTrace] };
      runCdaShape('positive-observed-projection', baseline);
      const mutateCda = (label, expectedDiagnostic, mutate) => {
        const fixture = JSON.parse(JSON.stringify(baseline));
        const trace = fixture.traces[0];
        mutate(trace);
        runCdaShape(label, fixture, expectedDiagnostic);
      };
      const requiredAction = (trace, label, predicate = () => true) => {
        const action = trace.actions.find(candidate => candidate.label === label && predicate(candidate));
        if (!action) throw new Error('CDA trace lacks the required ' + label + ' lifecycle for its self-test mutation');
        return action;
      };
      const relationApply = trace => requiredAction(trace, 'RelatedSourceApply');
      const relationCancel = trace => requiredAction(trace, 'RelatedSourceCancel');
      const relatedPreview = trace => requiredAction(trace, 'RelatedSourcePreview');
      const relatedPreviewForConsumer = (trace, label) => {
        const consumer = requiredAction(trace, label);
        const ownerId = consumer.relatedSourceLifecycle?.owner?.browserRequestId;
        const preview = trace.actions.find(action => action.label === 'RelatedSourcePreview' &&
          action.relatedSourceLifecycle?.owner?.browserRequestId === ownerId);
        if (!preview) throw new Error('CDA trace lacks the selected RelatedSourcePreview for ' + label);
        return preview;
      };

      mutateCda('missing-related-snapshot', 'must preserve explorerId, group, relatedSource', trace => {
        delete relatedPreview(trace).relatedSourceLifecycle.savedAfter.relatedSource;
      });
      mutateCda('wrong-related-input-group-binding', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.inputStepId = 'wrong-group-step-self-test';
      });
      mutateCda('wrong-related-source-path', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.source.path = 'code';
      });
      mutateCda('wrong-related-route', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.route[0].toResourceType = 'Encounter';
      });
      mutateCda('wrong-related-output-identity', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.outputColumnId = 'wrong-output-self-test';
      });
      mutateCda('wrong-related-all-policy', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.form = 'FIRST';
      });
      mutateCda('wrong-related-choice-identity', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.choiceId = 'wrong-choice-self-test';
      });
      mutateCda('wrong-related-contributor-policy', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.contributorPolicy = 'ANY_MATCH';
      });
      mutateCda('wrong-related-row-value-policy', 'candidateIntent.proposed must match', trace => {
        relationApply(trace).relatedSourceLifecycle.candidateIntent.proposed.rowValuePolicy = 'FIRST';
      });
      mutateCda('wrong-group-aggregate', 'group aggregate diverges from the GROUP step', trace => {
        const action = requiredAction(trace, 'EditGroup');
        action.groupLifecycle.savedAfter.group.aggregate = 'COUNT_ROWS';
      });
      mutateCda('related-cancel-mutates-saved-cas', 'state-preserving event must retain exact saved workspace and draft CAS', trace => {
        const action = relationCancel(trace);
        action.relatedSourceLifecycle.savedAfter.draftDigest += '-mutated-self-test';
      });
      mutateCda('related-apply-without-current-candidate', 'Apply request must consume the proposalId from the current candidate', trace => {
        const action = relationApply(trace);
        const command = action.request.request.commands.find(candidate => candidate.type === 'APPLY_CONSTRUCTION_PROPOSAL');
        command.proposalId += '-stale-self-test';
      });
      mutateCda('related-cancel-without-owner-ready-panel', 'must consume a candidate with its own later READY panel observation', trace => {
        relatedPreviewForConsumer(trace, 'RelatedSourceCancel').relatedSourceLifecycle.renderedObservation = null;
      });
      mutateCda('related-apply-without-owner-ready-panel', 'must consume a candidate with its own later READY panel observation', trace => {
        relatedPreviewForConsumer(trace, 'RelatedSourceApply').relatedSourceLifecycle.renderedObservation = null;
      });
      mutateCda('related-owner-mismatch', 'owner identity must match the actual native request', trace => {
        relationApply(trace).relatedSourceLifecycle.owner.browserRequestId = 'different-owner-self-test';
      });
      mutateCda('stale-related-owner-mount-epoch', 'actual request identity, mount epoch, and document time origin', trace => {
        const lifecycle = relationApply(trace).relatedSourceLifecycle;
        lifecycle.owner.mountEpoch = (lifecycle.mountEpoch + 1) % (cdaJourneyCase.maxMountEpoch + 1);
      });
      mutateCda('reload-loses-related-binding', 'mountedRelatedSource', trace => {
        const action = requiredAction(trace, 'Reload', candidate => candidate.groupLifecycle.savedAfter.relatedSource !== null);
        action.groupLifecycle.mountedRelatedSource = null;
      });
      mutateCda('reload-invents-related-binding', 'mountedRelatedSource', trace => {
        const action = requiredAction(trace, 'Reload', candidate => candidate.groupLifecycle.savedAfter.relatedSource === null);
        const savedRelated = relationApply(trace).relatedSourceLifecycle.savedAfter.relatedSource;
        action.groupLifecycle.mountedRelatedSource = JSON.parse(JSON.stringify(savedRelated));
      });
      mutateCda('group-round-trip-drops-related-binding', 'relatedSource projection does not match', trace => {
        const action = requiredAction(trace, 'EditGroupAgain');
        action.groupLifecycle.savedAfter.relatedSource = null;
      });
      mutateCda('same-version-workspace-mutation', 'state-preserving event must retain exact saved workspace and draft CAS', trace => {
        const action = relationCancel(trace);
        action.relatedSourceLifecycle.savedAfter.workspace.explorer.title += ' changed at same version';
      });
      mutateCda('same-version-digest-mutation', 'state-preserving event must retain exact saved workspace and draft CAS', trace => {
        const action = relationCancel(trace);
        action.relatedSourceLifecycle.savedAfter.draftDigest += '-same-version-self-test';
      });
      mutateCda('duplicate-mutating-request-id', 'browserRequestId duplicates another observed native request', trace => {
        const applies = trace.actions.filter(action => action.label === 'RelatedSourceApply');
        applies[1].request.browserRequestId = applies[0].request.browserRequestId;
        applies[1].response.browserRequestId = applies[0].request.browserRequestId;
        applies[1].settlement.browserRequestId = applies[0].request.browserRequestId;
      });
      mutateCda('orphaned-proposal-request', 'Preview request has no modeled response settlement', trace => {
        const original = requiredAction(trace, 'Preview', action => action.groupLifecycle?.candidateIntent?.kind === 'edit');
        const copy = JSON.parse(JSON.stringify(original));
        const suffix = '-orphan-self-test';
        copy.request.browserRequestId += suffix;
        copy.groupLifecycle.owner.browserRequestId += suffix;
        if (typeof copy.request.requestId === 'string') {
          copy.request.requestId += suffix;
          copy.groupLifecycle.owner.requestId += suffix;
          if (typeof copy.owner?.requestId === 'string') copy.owner.requestId += suffix;
        }
        if (typeof copy.request.captureId === 'string') {
          copy.request.captureId += suffix;
          copy.request.request.captureId += suffix;
          copy.groupLifecycle.owner.captureId += suffix;
          copy.groupLifecycle.candidateIntent.rawProposalBody.captureId += suffix;
          if (typeof copy.owner?.captureId === 'string') copy.owner.captureId += suffix;
        }
        copy.observedEpoch = original.observedEpoch;
        const index = trace.actions.indexOf(original);
        trace.actions.splice(index + 1, 0, copy);
      });
      mutateCda('unallowlisted-post-support-event', 'method/path allowlist', trace => {
        trace.unmodeledEvents.push({
          kind: 'table-preview-read',
          note: 'self-test unknown POST',
          classification: 'unsupported',
          reason: 'self-test unknown method/path must be rejected',
          requests: [{ method: 'POST', path: '/authoring/v2/unknown', isMutation: false,
            browserRequestId: 'unknown-post-self-test', requestId: 'unknown-post-wire-self-test' }],
        });
      });
      mutateCda('read-only-support-mutation-flag', 'independently marked non-mutating', trace => {
        const event = trace.unmodeledEvents.find(candidate => Array.isArray(candidate.requests) && candidate.requests.length > 0);
        if (!event) throw new Error('CDA trace lacks grouped read-only support requests for the mutation-flag control');
        event.requests[0].isMutation = true;
      });
      mutateCda('duplicate-native-request-observer-index', 'native request observer index must be unique', trace => {
        const applies = trace.actions.filter(action => action.label === 'RelatedSourceApply');
        applies[1].request.index = applies[0].request.index;
        const rawRequest = trace.nativeRequests.find(entry => entry.browserRequestId === applies[1].request.browserRequestId);
        rawRequest.index = applies[0].request.index;
      });
      mutateCda('native-ledger-missing-modeled-request', 'modeled/support request is absent from the complete native observer ledger', trace => {
        const action = relationApply(trace);
        trace.nativeRequests = trace.nativeRequests.filter(entry => entry.browserRequestId !== action.request.browserRequestId);
      });
      mutateCda('missing-native-request-ledger', '.nativeRequests must preserve the complete native observer request ledger', trace => {
        delete trace.nativeRequests;
      });
      mutateCda('native-ledger-unknown-mutating-route', 'unknown mutating request', trace => {
        const request = JSON.parse(JSON.stringify(trace.nativeRequests.find(entry => entry.isMutation === true)));
        request.index = Math.max(...trace.nativeRequests.map(entry => entry.index)) + 1;
        request.startedAt = Math.max(...trace.nativeRequests.map(entry => entry.startedAt)) + 1;
        request.browserRequestId += '-unknown-ledger-self-test';
        if (typeof request.requestId === 'string') request.requestId += '-unknown-ledger-self-test';
        request.path = '/authoring/v2/unknown-mutating-route';
        trace.nativeRequests.push(request);
      });
      mutateCda('native-ledger-unbound-read-only-request', 'no modeled action or explicit read-only support binding', trace => {
        const request = JSON.parse(JSON.stringify(trace.nativeRequests.find(entry => entry.isMutation === false)));
        request.index = Math.max(...trace.nativeRequests.map(entry => entry.index)) + 1;
        request.startedAt = Math.max(...trace.nativeRequests.map(entry => entry.startedAt)) + 1;
        request.browserRequestId += '-unbound-ledger-self-test';
        if (typeof request.requestId === 'string') request.requestId += '-unbound-ledger-self-test';
        trace.nativeRequests.push(request);
      });
      mutateCda('native-ledger-duplicate-browser-request-id', 'browserRequestId must be unique across the complete observer ledger', trace => {
        trace.nativeRequests.push(JSON.parse(JSON.stringify(trace.nativeRequests[0])));
      });
      mutateCda('nonmonotonic-native-request-start-time', 'native request start times must follow the observer index chronology', trace => {
        const applies = trace.actions.filter(action => action.label === 'RelatedSourceApply');
        applies[1].request.startedAt = applies[0].request.startedAt - 1;
        applies[1].settlement.startedAt = applies[1].request.startedAt;
        const rawRequest = trace.nativeRequests.find(entry => entry.browserRequestId === applies[1].request.browserRequestId);
        rawRequest.startedAt = applies[1].request.startedAt;
      });
      const observedOwnerEpochs = new Set(cdaTrace.actions
        .filter(action => ['Preview', 'RelatedSourcePreview'].includes(action.label))
        .map(action => (action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner ??
          action.relatedSourceLifecycle?.owner)?.mountEpoch)
        .filter(Number.isInteger));
      for (const epoch of [...observedOwnerEpochs].sort((left, right) => left - right)) {
        mutateCda('request-owner-mount-epoch-' + epoch,
          'INVALID trace', trace => {
            const proposal = trace.actions.find(action =>
              ['Preview', 'RelatedSourcePreview'].includes(action.label) &&
              (action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner ??
                action.relatedSourceLifecycle?.owner)?.mountEpoch === epoch);
            if (!proposal) throw new Error('CDA trace lacks a proposal owner at observed mount epoch ' + epoch);
            const lifecycle = proposal.groupLifecycle ?? proposal.rowExpansionLifecycle ?? proposal.relatedSourceLifecycle;
            lifecycle.owner.mountEpoch = (epoch + 1) % (cdaJourneyCase.maxMountEpoch + 1);
          });
      }

      const requiredMutationControlNames = expectedCdaMutationControlNames(cdaJourneyCase, cdaTrace);
      const receiptProbe = requiredMutationControlNames.map(name => ({ name, status: 'pass' }));
      const reportPath = join(workDirectory, 'cda-trace-body-tamper.report.json');
      const tamperedPath = join(workDirectory, 'cda-trace-body-tamper.traces.json');
      const tamperedTraceBytes = Buffer.concat([rawTraceBytes, Buffer.from('\n \t')]);
      const tamperedSha256 = createHash('sha256').update(tamperedTraceBytes).digest('hex');
      writeFileSync(tamperedPath, tamperedTraceBytes);
      const tamperedReplay = spawnSync(process.execPath, [
        'scripts/verify-builder-traces.mjs',
        '--traces', tamperedPath,
        '--contract', contractPath,
        '--allow-partial',
        '--shape-only',
        '--output', reportPath,
        '--quiet',
      ], { cwd: ROOT, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
      let tamperedReplayReport = null;
      try {
        tamperedReplayReport = JSON.parse(readFileSync(reportPath, 'utf8'));
      } catch {}
      const tamperedReceipt = tamperedReplayReport?.builderJourneyTrace;
      const traceBodyTamperPassed = tamperedReplay.status === 0 &&
        tamperedSha256 !== traceSha256 && tamperedReplayReport?.traceSha256 === tamperedSha256 &&
        tamperedReceipt?.traceSha256 === tamperedSha256;
      cdaControlResults.push({ name: 'trace-body-tamper-rejected', status: traceBodyTamperPassed ? 'pass' : 'fail' });
      if (!traceBodyTamperPassed) {
        console.error('FAIL CDA receipt binding: verifier receipt did not track changed raw trace bytes');
        console.error(((tamperedReplay.stdout || '') + (tamperedReplay.stderr || '')).split('\n').slice(-16).join('\n'));
        cdaControlsPassed = false;
      }

      const missingNameProbe = receiptProbe.filter(control => control.name !== requiredMutationControlNames[0]);
      const missingNameRejected = !mutationControlNamesMatch(missingNameProbe, requiredMutationControlNames);
      cdaControlResults.push({ name: 'mutation-control-list-missing-name-rejected', status: missingNameRejected ? 'pass' : 'fail' });

      const substitutedNameProbe = receiptProbe.map((control, index) => index === 0
        ? { ...control, name: 'substituted-mutation-control-self-test' }
        : control);
      const substitutedNameRejected = !mutationControlNamesMatch(substitutedNameProbe, requiredMutationControlNames);
      cdaControlResults.push({ name: 'mutation-control-list-substitution-rejected', status: substitutedNameRejected ? 'pass' : 'fail' });

      const duplicateNameProbe = [...receiptProbe, receiptProbe[0]];
      const duplicateNameRejected = !mutationControlNamesMatch(duplicateNameProbe, requiredMutationControlNames);
      cdaControlResults.push({ name: 'mutation-control-list-duplicate-name-rejected', status: duplicateNameRejected ? 'pass' : 'fail' });

      if (!mutationControlNamesMatch(cdaControlResults, requiredMutationControlNames)) {
        console.error('FAIL CDA mutation controls: names must exactly and uniquely match the contract and every control must pass');
        cdaControlsPassed = false;
      }

      if (selfTestOutputPath) {
        const allSelfTestsPassed = crossEvidencePassed && choiceProofPassed && requestCancellationPassed &&
          repeatEditOwnerPassed && repeatEditRequiredPassed && lateAbortOwnerPassed &&
          groupLifecycleControlsPassed && cdaControlsPassed &&
          mutationControlNamesMatch(cdaControlResults, requiredMutationControlNames);
        const selfTestReportPath = resolve(ROOT, selfTestOutputPath);
        const receipt = {
          traceName: cdaJourneyCase.traceName,
          nativeCaseKey: cdaJourneyCase.workflow,
          tracePath: resolve(ROOT, tracesPath),
          traceSha256,
          selfTestReportPath,
          status: allSelfTestsPassed ? 'pass' : 'fail',
          requiredMutationControlNames,
          mutationControls: cdaControlResults,
        };
        const receiptProblem = selfTestReceiptProblem(receipt, {
          traceName: cdaJourneyCase.traceName,
          nativeCaseKey: cdaJourneyCase.workflow,
          tracePath: resolve(ROOT, tracesPath),
          selfTestReportPath,
        });
        if (receiptProblem) {
          console.error('FAIL self-test receipt: ' + receiptProblem);
          cdaControlsPassed = false;
          receipt.status = 'fail';
        }
        mkdirSync(dirname(selfTestReportPath), { recursive: true });
        writeFileSync(selfTestReportPath, JSON.stringify(receipt, null, 2) + '\n');
      }
    }
  }
  rmSync(workDirectory, { recursive: true, force: true });
  if (!crossEvidencePassed || !choiceProofPassed || !requestCancellationPassed
    || !repeatEditOwnerPassed || !repeatEditRequiredPassed || !lateAbortOwnerPassed || !groupLifecycleControlsPassed
    || !cdaControlsPassed) process.exit(1);
  console.log('PASS BuilderJourney cross-evidence, choice-proof, and request-cancellation self-test');
  process.exit(0);
}

if (!runPositiveBaseline()) {
  rmSync(workDirectory, { recursive: true, force: true });
  process.exit(1);
}

let passed = true;
try {
  if (!journeyOnly) {
    passed = runMutation('initial-observation-mismatch', (mutated) => {
      const trace = mutated.traces.find((candidate) => candidate.model === 'BuilderPreview');
      if (trace?.initialObservation) {
        trace.initialObservation.status = trace.initialObservation.status === 'ready' ? 'idle' : 'ready';
        mutated.traces = [trace];
      }
    }, 'trace validation failed', { allowPartial: true, expectNoInitialState: true }) && passed;

    const retainMountedReconcile = (mutated) => {
      const trace = mutated.traces.find((candidate) => candidate.model === 'ReconcileLifecycle');
      if (trace) mutated.traces = [trace];
      return trace;
    };
    const lateReconcile = 'LateReconcileIgnored(A)';

    passed = runMutation('reconcile-late-receipt-takeover', (mutated) => {
      const trace = retainMountedReconcile(mutated);
      const late = trace?.actions.find((action) => action.label === lateReconcile);
      if (late?.observation?.reconcile?.receipt) {
        late.observation.reconcile.receipt = {
          modelKey: 'R1',
          receiptId: 'receipt-A',
          requestKey: 'A',
          workspaceKey: 'W1',
          outputModelKey: 'O1',
          outputId: 'specimens',
        };
      }
    }, semanticFailureFor(lateReconcile), { allowPartial: true }) && passed;

    passed = runMutation('reconcile-late-output-takeover', (mutated) => {
      const trace = retainMountedReconcile(mutated);
      const late = trace?.actions.find((action) => action.label === lateReconcile);
      if (late?.observation?.reconcile?.current) late.observation.reconcile.current.outputId = 'patients';
    }, semanticFailureFor(lateReconcile), { allowPartial: true }) && passed;

    passed = runMutation('reconcile-late-request-owner-mismatch', (mutated) => {
      const trace = retainMountedReconcile(mutated);
      const late = trace?.actions.find((action) => action.label === lateReconcile);
      const requestB = trace?.actions.find((action) => action.label === 'WorkspaceChangeStartsReconcile(B)')?.observation?.reconcile?.request;
      if (late?.observation?.reconcile?.request && requestB) {
        late.observation.reconcile.request.rawRequestId = requestB.rawRequestId;
      }
    }, 'request identity differs from the original A request', { allowPartial: true }) && passed;

    passed = runMutation('reconcile-late-response-owner-mismatch', (mutated) => {
      const trace = retainMountedReconcile(mutated);
      const late = trace?.actions.find((action) => action.label === lateReconcile);
      if (late?.observation?.reconcile?.returned) late.observation.reconcile.returned.requestKey = 'B';
    }, 'returned raw identity does not match its request/receipt/output model keys', { allowPartial: true }) && passed;

    passed = runMutation('reconcile-settlement-order', (mutated) => {
      const trace = retainMountedReconcile(mutated);
      if (trace) [trace.actions[1], trace.actions[2]] = [trace.actions[2], trace.actions[1]];
    }, 'must be ReconcileSuccess(B)', { allowPartial: true }) && passed;
  }

  passed = runMutation('observation-mismatch', (mutated) => {
    for (const trace of mutated.traces) {
      for (const action of trace.actions ?? []) {
        if (action.observation?.status === 'ready') {
          action.observation.status = 'needs-repair';
          return;
        }
      }
    }
  }, 'trace validation failed') && passed;

  passed = runMutation('wrong-model', (mutated) => {
    const trace = mutated.traces.find((candidate) => candidate.model === 'BuilderPreview');
    if (trace) trace.model = 'ConstructionLifecycle';
  }, 'is not an action of ConstructionLifecycle') && passed;

  passed = runMutation('missing-required-event', (mutated) => {
    for (const trace of mutated.traces) {
      trace.actions = (trace.actions ?? []).filter((action) => action.event !== requiredEvent);
    }
  }, 'required events missing: ' + requiredEvent) && passed;

  passed = runMutation('discovery-alias-wrong-event', (mutated) => {
    const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
      trace.model === 'BuilderPreview' &&
      action.label === 'DiscoveryComplete' &&
      action.event === 'construction-discovery'));
    if (hit) {
      hit.action.label = 'DiscoveryComplete(complete)';
      hit.action.event = 'proposal-response';
    }
  }, 'does not match contract event "construction-discovery" for DiscoveryComplete(complete)', { allowPartial: true }) && passed;

  const lateProposalLabel = 'LateProposalCompleteIgnored(P1)';
  const currentOwnerLateLabel = 'LateProposalCompleteIgnored(P2)';
  passed = runMutation('current-owner-falsely-reported-as-late', (mutated) => {
    const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action, actionIndex) =>
      trace.model === 'ConstructionLifecycle' &&
      action.label === lateProposalLabel &&
      action.owner?.kind === 'proposal' &&
      action.owner.attempt === 'P1' &&
      trace.actions.slice(0, actionIndex).some((prior) => prior.label === 'ReplaceProposalOwner')));
    if (hit) {
      hit.action.label = currentOwnerLateLabel;
      hit.action.owner = { ...hit.action.owner, attempt: 'P2' };
    }
  }, semanticFailureFor(currentOwnerLateLabel), { allowPartial: true }) && passed;

  passed = runMutation('late-proposal-mutates-current-owner', (mutated) => {
    const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action, actionIndex) =>
      trace.model === 'ConstructionLifecycle' &&
      action.label === lateProposalLabel &&
      action.owner?.kind === 'proposal' &&
      action.owner.attempt === 'P1' &&
      action.observation?.status !== 'applying' &&
      !hasAcceptedApplyBefore(trace, actionIndex)));
    if (hit) hit.action.observation.status = 'applying';
  }, semanticFailureFor(lateProposalLabel), { allowPartial: true }) && passed;

  passed = runMutation('late-proposal-steals-current-proposal-id', (mutated) => {
    const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
      trace.model === 'ConstructionLifecycle' &&
      action.label === lateProposalLabel &&
      action.owner?.kind === 'proposal' &&
      action.owner.attempt === 'P1' &&
      typeof action.observation?.proposalId === 'string' &&
      action.observation.proposalId !== 'proposal-A'));
    if (hit) hit.action.observation.proposalId = 'proposal-A';
  }, semanticFailureFor(lateProposalLabel), { allowPartial: true }) && passed;

  const rejectedApplyLabel = 'BeginApply(rejected)';
  passed = runMutation('rejected-begin-apply-mutates-current-owner', (mutated) => {
    const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action, actionIndex) =>
      trace.model === 'ConstructionLifecycle' &&
      action.label === rejectedApplyLabel &&
      action.owner?.kind === 'proposal' &&
      action.owner.attempt === 'P2' &&
      action.expectedProposal === 'P1' &&
      action.observation?.status !== 'applying' &&
      !hasAcceptedApplyBefore(trace, actionIndex)));
    if (hit) hit.action.observation.status = 'applying';
  }, semanticFailureFor(rejectedApplyLabel), { allowPartial: true }) && passed;

  const startProposalLabel = 'StartProposal(P1)';
  const proposalCompleteLabel = 'ProposalComplete(P1)';
  const startPreviewLabel = 'StartPreview(P1)';
  passed = runMutation('preview-start-before-proposal-completion', (mutated) => {
    const hit = mutated.traces.find((trace) => {
      if (trace.model !== 'ConstructionLifecycle') return false;
      const startIndex = trace.actions.findIndex((action) =>
        action.label === startProposalLabel &&
        action.owner?.kind === 'proposal' &&
        action.owner.attempt === 'P1');
      const completeIndex = trace.actions.findIndex((action) =>
        action.label === proposalCompleteLabel &&
        action.owner?.kind === 'proposal' &&
        action.owner.attempt === 'P1');
      const previewIndex = trace.actions.findIndex((action) =>
        action.label === startPreviewLabel &&
        action.owner?.kind === 'preview' &&
        action.owner.attempt === 'P1');
      return startIndex === 0 && completeIndex > startIndex && previewIndex > completeIndex;
    });
    if (hit) {
      mutated.traces = [hit];
      const completeIndex = hit.actions.findIndex((action) => action.label === proposalCompleteLabel);
      const previewIndex = hit.actions.findIndex((action) => action.label === startPreviewLabel);
      [hit.actions[completeIndex], hit.actions[previewIndex]] = [hit.actions[previewIndex], hit.actions[completeIndex]];
    }
  }, semanticFailureFor(startPreviewLabel), { allowPartial: true }) && passed;

  passed = runMutation('ready-with-mismatched-embedded-preview-receipt', (mutated) => {
    const hit = retainOnlyTrace(mutated, findTraceAction(mutated.traces, (trace, action) =>
      trace.model === 'ConstructionLifecycle' &&
      action.label === proposalCompleteLabel &&
      action.owner?.kind === 'proposal' &&
      action.owner.attempt === 'P1' &&
      action.observation?.status === 'ready' &&
      action.observation.canApply === true &&
      typeof action.observation.proposalId === 'string' &&
      typeof action.observation.previewReceiptId === 'string' &&
      action.observation.previewReceiptId === action.observation.proposalId &&
      action.observation.previewReceiptPresent === true &&
      action.observation.rowsPresent === true &&
      action.observation.receiptPresent === true));
    if (hit) hit.action.observation.previewReceiptId = 'forged-preview-receipt';
  }, semanticFailureFor(proposalCompleteLabel), { allowPartial: true }) && passed;

  if (!formalOnly) passed = runJourneyCrossEvidenceMutation() && passed;

  passed = runSourceProfileMutation() && passed;
} finally {
  rmSync(workDirectory, { recursive: true, force: true });
}

if (!passed) process.exit(1);
console.log('PASS builder trace validator rejected every mutation');
