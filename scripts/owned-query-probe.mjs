#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { open, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateOwnedArangoContainer } from './lib/local-preview-index-explain.mjs';
import { buildArangoShellInvocation } from './verify-ui/helpers/owned-arangosh-command.mjs';

const COMPOSE_PROJECT = 'loom-dev-6d7df93d6a37';
const CONTAINER = `${COMPOSE_PROJECT}-arangodb-1`;
const DATABASE = 'loom_dev';
const PROJECT_SCOPE = 'loom_dev_cda_fhir';
const GENERATION_SCOPE = 'cda-fhir-v1';
const MAX_RUNTIME_SECONDS = 8;
const MAX_MEMORY_BYTES = 268435456;
const SAFE_INTEGER_COUNT_KEYS = new Set([
  'scoped_roots',
  'scoped_patients',
  'scoped_observations',
  'valid_root_edges',
  'roots_with_valid_patient',
  'reachable_patients',
  'valid_patient_observation_edges',
  'patients_with_observations',
  'reachable_observations',
  'discovery_category_groups',
  'reachable_patient_observation_pairs',
  'pivot_cell_count',
]);
const EXPLAIN_HOST_TIMEOUT_MS = 15000;
const HOST_STARTUP_MARGIN_MS = 2000;
const MIN_EXECUTE_HOST_TIMEOUT_MS = 2500;
const MAX_EXECUTE_HOST_TIMEOUT_MS = MAX_RUNTIME_SECONDS * 1000 + HOST_STARTUP_MARGIN_MS;
const MARKER = '__LOOM_OWNED_QUERY_PROBE__';
const SAFE_ARANGO_ERROR = 'Arango reported a query error.';
const SAFE_ARANGO_WARNING = 'Arango reported a query warning.';
const WRITE_NODE_TYPES = new Set([
  'InsertNode',
  'ModifyNode',
  'ModificationNode',
  'RemoveNode',
  'ReplaceNode',
  'UpdateNode',
  'UpsertNode',
]);

const sha256 = value => createHash('sha256').update(value).digest('hex');
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const finiteNumberOrNull = value => Number.isFinite(value) ? value : null;
const nonNegativeIntegerOrNull = value => Number.isSafeInteger(value) && value >= 0 ? value : null;
const isSafeRuleName = value => typeof value === 'string' && /^[a-z][a-z0-9-]{0,127}$/.test(value);
function safeProfileNodes(value) {
  if (!Array.isArray(value)) return [];
  return value.flatMap(node => {
    if (!isRecord(node)) return [];
    const { id, calls, items, runtime } = node;
    if (nonNegativeIntegerOrNull(id) === null
      || nonNegativeIntegerOrNull(calls) === null
      || nonNegativeIntegerOrNull(items) === null
      || !Number.isFinite(runtime) || runtime < 0) return [];
    return [{ id, calls, items, runtime }];
  });
}
const executionHostTimeout = maxRuntimeSeconds => Math.min(
  MAX_EXECUTE_HOST_TIMEOUT_MS,
  Math.max(MIN_EXECUTE_HOST_TIMEOUT_MS, Math.ceil(maxRuntimeSeconds * 1000) + HOST_STARTUP_MARGIN_MS),
);

function quoteJavaScript(value) {
  return JSON.stringify(value);
}

function escapeScriptAtSigns(script) {
  return script.replaceAll('@', '\\u0040');
}

