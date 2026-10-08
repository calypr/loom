import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { extractNativeFailureInput } from '../extract-native-failure-input.mjs';
import { prepareCdaMembershipOracle } from '../cda-current-draft-membership-oracle.mjs';

const scope = { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
const fingerprint = { sha256: 'a'.repeat(64), files: 1508 };
const checkName = 'canceled Membership removal proposal renders an empty target';
const cliPath = fileURLToPath(new URL('../extract-native-failure-input.mjs', import.meta.url));
const hash = bytes => createHash('sha256').update(bytes).digest('hex');

function writeJson(path, value) {
  const bytes = Buffer.from(`${JSON.stringify(value)}\n`);
  writeFileSync(path, bytes);
  return hash(bytes);
}

function registerCleanup(t, directory) {
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

function makeLegacyInputs(t) {
  const directory = registerCleanup(t, mkdtempSync(join(tmpdir(), 'native-failure-legacy-')));
  const paths = Object.fromEntries(['report', 'results', 'runnerLog', 'summary', 'expected']
    .map(name => [name, join(directory, `${name}.${name === 'runnerLog' ? 'log' : 'json'}`)]));
  const response = {
    proposalId: 'proposal-literal',
    outputId: 'out-literal',
    authorization: 'Bearer response-secret-value',
    candidateConstruction: { version: 1, steps: [] },
    preview: { columns: [], rows: [{ __loom_row_id: 'observed-only-row' }] },
  };
  const request = {
    browserRequestId: 'playwright-139',
    requestId: 'construction-proposal-literal',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/explorer-literal/authoring/v2/construction-proposals',
    triggerAction: 'canceled: propose removal of the exact saved Membership step',
    body: { outputId: 'out-literal', apiKey: 'request-secret-value', candidateConstruction: { version: 1, steps: [] } },
    status: 200,
    response,
  };
  const report = {
    schemaVersion: 1,
    scenario: 'cda-current-draft-membership',
    case: 'membership',
    status: 'failed',
    target: { ...scope, explorer: 'explorer-literal', sourceFingerprint: fingerprint, apiBuildIdentity: 'api-build-literal' },
    verificationIdentity: {
      sourceFingerprint: { before: fingerprint, after: fingerprint },
      sourceFreeze: { unchanged: true, invalidatesRun: false },
    },
    assertions: [{ dimension: 'correctness', name: checkName, status: 'failed', evidence: { rows: [] } }],
    nativeRequests: [request],
    failureEvidence: {
      capturedAt: '2026-10-06T06:48:38.116Z',
      reason: 'Authorization: Bearer failure-secret-value',
      page: { url: 'http://127.0.0.1:30008/', bodyText: 'Observed preview text.' },
    },
  };
  const reportSha = writeJson(paths.report, report);
  const resultsSha = writeJson(paths.results, { status: 'failed', test: 'membership' });
  const runnerLogBytes = Buffer.from('retained runner log\n');
  const runnerLogSha = hash(runnerLogBytes);
  writeFileSync(paths.runnerLog, runnerLogBytes);
  writeJson(paths.summary, {
    status: 'failed',
    scenario: report.scenario,
    case: report.case,
    classification: 'HARNESS_EXPECTATION',
    sourceFingerprint: fingerprint,
    rawReportPath: paths.report,
    rawReportSha256: reportSha,
    playwrightResultsPath: paths.results,
    playwrightResultsSha256: resultsSha,
    runnerLogPath: paths.runnerLog,
    runnerLogSha256: runnerLogSha,
    assertions: { failedName: checkName },
    firstFailure: { check: checkName, observation: 'The native request returned a populated preview.' },
  });
  return { directory, paths, report, request, response, sourceFingerprint: fingerprint };
}

function makeBracketInputs(t, { responseAvailable = false } = {}) {
  const directory = registerCleanup(t, mkdtempSync(join(tmpdir(), 'native-failure-bracket-')));
  const paths = Object.fromEntries(['report', 'playwright', 'sourceBefore', 'sourceAfter', 'summary', 'out']
    .map(name => [name, join(directory, `${name}.json`)]));
  const request = {
    browserRequestId: 'playwright-24',
    requestId: 'construction-proposal-literal',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/explorer-literal/authoring/v2/construction-proposals',
    triggerAction: 'apply Group operation',
    authorization: 'Bearer bracket-secret-value',
    status: 200,
    ...(responseAvailable ? { response: { outputId: 'out-observed', preview: { rows: [{ count: 3 }] } } } : {}),
  };
  const report = {
    schemaVersion: 1,
    scenario: 'cda-group-numeric-filter',
    case: 'numeric-filter-after-group',
    caseName: 'numeric-filter-after-group',
    status: 'failed',
    target: { ...scope, explorer: 'explorer-literal', sourceFingerprint: fingerprint, apiBuildIdentity: 'api-build-literal' },
    verificationIdentity: { sourceFingerprint: { before: fingerprint, after: fingerprint }, sourceFreeze: { unchanged: true } },
    assertions: [],
    missingRequiredChecks: [checkName],
    nativeRequests: [request],
    failureEvidence: { reason: 'Browser action failed after the retained native request.', page: { url: 'http://127.0.0.1:30008/', bodyText: 'No failed assertion row was recorded.' } },
  };
  const reportSha = writeJson(paths.report, report);
  writeJson(paths.playwright, { suites: [], status: 'failed' });
  writeJson(paths.sourceBefore, { ...fingerprint });
  writeJson(paths.sourceAfter, { ...fingerprint });
  const summary = {
    schemaVersion: 1,
    status: 'failed',
    scenario: report.scenario,
    case: report.case,
    evidence: {
      summary: paths.summary,
      domainReport: paths.report,
      playwrightReport: paths.playwright,
      sourceBefore: paths.sourceBefore,
      sourceAfter: paths.sourceAfter,
    },
    integrity: { status: 'PASS', dimensions: { source: { status: 'PASS', before: fingerprint, after: fingerprint } } },
    lifecycle: { status: 'failed', missingCheckNames: [checkName] },
    reviewPacket: {
      status: 'failed',
      firstFailureReason: 'Browser action failed after the retained native request.',
      failedCheckNames: [],
      missingCheckNames: [checkName],
      report: paths.report,
      playwrightJson: paths.playwright,
    },
  };
  writeJson(paths.summary, summary);
  return { directory, paths, report, request, sourceFingerprint: fingerprint, summary };
}

function extract(input, overrides = {}) {
  return extractNativeFailureInput({
    summaryPath: input.paths.summary,
    browserRequestId: input.request.browserRequestId,
    requestId: input.request.requestId,
    ...overrides,
  });
}

function runCli(input, outputPath, { browserRequestId = input.request.browserRequestId,
  requestId = input.request.requestId, checkName, expectedPath } = {}) {
  return spawnSync(process.execPath, [cliPath,
    '--summary', input.paths.summary,
    '--browser-request-id', browserRequestId,
    '--request-id', requestId,
    '--out', outputPath,
    ...(checkName ? ['--check', checkName] : []),
    ...(expectedPath ? ['--expected', expectedPath] : [])], { encoding: 'utf8' });
}

test('extracts a legacy failed assertion with verified input hashes and explicitly missing oracle', t => {
  const input = makeLegacyInputs(t);
  const fixture = extract(input);

  assert.equal(fixture.kind, 'retained-native-failure-input');
  assert.equal(fixture.expectation.status, 'missing-independent-oracle');
  assert.equal(fixture.provenance.artifacts.find(artifact => artifact.label === 'domainReport').hashBasis, 'verified-against-summary');
  assert.equal(fixture.provenance.artifacts.find(artifact => artifact.label === 'playwrightReport').hashBasis, 'verified-against-summary');
  assert.deepEqual(fixture.provenance.sourceFingerprint, input.sourceFingerprint);
  assert.equal(fixture.failureSelection.assertionName, checkName);
  assert.equal(fixture.requestEvidence.nativeRequest.browserRequestId, 'playwright-139');
  assert.equal(fixture.requestEvidence.nativeRequest.requestId, 'construction-proposal-literal');
  assert.equal(fixture.requestSelection.causalLinkageToFailure, 'not-established-by-extractor');
  assert.equal(JSON.stringify(fixture).includes('response-secret-value'), false);
  assert.equal(JSON.stringify(fixture).includes('request-secret-value'), false);
  assert.equal(JSON.stringify(fixture).includes('failure-secret-value'), false);
});

test('native bracket summary hashes retained evidence at extraction and preserves integrity and request-only state', t => {
  const input = makeBracketInputs(t);
  const fixture = extract(input);
  const domain = fixture.provenance.artifacts.find(artifact => artifact.label === 'domainReport');
  const playwright = fixture.provenance.artifacts.find(artifact => artifact.label === 'playwrightReport');

  assert.equal(domain.hashBasis, 'captured-at-extraction');
  assert.equal(domain.sha256, hash(readFileSync(input.paths.report)));
  assert.equal(playwright.hashBasis, 'captured-at-extraction');
  assert.deepEqual(fixture.provenance.integrity, input.summary.integrity);
  assert.equal(fixture.failureSelection.selectionMethod, 'retained-first-failure-context');
  assert.equal(fixture.failureSelection.assertionName, null);
  assert.equal(fixture.requestEvidence.responseState, 'not-retained');
  assert.equal(Object.hasOwn(fixture.requestEvidence.nativeRequest, 'response'), false);
  assert.equal(fixture.expectation.status, 'missing-independent-oracle');
});

test('uses the real Membership raw oracle with literal expected values, while leaving derivation unverified', t => {
  const input = makeLegacyInputs(t);
  const rawRows = [
    { _id: 'Observation/z', id: 'obs-z', ...scope, resourceType: 'Observation' },
    { _id: 'Observation/a', id: 'obs-a', ...scope, resourceType: 'Observation' },
    { _id: 'Observation/m', id: 'obs-m', ...scope, resourceType: 'Observation' },
  ];
  const oracle = prepareCdaMembershipOracle(rawRows, scope);
  const literalValue = { includeIDs: ['obs-a'], excludeIDs: ['obs-m'] };
  assert.deepEqual({ includeIDs: oracle.includeIDs, excludeIDs: oracle.excludeIDs }, literalValue);

  const expected = {
    schemaVersion: 1,
    kind: 'value-expectation',
    sourceFingerprint: input.sourceFingerprint,
    target: scope,
    provenance: { basis: 'raw Observation fixture used by prepareCdaMembershipOracle' },
    value: literalValue,
  };
  writeJson(input.paths.expected, expected);
  const fixture = extract(input, { expectedPath: input.paths.expected });

  assert.equal(fixture.expectation.status, 'separately-supplied-provenance-unverified');
  assert.equal(fixture.expectation.independenceVerified, false);
  assert.equal(fixture.expectation.sha256, hash(readFileSync(input.paths.expected)));
  assert.deepEqual(fixture.expectation.value, literalValue);
  assert.deepEqual(fixture.expectation.declaration, expected.provenance);
});

test('does not mistake equality between a separate expectation and observed response for verified independence', t => {
  const input = makeLegacyInputs(t);
  const expected = {
    schemaVersion: 1,
    kind: 'value-expectation',
    sourceFingerprint: input.sourceFingerprint,
    target: scope,
    provenance: { basis: 'separate value oracle supplied by the case owner' },
    value: input.response,
  };
  writeJson(input.paths.expected, expected);
  const fixture = extract(input, { expectedPath: input.paths.expected });
  assert.equal(fixture.expectation.status, 'separately-supplied-provenance-unverified');
  assert.equal(fixture.expectation.independenceVerified, false);
  assert.equal(fixture.expectation.value.outputId, input.response.outputId);
  assert.deepEqual(fixture.expectation.value.preview, input.response.preview);
  assert.equal(fixture.expectation.value.authorization, '[REDACTED]');
});

test('CLI succeeds on native bracket input, fails closed on selection/hash errors, and never overwrites inputs or existing output', t => {
  const input = makeBracketInputs(t);
  const outputPath = input.paths.out;
  const summaryHashBefore = hash(readFileSync(input.paths.summary));
  const reportHashBefore = hash(readFileSync(input.paths.report));
  const playwrightHashBefore = hash(readFileSync(input.paths.playwright));
  const success = runCli(input, outputPath);
  assert.equal(success.status, 0, success.stderr);
  const output = JSON.parse(readFileSync(outputPath, 'utf8'));
  assert.equal(output.expectation.status, 'missing-independent-oracle');
  assert.equal(output.provenance.integrity.status, 'PASS');
  assert.equal(output.requestEvidence.responseState, 'not-retained');
  assert.equal(JSON.stringify(output).includes('bracket-secret-value'), false);
  assert.equal(JSON.stringify(output).includes('[REDACTED]'), true);
  assert.equal(hash(readFileSync(input.paths.summary)), summaryHashBefore);
  assert.equal(hash(readFileSync(input.paths.report)), reportHashBefore);
  assert.equal(hash(readFileSync(input.paths.playwright)), playwrightHashBefore);

  const inputOverwrite = runCli(input, input.paths.summary);
  assert.notEqual(inputOverwrite.status, 0);
  assert.equal(hash(readFileSync(input.paths.summary)), summaryHashBefore);

  const wrongRequest = runCli(input, join(input.directory, 'wrong-request.json'), { requestId: 'wrong-id' });
  assert.notEqual(wrongRequest.status, 0);
  assert.equal(existsSync(join(input.directory, 'wrong-request.json')), false);

  const unsupportedCheck = runCli(input, join(input.directory, 'unsupported-check.json'), { checkName });
  assert.notEqual(unsupportedCheck.status, 0);
  assert.equal(existsSync(join(input.directory, 'unsupported-check.json')), false);

  const occupiedPath = join(input.directory, 'occupied.json');
  writeFileSync(occupiedPath, 'keep this input');
  const overwrite = runCli(input, occupiedPath);
  assert.notEqual(overwrite.status, 0);
  assert.equal(readFileSync(occupiedPath, 'utf8'), 'keep this input');

  const altered = makeLegacyInputs(t);
  writeFileSync(altered.paths.results, 'tampered retained results');
  const badHash = runCli(altered, join(altered.directory, 'bad-hash-output.json'));
  assert.notEqual(badHash.status, 0);
  assert.equal(existsSync(join(altered.directory, 'bad-hash-output.json')), false);
});

test('ambiguous selection fails and explicit --check selects one of multiple failed assertions', t => {
  const input = makeLegacyInputs(t);
  const secondName = 'another retained failed assertion';
  const report = {
    ...input.report,
    assertions: [...input.report.assertions, { dimension: 'correctness', name: secondName, status: 'failed' }],
  };
  const reportSha = writeJson(input.paths.report, report);
  const summary = JSON.parse(readFileSync(input.paths.summary, 'utf8'));
  summary.rawReportSha256 = reportSha;
  writeJson(input.paths.summary, summary);

  assert.throws(() => extract(input), /provide --check/);
  const selected = extract(input, { checkName: secondName });
  assert.equal(selected.failureSelection.assertionName, secondName);
  assert.equal(selected.failureSelection.selectionMethod, 'explicit-failed-assertion');
  const cliOutput = join(input.directory, 'selected-check.json');
  const cli = runCli(input, cliOutput, { checkName: secondName });
  assert.equal(cli.status, 0, cli.stderr);
  assert.equal(JSON.parse(readFileSync(cliOutput, 'utf8')).failureSelection.assertionName, secondName);

  const duplicate = { ...report, nativeRequests: [...report.nativeRequests, input.request] };
  const duplicateSha = writeJson(input.paths.report, duplicate);
  summary.rawReportSha256 = duplicateSha;
  writeJson(input.paths.summary, summary);
  assert.throws(() => extract(input, { checkName: secondName }), /exactly one native request/);
});
