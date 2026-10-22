#!/usr/bin/env node
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const ROOT = realpathSync(resolve(SCRIPT_DIR, '..'));
const MODEL_DIR = 'verification/builder-interactions';
const CONTRACT_RELATIVE = MODEL_DIR + '/builder-trace-contract.json';
const SOURCE_MANIFEST_RELATIVE = MODEL_DIR + '/builder-preview-contract.json';
const TLC_VERSION = '1.7.4';
const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';
const TLC_URL = 'https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar';
const DEFAULT_EXPECTED_TRACE_COUNT = 69;
const DEFAULT_EXPECTED_SCHEDULE_COUNT = 69;
const DEFAULT_EXPECTED_FINDING_COUNT = 0;
const STATUSES = new Set([
  'idle',
  'previewing',
  'ready',
  'error',
  'needs-repair',
  'applying',
  'requesting',
  'succeeded',
  'stale',
  'conflict',
  'invalid',
  'failed',
]);
const MODEL_STATUSES = new Map([
  ['BuilderJourney', new Set(['idle', 'selected', 'reshaping', 'pivot-ready', 'previewing', 'ready', 'error'])],
]);

const OBSERVATION_BOOLEAN_FIELDS = [
  'canApply',
  'proposalIdPresent',
  'previewReceiptPresent',
  'rowsPresent',
  'receiptPresent',
];
const BUILDER_JOURNEY_PREVIEW_SURFACES = new Set(['row-operation', 'main-table', 'unknown']);