/** Build the bounded arangosh request script used for parser plus EXPLAIN. */
export function buildExplainRequestScript({ database = DATABASE, query, bindVars }) {
  const requestBody = quoteJavaScript(JSON.stringify({ query, bindVars }));
  const parseBody = quoteJavaScript(JSON.stringify({ query }));
  const marker = quoteJavaScript(MARKER);
  const script = `(() => {
try {
  const request = require('@arangodb/request');
  function decode(response) {
    if (!response) return null;
    if (typeof response.body === 'string') {
      try { return JSON.parse(response.body); } catch (_) { return null; }
    }
    return response.body && typeof response.body === 'object' ? response.body : null;
  }
  function safeNumber(value) { return Number.isFinite(value) ? value : null; }
  function errorSummary(body, statusCode) {
    const error = !!(body && body.error === true)
      || (Number.isFinite(statusCode) && (statusCode < 200 || statusCode >= 300));
    return {
      error,
      errorNum: safeNumber(body && body.errorNum),
      code: null,
      message: error ? ${quoteJavaScript(SAFE_ARANGO_ERROR)} : null,
    };
  }
  const parseStarted = Date.now();
  let parseResponse;
  let parseResult;
  try {
    parseResponse = request({
      url: 'http://127.0.0.1:8529/_db/${database}/_api/query',
      method: 'POST',
      body: ${parseBody},
      headers: {'content-type':'application/json'},
    });
    parseResult = decode(parseResponse);
  } catch (_) {
    print(${marker} + JSON.stringify({
      stage: 'parse-transport', parseStatusCode: null,
      parseServerElapsedMs: Date.now() - parseStarted, message: 'Arango parser transport failed.',
    }));
    return;
  }
  const parseStatusCode = safeNumber(parseResponse.statusCode ?? parseResponse.status);
  const parseServerElapsedMs = Date.now() - parseStarted;
  if (!parseResult || parseStatusCode < 200 || parseStatusCode >= 300 || parseResult.parsed !== true) {
    const parserError = errorSummary(parseResult, parseStatusCode);
    print(${marker} + JSON.stringify({
      stage: 'parse-failed', parseStatusCode, parseServerElapsedMs,
      errorNum: parserError.errorNum, errorCode: parseStatusCode,
      message: parserError.message,
    }));
    return;
  }
  const parserVariables = Array.isArray(parseResult.bindVars) ? parseResult.bindVars : [];
  const parserVariableNames = parserVariables.map(value => {
    const name = typeof value === 'string' ? value : value && value.name;
    return typeof name === 'string' ? name.replace(/^@+/, '') : '';
  }).filter(Boolean);
  const parseCollections = Array.isArray(parseResult.collections) ? parseResult.collections : [];
  const parserCollectionWrites = parseCollections.some(value => value && value.type && value.type !== 'read');
  const requiredVariables = ['project', 'auth_resource_paths_unrestricted', 'auth_resource_paths'];
  const missingScopeVariables = requiredVariables.filter(name => !parserVariableNames.includes(name));
  if (!parserVariableNames.includes('dataset_generation') && !parserVariableNames.includes('generation')) {
    missingScopeVariables.push('generation');
  }
  if (missingScopeVariables.length > 0) {
    print(${marker} + JSON.stringify({
      stage: 'parse-scope-failed', parseStatusCode, parseServerElapsedMs,
      parserReadOnly: !parserCollectionWrites,
      message: 'The parsed query does not use all required scope binds.',
    }));
    return;
  }
  if (parserCollectionWrites) {
    print(${marker} + JSON.stringify({
      stage: 'parse-write-scope-failed', parseStatusCode, parseServerElapsedMs,
      parserReadOnly: false,
      message: 'The parser reported a non-read collection access.',
    }));
    return;
  }
  const explainStarted = Date.now();
  let explainResponse;
  let explainBody;
  try {
    explainResponse = request({
      url: 'http://127.0.0.1:8529/_db/${database}/_api/explain',
      method: 'POST',
      body: ${requestBody},
      headers: {'content-type':'application/json'},
    });
    explainBody = decode(explainResponse);
  } catch (_) {
    print(${marker} + JSON.stringify({
      stage: 'explain-transport', parseStatusCode, parseServerElapsedMs,
      explainStatusCode: null, explainServerElapsedMs: Date.now() - explainStarted,
      parserReadOnly: true,
      message: 'Arango EXPLAIN transport failed.',
    }));
    return;
  }
  const explainStatusCode = safeNumber(explainResponse.statusCode ?? explainResponse.status);
  const explainServerElapsedMs = Date.now() - explainStarted;
  const plans = explainBody && (explainBody.plan ? [explainBody.plan, ...(explainBody.plans || [])] : (explainBody.plans || []));
  const selectedPlan = explainBody && (explainBody.plan || (Array.isArray(explainBody.plans) ? explainBody.plans[0] : null));
  const selectedPlanIndex = Array.isArray(plans) ? plans.indexOf(selectedPlan) : -1;
  const nodes = [];
  const indexes = [];
  const warnings = (explainBody && Array.isArray(explainBody.warnings) ? explainBody.warnings : []).map(value => ({
    code: safeNumber(value && value.code), message: ${quoteJavaScript(SAFE_ARANGO_WARNING)},
  }));
  if (Array.isArray(plans)) {
    plans.forEach((plan, planIndex) => {
      for (const node of (Array.isArray(plan && plan.nodes) ? plan.nodes : [])) {
        const nodeType = typeof node.type === 'string' ? node.type : '';
        const collection = typeof node.collection === 'string' ? node.collection : null;
        const nodeId = safeNumber(node.id);
        nodes.push({
          plan: planIndex, nodeId, type: nodeType,
          collection, estimatedNrItems: safeNumber(node.estimatedNrItems),
        });
        function flattenIndexes(value) {
          if (Array.isArray(value)) return value.flatMap(flattenIndexes);
          if (!value || typeof value !== 'object') return [];
          if (['id', 'name', 'type', 'fields'].some(key => value[key] !== undefined)) return [value];
          return Object.values(value).flatMap(flattenIndexes);
        }
        for (const index of flattenIndexes(node.indexes)) {
          indexes.push({
            plan: planIndex, nodeId, nodeType,
            collection: typeof (index.collection || collection) === 'string' ? (index.collection || collection) : null,
            id: typeof index.id === 'string' ? index.id : null,
            name: typeof index.name === 'string' ? index.name : null,
            type: typeof index.type === 'string' ? index.type : null,
            fields: Array.isArray(index.fields) ? index.fields.filter(value => typeof value === 'string') : [],
          });
        }
      }
    });
  }
  const planEstimates = Array.isArray(plans) ? plans.map((plan, planIndex) => ({
    plan: planIndex,
    estimatedCost: safeNumber(plan && plan.estimatedCost),
    estimatedNrItems: safeNumber(plan && plan.estimatedNrItems),
  })) : [];
  const appliedRules = Array.isArray(selectedPlan && selectedPlan.rules)
    ? [...new Set(selectedPlan.rules.filter(value => typeof value === 'string' && /^[a-z][a-z0-9-]{0,127}$/.test(value)))]
    : [];
  const selectedNodes = nodes.filter(node => node.plan === selectedPlanIndex);
  const planOk = explainStatusCode >= 200 && explainStatusCode < 300
    && explainBody && explainBody.error !== true && planEstimates.length > 0
    && selectedNodes.length > 0 && selectedNodes.every(node => Number.isSafeInteger(node.nodeId) && node.nodeId >= 0 && node.type.length > 0);
  const writeNodeFound = selectedNodes.some(node => ${quoteJavaScript([...WRITE_NODE_TYPES])}.includes(node.type));
  const responseError = explainBody
    ? errorSummary(explainBody, explainStatusCode)
    : { error: true, errorNum: null, code: null, message: ${quoteJavaScript(SAFE_ARANGO_ERROR)} };
  print(${marker} + JSON.stringify({
    stage: planOk ? 'explain-complete' : 'explain-failed', parseStatusCode, parseServerElapsedMs,
    explainStatusCode, explainServerElapsedMs, errorNum: responseError.errorNum,
    errorCode: explainStatusCode, message: responseError.message,
    planCount: planEstimates.length, planEstimates, appliedRules, selectedPlanIndex, nodes, indexes, warnings,
    parserReadOnly: true,
    readOnlyPlan: planOk && !writeNodeFound,
  }));
} catch (_) {
  print(${marker} + JSON.stringify({stage:'script-failed', message:'Arango query probe script failed safely.'}));
}
})();`;
  return escapeScriptAtSigns(script);
}

