#!/usr/bin/env node
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';

const EXPECTED_KIND = 'value-expectation';
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');

function absolutePath(path, basePath) {
  return isAbsolute(path) ? resolve(path) : resolve(basePath, path);
}

function readJsonInput(path, label, basePath = process.cwd()) {
  const pathAbsolute = absolutePath(path, basePath);
  const bytes = readFileSync(pathAbsolute);
  let value;
  try {
    value = JSON.parse(bytes.toString('utf8'));
  } catch (error) {
    throw new Error(`${label} is not valid JSON: ${error.message}`);
  }
  assert(value && typeof value === 'object' && !Array.isArray(value), `${label} must contain a JSON object`);
  return { path: pathAbsolute, bytes, sha256: sha256(bytes), value };
}

function captureArtifact(path, label, { basePath, declaredSha256, required = false } = {}) {
  if (!path) {
    assert(!required, `${label} path is missing`);
    return null;
  }
  const pathAbsolute = absolutePath(path, basePath);
  if (!existsSync(pathAbsolute)) {
    assert(!required, `${label} does not exist: ${pathAbsolute}`);
    return { label, path: pathAbsolute, status: 'missing-at-extraction' };
  }
  const bytes = readFileSync(pathAbsolute);
  const actualSha256 = sha256(bytes);
  if (declaredSha256 !== undefined && declaredSha256 !== null) {
    assert.match(declaredSha256, /^[a-f0-9]{64}$/, `${label} declared SHA-256 is malformed`);
    assert.equal(actualSha256, declaredSha256, `${label} hash does not match its retained summary`);
  }
  return {
    label,
    path: pathAbsolute,
    sha256: actualSha256,
    hashBasis: declaredSha256 ? 'verified-against-summary' : 'captured-at-extraction',
    bytes: bytes.length,
  };
}

function reportReferences(summary) {
  const evidence = summary.evidence ?? {};
  const reviewPacket = summary.reviewPacket ?? {};
  return {
    domainReportPath: summary.rawReportPath ?? evidence.domainReport ?? reviewPacket.report,
    domainReportSha256: summary.rawReportSha256 ?? summary.domainReportSha256 ?? evidence.domainReportSha256,
    playwrightReportPath: summary.playwrightResultsPath ?? summary.playwrightReportPath
      ?? evidence.playwrightReport ?? reviewPacket.playwrightJson,
    playwrightReportSha256: summary.playwrightResultsSha256 ?? summary.playwrightReportSha256
      ?? evidence.playwrightReportSha256,
    runnerLogPath: summary.runnerLogPath ?? summary.runnerLog,
    runnerLogSha256: summary.runnerLogSha256,
  };
}

function sourceFingerprintFor(summary, report) {
  const source = summary.sourceFingerprint
    ?? summary.integrity?.dimensions?.source?.before
    ?? report.target?.sourceFingerprint;
  assert.match(source?.sha256 ?? '', /^[a-f0-9]{64}$/, 'Source fingerprint is missing from summary and domain report');
  assert(Number.isInteger(source.files) && source.files > 0, 'Source file count is missing');
  const reportSource = report.target?.sourceFingerprint;
  if (reportSource?.sha256) assert.equal(reportSource.sha256, source.sha256, 'Domain report source fingerprint differs from summary');
  if (reportSource?.files != null) assert.equal(reportSource.files, source.files, 'Domain report source file count differs from summary');

  const before = summary.integrity?.dimensions?.source?.before ?? report.verificationIdentity?.sourceFingerprint?.before;
  const after = summary.integrity?.dimensions?.source?.after ?? report.verificationIdentity?.sourceFingerprint?.after;
  if (before?.sha256) assert.equal(before.sha256, source.sha256, 'Source-before fingerprint differs from the retained report');
  if (after?.sha256) assert.equal(after.sha256, source.sha256, 'Source-after fingerprint differs from the retained report');
  return { sha256: source.sha256, files: source.files };
}