function fail(message) {
  throw new Error(message);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function readJson(pathOrRelative, root = ROOT) {
  return JSON.parse(readFileSync(resolve(root, pathOrRelative), 'utf8'));
}

function canonicalJson(value) {
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  if (value && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

function currentSourceCommit() {
  const result = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' });
  if (result.error) fail('cannot resolve current Git HEAD: ' + result.error.message);
  if (result.status !== 0) fail('cannot resolve current Git HEAD: ' + (result.stderr || result.stdout || '').trim());
  const commit = result.stdout.trim();
  if (!/^[0-9a-f]{40}$/.test(commit)) fail('current Git HEAD is not a 40-character lowercase SHA');
  return commit;
}

function validateSourceProvenance(payload, contract) {
  if (payload.sourceProfile !== contract.sourceProfile) {
    fail('trace sourceProfile ' + String(payload.sourceProfile) + ' does not match contract sourceProfile ' + contract.sourceProfile);
  }
  if (typeof payload.sourceCommit !== 'string' || !/^[0-9a-f]{40}$/.test(payload.sourceCommit)) {
    fail('trace sourceCommit must be a 40-character lowercase Git SHA');
  }
  const head = currentSourceCommit();
  if (payload.sourceCommit !== head) {
    fail('trace sourceCommit ' + payload.sourceCommit + ' does not match current Git HEAD ' + head);
  }
  if (typeof payload.contractSourceCommit !== 'string' || !/^[0-9a-f]{40}$/.test(payload.contractSourceCommit)) {
    fail('trace contractSourceCommit must be a 40-character lowercase Git SHA');
  }
  if (payload.contractSourceCommit !== contract.sourceCommit) {
    fail('trace contractSourceCommit ' + payload.contractSourceCommit + ' does not match selected contract sourceCommit ' + contract.sourceCommit);
  }
  if (typeof payload.sourceFingerprint !== 'string' || !/^[0-9a-f]{64}$/.test(payload.sourceFingerprint)) {
    fail('trace sourceFingerprint must be a 64-character lowercase SHA-256');
  }

  const sourceManifest = readJson(SOURCE_MANIFEST_RELATIVE);
  if (!Array.isArray(sourceManifest.sources) || sourceManifest.sources.length === 0) {
    fail('reviewed source manifest must contain a non-empty sources array');
  }
  const seenPaths = new Set();
  const actualEntries = sourceManifest.sources.map((source, index) => {
    if (!isPlainObject(source) || typeof source.path !== 'string' || typeof source.sha256 !== 'string' ||
        !/^[0-9a-f]{64}$/.test(source.sha256)) {
      fail('reviewed source manifest sources[' + index + '] must contain path and SHA-256');
    }
    if (seenPaths.has(source.path)) fail('reviewed source manifest repeats path ' + source.path);
    seenPaths.add(source.path);
    const absolute = resolve(ROOT, source.path);
    const local = relative(ROOT, absolute);
    if (!local || local.startsWith('..') || resolve(ROOT, local) !== absolute) {
      fail('reviewed source path escapes repository root: ' + source.path);
    }
    if (!existsSync(absolute)) fail('reviewed source file is missing: ' + source.path);
    const actual = sha256(readFileSync(absolute));
    if (actual !== source.sha256) {
      fail('reviewed source SHA mismatch at ' + source.path + ': expected ' + source.sha256 + ', found ' + actual);
    }
    return { path: source.path, sha256: actual };
  }).sort((left, right) => left.path.localeCompare(right.path));
  const fingerprint = sha256(canonicalJson(actualEntries));
  if (payload.sourceFingerprint !== fingerprint) {
    fail('trace sourceFingerprint ' + payload.sourceFingerprint + ' does not match reviewed source content ' + fingerprint);
  }
  return { profile: contract.sourceProfile, head, sourceFingerprint: fingerprint, sourceCount: actualEntries.length };
}

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function verifyJava() {
  const result = spawnSync('java', ['-version'], { encoding: 'utf8' });
  if (result.error) fail('Java is required to run TLC ' + TLC_VERSION + ': ' + result.error.message);
  if (result.status !== 0) fail('Java is required to run TLC ' + TLC_VERSION + ': ' + (result.stderr || result.stdout || '').trim());
  return (result.stderr || result.stdout || '').trim().split('\n')[0];
}

function verifyJar(path) {
  if (!existsSync(path)) fail('TLC jar does not exist at ' + path);
  const absolute = realpathSync(path);
  const actual = sha256(readFileSync(absolute));
  if (actual !== TLC_SHA256) {
    fail('TLC ' + TLC_VERSION + ' checksum mismatch at ' + absolute + ': expected ' + TLC_SHA256 + ', found ' + actual);
  }
  return absolute;
}

async function acquireTlcJar() {
  if (process.env.TLA2TOOLS_JAR) {
    return verifyJar(process.env.TLA2TOOLS_JAR);
  }
  const cacheDirectory = join(tmpdir(), 'loom-builder-interactions-tools');
  const cachePath = join(cacheDirectory, 'tla2tools-' + TLC_VERSION + '.jar');
  if (existsSync(cachePath)) return verifyJar(cachePath);
  console.log('Downloading TLC ' + TLC_VERSION + ' from the official TLA+ release URL.');
  const response = await fetch(TLC_URL, { signal: AbortSignal.timeout(30000) });
  if (!response.ok) fail('TLC download failed with HTTP ' + response.status);
  const payload = Buffer.from(await response.arrayBuffer());
  const actual = sha256(payload);
  if (actual !== TLC_SHA256) fail('downloaded TLC checksum mismatch: expected ' + TLC_SHA256 + ', found ' + actual);
  mkdirSync(cacheDirectory, { recursive: true });
  const temporaryPath = cachePath + '.partial-' + process.pid + '-' + randomUUID();
  writeFileSync(temporaryPath, payload, { flag: 'wx' });
  renameSync(temporaryPath, cachePath);
  return verifyJar(cachePath);
}

function parseArgs() {
  const args = process.argv.slice(2);
  const options = { allowPartial: false, quiet: false, traces: null, output: null, contract: null };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--allow-partial') options.allowPartial = true;
    else if (arg === '--quiet') options.quiet = true;
    else if (arg === '--traces') options.traces = args[++index];
    else if (arg === '--output') options.output = args[++index];
    else if (arg === '--contract') options.contract = args[++index];
    else if (arg === '--help' || arg === '-h') {
      console.log('usage: node scripts/verify-builder-traces.mjs --traces <traces.json> [--output <report.json>] [--contract <contract.json>] [--allow-partial] [--quiet]');
      process.exit(0);
    } else {
      fail('unknown argument ' + arg + '; use --help for usage');
    }
  }
  if (!options.traces) fail('missing --traces <traces.json>');
  return options;
}

function validateContract(contract) {
  if (!isPlainObject(contract)) fail('trace contract must be a JSON object');
  if (contract.schemaVersion !== 1) fail('trace contract schemaVersion must be 1');
  if (typeof contract.sourceCommit !== 'string' || !/^[0-9a-f]{40}$/.test(contract.sourceCommit)) {
    fail('trace contract sourceCommit must be a 40-character lowercase Git SHA');
  }
  if (!['formal', 'journey'].includes(contract.sourceProfile)) {
    fail('trace contract sourceProfile must be "formal" or "journey"');
  }
  if (!isPlainObject(contract.models)) fail('trace contract models must be an object');
  for (const [model, definition] of Object.entries(contract.models)) {
    if (!isPlainObject(definition)) fail('trace contract model ' + model + ' must be an object');
    if (!Array.isArray(definition.actions) || definition.actions.length === 0) {
      fail('trace contract model ' + model + ' must list actions');
    }
    if (definition.actions.some((action) => typeof action !== 'string' || action.length === 0)) {
      fail('trace contract model ' + model + ' has a non-string action');
    }
  }
  if (!isPlainObject(contract.actionBindings)) fail('trace contract actionBindings must be an object');
  for (const [label, binding] of Object.entries(contract.actionBindings)) {
    if (!isPlainObject(binding) || typeof binding.event !== 'string' || binding.event.length === 0) {
      fail('trace contract actionBinding ' + label + ' must have an event');
    }
  }
  if (contract.unmodeledEventPolicy !== undefined) {
    if (!isPlainObject(contract.unmodeledEventPolicy)) {
      fail('trace contract unmodeledEventPolicy must be an object when present');
    }
    for (const [kind, reason] of Object.entries(contract.unmodeledEventPolicy)) {
      if (typeof reason !== 'string' || reason.trim().length === 0) {
        fail('trace contract unmodeledEventPolicy ' + kind + ' must have a reason');
      }
    }
  }
  if (!isPlainObject(contract.phase1)) fail('trace contract phase1 must be an object');
  const phase1 = contract.phase1;
  if (!Array.isArray(phase1.requiredActions) || phase1.requiredActions.some((action) => typeof action !== 'string')) {
    fail('trace contract phase1.requiredActions must be an array of strings');
  }
  if (!Array.isArray(phase1.requiredEvents) || phase1.requiredEvents.some((event) => typeof event !== 'string')) {
    fail('trace contract phase1.requiredEvents must be an array of strings');
  }
  if (phase1.requiredModels !== undefined && (!Array.isArray(phase1.requiredModels) || phase1.requiredModels.some((model) => typeof model !== 'string'))) {
    fail('trace contract phase1.requiredModels must be an array of strings when present');
  }
  for (const model of phase1.requiredModels ?? []) {
    if (!contract.models[model]) fail('trace contract phase1.requiredModels names unknown model ' + model);
  }
  return contract;
}

function expectedCounts(contract) {
  const phase1 = contract.phase1;
  const scheduleCount = Number.isInteger(phase1.expectedScheduleCount)
    ? phase1.expectedScheduleCount : DEFAULT_EXPECTED_SCHEDULE_COUNT;
  const findingCount = Number.isInteger(phase1.expectedFindingCount)
    ? phase1.expectedFindingCount : DEFAULT_EXPECTED_FINDING_COUNT;
  const traceCount = Number.isInteger(phase1.expectedTraceCount)
    ? phase1.expectedTraceCount : DEFAULT_EXPECTED_TRACE_COUNT;
  return { scheduleCount, findingCount, traceCount };
}

function allowedEvents(contract) {
  const events = new Set(Object.values(contract.actionBindings).map((binding) => binding.event));
  for (const event of contract.phase1.requiredEvents) events.add(event);
  return events;
}

function normalizedActionLabel(model, label, actions) {
  if (actions.has(label)) return label;
  // BuilderPreview's DiscoveryComplete action records its outcome in lastAction.
  if (model === 'BuilderPreview' && /^DiscoveryComplete\(/.test(label) && actions.has('DiscoveryComplete')) {
    return 'DiscoveryComplete';
  }
  return null;
}

function ownerProblem(owner, where, expectedKind = null, expectedAttempt = null) {
  if (!isPlainObject(owner)) return where + ' must be an object';
  if (!['proposal', 'preview', 'apply', 'discovery'].includes(owner.kind)) {
    return where + '.kind must be proposal, preview, apply, or discovery';
  }
  const attempts = owner.kind === 'discovery' ? new Set(['D1']) : new Set(['P1', 'P2']);
  if (!attempts.has(owner.attempt)) return where + '.attempt is not a bounded attempt for ' + owner.kind;
  if (expectedKind && owner.kind !== expectedKind) {
    return where + '.kind must be ' + expectedKind + ' for this action';
  }
  if (expectedAttempt && owner.attempt !== expectedAttempt) {
    return where + '.attempt ' + owner.attempt + ' does not match action attempt ' + expectedAttempt;
  }
  return null;
}

function requiredOwnerForAction(model, label) {
  if (model === 'ConstructionLifecycle') {
    if (/^(?:StartProposal\(P[12]\)|ReplaceProposalOwner|RetryProposal\(P1-to-P2\)|CancelProposalOwner|ProposalComplete\(P[12]\)|ProposalError\(P[12]\)|LateProposal(?:Error|Complete)Ignored\(P[12]\)|StartPreview\(P[12]\)|Preview(?:Error|Complete)\(P[12]\)|LatePreview(?:Error|Complete)Ignored\(P[12]\)|BeginApply\((?:accepted|rejected)\))$/.test(label)) return true;
    return false;
  }
  if (model === 'BuilderPreview') {
    return new Set([
      'StartProposal', 'ReplaceProposal', 'ProposalComplete', 'StartPreview',
      'PreviewComplete', 'BeginApply', 'ApplyAck', 'ScheduleDiscovery', 'DiscoveryComplete',
    ]).has(label);
  }
  if (model === 'BuilderJourney') {
    if (/^(?:Preview|PreviewComplete|PreviewError|LatePreviewCompleteIgnored|LatePreviewErrorIgnored)$/.test(label)) return true;
    return new Set([
      'Group', 'GroupAgain', 'AddColumn', 'EditGroup', 'RemoveGroup',
    ]).has(label);
  }
  return false;
}

function expectedOwnerKind(model, label) {
  if (model === 'ConstructionLifecycle') {
    if (/^(?:StartProposal|ReplaceProposalOwner|RetryProposal|CancelProposalOwner|ProposalComplete|ProposalError|LateProposal)/.test(label)) return 'proposal';
    if (/^(?:StartPreview|PreviewComplete|PreviewError|LatePreview)/.test(label)) return 'preview';
    if (/^BeginApply/.test(label)) return 'proposal';
  }
  if (model === 'BuilderPreview') {
    if (/^(?:StartProposal|ReplaceProposal|ProposalComplete)/.test(label)) return 'proposal';
    if (/^(?:StartPreview|PreviewComplete)/.test(label)) return 'preview';
    if (/^(?:BeginApply|ApplyAck)/.test(label)) return 'apply';
    if (/^(?:ScheduleDiscovery|DiscoveryComplete)/.test(label)) return 'discovery';
  }
  if (model === 'BuilderJourney') {
    if (/^(?:Preview|PreviewComplete|PreviewError|LatePreviewCompleteIgnored|LatePreviewErrorIgnored)$/.test(label)) return 'proposal';
    if (/^(?:Group|GroupAgain|AddColumn|EditGroup|RemoveGroup)$/.test(label)) return 'apply';
  }
  return null;
}

function requestProblem(request, where) {
  if (!isPlainObject(request)) return where + ' must be an object';
  if (typeof request.method !== 'string' || typeof request.path !== 'string') {
    return where + ' must include string method and path';
  }
  if (request.requestId !== undefined && request.requestId !== null && typeof request.requestId !== 'string') {
    return where + '.requestId must be a string or null when present';
  }
  if (request.captureId !== undefined && typeof request.captureId !== 'string') return where + '.captureId must be a string when present';
  return null;
}

function responseProblem(response, where) {
  if (!isPlainObject(response)) return where + ' must be an object';
  if (!(typeof response.status === 'string' || typeof response.status === 'number')) {
    return where + '.status must be a string or number';
  }
  if (response.requestId !== undefined && response.requestId !== null && typeof response.requestId !== 'string') return where + '.requestId must be a string or null when present';
  if (response.captureId !== undefined && typeof response.captureId !== 'string') return where + '.captureId must be a string when present';
  if (response.proposalId !== undefined && response.proposalId !== null && typeof response.proposalId !== 'string') {
    return where + '.proposalId must be a string or null when present';
  }
  for (const field of ['proposalOutputId', 'proposalSnapshotToken', 'proposalDraftDigest', 'proposalPreviewStatus']) {
    if (response[field] !== undefined && response[field] !== null && typeof response[field] !== 'string') {
      return where + '.' + field + ' must be a string or null when present';
    }
  }
  if (response.proposalDraftVersion !== undefined && response.proposalDraftVersion !== null && typeof response.proposalDraftVersion !== 'number') {
    return where + '.proposalDraftVersion must be a number or null when present';
  }
  if (response.errorCode !== undefined && response.errorCode !== null && typeof response.errorCode !== 'string') {
    return where + '.errorCode must be a string or null when present';
  }
  if (response.preview !== undefined && response.preview !== null) {
    if (!isPlainObject(response.preview)) return where + '.preview must be an object or null';
    for (const field of ['receiptId', 'outputId']) {
      if (response.preview[field] !== undefined && response.preview[field] !== null && typeof response.preview[field] !== 'string') {
        return where + '.preview.' + field + ' must be a string or null';
      }
    }
    if (response.preview.rowCount !== undefined && response.preview.rowCount !== null && typeof response.preview.rowCount !== 'number') {
      return where + '.preview.rowCount must be a number or null';
    }
    if (response.preview.rowsPresent !== undefined && typeof response.preview.rowsPresent !== 'boolean') {
      return where + '.preview.rowsPresent must be a boolean';
    }
  }
  return null;
}

function journeyPreviewEvidenceProblem(action, where) {
  if (action.label !== 'PreviewComplete' || action.observation?.status !== 'ready' || action.response === null) return null;
  const response = action.response;
  const observation = action.observation;
  const embedded = observation.embeddedProposalPreview;
  if (typeof response.proposalId !== 'string' || response.proposalId.length === 0 ||
      typeof observation.proposalId !== 'string' || observation.proposalId.length === 0 ||
      response.proposalId !== observation.proposalId) {
    return where + ' response.proposalId does not match displayed observation.proposalId';
  }
  if (!['row-operation', 'main-table'].includes(observation.proposalPreviewSurface)) {
    return where + ' ready proposal has no matching rendered proposal preview surface';
  }
  const pairs = [
    ['proposalOutputId', observation.proposalOutputId, 'response.proposalOutputId does not match displayed observation.proposalOutputId'],
    ['proposalSnapshotToken', observation.proposalSnapshotToken, 'response.proposalSnapshotToken does not match displayed observation.proposalSnapshotToken'],
    ['proposalDraftVersion', observation.proposalDraftVersion, 'response.proposalDraftVersion does not match displayed observation.proposalDraftVersion'],
    ['proposalDraftDigest', observation.proposalDraftDigest, 'response.proposalDraftDigest does not match displayed observation.proposalDraftDigest'],
  ];
  for (const [responseField, observedValue, message] of pairs) {
    const responseValue = response[responseField];
    if (responseValue !== undefined && responseValue !== null &&
        (observedValue === undefined || observedValue === null || responseValue !== observedValue)) {
      return where + ' ' + message;
    }
  }
  if (isPlainObject(response.preview)) {
    const previewPairs = [
      ['receiptId', embedded?.receiptId, 'preview.receiptId', 'embeddedProposalPreview.receiptId'],
      ['outputId', embedded?.outputId, 'preview.outputId', 'embeddedProposalPreview.outputId'],
    ];
    for (const [responseField, observedValue, responseName, observedName] of previewPairs) {
      const responseValue = response.preview[responseField];
      if (responseValue !== undefined && responseValue !== null &&
          (observedValue === undefined || observedValue === null || responseValue !== observedValue)) {
        return where + ' response.' + responseName + ' does not match displayed observation.' + observedName;
      }
    }
    const renderedRows = embedded?.rowsPresent;
    if (typeof response.preview.rowsPresent === 'boolean' &&
        (typeof renderedRows !== 'boolean' || response.preview.rowsPresent !== renderedRows)) {
      return where + ' response.preview.rowsPresent does not match displayed observation.embeddedProposalPreview.rowsPresent';
    }
  }
  return null;
}

function settlementProblem(settlement, where) {
  if (!isPlainObject(settlement)) return where + ' must be an object';
  if (!['http-response', 'request-failed'].includes(settlement.kind)) {
    return where + '.kind must be http-response or request-failed';
  }
  if (typeof settlement.captureId !== 'string' || settlement.captureId.length === 0) {
    return where + '.captureId must be a non-empty string';
  }
  if (settlement.errorText !== undefined && typeof settlement.errorText !== 'string') {
    return where + '.errorText must be a string when present';
  }
  return null;
}

function observationProblem(observation, where, model) {
  if (!isPlainObject(observation)) return where + ' must be an object';
  if (typeof observation.status !== 'string') return where + '.status must be a string';
  const statuses = MODEL_STATUSES.get(model) ?? STATUSES;
  if (!(statuses.has(observation.status) || (model === 'BuilderJourney' && ['absent', 'unknown'].includes(observation.status)))) {
    return where + '.status "' + observation.status + '" has no model observation mapping for ' + model;
  }
  if (observation.tablePreviewStatus !== undefined && typeof observation.tablePreviewStatus !== 'string') {
    return where + '.tablePreviewStatus must be a string when present';
  }
  for (const field of OBSERVATION_BOOLEAN_FIELDS) {
    if (typeof observation[field] !== 'boolean') return where + '.' + field + ' must be a boolean';
  }
  const identityFields = ['proposalId', 'proposalOutputId', 'proposalSnapshotToken', 'proposalDraftDigest', 'previewReceiptId', 'previewOutputId'];
  for (const field of identityFields) {
    if (observation[field] !== undefined && observation[field] !== null && typeof observation[field] !== 'string') {
      return where + '.' + field + ' must be a string or null';
    }
  }
  if (observation.proposalDraftVersion !== undefined && observation.proposalDraftVersion !== null && typeof observation.proposalDraftVersion !== 'number') {
    return where + '.proposalDraftVersion must be a number or null';
  }
  if (model !== 'BuilderJourney' && identityFields.some((field) => observation[field] === undefined) ||
      model !== 'BuilderJourney' && observation.proposalDraftVersion === undefined) {
    return where + ' must include explicit null/string hook proposal and preview identity fields';
  }
  for (const [field, nested] of [['sourceTablePreview', observation.sourceTablePreview], ['embeddedProposalPreview', observation.embeddedProposalPreview]]) {
    if (nested === undefined || nested === null) continue;
    if (!isPlainObject(nested)) return where + '.' + field + ' must be an object or null';
    if (nested.status !== undefined && nested.status !== null && typeof nested.status !== 'string') return where + '.' + field + '.status must be a string or null';
    for (const idField of ['receiptId', 'outputId', 'proposalId', 'draftDigest']) {
      if (nested[idField] !== undefined && nested[idField] !== null && typeof nested[idField] !== 'string') {
        return where + '.' + field + '.' + idField + ' must be a string or null';
      }
    }
    for (const numberField of ['draftVersion', 'rowCount', 'renderedRowCount']) {
      if (nested[numberField] !== undefined && nested[numberField] !== null && typeof nested[numberField] !== 'number') {
        return where + '.' + field + '.' + numberField + ' must be a number or null';
      }
    }
    if (nested.rowsPresent !== undefined && typeof nested.rowsPresent !== 'boolean') return where + '.' + field + '.rowsPresent must be a boolean';
  }
  if (observation.apply !== undefined && observation.apply !== null) {
    if (!isPlainObject(observation.apply)) return where + '.apply must be an object or null';
    for (const field of ['available', 'enabled', 'applying']) {
      if (observation.apply[field] !== undefined && observation.apply[field] !== null && typeof observation.apply[field] !== 'boolean') {
        return where + '.apply.' + field + ' must be a boolean or null';
      }
    }
  }
  if (model === 'BuilderJourney') {
    const surface = observation.proposalPreviewSurface;
    if (typeof surface !== 'string' || !BUILDER_JOURNEY_PREVIEW_SURFACES.has(surface)) {
      return where + '.proposalPreviewSurface must be row-operation, main-table, or unknown';
    }
    if (!Object.hasOwn(observation, 'embeddedProposalPreview')) {
      return where + '.embeddedProposalPreview must be explicit for the selected proposal preview surface';
    }
    const embedded = observation.embeddedProposalPreview;
    if (surface === 'unknown') {
      if (embedded !== null) {
        return where + '.proposalPreviewSurface "unknown" cannot claim an embedded proposal preview';
      }
      if (observation.previewReceiptPresent || observation.rowsPresent || observation.receiptPresent ||
          observation.previewReceiptId !== null || observation.previewOutputId !== null) {
        return where + '.proposalPreviewSurface "unknown" cannot populate proposal preview fields';
      }
    } else {
      if (!isPlainObject(embedded)) {
        return where + '.proposalPreviewSurface ' + JSON.stringify(surface) + ' requires an embedded proposal preview';
      }
      if (typeof observation.proposalId !== 'string' || observation.proposalId.length === 0) {
        return where + '.proposalPreviewSurface ' + JSON.stringify(surface) + ' requires a rendered proposalId';
      }
      if (surface === 'main-table') {
        const rawMainPreview = observation.sourceTablePreview;
        const rawMainProposalId = rawMainPreview?.proposalId;
        if (typeof rawMainProposalId !== 'string' || rawMainProposalId.length === 0) {
          return where + '.main-table proposal preview requires sourceTablePreview.proposalId';
        }
        if (rawMainProposalId !== observation.proposalId) {
          return where + '.main-table sourceTablePreview.proposalId does not match displayed proposalId';
        }
        for (const field of ['status', 'receiptId', 'outputId', 'rowsPresent']) {
          if (!Object.hasOwn(rawMainPreview, field) || !Object.hasOwn(embedded, field) ||
              rawMainPreview[field] !== embedded[field]) {
            return where + '.main-table embeddedProposalPreview.' + field + ' does not match raw sourceTablePreview.' + field;
          }
        }
        if (rawMainPreview.rowCount !== undefined && rawMainPreview.rowCount !== null &&
            rawMainPreview.rowCount !== embedded.rowCount) {
          return where + '.main-table embeddedProposalPreview.rowCount does not match raw sourceTablePreview.rowCount';
        }
      }
      if (embedded.proposalId !== observation.proposalId) {
        return where + '.embeddedProposalPreview.proposalId does not match displayed proposalId';
      }
      if (observation.previewReceiptPresent !== Boolean(embedded.receiptId) ||
          observation.receiptPresent !== Boolean(embedded.receiptId) ||
          observation.rowsPresent !== Boolean(embedded.rowsPresent) ||
          observation.previewReceiptId !== (embedded.receiptId ?? null) ||
          observation.previewOutputId !== (embedded.outputId ?? null)) {
        return where + '.proposal preview fields must be flattened from the selected surface only';
      }
    }
  }
  return null;
}

function traceShapeProblem(trace, index, contract, events) {
  const where = 'traces[' + index + ']';
  if (!isPlainObject(trace)) return { message: where + ' must be an object' };
  if (typeof trace.name !== 'string' || trace.name.length === 0) return { message: where + '.name must be a non-empty string' };
  if (typeof trace.model !== 'string' || !contract.models[trace.model]) {
    return { message: where + '.model "' + String(trace.model) + '" is not defined by the contract' };
  }
  const actions = new Set(contract.models[trace.model].actions);
  if (!Array.isArray(trace.actions)) return { message: where + '.actions must be an array' };
  if (trace.unmodeledEvents !== undefined) {
    if (!Array.isArray(trace.unmodeledEvents)) {
      return { message: where + '.unmodeledEvents must be an array' };
    }
    for (let eventIndex = 0; eventIndex < trace.unmodeledEvents.length; eventIndex += 1) {
      const event = trace.unmodeledEvents[eventIndex];
      const eventWhere = where + '.unmodeledEvents[' + eventIndex + ']';
      if (!isPlainObject(event) || typeof event.kind !== 'string' || typeof event.note !== 'string' ||
          event.classification !== 'unsupported' || typeof event.reason !== 'string' || event.reason.trim().length === 0) {
        return { message: eventWhere + ' must include kind, note, classification "unsupported", and a non-empty reason' };
      }
    }
  }
  const initialProblem = observationProblem(trace.initialObservation, where + '.initialObservation', trace.model);
  if (initialProblem) return { message: initialProblem };
  if (trace.initialPreview !== undefined) {
    if (!isPlainObject(trace.initialPreview)) return { message: where + '.initialPreview must be an object' };
    const ownerProblemText = ownerProblem(trace.initialPreview.owner, where + '.initialPreview.owner', 'preview');
    if (ownerProblemText) return { message: ownerProblemText };
    const requestProblemText = requestProblem(trace.initialPreview.request, where + '.initialPreview.request');
    if (requestProblemText) return { message: requestProblemText };
    const responseProblemText = responseProblem(trace.initialPreview.response, where + '.initialPreview.response');
    if (responseProblemText) return { message: responseProblemText };
    const settlementProblemText = settlementProblem(trace.initialPreview.settlement, where + '.initialPreview.settlement');
    if (settlementProblemText) return { message: settlementProblemText };
    const sourcePreview = trace.initialObservation.sourceTablePreview;
    const responsePreview = trace.initialPreview.response.preview;
    if (trace.initialObservation.tablePreviewStatus === 'ready') {
      if (!isPlainObject(sourcePreview) || !isPlainObject(responsePreview)) {
        return { message: where + '.initialPreview must retain both response preview and rendered sourceTablePreview evidence' };
      }
      for (const [domField, responseField] of [['receiptId', 'receiptId'], ['outputId', 'outputId']]) {
        const domValue = sourcePreview[domField];
        const responseValue = responsePreview[responseField];
        if (typeof domValue !== 'string' || domValue.length === 0 ||
            typeof responseValue !== 'string' || responseValue.length === 0 || domValue !== responseValue) {
          return { message: where + '.initialPreview response ' + responseField + ' must match rendered sourceTablePreview ' + domField };
        }
      }
      if (sourcePreview.rowsPresent !== true || responsePreview.rowsPresent !== true) {
        return { message: where + '.initialPreview ready evidence requires rendered source and response rows' };
      }
    }
    const captureId = trace.initialPreview.request.captureId;
    const requestId = trace.initialPreview.request.requestId;
    const responseRequestId = trace.initialPreview.response.requestId;
    if (typeof captureId !== 'string' || captureId.length === 0 ||
        trace.initialPreview.response.captureId !== captureId ||
        trace.initialPreview.settlement.captureId !== captureId ||
        trace.initialPreview.settlement.kind !== 'http-response') {
      return { message: where + '.initialPreview request/response captureId must be present and identical' };
    }
    if (requestId !== null && responseRequestId !== null && requestId !== responseRequestId) {
      return { message: where + '.initialPreview non-null wire requestIds do not match' };
    }
  }
  if (trace.model === 'BuilderJourney' && trace.initialObservation.tablePreviewStatus === 'ready' && !trace.initialPreview) {
    return { message: where + '.initialPreview is required for an observed ready source table preview' };
  }
  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    if (!isPlainObject(action)) return { message: actionWhere + ' must be an object', actionIndex };
    if (typeof action.label !== 'string' || action.label.length === 0) {
      return { message: actionWhere + '.label must be a non-empty string', actionIndex };
    }
    if (typeof action.event !== 'string' || action.event.length === 0) {
      return { message: actionWhere + '.event must be a non-empty string', actionIndex };
    }
    const normalizedLabel = normalizedActionLabel(trace.model, action.label, actions);
    if (!normalizedLabel) {
      return { message: actionWhere + '.label "' + action.label + '" is not an action of ' + trace.model, actionIndex };
    }
    if (!events.has(action.event)) {
      return { message: actionWhere + '.event "' + action.event + '" is not defined by the contract', actionIndex };
    }
    const binding = contract.actionBindings[normalizedLabel];
    if (!binding) {
      return { message: actionWhere + '.label "' + action.label + '" has no action binding in the selected contract', actionIndex };
    }
    if (binding.event !== action.event) {
      return {
        message: actionWhere + '.event "' + action.event + '" does not match contract event "' + binding.event + '" for ' + action.label,
        actionIndex,
      };
    }
    const expectedKind = expectedOwnerKind(trace.model, action.label);
    if (action.owner === undefined && requiredOwnerForAction(trace.model, action.label)) {
      return { message: actionWhere + '.owner is required for ' + action.label, actionIndex };
    }
    if (action.owner === null) {
      if (trace.model !== 'ConstructionLifecycle' || action.label !== 'BeginApply(rejected)') {
        return { message: actionWhere + '.owner may be null only for an ownerless rejected BeginApply', actionIndex };
      }
    } else if (action.owner !== undefined) {
      const expectedAttempt = trace.model === 'ConstructionLifecycle'
        ? action.label.match(/\((P[12])\)$/)?.[1] ??
          (action.label === 'ReplaceProposalOwner' || action.label === 'RetryProposal(P1-to-P2)' ? 'P2' : null)
        : trace.model === 'BuilderPreview' && action.label === 'StartProposal'
          ? 'P1'
          : trace.model === 'BuilderPreview' && action.label === 'ReplaceProposal'
            ? 'P2'
            : null;
      const ownerProblemText = ownerProblem(action.owner, actionWhere + '.owner', expectedKind, expectedAttempt);
      if (ownerProblemText) return { message: ownerProblemText, actionIndex };
    }
    if (action.expectedProposal !== undefined) {
      if (!['P1', 'P2'].includes(action.expectedProposal)) {
        return { message: actionWhere + '.expectedProposal must be P1 or P2 when present', actionIndex };
      }
      if (!/^BeginApply(?:\(|$)/.test(action.label)) {
        return { message: actionWhere + '.expectedProposal is only valid on BeginApply actions', actionIndex };
      }
    }
    if (action.request !== undefined) {
      const problem = requestProblem(action.request, actionWhere + '.request');
      if (problem) return { message: problem, actionIndex };
    }
    if (action.response !== undefined && action.response !== null) {
      const problem = responseProblem(action.response, actionWhere + '.response');
      if (problem) return { message: problem, actionIndex };
    }
    if (action.settlement !== undefined) {
      const problem = settlementProblem(action.settlement, actionWhere + '.settlement');
      if (problem) return { message: problem, actionIndex };
    }
    if (trace.model === 'BuilderJourney') {
      if (action.owner !== undefined && action.owner === null) return { message: actionWhere + '.owner cannot be null for a BuilderJourney action', actionIndex };
      if (action.owner !== undefined && !requiredOwnerForAction(trace.model, action.label)) {
        return { message: actionWhere + '.owner is not valid for a local BuilderJourney event', actionIndex };
      }
      const previewSettlement = ['PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored'].includes(action.label);
      if (action.owner && action.request === undefined) return { message: actionWhere + '.request is required for an owned BuilderJourney action', actionIndex };
      if (action.label !== 'Preview' && action.owner && action.response === undefined) {
        return { message: actionWhere + '.response is required for a BuilderJourney settlement action', actionIndex };
      }
      if (action.label !== 'Preview' && action.owner && action.settlement === undefined) {
        return { message: actionWhere + '.settlement is required for a BuilderJourney settlement action', actionIndex };
      }
      if (action.owner && (typeof action.request?.captureId !== 'string' || action.request.captureId.length === 0)) {
        return { message: actionWhere + '.request.captureId is required for an owned BuilderJourney action', actionIndex };
      }
      if (action.owner && action.label !== 'Preview' && action.request?.captureId !== action.settlement?.captureId) {
        return { message: actionWhere + '.request.captureId does not match settlement.captureId', actionIndex };
      }
      if (action.owner && action.label !== 'Preview' && action.response !== null && action.request?.captureId !== action.response?.captureId) {
        return { message: actionWhere + '.request.captureId does not match response.captureId', actionIndex };
      }
      if (action.owner && action.label !== 'Preview' && action.response !== null && action.request?.requestId !== null && action.response?.requestId !== null &&
          action.request?.requestId !== action.response?.requestId) {
        return { message: actionWhere + '.non-null wire requestIds do not match', actionIndex };
      }
      if (action.owner && action.label !== 'Preview' && action.response === null &&
          (!['PreviewError', 'LatePreviewErrorIgnored'].includes(action.label) || action.settlement?.kind !== 'request-failed')) {
        return { message: actionWhere + ' response:null is allowed only for a request-failed preview error settlement', actionIndex };
      }
      if (action.owner && action.label !== 'Preview' && action.response !== null && action.settlement?.kind !== 'http-response') {
        return { message: actionWhere + ' HTTP response requires settlement.kind http-response', actionIndex };
      }
      if (action.owner && action.label !== 'Preview' && action.response === null && action.settlement?.kind !== 'request-failed') {
        return { message: actionWhere + ' response:null requires settlement.kind request-failed', actionIndex };
      }
      if (previewSettlement) {
        const captureId = action.request?.captureId;
        if (typeof captureId !== 'string' || captureId.length === 0 ||
            action.settlement?.captureId !== captureId ||
            (action.response !== null && action.response?.captureId !== captureId)) {
          return { message: actionWhere + ' request/response captureId must be present and identical', actionIndex };
        }
      }
    }
    const observation = observationProblem(action.observation, actionWhere + '.observation', trace.model);
    if (observation) return { message: observation, actionIndex };
    if (trace.model === 'BuilderJourney') {
      const evidenceProblem = journeyPreviewEvidenceProblem(action, actionWhere);
      if (evidenceProblem) return { message: evidenceProblem, actionIndex };
    }
  }
  if (trace.model === 'BuilderJourney') {
    const previewStartByCapture = new Map();
    const pairedPreviewCaptures = new Set();
    const previewSettlementLabels = new Set([
      'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored',
    ]);
    for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
      const action = trace.actions[actionIndex];
      const actionWhere = where + '.actions[' + actionIndex + ']';
      if (action.label === 'Preview') {
        const captureId = action.request?.captureId;
        if (previewStartByCapture.has(captureId)) {
          return { message: actionWhere + '.request.captureId duplicates another Preview request', actionIndex };
        }
        previewStartByCapture.set(captureId, { action, actionIndex });
      }
      if (!previewSettlementLabels.has(action.label)) continue;
      const captureId = action.request?.captureId;
      const start = previewStartByCapture.get(captureId);
      if (!start) {
        return { message: actionWhere + ' has no Preview request with the same captureId', actionIndex };
      }
      if (pairedPreviewCaptures.has(captureId)) {
        return { message: actionWhere + ' duplicates a preview settlement for the same captureId', actionIndex };
      }
      const request = start.action.request;
      const settlementRequest = action.request;
      if (canonicalJson(start.action.owner) !== canonicalJson(action.owner) ||
          request.method !== settlementRequest.method || request.path !== settlementRequest.path ||
          request.captureId !== captureId || settlementRequest.captureId !== captureId) {
        return { message: actionWhere + ' preview settlement must match its Preview owner, method, path, and captureId', actionIndex };
      }
      const wireIds = [request.requestId, settlementRequest.requestId, action.response?.requestId]
        .filter((requestId) => requestId !== null && requestId !== undefined);
      if (wireIds.some((requestId) => requestId !== wireIds[0])) {
        return { message: actionWhere + ' preview settlement has a mismatched non-null wire requestId', actionIndex };
      }
      pairedPreviewCaptures.add(captureId);
    }
    for (const [captureId, start] of previewStartByCapture) {
      if (!pairedPreviewCaptures.has(captureId)) {
        return {
          message: where + '.actions[' + start.actionIndex + '] Preview request has no matching settlement with the same captureId',
          actionIndex: start.actionIndex,
        };
      }
    }
    const requestCaptureIds = new Set();
    const registerCapture = (captureId, location) => {
      if (typeof captureId !== 'string' || captureId.length === 0) return null;
      if (requestCaptureIds.has(captureId)) return location + '.captureId duplicates another request capture';
      requestCaptureIds.add(captureId);
      return null;
    };
    if (trace.initialPreview) {
      const problem = registerCapture(trace.initialPreview.request.captureId, where + '.initialPreview.request');
      if (problem) return { message: problem };
    }
    for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
      const action = trace.actions[actionIndex];
      if (!action.owner || !action.request) continue;
      if (['PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored'].includes(action.label)) continue;
      const problem = registerCapture(action.request.captureId, where + '.actions[' + actionIndex + '].request');
      if (problem) return { message: problem, actionIndex };
    }
  }
  return null;
}

function traceKey(trace) {
  return canonicalJson({
    model: trace.model,
    initialObservation: trace.initialObservation,
    initialPreview: trace.initialPreview ?? null,
    actions: trace.actions.map((action) => ({
      label: action.label,
      event: action.event,
      observation: action.observation,
      owner: action.owner ?? null,
      expectedProposal: action.expectedProposal ?? null,
      request: action.request ?? null,
      response: action.response ?? null,
      settlement: action.settlement ?? null,
    })),
  });
}

function isFindingTrace(trace) {
  if (trace.expectedFailure === true) return true;
  if (trace.kind === 'finding' || trace.kind === 'expected-failure') return true;
  return /\bfinding\b|^finding:/i.test(trace.name);
}

function tlaString(value) {
  return JSON.stringify(value);
}

function tlaBool(value) {
  return value ? 'TRUE' : 'FALSE';
}

function tlaOptionalBoolean(value) {
  if (value === true) return '"true"';
  if (value === false) return '"false"';
  return '"unknown"';
}

function tlaObservedValue(value) {
  if (value === undefined || value === null) return '"NoObservedValue"';
  if (typeof value === 'string') return tlaString(value);
  if (typeof value === 'number') return String(value);
  if (typeof value === 'boolean') return tlaBool(value);
  return '"NoObservedValue"';
}

function nestedObservedValue(observation, objectField, field) {
  const nested = observation[objectField];
  return isPlainObject(nested) ? nested[field] : undefined;
}

function tlaObservation(observation) {
  return '[status |-> ' + tlaObservedValue(observation.status) +
    ', tablePreviewStatus |-> ' + tlaObservedValue(observation.tablePreviewStatus) +
    ', proposalId |-> ' + tlaObservedValue(observation.proposalId) +
    ', proposalOutputId |-> ' + tlaObservedValue(observation.proposalOutputId) +
    ', proposalSnapshotToken |-> ' + tlaObservedValue(observation.proposalSnapshotToken) +
    ', proposalDraftVersion |-> ' + tlaObservedValue(observation.proposalDraftVersion) +
    ', proposalDraftDigest |-> ' + tlaObservedValue(observation.proposalDraftDigest) +
    ', previewReceiptId |-> ' + tlaObservedValue(observation.previewReceiptId) +
    ', previewOutputId |-> ' + tlaObservedValue(observation.previewOutputId) +
    ', sourcePreviewStatus |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'status')) +
    ', sourcePreviewReceiptId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'receiptId')) +
    ', sourcePreviewOutputId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'outputId')) +
    ', sourcePreviewProposalId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'proposalId')) +
    ', sourcePreviewDraftVersion |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'draftVersion')) +
    ', sourcePreviewDraftDigest |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'draftDigest')) +
    ', sourcePreviewRowsPresent |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'sourceTablePreview', 'rowsPresent')) +
    ', sourcePreviewRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'rowCount')) +
    ', sourcePreviewRenderedRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'renderedRowCount')) +
    ', embeddedPreviewStatus |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'status')) +
    ', embeddedPreviewReceiptId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'receiptId')) +
    ', embeddedPreviewOutputId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'outputId')) +
    ', embeddedPreviewProposalId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'proposalId')) +
    ', embeddedPreviewDraftVersion |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'draftVersion')) +
    ', embeddedPreviewDraftDigest |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'draftDigest')) +
    ', embeddedPreviewRowsPresent |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'embeddedProposalPreview', 'rowsPresent')) +
    ', embeddedPreviewRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'rowCount')) +
    ', embeddedPreviewRenderedRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'renderedRowCount')) +
    ', applyAvailable |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'apply', 'available')) +
    ', applyEnabled |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'apply', 'enabled')) +
    ', applying |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'apply', 'applying')) +
    ', canApply |-> ' + tlaBool(observation.canApply) +
    ', proposalIdPresent |-> ' + tlaBool(observation.proposalIdPresent) +
    ', previewReceiptPresent |-> ' + tlaBool(observation.previewReceiptPresent) +
    ', rowsPresent |-> ' + tlaBool(observation.rowsPresent) +
    ', receiptPresent |-> ' + tlaBool(observation.receiptPresent) + ']';
}

function tlaSequence(items) {
  return '<<' + items.map((item) => '  ' + item).join(',\n') + '>>';
}

function builderPreviewObservationMapping(prime) {
  return [
    '  /\\ (obs.status = "previewing" =>',
    '       ((proposalPhase' + prime + ' \\in {"pending", "ready"}) /\\',
    '        (previewPhase' + prime + ' \\in {"none", "pending"})))',
    '  /\\ (obs.status = "ready" =>',
    '       ((proposalPhase' + prime + ' = "ready") /\\ (previewPhase' + prime + ' = "ready")))',
    '  /\\ (obs.status = "error" =>',
    '       ((proposalPhase' + prime + ' = "error") \\/ (previewPhase' + prime + ' = "error")))',
    '  /\\ (obs.status = "needs-repair" =>',
    '       ((proposalPhase' + prime + ' = "error") \\/ (previewPhase' + prime + ' = "error")))',
    '  /\\ (obs.status = "applying" => (applyPhase' + prime + ' = "applying"))',
    '  /\\ (obs.canApply =>',
    '       ((proposalPhase' + prime + ' = "ready") /\\',
    '        (previewPhase' + prime + ' = "ready")))',
    '  /\\ (obs.canApply => (obs.previewReceiptId = obs.proposalId))',
    '  /\\ (obs.canApply => (obs.previewOutputId = obs.proposalOutputId))',
    '  /\\ (obs.previewReceiptPresent => (previewPhase' + prime + ' = "ready"))',
    '  /\\ (obs.rowsPresent => (previewPhase' + prime + ' = "ready"))',
  ].join('\n');
}


function builderJourneyObservationMapping(prime) {
  const preview = 'preview' + prime;
  const proposalIdPresent = 'proposalIdPresent' + prime;
  const tablePreview = 'tablePreview' + prime;
  const sourceRowsPresent = 'sourceRowsPresent' + prime;
  return [
    '  /\\ (obs.status = "previewing" => (' + preview + ' = "pending"))',
    '  /\\ (obs.status = "ready" => (' + preview + ' = "ready"))',
    '  /\\ (obs.status \\in {"error", "needs-repair"} => (' + preview + ' = "error"))',
    '  /\\ (obs.embeddedPreviewStatus = "ready" => (' + preview + ' = "ready"))',
    '  /\\ (obs.embeddedPreviewStatus = "error" => (' + preview + ' = "error"))',
    '  /\\ (obs.tablePreviewStatus = "ready" => (' + tablePreview + ' = "ready"))',
    '  /\\ (obs.tablePreviewStatus = "error" => (' + tablePreview + ' = "error"))',
    '  /\\ (obs.sourcePreviewRowsPresent = "true" => ' + sourceRowsPresent + ')',
    '  /\\ (obs.sourcePreviewRowsPresent = "false" => ~' + sourceRowsPresent + ')',
    '  /\\ obs.proposalIdPresent <=> ' + proposalIdPresent,
    '  /\\ obs.previewReceiptPresent <=> (' + preview + ' = "ready")',
    '  /\\ obs.receiptPresent <=> (' + preview + ' = "ready")',
    '  /\\ obs.rowsPresent <=> rowsPresent' + prime,
    '  /\\ obs.canApply <=> ((' + preview + ' = "ready") /\\ ' + proposalIdPresent + ' /\\ rowsPresent' + prime + ')',
    '  /\\ (obs.canApply => obs.previewReceiptId = obs.proposalId)',
    '  /\\ (obs.canApply => obs.previewOutputId = obs.proposalOutputId)',
    '  /\\ (obs.embeddedPreviewRowsPresent = "true" => rowsPresent' + prime + ')',
    '  /\\ (obs.embeddedPreviewRowsPresent = "false" => ~rowsPresent' + prime + ')',
    '  /\\ (obs.applyEnabled = "true" => obs.applyAvailable = "true")',
    '  /\\ (obs.applying = "true" => obs.applyAvailable = "true")',
  ].join('\n');
}


function reconcileLifecycleObservationMapping(prime) {
  const state = prime ? "r'" : 'r';
  return [
    '  /\\ (obs.status = "idle" => (' + state + '.phase = "idle"))',
    '  /\\ (obs.status = "requesting" => (' + state + '.phase = "requesting"))',
    '  /\\ (obs.status = "succeeded" => (' + state + '.phase = "succeeded"))',
    '  /\\ (obs.status = "stale" => (' + state + '.phase = "stale"))',
    '  /\\ (obs.status = "conflict" => (' + state + '.phase = "conflict"))',
    '  /\\ (obs.status = "invalid" => (' + state + '.phase = "invalid"))',
    '  /\\ (obs.status = "failed" => (' + state + '.phase = "failed"))',
    '  /\\ (obs.receiptPresent => (' + state + '.receipt.id # "NONE"))',
    '  /\\ (~obs.receiptPresent => (' + state + '.receipt.id = "NONE"))',
  ].join('\n');
}

function constructionLifecycleObservationMapping(prime) {
  const applicable = prime ? 'ResourceIsApplicableNext(attempt)' : 'ResourceIsApplicable(attempt)';
  const overrideState = prime ? "overrideState'" : 'overrideState';
  const owner = prime ? "owner'" : 'owner';
  const currentIdentity = prime ? "currentIdentity'" : 'currentIdentity';
  const attemptPhase = 'attemptPhase' + prime;
  const requestIdentity = 'requestIdentity' + prime;
  const proposalIds = 'proposalIdPresent' + prime;
  const previewReceipts = 'previewReceipt' + prime;
  const previewRows = 'previewRowsNonNull' + prime;
  const activeAttempt = (predicate) =>
    '(\\E attempt \\in Attempts: ' + owner + ' = attempt /\\ (' + requestIdentity + ')[attempt] = ' + currentIdentity + ' /\\ ' + predicate + ')';
  const visibleProposalId = activeAttempt('(' + attemptPhase + ')[attempt] \\in {"ready", "needs-repair"} /\\ (' + proposalIds + ')[attempt]');
  const visiblePreviewReceipt = activeAttempt('(' + attemptPhase + ')[attempt] = "ready" /\\ (' + previewReceipts + ')[attempt] # NoReceipt');
  const visiblePreviewRows = activeAttempt('(' + attemptPhase + ')[attempt] = "ready" /\\ (' + previewRows + ')[attempt]');
  return [
    '  /\\ (obs.status = "idle") <=> (' + owner + ' = NoAttempt \\/ (\\E attempt \\in Attempts: ' + owner + ' = attempt /\\ (' + requestIdentity + ')[attempt] # ' + currentIdentity + '))',
    '  /\\ (obs.status = "previewing") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] \\in {"proposal-pending", "preview-pending"}') + ')',
    '  /\\ (obs.status = "ready") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] = "ready"') + ')',
    '  /\\ (obs.status = "error") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] = "error"') + ')',
    '  /\\ (obs.status = "needs-repair") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] = "needs-repair"') + ')',
    '  /\\ (obs.status = "applying") <=> (overrideState' + prime + ' = "applying")',
    '  /\\ obs.canApply <=> (' + activeAttempt(applicable) + ' /\\ ' + overrideState + ' # "applying")',
    '  /\\ (obs.canApply => obs.previewReceiptId = obs.proposalId)',
    '  /\\ (obs.canApply => obs.previewOutputId = obs.proposalOutputId)',
    '  /\\ obs.proposalIdPresent <=> ' + visibleProposalId,
    '  /\\ obs.previewReceiptPresent <=> ' + visiblePreviewReceipt,
    '  /\\ obs.receiptPresent <=> ' + visiblePreviewReceipt,
    '  /\\ obs.rowsPresent <=> ' + visiblePreviewRows,
  ].join('\n');
}