/** Build the single bounded read-only db._query request script. */
export function buildExecuteRequestScript({ query, bindVars, maxRuntimeSeconds, memoryLimitBytes, safeIntegerCounts = false }) {
  const queryLiteral = quoteJavaScript(query);
  const bindLiteral = quoteJavaScript(bindVars);
  const marker = quoteJavaScript(MARKER);
  const script = `
const started = Date.now();
try {
  const cursor = db._query(${queryLiteral}, ${bindLiteral}, {
    maxRuntime: ${maxRuntimeSeconds}, memoryLimit: ${memoryLimitBytes}, profile: 2,
  });
  function safeCount(value) {
    return Number.isSafeInteger(value) && value >= 0 ? value : null;
  }
  function safeProfileNode(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
    const {id, calls, items, runtime} = value;
    if (safeCount(id) === null || safeCount(calls) === null || safeCount(items) === null
      || !Number.isFinite(runtime) || runtime < 0) return null;
    return {id, calls, items, runtime};
  }
  let resultCount = 0;
  let safeIntegerCounts = null;
  while (cursor.hasNext()) {
    const row = cursor.next();
    resultCount += 1;
    if (${safeIntegerCounts}) {
      if (resultCount !== 1 || !row || typeof row !== 'object' || Array.isArray(row)) {
        throw new Error('query did not return one integer-count object');
      }
      const entries = Object.entries(row);
      const allowedKeys = new Set(${JSON.stringify([...SAFE_INTEGER_COUNT_KEYS])});
      if (entries.length === 0 || entries.length > ${SAFE_INTEGER_COUNT_KEYS.size}
        || entries.some(([key, value]) => !allowedKeys.has(key) || !Number.isSafeInteger(value) || value < 0)) {
        throw new Error('query result did not match the safe integer-count schema');
      }
      safeIntegerCounts = {};
      for (const [key, value] of entries) safeIntegerCounts[key] = value;
    }
  }
  if (${safeIntegerCounts} && resultCount !== 1) {
    throw new Error('query did not return one integer-count object');
  }
  let stats = null;
  let warnings = [];
  try {
    const extra = cursor.getExtra();
    const source = extra && extra.stats;
    warnings = (extra && Array.isArray(extra.warnings) ? extra.warnings : []).map(value => ({
      code: Number.isFinite(value && value.code) ? value.code : null,
      message: ${quoteJavaScript(SAFE_ARANGO_WARNING)},
    }));
    if (source) stats = {
      scannedIndex: Number.isFinite(source.scannedIndex) ? source.scannedIndex : null,
      scannedFull: Number.isFinite(source.scannedFull) ? source.scannedFull : null,
      peakMemoryUsage: Number.isFinite(source.peakMemoryUsage) ? source.peakMemoryUsage : null,
      executionTime: Number.isFinite(source.executionTime) ? source.executionTime : null,
      documentLookups: safeCount(source.documentLookups),
      nodes: Array.isArray(source.nodes) ? source.nodes.map(safeProfileNode).filter(Boolean) : [],
    };
  } catch (_) {}
  const result = {ok:true, elapsedMs:Date.now()-started, resultCount, stats, warnings};
  if (${safeIntegerCounts}) result.safeIntegerCounts = safeIntegerCounts;
  print(${marker} + JSON.stringify(result));
} catch (error) {
  print(${marker} + JSON.stringify({
    ok:false, elapsedMs:Date.now()-started,
    errorNum: Number.isFinite(error && error.errorNum) ? error.errorNum : null,
    errorCode: Number.isFinite(error && error.code) ? error.code : null,
    message: ${quoteJavaScript(SAFE_ARANGO_ERROR)},
  }));
}`;
  return escapeScriptAtSigns(script);
}