function selectFailure(summary, report, checkName) {
  const assertions = Array.isArray(report.assertions) ? report.assertions : [];
  const failedAssertions = assertions.filter(assertion => assertion.status === 'failed');
  if (checkName) {
    const matches = failedAssertions.filter(assertion => assertion.name === checkName);
    assert.equal(matches.length, 1, '--check must name exactly one retained failed assertion');
    return { name: checkName, assertion: matches[0], selectionMethod: 'explicit-failed-assertion' };
  }
  if (failedAssertions.length === 1) {
    return { name: failedAssertions[0].name, assertion: failedAssertions[0], selectionMethod: 'unique-failed-assertion' };
  }
  if (failedAssertions.length > 1) {
    throw new Error(`Domain report has ${failedAssertions.length} failed assertions; provide --check with one exact name`);
  }

  const firstFailureReason = summary.reviewPacket?.firstFailureReason
    ?? report.failureEvidence?.reason
    ?? summary.workflowError;
  assert(typeof firstFailureReason === 'string' && firstFailureReason.trim(),
    'No failed assertion or retained first-failure context is available');
  return { name: null, assertion: null, selectionMethod: 'retained-first-failure-context', firstFailureReason };
}

function loadRetainedFailureContext(summaryPath) {
  const summaryInput = readJsonInput(summaryPath, 'Summary');
  const summary = summaryInput.value;
  assert.equal(summary.status, 'failed', 'Summary must identify a failed retained case');
  const references = reportReferences(summary);
  const summaryDirectory = dirname(summaryInput.path);
  const domainArtifact = captureArtifact(references.domainReportPath, 'domainReport', {
    basePath: summaryDirectory,
    declaredSha256: references.domainReportSha256,
    required: true,
  });
  const playwrightArtifact = captureArtifact(references.playwrightReportPath, 'playwrightReport', {
    basePath: summaryDirectory,
    declaredSha256: references.playwrightReportSha256,
    required: true,
  });
  const runnerLogArtifact = references.runnerLogPath
    ? captureArtifact(references.runnerLogPath, 'runnerLog', {
      basePath: summaryDirectory,
      declaredSha256: references.runnerLogSha256,
    })
    : null;
  const report = JSON.parse(readFileSync(domainArtifact.path, 'utf8'));
  assert.equal(report.status, 'failed', 'Domain report must identify a failed retained case');
  assert.equal(report.scenario, summary.scenario, 'Domain report scenario does not match summary');
  assert.equal(report.case ?? report.caseName, summary.case, 'Domain report case does not match summary');
  const sourceFingerprint = sourceFingerprintFor(summary, report);
  const target = {
    project: report.target?.project ?? summary.targetIdentity?.project ?? null,
    generation: report.target?.generation ?? summary.targetIdentity?.generation ?? null,
    explorer: report.target?.explorer ?? null,
  };
  assert(target.project && target.generation, 'Domain report project and generation are required');
  return {
    summaryInput,
    summary,
    summaryDirectory,
    domainArtifact,
    playwrightArtifact,
    runnerLogArtifact,
    report,
    sourceFingerprint,
    target,
  };
}

function supplementalArtifacts(summary, basePath, excludedPaths) {
  const artifacts = [];
  const seen = new Set(excludedPaths.map(path => resolve(path)));
  const add = (label, path) => {
    if (typeof path !== 'string' || !path) return;
    const pathAbsolute = absolutePath(path, basePath);
    if (seen.has(pathAbsolute)) return;
    seen.add(pathAbsolute);
    artifacts.push(captureArtifact(path, label, { basePath }));
  };
  for (const [name, path] of Object.entries(summary.evidence ?? {})) {
    if (name !== 'summary') add(`evidence.${name}`, path);
  }
  for (const [name, command] of Object.entries(summary.commands ?? {})) {
    add(`commands.${name}.stdout`, command.stdoutPath);
    add(`commands.${name}.stderr`, command.stderrPath);
  }
  return artifacts.filter(Boolean);
}

function verifyExpectedInput(expectedInput, sourceFingerprint, target) {
  const expected = expectedInput.value;
  assert.equal(expected.schemaVersion, 1, 'Expected input schemaVersion must be 1');
  assert.equal(expected.kind, EXPECTED_KIND, `Expected input kind must be ${EXPECTED_KIND}`);
  assert.deepEqual(expected.sourceFingerprint, sourceFingerprint, 'Expected input source fingerprint must match the retained run');
  assert.equal(expected.target?.project, target.project, 'Expected input project must match the retained run');
  assert.equal(expected.target?.generation, target.generation, 'Expected input generation must match the retained run');
  assert(Object.hasOwn(expected, 'value'), 'Expected input must provide a value');
  return expected;
}

function authoringV2Path(project, explorer, endpoint) {
  return `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/${endpoint}`;
}

