#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { basename, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { sourceFingerprint } from './verify-ui/helpers/source-fingerprint.mjs';

const ROOT = resolve(fileURLToPath(new URL('..', import.meta.url)));
const DEFAULT_OUTPUT = 'verification/builder-interactions/browser-evidence.json';
const JOURNEY_AUDIT = 'verification/builder-interactions/builder-journey-audit.json';
const CDA_JOURNEYS = 'verification/builder-interactions/cda-mini-journeys.json';
const REGISTRY = 'scripts/verify-ui/registry.mjs';
const BUILDER_SOURCE_CONTRACT = 'verification/builder-interactions/builder-preview-contract.json';
const HISTORICAL_RECOMPILE_ID = 'builder-controls:recompile';
const PLAYWRIGHT_SANITIZER = 'scripts/verify-ui/helpers/playwright-browser.mjs';

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const fail = (message) => { throw new Error(message); };
const requireValue = (condition, message) => { if (!condition) fail(message); };
const stableJson = (value) => Array.isArray(value)
  ? value.map(stableJson)
  : value && typeof value === 'object'
    ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, stableJson(value[key])]))
    : value;
const sameJson = (left, right) => JSON.stringify(stableJson(left)) === JSON.stringify(stableJson(right));
const canonicalJson = (value) => Array.isArray(value)
  ? '[' + value.map(canonicalJson).join(',') + ']'
  : value && typeof value === 'object'
    ? '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}'
    : JSON.stringify(value);
const asArray = (value) => Array.isArray(value) ? value : [];
const isSha256 = (value) => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value);
const isGitCommit = (value) => typeof value === 'string' && /^[0-9a-f]{40}$/.test(value);

const jsonFromBytes = (bytes, label) => {
  try { return JSON.parse(bytes.toString('utf8')); }
  catch (error) { fail(label + ' is not valid JSON: ' + error.message); }
};

const pathWithin = (root, path, label) => {
  requireValue(typeof path === 'string' && path.length > 0, label + ' path is required');
  requireValue(!isAbsolute(path), label + ' path must be repository-relative');
  const absolute = resolve(root, path);
  const local = relative(root, absolute);
  requireValue(local !== '..' && !local.startsWith('..' + sep) && !isAbsolute(local), label + ' path escapes repository root: ' + path);
  return absolute;
};

const displayPath = (root, absolute) => {
  const path = relative(root, absolute);
  return path.startsWith('..') ? basename(absolute) : path.split(sep).join('/');
};

const readJson = (root, path, label = path) => {
  const absolute = pathWithin(root, path, label);
  requireValue(existsSync(absolute), label + ' does not exist: ' + path);
  return jsonFromBytes(readFileSync(absolute), label);
};

const checkedArtifact = (root, descriptor, label, parseJson = true) => {
  requireValue(descriptor && typeof descriptor === 'object', label + ' descriptor is missing');
  requireValue(isSha256(descriptor.sha256), label + ' descriptor must contain a 64-character SHA-256');
  const absolute = pathWithin(root, descriptor.path, label);
  requireValue(existsSync(absolute), label + ' artifact is missing: ' + descriptor.path);
  const bytes = readFileSync(absolute);
  const actual = sha256(bytes);
  requireValue(actual === descriptor.sha256, label + ' SHA-256 mismatch for ' + descriptor.path + ': expected ' + descriptor.sha256 + ', found ' + actual);
  return { absolute, bytes, value: parseJson ? jsonFromBytes(bytes, label) : null, path: descriptor.path, sha256: actual };
};

const artifactRefsFor = (run) => {
  const refs = {};
  for (const key of ['nativeReport', 'bracketSummary', 'trace', 'tlaReport', 'traceReport']) {
    if (run[key]) refs[key] = run[key];
  }
  for (const [key, value] of Object.entries(run.artifacts ?? {})) refs[key] = value;
  for (const [key, value] of Object.entries(run.verificationFreeze?.artifacts ?? {})) refs['freeze.' + key] = value;
  if (run.traceMutationControls?.artifact) refs.traceControls = run.traceMutationControls.artifact;
  return refs;
};

const sourceCommitFor = (run) => run.sourceIdentity?.commit ?? run.sourceCommit;
const proofFingerprintFor = (run) => run.sourceIdentity?.proofSourceFingerprint ?? run.proofSourceFingerprint;
const watchedFingerprintFor = (run) => run.sourceIdentity?.watchedTreeFingerprint;
const watchedFileCountFor = (run) => run.sourceIdentity?.watchedTreeFileCount;

const gitBlob = (gitRoot, commit, path) => {
  try {
    const objectId = execFileSync('git', ['-C', gitRoot, 'rev-parse', commit + ':' + path], { encoding: 'utf8' }).trim();
    const bytes = execFileSync('git', ['-C', gitRoot, 'show', commit + ':' + path]);
    return { objectId, bytes };
  } catch (error) {
    return null;
  }
};

const blobAtTestedCommit = (root, commit, path) => gitBlob(root, commit, path) ?? gitBlob(ROOT, commit, path);

const gitBlobs = (gitRoot, commit, paths) => {
  try {
    const input = Buffer.from(paths.map((path) => commit + ':' + path + '\n').join(''));
    const output = execFileSync('git', ['-C', gitRoot, 'cat-file', '--batch'], { input, maxBuffer: 64 * 1024 * 1024 });
    const blobs = new Map();
    let offset = 0;
    for (const path of paths) {
      const headerEnd = output.indexOf(0x0a, offset);
      if (headerEnd < 0) return null;
      const header = output.toString('utf8', offset, headerEnd);
      const match = /^([0-9a-f]{40}) blob (\d+)$/.exec(header);
      if (!match) return null;
      const size = Number(match[2]);
      const start = headerEnd + 1;
      const end = start + size;
      if (!Number.isSafeInteger(size) || end >= output.length || output[end] !== 0x0a) return null;
      blobs.set(path, { objectId: match[1], bytes: output.subarray(start, end) });
      offset = end + 1;
    }
    return blobs;
  } catch (error) {
    return null;
  }
};

const blobsAtTestedCommit = (root, commit, paths) => gitBlobs(root, commit, paths) ?? gitBlobs(ROOT, commit, paths);

const sanitizerAtCommit = async (root, commit, manifest, label) => {
  const helper = blobAtTestedCommit(root, commit, PLAYWRIGHT_SANITIZER);
  requireValue(helper, label + ': sanitizer source is unavailable at tested source commit');
  const helperPath = pathWithin(root, PLAYWRIGHT_SANITIZER, 'tested sanitizer');
  requireValue(existsSync(helperPath), label + ': current sanitizer source is missing');
  const currentBytes = readFileSync(helperPath);
  requireValue(sha256(currentBytes) === sha256(helper.bytes), label + ': current sanitizer bytes differ from tested source commit');
  requireValue(manifest?.[PLAYWRIGHT_SANITIZER] === sha256(currentBytes), label + ': native manifest sanitizer hash differs from tested helper bytes');
  const sanitizerModule = await import(pathToFileURL(helperPath).href);
  requireValue(typeof sanitizerModule.sanitizePayload === 'function', label + ': tested sanitizer does not export sanitizePayload');
  return sanitizerModule;
};