function parseArguments(argv) {
  const options = { execute: false, safeIntegerCounts: false, maxRuntimeSeconds: MAX_RUNTIME_SECONDS, memoryLimitBytes: MAX_MEMORY_BYTES };
  const valueOptions = new Set(['--query', '--bind-vars', '--output', '--max-runtime-seconds', '--memory-limit-bytes', '--expected-index']);
  const seen = new Set();
  for (let index = 0; index < argv.length; index += 1) {
    const option = argv[index];
    if (option === '--execute') {
      options.execute = true;
      continue;
    }
    if (option === '--safe-integer-counts') {
      options.safeIntegerCounts = true;
      continue;
    }
    if (!valueOptions.has(option) || index + 1 >= argv.length || argv[index + 1].startsWith('--')) {
      options.parseError = 'invalid-arguments';
      break;
    }
    const value = argv[++index];
    if (seen.has(option)) {
      options.parseError = 'duplicate-option';
      break;
    }
    seen.add(option);
    switch (option) {
      case '--query': options.queryPath = value; break;
      case '--bind-vars': options.bindVarsPath = value; break;
      case '--output': options.outputPath = value; break;
      case '--expected-index': options.expectedIndex = value; break;
      case '--max-runtime-seconds': options.maxRuntimeSeconds = Number(value); break;
      case '--memory-limit-bytes': options.memoryLimitBytes = Number(value); break;
      default: options.parseError = 'invalid-arguments';
    }
  }
  return options;
}

function validateCliOptions(options) {
  if (!options.queryPath || !options.bindVarsPath || !options.outputPath) return 'query, bind-vars, and output paths are required';
  if (options.safeIntegerCounts && !options.execute) return 'safe-integer-counts requires execute';
  if (!Number.isFinite(options.maxRuntimeSeconds) || options.maxRuntimeSeconds <= 0 || options.maxRuntimeSeconds > MAX_RUNTIME_SECONDS) {
    return `max-runtime-seconds must be positive and no greater than ${MAX_RUNTIME_SECONDS}`;
  }
  if (!Number.isInteger(options.memoryLimitBytes) || options.memoryLimitBytes < 1 || options.memoryLimitBytes > MAX_MEMORY_BYTES) {
    return `memory-limit-bytes must be an integer from 1 through ${MAX_MEMORY_BYTES}`;
  }
  if (options.expectedIndex !== undefined && !options.expectedIndex.trim()) return 'expected-index must be non-empty';
  return null;
}

