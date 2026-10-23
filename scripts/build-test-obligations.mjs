#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { basename, relative, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const MODEL_DIR = 'verification/builder-interactions';
const LEDGER = MODEL_DIR + '/route-contract.json';
const TRACE_CONTRACT = MODEL_DIR + '/builder-trace-contract.json';
const SEMANTIC_CATALOG = MODEL_DIR + '/mjs-source-semantics.json';
const BROWSER_EVIDENCE = MODEL_DIR + '/browser-evidence.json';
const REGISTRY = 'scripts/verify-ui/registry.mjs';
const FORMAL_RUNNER = 'scripts/verify-builder-formal.mjs';
const UI_ROOT = 'ui/packages/loom-ui/src/features/ExplorerBuilder';
const GO_ROOT = 'internal';
const OUTPUT_JSON = MODEL_DIR + '/test-obligations.json';
const OUTPUT_MD = MODEL_DIR + '/test-obligations.md';

const args = process.argv.slice(2);
const jsonPath = valueFor('--json') ?? OUTPUT_JSON;
const markdownPath = valueFor('--markdown') ?? OUTPUT_MD;
const check = args.includes('--check');

function valueFor(name) {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
}

function fail(message) {
  console.error('FAIL ' + message);
  process.exit(1);
}

function readJson(path) {
  return JSON.parse(readFileSync(resolve(ROOT, path), 'utf8'));
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function walkFiles(root, predicate) {
  const absolute = resolve(ROOT, root);
  const files = [];
  const walk = (directory) => {
    for (const entry of readdirSync(directory)) {
      const path = resolve(directory, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (predicate(path)) files.push(path);
    }
  };
  walk(absolute);
  return files.sort();
}

function uniqueSorted(values) {
  return [...new Set(values)].sort();
}

function scriptRoleHint(path) {
  if (/\.(test|spec)\.mjs$/.test(path)) return path.endsWith('.spec.mjs') ? 'browser-spec' : 'test';
  if (/\.config\.mjs$/.test(path)) return 'config';
  if (path.includes('/helpers/') || path.includes('/lib/')) return 'helper-or-oracle';
  if (path.includes('/workflows/')) return 'browser-workflow';
  return 'tool-or-module';
}

function gitOutput(args, encoding = 'utf8') {
  const result = spawnSync('git', args, { cwd: ROOT, encoding, maxBuffer: 64 * 1024 * 1024 });
  if (result.status !== 0) fail('git ' + args.join(' ') + ' failed: ' + (result.stderr || result.error?.message));
  return result.stdout;
}

function trackedMjsPaths(commit = null) {
  const output = commit
    ? gitOutput(['ls-tree', '-r', '--name-only', '-z', commit])
    : gitOutput(['ls-files', '-z']);
  return output.split('\0').filter((path) => path.endsWith('.mjs')).sort();
}

function blobInfoAtCommit(commit, paths) {
  const infos = new Map();
  if (paths.length === 0) return infos;
  const input = Buffer.from(paths.map((path) => commit + ':' + path + '\n').join(''));
  const result = spawnSync('git', ['cat-file', '--batch'], {
    cwd: ROOT,
    input,
    encoding: null,
    maxBuffer: 64 * 1024 * 1024,
  });
  if (result.status !== 0) fail('cannot resolve reviewed catalog source: ' + (result.stderr?.toString('utf8') || result.error?.message));
  const output = result.stdout;
  let offset = 0;
  for (const path of paths) {
    const headerEnd = output.indexOf(0x0a, offset);
    if (headerEnd < 0) fail('git cat-file returned a malformed header for ' + path);
    const header = output.subarray(offset, headerEnd).toString('utf8');
    offset = headerEnd + 1;
    const match = header.match(/^[a-f0-9]+ blob (\d+)$/);
    if (!match) {
      infos.set(path, null);
      continue;
    }
    const size = Number(match[1]);
    const body = output.subarray(offset, offset + size);
    if (body.length !== size || output[offset + size] !== 0x0a) fail('git cat-file returned a malformed blob for ' + path);
    const lineCount = body.length === 0 ? 0
      : body.reduce((count, byte) => count + (byte === 0x0a ? 1 : 0), 0) + (body.at(-1) === 0x0a ? 0 : 1);
    infos.set(path, { sha256: createHash('sha256').update(body).digest('hex'), lineCount, sourceLines: body.toString('utf8').split(/\r?\n/) });
    offset += size + 1;
  }
  return infos;
}

function nonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function callArgumentsAt(lines, lineNumber, callPattern) {
  const index = lineNumber - 1;
  const startLine = lines[index] ?? '';
  const match = callPattern.exec(startLine);
  if (!match) return [];
  const excerpt = lines.slice(index, Math.min(lines.length, index + 12)).join('\n');
  const openParen = startLine.indexOf('(', match.index);
  const args = [];
  let start = openParen + 1;
  let parens = 1;
  let brackets = 0;
  let braces = 0;
  let quote = null;
  let escaped = false;
  for (let cursor = start; cursor < excerpt.length; cursor++) {
    const char = excerpt[cursor];
    if (quote) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === "'" || char === '"' || char === '`') quote = char;
    else if (char === '(') parens++;
    else if (char === ')') {
      if (parens === 1) {
        args.push(excerpt.slice(start, cursor).trim());
        return args;
      }
      parens--;
    } else if (char === '[') brackets++;
    else if (char === ']') brackets--;
    else if (char === '{') braces++;
    else if (char === '}') braces--;
    else if (char === ',' && parens === 1 && brackets === 0 && braces === 0) {
      args.push(excerpt.slice(start, cursor).trim());
      start = cursor + 1;
    }
  }
  return args;
}

function diagnosticReporterSummary(lines) {
  const source = lines.join('\n');
  let recordCheckStatusIndex = null;
  if (/\bimport\s*\{[^}]*\brecordCheck\b[^}]*\}\s*from\s*['"][^'"]*report\.mjs['"]/.test(source)) {
    recordCheckStatusIndex = 3;
  } else {
    const declaration = source.match(/\b(?:const|let|var)\s+recordCheck\s*=\s*(?:async\s*)?\(([^)]*)\)\s*=>|\bfunction\s+recordCheck\s*\(([^)]*)\)/);
    const parameters = declaration?.[1] ?? declaration?.[2];
    if (parameters !== undefined) {
      const statusIndex = parameters.split(',').map((parameter) => parameter.trim().split('=')[0].trim())
        .findIndex((parameter) => /^(passed|success|condition|check|ok|valid|isValid|result|didPass)$/i.test(parameter));
      if (statusIndex >= 0) recordCheckStatusIndex = statusIndex;
    }
  }
  const directPassedReporter = /\b(?:const|let)\s+recordAssertion\s*=\s*\([^)]*\)\s*=>\s*report\.assertions\.push\s*\(\s*\{[^}]*\bstatus\s*:\s*['"]passed['"][^}]*\}\s*\)/.test(source);
  const cdaPassedReporter = /\b(?:const|let)\s+recordAssertion\s*=\s*\([^)]*\)\s*=>\s*\{\s*cda\.check\s*\([^)]*,\s*true\s*,[^)]*\)\s*;\s*report\.assertions\.push\s*\(\s*\{[^}]*\bstatus\s*:\s*['"]passed['"][^}]*\}\s*\)\s*;\s*\}/.test(source);
  return {
    recordCheckStatusIndex,
    unconditionalPassedRecordAssertion: directPassedReporter || cdaPassedReporter,
    unconditionalTrueRecordCheck: /\b(?:const|let)\s+recordCheck\s*=\s*\([^)]*\)\s*=>\s*cda\.check\s*\([^)]*,\s*true\s*,/.test(source),
  };
}