const registryForCommit = async (root, commit) => {
  const source = blobAtTestedCommit(root, commit, REGISTRY);
  requireValue(source, 'tested registry is unavailable at source commit ' + commit);
  const bytes = source.bytes;
  const moduleUrl = 'data:text/javascript;base64,' + bytes.toString('base64');
  const registryModule = await import(moduleUrl);
  requireValue(typeof registryModule.scenarioCaseFor === 'function', 'tested registry does not export scenarioCaseFor');
  return registryModule;
};

const currentRegistryForRoot = async (root) => {
  const registryPath = pathWithin(root, REGISTRY, 'current verify-ui registry');
  requireValue(existsSync(registryPath), 'current verify-ui registry is missing');
  const bytes = readFileSync(registryPath);
  const registryModule = await import('data:text/javascript;base64,' + bytes.toString('base64'));
  requireValue(typeof registryModule.scenarioCaseFor === 'function', 'current verify-ui registry does not export scenarioCaseFor');
  return registryModule;
};

const sourceEntriesFrom = (root, sources, label, readSource) => {
  if (!Array.isArray(sources) || sources.length === 0) fail(label + ' must contain a non-empty sources array');
  const seen = new Set();
  const entries = [];
  const declaredMismatches = [];
  for (let index = 0; index < sources.length; index += 1) {
    const source = sources[index];
    if (!source || typeof source.path !== 'string' || !isSha256(source.sha256)) {
      fail(label + ' sources[' + index + '] must contain a repository-relative path and a 64-character SHA-256');
    }
    if (/[\r\n]/.test(source.path)) fail(label + ' source path contains a line break');
    pathWithin(root, source.path, label + ' source');
    if (seen.has(source.path)) fail(label + ' repeats source path ' + source.path);
    seen.add(source.path);
    const bytes = readSource(source.path);
    if (!bytes) fail(label + ' source is unavailable: ' + source.path);
    const actual = sha256(bytes);
    if (actual !== source.sha256) declaredMismatches.push(source.path);
    entries.push({ path: source.path, sha256: actual });
  }
  entries.sort((left, right) => left.path.localeCompare(right.path));
  return { entries, sha256: sha256(canonicalJson(entries)), declaredMismatches };
};

const historicalProofScope = (root, sourceCommit) => {
  const pinned = blobAtTestedCommit(root, sourceCommit, BUILDER_SOURCE_CONTRACT);
  if (!pinned) return { status: 'unavailable', reason: 'the Builder source manifest is unavailable at tested source commit ' + sourceCommit };
  let contract;
  try { contract = JSON.parse(pinned.bytes.toString('utf8')); }
  catch (error) { return { status: 'unavailable', reason: 'the Builder source manifest at tested source commit is invalid JSON: ' + error.message }; }
  try {
    const paths = [];
    const seen = new Set();
    for (let index = 0; index < (contract.sources ?? []).length; index += 1) {
      const source = contract.sources[index];
      if (!source || typeof source.path !== 'string' || !isSha256(source.sha256)) {
        fail('Builder source manifest at tested source commit sources[' + index + '] must contain a repository-relative path and a 64-character SHA-256');
      }
      if (/[\r\n]/.test(source.path)) fail('Builder source manifest at tested source commit contains a source path with a line break');
      pathWithin(root, source.path, 'Builder source manifest at tested source commit source');
      if (seen.has(source.path)) fail('Builder source manifest at tested source commit repeats source path ' + source.path);
      seen.add(source.path);
      paths.push(source.path);
    }
    const blobs = blobsAtTestedCommit(root, sourceCommit, paths);
    if (!blobs) fail('could not load the historical Builder source scope from Git at ' + sourceCommit);
    const result = sourceEntriesFrom(root, contract.sources, 'Builder source manifest at tested source commit', (path) => blobs.get(path)?.bytes);
    const mismatches = result.declaredMismatches;
    if (mismatches.length > 0) {
      return { status: 'unavailable', reason: 'the ' + result.entries.length + '-file historical source scope has manifest hashes that do not match Git content at ' + sourceCommit + ': ' + mismatches.join(', ') };
    }
    return { status: 'current', sourceCommit, ...result };
  } catch (error) {
    return { status: 'unavailable', reason: error.message };
  }
};

const currentProofScope = (root) => {
  const contractPath = pathWithin(root, BUILDER_SOURCE_CONTRACT, 'current Builder source manifest');
  if (!existsSync(contractPath)) return { status: 'unavailable', reason: 'current Builder source manifest is missing' };
  let contract;
  try { contract = jsonFromBytes(readFileSync(contractPath), 'current Builder source manifest'); }
  catch (error) { return { status: 'unavailable', reason: error.message }; }
  try {
    const result = sourceEntriesFrom(root, contract.sources, 'current Builder source manifest', (path) => {
      const absolute = pathWithin(root, path, 'current Builder source');
      return existsSync(absolute) ? readFileSync(absolute) : null;
    });
    return { status: 'current', ...result };
  } catch (error) {
    return { status: 'unavailable', reason: error.message };
  }
};

const requireHistoricalProofFingerprint = (root, sourceCommit, expectedFingerprint, label) => {
  const historical = historicalProofScope(root, sourceCommit);
  requireValue(historical.status === 'current', label + ': ' + historical.reason);
  requireValue(expectedFingerprint === historical.sha256, label + ': retained proof fingerprint contradicts the verified ' + historical.entries.length + '-file historical source scope at tested source commit ' + sourceCommit + ': expected ' + historical.sha256 + ', recorded ' + expectedFingerprint);
  return historical;
};

const currentProofFingerprint = (root, historical, expectedFingerprint) => {
  if (historical.status !== 'current') return historical;
  if (expectedFingerprint !== historical.sha256) {
    return {
      status: 'unavailable',
      reason: 'retained proof fingerprint contradicts the verified ' + historical.entries.length + '-file historical source scope at tested source commit ' + historical.sourceCommit + ': expected ' + historical.sha256 + ', recorded ' + expectedFingerprint,
      historicalFingerprint: historical.sha256,
      historicalFileCount: historical.entries.length,
    };
  }
  const current = currentProofScope(root);
  if (current.status !== 'current') return current;
  const historicalPaths = new Set(historical.entries.map((entry) => entry.path));
  const currentPaths = new Set(current.entries.map((entry) => entry.path));
  const added = [...currentPaths].filter((path) => !historicalPaths.has(path)).sort();
  const removed = [...historicalPaths].filter((path) => !currentPaths.has(path)).sort();
  if (added.length > 0 || removed.length > 0) {
    return {
      status: 'stale',
      reason: 'Builder source scope changed from ' + historical.entries.length + ' historical files to ' + current.entries.length + ' current files (added: ' + (added.join(', ') || 'none') + '; removed: ' + (removed.join(', ') || 'none') + ')',
      historicalFingerprint: historical.sha256,
      currentFingerprint: current.sha256,
      historicalFileCount: historical.entries.length,
      currentFileCount: current.entries.length,
    };
  }
  const currentByPath = new Map(current.entries.map((entry) => [entry.path, entry.sha256]));
  const changed = historical.entries.filter((entry) => currentByPath.get(entry.path) !== entry.sha256).map((entry) => entry.path);
  if (changed.length > 0 || current.sha256 !== historical.sha256) {
    return {
      status: 'stale',
      reason: 'Builder source bytes changed within the ' + historical.entries.length + '-file historical source scope: ' + (changed.join(', ') || 'current fingerprint differs'),
      historicalFingerprint: historical.sha256,
      currentFingerprint: current.sha256,
      historicalFileCount: historical.entries.length,
      currentFileCount: current.entries.length,
    };
  }
  const currentManifestMismatches = current.declaredMismatches;
  if (currentManifestMismatches.length > 0) {
    return {
      status: 'stale',
      reason: 'current Builder source manifest hashes differ from current source bytes: ' + currentManifestMismatches.join(', '),
      historicalFingerprint: historical.sha256,
      currentFingerprint: current.sha256,
      historicalFileCount: historical.entries.length,
      currentFileCount: current.entries.length,
    };
  }
  return {
    status: 'current',
    reason: 'retained proof fingerprint matches the ' + historical.entries.length + '-file historical source scope and current Builder source manifest',
    sha256: current.sha256,
    count: current.entries.length,
  };
};