function actionLabelMatches(model) {
  if (model !== 'BuilderPreview') return 'ActionLabelMatches(label) == lastAction\' = label';
  return [
    "ActionLabelMatches(label) ==",
    "  lastAction' = label",
    '  \\/ (label = "DiscoveryComplete"',
    '      /\\ lastAction\' \\in {"DiscoveryComplete(complete)",',
    '                          "DiscoveryComplete(ignored)",',
    '                          "DiscoveryComplete(missing-unsupported)"})',
  ].join('\n');
}

function traceOwnerValue(action) {
  return action.owner && typeof action.owner === 'object' ? action.owner.attempt : 'NoAttempt';
}

function traceExpectedProposalValue(action) {
  return action.expectedProposal ?? 'NoExpectedProposal';
}

function actionOwnerMatches(model) {
  if (model === 'ConstructionLifecycle') {
    return [
      'ActionOwnerMatches(index) ==',
      '  LET label == TraceActions[index]',
      '      attempt == TraceOwnerAttempts[index]',
      '      expected == TraceExpectedProposals[index]',
      '  IN CASE label = "StartProposal(P1)" -> attempt = "P1" /\\ owner\' = Attempt1',
      '       [] label = "StartProposal(P2)" -> attempt = "P2" /\\ owner\' = Attempt2',
      '       [] label = "ReplaceProposalOwner" -> /\\ owner\' = Attempt2 /\\ attempt = "P2"',
      '       [] label = "RetryProposal(P1-to-P2)" -> /\\ owner\' = Attempt2 /\\ attempt = "P2"',
      '       [] label = "CancelProposalOwner" -> owner = IF attempt = "P1" THEN Attempt1 ELSE Attempt2',
      '       [] label \\in {"ProposalComplete(P1)", "ProposalError(P1)", "PreviewComplete(P1)", "PreviewError(P1)", "StartPreview(P1)", "LateProposalCompleteIgnored(P1)", "LateProposalErrorIgnored(P1)", "LatePreviewCompleteIgnored(P1)", "LatePreviewErrorIgnored(P1)"} -> attempt = "P1"',
      '       [] label \\in {"ProposalComplete(P2)", "ProposalError(P2)", "PreviewComplete(P2)", "PreviewError(P2)", "StartPreview(P2)", "LateProposalCompleteIgnored(P2)", "LateProposalErrorIgnored(P2)", "LatePreviewCompleteIgnored(P2)", "LatePreviewErrorIgnored(P2)"} -> attempt = "P2"',
      '       [] label \\in {"BeginApply(accepted)", "BeginApply(rejected)"} ->',
      '            /\\ owner = IF attempt = "P1" THEN Attempt1 ELSE IF attempt = "P2" THEN Attempt2 ELSE NoAttempt',
      '            /\\ (expected = "P1" => lastBeginApplyExpected\' = Attempt1)',
      '            /\\ (expected = "P2" => lastBeginApplyExpected\' = Attempt2)',
      '            /\\ lastBeginApplyExpected\' = IF expected = "P1" THEN Attempt1 ELSE IF expected = "P2" THEN Attempt2 ELSE NoExpectedProposal',
      '       [] OTHER -> TRUE',
    ].join('\n');
  }
  if (model === 'BuilderPreview') {
    return [
      'ActionOwnerMatches(index) ==',
      '  LET label == TraceActions[index]',
      '      attempt == TraceOwnerAttempts[index]',
      '      generation == IF attempt = "P1" THEN 1 ELSE 2',
      '  IN CASE label = "StartProposal" -> /\\ proposalOwner\'.generation = generation /\\ attempt = "P1"',
      '       [] label = "ReplaceProposal" -> /\\ proposalOwner\'.generation = generation /\\ attempt = "P2"',
      '       [] label = "ProposalComplete" -> proposalOwner.generation = generation',
      '       [] label = "StartPreview" -> previewOwner\'.generation = generation',
      '       [] label = "PreviewComplete" -> previewOwner.generation = generation',
      '       [] label = "BeginApply" -> applyOwner\'.generation = generation',
      '       [] label = "ApplyAck" -> applyOwner.generation = generation',
      '       [] OTHER -> TRUE',
    ].join('\n');
  }
  if (model === 'BuilderJourney') {
    return [
      'ActionOwnerMatches(index) ==',
      '  LET label == TraceActions[index]',
      '      attempt == TraceOwnerAttempts[index]',
      '  IN CASE label = "Preview" -> /\\ attempt \\in {"P1", "P2"} /\\ proposalPreviewOwner\' = attempt',
      '       [] label \\in {"PreviewComplete", "PreviewError"} -> proposalPreviewOwner = attempt',
      '       [] label \\in {"LatePreviewCompleteIgnored", "LatePreviewErrorIgnored"} -> /\\ proposalPreviewOwner \\in {"P1", "P2"} /\\ proposalPreviewOwner # attempt',
      '       [] label \\in {"Group", "GroupAgain", "AddColumn", "EditGroup", "RemoveGroup"} -> attempt = proposalPreviewOwner',
      '       [] OTHER -> TRUE',
    ].join('\n');
  }
  return 'ActionOwnerMatches(index) == TRUE';
}

