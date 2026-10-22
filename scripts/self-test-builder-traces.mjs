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
const contractPath = valueFor('--contract') ?? (journeyOnly
  ? 'verification/builder-interactions/builder-journey-contract.json'
  : 'verification/builder-interactions/builder-trace-contract.json');
if (!tracesPath && !journeyOnly) {
  console.error('usage: node scripts/self-test-builder-traces.mjs --traces <traces.json> [--contract <contract.json>] [--formal-only|--journey-only]');
  process.exit(1);
}

let payload = tracesPath ? JSON.parse(readFileSync(resolve(ROOT, tracesPath), 'utf8')) : null;
const contract = JSON.parse(readFileSync(resolve(ROOT, contractPath), 'utf8'));
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
const runMutation = (label, mutate, expectedText, { allowPartial = false, semanticActionLabel = null } = {}) => {
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
  if (semanticActionLabel) verifierArgs.push('--output', reportPath);
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
  console.log('PASS journey positive baseline: validator accepted the self-test-only capture-shaped preview trace');

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
  if (!absentProposalId || !mismatchedProposalId) return false;
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
  payload = {
    schemaVersion: 1,
    sourceProfile: contract.sourceProfile,
    sourceCommit: (head.stdout || '').trim(),
    contractSourceCommit: contract.sourceCommit,
    sourceFingerprint: fingerprintFor(profile.sources, ROOT),
    traces: [],
  };
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
  const passed = runJourneyCrossEvidenceMutation();
  rmSync(workDirectory, { recursive: true, force: true });
  if (!passed) process.exit(1);
  console.log('PASS BuilderJourney cross-evidence self-test');
  process.exit(0);
}

if (!runPositiveBaseline()) {
  rmSync(workDirectory, { recursive: true, force: true });
  process.exit(1);
}

let passed = true;
try {
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