function selectedProposalCheckpoint(target, selectedRequest) {
  assert(target.explorer, 'Domain report explorer is required to select prerequisite state');
  assert.equal(selectedRequest.method, 'POST', 'Selected proposal must be POST');
  assert.equal(selectedRequest.path, authoringV2Path(target.project, target.explorer, 'construction-proposals'),
    'Selected proposal must use the exact retained explorer authoring-v2 path');
  assert(Number.isFinite(selectedRequest.startedAt), 'Selected proposal start time is missing');
  const body = selectedRequest.body;
  assert(body && typeof body === 'object' && !Array.isArray(body), 'Selected proposal request body is missing');
  assert(typeof body.snapshotToken === 'string' && body.snapshotToken.trim(),
    'Selected proposal snapshotToken must be a nonempty string');
  assert(Number.isInteger(body.expectedDraftVersion) && body.expectedDraftVersion >= 0,
    'Selected proposal expectedDraftVersion must be a nonnegative integer');
  assert(typeof body.expectedDraftDigest === 'string' && body.expectedDraftDigest.trim(),
    'Selected proposal expectedDraftDigest must be a nonempty string');
  assert(typeof body.outputId === 'string' && body.outputId.trim(), 'Selected proposal outputId must be a nonempty string');
  return {
    snapshotToken: body.snapshotToken,
    draftVersion: body.expectedDraftVersion,
    draftDigest: body.expectedDraftDigest,
    outputId: body.outputId,
  };
}

function selectPrerequisiteState(report, target, selectedRequest, selectedIndex, browserRequestId) {
  if (browserRequestId === undefined) {
    return {
      status: 'missing-at-extraction',
      selectionMethod: 'not-selected',
      causalLinkageToFailure: 'not-established-by-extractor',
      reason: 'No exact prerequisite state request ID was supplied; nearby state was not inferred.',
    };
  }
  assert(typeof browserRequestId === 'string' && browserRequestId.trim(), 'stateBrowserRequestId must be a nonempty exact browser request ID');

  const requests = report.nativeRequests ?? [];
  const matches = requests.filter(entry => entry.browserRequestId === browserRequestId);
  assert.equal(matches.length, 1, `Expected exactly one prerequisite native request for ${browserRequestId}`);
  const state = matches[0];
  const stateIndex = requests.indexOf(state);
  const owner = { project: target.project, explorer: target.explorer };
  const reconcilePath = authoringV2Path(target.project, target.explorer, 'reconcile');
  const selectedCheckpoint = selectedProposalCheckpoint(target, selectedRequest);
  assert.equal(state.method, 'POST', 'Prerequisite state must be POST');
  assert.equal(state.path, reconcilePath, 'Prerequisite state must use the exact same-owner reconcile path');
  assert.equal(state.status, 200, 'Prerequisite reconcile must have a successful HTTP 200 response');
  assert(Number.isFinite(state.startedAt), 'Prerequisite reconcile start time is missing');
  assert(Number.isFinite(state.responseReceivedAt), 'Prerequisite reconcile response event is missing');
  assert(Number.isFinite(state.completedAt), 'Prerequisite reconcile completion event is missing');
  assert(state.responseReceivedAt >= state.startedAt && state.completedAt >= state.responseReceivedAt,
    'Prerequisite reconcile terminal event times are out of order');
  assert(!state.failure, 'Prerequisite reconcile has a retained request failure');
  assert(state.response && typeof state.response === 'object' && !Array.isArray(state.response),
    'Prerequisite reconcile response body is missing');
  assert(typeof state.requestId === 'string' && state.requestId.trim(), 'Prerequisite reconcile native requestId is missing');
  assert(Number.isFinite(selectedRequest.startedAt), 'Selected proposal start time is missing');
  assert(stateIndex < selectedIndex && state.completedAt < selectedRequest.startedAt,
    'Prerequisite reconcile must precede the selected proposal in report order and time');

  const stateBody = state.body;
  const response = state.response;
  assert(stateBody && typeof stateBody === 'object' && !Array.isArray(stateBody),
    'Prerequisite reconcile request body is missing');
  for (const [label, value] of [
    ['snapshotToken', stateBody.snapshotToken],
    ['draftVersion', stateBody.draftVersion],
    ['draftDigest', stateBody.draftDigest],
  ]) assert(value !== undefined && value !== null, `Prerequisite reconcile ${label} is missing`);
  assert.equal(stateBody.snapshotToken, selectedCheckpoint.snapshotToken, 'Prerequisite reconcile snapshotToken differs from selected proposal');
  assert.equal(stateBody.draftVersion, selectedCheckpoint.draftVersion, 'Prerequisite reconcile draftVersion differs from selected proposal');
  assert.equal(stateBody.draftDigest, selectedCheckpoint.draftDigest, 'Prerequisite reconcile draftDigest differs from selected proposal');
  assert.equal(response.snapshotToken, stateBody.snapshotToken, 'Prerequisite reconcile response snapshotToken differs from its request');

  const outputId = selectedCheckpoint.outputId;
  const matchingOutputs = (Array.isArray(response.outputs) ? response.outputs : [])
    .filter(output => output?.outputId === outputId);
  assert.equal(matchingOutputs.length, 1, 'Prerequisite reconcile must retain exactly one matching output');
  const builder = response.builder;
  assert(builder && typeof builder === 'object' && Array.isArray(builder.documents),
    'Prerequisite reconcile builder documents are missing');
  const matchingDocuments = builder.documents.filter(document => document?.output?.id === outputId);
  assert.equal(matchingDocuments.length, 1, 'Prerequisite reconcile must retain exactly one matching builder document');
  const document = matchingDocuments[0];
  assert(Array.isArray(document.columns) && document.columns.length > 0,
    'Prerequisite reconcile matching document columns are missing');

  return {
    status: 'retained',
    selectionMethod: 'operator-selected-exact-browser-request-id',
    causalLinkageToFailure: 'not-established-by-extractor',
    browserRequestId,
    requestId: state.requestId,
    method: state.method,
    path: state.path,
    statusCode: state.status,
    startedAt: state.startedAt,
    responseReceivedAt: state.responseReceivedAt,
    completedAt: state.completedAt,
    owner,
    checkpoint: {
      snapshotToken: stateBody.snapshotToken,
      draftVersion: stateBody.draftVersion,
      draftDigest: stateBody.draftDigest,
      outputId,
    },
    output: sanitizePayload(matchingOutputs[0]),
    document: sanitizePayload({
      output: document.output,
      rootResourceType: document.rootResourceType,
      columns: document.columns,
    }),
  };
}