const sourceFreshness = (root, expected) => {
  if (!isSha256(expected)) return { status: 'unavailable', reason: 'retained evidence has no full 64-character source fingerprint' };
  try {
    const current = sourceFingerprint(root);
    if (current.sha256 === expected) return { status: 'current', reason: 'retained watched-tree fingerprint matches the current watched source tree' };
    return {
      status: 'stale',
      reason: 'retained watched-tree fingerprint ' + expected + ' differs from current watched-tree fingerprint ' + current.sha256,
      currentFingerprint: current.sha256,
    };
  } catch (error) {
    return { status: 'unavailable', reason: 'current watched-tree fingerprint is unavailable: ' + error.message };
  }
};

const nativeRunSourceFreshness = (root, run, historical) => {
  const watched = sourceFreshness(root, watchedFingerprintFor(run));
  const proofExpected = proofFingerprintFor(run);
  if (!isSha256(proofExpected)) {
    return { status: 'unavailable', reason: 'retained proof-source fingerprint is missing or malformed' };
  }
  const currentProof = currentProofFingerprint(root, historical, proofExpected);
  if (currentProof.status !== 'current') return currentProof;
  if (watched.status !== 'current') return watched;
  return { status: 'current', reason: 'retained watched-tree and pinned proof-source fingerprints match current source' };
};

const browserCaseRows = async (root, reportPaths, currentWatched, currentRegistry) => {
  const cases = [];
  const generatedFrom = [];
  for (const reportPath of reportPaths) {
    const absolute = isAbsolute(reportPath) ? resolve(reportPath) : pathWithin(root, reportPath, 'browser report');
    requireValue(existsSync(absolute), 'browser report does not exist: ' + reportPath);
    const bytes = readFileSync(absolute);
    const report = jsonFromBytes(bytes, 'browser report ' + reportPath);
    requireValue(report && typeof report === 'object' && !Array.isArray(report), 'browser report must be an object: ' + reportPath);
    requireValue(report.status === 'passed', reportPath + ': browser report status is ' + report.status + ', expected passed');
    requireValue(typeof report.scenario === 'string' && report.scenario.length > 0, reportPath + ': scenario is required');
    requireValue(typeof report.case === 'string' && report.case.length > 0, reportPath + ': case is required');
    const source = report.target?.sourceFingerprint;
    requireValue(isSha256(source?.sha256) && Number.isInteger(source.files), reportPath + ': target.sourceFingerprint must have a 64-character sha256 and integer file count');
    const apiBuildIdentity = report.target?.apiBuildIdentity;
    requireValue(typeof apiBuildIdentity === 'string' && /^[0-9a-f]{64}:[0-9a-f]{64}:[0-9a-f]{64}$/.test(apiBuildIdentity), reportPath + ': target.apiBuildIdentity must have three 64-character SHA-256 parts');
    const nativeRequests = Array.isArray(report.nativeRequests) ? report.nativeRequests : [];
    const injectedRequests = Array.isArray(report.injectedRequests) ? report.injectedRequests : [];
    const coveredRoutes = [...new Set(nativeRequests.filter((request) => request.method && request.path).map((request) => routeKey(request.method, request.path)))].sort();
    const injectedRoutes = [...new Set(injectedRequests.filter((request) => request.method && request.path).map((request) => routeKey(request.method, request.path)))].sort();
    requireValue(coveredRoutes.length > 0, reportPath + ': report has no native API routes');
    const assertions = asArray(report.assertions).map((assertion) => ({ name: assertion.name, status: assertion.status, dimension: assertion.dimension ?? null }));
    const failed = assertions.filter((assertion) => assertion.status !== 'passed');
    requireValue(failed.length === 0, reportPath + ': report has failed assertions: ' + failed.map((assertion) => assertion.name).join(', '));
    const sourceStatus = source.sha256 === currentWatched?.sha256 && source.files === currentWatched?.files
      ? { status: 'current', reason: 'report watched-tree fingerprint matches the current watched source tree' }
      : currentWatched
        ? { status: 'stale', reason: 'report watched-tree fingerprint ' + source.sha256 + ' (' + source.files + ' files) differs from current watched-tree fingerprint ' + currentWatched.sha256 + ' (' + currentWatched.files + ' files)' }
        : { status: 'unavailable', reason: 'current watched-tree fingerprint is unavailable' };
    if (sourceStatus.status === 'current') {
      requireValue(currentRegistry, reportPath + ': current-source report cannot be bound to the current tested registry');
      const registeredCase = currentRegistry.scenarioCaseFor(report.scenario, report.case);
      requireValue(registeredCase, reportPath + ': case is absent from the current registry: ' + report.scenario + ':' + report.case);
      requireValue(Array.isArray(registeredCase.requiredChecks) && registeredCase.requiredChecks.length > 0, reportPath + ': current registry case has no required checks');
      assertReportAssertions(report, registeredCase.requiredChecks, reportPath);
    }
    const id = report.scenario + ':' + report.case;
    requireValue(!cases.some((entry) => entry.id === id), 'duplicate browser case id: ' + id);
    cases.push({
      id,
      scenario: report.scenario,
      case: report.case,
      name: report.scenario + ' ' + report.case,
      status: sourceStatus.status === 'current' ? 'current' : 'historical',
      sourceStatus: sourceStatus.status,
      sourceStatusReason: sourceStatus.reason,
      recordedAt: report.finishedAt ?? report.startedAt ?? null,
      sourceFingerprint: source.sha256,
      sourceFileCount: source.files,
      apiBuildIdentity,
      coveredRoutes,
      injectedRoutes,
      assertions,
      report: displayPath(root, absolute),
      reportSha256: sha256(bytes),
    });
    generatedFrom.push(displayPath(root, absolute));
  }
  return { cases, generatedFrom };
};

const normalizeRoute = (path) => path
  .replace(/\?.*$/, '')
  .replace(/^\/api\/v1\/projects\/[^/]+/, '/api/v1/projects/{project}')
  .replace(/\/explorers\/[^/]+/, '/explorers/{explorerId}');
const routeKey = (method, path) => String(method).toUpperCase() + ' ' + normalizeRoute(String(path));

const assertReportAssertions = (report, registeredChecks, runLabel) => {
  requireValue(report.status === 'passed', runLabel + ': native report status is ' + report.status + ', expected passed');
  requireValue(sameJson(report.requiredChecks, registeredChecks), runLabel + ': native requiredChecks differ from the tested registry');
  requireValue(asArray(report.missingRequiredChecks).length === 0, runLabel + ': native report has missing required checks: ' + asArray(report.missingRequiredChecks).join(', '));
  const assertions = asArray(report.assertions);
  requireValue(assertions.length > 0, runLabel + ': native report contains no assertions');
  const names = new Set();
  for (const assertion of assertions) {
    requireValue(typeof assertion.name === 'string' && assertion.name.length > 0, runLabel + ': native assertion is missing a name');
    names.add(assertion.name);
    requireValue(assertion.status === 'passed', runLabel + ': native assertion failed: ' + assertion.name);
  }
  for (const check of registeredChecks) {
    requireValue(names.has(check), runLabel + ': registered required check has no native assertion: ' + check);
  }
  return assertions.length;
};