function actionIdentityPreserved(model) {
  const labels = [
    'LateProposalCompleteIgnored',
    'LateProposalCompleteIgnored(P1)', 'LateProposalCompleteIgnored(P2)',
    'LateProposalErrorIgnored(P1)', 'LateProposalErrorIgnored(P2)',
    'LatePreviewCompleteIgnored',
    'LatePreviewCompleteIgnored(P1)', 'LatePreviewCompleteIgnored(P2)',
    'LatePreviewErrorIgnored',
    'LatePreviewErrorIgnored(P1)', 'LatePreviewErrorIgnored(P2)',
    'BeginApply(rejected)',
  ].map(tlaString);
  const fields = [
    'status', 'tablePreviewStatus', 'canApply', 'proposalIdPresent', 'previewReceiptPresent', 'rowsPresent', 'receiptPresent',
    'proposalId', 'proposalOutputId', 'proposalSnapshotToken', 'proposalDraftVersion', 'proposalDraftDigest',
    'previewReceiptId', 'previewOutputId',
    'sourcePreviewStatus', 'sourcePreviewReceiptId', 'sourcePreviewOutputId', 'sourcePreviewProposalId', 'sourcePreviewDraftVersion', 'sourcePreviewDraftDigest', 'sourcePreviewRowsPresent', 'sourcePreviewRowCount', 'sourcePreviewRenderedRowCount',
    'embeddedPreviewStatus', 'embeddedPreviewReceiptId', 'embeddedPreviewOutputId', 'embeddedPreviewProposalId',
    'embeddedPreviewDraftVersion', 'embeddedPreviewDraftDigest', 'embeddedPreviewRowsPresent', 'embeddedPreviewRowCount', 'embeddedPreviewRenderedRowCount',
    'applyAvailable', 'applyEnabled', 'applying',
  ];
  const equalities = fields.map((field) =>
    '       /\\ before.' + field + ' = after.' + field,
  );
  return [
    'ObservedIdentityUnchanged(before, after) ==',
    ...equalities,
    '',
    'ActionIdentityPreserved(index) ==',
    '  LET label == TraceActions[index]',
    '      before == TraceObservations[index - 1]',
    '      after == TraceObservations[index]',
    '  IN IF label \\in {' + labels.join(', ') + '}',
    '     THEN ObservedIdentityUnchanged(before, after)',
    ...(model === 'BuilderPreview'
      ? [
          '     ELSE IF label = "ApplyAck" /\\ applyOwner.generation # proposalOwner.generation',
          '          THEN ObservedIdentityUnchanged(before, after)',
          '          ELSE TRUE',
        ]
      : ['     ELSE TRUE']),
  ].join('\n');
}