/**
 * Extract a request chosen by exact browser/native IDs. The report may not establish
 * causal linkage between that request and a check; the output records that limitation.
 */
export function extractNativeFailureInput({ summaryPath, browserRequestId, requestId, stateBrowserRequestId, checkName, expectedPath }) {
  assert(typeof browserRequestId === 'string' && browserRequestId.trim(), 'browserRequestId is required');
  assert(typeof requestId === 'string' && requestId.trim(), 'requestId is required');
  const {
    summaryInput,
    summary,
    summaryDirectory,
    domainArtifact,
    playwrightArtifact,
    runnerLogArtifact,
    report,
    sourceFingerprint,
    target,
  } = loadRetainedFailureContext(summaryPath);
  const failure = selectFailure(summary, report, checkName);
  const nativeRequests = report.nativeRequests ?? [];
  const browserMatches = nativeRequests.filter(entry => entry.browserRequestId === browserRequestId);
  assert.equal(browserMatches.length, 1, `Expected exactly one native request for ${browserRequestId}`);
  const request = browserMatches[0];
  const prerequisiteState = selectPrerequisiteState(report, target, request, nativeRequests.indexOf(request), stateBrowserRequestId);
  assert.equal(request.requestId, requestId, 'Selected browser request has a different native requestId');

  const expectedInput = expectedPath ? readJsonInput(expectedPath, 'Expected input') : null;
  const expectedValue = expectedInput ? verifyExpectedInput(expectedInput, sourceFingerprint, target) : null;
  const artifacts = [
    { label: 'summary', path: summaryInput.path, sha256: summaryInput.sha256, hashBasis: 'captured-at-extraction', bytes: summaryInput.bytes.length },
    domainArtifact,
    playwrightArtifact,
    ...(runnerLogArtifact ? [runnerLogArtifact] : []),
    ...supplementalArtifacts(summary, summaryDirectory, [summaryInput.path, domainArtifact.path, playwrightArtifact.path,
      runnerLogArtifact?.path].filter(Boolean)),
  ];

  return {
    schemaVersion: 1,
    kind: 'retained-native-failure-input',
    failureSelection: {
      assertionName: failure.name,
      selectionMethod: failure.selectionMethod,
      firstFailureReason: sanitizeText(report.failureEvidence?.reason
        ?? failure.firstFailureReason
        ?? summary.reviewPacket?.firstFailureReason
        ?? summary.workflowError
        ?? '').slice(0, 2_000),
      assertion: sanitizePayload(failure.assertion),
    },
    requestSelection: {
      browserRequestId,
      requestId,
      method: 'operator-selected-exact-identifiers',
      causalLinkageToFailure: 'not-established-by-extractor',
    },
    requestEvidence: {
      responseState: request.response && typeof request.response === 'object' ? 'retained' : 'not-retained',
      nativeRequest: sanitizePayload(request),
      prerequisiteState,
    },
    expectation: expectedInput ? {
      status: 'separately-supplied-provenance-unverified',
      path: expectedInput.path,
      sha256: expectedInput.sha256,
      declaration: sanitizePayload(expectedValue.provenance ?? null),
      independenceVerified: false,
      value: sanitizePayload(expectedValue.value),
    } : {
      status: 'missing-independent-oracle',
      independenceVerified: false,
      reason: 'No separately supplied expected-value file was provided; the captured response is observed output, not an oracle.',
    },
    provenance: {
      scenario: report.scenario,
      case: report.case ?? report.caseName,
      target,
      sourceFingerprint,
      sourceFreeze: sanitizePayload(report.verificationIdentity?.sourceFreeze
        ?? summary.integrity?.dimensions?.source
        ?? null),
      apiBuildIdentity: report.target?.apiBuildIdentity ?? null,
      integrity: sanitizePayload(summary.integrity ?? null),
      artifacts,
    },
    failureContext: {
      capturedAt: report.failureEvidence?.capturedAt ?? null,
      reason: sanitizeText(report.failureEvidence?.reason
        ?? summary.reviewPacket?.firstFailureReason
        ?? summary.workflowError
        ?? '').slice(0, 2_000),
      pageUrl: sanitizeText(report.failureEvidence?.page?.url ?? '').slice(0, 1_000),
      pageTextExcerpt: sanitizeText(report.failureEvidence?.page?.bodyText ?? '').slice(0, 2_000),
    },
  };
}