const assertNativeSourceManifest = async (root, native, sourceCommit, label) => {
  const manifest = native.sourceFingerprintManifest;
  const current = native.target?.sourceFingerprint;
  requireValue(manifest?.before && manifest?.after, label + ': native report is missing its source fingerprint manifests');
  requireValue(sameJson(manifest.before, manifest.after), label + ': native source fingerprint manifests changed during the browser run');
  requireValue(Object.keys(manifest.before).length === current?.files, label + ': native source manifest file count differs from its fingerprint');
  const redactedPaths = Object.entries(manifest.before).filter(([, value]) => value === '[REDACTED]').map(([path]) => path).sort();
  requireValue(Object.entries(manifest.before).every(([path, value]) => redactedPaths.includes(path) || isSha256(value)), label + ': native source manifest contains a malformed file hash');
  const sanitizer = await sanitizerAtCommit(root, sourceCommit, manifest.before, label);
  const rawManifest = { ...manifest.before };
  const redactedHashes = {};
  for (const path of redactedPaths) {
    const blob = blobAtTestedCommit(root, sourceCommit, path);
    requireValue(blob, label + ': redacted source file is unavailable at tested commit: ' + path);
    const fileHash = sha256(blob.bytes);
    requireValue(sanitizer.sanitizePayload({ [path]: fileHash })[path] === '[REDACTED]', label + ': native source manifest redaction is not explained by the tested sanitizer: ' + path);
    redactedHashes[path] = fileHash;
    rawManifest[path] = fileHash;
  }
  requireValue(sameJson(sanitizer.sanitizePayload(rawManifest), manifest.before), label + ': native source manifest differs from tested sanitizer output');
  return { sanitizer, redactedHashes };
};

const assertTlaReceipt = (traceDocument, tla, sourceCommit, proofFingerprint, traceName, model, unsupported, label) => {
  requireValue(traceDocument.sourceCommit === sourceCommit && tla.sourceCommit === sourceCommit, label + ': trace/TLA source commit does not match retained source identity');
  requireValue(traceDocument.sourceFingerprint === proofFingerprint && tla.sourceFingerprint === proofFingerprint, label + ': trace/TLA source fingerprint does not match retained source identity');
  requireValue(traceDocument.contractSourceCommit === tla.contractSourceCommit, label + ': trace and TLA contract source commits differ');
  requireValue(traceDocument.sourceProfile === tla.sourceProfile, label + ': trace and TLA source profiles differ');
  requireValue(tla.traceCount === 1 && tla.uniqueTraceCount === 1 && tla.validatedCount === 1 && tla.invalidCount === 0, label + ': TLA receipt does not contain exactly one validated trace');
  requireValue(tla.coverage?.requiredModels?.missing?.length === 0, label + ': TLA receipt is missing a required model');
  requireValue(tla.coverage?.requiredActions?.missing?.length === 0, label + ': TLA receipt is missing required actions');
  requireValue(Object.keys(tla.coverage?.requiredActionsByTrace?.missing ?? {}).length === 0, label + ': TLA receipt has missing per-trace actions');
  const missingRequiredEvents = tla.coverage?.requiredEvents?.missing;
  const missingEventNames = asArray(missingRequiredEvents).map((event) => typeof event === 'string' ? event : event?.name ?? JSON.stringify(event));
  requireValue(Array.isArray(missingRequiredEvents) && missingRequiredEvents.length === 0, label + ': TLA receipt is missing required events: ' + (missingEventNames.join(', ') || 'coverage.requiredEvents.missing must be an empty array'));
  const traceRecords = asArray(traceDocument.traces);
  const receiptRecords = asArray(tla.traces);
  requireValue(traceRecords.length === 1 && receiptRecords.length === 1, label + ': trace and TLA receipt must each contain one trace');
  const trace = traceRecords[0];
  const receipt = receiptRecords[0];
  requireValue(trace.name === traceName && receipt.name === traceName, label + ': trace/TLA journey identity mismatch; expected ' + traceName);
  requireValue(trace.model === model && receipt.model === model, label + ': trace/TLA model identity mismatch; expected ' + model);
  requireValue(receipt.status === 'pass', label + ': TLA receipt status is ' + receipt.status + ', expected pass');
  requireValue(receipt.actionCount === asArray(trace.actions).length, label + ': TLA action count differs from retained trace');
  requireValue(sameJson(tla.unmodeledEvents?.byKind ?? {}, unsupported), label + ': TLA unsupported-event counts differ from the explicit native-only boundary');
  requireValue(tla.unmodeledEvents?.unclassified?.total === 0, label + ': TLA receipt contains unclassified unsupported events');
  return { trace, receipt };
};

const assertBracket = (bracket, native, registeredChecks, expected, label) => {
  requireValue(bracket.status === 'passed' && bracket.nativeStatus === 'passed', label + ': retained native bracket did not pass');
  requireValue(bracket.scenario === expected.scenario && bracket.case === expected.caseName, label + ': bracket case identity mismatch');
  requireValue(bracket.lifecycle?.reportStatus === 'passed', label + ': bracket lifecycle status did not pass');
  requireValue(bracket.lifecycle?.reportIdentity?.scenario === expected.scenario && bracket.lifecycle?.reportIdentity?.caseName === expected.caseName, label + ': bracket report identity mismatch');
  requireValue(bracket.lifecycle?.requiredCheckListMatchesRegistry === true, label + ': bracket did not bind required checks to its registry');
  requireValue(bracket.lifecycle?.requiredCheckCount === registeredChecks.length && bracket.lifecycle?.passedCheckCount === registeredChecks.length, label + ': bracket required-check counts do not match the tested registry');
  requireValue(asArray(bracket.lifecycle?.failedCheckNames).length === 0 && asArray(bracket.lifecycle?.missingCheckNames).length === 0, label + ': bracket has failed or missing required checks');
  const source = bracket.integrity?.dimensions?.source;
  const current = native.target?.sourceFingerprint;
  requireValue(bracket.integrity?.status === 'PASS' && source?.status === 'PASS', label + ': bracket source integrity did not pass');
  requireValue(source.before?.sha256 === current?.sha256 && source.after?.sha256 === current?.sha256, label + ': bracket source fingerprints do not match native report');
  requireValue(source.before?.files === current?.files && source.after?.files === current?.files, label + ': bracket source file counts do not match native report');
  const api = bracket.integrity?.dimensions?.apiBuildIdentity;
  requireValue(api?.status === 'PASS' && api.before === native.target?.apiBuildIdentity && api.after === native.target?.apiBuildIdentity, label + ': bracket API identity does not match native report');
};