function run(command, args, timeout) {
  return spawnSync(command, args, {
    encoding: 'utf8',
    timeout,
    maxBuffer: 8 * 1024 * 1024,
    windowsHide: true,
  });
}

function baseReport(options) {
  return {
    schemaVersion: 1,
    status: 'failed',
    operation: options.execute ? 'EXPLAIN and at most one bounded read-only query' : 'read-only parser and EXPLAIN only',
    target: {
      composeProject: COMPOSE_PROJECT,
      container: CONTAINER,
      database: DATABASE,
      project: PROJECT_SCOPE,
      generation: GENERATION_SCOPE,
    },
    querySource: options.queryPath ? resolve(options.queryPath) : null,
    bindVarsSource: options.bindVarsPath ? resolve(options.bindVarsPath) : null,
    querySha256: null,
    bindVarsSha256: null,
    bindScope: null,
    expectedIndex: options.expectedIndex ?? null,
    bounds: {
      execute: options.execute,
      maxRuntimeSeconds: options.maxRuntimeSeconds,
      memoryLimitBytes: options.memoryLimitBytes,
      explainHostTimeoutMs: EXPLAIN_HOST_TIMEOUT_MS,
      executeHostTimeoutMs: options.execute ? executionHostTimeout(options.maxRuntimeSeconds) : null,
    },
    timing: {
      inspectHostElapsedMs: null,
      parseAndExplainHostElapsedMs: null,
      parseServerElapsedMs: null,
      explainServerElapsedMs: null,
      executeHostElapsedMs: null,
      executeServerElapsedMs: null,
    },
    process: { inspect: null, parseAndExplain: null, execute: null },
    parser: { parsed: false, readOnlyCollectionAccess: false },
    plan: {
      readOnly: false,
      planCount: 0,
      planEstimates: [],
      nodes: [],
      appliedRules: [],
      indexes: [],
      scanNodes: [],
      fullCollectionScans: [],
      candidateIndexSelected: null,
    },
    execution: null,
    response: { errorNum: null, errorCode: null, message: null, warnings: [] },
    failure: null,
  };
}

function fail(report, stage, message, errorNum = null, errorCode = null) {
  report.status = 'failed';
  report.failure = { stage, message };
  report.response = {
    ...report.response,
    errorNum: finiteNumberOrNull(errorNum),
    errorCode: finiteNumberOrNull(errorCode),
    message,
  };
  return report;
}

function validateBindScope(query, bindVars) {
  if (!isRecord(bindVars)) return { message: 'bind-vars JSON must be an object' };
  if (bindVars.project !== PROJECT_SCOPE) return { message: 'project bind does not match the required scope' };
  const generationNames = ['dataset_generation', 'generation'].filter(name => Object.hasOwn(bindVars, name));
  if (generationNames.length === 0 || generationNames.some(name => bindVars[name] !== GENERATION_SCOPE)) {
    return { message: 'generation bind does not match the required scope' };
  }
  if (!Object.hasOwn(bindVars, 'auth_resource_paths_unrestricted') || !Object.hasOwn(bindVars, 'auth_resource_paths')) {
    return { message: 'authorization bind scope must be explicit' };
  }
  const unrestricted = bindVars.auth_resource_paths_unrestricted === true
    && (bindVars.auth_resource_paths === null || (Array.isArray(bindVars.auth_resource_paths) && bindVars.auth_resource_paths.length === 0));
  const restricted = bindVars.auth_resource_paths_unrestricted === false
    && Array.isArray(bindVars.auth_resource_paths)
    && bindVars.auth_resource_paths.every(value => typeof value === 'string');
  if (!unrestricted && !restricted) return { message: 'authorization bind scope is invalid' };

  const collectionBindNames = [...query.matchAll(/@@([A-Za-z_][A-Za-z0-9_]*)/g)].map(match => match[1]);
  if (!collectionBindNames.includes('root_collection')) return { message: 'query must preserve the @root_collection placeholder' };
  const missingCollectionBinds = [...new Set(collectionBindNames)].filter(name => {
    const value = bindVars[`@${name}`];
    return typeof value !== 'string' || !value;
  });
  if (missingCollectionBinds.length > 0) return { message: 'one or more collection bind values are missing' };
  const referencedScopeBinds = ['project', 'auth_resource_paths_unrestricted', 'auth_resource_paths'];
  const missingScopeBinds = referencedScopeBinds.filter(name => !new RegExp(`(^|[^@])@${name}\\b`).test(query));
  const generationBindName = generationNames.find(name => new RegExp(`(^|[^@])@${name}\\b`).test(query));
  if (!generationBindName) missingScopeBinds.push('generation');
  if (missingScopeBinds.length > 0) return { message: 'query must use each explicit project, generation, and authorization scope bind' };
  return {
    authScope: unrestricted ? 'unrestricted' : 'restricted',
    generationBindName,
    collectionBindCount: new Set(collectionBindNames).size,
  };
}

