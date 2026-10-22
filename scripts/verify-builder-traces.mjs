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
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const ROOT = realpathSync(resolve(SCRIPT_DIR, '..'));
const MODEL_DIR = 'verification/builder-interactions';
const CONTRACT_RELATIVE = MODEL_DIR + '/builder-trace-contract.json';
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

function observationProblem(observation, where, model) {
  if (!isPlainObject(observation)) return where + ' must be an object';
  if (typeof observation.status !== 'string') return where + '.status must be a string';
  const statuses = MODEL_STATUSES.get(model) ?? STATUSES;
  if (!statuses.has(observation.status)) return where + '.status "' + observation.status + '" has no model observation mapping for ' + model;
  for (const field of OBSERVATION_BOOLEAN_FIELDS) {
    if (typeof observation[field] !== 'boolean') return where + '.' + field + ' must be a boolean';
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
      if (typeof event === 'string') continue;
      if (!isPlainObject(event) || typeof event.kind !== 'string' || typeof event.note !== 'string') {
        return { message: eventWhere + ' must be a string or an object with string kind and note' };
      }
    }
  }
  const initialProblem = observationProblem(trace.initialObservation, where + '.initialObservation', trace.model);
  if (initialProblem) return { message: initialProblem };
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
    if (!normalizedActionLabel(trace.model, action.label, actions)) {
      return { message: actionWhere + '.label "' + action.label + '" is not an action of ' + trace.model, actionIndex };
    }
    if (!events.has(action.event)) {
      return { message: actionWhere + '.event "' + action.event + '" is not defined by the contract', actionIndex };
    }
    const binding = contract.actionBindings[action.label];
    if (binding && binding.event !== action.event) {
      return {
        message: actionWhere + '.event "' + action.event + '" does not match contract event "' + binding.event + '" for ' + action.label,
        actionIndex,
      };
    }
    const observation = observationProblem(action.observation, actionWhere + '.observation', trace.model);
    if (observation) return { message: observation, actionIndex };
  }
  return null;
}

function traceKey(trace) {
  return canonicalJson({
    model: trace.model,
    initialObservation: trace.initialObservation,
    actions: trace.actions.map((action) => ({
      label: action.label,
      event: action.event,
      observation: action.observation,
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

function tlaObservation(observation) {
  return '[status |-> ' + tlaString(observation.status) +
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
    '  /\\ (obs.previewReceiptPresent => (previewPhase' + prime + ' = "ready"))',
    '  /\\ (obs.rowsPresent => (previewPhase' + prime + ' = "ready"))',
  ].join('\n');
}


function builderJourneyObservationMapping(prime) {
  const phase = 'phase' + prime;
  const reshapeCount = 'reshapeCount' + prime;
  const pivotReady = 'pivotReady' + prime;
  const preview = 'preview' + prime;
  return [
    '  /\\ (obs.status = "idle" => (' + phase + ' = "idle"))',
    '  /\\ (obs.status = "selected" => (' + phase + ' = "selected"))',
    '  /\\ (obs.status = "reshaping" => (' + phase + ' = "reshaping"))',
    '  /\\ (obs.status = "pivot-ready" =>',
    '       ((' + phase + ' \\in {"selected", "reshaping"}) /\\ ' + pivotReady + '))',
    '  /\\ (obs.status = "previewing" => (' + preview + ' = "pending"))',
    '  /\\ (obs.status = "ready" => (' + preview + ' = "ready"))',
    '  /\\ (obs.status = "error" => (' + preview + ' = "error"))',
    '  /\\ (obs.canApply =>',
    '       ((' + phase + ' = "reshaping") /\\',
    '        (' + reshapeCount + ' = 2) /\\',
    '        (' + preview + ' = "none")))',
    '  /\\ (obs.previewReceiptPresent => (' + preview + ' = "ready"))',
    '  /\\ (obs.rowsPresent => (' + preview + ' = "ready"))',
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
  return [
    '  /\\ (obs.status = "previewing" =>',
    '       (\\E attempt \\in Attempts:',
    '          (attemptPhase' + prime + ')[attempt] \\in {"proposal-pending", "preview-pending"}))',
    '  /\\ (obs.status = "ready" =>',
    '       (\\E attempt \\in Attempts: (attemptPhase' + prime + ')[attempt] = "ready"))',
    '  /\\ (obs.status = "error" =>',
    '       (\\E attempt \\in Attempts: (attemptPhase' + prime + ')[attempt] = "error"))',
    '  /\\ (obs.status = "needs-repair" =>',
    '       (\\E attempt \\in Attempts: (attemptPhase' + prime + ')[attempt] = "needs-repair"))',
    '  /\\ (obs.status = "applying" => (overrideState' + prime + ' = "applying"))',
    '  /\\ (obs.canApply =>',
    '       (\\E attempt \\in Attempts:',
    '          ' + applicable + ' /\\ (requestIdentity' + prime + ')[attempt] = currentIdentity' + prime + '))',
    '  /\\ (obs.previewReceiptPresent =>',
    '       (\\E attempt \\in Attempts: (previewReceipt' + prime + ')[attempt] # NoReceipt))',
    '  /\\ (obs.rowsPresent =>',
    '       (\\E attempt \\in Attempts: (previewRowsNonNull' + prime + ')[attempt]))',
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

function generateModule(trace, moduleName) {
  const actions = ['"Init"', ...trace.actions.map((action) => tlaString(action.label))];
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
    actionLabelMatches(trace.model),
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
  if (payload.sourceCommit !== contract.sourceCommit) {
    fail('trace sourceCommit ' + payload.sourceCommit + ' does not match contract sourceCommit ' + contract.sourceCommit);
  }
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
    sourceCommit: contract.sourceCommit,
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