const assertFreeze = async (root, run, native, sourceCommit, sourceManifest, label) => {
  const freeze = run.verificationFreeze;
  if (!freeze) return;
  requireValue(freeze.status === 'pass' && freeze.sourceBeforeAfterUnchanged === true, label + ': verification freeze did not pass unchanged-source checks');
  requireValue(freeze.sourceFingerprint?.sha256 === native.target?.sourceFingerprint?.sha256, label + ': verification freeze source fingerprint does not match native report');
  requireValue(freeze.sourceFingerprint?.files === native.target?.sourceFingerprint?.files, label + ': verification freeze source file count does not match native report');
  const beforeRef = freeze.artifacts?.['source-before'];
  const afterRef = freeze.artifacts?.['source-after'];
  requireValue(beforeRef && afterRef, label + ': verification freeze is missing source-before/source-after artifacts');
  const before = checkedArtifact(root, beforeRef, label + ' source-before');
  const after = checkedArtifact(root, afterRef, label + ' source-after');
  for (const [phase, capture] of [['before', before.value], ['after', after.value]]) {
    requireValue(capture.phase === phase, label + ': verification freeze ' + phase + ' artifact has wrong phase');
    requireValue(capture.fingerprint?.sha256 === native.target?.sourceFingerprint?.sha256, label + ': verification freeze ' + phase + ' artifact fingerprint does not match native report');
    requireValue(capture.fingerprint?.files === native.target?.sourceFingerprint?.files, label + ': verification freeze ' + phase + ' artifact file count does not match native report');
  }
  requireValue(sameJson(before.value.manifest, after.value.manifest), label + ': verification freeze source manifest changed during the frozen run');
  requireValue(Object.keys(before.value.manifest ?? {}).length === native.target?.sourceFingerprint?.files, label + ': verification freeze source manifest file count differs from native report');
  requireValue(Object.values(before.value.manifest ?? {}).every(isSha256), label + ': verification freeze source manifest contains a malformed file hash');
  requireValue(sameJson(sourceManifest.sanitizer.sanitizePayload(before.value.manifest), native.sourceFingerprintManifest.before), label + ': sanitized verification freeze manifest differs from native report manifest');
  for (const [path, expected] of Object.entries(sourceManifest.redactedHashes)) {
    requireValue(before.value.manifest[path] === expected && after.value.manifest[path] === expected, label + ': freeze manifest hash for redacted path does not match tested commit: ' + path);
  }
};

const makeRetainedNativeRow = async (root, journey, runId, run, expectedCase) => {
  const label = 'retained run ' + runId;
  requireValue(run.result === 'pass', label + ': audit result is not pass');
  const refs = artifactRefsFor(run);
  const artifacts = {};
  const values = {};
  const artifactBytes = {};
  for (const [key, ref] of Object.entries(refs)) {
    const checked = checkedArtifact(root, ref, label + ' ' + key, key !== 'traceControls');
    artifacts[key] = { path: ref.path, sha256: checked.sha256 };
    values[key] = checked.value;
    artifactBytes[key] = checked.bytes;
  }

  const native = values.nativeReport;
  const bracket = values.bracketSummary;
  const traceDocument = values.trace;
  const tla = values.tlaReport;
  const journeyReport = values.journeyReport;
  requireValue(native && bracket && traceDocument && tla, label + ': native run lacks native report, bracket summary, trace, or TLA receipt');
  const sourceCommit = sourceCommitFor(run);
  const proofFingerprint = proofFingerprintFor(run);
  const watchedFingerprint = watchedFingerprintFor(run);
  const watchedFiles = watchedFileCountFor(run);
  requireValue(isGitCommit(sourceCommit), label + ': source commit is missing or malformed');
  requireValue(isSha256(proofFingerprint), label + ': proof-source fingerprint is missing or malformed');
  const historicalScope = requireHistoricalProofFingerprint(root, sourceCommit, proofFingerprint, label);
  requireValue(isSha256(watchedFingerprint), label + ': watched-tree fingerprint must be a full 64-character SHA-256');
  requireValue(Number.isInteger(watchedFiles), label + ': watched-tree file count is missing');
  requireValue(native.scenario === expectedCase.scenario && native.case === expectedCase.caseName, label + ': native case identity mismatch');
  requireValue(native.target?.sourceFingerprint?.sha256 === watchedFingerprint, label + ': native report watched-tree fingerprint differs from retained source identity');
  requireValue(native.target?.sourceFingerprint?.files === watchedFiles, label + ': native report watched-tree file count differs from retained source identity');
  requireValue(native.apiBuildFreeze?.before === native.target?.apiBuildIdentity && native.apiBuildFreeze?.after === native.target?.apiBuildIdentity, label + ': native report API build identity is inconsistent');

  const testedRegistry = await registryForCommit(root, sourceCommit);
  const registeredCase = testedRegistry.scenarioCaseFor(expectedCase.scenario, expectedCase.caseName);
  requireValue(registeredCase, label + ': case is absent from registry at tested source commit ' + sourceCommit);
  const registeredChecks = registeredCase.requiredChecks;
  requireValue(Array.isArray(registeredChecks) && registeredChecks.length > 0, label + ': tested registry contains no required checks');
  const assertionCount = assertReportAssertions(native, registeredChecks, label);
  requireValue(run.counts?.nativeAssertionsPassed === assertionCount, label + ': audit native assertion count does not match retained report');
  requireValue(run.counts?.requiredChecksPassed === registeredChecks.length && run.counts?.missingRequiredChecks === 0, label + ': audit required-check counts do not match tested registry');
  const sourceManifest = await assertNativeSourceManifest(root, native, sourceCommit, label);
  assertBracket(bracket, native, registeredChecks, expectedCase, label);
  await assertFreeze(root, run, native, sourceCommit, sourceManifest, label);

  const expectedTraceName = run.traceMutationControls?.positiveTraceName ?? run.selectedNativeCase?.traceName;
  requireValue(typeof expectedTraceName === 'string' && expectedTraceName.length > 0, label + ': expected trace name is missing');
  const unsupported = run.formalBoundary?.unsupportedNativeEvents;
  requireValue(unsupported && typeof unsupported === 'object' && Object.keys(unsupported).length > 0, label + ': explicit unsupported native-event boundary is missing');
  const { trace, receipt } = assertTlaReceipt(traceDocument, tla, sourceCommit, proofFingerprint, expectedTraceName, journey.model, unsupported, label);
  requireValue(traceDocument.traces.length === 1 && trace.actions.length === run.counts?.traceActionCount, label + ': retained trace action count differs from audit');
  const requiredActions = registeredCase.journeyTrace?.requiredActions ?? [];
  requireValue(requiredActions.length > 0, label + ': tested registry declares no required trace actions');
  const observedActions = asArray(trace.actions).map((action) => action.label);
  for (const action of requiredActions) {
    requireValue(observedActions.includes(action), label + ': retained trace is missing registered required action ' + action);
    requireValue(asArray(tla.coverage?.requiredActions?.missing).length === 0, label + ': TLA receipt is missing registered required action ' + action);
  }
  requireValue(run.traceMutationControls?.result === 'passed', label + ': repeat-edit trace mutation controls did not pass');
  requireValue(requiredActions.every((action) => run.traceMutationControls?.requiredAction === action), label + ': repeat-edit control does not match the tested registry required action');
  const expectedRejections = asArray(run.traceMutationControls?.expectedRejections);
  requireValue(expectedRejections.some((reason) => typeof reason === 'string' && (/repeat-edit-required/.test(reason) || /missing .*EditGroupAgain/i.test(reason))), label + ': repeat-edit negative control does not cover a missing EditGroupAgain action');
  if (artifactBytes.traceControls) {
    const controlLog = artifactBytes.traceControls.toString('utf8');
    for (const rejection of expectedRejections) {
      requireValue(controlLog.includes(rejection), label + ': retained trace-control log is missing rejection ' + rejection);
    }
  }
  if (run.traceMutationControls?.traceArtifact) {
    requireValue(run.traceMutationControls.traceArtifact === run.trace?.path, label + ': repeat-edit control trace path differs from the retained trace descriptor');
    requireValue(run.traceMutationControls.traceArtifactSha256 === run.trace?.sha256, label + ': repeat-edit control trace hash differs from the retained trace descriptor');
  }
  if (run.traceMutationControls?.testedSourceCommit) {
    requireValue(run.traceMutationControls.testedSourceCommit === sourceCommit, label + ': repeat-edit controls cite a different source commit');
  }
  if (journeyReport) {
    requireValue(journeyReport.result === 'pass' && journeyReport.playwrightStatus === 0 && journeyReport.tlaStatus === 'pass', label + ': retained journey runner report did not pass');
    requireValue(journeyReport.journey?.name === expectedTraceName && journeyReport.journey?.model === journey.model, label + ': journey runner report identity/model mismatch');
    requireValue(journeyReport.sourceCommit === sourceCommit && journeyReport.sourceFingerprint === proofFingerprint, label + ': journey runner report source identity differs from retained source identity');
    requireValue(journeyReport.contractSourceCommit === traceDocument.contractSourceCommit, label + ': journey runner report contract source commit differs from retained trace');
    requireValue(journeyReport.nativeCase?.scenarioID === expectedCase.scenario && journeyReport.nativeCase?.caseName === expectedCase.caseName, label + ': journey runner report native-case identity mismatch');
    requireValue(journeyReport.nativeCase?.traceName === expectedTraceName && sameJson(journeyReport.nativeCase?.requiredTraceActions, requiredActions), label + ': journey runner report registered trace actions differ from tested registry');
    requireValue(journeyReport.tla?.sourceCommit === sourceCommit && journeyReport.tla?.sourceFingerprint === proofFingerprint, label + ': journey runner report embedded TLA identity differs from retained TLA receipt');
  }
  const actualUnsupported = tla.unmodeledEvents?.byKind ?? {};
  requireValue(sameJson(actualUnsupported, unsupported), label + ': audit/native-only Cancel and reload events differ from the TLA receipt');

  const source = bracket.integrity.dimensions.source.before;
  const apiIdentity = native.target.apiBuildIdentity;
  requireValue(run.target?.apiBuildIdentity === apiIdentity, label + ': audit target API identity differs from retained native report');
  requireValue(run.sourceIdentity?.contractSourceCommit === traceDocument.contractSourceCommit, label + ': audit contract source commit differs from retained trace');
  requireValue(run.counts?.tlcGeneratedStates === receipt.tlc?.stateCount, label + ': audit TLC state count differs from retained TLA receipt');
  requireValue(run.counts?.tlcDistinctStates === receipt.tlc?.distinctStateCount, label + ': audit TLC distinct-state count differs from retained TLA receipt');
  requireValue(run.counts?.tlcDepth === receipt.tlc?.depth, label + ': audit TLC depth differs from retained TLA receipt');
  const freshness = nativeRunSourceFreshness(root, run, historicalScope);
  return {
    id: journey.id + ':' + runId + ':' + sourceCommit,
    runId,
    journey: journey.id,
    scenario: expectedCase.scenario,
    case: expectedCase.caseName,
    traceName: expectedTraceName,
    status: 'historical',
    sourceStatus: freshness.status,
    sourceStatusReason: freshness.reason,
    recordedAt: run.recordedAt?.finishedAt ?? run.finishedAt ?? null,
    sourceIdentity: {
      commit: sourceCommit,
      proofSourceFingerprint: proofFingerprint,
      watchedTreeFingerprint: watchedFingerprint,
      watchedTreeFileCount: watchedFiles,
      currentBracketFingerprint: source.sha256,
    },
    apiBuildIdentity: apiIdentity,
    counts: {
      nativeAssertionsPassed: assertionCount,
      requiredChecksPassed: registeredChecks.length,
      traceActionCount: trace.actions.length,
      tlcGeneratedStates: receipt.tlc?.stateCount,
      tlcDistinctStates: receipt.tlc?.distinctStateCount,
      tlcDepth: receipt.tlc?.depth,
    },
    requiredActions,
    unsupportedNativeEvents: unsupported,
    artifacts,
  };
};