function safeProcessError(result) {
  if (result.error?.code === 'ETIMEDOUT') return 'host command exceeded its bounded timeout';
  if (result.error?.code === 'ENOENT') return 'required local command was unavailable';
  if (result.error) return 'host command could not complete';
  return 'host command returned a nonzero status';
}

function processSummary(result) {
  const hostErrorCode = typeof result.error?.code === 'string' && /^[A-Z0-9_]+$/.test(result.error.code)
    ? result.error.code
    : null;
  const signal = typeof result.signal === 'string' && /^SIG[A-Z0-9]+$/.test(result.signal) ? result.signal : null;
  return {
    exitCode: finiteNumberOrNull(result.status),
    hostErrorCode,
    signal,
  };
}

function parseMarker(stdout) {
  const line = String(stdout ?? '').split(/\r?\n/).find(value => value.includes(MARKER));
  if (!line) return null;
  try { return JSON.parse(line.slice(line.indexOf(MARKER) + MARKER.length)); } catch { return null; }
}

function planReadOnlySummary(envelope, expectedIndex) {
  const allNodes = Array.isArray(envelope.nodes) ? envelope.nodes.filter(isRecord) : [];
  const selectedPlanIndex = Number.isSafeInteger(envelope.selectedPlanIndex) && envelope.selectedPlanIndex >= 0
    ? envelope.selectedPlanIndex
    : 0;
  const nodes = allNodes.flatMap(node => {
    if (node.plan !== selectedPlanIndex || nonNegativeIntegerOrNull(node.nodeId) === null
      || typeof node.type !== 'string' || node.type.length === 0) return [];
    return [{ id: node.nodeId, type: node.type }];
  });
  const writes = nodes.some(node => WRITE_NODE_TYPES.has(node.type));
  const readOnly = nodes.length > 0 && envelope.readOnlyPlan === true && !writes;
  const indexes = Array.isArray(envelope.indexes) ? envelope.indexes : [];
  const fullCollectionScans = allNodes.filter(node => node.type === 'EnumerateCollectionNode').map(node => ({
    plan: node.plan, nodeId: node.nodeId, collection: node.collection,
  }));
  const scanNodes = allNodes.filter(node => /^Enumerate.*Collection|Index/.test(node.type ?? '')).map(node => ({
    plan: node.plan,
    nodeId: node.nodeId,
    type: node.type,
    collection: node.collection,
    estimatedNrItems: node.estimatedNrItems,
    indexes: indexes.filter(index => index.plan === node.plan && index.nodeId === node.nodeId),
  }));
  const candidateIndexSelected = expectedIndex === undefined ? null : indexes.some(index => index.name === expectedIndex);
  const appliedRules = Array.isArray(envelope.appliedRules)
    ? [...new Set(envelope.appliedRules.filter(isSafeRuleName))]
    : [];
  return {
    readOnly,
    planCount: Number.isInteger(envelope.planCount) ? envelope.planCount : 0,
    planEstimates: Array.isArray(envelope.planEstimates) ? envelope.planEstimates : [],
    nodes,
    appliedRules,
    indexes,
    scanNodes,
    fullCollectionScans,
    candidateIndexSelected,
  };
}