function isDiagnosticAssertionRecorder(lines, lineNumber, reporterSummary) {
  const cdaCheckArgs = callArgumentsAt(lines, lineNumber, /\bcda\.check\s*\(/);
  if (cdaCheckArgs[2] === 'true') return true;
  const recordCheckArgs = callArgumentsAt(lines, lineNumber, /\brecordCheck\s*\(/);
  if (reporterSummary.recordCheckStatusIndex !== null
    && recordCheckArgs[reporterSummary.recordCheckStatusIndex] === 'true') return true;
  if (reporterSummary.unconditionalPassedRecordAssertion && /\brecordAssertion\s*\(/.test(lines[lineNumber - 1] ?? '')) return true;
  return reporterSummary.unconditionalTrueRecordCheck && /\brecordCheck\s*\(/.test(lines[lineNumber - 1] ?? '');
}

function buildRegistryCaseIndex(registry) {
  const cases = new Map();
  const requiredChecks = (value) => {
    if (Array.isArray(value)) return value.filter(nonEmptyString);
    if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
    return ['owned', 'custom'].flatMap((kind) =>
      Array.isArray(value[kind]) ? value[kind].filter(nonEmptyString) : []);
  };
  for (const scenario of registry) {
    for (const [caseName, caseData] of Object.entries(scenario.cases ?? {})) {
      cases.set(scenario.id + ':' + caseName, {
        requiredChecks: new Set(requiredChecks(caseData.requiredChecks)),
        focusedCheckIds: new Set((caseData.focusedChecks ?? []).map((check) => check.id).filter(Boolean)),
      });
    }
  }
  return cases;
}

function buildJourneyAssemblyRows({ claimMappingsByCase, sourceFilesByClaim, scriptRowsByPath }) {
  return [...claimMappingsByCase.entries()].map(([caseId, mappings]) => {
    const claimsByRelation = {
      direct: new Set(),
      partial: new Set(),
      supporting: new Set(),
    };
    const sourcePaths = new Set();
    const checkRefs = new Set();
    const candidateModels = new Set();
    for (const mapping of mappings) {
      claimsByRelation[mapping.relation]?.add(mapping.claimId);
      for (const check of mapping.requiredChecks ?? []) checkRefs.add('required:' + check);
      for (const checkId of mapping.focusedCheckIds ?? []) checkRefs.add('focused:' + checkId);
      for (const source of sourceFilesByClaim.get(mapping.claimId) ?? []) {
        sourcePaths.add(source.path);
        const file = scriptRowsByPath.get(source.path);
        if (file?.formalApplicability?.kind !== 'candidate') continue;
        for (const binding of file.formalApplicability.bindings ?? []) {
          if (nonEmptyString(binding.model)) candidateModels.add(binding.model);
        }
      }
    }
    return {
      caseId,
      sourceFileCount: sourcePaths.size,
      directClaims: claimsByRelation.direct.size,
      partialClaims: claimsByRelation.partial.size,
      supportingClaims: claimsByRelation.supporting.size,
      registeredCheckCount: checkRefs.size,
      candidateFormalModels: uniqueSorted([...candidateModels]),
    };
  }).sort((left, right) => left.caseId.localeCompare(right.caseId));
}

function catalogErrors(catalog, {
  currentPaths,
  currentHashes,
  baselinePaths,
  baselineInventorySha256,
  reviewedBlobInfos,
  caseIndex,
  models,
  traceContract,
  journeyAudit,
}) {
  const errors = [];
  const error = (message) => errors.push(message);
  const isRecord = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
  const asArray = (value) => Array.isArray(value) ? value : [];
  const evidenceLineCounts = new Map([...reviewedBlobInfos].map(([path, info]) => [path, info?.lineCount]));
  const reporterSummaries = new Map([...reviewedBlobInfos].map(([path, info]) => [path, diagnosticReporterSummary(info?.sourceLines ?? [])]));
  const validateEvidence = (anchors, label, required = false) => {
    if (!Array.isArray(anchors)) {
      error(required ? label + ' needs at least one source anchor and must be an array' : label + ' must be an array');
      return;
    }
    if (required && anchors.length === 0) error(label + ' needs at least one source anchor');
    for (const anchor of anchors) {
      if (!isRecord(anchor) || !nonEmptyString(anchor.path) || !Number.isInteger(anchor.line)
        || anchor.line < 1 || !nonEmptyString(anchor.kind)) {
        error(label + ' entries need repository path, positive line, and kind');
        continue;
      }
      const fullPath = resolve(ROOT, anchor.path);
      const pathFromRoot = relative(ROOT, fullPath);
      if (pathFromRoot === '..' || pathFromRoot.startsWith('../') || pathFromRoot.startsWith('..\\')) {
        error(label + ' path escapes the repository: ' + anchor.path);
        continue;
      }
      let lineCount = evidenceLineCounts.get(anchor.path);
      if (lineCount === undefined) {
        try {
          const text = readFileSync(fullPath, 'utf8');
          lineCount = text.length === 0 ? 0 : text.split(/\r?\n/).length - (text.endsWith('\n') ? 1 : 0);
          evidenceLineCounts.set(anchor.path, lineCount);
        } catch {
          error(label + ' references unavailable source: ' + anchor.path);
          continue;
        }
      }
      if (anchor.line > lineCount) error(label + ' line is outside reviewed source: ' + anchor.path + ':' + anchor.line);
    }
  };
  const activePaths = new Set();
  const retiredPaths = new Set();
  const filesByPath = new Map();
  const claimsById = new Map();
  const sourceAssertionIds = new Set();
  const roles = new Set(['test', 'helper-or-oracle', 'browser-workflow', 'browser-spec', 'tool-or-module', 'config']);
  const testLayers = new Set(['browser-workflow', 'browser-spec', 'node-test', 'test-support', 'command-line', 'runtime-module', 'configuration']);
  const evidenceKinds = new Set(['test-assertion', 'runtime-guard', 'oracle-computation', 'fixture-fact', 'call-through', 'trace-capture']);

  if (!isRecord(catalog) || catalog.schemaVersion !== 1) return ['catalog schemaVersion must be 1'];
  if (!nonEmptyString(catalog.sourceCommit)) error('catalog sourceCommit is required');
  if (nonEmptyString(catalog.sourceCommit) && !/^[a-f0-9]{40}$/.test(catalog.sourceCommit)) error('catalog sourceCommit must be a full commit hash');
  if (!/^[a-f0-9]{64}$/.test(catalog.inventorySha256 ?? '')) error('catalog inventorySha256 must be a 64-character lowercase SHA-256');
  if (!/^[a-f0-9]{64}$/.test(baselineInventorySha256 ?? '')) error('baseline inventory artifact SHA-256 could not be resolved');
  else if (catalog.inventorySha256 !== baselineInventorySha256) error('catalog inventorySha256 does not match the baseline inventory artifact');
  for (const forbidden of ['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness']) {
    if (Object.hasOwn(catalog, forbidden)) error('catalog cannot author runtime field ' + forbidden);
  }
  if (!Array.isArray(catalog.claims)) error('catalog claims must be an array');
  if (!Array.isArray(catalog.files)) error('catalog files must be an array');
  if (!Array.isArray(catalog.claims) || !Array.isArray(catalog.files)) return errors;

  for (const claim of catalog.claims) {
    if (!isRecord(claim) || !nonEmptyString(claim.id) || !nonEmptyString(claim.statement)) {
      error('each claim needs a stable id and statement');
      continue;
    }
    if (claim.id.startsWith('source:') || /:L\d+:/.test(claim.id)) error('semantic claim ID cannot be a source-line occurrence ID: ' + claim.id);
    if (claimsById.has(claim.id)) error('duplicate/conflicting semantic claim definition: ' + claim.id);
    else claimsById.set(claim.id, claim);
    if (!['behavior', 'invariant', 'oracle', 'tool-contract'].includes(claim.category)) {
      error('claim ' + claim.id + ' has unknown category ' + String(claim.category));
    }
    if (!Array.isArray(claim.journeys)) error('claim ' + claim.id + ' journeys must be an array');
    const claimCaseIds = new Set();
    for (const mapping of asArray(claim.journeys)) {
      if (!isRecord(mapping) || !nonEmptyString(mapping.caseId)) {
        error('claim ' + claim.id + ' has a malformed journey mapping');
        continue;
      }
      if (['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness'].some((key) => Object.hasOwn(mapping, key))) {
        error('claim ' + claim.id + ' journey mapping cannot author runtime status');
      }
      const registeredCase = caseIndex.get(mapping.caseId);
      if (!registeredCase) {
        error('claim ' + claim.id + ' references unknown journey/case ' + mapping.caseId);
        continue;
      }
      if (!['direct', 'partial', 'supporting'].includes(mapping.relation)) {
        error('claim ' + claim.id + ' has unknown relation for ' + mapping.caseId);
      }
      if (claimCaseIds.has(mapping.caseId)) error('claim ' + claim.id + ' duplicates journey mapping ' + mapping.caseId);
      claimCaseIds.add(mapping.caseId);
      if (mapping.requiredChecks !== undefined && !Array.isArray(mapping.requiredChecks)) error('claim ' + claim.id + ' requiredChecks must be an array');
      if (mapping.focusedCheckIds !== undefined && !Array.isArray(mapping.focusedCheckIds)) error('claim ' + claim.id + ' focusedCheckIds must be an array');
      if (['direct', 'partial'].includes(mapping.relation)
        && (mapping.requiredChecks ?? []).length + (mapping.focusedCheckIds ?? []).length === 0) {
        error('claim ' + claim.id + ' direct/partial journey mapping needs an exact registered check: ' + mapping.caseId);
      }
      for (const check of asArray(mapping.requiredChecks)) {
        if (!registeredCase.requiredChecks.has(check)) error('claim ' + claim.id + ' references unknown required check in ' + mapping.caseId + ': ' + check);
      }
      for (const checkId of asArray(mapping.focusedCheckIds)) {
        if (!registeredCase.focusedCheckIds.has(checkId)) error('claim ' + claim.id + ' references unknown focused check in ' + mapping.caseId + ': ' + checkId);
      }
      validateEvidence(mapping.evidence, 'claim ' + claim.id + ' journey evidence for ' + mapping.caseId, true);
    }
    for (const forbidden of ['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness']) {
      if (Object.hasOwn(claim, forbidden)) error('claim ' + claim.id + ' cannot author runtime field ' + forbidden);
    }
  }

  for (const file of catalog.files) {
    if (!isRecord(file) || !nonEmptyString(file.path)) {
      error('each catalog file needs a repository-relative path');
      continue;
    }
    if (!file.path.endsWith('.mjs')) error('catalog path is not an MJS file: ' + file.path);
    if (filesByPath.has(file.path)) error('duplicate catalog file record: ' + file.path);
    else filesByPath.set(file.path, file);
    if (!['active', 'retired'].includes(file.lifecycle)) error('catalog file has invalid lifecycle: ' + file.path);
    if (file.lifecycle === 'active') activePaths.add(file.path);
    if (file.lifecycle === 'retired') retiredPaths.add(file.path);
    if (!nonEmptyString(file.sha256) || !/^[a-f0-9]{64}$/.test(file.sha256)) error('catalog file needs a SHA-256: ' + file.path);
    if (!nonEmptyString(file.purpose)) error('catalog file needs a concrete purpose: ' + file.path);
    if (!roles.has(file.role)) error('catalog file has unknown role ' + String(file.role) + ': ' + file.path);
    if (!testLayers.has(file.testLayer)) error('catalog file has unknown test layer ' + String(file.testLayer) + ': ' + file.path);
    if (!isRecord(file.sourceReview) || file.sourceReview.status !== 'reviewed'
      || !Array.isArray(file.sourceReview.evidence) || file.sourceReview.evidence.length === 0) {
      error('catalog file needs reviewed source evidence: ' + file.path);
    }
    validateEvidence(file.sourceReview?.evidence, 'sourceReview evidence for ' + file.path, true);
    if (file.sourceReview?.reviewedCommit !== undefined && !/^[a-f0-9]{40}$/.test(file.sourceReview.reviewedCommit)) {
      error('sourceReview reviewedCommit must be a full commit hash: ' + file.path);
    }
    if (!['high', 'medium', 'low'].includes(file.sourceReview?.confidence)) {
      error('sourceReview has unknown confidence: ' + file.path);
    }
    if (!isRecord(file.executionDiscovery)
      || !['static-path-found', 'no-static-path-found', 'uncertain-dynamic-or-manual'].includes(file.executionDiscovery.status)) {
      error('catalog file needs an explicit invocation-discovery state: ' + file.path);
    }
    if (file.executionDiscovery && !Array.isArray(file.executionDiscovery.evidence)) error('executionDiscovery evidence must be an array: ' + file.path);
    validateEvidence(file.executionDiscovery?.evidence, 'executionDiscovery evidence for ' + file.path);
    if (file.executionDiscovery && !Array.isArray(file.executionDiscovery.unresolvedFacts)) error('executionDiscovery unresolvedFacts must be an array: ' + file.path);
    if (file.executionDiscovery?.status === 'static-path-found' && (file.executionDiscovery.evidence ?? []).length === 0) {
      error('static invocation finding needs a source anchor: ' + file.path);
    }
    if (['no-static-path-found', 'uncertain-dynamic-or-manual'].includes(file.executionDiscovery?.status)
      && (!Array.isArray(file.executionDiscovery.unresolvedFacts) || file.executionDiscovery.unresolvedFacts.length === 0)) {
      error('missing static invocation requires an explicit dynamic/manual uncertainty: ' + file.path);
    }
    if (!Array.isArray(file.assertions)) error('catalog assertions must be an array: ' + file.path);
    if (!Array.isArray(file.unresolvedFacts)) error('catalog unresolvedFacts must be an array: ' + file.path);
    for (const assertion of asArray(file.assertions)) {
      if (!isRecord(assertion) || !nonEmptyString(assertion.sourceAssertionId)
        || !nonEmptyString(assertion.semanticClaimId)) {
        error('catalog assertion needs sourceAssertionId and semanticClaimId: ' + file.path);
        continue;
      }
      if (!claimsById.has(assertion.semanticClaimId)) error('undefined semantic claim ' + assertion.semanticClaimId + ' in ' + file.path);
      if (sourceAssertionIds.has(assertion.sourceAssertionId)) error('duplicate source assertion occurrence: ' + assertion.sourceAssertionId);
      sourceAssertionIds.add(assertion.sourceAssertionId);
      const occurrencePrefix = 'source:' + file.path + ':L';
      if (!assertion.sourceAssertionId.startsWith(occurrencePrefix)) error('sourceAssertionId must identify this file and line: ' + assertion.sourceAssertionId);
      const occurrenceLine = Number(assertion.sourceAssertionId.slice(occurrencePrefix.length).split(':', 1)[0]);
      if (!Number.isInteger(occurrenceLine) || !asArray(assertion.sourceLines).includes(occurrenceLine)) {
        error('sourceAssertionId line must appear in sourceLines: ' + assertion.sourceAssertionId);
      }
      if (!evidenceKinds.has(assertion.evidenceKind)) error('assertion has unknown evidence kind ' + String(assertion.evidenceKind) + ': ' + assertion.sourceAssertionId);
      if (!Array.isArray(assertion.sourceLines) || assertion.sourceLines.some((line) => !Number.isInteger(line) || line < 1)) {
        error('assertion needs positive source line anchors: ' + assertion.sourceAssertionId);
      }
      const reviewedInfo = reviewedBlobInfos.get(file.path);
      if (Array.isArray(assertion.sourceLines) && reviewedInfo
        && assertion.sourceLines.some((line) => line > reviewedInfo.lineCount)) {
        error('assertion line anchor is outside reviewed source revision: ' + assertion.sourceAssertionId);
      }
      if (assertion.evidenceKind === 'test-assertion' && reviewedInfo?.sourceLines
        && asArray(assertion.sourceLines).some((line) => isDiagnosticAssertionRecorder(reviewedInfo.sourceLines, line, reporterSummaries.get(file.path)))) {
        error('test-assertion points to a diagnostic check/recordCheck/recordAssertion reporter rather than its underlying assertion: ' + assertion.sourceAssertionId);
      }
      if (!['independent', 'shared', 'derived'].includes(assertion.independence)) {
        error('assertion has unknown independence kind: ' + assertion.sourceAssertionId);
      }
      for (const forbidden of ['claim', 'statement', 'category', 'status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness']) {
        if (Object.hasOwn(assertion, forbidden)) error('assertion ' + assertion.sourceAssertionId + ' cannot duplicate claim or runtime field ' + forbidden);
      }
    }
    if (!isRecord(file.formalApplicability) || !nonEmptyString(file.formalApplicability.kind)) {
      error('catalog file needs an explicit formal applicability decision: ' + file.path);
    }
    validateEvidence(file.formalApplicability?.evidence, 'formalApplicability evidence for ' + file.path, true);
    if (file.formalApplicability && !['direct-trace', 'trace-support', 'candidate', 'not-applicable', 'uncertain'].includes(file.formalApplicability.kind)) {
      error('catalog file has unknown formal applicability kind: ' + file.path);
    }
    if (file.formalApplicability?.kind === 'candidate' && asArray(file.formalApplicability.bindings).length === 0
      && (!nonEmptyString(file.formalApplicability.rationale) || !nonEmptyString(file.formalApplicability.limitation))) {
      error('unmapped formal candidate needs a concrete rationale and gap: ' + file.path);
    }
    if (['direct-trace', 'trace-support'].includes(file.formalApplicability?.kind)
      && (!Array.isArray(file.formalApplicability.bindings) || file.formalApplicability.bindings.length === 0)) {
      error('direct formal trace association needs a model binding: ' + file.path);
    }
    if (file.formalApplicability && !nonEmptyString(file.formalApplicability.rationale)) {
      error('formal applicability needs a concrete rationale: ' + file.path);
    }
    if (file.formalApplicability && !nonEmptyString(file.formalApplicability.limitation)) {
      error('formal applicability needs an explicit limitation: ' + file.path);
    }
    if (file.formalApplicability?.bindings !== undefined && !Array.isArray(file.formalApplicability.bindings)) {
      error('formal applicability bindings must be an array: ' + file.path);
    }
    for (const binding of asArray(file.formalApplicability?.bindings)) {
      if (!isRecord(binding) || !nonEmptyString(binding.model)) {
        error('formal binding needs a model id: ' + file.path);
        continue;
      }
      if (!models.has(binding.model)) error('unknown formal model ' + binding.model + ' in ' + file.path);
      const definition = models.get(binding.model);
      for (const field of ['actions', 'invariants', 'scheduleIds', 'journeyIds']) {
        if (binding[field] !== undefined && !Array.isArray(binding[field])) error('formal binding ' + field + ' must be an array: ' + file.path);
      }
      for (const action of asArray(binding.actions)) {
        const baseAction = String(action).replace(/\(.*\)$/, '');
        if (!definition?.actions.includes(action) && !definition?.actions.includes(baseAction)) error('unknown formal action ' + binding.model + '.' + action + ' in ' + file.path);
      }
      for (const invariant of asArray(binding.invariants)) {
        if (!definition?.invariants.includes(invariant)) error('unknown formal invariant ' + binding.model + '.' + invariant + ' in ' + file.path);
      }
      for (const scheduleId of asArray(binding.scheduleIds)) {
        if (!Object.hasOwn(traceContract.schedules ?? {}, scheduleId)) {
          error('unknown formal schedule ' + scheduleId + ' in ' + file.path);
        } else if (traceContract.schedules[scheduleId].model !== binding.model) {
          error('formal schedule ' + scheduleId + ' belongs to a different model in ' + file.path);
        }
      }
      for (const journeyId of asArray(binding.journeyIds)) {
        if (!(journeyAudit.journeys ?? []).some((journey) => journey.id === journeyId && journey.model === binding.model)) {
          error('unknown formal journey trace ' + journeyId + ' for ' + binding.model + ' in ' + file.path);
        }
      }
    }
    if (!isRecord(file.disposition) || !['keep', 'integrate', 'merge-candidate', 'retire-candidate', 'unresolved'].includes(file.disposition.kind)
      || !nonEmptyString(file.disposition.reason)) {
      error('catalog file needs a concrete replacement disposition and reason: ' + file.path);
    }
    if (file.disposition && Object.hasOwn(file.disposition, 'gap')) {
      error('replacement-proof gap must be recorded only at replacementProof.gap: ' + file.path);
    }
    if (!isRecord(file.replacementProof) || !Array.isArray(file.replacementProof.paths)
      || !Array.isArray(file.replacementProof.evidence) || typeof file.replacementProof.gap !== 'string') {
      error('catalog file needs replacementProof with its canonical gap field: ' + file.path);
    } else if (Object.hasOwn(file.replacementProof, 'proofNeeded')) {
      error('replacementProof cannot duplicate its canonical gap in proofNeeded: ' + file.path);
    }
    for (const replacementPath of asArray(file.replacementProof?.paths)) {
      if (!nonEmptyString(replacementPath)) error('replacementProof paths must be nonempty source paths: ' + file.path);
    }
    for (const proof of asArray(file.replacementProof?.evidence)) {
      if (!isRecord(proof) || !nonEmptyString(proof.path) || !/^[a-f0-9]{64}$/.test(proof.sha256)
        || !nonEmptyString(proof.kind)) {
        error('replacementProof evidence needs path, SHA-256, and kind: ' + file.path);
      }
    }
    if (file.formalApplicability && ['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness'].some((key) => Object.hasOwn(file.formalApplicability, key))) {
      error('formal applicability cannot author runtime status: ' + file.path);
    }
    if (file.disposition && ['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness'].some((key) => Object.hasOwn(file.disposition, key))) {
      error('replacement disposition cannot author runtime status: ' + file.path);
    }
    if (file.replacementProof && ['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness'].some((key) => Object.hasOwn(file.replacementProof, key))) {
      error('replacement proof cannot author runtime status: ' + file.path);
    }
    if (file.oracle !== undefined && file.oracle !== null) {
      if (!isRecord(file.oracle) || !nonEmptyString(file.oracle.kind) || !nonEmptyString(file.oracle.dataScope)
        || !Array.isArray(file.oracle.evidence) || file.oracle.evidence.length === 0) {
        error('file oracle needs kind, dataScope, and source evidence: ' + file.path);
      }
      validateEvidence(file.oracle?.evidence, 'oracle evidence for ' + file.path, true);
    }
    const reviewedInfo = reviewedBlobInfos.get(file.path);
    if (!reviewedInfo) error('reviewed source revision does not contain ' + file.path);
    else if (file.sha256 !== reviewedInfo.sha256) error('catalog SHA-256 does not match reviewed source revision for ' + file.path);
    if (file.lifecycle === 'retired' && file.disposition?.kind !== 'integrate') error('retired file lifecycle needs an integrated disposition: ' + file.path);
    if (file.lifecycle === 'retired' && file.replacementProof?.gap !== '') error('retired file lifecycle requires a closed replacement-proof gap: ' + file.path);
    if (file.duplicateCandidates !== undefined && (!Array.isArray(file.duplicateCandidates)
      || file.duplicateCandidates.some((candidate) => !nonEmptyString(candidate)))) {
      error('duplicateCandidates must contain only repository-relative MJS paths: ' + file.path);
    }
    if (asArray(file.sourceReview?.evidence).length && !asArray(file.sourceReview.evidence).some((anchor) => anchor.path === file.path)) {
      error('source review needs an anchor in the reviewed MJS file: ' + file.path);
    }
    for (const forbidden of ['status', 'runtimeStatus', 'runtimeEvidence', 'passed', 'result', 'freshness']) {
      if (Object.hasOwn(file, forbidden)) error('catalog file cannot author runtime field ' + forbidden + ': ' + file.path);
    }
  }

  for (const file of catalog.files.filter((entry) => isRecord(entry) && nonEmptyString(entry.path))) {
    const candidates = asArray(file.duplicateCandidates);
    if (new Set(candidates).size !== candidates.length) error('duplicateCandidates repeats a path: ' + file.path);
    for (const candidate of candidates) {
      if (candidate === file.path || !candidate.endsWith('.mjs') || !filesByPath.has(candidate)) {
        error('duplicateCandidates must reference a different catalogued MJS path: ' + file.path + ' -> ' + candidate);
      }
    }
  }

  for (const path of currentPaths) {
    const file = filesByPath.get(path);
    if (!file || file.lifecycle !== 'active') error('tracked MJS lacks one active catalog record: ' + path);
    else if (file.sha256 !== currentHashes.get(path)) error('catalog SHA-256 is stale for tracked MJS: ' + path);
  }
  for (const path of activePaths) {
    if (!currentPaths.has(path)) error('active catalog record is not currently tracked: ' + path);
  }
  for (const path of baselinePaths) {
    const file = filesByPath.get(path);
    if (!file) error('baseline MJS has no catalog or retirement record: ' + path);
  }
  for (const path of retiredPaths) {
    const file = filesByPath.get(path);
    if (currentPaths.has(path)) error('retired catalog record is still tracked: ' + path);
    if (!baselinePaths.has(path) && !file?.sourceReview?.reviewedCommit) error('retired catalog record needs a reviewed historical source revision: ' + path);
    if (!isRecord(file?.replacementProof) || !Array.isArray(file.replacementProof.paths)
      || file.replacementProof.paths.length === 0 || !Array.isArray(file.replacementProof.evidence)
      || file.replacementProof.evidence.length === 0) {
      error('retired catalog record needs replacement paths and proof references: ' + path);
    }
  }
  return errors;
}

function selfTestCatalogValidation() {
  const assert = (condition, message) => {
    if (!condition) fail('catalog self-test: ' + message);
  };
  const sourcePath = 'scripts/example.test.mjs';
  const sourceCommit = 'b'.repeat(40);
  const sourceHash = 'a'.repeat(64);
  const nestedRegistryCase = buildRegistryCaseIndex([{
    id: 'example',
    cases: {
      case: {
        requiredChecks: {
          owned: ['shows source rows'],
          custom: ['shows custom rows'],
        },
        focusedChecks: [{ id: 'row-oracle' }],
      },
    },
  }]);
  assert(nestedRegistryCase.get('example:case')?.requiredChecks.has('shows source rows')
    && nestedRegistryCase.get('example:case')?.requiredChecks.has('shows custom rows'),
  'registry case index should include known owned/custom required-check variants');
  const assemblyRows = buildJourneyAssemblyRows({
    claimMappingsByCase: new Map([['example:case', [
      { claimId: 'example.direct', relation: 'direct', requiredChecks: ['shows source rows'], focusedCheckIds: ['row-oracle'] },
      { claimId: 'example.partial', relation: 'partial', requiredChecks: ['shows source rows', 'shows custom rows'], focusedCheckIds: ['row-oracle'] },
      { claimId: 'example.supporting', relation: 'supporting', requiredChecks: [], focusedCheckIds: [] },
    ]]]),
    sourceFilesByClaim: new Map([
      ['example.direct', [{ path: 'scripts/example.test.mjs' }]],
      ['example.partial', [{ path: 'scripts/example.test.mjs' }, { path: 'scripts/other.test.mjs' }]],
      ['example.supporting', [{ path: 'scripts/other.test.mjs' }]],
    ]),
    scriptRowsByPath: new Map([
      ['scripts/example.test.mjs', { formalApplicability: { kind: 'candidate', bindings: [{ model: 'ExampleModel' }] } }],
      ['scripts/other.test.mjs', { formalApplicability: { kind: 'not-applicable', bindings: [] } }],
    ]),
  });
  assert(assemblyRows.length === 1 && assemblyRows[0].sourceFileCount === 2
    && assemblyRows[0].directClaims === 1 && assemblyRows[0].partialClaims === 1
    && assemblyRows[0].supportingClaims === 1 && assemblyRows[0].registeredCheckCount === 3
    && assemblyRows[0].candidateFormalModels.join(',') === 'ExampleModel',
  'journey assembly summary should deduplicate source files/check refs and count claims by relation');
  console.log('PASS journey assembly summary self-test');
  const context = {
    currentPaths: new Set([sourcePath]),
    currentHashes: new Map([[sourcePath, sourceHash]]),
    baselinePaths: new Set([sourcePath]),
    baselineInventorySha256: 'c'.repeat(64),
    reviewedBlobInfos: new Map([[sourcePath, { sha256: sourceHash, lineCount: 2, sourceLines: ['assert.deepEqual(actual, expected);', 'cda.check(\'correctness\', \'status label\', true, evidence);'] }]]),
    caseIndex: nestedRegistryCase,
    models: new Map([['ExampleModel', { actions: ['LoadRows'], invariants: ['RowsPreserved'] }]]),
    traceContract: { schedules: { 'load-rows': { model: 'ExampleModel' } } },
    journeyAudit: { journeys: [{ id: 'load-preview', model: 'ExampleModel' }] },
  };
  const validCatalog = () => ({
    schemaVersion: 1,
    sourceCommit,
    inventorySha256: 'c'.repeat(64),
    claims: [{
      id: 'example.rows-preserved',
      statement: 'The loaded row set matches the independent fixture oracle.',
      category: 'behavior',
      journeys: [{
        caseId: 'example:case',
        relation: 'direct',
        requiredChecks: ['shows source rows', 'shows custom rows'],
        focusedCheckIds: ['row-oracle'],
        evidence: [{ path: sourcePath, line: 1, kind: 'registered journey check' }],
      }],
    }],
    files: [{
      path: sourcePath,
      lifecycle: 'active',
      sha256: sourceHash,
      purpose: 'Runs the fixture-backed row rendering check.',
      role: 'test',
      testLayer: 'node-test',
      sourceReview: { status: 'reviewed', reviewedCommit: sourceCommit, confidence: 'high', evidence: [{ path: sourcePath, line: 1, kind: 'test assertion' }] },
      executionDiscovery: { status: 'static-path-found', evidence: [{ path: 'scripts/package.json', line: 1, kind: 'registered test command' }], unresolvedFacts: [] },
      assertions: [{
        sourceAssertionId: 'source:scripts/example.test.mjs:L1:rows-match',
        semanticClaimId: 'example.rows-preserved',
        evidenceKind: 'test-assertion',
        sourceLines: [1],
        independence: 'independent',
      }],
      formalApplicability: {
        kind: 'direct-trace',
        rationale: 'The captured load journey is checked against the row-preservation invariant.',
        evidence: [{ path: sourcePath, line: 1, kind: 'checks captured load trace' }],
        limitation: 'The TLA model checks row presence, not exact cell values.',
        bindings: [{ model: 'ExampleModel', actions: ['LoadRows'], invariants: ['RowsPreserved'], scheduleIds: ['load-rows'], journeyIds: ['load-preview'] }],
      },
      disposition: { kind: 'keep', reason: 'Owns the independent source-row oracle.' },
      replacementProof: { paths: [], evidence: [], gap: '' },
      unresolvedFacts: [],
    }],
  });
  const errorsFor = (catalog, overrides = {}) => catalogErrors(catalog, { ...context, ...overrides });
  const expectError = (label, catalog, fragment, overrides = {}) => {
    const errors = errorsFor(catalog, overrides);
    assert(errors.some((message) => message.includes(fragment)), label + ' should reject with ' + fragment + '; got ' + errors.join(' | '));
  };

  const validErrors = errorsFor(validCatalog());
  assert(validErrors.length === 0, 'valid catalog fixture should pass: ' + validErrors.join(' | '));

  const missingFile = validCatalog();
  missingFile.files = [];
  expectError('missing file record', missingFile, 'lacks one active catalog record');

  const staleSha = validCatalog();
  staleSha.files[0].sha256 = 'c'.repeat(64);
  expectError('stale source SHA', staleSha, 'SHA-256');

  const staleInventorySha = validCatalog();
  staleInventorySha.inventorySha256 = 'd'.repeat(64);
  expectError('stale inventory SHA', staleInventorySha, 'does not match the baseline inventory artifact');

  const shortInventorySha = validCatalog();
  shortInventorySha.inventorySha256 = 'd'.repeat(58);
  expectError('short inventory SHA', shortInventorySha, 'must be a 64-character lowercase SHA-256');

  const missingFormalEvidence = validCatalog();
  delete missingFormalEvidence.files[0].formalApplicability.evidence;
  expectError('missing formal evidence', missingFormalEvidence, 'needs at least one source anchor');

  const unknownJourney = validCatalog();
  unknownJourney.claims[0].journeys[0].caseId = 'missing:case';
  expectError('unknown journey', unknownJourney, 'unknown journey/case');

  const unknownRequiredCheck = validCatalog();
  unknownRequiredCheck.claims[0].journeys[0].requiredChecks = ['not registered'];
  expectError('unknown required check', unknownRequiredCheck, 'unknown required check');

  const undefinedClaim = validCatalog();
  undefinedClaim.files[0].assertions[0].semanticClaimId = 'missing.claim';
  expectError('undefined semantic claim', undefinedClaim, 'undefined semantic claim');

  const conflictingClaim = validCatalog();
  conflictingClaim.claims.push({ ...conflictingClaim.claims[0], statement: 'A conflicting definition.' });
  expectError('conflicting claim definition', conflictingClaim, 'duplicate/conflicting semantic claim definition');

  const unknownModel = validCatalog();
  unknownModel.files[0].formalApplicability.bindings[0].model = 'MissingModel';
  expectError('unknown model', unknownModel, 'unknown formal model');

  const unknownAction = validCatalog();
  unknownAction.files[0].formalApplicability.bindings[0].actions = ['MissingAction'];
  expectError('unknown action', unknownAction, 'unknown formal action');

  const unknownSchedule = validCatalog();
  unknownSchedule.files[0].formalApplicability.bindings[0].scheduleIds = ['missing-schedule'];
  expectError('unknown schedule', unknownSchedule, 'unknown formal schedule');

  const forgedRuntimeStatus = validCatalog();
  forgedRuntimeStatus.files[0].runtimeEvidence = { status: 'passed' };
  expectError('forged runtime result', forgedRuntimeStatus, 'cannot author runtime field');

  const retired = validCatalog();
  retired.files[0].lifecycle = 'retired';
  retired.files[0].disposition = { kind: 'integrate', reason: 'Replaced by the canonical row oracle.' };
  retired.files[0].replacementProof = { paths: ['scripts/replacement.test.mjs'], evidence: [{ path: 'docs/replacement-run.json', sha256: 'd'.repeat(64), kind: 'captured replacement run' }], gap: '' };
  const retiredContext = {
    ...context,
    currentPaths: new Set(),
    currentHashes: new Map(),
    reviewedBlobInfos: new Map([[sourcePath, { sha256: sourceHash, lineCount: 2 }]]),
  };
  const retiredErrors = errorsFor(retired, retiredContext);
  assert(retiredErrors.length === 0, 'retired baseline record with replacement evidence should remain catalogued: ' + retiredErrors.join(' | '));
  retired.files[0].replacementProof = undefined;
  expectError('retired file without proof', retired, 'needs replacement paths and proof references', retiredContext);

  const duplicateGap = validCatalog();
  duplicateGap.files[0].disposition.gap = 'Duplicate gap text';
  expectError('duplicate replacement gap', duplicateGap, 'only at replacementProof.gap');

  const duplicateProofNeed = validCatalog();
  duplicateProofNeed.files[0].replacementProof.proofNeeded = 'Duplicate proof-gap text';
  expectError('duplicate proof-needed field', duplicateProofNeed, 'cannot duplicate its canonical gap');

  const duplicateAssertionCategory = validCatalog();
  duplicateAssertionCategory.files[0].assertions[0].category = 'behavior';
  expectError('duplicate assertion category', duplicateAssertionCategory, 'cannot duplicate claim');

  const diagnosticCheckClaim = validCatalog();
  diagnosticCheckClaim.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L2:status-label';
  diagnosticCheckClaim.files[0].assertions[0].sourceLines = [2];
  expectError('literal-true diagnostic is not a test assertion', diagnosticCheckClaim, 'diagnostic check/recordCheck/recordAssertion reporter');

  const diagnosticRecordCheck = validCatalog();
  diagnosticRecordCheck.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L2:recorded-check';
  diagnosticRecordCheck.files[0].assertions[0].sourceLines = [2];
  const recordCheckContext = {
    ...context,
    reviewedBlobInfos: new Map([[sourcePath, {
      ...context.reviewedBlobInfos.get(sourcePath),
      sourceLines: ['import { recordCheck } from \'../helpers/report.mjs\';', 'recordCheck(report, \'correctness\', \'status label\', true, evidence);'],
    }]]),
  };
  expectError('recordCheck reporter is not a test assertion', diagnosticRecordCheck, 'diagnostic check/recordCheck/recordAssertion reporter', recordCheckContext);

  const predicateRecordCheck = validCatalog();
  predicateRecordCheck.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L2:recorded-check';
  predicateRecordCheck.files[0].assertions[0].sourceLines = [2];
  const predicateRecordCheckContext = {
    ...context,
    reviewedBlobInfos: new Map([[sourcePath, {
      ...context.reviewedBlobInfos.get(sourcePath),
      sourceLines: [
        'function recordCheck(name, condition, evidence) { if (!condition) throw new Error(name); }',
        'recordCheck(\'row count matches fixture\', actualRows.length === 2, true);',
      ],
    }]]),
  };
  assert(errorsFor(predicateRecordCheck, predicateRecordCheckContext).length === 0, 'real predicate wrapper with boolean evidence remains a valid test assertion');

  const unconditionalRecordCheck = validCatalog();
  unconditionalRecordCheck.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L2:forced-check';
  unconditionalRecordCheck.files[0].assertions[0].sourceLines = [2];
  const unconditionalRecordCheckContext = {
    ...context,
    reviewedBlobInfos: new Map([[sourcePath, {
      ...context.reviewedBlobInfos.get(sourcePath),
      sourceLines: [
        'const recordCheck = (name, evidence) => cda.check(\'correctness\', name, true, evidence);',
        'recordCheck(\'row property label\', actualRows);',
      ],
    }]]),
  };
  expectError('unconditional same-file recordCheck is not a test assertion', unconditionalRecordCheck, 'diagnostic check/recordCheck/recordAssertion reporter', unconditionalRecordCheckContext);

  const diagnosticRecordAssertion = validCatalog();
  diagnosticRecordAssertion.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L2:recorded-assertion';
  diagnosticRecordAssertion.files[0].assertions[0].sourceLines = [2];
  const recordAssertionContext = {
    ...context,
    reviewedBlobInfos: new Map([[sourcePath, {
      ...context.reviewedBlobInfos.get(sourcePath),
      lineCount: 3,
      sourceLines: [
        'assert.deepEqual(actual, expected);',
        'recordAssertion(\'status label\', evidence);',
        'const recordAssertion = (name, evidence) => report.assertions.push({ name, status: \'passed\', evidence });',
      ],
    }]]),
  };
  expectError('recordAssertion reporter is not a test assertion', diagnosticRecordAssertion, 'diagnostic check/recordCheck/recordAssertion reporter', recordAssertionContext);

  const cdaRecordAssertionReporter = validCatalog();
  cdaRecordAssertionReporter.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L5:recorded-assertion';
  cdaRecordAssertionReporter.files[0].assertions[0].sourceLines = [5];
  const cdaRecordAssertionContext = {
    ...context,
    reviewedBlobInfos: new Map([[sourcePath, {
      ...context.reviewedBlobInfos.get(sourcePath),
      lineCount: 5,
      sourceLines: [
        'const recordAssertion = (dimension, name, evidenceValue) => {',
        '  cda.check(dimension, name, true, evidenceValue);',
        '  report.assertions.push({ name, status: \'passed\', evidence: evidenceValue });',
        '};',
        'recordAssertion(\'domain property label\', evidence);',
      ],
    }]]),
  };
  expectError('cda completion wrapper is not a test assertion', cdaRecordAssertionReporter, 'diagnostic check/recordCheck/recordAssertion reporter', cdaRecordAssertionContext);

  const predicateRecordAssertion = validCatalog();
  predicateRecordAssertion.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L4:recorded-equality';
  predicateRecordAssertion.files[0].assertions[0].sourceLines = [4];
  const predicateRecordAssertionContext = {
    ...context,
    reviewedBlobInfos: new Map([[sourcePath, {
      ...context.reviewedBlobInfos.get(sourcePath),
      lineCount: 4,
      sourceLines: [
        'function recordAssertion(report, name, expected, actual) {',
        '  if (JSON.stringify(expected) !== JSON.stringify(actual)) throw new Error(name);',
        '}',
        'recordAssertion(report, \'fixture row equality\', expected, actual);',
      ],
    }]]),
  };
  assert(errorsFor(predicateRecordAssertion, predicateRecordAssertionContext).length === 0, 'real recordAssertion equality guard remains a valid test assertion');

  const diagnosticCallThrough = validCatalog();
  diagnosticCallThrough.files[0].assertions[0].sourceAssertionId = 'source:scripts/example.test.mjs:L2:status-label';
  diagnosticCallThrough.files[0].assertions[0].sourceLines = [2];
  diagnosticCallThrough.files[0].assertions[0].evidenceKind = 'call-through';
  assert(errorsFor(diagnosticCallThrough).length === 0, 'literal-true diagnostic may be catalogued as a call-through');

  console.log('PASS semantic catalog validation self-test: 28 positive and negative fixtures');
}

function scriptInventory(modelDefinitions, registry, traceContract, journeyAudit, catalog) {
  const listed = spawnSync('git', ['ls-files', '-z'], { cwd: ROOT, encoding: 'utf8' });
  if (listed.status !== 0) fail('cannot inventory tracked scripts: ' + (listed.stderr || listed.error?.message));
  const tracked = listed.stdout.split('\0').filter(Boolean).sort();
  const paths = tracked.filter((path) => path.endsWith('.mjs'));
  const currentHashes = new Map(paths.filter((path) => existsSync(resolve(ROOT, path)))
    .map((path) => [path, createHash('sha256').update(readFileSync(resolve(ROOT, path))).digest('hex')]));
  const baselinePaths = new Set(trackedMjsPaths(catalog.sourceCommit));
  const baselineInventorySha256 = blobInfoAtCommit(catalog.sourceCommit, [OUTPUT_JSON]).get(OUTPUT_JSON)?.sha256;
  const currentPathSet = new Set(paths);
  const reviewedPathsByCommit = new Map();
  for (const file of catalog.files ?? []) {
    const commit = file.sourceReview?.reviewedCommit ?? catalog.sourceCommit;
    if (!reviewedPathsByCommit.has(commit)) reviewedPathsByCommit.set(commit, []);
    reviewedPathsByCommit.get(commit).push(file.path);
  }
  const reviewedBlobInfos = new Map();
  for (const [commit, reviewedPaths] of reviewedPathsByCommit) {
    for (const [path, info] of blobInfoAtCommit(commit, uniqueSorted(reviewedPaths))) reviewedBlobInfos.set(path, info);
  }
  const caseIndex = buildRegistryCaseIndex(registry);
  const errors = catalogErrors(catalog, {
    currentPaths: currentPathSet,
    currentHashes,
    baselinePaths,
    baselineInventorySha256,
    reviewedBlobInfos,
    caseIndex,
    models: modelDefinitions,
    traceContract,
    journeyAudit,
  });
  if (errors.length) fail('semantic MJS catalog invalid:\n' + errors.map((message) => '- ' + message).join('\n'));

  const catalogByPath = new Map(catalog.files.map((file) => [file.path, file]));
  const rows = new Map(paths.map((path) => [path, {
    path,
    roleHint: scriptRoleHint(path),
    present: true,
    sha256: currentHashes.get(path),
    staticReferenceCandidates: [],
  }]));
  // Do not consume our own generated output: that would make --check recursive.
  for (const source of tracked.filter((path) => path !== OUTPUT_JSON && path !== OUTPUT_MD
    && path !== SEMANTIC_CATALOG
    && !path.startsWith('generated/') && !path.includes('/runtime/')
    && (/\.(mjs|ts|tsx|py|sh|md|ya?ml)$/.test(path) || path.endsWith('package.json') || path === 'Makefile'))) {
    if (!existsSync(resolve(ROOT, source))) continue;
    const text = readFileSync(resolve(ROOT, source), 'utf8');
    for (const match of text.matchAll(/(?:from\s*|import\s*\(\s*|import\s*)['"]([^'"]+\.mjs)['"]/g)) {
      if (!match[1].startsWith('.')) continue;
      const target = relative(ROOT, resolve(ROOT, source, '..', match[1]));
      if (rows.has(target)) rows.get(target).staticReferenceCandidates.push({ kind: 'literal-import-candidate', source, line: text.slice(0, match.index).split('\n').length });
    }
    for (const path of paths) {
      if (path === source) continue;
      const index = text.indexOf(path);
      if (index >= 0) rows.get(path).staticReferenceCandidates.push({ kind: 'path-mention', source, line: text.slice(0, index).split('\n').length });
    }
  }
  const catalogRows = [];
  const sourceFilesByClaim = new Map();
  const claimsById = new Map(catalog.claims.map((claim) => [claim.id, claim]));
  const claimMappingsByCase = new Map();
  for (const claim of catalog.claims) {
    for (const mapping of claim.journeys ?? []) {
      if (!claimMappingsByCase.has(mapping.caseId)) claimMappingsByCase.set(mapping.caseId, []);
      claimMappingsByCase.get(mapping.caseId).push({ claimId: claim.id, ...mapping });
    }
  }
  const rowFor = (source, lifecycle, row = null) => {
    const assertionRows = (source.assertions ?? []).map((assertion) => {
      if (!sourceFilesByClaim.has(assertion.semanticClaimId)) sourceFilesByClaim.set(assertion.semanticClaimId, []);
      sourceFilesByClaim.get(assertion.semanticClaimId).push({
        path: source.path,
        lifecycle,
        sourceAssertionId: assertion.sourceAssertionId,
        evidenceKind: assertion.evidenceKind,
        sourceLines: assertion.sourceLines,
      });
      return { ...assertion };
    });
    const semanticClaimIds = uniqueSorted(assertionRows.map((assertion) => assertion.semanticClaimId));
    const journeyMappings = uniqueSorted(semanticClaimIds.flatMap((claimId) => (claimsById.get(claimId).journeys ?? []).map((mapping) => mapping.caseId)))
      .map((caseId) => ({
        caseId,
        claims: semanticClaimIds.flatMap((claimId) => (claimsById.get(claimId).journeys ?? [])
          .filter((mapping) => mapping.caseId === caseId)
          .map((mapping) => ({ id: claimId, relation: mapping.relation, requiredChecks: mapping.requiredChecks ?? [], focusedCheckIds: mapping.focusedCheckIds ?? [] }))),
      }));
    return {
      ...(row ?? { path: source.path, roleHint: scriptRoleHint(source.path), staticReferenceCandidates: [], staticReferenceCandidateCount: 0 }),
      path: source.path,
      lifecycle,
      origin: baselinePaths.has(source.path) ? 'baseline' : 'added',
      present: lifecycle === 'active',
      sha256: source.sha256,
      purpose: source.purpose,
      role: source.role,
      testLayer: source.testLayer,
      sourceReview: {
        ...source.sourceReview,
        reviewedCommit: source.sourceReview.reviewedCommit ?? catalog.sourceCommit,
      },
      sourceRevision: {
        commit: source.sourceReview.reviewedCommit ?? catalog.sourceCommit,
        sha256: source.sha256,
      },
      executionDiscovery: source.executionDiscovery,
      assertions: assertionRows,
      semanticClaimIds,
      journeyMappings,
      oracle: source.oracle ?? null,
      formalApplicability: source.formalApplicability,
      disposition: source.disposition,
      replacementProof: source.replacementProof ?? null,
      duplicateCandidates: source.duplicateCandidates ?? [],
      runtimeEvidence: { status: 'unverified', reason: 'No exact current run artifact is joined to this MJS SHA-256.' },
    };
  };
  for (const [path, row] of rows) {
    const source = catalogByPath.get(path);
    catalogRows.push(rowFor(source, 'active', {
      ...row,
      staticReferenceCandidateCount: row.staticReferenceCandidates.length,
      staticReferenceCandidates: row.staticReferenceCandidates.slice(0, 8),
    }));
  }
  for (const source of catalog.files.filter((file) => file.lifecycle === 'retired')) {
    catalogRows.push(rowFor(source, 'retired'));
  }
  const ordered = catalogRows.sort((left, right) => left.path.localeCompare(right.path));
  const retiredPaths = catalog.files.filter((file) => file.lifecycle === 'retired').map((file) => file.path);
  const semanticClaims = catalog.claims.map((claim) => ({
    id: claim.id,
    statement: claim.statement,
    category: claim.category,
    journeys: claim.journeys ?? [],
    sourceAssertions: (sourceFilesByClaim.get(claim.id) ?? []).sort((left, right) => left.path.localeCompare(right.path) || left.sourceAssertionId.localeCompare(right.sourceAssertionId)),
  }));
  const sourceOccurrences = catalog.files.flatMap((file) => file.assertions ?? []);
  const countBy = (items, keyOf) => Object.fromEntries([...items.reduce((counts, item) => {
    const key = keyOf(item);
    counts.set(key, (counts.get(key) ?? 0) + 1);
    return counts;
  }, new Map()).entries()].sort(([left], [right]) => left.localeCompare(right)));
  const assertionEvidenceKindCounts = countBy(sourceOccurrences, (occurrence) => occurrence.evidenceKind);
  const summary = {
    baselineFiles: baselinePaths.size,
    currentFiles: currentPathSet.size,
    baselineRetired: [...retiredPaths].filter((path) => baselinePaths.has(path)).length,
    currentAdded: [...currentPathSet].filter((path) => !baselinePaths.has(path)).length,
    sourceReviewed: catalog.files.filter((file) => file.sourceReview?.status === 'reviewed').length,
    runtimeUnverified: ordered.length,
    invocationUncertain: catalog.files.filter((file) => file.executionDiscovery?.status === 'uncertain-dynamic-or-manual').length,
    semanticClaims: catalog.claims.length,
    sourceAssertionOccurrences: sourceOccurrences.length,
    sourceAssertions: sourceOccurrences.filter((occurrence) => occurrence.evidenceKind === 'test-assertion').length,
    nonAssertionOccurrences: sourceOccurrences.filter((occurrence) => occurrence.evidenceKind !== 'test-assertion').length,
    claimsWithoutSourceAssertions: semanticClaims.filter((claim) => !claim.sourceAssertions.some((source) => source.evidenceKind === 'test-assertion')).length,
    claimsWithoutJourneyMappings: semanticClaims.filter((claim) => claim.journeys.length === 0).length,
    assertionEvidenceKindCounts,
    roleCounts: countBy(ordered, (file) => file.role),
    formalApplicabilityCounts: countBy(ordered, (file) => file.formalApplicability.kind),
    dispositionCounts: countBy(ordered, (file) => file.disposition.kind),
  };
  return { rows: ordered, semanticClaims, claimMappingsByCase, sourceFilesByClaim, summary };
}

function normalizeRoute(value) {
  return value
    .replace(/\{explorer\}/g, '{explorerId}')
    .replace(/\{encodedProject\}/g, '{project}')
    .replace(/\?.*$/, '')
    .replace(/\/+$/, '');
}

function routeKey(method, path) {
  return String(method).toUpperCase() + ' ' + normalizeRoute(String(path));
}

function parseEndpoint(value) {
  const match = String(value).match(/^([A-Z]+)\s+(.+)$/);
  return match ? routeKey(match[1], match[2]) : null;
}

function routePathPattern(path) {
  return path
    .split('/')
    .map((segment) => (/^\{[^}]+\}$/.test(segment) ? '[^/]+' : escapeRegExp(segment)))
    .join('/');
}

function routeSuffix(path) {
  const segments = path.split('/').filter(Boolean);
  const suffix = segments.at(-1) ?? '';
  return /^\{[^}]+\}$/.test(suffix) ? null : suffix;
}

function parseTlaText(text) {
  const definitions = new Set();
  for (const match of text.matchAll(/^([A-Za-z_][A-Za-z0-9_]*)(?:\([^)]*\))?\s*==/gm)) {
    definitions.add(match[1]);
  }
  const next = text.match(/^Next\s*==\s*([\s\S]*?)^Spec\s*==/m);
  if (!next) throw new Error('TLA model has no Next == ... Spec == block');
  const actions = [];
  for (const clause of next[1].split(/\n\s*\\\/\s*/).map((part) => part.replace(/^\\\/\s*/, '')).filter(Boolean)) {
    let target = clause.trim();
    const colon = target.lastIndexOf(':');
    if (target.includes('\\E') && colon >= 0) target = target.slice(colon + 1);
    const names = [...definitions].filter((name) => new RegExp('\\b' + escapeRegExp(name) + '\\b').test(target));
    const action = names.find((name) => !/^Inv_/.test(name) && !['TypeOK', 'Next', 'Spec', 'Init'].includes(name));
    if (action) actions.push(action);
  }
  if (actions.length === 0) throw new Error('TLA Next == block has no parseable action');
  return { actions: uniqueSorted(actions), invariants: [] };
}

function parseTla(modelFile) {
  try {
    return parseTlaText(readFileSync(modelFile, 'utf8'));
  } catch (error) {
    fail(modelFile + ': ' + error.message);
  }
}

function parseInvariantsText(text) {
  return uniqueSorted([...text.matchAll(/^INVARIANT\s+([A-Za-z_][A-Za-z0-9_]*)/gm)]
    .map((match) => match[1])
    .filter((name) => name !== 'TypeOK'));
}

function parseInvariants(cfgFile) {
  return parseInvariantsText(readFileSync(cfgFile, 'utf8'));
}

function selfTestParser() {
  const assert = (condition, message) => {
    if (!condition) fail('parser self-test: ' + message);
  };
  const expectThrow = (label, text) => {
    try {
      parseTlaText(text);
    } catch {
      return;
    }
    fail('parser self-test: ' + label + ' did not fail');
  };
  const twoActions = `---- MODULE TwoActions ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
FirstAction == x = 0 /\\ x' = 1
SecondAction == x = 1 /\\ x' = 0
Next ==
  \\/ FirstAction
  \\/ SecondAction
Spec == Init /\\ [][Next]_x
====`;
  const parsed = parseTlaText(twoActions);
  assert(parsed.actions.includes('FirstAction'), 'dropped the first Next action');
  assert(parsed.actions.includes('SecondAction'), 'dropped the second Next action');
  assert(parsed.actions.length === 2, 'expected 2 actions, got ' + parsed.actions.length);

  const existsBound = `---- MODULE Exists ----
EXTENDS Naturals
VARIABLES x
Requests == {1}
Init == x = 0
Handle(request) == x' = request
Stop == x' = 0
Next ==
  \\/ \\E request \\in Requests :
       Handle(request)
  \\/ Stop
Spec == Init /\\ [][Next]_x
====`;
  const existsParsed = parseTlaText(existsBound);
  assert(existsParsed.actions.includes('Handle'), 'dropped the exists-bound action');
  assert(existsParsed.actions.includes('Stop'), 'dropped the action after the exists bound');

  const singleAction = `---- MODULE Single ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
OnlyAction == x' = 1
Next == OnlyAction
Spec == Init /\\ [][Next]_x
====`;
  assert(parseTlaText(singleAction).actions.join(',') === 'OnlyAction', 'did not parse a single Next action');

  const commented = `---- MODULE Commented ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
FirstAction == x = 0 /\\ x' = 1 \\* first branch
SecondAction == x = 1 /\\ x' = 0
Next ==
  \\/ FirstAction \\* selected branch
  \\/ SecondAction
Spec == Init /\\ [][Next]_x
====`;
  assert(parseTlaText(commented).actions.join(',') === 'FirstAction,SecondAction', 'did not ignore TLA comments');

  const filtered = `---- MODULE Filtered ----
EXTENDS Naturals
VARIABLES x
Init == x = 0
Inv_Bad == x = 1
GoodAction == x' = 1
Next ==
  \\/ Inv_Bad
  \\/ GoodAction
Spec == Init /\\ [][Next]_x
====`;
  assert(parseTlaText(filtered).actions.join(',') === 'GoodAction', 'did not filter Inv_ actions');

  expectThrow('missing Next/Spec', '---- MODULE Missing ----\n====');
  expectThrow('missing Spec', `---- MODULE MissingSpec ----
EXTENDS Naturals
VARIABLES x
Action == x' = 1
Next == Action
====`);
  expectThrow('unknown action', `---- MODULE Unknown ----
EXTENDS Naturals
VARIABLES x
Next == MissingAction
Spec == [][Next]_x
====`);
  expectThrow('only invariant actions', `---- MODULE InvOnly ----
EXTENDS Naturals
VARIABLES x
Inv_Bad == x = 1
Next == Inv_Bad
Spec == [][Next]_x
====`);

  const invariants = parseInvariantsText('INVARIANT TypeOK\nINVARIANT Safety\nINVARIANT Safety\n');
  assert(invariants.join(',') === 'Safety', 'invariant parser did not filter TypeOK or deduplicate');
  console.log('PASS obligation parser self-test: 10 fixtures');
}

function testNameFromFile(text) {
  const names = [];
  const pattern = /\b(?:it|test)(?:\.each\s*\([^)]*\))?\s*\(\s*(['"`])([\s\S]*?)\1/g;
  for (const match of text.matchAll(pattern)) {
    const name = match[2].replace(/\s+/g, ' ').trim();
    if (name) names.push(name);
  }
  return names;
}

function mappingRank(mapping) {
  return {
    'browser-evidence': 6,
    'route-proof': 5,
    'registry-endpoints': 4,
    'go-route': 3,
    'go-route-suffix': 2,
    'keyword-model': 1,
    unmapped: 0,
  }[mapping] ?? 0;
}

function betterMapping(left, right) {
  return mappingRank(left) >= mappingRank(right) ? left : right;
}

if (args.includes('--self-test')) {
  selfTestParser();
  selfTestCatalogValidation();
  if (scriptRoleHint('scripts/a.spec.mjs') !== 'browser-spec'
    || scriptRoleHint('scripts/a.test.mjs') !== 'test'
    || scriptRoleHint('scripts/lib/a.mjs') !== 'helper-or-oracle') fail('script inventory role hint classification failed');
  process.exit(0);
}

const ledger = readJson(LEDGER);
const traceContract = readJson(TRACE_CONTRACT);
const journeyAudit = readJson(MODEL_DIR + '/builder-journey-audit.json');
const semanticCatalog = readJson(SEMANTIC_CATALOG);
const browserEvidence = existsSync(resolve(ROOT, BROWSER_EVIDENCE)) ? readJson(BROWSER_EVIDENCE) : { cases: [] };
const routeByKey = new Map();
const actionToRoutes = new Map();
const actionRouteBinding = new Map();
for (const route of ledger.routes ?? []) {
  const key = routeKey(route.method, route.path);
  routeByKey.set(key, route);
  const routeActions = route.proof?.routeActions ?? [];
  const actions = routeActions.length > 0 ? routeActions : route.proof?.modelActions ?? [];
  for (const action of actions) {
    if (!actionToRoutes.has(action)) actionToRoutes.set(action, new Set());
    actionToRoutes.get(action).add(key);
    if (routeActions.length === 0) actionRouteBinding.set(action, 'candidate');
    else if (actionRouteBinding.get(action) !== 'candidate') actionRouteBinding.set(action, 'route');
  }
}

const cfgFiles = walkFiles(MODEL_DIR, (path) => path.endsWith('.cfg'));
const modelFiles = walkFiles(MODEL_DIR, (path) => path.endsWith('.tla'));
const models = new Map();
for (const file of modelFiles) {
  const model = basename(file, '.tla');
  const parsed = parseTla(file);
  const exactCfg = cfgFiles.find((candidate) => basename(candidate) === model + '.cfg');
  const relatedCfg = exactCfg ?? cfgFiles.find((candidate) => basename(candidate).startsWith(model));
  const invariants = relatedCfg ? parseInvariants(relatedCfg) : [];
  models.set(model, { actions: parsed.actions, invariants, cfg: relatedCfg ? relative(ROOT, relatedCfg) : null });
}

const obligations = [];
const actionObligations = new Map();
const actionNameToObligationIds = new Map();
const actionNameToModels = new Map();
for (const [model, definition] of models) {
  const modelRoutes = uniqueSorted([...definition.actions].flatMap((action) => [...(actionToRoutes.get(action) ?? [])]));
  for (const action of definition.actions) {
    const id = model + '.' + action;
    const obligation = {
      id,
      model,
      kind: 'action',
      action,
      invariant: null,
      routes: uniqueSorted([...(actionToRoutes.get(action) ?? [])]),
      tests: [],
      suspectedModels: [],
      routeBinding: actionRouteBinding.get(action) ?? 'unbound',
      status: 'missing',
    };
    obligations.push(obligation);
    actionObligations.set(id, obligation);
    if (!actionNameToObligationIds.has(action)) actionNameToObligationIds.set(action, []);
    actionNameToObligationIds.get(action).push(id);
    if (!actionNameToModels.has(action)) actionNameToModels.set(action, new Set());
    actionNameToModels.get(action).add(model);
  }
  for (const invariant of definition.invariants) {
    const id = model + '.' + invariant;
    obligations.push({
      id,
      model,
      kind: 'invariant',
      action: null,
      invariant,
      routes: modelRoutes,
      tests: [],
      suspectedModels: [model],
      routeBinding: modelRoutes.length > 0 ? 'candidate' : 'unbound',
      status: 'missing',
    });
  }
}

const tests = new Map();
function addTest(record) {
  const key = record.id;
  if (!tests.has(key)) {
    tests.set(key, {
      ...record,
      routes: uniqueSorted(record.routes ?? []),
      obligations: uniqueSorted(record.obligations ?? []),
      suspectedModels: uniqueSorted(record.suspectedModels ?? []),
      suspectedActions: uniqueSorted(record.suspectedActions ?? []),
      checks: record.checks ?? [],
    });
    return tests.get(key);
  }
  const existing = tests.get(key);
  existing.routes = uniqueSorted([...existing.routes, ...(record.routes ?? [])]);
  existing.obligations = uniqueSorted([...existing.obligations, ...(record.obligations ?? [])]);
  existing.suspectedModels = uniqueSorted([...existing.suspectedModels, ...(record.suspectedModels ?? [])]);
  existing.suspectedActions = uniqueSorted([...existing.suspectedActions, ...(record.suspectedActions ?? [])]);
  existing.checks = uniqueSorted([...existing.checks, ...(record.checks ?? [])]);
  existing.mapping = betterMapping(existing.mapping, record.mapping);
  return existing;
}

function actionIdsFor(action) {
  const exact = actionNameToObligationIds.get(action) ?? [];
  if (exact.length) return exact;
  return actionNameToObligationIds.get(action.replace(/\(.*\)$/, '')) ?? [];
}

function obligationIdsForRoute(route) {
  if (!route?.proof?.model) return [];
  const model = route.proof.model.replace(/\.tla$/, '');
  const actions = route.proof.coveredActions?.length ? route.proof.coveredActions : route.proof.modelActions ?? [];
  return uniqueSorted(actions.flatMap((action) => {
    return actionIdsFor(action).filter((id) => id.startsWith(model + '.'));
  }));
}

function suspectedActionsForRoutes(routes) {
  return uniqueSorted(routes.flatMap((key) => {
    const route = routeByKey.get(key);
    return route?.proof?.routeActions?.length ? route.proof.routeActions : route?.proof?.modelActions ?? [];
  }));
}

function suspectedModelsForRoutes(routes) {
  return uniqueSorted(routes.flatMap((key) => {
    const route = routeByKey.get(key);
    return suspectedActionsForRoutes([key]).flatMap((action) => {
      const modelsForAction = actionNameToModels.get(action) ?? actionNameToModels.get(action.replace(/\(.*\)$/, '')) ?? [];
      return [...modelsForAction];
    });
  }));
}

function routeObligations(routes) {
  return uniqueSorted(routes.flatMap((key) => {
    const route = routeByKey.get(key);
    return route ? obligationIdsForRoute(route) : [];
  }));
}

const browserCases = new Map((browserEvidence.cases ?? []).map((entry) => [entry.id, entry]));
const { registry } = await import(pathToFileURL(resolve(ROOT, REGISTRY)).href);
const scriptCatalog = scriptInventory(models, registry, traceContract, journeyAudit, semanticCatalog);
const scripts = scriptCatalog.rows;
const scriptRowsByPath = new Map(scripts.map((script) => [script.path, script]));
const journeyAssemblyRows = buildJourneyAssemblyRows({
  claimMappingsByCase: scriptCatalog.claimMappingsByCase,
  sourceFilesByClaim: scriptCatalog.sourceFilesByClaim,
  scriptRowsByPath,
});
const toolModuleFiles = scripts.filter((script) => script.role === 'tool-or-module');
const unmappedJourneyFiles = scripts.filter((script) => script.journeyMappings.length === 0);
const unmappedJourneyClaims = scriptCatalog.semanticClaims.filter((claim) => claim.journeys.length === 0);
const semanticClaimIdsForCase = (caseId) => uniqueSorted(
  (scriptCatalog.claimMappingsByCase.get(caseId) ?? []).map((mapping) => mapping.claimId),
);
for (const entry of registry) {
  const entryRoutes = uniqueSorted((entry.endpoints ?? []).map(parseEndpoint).filter(Boolean));
  for (const [caseName, caseData] of Object.entries(entry.cases ?? {})) {
    const id = entry.id + ':' + caseName;
    const evidence = browserCases.get(id);
    const routes = evidence?.coveredRoutes?.length ? uniqueSorted(evidence.coveredRoutes) : entryRoutes;
    addTest({
      id: 'playwright:' + id,
      kind: 'playwright',
      level: 'browser',
      name: caseData.playwrightGrep ?? caseName,
      caseName,
      file: caseData.playwrightTest ?? entry.script ?? REGISTRY,
      routes,
      obligations: evidence ? routeObligations(routes) : [],
      suspectedModels: suspectedModelsForRoutes(routes),
      checks: Array.isArray(caseData.requiredChecks) ? caseData.requiredChecks : [],
      focusedChecks: uniqueSorted((caseData.focusedChecks ?? []).map((check) => check.id)),
      semanticClaimIds: semanticClaimIdsForCase(id),
      mapping: evidence ? 'browser-evidence' : 'registry-endpoints',
      evidence: evidence ? BROWSER_EVIDENCE : REGISTRY,
    });
  }
}

for (const [caseId, evidence] of browserCases) {
  const existing = tests.get('playwright:' + caseId);
  if (existing) {
    existing.mapping = 'browser-evidence';
    existing.evidence = BROWSER_EVIDENCE;
    existing.checks = uniqueSorted([...existing.checks, ...(evidence.assertions ?? []).map((assertion) => assertion.name)]);
  }
}

const modelKeywords = [
  { model: 'ReconcileLifecycle', pattern: /reconcil/i },
  { model: 'BuilderPreview', pattern: /preview|proposal|apply|applyack|beginapply/i },
  { model: 'ConstructionLifecycle', pattern: /construction|proposal|apply/i },
  { model: 'RowDefinitionLifecycle', pattern: /row.?definition|row definition/i },
  { model: 'Population', pattern: /population|selection|cohort/i },
  { model: 'Interpretation', pattern: /interpretation|feature meaning/i },
  { model: 'RootChange', pattern: /root.?change|root change/i },
  { model: 'DraftRestore', pattern: /draft.?restore|undo/i },
  { model: 'ColumnPresentation', pattern: /column|presentation/i },
  { model: 'WorkspaceLifecycle', pattern: /explorer|hydration|workspace/i },
  { model: 'LifecycleEntry', pattern: /command|receipt|selection|route/i },
  { model: 'SupportingSurfaces', pattern: /lineage|frame|graph|review/i },
  { model: 'BuilderOperations', pattern: /step|operation|editor/i },
];

const vitestFiles = walkFiles(UI_ROOT, (path) => /\.(test|spec)\.(ts|tsx)$/.test(path));
for (const file of vitestFiles) {
  const relativeFile = relative(ROOT, file);
  const text = readFileSync(file, 'utf8');
  for (const name of testNameFromFile(text)) {
    const haystack = relativeFile + ' ' + name;
    const suspectedModels = uniqueSorted(modelKeywords.filter((entry) => entry.pattern.test(haystack)).map((entry) => entry.model));
    addTest({
      id: 'vitest:' + relativeFile + '::' + name,
      kind: 'vitest',
      level: 'unit',
      name,
      file: relativeFile,
      routes: [],
      obligations: [],
      suspectedModels,
      mapping: suspectedModels.length > 0 ? 'keyword-model' : 'unmapped',
      evidence: relativeFile,
    });
  }
}

const formalRunner = readFileSync(resolve(ROOT, FORMAL_RUNNER), 'utf8');
const formalGoTests = uniqueSorted([
  ...[...formalRunner.matchAll(/'(Test[A-Za-z0-9_]+)'/g)].map((match) => match[1]),
]);
const formalGoSet = new Set(formalGoTests);

const goTests = [];
for (const file of walkFiles(GO_ROOT, (path) => path.endsWith('_test.go'))) {
  const relativeFile = relative(ROOT, file);
  const text = readFileSync(file, 'utf8');
  const functions = [...text.matchAll(/^func (Test[A-Za-z0-9_]+)\(/gm)];
  for (let index = 0; index < functions.length; index += 1) {
    const match = functions[index];
    const start = match.index;
    const end = index + 1 < functions.length ? functions[index + 1].index : text.length;
    const body = text.slice(start, end);
    if (!body.includes('/authoring/v2/') && !formalGoSet.has(match[1])) continue;
    const routes = [];
    let mapping = 'unmapped';
    for (const route of ledger.routes ?? []) {
      if (new RegExp(routePathPattern(route.path)).test(body)) {
        routes.push(routeKey(route.method, route.path));
        mapping = 'go-route';
        continue;
      }
      const suffix = routeSuffix(route.path);
      if (!suffix || !route.path.includes('/authoring/v2/')) continue;
      if (new RegExp('["\'`]\\/' + escapeRegExp(suffix) + '["\'`]').test(body)) {
        routes.push(routeKey(route.method, route.path));
        mapping = betterMapping(mapping, 'go-route-suffix');
      }
    }
    goTests.push({
      id: 'go:' + relativeFile + '::' + match[1],
      kind: 'go',
      level: 'server',
      name: match[1],
      file: relativeFile,
      routes: uniqueSorted(routes),
      obligations: routeObligations(uniqueSorted(routes)),
      suspectedModels: suspectedModelsForRoutes(uniqueSorted(routes)),
      suspectedActions: suspectedActionsForRoutes(uniqueSorted(routes)),
      mapping,
      evidence: relativeFile,
    });
  }
}

for (const test of goTests) {
  addTest({
    ...test,
    id: 'go:' + test.file + '::' + test.name,
  });
}

for (const route of ledger.routes ?? []) {
  if (route.proof?.status !== 'modeled-and-traced') continue;
  const routeKeyValue = routeKey(route.method, route.path);
  const actionIds = obligationIdsForRoute(route);
  if (route.proof.goTest) {
    const match = [...tests.values()].find((test) => test.kind === 'go' && test.name === route.proof.goTest);
    if (match) {
      match.routes = uniqueSorted([...match.routes, routeKeyValue]);
      match.obligations = uniqueSorted([...match.obligations, ...actionIds]);
      match.mapping = betterMapping(match.mapping, 'route-proof');
      match.evidence = route.proof.goTest;
    } else {
      addTest({
        id: 'go:' + route.proof.goTest,
        kind: 'go',
        level: 'server',
        name: route.proof.goTest,
        file: route.proof.uiTest ?? FORMAL_RUNNER,
        routes: [routeKeyValue],
        obligations: actionIds,
        mapping: 'route-proof',
        evidence: route.proof.goTest,
      });
    }
  }
  if (route.proof.uiTest && route.proof.schedule) {
    const schedule = traceContract.schedules?.[route.proof.schedule];
    const name = schedule?.test ?? route.proof.schedule;
    const file = route.proof.uiTest;
    const existing = [...tests.values()].find((test) => test.kind === 'vitest' && test.file === file && test.name.includes(name));
    if (existing) {
      existing.routes = uniqueSorted([...existing.routes, routeKeyValue]);
      existing.obligations = uniqueSorted([...existing.obligations, ...actionIds]);
      existing.mapping = 'route-proof';
      existing.evidence = route.proof.schedule;
    } else {
      addTest({
        id: 'vitest:' + file + '::' + name,
        kind: 'vitest',
        level: 'unit',
        name,
        file,
        routes: [routeKeyValue],
        obligations: actionIds,
        mapping: 'route-proof',
        evidence: route.proof.schedule,
      });
    }
  }
  if (route.proof.browserCase) {
    const match = tests.get('playwright:' + route.proof.browserCase);
    if (match) {
      match.routes = uniqueSorted([...match.routes, routeKeyValue]);
      match.obligations = uniqueSorted([...match.obligations, ...actionIds]);
      match.mapping = 'route-proof';
    } else {
      addTest({
        id: 'playwright:' + route.proof.browserCase,
        kind: 'playwright',
        level: 'browser',
        name: route.proof.browserCase,
        file: BROWSER_EVIDENCE,
        routes: [routeKeyValue],
        obligations: actionIds,
        mapping: 'route-proof',
        evidence: BROWSER_EVIDENCE,
      });
    }
  }
}

for (const obligation of obligations) {
  for (const test of tests.values()) {
    if (test.obligations.includes(obligation.id)) {
      obligation.tests.push({
        id: test.id,
        kind: test.kind,
        level: test.level,
        name: test.name,
        file: test.file,
        mapping: test.mapping,
      });
    }
  }
  obligation.tests = obligation.tests.sort((left, right) => left.level.localeCompare(right.level) || left.name.localeCompare(right.name));
  if (obligation.tests.some((test) => mappingRank(test.mapping) >= mappingRank('go-route'))) {
    obligation.status = 'covered';
  } else if (obligation.tests.length > 0 || obligation.suspectedModels.length > 0) {
    obligation.status = 'partial';
  }
}

const obligationsById = new Map(obligations.map((obligation) => [obligation.id, obligation]));
for (const obligation of obligations) {
  if (obligation.kind !== 'invariant') continue;
  const modelActions = models.get(obligation.model)?.actions ?? [];
  const testsForModel = new Set();
  for (const action of modelActions) {
    for (const test of obligationsById.get(obligation.model + '.' + action)?.tests ?? []) {
      testsForModel.add(test.id);
    }
  }
  obligation.tests = [...testsForModel].map((id) => {
    const test = tests.get(id);
    return {
      id: test.id,
      kind: test.kind,
      level: test.level,
      name: test.name,
      file: test.file,
      mapping: 'model-action',
    };
  }).sort((left, right) => left.level.localeCompare(right.level) || left.name.localeCompare(right.name));
  if (obligation.tests.length > 0) obligation.status = 'partial';
}

const exactTests = [...tests.values()].filter((test) => test.obligations.length > 0);
const unmappedTests = [...tests.values()].filter((test) => test.obligations.length === 0);
const gaps = obligations.filter((obligation) => obligation.status === 'missing');
const partial = obligations.filter((obligation) => obligation.status === 'partial');
const covered = obligations.filter((obligation) => obligation.status === 'covered');

const obligationDuplicates = obligations
  .map((obligation) => ({
    obligation: obligation.id,
    byLevel: Object.fromEntries(['browser', 'unit', 'server', 'model'].map((level) => [
      level,
      obligation.tests.filter((test) => test.level === level).map((test) => test.id),
    ]).filter(([, ids]) => ids.length > 1)),
  }))
  .filter((entry) => Object.keys(entry.byLevel).length > 0);

const testNameGroups = new Map();
for (const test of tests.values()) {
  if (!['vitest', 'go'].includes(test.kind)) continue;
  const key = test.kind + ':' + test.name.toLowerCase().replace(/\s+/g, ' ').trim();
  if (!testNameGroups.has(key)) testNameGroups.set(key, []);
  testNameGroups.get(key).push(test);
}
const duplicateTestNames = [...testNameGroups.entries()]
  .filter(([, entries]) => entries.length > 1)
  .map(([name, entries]) => ({ name, tests: entries.map((test) => test.id) }));

const checkGroups = new Map();
for (const test of tests.values()) {
  if (test.kind !== 'playwright') continue;
  for (const check of test.checks) {
    if (!checkGroups.has(check)) checkGroups.set(check, []);
    checkGroups.get(check).push(test.id);
  }
}
const duplicateChecks = [...checkGroups.entries()]
  .filter(([, ids]) => ids.length > 1)
  .map(([check, ids]) => ({ check, tests: ids }));

const sharedRouteProofTests = new Map();
for (const route of ledger.routes ?? []) {
  if (route.proof?.status !== 'modeled-and-traced') continue;
  for (const [kind, value] of [['uiTest', route.proof.uiTest], ['goTest', route.proof.goTest], ['browserCase', route.proof.browserCase]]) {
    if (!value) continue;
    const key = kind + ':' + value;
    if (!sharedRouteProofTests.has(key)) sharedRouteProofTests.set(key, []);
    sharedRouteProofTests.get(key).push(route.id);
  }
}
const sharedProofs = [...sharedRouteProofTests.entries()]
  .filter(([, routes]) => routes.length > 1)
  .map(([test, routes]) => ({ test, routes }));

const summary = {
  models: models.size,
  actions: obligations.filter((obligation) => obligation.kind === 'action').length,
  invariants: obligations.filter((obligation) => obligation.kind === 'invariant').length,
  obligations: obligations.length,
  covered: covered.length,
  partial: partial.length,
  missing: gaps.length,
  missingRouteBound: gaps.filter((obligation) => obligation.routeBinding === 'route').length,
  missingCandidate: gaps.filter((obligation) => obligation.routeBinding === 'candidate').length,
  missingUnbound: gaps.filter((obligation) => obligation.routeBinding === 'unbound').length,
  tests: tests.size,
  testsWithObligations: exactTests.length,
  testsWithoutObligations: unmappedTests.length,
  obligationsWithMultipleTests: obligationDuplicates.length,
  duplicateTestNames: duplicateTestNames.length,
  repeatedPlaywrightChecks: duplicateChecks.length,
  sharedProofs: sharedProofs.length,
  candidateRouteBindings: obligations.filter((obligation) => obligation.routeBinding === 'candidate').length,
};

const report = {
  schemaVersion: 2,
  sources: {
    modelDir: MODEL_DIR,
    routeLedger: LEDGER,
    traceContract: TRACE_CONTRACT,
    semanticCatalog: SEMANTIC_CATALOG,
    browserEvidence: existsSync(resolve(ROOT, BROWSER_EVIDENCE)) ? BROWSER_EVIDENCE : null,
    registry: REGISTRY,
    formalRunner: FORMAL_RUNNER,
    uiRoot: UI_ROOT,
    goRoot: GO_ROOT,
  },
  summary,
  scripts,
  mjsCatalogSummary: scriptCatalog.summary,
  semanticClaims: scriptCatalog.semanticClaims,
  scriptInventoryBounds: 'The file catalog is source-reviewed and hash-bound to a reviewed source revision. Static import and path mentions remain candidates, not proof of reachability; dynamic and manual invocation uncertainty is recorded separately. Semantic claims, journey mappings, and formal bindings describe intended associations. Every MJS runtime status is unverified unless an exact current artifact is joined to its source SHA; labels do not prove execution or model refinement. Retirement rows preserve their source and replacement references.',
  obligations,
  tests: [...tests.values()].sort((left, right) => left.kind.localeCompare(right.kind) || left.name.localeCompare(right.name)),
  duplicates: {
    multipleTests: obligationDuplicates,
    testNames: duplicateTestNames,
    checks: duplicateChecks,
    sharedProofs,
  },
  gaps: gaps.map((obligation) => obligation.id),
  partial: partial.map((obligation) => obligation.id),
};

const serialized = JSON.stringify(report, null, 2) + '\n';
if (check) {
  if (!existsSync(resolve(ROOT, jsonPath))) fail(jsonPath + ' does not exist');
  if (readFileSync(resolve(ROOT, jsonPath), 'utf8') !== serialized) {
    fail(jsonPath + ' is stale; run node scripts/build-test-obligations.mjs');
  }
  console.log('PASS ' + jsonPath + ' is current');
} else {
  writeFileSync(resolve(ROOT, jsonPath), serialized);
}

const lines = [];
lines.push('# Test obligations');
lines.push('');
lines.push('Status: generated from the TLA models and existing test metadata.');
lines.push('');
lines.push('## Script quality inventory');
lines.push('');
lines.push(scriptCatalog.summary.baselineFiles + ' MJS files in the reviewed baseline are accounted for as active or retired records.');
lines.push(scripts.filter((script) => script.lifecycle === 'active').length + ' are currently tracked; ' + scriptCatalog.summary.baselineRetired + ' baseline files have retained tombstones.');
lines.push(scriptCatalog.summary.semanticClaims + ' semantic claims are defined once; ' + scriptCatalog.summary.sourceAssertions + ' source occurrences are direct test assertions, out of ' + scriptCatalog.summary.sourceAssertionOccurrences + ' total labeled occurrences.');
lines.push(scriptCatalog.summary.nonAssertionOccurrences + ' other source-evidence occurrences are labeled by kind and do not count as direct test assertions.');
lines.push(scriptCatalog.summary.sourceReviewed + ' catalog records have source-review evidence; ' + scriptCatalog.summary.runtimeUnverified + ' MJS records remain runtime-unverified.');
lines.push(toolModuleFiles.length + ' files are classified as independent tool/module purposes; ' + unmappedJourneyFiles.length + ' MJS files and ' + unmappedJourneyClaims.length + ' semantic claims have no canonical journey mapping.');
lines.push('`role` and `testLayer` are source-reviewed classifications. `roleHint` and static references are generated path/search hints.');
lines.push('');
lines.push('| Role | Files |');
lines.push('| --- | ---: |');
for (const role of uniqueSorted(scripts.map((script) => script.role))) lines.push('| ' + role + ' | ' + scripts.filter((script) => script.role === role).length + ' |');
lines.push('');
lines.push('### Journey assembly by canonical case');
lines.push('');
lines.push('| Canonical case | Source files | Direct claims | Partial claims | Supporting claims | Registered check refs | Candidate formal models |');
lines.push('| --- | ---: | ---: | ---: | ---: | ---: | --- |');
for (const row of journeyAssemblyRows) {
  lines.push('| `' + row.caseId + '` | ' + row.sourceFileCount + ' | ' + row.directClaims + ' | ' + row.partialClaims + ' | ' + row.supportingClaims + ' | ' + row.registeredCheckCount + ' | ' + (row.candidateFormalModels.join(', ') || '—') + ' |');
}
lines.push('');
lines.push('Counts deduplicate semantic claim IDs, source paths, and exact required/focused check references per case. Candidate models come only from files explicitly labeled `formalApplicability.kind: candidate`; these source associations do not establish execution or model refinement.');
lines.push('');
lines.push('### Independently scoped tools and unmapped source');
lines.push('');
lines.push('| Scope | Count |');
lines.push('| --- | ---: |');
lines.push('| Tool/module files | ' + toolModuleFiles.length + ' |');
lines.push('| Tool/module files with a canonical case mapping | ' + toolModuleFiles.filter((script) => script.journeyMappings.length > 0).length + ' |');
lines.push('| Tool/module files without a canonical case mapping | ' + toolModuleFiles.filter((script) => script.journeyMappings.length === 0).length + ' |');
lines.push('| All MJS files without a canonical case mapping | ' + unmappedJourneyFiles.length + ' |');
lines.push('| Semantic claims without a canonical case mapping | ' + unmappedJourneyClaims.length + ' |');
lines.push('');
lines.push('### Explicit formal links');
lines.push('');
const formalLinks = scripts.filter((script) => script.formalApplicability.kind !== 'not-applicable');
for (const script of formalLinks.slice(0, 80)) {
  const bindings = (script.formalApplicability.bindings ?? []).map((binding) => binding.model + (binding.actions?.length ? ' [' + binding.actions.join(', ') + ']' : '')
    + (binding.scheduleIds?.length ? ' schedule=' + binding.scheduleIds.join(',') : '')
    + (binding.journeyIds?.length ? ' journey=' + binding.journeyIds.join(',') : '')).join('; ');
  lines.push('- `' + script.path + '`: ' + script.formalApplicability.kind + (bindings ? ' → ' + bindings : '')
    + (script.formalApplicability.limitation ? '; gap: ' + script.formalApplicability.limitation : ''));
}
if (formalLinks.length > 80) lines.push('- ' + (formalLinks.length - 80) + ' additional formal applicability rows are in the JSON report.');
if (formalLinks.length === 0) lines.push('No file is currently marked as formally applicable or a candidate.');
lines.push('');
lines.push('These are source associations, not passing runs. Model actions, invariants, schedule IDs, and journey IDs are listed per link in JSON. The report does not promote an obligation to covered from a label; coverage still requires the existing scoped evidence.');
lines.push('');
lines.push('### Dispositions and replacement evidence');
lines.push('');
lines.push('| Disposition | Files |');
lines.push('| --- | ---: |');
for (const disposition of uniqueSorted(Object.keys(scriptCatalog.summary.dispositionCounts))) {
  lines.push('| ' + disposition + ' | ' + scriptCatalog.summary.dispositionCounts[disposition] + ' |');
}
lines.push('');
const dispositionReviewRows = scripts.filter((script) => script.disposition.kind !== 'keep');
lines.push('| MJS file | Lifecycle / disposition | Purpose and decision reason | Replacement paths and evidence | Remaining proof gap |');
lines.push('| --- | --- | --- | --- | --- |');
const markdownCell = (value) => String(value ?? '').replaceAll('|', '\\|').replace(/\s+/g, ' ').trim();
for (const script of dispositionReviewRows) {
  const replacement = [
    ...(script.replacementProof?.paths ?? []),
    ...(script.replacementProof?.evidence ?? []).map((evidence) => evidence.path + ' [' + evidence.kind + ']'),
  ];
  lines.push('| `' + markdownCell(script.path) + '` | ' + markdownCell(script.lifecycle + ' / ' + script.disposition.kind)
    + ' | ' + markdownCell(script.purpose + ' ' + script.disposition.reason)
    + ' | ' + markdownCell(replacement.join('; ') || '—')
    + ' | ' + markdownCell(script.replacementProof?.gap || 'No remaining proof gap recorded.') + ' |');
}
if (dispositionReviewRows.length === 0) lines.push('| — | — | All MJS files are marked keep. | — | — |');
lines.push('');
lines.push('Disposition labels and replacement evidence come from the source catalog. They do not turn a candidate into runtime proof; independent oracles and replacement gates remain explicit in purpose, evidence, and any nonempty gap.');
lines.push('');
lines.push(report.scriptInventoryBounds);
lines.push('');
lines.push('## Summary');
lines.push('');
lines.push('| Measure | Count |');
lines.push('| --- | ---: |');
for (const [key, value] of Object.entries(summary)) lines.push('| ' + key + ' | ' + value + ' |');
lines.push('');
lines.push('## Obligations with no exact test');
lines.push('');
for (const obligation of gaps.slice(0, 80)) lines.push('- `' + obligation.id + '` (' + obligation.routes.length + ' route(s))');
if (gaps.length > 80) lines.push('- ' + (gaps.length - 80) + ' more in the JSON report.');
lines.push('');
lines.push('## Obligations with exact tests');
lines.push('');
for (const obligation of covered.slice(0, 80)) {
  lines.push('- `' + obligation.id + '`: ' + obligation.tests.map((test) => test.kind + ' `' + test.name + '`').join(', '));
}
if (covered.length > 80) lines.push('- ' + (covered.length - 80) + ' more in the JSON report.');
lines.push('');
lines.push('## Obligations with multiple tests');
lines.push('');
for (const entry of obligationDuplicates.slice(0, 80)) {
  const tests = Object.entries(entry.byLevel).flatMap(([level, ids]) => ids.map((id) => level + ':' + id));
  lines.push('- `' + entry.obligation + '`: ' + Object.values(entry.byLevel).reduce((sum, ids) => sum + ids.length, 0) + ' tests (' + tests.join(', ') + ')');
}
if (obligationDuplicates.length === 0) lines.push('No obligation has more than one test at the same level.');
lines.push('');
lines.push('## Duplicate test names');
lines.push('');
for (const entry of duplicateTestNames.slice(0, 40)) lines.push('- `' + entry.name + '`: ' + entry.tests.length + ' tests (' + entry.tests.join(', ') + ')');
if (duplicateTestNames.length === 0) lines.push('No duplicate test names found.');
lines.push('');
lines.push('## Repeated Playwright checks');
lines.push('');
for (const entry of duplicateChecks.slice(0, 40)) lines.push('- `' + entry.check + '`: ' + entry.tests.length + ' cases (' + entry.tests.join(', ') + ')');
if (duplicateChecks.length === 0) lines.push('No Playwright check is reused across cases.');
lines.push('');
lines.push('## Tests with no exact obligation');
lines.push('');
for (const test of unmappedTests.slice(0, 80)) {
  const suspects = test.suspectedModels?.length ? '; suspected models: ' + test.suspectedModels.join(', ') : '';
  const actions = test.suspectedActions?.length ? '; suspected actions: ' + test.suspectedActions.join(', ') : '';
  lines.push('- `' + test.kind + '` `' + test.name + '` in `' + test.file + '`' + suspects + actions);
}
if (unmappedTests.length > 80) lines.push('- ' + (unmappedTests.length - 80) + ' more in the JSON report.');
lines.push('');
lines.push('## Shared route proof evidence');
lines.push('');
for (const entry of sharedProofs) lines.push('- `' + entry.test + '`: ' + entry.routes.join(', '));
if (sharedProofs.length === 0) lines.push('No shared route proof evidence found.');
lines.push('');
lines.push('## Bounds');
lines.push('');
lines.push('The test index is heuristic. Registry endpoints, Go route strings, and route proofs are strong links. Vitest test names are keyword links. The report marks the mapping on every test.');
lines.push('');

if (check) {
  if (!existsSync(resolve(ROOT, markdownPath)) || readFileSync(resolve(ROOT, markdownPath), 'utf8') !== lines.join('\n')) {
    fail(markdownPath + ' is stale; run node scripts/build-test-obligations.mjs');
  }
} else {
  writeFileSync(resolve(ROOT, markdownPath), lines.join('\n'));
  console.log('wrote ' + jsonPath + ' and ' + markdownPath + ' with ' + obligations.length + ' obligations and ' + tests.size + ' tests');
}