const historicalBasicBudgetRange = (root, sourceCommit, label) => {
  const pinned = blobAtTestedCommit(root, sourceCommit, JOURNEY_AUDIT);
  requireValue(pinned, label + ': historical budget audit source is unavailable at ' + sourceCommit + ':' + JOURNEY_AUDIT);
  const audit = jsonFromBytes(pinned.bytes, label + ' historical budget audit source');
  const journey = asArray(audit.journeys).find((entry) => entry.id === 'edit-remove-reload');
  requireValue(journey, label + ': historical budget audit source has no edit-remove-reload journey at ' + sourceCommit + ':' + JOURNEY_AUDIT);
  const range = journey.manualBasicBudgetProbe?.backendResponseRange;
  requireValue(Array.isArray(range) && range.length === 2 && range.every(Number.isInteger) && range[0] <= range[1], label + ': historical budget range is missing or malformed at ' + sourceCommit + ':' + JOURNEY_AUDIT);
  return { range, source: sourceCommit + ':' + JOURNEY_AUDIT };
};

const makeBudgetRow = (root, journey, runId, run) => {
  const label = 'retained run ' + runId;
  requireValue(run.result === 'pass' && run.manualOnly === true && run.automationCoverage === false, label + ': standalone Basic budget evidence must remain manual-only pass');
  const sourceCommit = sourceCommitFor(run);
  requireValue(isGitCommit(sourceCommit), label + ': source commit is missing or malformed');
  const proofFingerprint = proofFingerprintFor(run);
  requireValue(isSha256(proofFingerprint), label + ': proof-source fingerprint is missing or malformed');
  const historicalScope = requireHistoricalProofFingerprint(root, sourceCommit, proofFingerprint, label);
  const historicalRange = historicalBasicBudgetRange(root, sourceCommit, label);
  const refs = artifactRefsFor(run);
  const artifacts = {};
  const values = {};
  for (const [key, ref] of Object.entries(refs)) {
    const checked = checkedArtifact(root, ref, label + ' ' + key);
    artifacts[key] = { path: ref.path, sha256: checked.sha256 };
    values[key] = checked.value;
  }
  const report = values.report;
  const traceDocument = values.trace;
  const tla = values.tlaReport;
  requireValue(report && traceDocument && tla, label + ': budget run requires report, trace, and TLA artifacts');
  const expectedTraceName = journey.manualBasicBudgetProbe?.traceName;
  requireValue(report.result === 'pass' && report.playwrightStatus === 0 && report.tlaStatus === 'pass', label + ': budget report did not pass');
  requireValue(!Object.hasOwn(report, 'nativeCase') && !Object.hasOwn(run, 'nativeCase'), label + ': budget-only run must not claim a native case');
  requireValue(typeof expectedTraceName === 'string' && report.journey?.name === expectedTraceName && report.journey?.model === journey.model, label + ': budget journey identity/model mismatch');
  requireValue(traceDocument.sourceCommit === sourceCommit && tla.sourceCommit === sourceCommit, label + ': budget trace/TLA source commit differs from audit');
  requireValue(report.sourceFingerprint === proofFingerprint, label + ': budget report source fingerprint differs from audit');
  requireValue(report.sourceProfile === 'journey' && report.sourceCommit === sourceCommit, label + ': budget report source provenance differs from audit');
  requireValue(report.contractSourceCommit === traceDocument.contractSourceCommit, label + ': budget report contract source commit differs from retained trace');
  requireValue(journey.manualBasicBudgetProbe?.status === 'pass' && journey.manualBasicBudgetProbe.manualOnly === true && journey.manualBasicBudgetProbe.automationCoverage === false, label + ': top-level Basic budget boundary is not manual-only pass');
  requireValue(journey.manualBasicBudgetProbe.backendResponses === run.counts?.backendResponses, label + ': top-level Basic budget count differs from retained report');
  requireValue(journey.manualBasicBudgetProbe.sourceCommit === sourceCommit && journey.manualBasicBudgetProbe.proofSourceFingerprint === proofFingerprint, label + ': top-level Basic budget provenance differs from retained report');
  requireValue(traceDocument.sourceFingerprint === proofFingerprint && tla.sourceFingerprint === proofFingerprint, label + ': budget trace/TLA source fingerprints differ from audit');
  const { trace, receipt } = assertTlaReceipt(traceDocument, tla, sourceCommit, proofFingerprint, expectedTraceName, journey.model, run.formalBoundary?.unsupportedNativeEvents, label);
  requireValue(sameJson(report.journey.actions, trace.actions.map((action) => action.label)), label + ': budget journey actions differ from retained trace');
  requireValue(report.journey.backendCallCount === run.counts?.backendResponses, label + ': budget backend-call count differs from audit');
  const range = run.counts?.requiredRange;
  requireValue(Array.isArray(range) && range.length === 2 && Number.isInteger(range[0]) && Number.isInteger(range[1]), label + ': budget range is malformed');
  requireValue(sameJson(range, historicalRange.range), label + ': run budget range differs from historical audit source ' + historicalRange.source + ': expected ' + JSON.stringify(historicalRange.range) + ', found ' + JSON.stringify(range));
  requireValue(sameJson(journey.manualBasicBudgetProbe?.backendResponseRange, historicalRange.range), label + ': current audit budget range differs from historical audit source ' + historicalRange.source + ': expected ' + JSON.stringify(historicalRange.range) + ', found ' + JSON.stringify(journey.manualBasicBudgetProbe?.backendResponseRange));
  requireValue(report.journey.backendCallCount >= range[0] && report.journey.backendCallCount <= range[1], label + ': budget backend-call count ' + report.journey.backendCallCount + ' is outside ' + range[0] + '..' + range[1]);
  requireValue(run.formalBoundary?.tlaStatus === 'pass' && run.formalBoundary.model === journey.model, label + ': budget formal boundary does not identify the passed model');
  requireValue(run.counts?.traceActionCount === trace.actions.length, label + ': budget trace-action count differs from audit');
  requireValue(run.counts?.tlcGeneratedStates === receipt.tlc?.stateCount && run.counts?.tlcDistinctStates === receipt.tlc?.distinctStateCount && run.counts?.tlcDepth === receipt.tlc?.depth, label + ': budget TLC counts differ from retained TLA receipt');
  const freshness = currentProofFingerprint(root, historicalScope, proofFingerprint);
  const sourceStatus = freshness.status !== 'current'
    ? freshness
    : proofFingerprint === freshness.sha256
      ? { status: 'current', reason: 'retained journey proof-source fingerprint matches current pinned Builder source scope' }
      : { status: 'stale', reason: 'retained journey proof-source fingerprint ' + proofFingerprint + ' differs from current pinned Builder source fingerprint ' + freshness.sha256 };
  return {
    id: journey.id + ':' + runId + ':' + sourceCommit,
    runId,
    journey: journey.id,
    nativeCase: null,
    traceName: expectedTraceName,
    status: 'historical',
    sourceStatus: sourceStatus.status,
    sourceStatusReason: sourceStatus.reason,
    recordedAt: report.finishedAt ?? report.startedAt ?? null,
    sourceIdentity: { commit: sourceCommit, proofSourceFingerprint: proofFingerprint },
    budget: {
      backendResponses: report.journey.backendCallCount,
      requiredRange: range,
      rangeAuthority: historicalRange.source,
      passed: true,
    },
    model: journey.model,
    traceActionCount: trace.actions.length,
    unsupportedEvents: run.formalBoundary.unsupportedNativeEvents,
    artifacts,
  };
};

