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

/**
 * Extract a request chosen by exact browser/native IDs. The report may not establish
 * causal linkage between that request and a check; the output records that limitation.
 */
export function extractNativeFailureInput({ summaryPath, browserRequestId, requestId, checkName, expectedPath }) {
  assert(typeof browserRequestId === 'string' && browserRequestId.trim(), 'browserRequestId is required');
  assert(typeof requestId === 'string' && requestId.trim(), 'requestId is required');
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
  const report = JSON.parse(domainArtifact && readFileSync(domainArtifact.path, 'utf8'));
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

  const failure = selectFailure(summary, report, checkName);
  const browserMatches = (report.nativeRequests ?? []).filter(entry => entry.browserRequestId === browserRequestId);
  assert.equal(browserMatches.length, 1, `Expected exactly one native request for ${browserRequestId}`);
  const request = browserMatches[0];
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

function parseCli(argv) {
  const parsed = parseArgs({
    args: argv,
    options: {
      summary: { type: 'string' },
      'browser-request-id': { type: 'string' },
      'request-id': { type: 'string' },
      check: { type: 'string' },
      expected: { type: 'string' },
      out: { type: 'string' },
      help: { type: 'boolean', short: 'h' },
    },
    allowPositionals: false,
    strict: true,
  });
  if (parsed.values.help) return { help: true };
  for (const option of ['summary', 'browser-request-id', 'request-id', 'out']) {
    assert(parsed.values[option]?.trim(), `--${option} is required`);
  }
  return {
    summaryPath: parsed.values.summary,
    browserRequestId: parsed.values['browser-request-id'],
    requestId: parsed.values['request-id'],
    checkName: parsed.values.check,
    expectedPath: parsed.values.expected,
    outputPath: parsed.values.out,
  };
}

export function main(argv = process.argv.slice(2)) {
  const options = parseCli(argv);
  if (options.help) {
    process.stdout.write('Usage: node scripts/verify-ui/helpers/extract-native-failure-input.mjs --summary <summary.json> --browser-request-id <id> --request-id <id> [--check <failed-assertion-name>] [--expected <value-expectation.json>] --out <fixture.json>\n');
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
