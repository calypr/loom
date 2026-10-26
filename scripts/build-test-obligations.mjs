#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync, mkdirSync } from 'node:fs';
import { basename, relative, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { isCurrentBrowserEvidence } from './lib/builder-route-evidence.mjs';
import { planFocusedCheckGroups } from './verify-ui/helpers/focused-check-groups.mjs';

const ROOT = resolve(new URL('..', import.meta.url).pathname);
const MODEL_DIR = 'verification/builder-interactions';
const LEDGER = MODEL_DIR + '/route-contract.json';
const TRACE_CONTRACT = MODEL_DIR + '/builder-trace-contract.json';
const BROWSER_EVIDENCE = MODEL_DIR + '/browser-evidence.json';
const REGISTRY = 'scripts/verify-ui/registry.mjs';
const FORMAL_RUNNER = 'scripts/verify-builder-formal.mjs';
const UI_ROOT = 'ui/packages/loom-ui/src/features/ExplorerBuilder';
const GO_ROOT = 'internal';
const OUTPUT_JSON = '.cache/builder-verification/test-obligations.json';
const OUTPUT_MD = '.cache/builder-verification/test-obligations.md';

const args = process.argv.slice(2);
const suppliedJsonPath = valueFor('--json');
const jsonPath = suppliedJsonPath ?? OUTPUT_JSON;
const markdownPath = valueFor('--markdown') ?? OUTPUT_MD;
const runtimeSummaryPaths = valuesFor('--runtime-summary');
const runtimeOutputPath = valueFor('--runtime-output');
const check = args.includes('--check');
if (args.includes('--details')) fail('The semantic catalog export is retired; use --json to export the derived obligation report.');

if (args.includes('--json') && (!suppliedJsonPath || suppliedJsonPath.startsWith('--'))) fail('--json needs a path');
if (runtimeSummaryPaths.length > 0 && !runtimeOutputPath) {
  fail('--runtime-summary requires an explicit external --runtime-output path');
}
if (runtimeSummaryPaths.length > 0 && (args.includes('--json') || args.includes('--markdown'))) {
  fail('--runtime-summary writes only to --runtime-output; static JSON/Markdown output paths cannot be overridden in runtime mode');
}
if (runtimeSummaryPaths.length === 0 && runtimeOutputPath) {
  fail('--runtime-output requires at least one --runtime-summary');
}

function valueFor(name) {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
}

function valuesFor(name) {
  const values = [];
  for (let index = 0; index < args.length; index++) {
    if (args[index] !== name) continue;
    const value = args[index + 1];
    if (!value || value.startsWith('--')) fail(name + ' needs a path');
    values.push(value);
  }
  return values;
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

function nonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function buildRegistryCaseIndex(registry) {
  const cases = new Map();
  for (const scenario of registry) {
    for (const [caseName, caseData] of Object.entries(scenario.cases ?? {})) {
      const focusedChecks = new Map();
      const configuredChecks = caseData.focusedChecks ?? [];
      planFocusedCheckGroups(configuredChecks, ROOT);
      for (const focusedCheck of configuredChecks) {
        if (!nonEmptyString(focusedCheck.id)) continue;
        focusedChecks.set(focusedCheck.id, {
          id: focusedCheck.id,
          cwd: focusedCheck.cwd ?? '.',
          command: Array.isArray(focusedCheck.command) ? [...focusedCheck.command] : [],
          sourceFiles: Array.isArray(focusedCheck.sourceFiles) ? [...focusedCheck.sourceFiles] : [],
        });
      }
      cases.set(scenario.id + ':' + caseName, {
        focusedChecks,
      });
    }
  }
  return cases;
}

function repoPath(path) {
  const absolute = resolve(ROOT, path);
  const fromRoot = relative(ROOT, absolute);
  if (fromRoot === '..' || fromRoot.startsWith('../') || fromRoot.startsWith('..\\')) return null;
  return fromRoot.split('\\').join('/');
}

function externalRuntimeOutput(path) {
  const absolute = resolve(ROOT, path);
  if (repoPath(absolute) !== null) throw new Error('--runtime-output must be outside the repository');
  return absolute;
}

function configuredCommandMjsPaths(focusedCheck, knownPaths) {
  const command = focusedCheck.command ?? [];
  const fileOffset = command[0] === 'node-test' ? 1 : 4;
  return uniqueSorted(command.slice(fileOffset)
    .filter((argument) => typeof argument === 'string' && argument.endsWith('.mjs'))
    .map((argument) => repoPath(argument))
    .filter((path) => knownPaths.has(path)));
}

function focusedInputPaths(focusedCheck) {
  const command = focusedCheck.command ?? [];
  const fileOffset = command[0] === 'node-test' ? 1 : 4;
  return [...command.slice(fileOffset), ...(focusedCheck.sourceFiles ?? [])];
}

function uniqueRuntimeEvidence(evidence) {
  const unique = new Map(evidence.map(entry => [JSON.stringify(entry), entry]));
  return [...unique.values()].sort((left, right) => left.caseId.localeCompare(right.caseId)
    || left.checkId.localeCompare(right.checkId)
    || left.summaryPath.localeCompare(right.summaryPath));
}

function currentInputSha256(path) {
  const absolute = resolve(ROOT, path);
  if (!existsSync(absolute) || !statSync(absolute).isFile()) return null;
  return createHash('sha256').update(readFileSync(absolute)).digest('hex');
}

function planFromRecordedStageCwd(focusedCheck, commandStage) {
  if (typeof commandStage?.cwd !== 'string' || resolve(commandStage.cwd) !== commandStage.cwd) return null;
  const cwdParts = focusedCheck.cwd === '.' ? [] : focusedCheck.cwd.split('/');
  const repoRoot = resolve(commandStage.cwd, ...cwdParts.map(() => '..'));
  return planFocusedCheckGroups([focusedCheck], repoRoot)[0];
}

function validateFocusedSummaryCase(summary, caseIndex, suppliedPath = 'runtime summary') {
  if (summary?.schemaVersion !== 1 || !nonEmptyString(summary.scenario) || !nonEmptyString(summary.case)) {
    throw new Error('runtime summary needs schemaVersion 1, scenario, and case: ' + suppliedPath);
  }
  if (!['checks-only', 'browser'].includes(summary.mode)) {
    throw new Error('runtime summary mode must be checks-only or browser: ' + suppliedPath);
  }
  const caseId = summary.scenario + ':' + summary.case;
  const registeredCase = caseIndex.get(caseId);
  if (!registeredCase) throw new Error('runtime summary names an unknown case: ' + caseId);
  const groups = summary.focusedChecks?.groups;
  const expectedGroupIds = [...registeredCase.focusedChecks.keys()];
  if (!Array.isArray(groups) || groups.length !== expectedGroupIds.length
    || groups.some((group, index) => group?.id !== expectedGroupIds[index])) {
    throw new Error('runtime summary focused groups do not exactly match the registered case: ' + caseId);
  }
  return { caseId, registeredCase, groups };
}

function validateFocusedGroup(summary, group, focusedCheck, caseId, summaryPath, summaryHash, knownPaths,
  fileSha256 = currentInputSha256) {
  const commandStage = summary.commands?.['focused-' + group.id];
  let plan;
  try {
    plan = planFromRecordedStageCwd(focusedCheck, commandStage);
  } catch {
    plan = null;
  }
  const commandPaths = configuredCommandMjsPaths(focusedCheck, knownPaths)
    .filter((path) => /\.(?:test|spec)\.mjs$/.test(path));
  const reject = (reason) => ({
    evidence: [],
    exceptions: commandPaths.map((path) => ({ path, reason: caseId + '/' + group.id + ' ' + reason })),
  });
  if (group.status !== 'passed' || group.exitCode !== 0 || group.timedOut === true || group.inputsUnchanged !== true) {
    return reject('did not report a passed focused group with unchanged inputs.');
  }
  if (!plan) return reject('command stage cwd cannot form the registered focused-check plan.');
  if (group.runner !== plan.runner) return reject('runner does not match the registered focused-check plan.');
  if (!commandStage || commandStage.executable !== plan.executable
    || JSON.stringify(commandStage.arguments) !== JSON.stringify(plan.args)
    || commandStage.cwd !== plan.cwd) {
    return reject('command stage executable, arguments, or cwd differs from the registered focused-check plan.');
  }
  if (commandStage.exitCode !== 0 || commandStage.timedOut === true || commandStage.aborted === true) {
    return reject('command stage failed, timed out, or aborted.');
  }
  const inputPaths = focusedInputPaths(focusedCheck);
  if (!Array.isArray(group.inputs) || group.inputs.length !== inputPaths.length) {
    return reject('input list does not exactly match configured command and sourceFiles inputs.');
  }
  const expectedInputs = [];
  for (const [index, path] of inputPaths.entries()) {
    const input = group.inputs[index];
    if (!isRecord(input) || input.path !== path || !/^[a-f0-9]{64}$/.test(input.sha256 ?? '')) {
      return reject('input list is missing, reordered, or contains an undeclared input.');
    }
    const currentSha256 = fileSha256(path);
    if (!currentSha256 || input.sha256 !== currentSha256) {
      return reject('input SHA differs from current file: ' + path + '.');
    }
    expectedInputs.push({ path, sha256: currentSha256 });
  }
  const declaredInputsHash = createHash('sha256').update(JSON.stringify(expectedInputs)).digest('hex');
  if (group.declaredInputsHash !== declaredInputsHash) return reject('declared input-set hash does not match current inputs.');
  return {
    evidence: commandPaths.map((path) => ({
      kind: 'focused-command-passed',
      path,
      caseId,
      checkId: group.id,
      runner: plan.runner,
      executable: plan.executable,
      arguments: plan.args,
      cwd: plan.cwd,
      currentSha256: expectedInputs.find((input) => input.path === path)?.sha256,
      summaryPath,
      summarySha256: summaryHash,
    })),
    exceptions: [],
  };
}

function loadFocusedTestEvidence(summaryPaths, caseIndex, knownPaths) {
  const evidenceByPath = new Map();
  const exceptionsByPath = new Map();
  const summaries = [];
  const add = (map, path, value) => {
    if (!map.has(path)) map.set(path, []);
    map.get(path).push(value);
  };
  for (const suppliedPath of summaryPaths) {
    const absolutePath = resolve(ROOT, suppliedPath);
    let summaryText;
    let summary;
    try {
      summaryText = readFileSync(absolutePath, 'utf8');
      summary = JSON.parse(summaryText);
    } catch (error) {
      throw new Error('cannot read runtime summary ' + suppliedPath + ': ' + error.message);
    }
    const { caseId, registeredCase, groups } = validateFocusedSummaryCase(summary, caseIndex, suppliedPath);
    const summaryHash = createHash('sha256').update(summaryText).digest('hex');
    const summaryDisplayPath = repoPath(absolutePath) ?? suppliedPath;
    summaries.push({ caseId, path: summaryDisplayPath, sha256: summaryHash, mode: summary.mode });
    for (const group of groups) {
      const focusedCheck = registeredCase.focusedChecks.get(group.id);
      const validation = validateFocusedGroup(
        summary,
        group,
        focusedCheck,
        caseId,
        summaryDisplayPath,
        summaryHash,
        knownPaths,
      );
      for (const evidence of validation.evidence) add(evidenceByPath, evidence.path, evidence);
      for (const exception of validation.exceptions) add(exceptionsByPath, exception.path, exception.reason);
    }
  }
  for (const [path, evidence] of evidenceByPath) evidenceByPath.set(path, uniqueRuntimeEvidence(evidence));
  for (const [path, exceptions] of exceptionsByPath) exceptionsByPath.set(path, uniqueSorted(exceptions));
  return { evidenceByPath, exceptionsByPath, summaries };
}

function selfTestFocusedReceipts() {
  const assert = (condition, message) => {
    if (!condition) throw new Error('focused receipt self-test: ' + message);
  };
  const commandPath = 'scripts/example.test.mjs';
  const pinnedMjsPath = 'scripts/example-helper.mjs';
  const pinnedDataPath = 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx';
  const check = {
    id: 'example-check',
    cwd: '.',
    command: ['node-test', commandPath],
    sourceFiles: [pinnedMjsPath, pinnedDataPath],
  };
  const plan = planFocusedCheckGroups([check], ROOT)[0];
  const focusedCheck = { ...check, plan };
  const caseId = 'builder-example:command-test';
  const caseIndex = new Map([[caseId, { focusedChecks: new Map([[check.id, focusedCheck]]) }]]);
  const currentHashes = new Map([
    [commandPath, 'a'.repeat(64)],
    [pinnedMjsPath, 'b'.repeat(64)],
    [pinnedDataPath, 'c'.repeat(64)],
  ]);
  const expectedInputs = focusedInputPaths(focusedCheck).map((path) => ({ path, sha256: currentHashes.get(path) }));
  const group = {
    id: check.id,
    runner: plan.runner,
    status: 'passed',
    exitCode: 0,
    timedOut: false,
    inputs: expectedInputs,
    declaredInputsHash: createHash('sha256').update(JSON.stringify(expectedInputs)).digest('hex'),
    inputsUnchanged: true,
  };
  const commandStage = {
    executable: plan.executable,
    arguments: [...plan.args],
    cwd: plan.cwd,
    exitCode: 0,
  };
  const browserFailedSummary = {
    schemaVersion: 1,
    scenario: 'builder-example',
    case: 'command-test',
    mode: 'browser',
    status: 'failed',
    nativeStatus: 'failed',
    focusedChecks: { groups: [group] },
    commands: { ['focused-' + check.id]: commandStage },
  };
  const matchingCase = validateFocusedSummaryCase(browserFailedSummary, caseIndex);
  assert(matchingCase.caseId === caseId, 'browser failure must not invalidate the per-case identity');
  const valid = validateFocusedGroup(
    browserFailedSummary, group, focusedCheck, caseId, 'summary.json', 'd'.repeat(64),
    new Set([commandPath, pinnedMjsPath]), (path) => currentHashes.get(path),
  );
  assert(valid.evidence.length === 1 && valid.evidence[0].kind === 'focused-command-passed'
    && valid.evidence[0].path === commandPath, 'passed command target alone receives focused-command evidence');

  const registryFixture = [{
    id: 'builder-example',
    cases: { 'command-test': { focusedChecks: [check] } },
  }];
  const registered = buildRegistryCaseIndex(registryFixture).get(caseId);
  assert(registered.focusedChecks.has(check.id), 'actual focused command stays registered');
  assert(valid.evidence.every((entry) => entry.path !== pinnedMjsPath),
    'a sourceFiles-only input never receives execution credit');

  const wrongCase = { ...browserFailedSummary, case: 'another-case' };
  let wrongCaseRejected = false;
  try { validateFocusedSummaryCase(wrongCase, caseIndex); } catch { wrongCaseRejected = true; }
  assert(wrongCaseRejected, 'wrong case is rejected');

  const supersededCommand = {
    ...browserFailedSummary,
    commands: { ['focused-' + check.id]: { ...commandStage, arguments: [...plan.args, '--extra'] } },
  };
  assert(validateFocusedGroup(supersededCommand, group, focusedCheck, caseId, 'summary.json', 'd'.repeat(64),
    new Set([commandPath, pinnedMjsPath]), (path) => currentHashes.get(path)).evidence.length === 0,
  'superset command argv is rejected');

  const missingInputGroup = { ...group, inputs: group.inputs.slice(0, -1) };
  missingInputGroup.declaredInputsHash = createHash('sha256').update(JSON.stringify(missingInputGroup.inputs)).digest('hex');
  assert(validateFocusedGroup(browserFailedSummary, missingInputGroup, focusedCheck, caseId, 'summary.json', 'd'.repeat(64),
    new Set([commandPath, pinnedMjsPath]), (path) => currentHashes.get(path)).evidence.length === 0,
  'missing declared input is rejected');

  const staleNonMjsInputGroup = { ...group, inputs: group.inputs.map((input) => ({ ...input })) };
  staleNonMjsInputGroup.inputs[2].sha256 = 'e'.repeat(64);
  staleNonMjsInputGroup.declaredInputsHash = createHash('sha256').update(JSON.stringify(staleNonMjsInputGroup.inputs)).digest('hex');
  assert(validateFocusedGroup(browserFailedSummary, staleNonMjsInputGroup, focusedCheck, caseId, 'summary.json', 'd'.repeat(64),
    new Set([commandPath, pinnedMjsPath]), (path) => currentHashes.get(path)).evidence.length === 0,
  'stale non-MJS declared input is rejected');

  let defaultOutputRejected = false;
  try { externalRuntimeOutput(OUTPUT_JSON); } catch { defaultOutputRejected = true; }
  assert(defaultOutputRejected, 'runtime output cannot replace the derived obligation report');
  const runtimeOutput = externalRuntimeOutput(resolve(ROOT, '../focused-command-join.json'));
  const runtimeRows = new Map([[commandPath, valid.evidence]]);
  const runtimeJoin = {
    summaries: [{ caseId, path: 'summary.json', sha256: 'd'.repeat(64), mode: 'browser' }],
    evidenceByPath: runtimeRows,
    exceptionsByPath: new Map(),
  };
  const firstReport = JSON.stringify(focusedCommandJoinReport(runtimeJoin), null, 2) + '\n';
  const secondReport = JSON.stringify(focusedCommandJoinReport(runtimeJoin), null, 2) + '\n';
  assert(runtimeOutput.endsWith('focused-command-join.json') && firstReport === secondReport,
    'explicit external report path is accepted and serialization is idempotent');
  console.log('PASS focused receipt self-test: exact plan, pinned-only source, and fail-closed negatives');
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
  selfTestFocusedReceipts();
  process.exit(0);
}

const ledger = readJson(LEDGER);
const traceContract = readJson(TRACE_CONTRACT);
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
const caseIndex = buildRegistryCaseIndex(registry);
let runtimeJoin;
if (runtimeSummaryPaths.length > 0) {
  const listed = spawnSync('git', ['ls-files', '-z', '--', '*.mjs'], { cwd: ROOT, encoding: 'utf8' });
  if (listed.status !== 0) fail('cannot identify tracked focused test files');
  const knownPaths = new Set(listed.stdout.split('\0').filter(Boolean));
  try {
    runtimeJoin = loadFocusedTestEvidence(runtimeSummaryPaths, caseIndex, knownPaths);
  } catch (error) {
    fail('runtime summary rejected: ' + error.message);
  }
}
for (const entry of registry) {
  const entryRoutes = uniqueSorted((entry.endpoints ?? []).map(parseEndpoint).filter(Boolean));
  for (const [caseName, caseData] of Object.entries(entry.cases ?? {})) {
    const id = entry.id + ':' + caseName;
    const evidence = browserCases.get(id);
    const currentEvidence = isCurrentBrowserEvidence(evidence);
    const routes = evidence ? uniqueSorted(evidence.coveredRoutes ?? []) : entryRoutes;
    addTest({
      id: 'playwright:' + id,
      kind: 'playwright',
      level: 'browser',
      name: caseData.playwrightGrep ?? caseName,
      caseName,
      file: caseData.playwrightTest ?? entry.script ?? REGISTRY,
      routes,
      obligations: currentEvidence ? routeObligations(routes) : [],
      suspectedModels: suspectedModelsForRoutes(routes),
      checks: Array.isArray(caseData.requiredChecks) ? caseData.requiredChecks : [],
      focusedChecks: uniqueSorted((caseData.focusedChecks ?? []).map((check) => check.id)),
      mapping: currentEvidence ? 'browser-evidence' : evidence ? 'unmapped' : 'registry-endpoints',
      evidence: evidence ? BROWSER_EVIDENCE : REGISTRY,
      browserEvidenceStatus: evidence ? { status: evidence.status, sourceStatus: evidence.sourceStatus } : null,
    });
  }
}

for (const [caseId, evidence] of browserCases) {
  const existing = tests.get('playwright:' + caseId);
  if (existing) {
    existing.browserEvidenceStatus = { status: evidence.status, sourceStatus: evidence.sourceStatus };
    if (isCurrentBrowserEvidence(evidence)) existing.mapping = 'browser-evidence';
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
    const browserEvidence = browserCases.get(route.proof.browserCase);
    if (isCurrentBrowserEvidence(browserEvidence)) {
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

function focusedCommandJoinReport(runtimeJoin) {
  const executedTests = [...runtimeJoin.evidenceByPath.entries()].flatMap(([path, evidence]) => evidence.map((entry) => ({
    path,
    currentSha256: entry.currentSha256,
    evidence: entry,
  }))).sort((left, right) => left.path.localeCompare(right.path)
    || left.evidence.caseId.localeCompare(right.evidence.caseId)
    || left.evidence.checkId.localeCompare(right.evidence.checkId));
  return {
    schemaVersion: 2,
    evidenceKind: 'focused-command-passed',
    summaries: runtimeJoin.summaries,
    executedTests,
    rejectedGroups: [...runtimeJoin.exceptionsByPath.entries()]
      .map(([path, exceptions]) => ({ path, exceptions }))
      .sort((left, right) => left.path.localeCompare(right.path)),
    scope: 'Focused command execution only; no case, journey, browser, or formal-model pass is claimed.',
  };
}

const report = {
  schemaVersion: 4,
  sources: {
    modelDir: MODEL_DIR,
    routeLedger: LEDGER,
    traceContract: TRACE_CONTRACT,
    browserEvidence: existsSync(resolve(ROOT, BROWSER_EVIDENCE)) ? BROWSER_EVIDENCE : null,
    registry: REGISTRY,
    formalRunner: FORMAL_RUNNER,
    uiRoot: UI_ROOT,
    goRoot: GO_ROOT,
  },
  summary,
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

const compactProjection = { ...report, projectionKind: 'compact-coverage' };
const serialized = JSON.stringify(compactProjection, null, 2) + '\n';
if (runtimeSummaryPaths.length > 0) {
  let outputPath;
  try {
    outputPath = externalRuntimeOutput(runtimeOutputPath);
  } catch (error) {
    fail('runtime output rejected: ' + error.message);
  }
  if (runtimeSummaryPaths.some((path) => resolve(ROOT, path) === outputPath)) {
    fail('--runtime-output cannot replace a supplied summary');
  }
  const runtimeSerialized = JSON.stringify(focusedCommandJoinReport(runtimeJoin), null, 2) + '\n';
  if (check) {
    if (!existsSync(outputPath)) fail(runtimeOutputPath + ' does not exist');
    if (readFileSync(outputPath, 'utf8') !== runtimeSerialized) fail(runtimeOutputPath + ' is stale; rerun with the same --runtime-summary inputs');
    console.log('PASS ' + runtimeOutputPath + ' is current');
  } else {
    writeFileSync(outputPath, runtimeSerialized);
    console.log('wrote explicit focused-command join to ' + runtimeOutputPath);
  }
} else if (check) {
  if (!existsSync(resolve(ROOT, jsonPath))) fail(jsonPath + ' does not exist');
  if (readFileSync(resolve(ROOT, jsonPath), 'utf8') !== serialized) {
    fail(jsonPath + ' is stale; run node scripts/build-test-obligations.mjs');
  }
  console.log('PASS ' + jsonPath + ' is current');
} else {
  mkdirSync(resolve(ROOT, jsonPath, '..'), { recursive: true });
  writeFileSync(resolve(ROOT, jsonPath), serialized);
}

const lines = [];
lines.push('# Test obligations');
lines.push('');
lines.push('Status: generated from the TLA models and existing test metadata.');
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

if (runtimeSummaryPaths.length > 0) {
  // Runtime joins are kept in the explicitly selected external artifact.
} else if (check) {
  if (!existsSync(resolve(ROOT, markdownPath)) || readFileSync(resolve(ROOT, markdownPath), 'utf8') !== lines.join('\n')) {
    fail(markdownPath + ' is stale; run node scripts/build-test-obligations.mjs');
  }
} else {
  mkdirSync(resolve(ROOT, markdownPath, '..'), { recursive: true });
  writeFileSync(resolve(ROOT, markdownPath), lines.join('\n'));
  console.log('wrote ' + jsonPath + ' and ' + markdownPath + ' with ' + obligations.length + ' obligations and ' + tests.size + ' tests');
}