const historicalRecompileRow = (root, audit) => {
  const pin = asArray(audit.browserEvidenceInputs).find((input) => input.caseId === HISTORICAL_RECOMPILE_ID);
  requireValue(pin, 'audit is missing the pinned historical Recompile input');
  const source = pin.source;
  requireValue(isGitCommit(source?.commit), 'historical Recompile input commit must be a full Git SHA');
  requireValue(typeof source?.path === 'string' && !isAbsolute(source.path), 'historical Recompile input path must be repository-relative');
  pathWithin(ROOT, source.path, 'historical Recompile Git source');
  requireValue(typeof source?.gitBlobObjectId === 'string' && /^[0-9a-f]{40}$/.test(source.gitBlobObjectId), 'historical Recompile input must pin a full Git blob object ID');
  const trusted = gitBlob(ROOT, source.commit, source.path);
  requireValue(trusted, 'accepted historical Recompile Git blob is unavailable');
  requireValue(trusted.objectId === source.gitBlobObjectId, 'historical Recompile Git blob object ID mismatch: expected ' + source.gitBlobObjectId + ', found ' + trusted.objectId);
  const baseline = jsonFromBytes(trusted.bytes, 'accepted historical browser evidence Git blob');
  const oldCase = asArray(baseline.cases).find((entry) => entry.id === HISTORICAL_RECOMPILE_ID);
  requireValue(oldCase, 'accepted historical browser evidence has no ' + HISTORICAL_RECOMPILE_ID + ' case');
  const reportAbsolute = pathWithin(root, oldCase.report, 'historical Recompile report');
  const missing = !existsSync(reportAbsolute);
  return {
    id: oldCase.id,
    scenario: oldCase.scenario,
    case: oldCase.case,
    name: oldCase.name,
    status: 'unavailable',
    sourceStatus: 'unavailable',
    sourceStatusReason: missing
      ? 'the report path pinned by the accepted Git baseline is not retained: ' + oldCase.report
      : 'the accepted Git baseline preserves route metadata only; its browser report has no retained verification descriptor',
    coveredRoutes: oldCase.coveredRoutes,
    injectedRoutes: oldCase.injectedRoutes,
    historicalMetadata: {
      source: source.commit + ':' + source.path,
      gitBlobObjectId: trusted.objectId,
      recordedStatus: oldCase.status,
      recordedSourceFingerprint: oldCase.sourceFingerprint,
      recordedReport: oldCase.report,
      recordedReportSha256: oldCase.reportSha256,
      authority: 'unverified preservation metadata; excluded from current route coverage',
    },
  };
};