async function writeFreshReport(outputHandle, report) {
  await outputHandle.writeFile(`${JSON.stringify(report, null, 2)}\n`);
  await outputHandle.close();
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  const outputPath = options.outputPath ? resolve(options.outputPath) : null;
  const report = baseReport(options);
  const cliError = options.parseError ? 'arguments contain an unknown, duplicate, or incomplete option' : validateCliOptions(options);
  let outputHandle = null;

  if (outputPath) {
    try {
      outputHandle = await open(outputPath, 'wx', 0o600);
    } catch (error) {
      const safeMessage = error?.code === 'EEXIST'
        ? 'output path already exists; report was not overwritten'
        : 'structured report could not be opened';
      process.stderr.write(`${safeMessage}\n`);
      process.exitCode = 1;
      return;
    }
  }

  if (cliError) {
    fail(report, 'arguments', cliError);
  } else {
    try {
      const queryBytes = await readFile(resolve(options.queryPath));
      const bindBytes = await readFile(resolve(options.bindVarsPath));
      const query = queryBytes.toString('utf8');
      const bindVars = JSON.parse(bindBytes.toString('utf8'));
      report.querySha256 = sha256(queryBytes);
      report.bindVarsSha256 = sha256(bindBytes);
      const bindScope = validateBindScope(query, bindVars);
      if (bindScope.message) {
        fail(report, 'input-validation', bindScope.message);
      } else {
        report.bindScope = {
          authorization: bindScope.authScope,
          generationBindName: bindScope.generationBindName,
          collectionBindCount: bindScope.collectionBindCount,
        };

        const inspectStarted = Date.now();
        const inspectResult = run('docker', ['inspect', CONTAINER], 10000);
        report.timing.inspectHostElapsedMs = Date.now() - inspectStarted;
        report.process.inspect = processSummary(inspectResult);
        if (inspectResult.error || inspectResult.status !== 0) {
          fail(report, 'container-inspect', safeProcessError(inspectResult));
        } else {
          let inspectRows;
          try { inspectRows = JSON.parse(inspectResult.stdout); } catch { inspectRows = null; }
          try {
            if (!Array.isArray(inspectRows) || inspectRows.length !== 1) throw new Error('ownership');
            validateOwnedArangoContainer(inspectRows[0]);
            const labels = inspectRows[0].Config?.Labels ?? {};
            if (labels['com.docker.compose.project'] !== COMPOSE_PROJECT
              || labels['com.docker.compose.service'] !== 'arangodb'
              || inspectRows[0].Name?.replace(/^\//, '') !== CONTAINER) throw new Error('ownership');
          } catch {
            fail(report, 'container-ownership', 'container is not the exact owned running Arango service');
          }
        }

        if (!report.failure) {
          const explainScript = buildExplainRequestScript({ query, bindVars });
          const invocation = buildArangoShellInvocation({ container: CONTAINER, database: DATABASE, script: explainScript });
          const started = Date.now();
          const result = run(invocation.command, invocation.args, EXPLAIN_HOST_TIMEOUT_MS);
          report.timing.parseAndExplainHostElapsedMs = Date.now() - started;
          report.process.parseAndExplain = processSummary(result);
          const envelope = parseMarker(result.stdout);
          if (!envelope) {
            fail(report, 'parse-and-explain-transport', result.error ? safeProcessError(result) : 'Arango result marker was missing or invalid');
          } else {
            report.timing.parseServerElapsedMs = finiteNumberOrNull(envelope.parseServerElapsedMs);
            report.timing.explainServerElapsedMs = finiteNumberOrNull(envelope.explainServerElapsedMs);
            report.parser.parsed = envelope.parseStatusCode >= 200 && envelope.parseStatusCode < 300
              && !['parse-transport', 'parse-failed'].includes(envelope.stage);
            report.parser.readOnlyCollectionAccess = report.parser.parsed && envelope.parserReadOnly === true;
            report.response = {
              ...report.response,
              errorNum: finiteNumberOrNull(envelope.errorNum),
              errorCode: finiteNumberOrNull(envelope.errorCode),
              message: typeof envelope.message === 'string' ? envelope.message : null,
              warnings: Array.isArray(envelope.warnings) ? envelope.warnings : [],
            };
            const plan = planReadOnlySummary(envelope, options.expectedIndex);
            report.plan = plan;
            if (envelope.stage === 'parse-transport' || envelope.stage === 'explain-transport') {
              fail(report, envelope.stage, envelope.message ?? 'Arango transport failed');
            } else if (envelope.stage !== 'explain-complete') {
              fail(report, envelope.stage ?? 'explain', envelope.message ?? 'Arango parser or EXPLAIN did not complete', envelope.errorNum, envelope.errorCode);
            } else if (!plan.readOnly) {
              fail(report, 'read-only-plan-check', 'EXPLAIN plan did not pass the read-only checks');
            } else if (result.error || result.status !== 0) {
              fail(report, 'parse-and-explain-host', safeProcessError(result));
            } else {
              report.status = 'explain-complete';
              if (options.expectedIndex !== undefined && plan.candidateIndexSelected !== true) {
                report.status = 'expected-index-mismatch';
                report.failure = { stage: 'expected-index', message: 'EXPLAIN did not select the expected index; execution was suppressed' };
              } else if (options.execute) {
                const executeScript = buildExecuteRequestScript({
                  query,
                  bindVars,
                  maxRuntimeSeconds: options.maxRuntimeSeconds,
                  memoryLimitBytes: options.memoryLimitBytes,
                  safeIntegerCounts: options.safeIntegerCounts,
                });
                const executeInvocation = buildArangoShellInvocation({
                  container: CONTAINER,
                  database: DATABASE,
                  script: executeScript,
                });
                const executeStarted = Date.now();
                const executeResult = run(executeInvocation.command, executeInvocation.args, executionHostTimeout(options.maxRuntimeSeconds));
                report.timing.executeHostElapsedMs = Date.now() - executeStarted;
                report.process.execute = processSummary(executeResult);
                const executeEnvelope = parseMarker(executeResult.stdout);
                if (!executeEnvelope) {
                  fail(report, 'execute-transport', executeResult.error ? safeProcessError(executeResult) : 'Arango result marker was missing or invalid');
                } else {
                  report.timing.executeServerElapsedMs = finiteNumberOrNull(executeEnvelope.elapsedMs);
                  report.execution = {
                    completed: executeEnvelope.ok === true,
                    resultCount: finiteNumberOrNull(executeEnvelope.resultCount),
                    scannedIndex: finiteNumberOrNull(executeEnvelope.stats?.scannedIndex),
                    scannedFull: finiteNumberOrNull(executeEnvelope.stats?.scannedFull),
                    peakMemoryUsage: finiteNumberOrNull(executeEnvelope.stats?.peakMemoryUsage),
                    peakMemoryUsageStatus: Number.isFinite(executeEnvelope.stats?.peakMemoryUsage) ? 'available' : 'unavailable',
                    executionTime: finiteNumberOrNull(executeEnvelope.stats?.executionTime),
                    documentLookups: nonNegativeIntegerOrNull(executeEnvelope.stats?.documentLookups),
                    nodes: safeProfileNodes(executeEnvelope.stats?.nodes),
                  };
                  report.response = {
                    ...report.response,
                    errorNum: finiteNumberOrNull(executeEnvelope.errorNum),
                    errorCode: finiteNumberOrNull(executeEnvelope.errorCode),
                    message: executeEnvelope.ok === true ? null : SAFE_ARANGO_ERROR,
                    warnings: Array.isArray(executeEnvelope.warnings)
                      ? [...report.response.warnings, ...executeEnvelope.warnings]
                      : report.response.warnings,
                  };
                  if (executeEnvelope.ok !== true) {
                    fail(report, 'execute', SAFE_ARANGO_ERROR, executeEnvelope.errorNum, executeEnvelope.errorCode);
                  } else if (options.safeIntegerCounts) {
                    const counts = executeEnvelope.safeIntegerCounts;
                    const validCounts = isRecord(counts)
                      && executeEnvelope.resultCount === 1
                      && Object.keys(counts).length > 0
                      && Object.keys(counts).length <= SAFE_INTEGER_COUNT_KEYS.size
                      && Object.entries(counts).every(([key, value]) => SAFE_INTEGER_COUNT_KEYS.has(key) && Number.isSafeInteger(value) && value >= 0);
                    if (!validCounts) {
                      fail(report, 'safe-integer-result', 'query did not return only non-negative safe integer counts');
                    } else {
                      report.execution.safeIntegerCounts = Object.fromEntries(
                        Object.entries(counts).sort(([left], [right]) => left.localeCompare(right)),
                      );
                    }
                  }
                  if (report.failure === null) {
                    if (executeEnvelope.ok === true && !executeResult.error && executeResult.status === 0) {
                      report.status = 'query-complete';
                    } else {
                      fail(report, 'execute', executeResult.error
                        ? safeProcessError(executeResult)
                        : 'bounded query execution did not complete', executeEnvelope.errorNum, executeEnvelope.errorCode);
                    }
                  }
                }
              }
            }
          }
        }
      }
    } catch (error) {
      const stage = error?.code === 'ENOENT' ? 'input-read' : error instanceof SyntaxError ? 'input-parse' : 'probe';
      fail(report, stage, stage === 'input-read' ? 'query or bind-vars source could not be read' : stage === 'input-parse' ? 'bind-vars source is not valid JSON' : 'query probe failed safely');
    }
  }

  if (outputPath) {
    try {
      await writeFreshReport(outputHandle, report);
    } catch (error) {
      const safeMessage = 'structured report could not be written';
      try { await outputHandle.close(); } catch {}
      process.stderr.write(`${safeMessage}\n`);
      process.exitCode = 1;
      return;
    }
    process.stdout.write(`${JSON.stringify({ outputPath, status: report.status, failure: report.failure })}\n`);
  } else {
    process.stderr.write('a fresh --output path is required to write the structured report\n');
  }
  if (report.status !== 'explain-complete' && report.status !== 'query-complete') process.exitCode = 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