function generateModule(trace, moduleName) {
  const actions = ['"Init"', ...trace.actions.map((action) => tlaString(action.label))];
  const ownerAttempts = ['"NoAttempt"', ...trace.actions.map((action) => tlaString(traceOwnerValue(action)))];
  const expectedProposals = ['"NoExpectedProposal"', ...trace.actions.map((action) => tlaString(traceExpectedProposalValue(action)))];
  const observations = [
    tlaObservation(trace.initialObservation),
    ...trace.actions.map((action) => tlaObservation(action.observation)),
  ];
  const modelMapping = trace.model === 'BuilderPreview'
    ? builderPreviewObservationMapping
    : trace.model === 'ReconcileLifecycle'
      ? reconcileLifecycleObservationMapping
      : trace.model === 'BuilderJourney'
        ? builderJourneyObservationMapping
        : constructionLifecycleObservationMapping;
  const nextApplicable = trace.model === 'ConstructionLifecycle'
    ? [
        'ResourceIsApplicableNext(attempt) ==',
        '  /\\ attempt \\in Attempts',
        "  /\\ (attemptPhase')[attempt] = ProposalReady",
        "  /\\ (requestIdentity')[attempt] \\in Identities",
        "  /\\ (responseMatches')[attempt]",
        "  /\\ (proposalIdPresent')[attempt]",
        "  /\\ (proposalReceipt')[attempt] = ReceiptFor(attempt)",
        "  /\\ (responseOutput')[attempt] = OutputFor((requestIdentity')[attempt])",
        "  /\\ (previewReceipt')[attempt] = (proposalReceipt')[attempt]",
        "  /\\ (previewOutput')[attempt] = OutputFor((requestIdentity')[attempt])",
        "  /\\ (previewRowsNonNull')[attempt]",
        '',
      ]
    : [];
  return [
    '---------------- MODULE ' + moduleName + ' ----------------',
    'EXTENDS ' + trace.model + ', Naturals, Sequences, TLC',
    '',
    'VARIABLES traceIndex',
    '',
    'TraceActions == ' + tlaSequence(actions),
    '',
    'TraceObservations == ' + tlaSequence(observations),
    '',
    'TraceOwnerAttempts == ' + tlaSequence(ownerAttempts),
    '',
    'TraceExpectedProposals == ' + tlaSequence(expectedProposals),
    '',
    actionLabelMatches(trace.model),
    '',
    actionOwnerMatches(trace.model),
    '',
    actionIdentityPreserved(trace.model),
    '',
    ...nextApplicable,
    'ObsMatches(obs) ==',
    modelMapping(''),
    '',
    'ObsMatchesNext(obs) ==',
    modelMapping("'"),
    '',
    'TraceInit ==',
    '  /\\ Init',
    '  /\\ traceIndex = 1',
    '  /\\ ObsMatches(TraceObservations[1])',
    '',
    'TraceNext ==',
    '  IF traceIndex < Len(TraceActions)',
    '  THEN /\\ Next',
    "       /\\ ActionLabelMatches(TraceActions[traceIndex + 1])",
    '       /\\ ActionOwnerMatches(traceIndex + 1)',
    '       /\\ ActionIdentityPreserved(traceIndex + 1)',
    '       /\\ ObsMatchesNext(TraceObservations[traceIndex + 1])',
    "       /\\ traceIndex' = traceIndex + 1",
    '  ELSE /\\ UNCHANGED vars',
    '       /\\ UNCHANGED traceIndex',
    '',
    '(* The usual [][TraceNext]_<<vars, traceIndex>> spec accepts stuttering at any state.',
    '   TraceProgress makes a short stutter before the last action a',
    '   violation. It is a trace-completion check, not a model safety',
    '   invariant; retained finding paths remain legal. *)',
    'TraceProgress ==',
    '  \\/ traceIndex = Len(TraceActions)',
    '  \\/ ENABLED TraceNext',
    '',
    'TraceSpec == TraceInit /\\ [][TraceNext]_<<vars, traceIndex>>',
    '',
    '====',
    '',
  ].join('\n');
}