const validateManualCdaBoundary = (root, journey) => {
  const expected = journey.manualCdaJourney;
  requireValue(expected?.status === 'not-run' && expected.manualOnly === true && expected.automationCoverage === false, 'audit CDA boundary must remain manual-only and not-run');
  const manifest = readJson(root, expected.manifest, 'CDA journey manifest');
  const entry = asArray(manifest.journeys).find((candidate) => candidate.id === expected.id);
  requireValue(entry, 'CDA journey manifest is missing the retained manual case ' + expected.id);
  requireValue(entry.status === 'not-run' && entry.manualOnly === true && entry.automationCoverage === false, 'CDA journey manifest no longer marks the case manual-only and not-run');
  return { manifest: expected.manifest, id: expected.id, status: 'not-run', manualOnly: true, automationCoverage: false };
};

export async function buildBuilderBrowserEvidence({ root = ROOT, reportPaths = [], requireCurrent = false } = {}) {
  const repoRoot = resolve(root);
  const audit = readJson(repoRoot, JOURNEY_AUDIT, 'Builder journey audit');
  const journey = asArray(audit.journeys).find((entry) => entry.id === 'edit-remove-reload');
  requireValue(journey, 'Builder journey audit is missing edit-remove-reload');
  const evidence = journey.evidence ?? {};
  const currentWatched = (() => { try { return sourceFingerprint(repoRoot); } catch { return null; } })();
  const currentRegistry = reportPaths.length > 0 && currentWatched ? await currentRegistryForRoot(repoRoot) : null;
  const browser = await browserCaseRows(repoRoot, reportPaths, currentWatched, currentRegistry);
  const retainedJourneys = [];
  retainedJourneys.push(await makeRetainedNativeRow(repoRoot, journey, 'authored-three', evidence.nativeAuthoredThree, {
    scenario: 'builder-authoring', caseName: 'group-entry',
  }));
  retainedJourneys.push(await makeRetainedNativeRow(repoRoot, journey, 'raw-basic', evidence.native, journey.nativeCase && {
    scenario: journey.nativeCase.scenario, caseName: journey.nativeCase.case,
  }));
  retainedJourneys.push(await makeRetainedNativeRow(repoRoot, journey, 'raw-basic-alias-908', evidence.nativeRawBasicAlias908, journey.nativeCase && {
    scenario: journey.nativeCase.scenario, caseName: journey.nativeCase.case,
  }));
  retainedJourneys.push(makeBudgetRow(repoRoot, journey, 'basic-budget-28', evidence.manualBasicBudgetProbe908));
  const cdaBoundary = validateManualCdaBoundary(repoRoot, journey);
  const legacyRecompile = historicalRecompileRow(repoRoot, audit);
  const cases = [...browser.cases, legacyRecompile].sort((left, right) => left.id.localeCompare(right.id));
  if (requireCurrent) {
    const stale = [...cases, ...retainedJourneys].filter((entry) => entry.status !== 'current' || entry.sourceStatus !== 'current');
    requireValue(stale.length === 0, '--require-current rejected evidence row(s): ' + stale.map((entry) => entry.id + ' (' + entry.status + '/' + entry.sourceStatus + ': ' + entry.sourceStatusReason + ')').join('; '));
  }
  const retainedGeneratedFrom = [JOURNEY_AUDIT, CDA_JOURNEYS];
  for (const row of retainedJourneys) {
    for (const artifact of Object.values(row.artifacts)) retainedGeneratedFrom.push(artifact.path);
  }
  return {
    schemaVersion: 3,
    generatedFrom: browser.generatedFrom,
    retainedGeneratedFrom: [...new Set(retainedGeneratedFrom)].sort(),
    cases,
    retainedJourneys: retainedJourneys.sort((left, right) => left.id.localeCompare(right.id)),
    boundaries: {
      groupCancelAndReload: 'Native assertions only. BuilderJourney reports these exact events as unsupported.',
      currentRuntime: { status: 'unavailable', reason: 'No retained journey row proves a current runtime, and this generator does not claim a fresh browser run.' },
      cda: cdaBoundary,
    },
  };
}

const parseArgs = (argv) => {
  const reportPaths = [];
  let root = ROOT;
  let output = DEFAULT_OUTPUT;
  let check = false;
  let fromEvidence = false;
  let requireCurrent = false;
  let selfTest = false;
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--report') reportPaths.push(argv[++index]);
    else if (arg === '--root') root = argv[++index];
    else if (arg === '--output') output = argv[++index];
    else if (arg === '--check') check = true;
    else if (arg === '--from-evidence') fromEvidence = true;
    else if (arg === '--require-current') requireCurrent = true;
    else if (arg === '--self-test') selfTest = true;
    else fail('unknown argument: ' + arg);
  }
  requireValue(reportPaths.every((path) => typeof path === 'string' && path.length > 0), '--report requires a path');
  requireValue(typeof root === 'string' && root.length > 0, '--root requires a path');
  requireValue(typeof output === 'string' && output.length > 0, '--output requires a path');
  return { root: resolve(root), output, check, fromEvidence, reportPaths, requireCurrent, selfTest };
};

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.selfTest) {
    requireValue(options.reportPaths.length === 0 && !options.fromEvidence && !options.check && !options.requireCurrent, '--self-test cannot be combined with report, evidence, check, or freshness options');
    const payload = await buildBuilderBrowserEvidence({ root: options.root });
    requireValue(payload.retainedJourneys.length === 4, '--self-test expected four retained journey runs');
    requireValue(payload.cases.find((entry) => entry.id === HISTORICAL_RECOMPILE_ID)?.status === 'unavailable', '--self-test did not preserve the missing Recompile record as unavailable');
    const budget = payload.retainedJourneys.find((entry) => entry.runId === 'basic-budget-28');
    requireValue(budget?.budget?.passed === true && budget.budget.backendResponses >= budget.budget.requiredRange[0] && budget.budget.backendResponses <= budget.budget.requiredRange[1] && !Object.hasOwn(budget, 'assertions'), '--self-test budget-only run gained native assertions or failed its registered response range');
    console.log('PASS retained Builder journey evidence: 4 per-run results, unavailable Recompile, explicit CDA boundary');
    return;
  }
  const outputPath = isAbsolute(options.output) ? resolve(options.output) : pathWithin(options.root, options.output, 'output');
  let reportPaths = options.reportPaths;
  if (options.fromEvidence) {
    if (reportPaths.length > 0) fail('--from-evidence cannot be combined with --report');
    requireValue(existsSync(outputPath), options.output + ' does not exist; cannot read --from-evidence');
    const current = jsonFromBytes(readFileSync(outputPath), options.output);
    reportPaths = asArray(current.generatedFrom).filter((path) => {
      try { return existsSync(pathWithin(options.root, path, 'generated browser report')); }
      catch { return false; }
    });
  }
  const payload = await buildBuilderBrowserEvidence({ root: options.root, reportPaths, requireCurrent: options.requireCurrent });
  const serialized = JSON.stringify(payload, null, 2) + '\n';
  if (options.check) {
    requireValue(existsSync(outputPath), options.output + ' does not exist; run the generator without --check');
    requireValue(readFileSync(outputPath, 'utf8') === serialized, options.output + ' is stale; rerun the generator with the same arguments');
    console.log('PASS ' + options.output + ' is current: ' + payload.cases.length + ' browser case(s), ' + payload.retainedJourneys.length + ' retained journey run(s)');
  } else {
    writeFileSync(outputPath, serialized);
    console.log('wrote ' + options.output + ' with ' + payload.cases.length + ' browser case(s) and ' + payload.retainedJourneys.length + ' retained journey run(s)');
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    console.error('FAIL ' + (error instanceof Error ? error.message : String(error)));
    process.exitCode = 1;
  });
}