export function listNativeFailureRequests({ summaryPath, checkName }) {
  const context = loadRetainedFailureContext(summaryPath);
  const { summaryInput, summary, report, target } = context;
  const failure = selectFailure(summary, report, checkName);
  assert(target.explorer, 'Domain report explorer is required to list prerequisite state requests');
  const proposalPath = authoringV2Path(target.project, target.explorer, 'construction-proposals');
  const reconcilePath = authoringV2Path(target.project, target.explorer, 'reconcile');
  const requests = Array.isArray(report.nativeRequests) ? report.nativeRequests : [];
  const proposals = [];

  for (const [proposalIndex, proposal] of requests.entries()) {
    if (proposal.method !== 'POST' || proposal.path !== proposalPath) continue;
    if (typeof proposal.browserRequestId !== 'string' || !proposal.browserRequestId.trim()) continue;
    if (typeof proposal.requestId !== 'string' || !proposal.requestId.trim()) continue;
    if (requests.filter(entry => entry.browserRequestId === proposal.browserRequestId).length !== 1) continue;

    let checkpoint;
    try {
      checkpoint = selectedProposalCheckpoint(target, proposal);
    } catch (error) {
      if (error.code === 'ERR_ASSERTION') continue;
      throw error;
    }

    const stateMatches = [];
    for (const [stateIndex, state] of requests.entries()) {
      if (state.method !== 'POST' || state.path !== reconcilePath || stateIndex >= proposalIndex) continue;
      if (typeof state.browserRequestId !== 'string' || !state.browserRequestId.trim()) continue;
      try {
        const retained = selectPrerequisiteState(report, target, proposal, proposalIndex, state.browserRequestId);
        stateMatches.push({
          browserRequestId: retained.browserRequestId,
          requestId: retained.requestId,
          method: retained.method,
          path: retained.path,
          statusCode: retained.statusCode,
          startedAt: retained.startedAt,
          responseReceivedAt: retained.responseReceivedAt,
          completedAt: retained.completedAt,
          checkpoint: retained.checkpoint,
          extractorArgs: [
            '--summary', summaryInput.path,
            '--browser-request-id', proposal.browserRequestId,
            '--request-id', proposal.requestId,
            ...(checkName ? ['--check', checkName] : []),
            '--state-browser-request-id', retained.browserRequestId,
            '--out', '<output-path>',
          ],
        });
      } catch (error) {
        if (error.code === 'ERR_ASSERTION') continue;
        throw error;
      }
    }

    proposals.push({
      browserRequestId: proposal.browserRequestId,
      requestId: proposal.requestId,
      method: proposal.method,
      path: proposal.path,
      statusCode: proposal.status ?? null,
      startedAt: Number.isFinite(proposal.startedAt) ? proposal.startedAt : null,
      checkpoint,
      stateSelectionStatus: stateMatches.length === 0 ? 'unmatched' : 'options-available',
      ...(stateMatches.length === 0
        ? { stateSelectionReason: 'no-preceding-reconcile-passed-the-existing-checkpoint-validator' }
        : {}),
      stateMatches,
    });
  }

  return {
    schemaVersion: 1,
    kind: 'construction-proposal-request-inventory',
    scope: 'validated-construction-proposal-requests-only',
    summary: {
      path: summaryInput.path,
      sha256: summaryInput.sha256,
      scenario: summary.scenario,
      case: summary.case,
    },
    failureSelection: { selectionMethod: failure.selectionMethod },
    inventoryStatus: proposals.length === 0 ? 'empty-within-scope' : 'validated-proposals-found',
    selectionRequired: true,
    causalLinkageToFailure: 'not-established-by-extractor',
    proposals,
  };
}