function generateConfig() {
  return [
    'SPECIFICATION TraceSpec',
    'INVARIANT TypeOK',
    'INVARIANT TraceProgress',
    '',
  ].join('\n');
}

function parseTlc(text) {
  const statesMatch = text.match(/([0-9,]+) states generated, ([0-9,]+) distinct states found/);
  const depthMatch = text.match(/The depth of the complete state graph search is ([0-9]+)/);
  return {
    stateCount: statesMatch ? Number(statesMatch[1].replaceAll(',', '')) : null,
    distinctStateCount: statesMatch ? Number(statesMatch[2].replaceAll(',', '')) : null,
    depth: depthMatch ? Number(depthMatch[1]) : null,
    passed: text.includes('Model checking completed. No error has been found.'),
  };
}

function outputTail(text, lines = 30) {
  const split = text.trimEnd().split('\n');
  return split.slice(-lines).join('\n');
}

function runTlc(jarPath, trace, uniqueIndex) {
  const runDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-traces-'));
  const modelDirectory = join(runDirectory, 'model');
  const metadataDirectory = join(runDirectory, 'states');
  mkdirSync(modelDirectory);
  mkdirSync(metadataDirectory);
  const moduleName = 'TraceCheck' + uniqueIndex;
  const moduleFile = moduleName + '.tla';
  const configFile = moduleName + '.cfg';
  copyFileSync(resolve(ROOT, MODEL_DIR, trace.model + '.tla'), join(modelDirectory, trace.model + '.tla'));
  writeFileSync(join(modelDirectory, moduleFile), generateModule(trace, moduleName));
  writeFileSync(join(modelDirectory, configFile), generateConfig());
  const args = [
    '-XX:+UseParallelGC', '-cp', jarPath, 'tlc2.TLC',
    '-workers', '4',
    '-metadir', metadataDirectory,
    '-config', configFile,
    moduleFile,
  ];
  const result = spawnSync('java', args, { cwd: modelDirectory, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  const text = (result.stdout || '') + (result.stderr || '');
  const parsed = parseTlc(text);
  if (parsed.passed) {
    return { ...parsed, status: 'pass', modulePath: join(modelDirectory, moduleFile), runDirectory };
  }
  const traceIndexes = [...text.matchAll(/traceIndex = ([0-9]+)/g)].map((match) => Number(match[1]));
  const lastTraceIndex = traceIndexes.length > 0 ? traceIndexes[traceIndexes.length - 1] : null;
  const actionIndex = lastTraceIndex === null ? null : lastTraceIndex - 1;
  const label = actionIndex !== null && actionIndex >= 0 && actionIndex < trace.actions.length
    ? trace.actions[actionIndex].label
    : null;
  return {
    ...parsed,
    status: 'fail',
    modulePath: join(modelDirectory, moduleFile),
    runDirectory,
    failure: {
      message: result.error ? result.error.message : 'TLC did not complete the trace',
      actionIndex,
      actionLabel: label,
      modulePath: join(modelDirectory, moduleFile),
      outputTail: outputTail(text),
    },
  };
}

function writeReport(outputPath, report) {
  const absolute = resolve(process.cwd(), outputPath);
  mkdirSync(dirname(absolute), { recursive: true });
  writeFileSync(absolute, JSON.stringify(report, null, 2) + '\n');
  return absolute;
}

function reportUnmodeledEvents(traces, policy = {}) {
  const byKind = new Map();
  const entries = [];
  for (const trace of traces) {
    const events = Array.isArray(trace.unmodeledEvents) ? trace.unmodeledEvents : [];
    if (events.length === 0) continue;
    for (const event of events) {
      const kind = typeof event === 'string' ? event : event.kind;
      byKind.set(kind, (byKind.get(kind) ?? 0) + 1);
    }
    entries.push({ name: trace.name, model: trace.model, events });
  }
  const unclassified = [...byKind.entries()]
    .filter(([kind]) => !(kind in policy))
    .sort(([left], [right]) => left.localeCompare(right));
  return {
    total: entries.reduce((sum, entry) => sum + entry.events.length, 0),
    byKind: Object.fromEntries([...byKind.entries()].sort(([left], [right]) => left.localeCompare(right))),
    unclassified: {
      total: unclassified.reduce((sum, [, count]) => sum + count, 0),
      byKind: Object.fromEntries(unclassified),
    },
    traces: entries,
  };
}

async function main() {
  const options = parseArgs();
  const contract = validateContract(readJson(options.contract ? resolve(process.cwd(), options.contract) : CONTRACT_RELATIVE));
  const counts = expectedCounts(contract);

  let payload;
  try {
    payload = JSON.parse(readFileSync(resolve(process.cwd(), options.traces), 'utf8'));
  } catch (error) {
    fail('cannot read trace JSON ' + options.traces + ': ' + error.message);
  }
  if (!isPlainObject(payload)) fail('trace JSON must be an object');
  if (payload.schemaVersion !== 1) fail('trace JSON schemaVersion must be 1');
  const provenance = validateSourceProvenance(payload, contract);
  if (!Array.isArray(payload.traces)) fail('trace JSON traces must be an array');

  const events = allowedEvents(contract);
  const traces = payload.traces;
  const shapeProblems = new Map();
  for (let index = 0; index < traces.length; index += 1) {
    const problem = traceShapeProblem(traces[index], index, contract, events);
    if (problem) shapeProblems.set(index, problem);
  }

  const findingCount = traces.filter(isFindingTrace).length;
  const scheduleCount = traces.length - findingCount;
  const countProblems = [];
  if (!options.allowPartial) {
    if (traces.length !== counts.traceCount) {
      countProblems.push('expected ' + counts.traceCount + ' traces, found ' + traces.length);
    }
    if (scheduleCount !== counts.scheduleCount) {
      countProblems.push('expected ' + counts.scheduleCount + ' schedule traces, found ' + scheduleCount);
    }
    if (findingCount !== counts.findingCount) {
      countProblems.push('expected ' + counts.findingCount + ' expected-failure finding traces, found ' + findingCount);
    }
  }

  const actionSet = new Set();
  const eventSet = new Set();
  for (const trace of traces) {
    const modelActions = new Set(contract.models[trace.model].actions);
    for (const action of trace.actions) {
      actionSet.add(normalizedActionLabel(trace.model, action.label, modelActions) ?? action.label);
      eventSet.add(action.event);
    }
  }
  const missingActions = contract.phase1.requiredActions.filter((action) => !actionSet.has(action));
  const missingEvents = contract.phase1.requiredEvents.filter((event) => !eventSet.has(event));
  const missingModels = (contract.phase1.requiredModels ?? []).filter((model) => !traces.some((trace) => trace.model === model));
  const coverageProblems = [];
  if (!options.allowPartial) {
    if (missingActions.length > 0) coverageProblems.push('required actions missing: ' + missingActions.join(', '));
    if (missingEvents.length > 0) coverageProblems.push('required events missing: ' + missingEvents.join(', '));
    if (missingModels.length > 0) coverageProblems.push('required models missing: ' + missingModels.join(', '));
  }

  const uniqueByKey = new Map();
  for (let index = 0; index < traces.length; index += 1) {
    const key = traceKey(traces[index]);
    if (!uniqueByKey.has(key)) uniqueByKey.set(key, []);
    uniqueByKey.get(key).push(index);
  }

  let javaVersion = null;
  let jarPath = null;
  const runResults = new Map();
  if (shapeProblems.size < traces.length) {
    javaVersion = verifyJava();
    jarPath = await acquireTlcJar();
  }

  let uniqueIndex = 0;
  for (const [key, indexes] of uniqueByKey.entries()) {
    const firstIndex = indexes[0];
    if (shapeProblems.has(firstIndex)) {
      runResults.set(key, {
        status: 'fail',
        structuralFailure: shapeProblems.get(firstIndex),
      });
      continue;
    }
    uniqueIndex += 1;
    const trace = traces[firstIndex];
    if (!options.quiet) console.log('Validating ' + trace.model + ' trace "' + trace.name + '" (' + trace.actions.length + ' actions, ' + indexes.length + ' copy/copies)...');
    runResults.set(key, runTlc(jarPath, trace, uniqueIndex));
  }

  const modelCounts = {};
  const reportTraces = traces.map((trace, index) => {
    const key = traceKey(trace);
    const run = runResults.get(key);
    const modelCount = modelCounts[trace.model] ?? 0;
    modelCounts[trace.model] = modelCount + 1;
    const structuralFailure = shapeProblems.get(index) ?? (run?.structuralFailure ?? null);
    const actionCount = Array.isArray(trace.actions) ? trace.actions.length : 0;
    const status = !structuralFailure && run && run.status === 'pass' ? 'pass' : 'fail';
    const failure = structuralFailure ? { ...structuralFailure } : (run?.failure ? { ...run.failure } : null);
    const entry = {
      name: trace.name,
      model: trace.model,
      status,
      actionCount,
      ...(run?.structuralFailure
        ? {}
        : {
            tlc: run
              ? {
                  stateCount: run.stateCount ?? null,
                  distinctStateCount: run.distinctStateCount ?? null,
                  depth: run.depth ?? null,
                }
              : { stateCount: null, distinctStateCount: null, depth: null },
          }),
      ...(failure ? { failure } : {}),
    };
    const indexes = uniqueByKey.get(key);
    if (indexes.length > 1 && indexes[0] !== index) entry.duplicateOf = traces[indexes[0]].name;
    return entry;
  });

  const invalid = reportTraces.filter((trace) => trace.status !== 'pass');
  const report = {
    schemaVersion: 1,
    sourceProfile: provenance.profile,
    sourceCommit: provenance.head,
    contractSourceCommit: payload.contractSourceCommit,
    sourceFingerprint: provenance.sourceFingerprint,
    traceCount: traces.length,
    uniqueTraceCount: uniqueByKey.size,
    validatedCount: reportTraces.length - invalid.length,
    invalidCount: invalid.length,
    modelCounts,
    coverage: {
      requiredModels: { missing: missingModels },
      requiredActions: { missing: missingActions },
      requiredEvents: { missing: missingEvents },
    },
    traces: reportTraces,
    unmodeledEvents: reportUnmodeledEvents(traces, contract.unmodeledEventPolicy),
    ...(options.allowPartial ? { partial: true } : {}),
  };
  if (javaVersion) report.tool = { name: 'TLC', version: TLC_VERSION, sha256: TLC_SHA256, java: javaVersion };
  const outputPath = options.output ? writeReport(options.output, report) : null;

  const traceReport = reportTraces.length >= 0 ? reportUnmodeledEvents(traces, contract.unmodeledEventPolicy) : null;
  const unclassifiedEvents = traceReport?.unclassified?.total ?? 0;
  if (!options.allowPartial && unclassifiedEvents > 0) {
    coverageProblems.push('unclassified implementation events: ' + JSON.stringify(traceReport.unclassified.byKind));
  }
  const globalProblems = [...countProblems, ...coverageProblems];
  if (options.allowPartial) {
    console.log('Partial mode: trace/schedule/finding counts and required coverage are reported, not enforced.');
    if (missingActions.length > 0) console.log('Missing required actions: ' + missingActions.join(', '));
    if (missingEvents.length > 0) console.log('Missing required events: ' + missingEvents.join(', '));
    if (missingModels.length > 0) console.log('Missing required models: ' + missingModels.join(', '));
  }

  if (outputPath) console.log('Wrote trace validation report to ' + outputPath);
  console.log('Traces: ' + report.traceCount + ' total, ' + report.uniqueTraceCount + ' unique, ' +
    report.validatedCount + ' validated, ' + report.invalidCount + ' invalid.');
  if (!options.quiet) {
    for (const trace of reportTraces) {
      const tlc = trace.tlc && trace.tlc.stateCount !== null
        ? ' [' + trace.tlc.stateCount + ' generated / ' + trace.tlc.distinctStateCount + ' distinct, depth ' + trace.tlc.depth + ']'
        : '';
      console.log((trace.status === 'pass' ? 'PASS ' : 'FAIL ') + trace.model + ' "' + trace.name + '"' + tlc);
    }
  }

  if (globalProblems.length > 0 || invalid.length > 0) {
    for (const problem of globalProblems) console.error('ERROR ' + problem);
    for (const trace of invalid) {
      const failure = trace.failure ?? {};
      const location = failure.actionIndex === null || failure.actionIndex === undefined
        ? ''
        : ' action ' + failure.actionIndex + (failure.actionLabel ? ' ("' + failure.actionLabel + '")' : '');
      console.error('INVALID trace "' + trace.name + '" model=' + trace.model + location + ': ' + (failure.message ?? 'trace did not validate'));
      if (failure.outputTail) console.error('TLC output tail:\n' + failure.outputTail);
      if (failure.modulePath) console.error('Generated module: ' + failure.modulePath);
    }
    fail('Builder trace validation failed');
  }
  console.log('PASS Builder trace validation: all ' + report.traceCount + ' traces match TLC model paths.');
}

main().catch((error) => {
  console.error('\nFAIL Builder trace validation: ' + error.message);
  process.exitCode = 1;
});