function parseCli(argv) {
  const parsed = parseArgs({
    args: argv,
    options: {
      summary: { type: 'string' },
      'browser-request-id': { type: 'string' },
      'request-id': { type: 'string' },
      'state-browser-request-id': { type: 'string' },
      check: { type: 'string' },
      expected: { type: 'string' },
      out: { type: 'string' },
      'list-requests': { type: 'boolean' },
      help: { type: 'boolean', short: 'h' },
    },
    allowPositionals: false,
    strict: true,
  });
  if (parsed.values.help) return { help: true };
  if (parsed.values['list-requests']) {
    assert(parsed.values.summary?.trim(), '--summary is required');
    for (const option of ['browser-request-id', 'request-id', 'state-browser-request-id', 'expected', 'out']) {
      assert(parsed.values[option] === undefined, `--${option} cannot be combined with --list-requests`);
    }
    return { mode: 'list-requests', summaryPath: parsed.values.summary, checkName: parsed.values.check };
  }
  for (const option of ['summary', 'browser-request-id', 'request-id', 'out']) {
    assert(parsed.values[option]?.trim(), `--${option} is required`);
  }
  return {
    summaryPath: parsed.values.summary,
    browserRequestId: parsed.values['browser-request-id'],
    requestId: parsed.values['request-id'],
    stateBrowserRequestId: parsed.values['state-browser-request-id'],
    checkName: parsed.values.check,
    expectedPath: parsed.values.expected,
    outputPath: parsed.values.out,
  };
}

export function main(argv = process.argv.slice(2)) {
  const options = parseCli(argv);
  if (options.help) {
    process.stdout.write('Usage: node scripts/verify-ui/helpers/extract-native-failure-input.mjs --summary <summary.json> --browser-request-id <id> --request-id <id> [--state-browser-request-id <reconcile-id>] [--check <failed-assertion-name>] [--expected <value-expectation.json>] --out <fixture.json>\n');
    process.stdout.write('       node scripts/verify-ui/helpers/extract-native-failure-input.mjs --summary <summary.json> --list-requests [--check <failed-assertion-name>]\n');
    return 0;
  }
  if (options.mode === 'list-requests') {
    process.stdout.write(`${JSON.stringify(listNativeFailureRequests(options), null, 2)}\n`);
    return 0;
  }
  const bundle = extractNativeFailureInput(options);
  const outputPath = resolve(options.outputPath);
  const inputPaths = [options.summaryPath, options.expectedPath,
    ...bundle.provenance.artifacts.map(artifact => artifact.path)].filter(Boolean).map(path => resolve(path));
  if (existsSync(outputPath)) {
    const realOutput = realpathSync(outputPath);
    assert(!inputPaths.some(inputPath => {
      try { return realpathSync(inputPath) === realOutput; } catch { return resolve(inputPath) === outputPath; }
    }), 'Output path must not replace a retained input artifact');
    throw new Error(`Output already exists: ${outputPath}`);
  }
  assert(!inputPaths.includes(outputPath), 'Output path must not replace a retained input artifact');
  mkdirSync(dirname(outputPath), { recursive: true });
  writeFileSync(outputPath, `${JSON.stringify(bundle, null, 2)}\n`, { flag: 'wx' });
  process.stdout.write(JSON.stringify({ outputPath, expectedInputStatus: bundle.expectation.status,
    domainReportSha256: bundle.provenance.artifacts.find(artifact => artifact.label === 'domainReport')?.sha256 }) + '\n');
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    process.exitCode = main();
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
