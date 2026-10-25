#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createDevSession, createFreshVerificationFixture } from './loom-dev.mjs';
import {
  finalizeNativeBuilderJourneyReceipt,
  nativeBuilderJourneyReceiptFromReport,
  builderJourneyReportPaths,
  builderJourneyTraceBindingFor,
  resolveNativeBuilderJourneyArtifactPath,
} from './verify-ui/helpers/builder-journey-trace.mjs';
import {
  builderFormalAdapterForBinding,
  builderFormalAdapterForContract,
} from './verify-ui/helpers/builder-formal-adapters.mjs';
import { assertLoadedDataset } from './verify-ui/helpers/dataset-preflight.mjs';
import { registry, scenarioCaseFor } from './verify-ui/registry.mjs';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(SCRIPT_DIR, '..');
const CONTRACT = 'verification/builder-interactions/builder-journey-contract.json';
const SOURCE_MANIFEST = 'verification/builder-interactions/builder-preview-contract.json';
const MANIFEST = 'verification/builder-interactions/builder-journey-audit.json';
const CONFIG = 'playwright.journey.config.mjs';
const SPEC_GREP = 'builder journey groups twice and previews the applied table';
const DEFAULT_TRACE = '.artifacts/builder-journey/trace.json';
const DEFAULT_REPORT = '.artifacts/builder-journey/report.json';

const parseArgs = () => {
  const args = process.argv.slice(2);
  const options = { trace: null, report: null, journey: null, nativeCase: null, manifest: null, project: null, generation: null, explorer: null, uiUrl: null, apiUrl: null, target: null, evidenceParent: null, bootstrap: false, checkBinding: false };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--trace') options.trace = args[++index];
    else if (arg === '--report') options.report = args[++index];
    else if (arg === '--journey') options.journey = args[++index];
    else if (arg === '--native-case') options.nativeCase = args[++index];
    else if (arg === '--check-binding') options.checkBinding = true;
    else if (arg === '--manifest') options.manifest = args[++index];
    else if (arg === '--target') options.target = args[++index];
    else if (arg === '--evidence-parent') options.evidenceParent = args[++index];
    else if (arg === '--project') options.project = args[++index];
    else if (arg === '--generation') options.generation = args[++index];
    else if (arg === '--explorer') options.explorer = args[++index];
    else if (arg === '--ui-url') options.uiUrl = args[++index];
    else if (arg === '--api-url') options.apiUrl = args[++index];
    else if (arg === '--bootstrap') options.bootstrap = true;
    else if (arg === '--help' || arg === '-h') {
      console.log('usage: node scripts/run-builder-journey.mjs [--manifest <manifest.json>] [--journey <id> | --native-case <scenario>/<case>] [--check-binding] [--trace <trace.json>] [--report <report.json>] [--target <owned-target.json>] [--evidence-parent <directory>] [--project <dataset> --generation <generation> [--explorer <id>] [--ui-url <url>] [--api-url <url>] [--bootstrap]]');
      process.exit(0);
    } else {
      throw new Error('unknown argument ' + arg + '; use --help for usage');
    }
  }
  if (options.journey && options.nativeCase) throw new Error('Choose either --journey or --native-case, not both.');
  if (options.checkBinding && !options.nativeCase) throw new Error('--check-binding requires --native-case.');
  if (options.checkBinding && (options.trace || options.report || options.target || options.evidenceParent || options.project || options.generation || options.explorer || options.uiUrl || options.apiUrl || options.bootstrap)) {
    throw new Error('--check-binding accepts only the registered native case selection.');
  }
  if (options.nativeCase && options.manifest) throw new Error('--manifest selects legacy audit journeys and cannot be combined with --native-case.');
  if (!options.nativeCase && (options.target || options.evidenceParent)) {
    throw new Error('--target and --evidence-parent are only valid with --native-case.');
  }
  if (options.nativeCase && (options.project || options.generation || options.explorer || options.uiUrl || options.apiUrl || options.bootstrap)) {
    throw new Error('--native-case uses the registered native fixture; pass --target for its owned target configuration.');
  }
  return options;
};

const git = (args) => {
  const result = spawnSync('git', args, { cwd: ROOT, encoding: 'utf8' });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error((result.stderr || result.stdout || '').trim());
  return result.stdout.trim();
};

const canonicalJson = (value) => {
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  if (value && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}';
  }
  return JSON.stringify(value);
};

const fingerprintPinnedSources = (sources) => {
  if (!Array.isArray(sources) || sources.length === 0) throw new Error('Builder source manifest has no pinned sources');
  const entries = sources.map((source) => {
    const actual = createHash('sha256').update(readFileSync(resolve(ROOT, source.path))).digest('hex');
    if (actual !== source.sha256) {
      throw new Error('Builder proof source drift at ' + source.path + ': expected ' + source.sha256 + ', found ' + actual);
    }
    return { path: source.path, sha256: actual };
  }).sort((left, right) => left.path.localeCompare(right.path));
  return createHash('sha256').update(canonicalJson(entries)).digest('hex');
};

const selectJourney = (journeyId, manifestPath = MANIFEST) => {
  const manifest = JSON.parse(readFileSync(resolve(ROOT, manifestPath), 'utf8'));
  const entry = manifest.journeys?.find((journey) => journey.id === journeyId);
  if (!entry) throw new Error('unknown journey ' + journeyId + '; see ' + MANIFEST);
  const specPath = String(entry.spec ?? '').replace(/^scripts\//, '');
  if (!specPath || !existsSync(resolve(SCRIPT_DIR, specPath))) {
    throw new Error('journey ' + journeyId + ' has no spec yet: ' + String(entry.spec ?? '(missing)'));
  }
  let nativeSelection = null;
  if (entry.nativeCase) {
    const scenarioID = String(entry.nativeCase.scenario ?? '');
    const caseName = String(entry.nativeCase.case ?? '');
    const scenario = registry.find((candidate) => candidate.id === scenarioID);
    if (!scenario || !caseName) throw new Error('journey ' + journeyId + ' has an invalid registered native case mapping');
    const registeredCase = scenarioCaseFor(scenario, caseName);
    if (entry.spec !== registeredCase.playwrightTest) {
      throw new Error('journey ' + journeyId + ' spec must match registered native case ' + scenarioID + '/' + caseName);
    }
    nativeSelection = {
      scenarioID,
      caseName,
      nativeCaseKey: `${scenarioID}/${caseName}`,
      playwrightTest: registeredCase.playwrightTest,
      playwrightGrep: registeredCase.playwrightGrep,
      traceName: registeredCase.journeyTrace?.name ?? null,
      model: registeredCase.journeyTrace?.model ?? entry.model ?? null,
      contract: registeredCase.journeyTrace?.contract ?? entry.contract ?? CONTRACT,
      requiredTraceActions: registeredCase.journeyTrace?.requiredActions ?? [],
      requiredActionCounts: registeredCase.journeyTrace?.requiredActionCounts ?? null,
      minimumActionCounts: registeredCase.journeyTrace?.minimumActionCounts ?? null,
      optionalActions: registeredCase.journeyTrace?.optionalActions ?? [],
    };
    if (registeredCase.journeyTrace?.contract && entry.contract && registeredCase.journeyTrace.contract !== entry.contract) {
      throw new Error('journey ' + journeyId + ' contract differs from the registered native journeyTrace binding');
    }
  }
  return { entry, specPath, contract: nativeSelection?.contract ?? entry.contract ?? CONTRACT, nativeSelection };
};

const parseNativeCaseId = (value) => {
  if (typeof value !== 'string' || value.trim() !== value) {
    throw new Error('--native-case must be an exact registered <scenario>/<case> value.');
  }
  const separator = value.indexOf('/');
  if (separator <= 0 || separator !== value.lastIndexOf('/') || separator === value.length - 1) {
    throw new Error('--native-case must be an exact registered <scenario>/<case> value.');
  }
  return { scenarioID: value.slice(0, separator), caseName: value.slice(separator + 1) };
};

const insideRepository = (path) => {
  const local = relative(ROOT, path);
  return local !== '' && local !== '..' && !local.startsWith('..' + sep) && !isAbsolute(local);
};

const isWithin = (parent, candidate) => {
  const local = relative(parent, candidate);
  return local === '' || (local !== '..' && !local.startsWith('..' + sep) && !isAbsolute(local));
};

const registeredNativeJourney = (nativeCaseId) => {
  const { scenarioID, caseName } = parseNativeCaseId(nativeCaseId);
  const scenario = registry.find((candidate) => candidate.id === scenarioID);
  if (!scenario) throw new Error('unknown registered native scenario: ' + scenarioID);
  const registeredCase = scenarioCaseFor(scenario, caseName);
  const binding = registeredCase.journeyTrace;
  if (!binding || binding.kind !== 'native-lifecycle') {
    throw new Error('registered case ' + scenarioID + '/' + caseName + ' has no native-lifecycle journeyTrace binding');
  }
  const adapter = builderFormalAdapterForBinding(binding);
  const targetMode = binding.targetMode ?? 'owned-config';
  if (!['owned-config', 'environment'].includes(targetMode) ||
      (targetMode === 'environment' && registeredCase.expectedIdentity !== undefined)) {
    throw new Error('registered native journey targetMode must truthfully match its explicit target identity binding');
  }
  if (Object.hasOwn(binding, 'formalProofAssertion') &&
      (typeof binding.formalProofAssertion !== 'string' || !binding.formalProofAssertion.trim() ||
       binding.formalProofAssertion.trim() !== binding.formalProofAssertion ||
       !Array.isArray(registeredCase.requiredChecks) ||
       registeredCase.requiredChecks.filter((name) => name === binding.formalProofAssertion).length !== 1)) {
    throw new Error('registered journeyTrace formalProofAssertion must name exactly one required native check');
  }
  if (typeof binding.name !== 'string' || !binding.name.trim() || binding.name.trim() !== binding.name) {
    throw new Error('registered journeyTrace name must be a non-empty exact string');
  }
  if (typeof binding.model !== 'string' || !binding.model.trim()) {
    throw new Error('registered journeyTrace model must be a non-empty string');
  }
  if (typeof binding.contract !== 'string' || !binding.contract.trim() || isAbsolute(binding.contract)) {
    throw new Error('registered journeyTrace contract must be a repository-relative path');
  }
  if (!Array.isArray(binding.requiredActions) || binding.requiredActions.length === 0 ||
      binding.requiredActions.some((action) => typeof action !== 'string' || !action.trim()) ||
      new Set(binding.requiredActions).size !== binding.requiredActions.length) {
    throw new Error('registered journeyTrace requiredActions must be a non-empty unique action list');
  }
  if (!binding.requiredActionCounts || typeof binding.requiredActionCounts !== 'object' || Array.isArray(binding.requiredActionCounts)) {
    throw new Error('registered native-lifecycle journeyTrace requires exact requiredActionCounts');
  }
  if (!binding.minimumActionCounts || typeof binding.minimumActionCounts !== 'object' || Array.isArray(binding.minimumActionCounts)) {
    throw new Error('registered native-lifecycle journeyTrace requires minimumActionCounts for observed proposal events');
  }
  if (!Array.isArray(binding.optionalActions) ||
      binding.optionalActions.some((action) => typeof action !== 'string' || !action.trim()) ||
      new Set(binding.optionalActions).size !== binding.optionalActions.length ||
      binding.optionalActions.some((action) => binding.requiredActions.includes(action))) {
    throw new Error('registered native-lifecycle journeyTrace requires a unique optionalActions list disjoint from requiredActions');
  }
  const exactActions = Object.entries(binding.requiredActionCounts)
    .filter(([, count]) => Number.isInteger(count) && count > 0)
    .map(([action]) => action)
    .sort();
  const minimumActions = Object.entries(binding.minimumActionCounts)
    .filter(([, count]) => Number.isInteger(count) && count > 0)
    .map(([action]) => action)
    .sort();
  if (Object.values(binding.requiredActionCounts).some((count) => !Number.isInteger(count) || count < 0) ||
      Object.values(binding.minimumActionCounts).some((count) => !Number.isInteger(count) || count < 1) ||
      canonicalJson([...exactActions, ...minimumActions].sort()) !== canonicalJson([...binding.requiredActions].sort()) ||
      exactActions.some((action) => minimumActions.includes(action))) {
    throw new Error('registered journeyTrace requiredActions must equal its unique exact-count and minimum-count action keys');
  }

  const contractPath = resolve(ROOT, binding.contract);
  if (!insideRepository(contractPath) || !existsSync(contractPath)) {
    throw new Error('registered journeyTrace contract is missing or escapes the repository: ' + binding.contract);
  }
  const contract = JSON.parse(readFileSync(contractPath, 'utf8'));
  if (contract.sourceProfile !== 'journey') throw new Error('registered native journey contract must declare sourceProfile "journey"');
  const contractAdapter = builderFormalAdapterForContract(contract);
  if (adapter.family !== 'cda-builder-lifecycle' && contractAdapter !== adapter) {
    throw new Error('registered native journey contract must bind the selected static formal adapter');
  }
  if (adapter.family !== 'cda-builder-lifecycle') {
    const formalContract = contract.nativeJourney;
    if (formalContract.nativeCaseKey !== `${scenarioID}/${caseName}` ||
        !Array.isArray(binding.requiredMutationControlNames) ||
        canonicalJson(binding.requiredMutationControlNames) !== canonicalJson(formalContract.requiredMutationControlNames)) {
      throw new Error('registered native journey case and required mutation controls differ from its static contract');
    }
  }
  const model = contract.models?.[binding.model];
  if (!model || !Array.isArray(model.actions) || !model.actions.length ||
      typeof model.module !== 'string' || !model.module.trim() ||
      typeof model.safetyConfig !== 'string' || !model.safetyConfig.trim()) {
    throw new Error('registered journeyTrace model is absent from its contract: ' + binding.model);
  }
  const modelDirectory = 'verification/builder-interactions';
  const modelDirectoryPath = resolve(ROOT, modelDirectory);
  const modelSourcePaths = [model.module, model.safetyConfig].map((path) => {
    if (isAbsolute(path)) throw new Error('registered journeyTrace model paths must be relative to ' + modelDirectory);
    const pathFromRepository = path.startsWith(modelDirectory + '/') ? path : join(modelDirectory, path);
    const absolute = resolve(ROOT, pathFromRepository);
    if (!insideRepository(absolute) || !isWithin(modelDirectoryPath, absolute)) {
      throw new Error('registered journeyTrace model path escapes ' + modelDirectory + ': ' + path);
    }
    return relative(ROOT, absolute).split(sep).join('/');
  });
  const missingModelActions = [...binding.requiredActions, ...binding.optionalActions]
    .filter((action) => !model.actions.includes(action));
  if (missingModelActions.length > 0) {
    throw new Error('registered journeyTrace actions are absent from its model contract: ' + missingModelActions.join(', '));
  }
  const actionCounts = contract.phase1?.requiredActionCountsByTrace?.[binding.name];
  if (!actionCounts || typeof actionCounts !== 'object' || Array.isArray(actionCounts)) {
    throw new Error('registered journeyTrace name has no exact action-count contract: ' + binding.name);
  }
  const nonzeroContractActions = Object.entries(actionCounts)
    .filter(([, count]) => Number.isInteger(count) && count > 0)
    .sort(([left], [right]) => left.localeCompare(right));
  const nonzeroBindingActions = Object.entries(binding.requiredActionCounts)
    .filter(([, count]) => count > 0)
    .sort(([left], [right]) => left.localeCompare(right));
  if (canonicalJson(nonzeroContractActions) !== canonicalJson(nonzeroBindingActions)) {
    throw new Error('registered journeyTrace action counts differ from its contract for ' + binding.name);
  }
  const actionMinimums = contract.phase1?.requiredActionMinimumsByTrace?.[binding.name];
  const effectiveActionMinimums = actionMinimums ?? (adapter.family === 'cda-builder-lifecycle' ? null : {});
  if (!effectiveActionMinimums || typeof effectiveActionMinimums !== 'object' || Array.isArray(effectiveActionMinimums)) {
    throw new Error('registered journeyTrace name has no action-minimum contract: ' + binding.name);
  }
  const contractMinimums = Object.entries(effectiveActionMinimums).sort(([left], [right]) => left.localeCompare(right));
  const bindingMinimums = Object.entries(binding.minimumActionCounts).sort(([left], [right]) => left.localeCompare(right));
  if (canonicalJson(contractMinimums) !== canonicalJson(bindingMinimums)) {
    throw new Error('registered journeyTrace action minimums differ from its contract for ' + binding.name);
  }
  const contractOptionalActions = contract.journeyCase?.optionalActions ?? contract.nativeJourney?.optionalActions ?? [];
  if (!Array.isArray(contractOptionalActions) ||
      canonicalJson([...contractOptionalActions].sort()) !== canonicalJson([...binding.optionalActions].sort())) {
    throw new Error('registered journeyTrace optional actions differ from its contract for ' + binding.name);
  }
  const contractActions = contract.phase1?.requiredActionsByTrace?.[binding.name];
  if (!Array.isArray(contractActions) || canonicalJson([...contractActions].sort()) !==
      canonicalJson([...binding.requiredActions].sort())) {
    throw new Error('registered journeyTrace required actions differ from its contract for ' + binding.name);
  }
  const manifest = JSON.parse(readFileSync(resolve(ROOT, SOURCE_MANIFEST), 'utf8'));
  const pinnedPaths = new Set((manifest.sources ?? []).map((source) => source.path));
  if (!Array.isArray(binding.captureSources) || binding.captureSources.length === 0 ||
      binding.captureSources.some((path) => typeof path !== 'string' || isAbsolute(path))) {
    throw new Error('registered native journeyTrace must pin its capture adapter and native workflow sources');
  }
  const workflowSource = 'scripts/verify-ui/workflows/' + String(scenario.script ?? '');
  const requiredPins = [
    binding.contract,
    ...modelSourcePaths,
    workflowSource,
    ...binding.captureSources,
    'scripts/run-builder-journey.mjs',
    'scripts/verify-builder-traces.mjs',
    'scripts/self-test-builder-traces.mjs',
    'scripts/verify-ui/helpers/builder-journey-trace.mjs',
    'scripts/verify-ui/helpers/builder-formal-adapters.mjs',
    'scripts/verify-ui/registry.mjs',
  ];
  const missingPins = requiredPins.filter((path) => typeof path !== 'string' || !pinnedPaths.has(path));
  if (missingPins.length > 0) {
    throw new Error('registered journeyTrace sources are absent from the Builder source manifest: ' + missingPins.join(', '));
  }
  const missingFiles = requiredPins.filter((path) => !existsSync(resolve(ROOT, path)));
  if (missingFiles.length > 0) {
    throw new Error('registered journeyTrace source file(s) are missing: ' + missingFiles.join(', '));
  }

  const nativeSelection = {
    scenarioID,
    caseName,
    nativeCaseKey: `${scenarioID}/${caseName}`,
    playwrightTest: registeredCase.playwrightTest,
    playwrightGrep: registeredCase.playwrightGrep,
    traceName: binding.name,
    model: binding.model,
    contract: binding.contract,
    formalProofAssertion: binding.formalProofAssertion,
    traceKind: binding.kind,
    captureKind: binding.captureKind,
    captureAdapter: binding.captureAdapter,
    targetMode,
    requiredMutationControlNames: contract.nativeJourney?.requiredMutationControlNames ?? null,
    requestCoverageBasis: contract.nativeJourney?.coverageBasis ?? null,
    requiredTraceActions: binding.requiredActions,
    requiredActionCounts: binding.requiredActionCounts,
    minimumActionCounts: binding.minimumActionCounts,
    optionalActions: binding.optionalActions,
    requiredChecks: registeredCase.requiredChecks,
    expectedIdentity: registeredCase.expectedIdentity ?? null,
  };
  return {
    entry: {
      id: 'native:' + scenarioID + '/' + caseName,
      nativeCase: { scenario: scenarioID, case: caseName },
      spec: registeredCase.playwrightTest,
      contract: binding.contract,
      model: binding.model,
      minBackendCalls: 0,
      nativeLifecycle: true,
      journeyTrace: binding,
    },
    specPath: String(registeredCase.playwrightTest).replace(/^scripts\//, ''),
    contract: binding.contract,
    nativeSelection,
    registeredCase,
  };
};

const runPlaywright = (env, specPath) => {
  const executable = join(SCRIPT_DIR, 'node_modules', '.bin', 'playwright');
  if (!existsSync(executable)) throw new Error('Playwright is not installed under scripts/node_modules');
  const args = ['test', '--config', CONFIG];
  if (specPath) args.push(specPath);
  else args.push('--grep', SPEC_GREP);
  args.push('--reporter=line');
  const result = spawnSync(executable, args, {
    cwd: SCRIPT_DIR,
    env,
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
};

export const bracketSummaryFromOutput = (output) => {
  const value = String(output ?? '').trim();
  for (let start = value.lastIndexOf('{'); start >= 0; start = value.lastIndexOf('{', start - 1)) {
    try {
      const candidate = JSON.parse(value.slice(start));
      if (candidate && typeof candidate === 'object' && typeof candidate.status === 'string' &&
          typeof candidate.scenario === 'string' && typeof candidate.case === 'string' &&
          typeof candidate.summary === 'string') return candidate;
    } catch {}
  }
  return null;
};

const runRegisteredNativeCase = (env, selection, { targetPath = null, evidenceParent = null } = {}) => {
  const args = [
    join(SCRIPT_DIR, 'run-native-verification-bracket.mjs'),
    '--scenario', selection.scenarioID,
    '--case', selection.caseName,
    '--grep', selection.playwrightGrep,
  ];
  if (selection.targetMode === 'environment') {
    if (targetPath) throw new Error('registered target-from-environment formal case cannot also receive --target');
    args.push('--target-from-environment');
    if (evidenceParent) args.push('--evidence-parent', evidenceParent);
  } else if (selection.traceKind === 'native-lifecycle') {
    if (!targetPath) throw new Error('registered native lifecycle case requires --target <owned-target-config>');
    args.push('--target', targetPath);
    if (evidenceParent) args.push('--evidence-parent', evidenceParent);
  } else {
    args.push('--target-from-environment');
  }
  const result = spawnSync(process.execPath, args, {
    cwd: ROOT,
    env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  const summary = bracketSummaryFromOutput(result.stdout);
  return {
    exitStatus: result.status ?? 1,
    summaryEnvelope: summary,
    stdout: result.stdout ?? '',
    stderr: result.stderr ?? '',
  };
};

const validateTrace = (tracePath, reportPath, contractPath, allowPartial) => {
  const args = [
    join(SCRIPT_DIR, 'verify-builder-traces.mjs'),
    '--traces', tracePath,
    '--contract', contractPath,
    '--output', reportPath,
    '--quiet',
  ];
  if (allowPartial) args.push('--allow-partial');
  const result = spawnSync(process.execPath, args, {
    cwd: ROOT,
    env: process.env,
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
};

const inspectNativeLifecycleJourney = (trace, traceRequirements) => {
  const expectedName = traceRequirements.name ?? traceRequirements.traceName;
  if (trace.name !== expectedName) {
    throw new Error('native journey trace name must be ' + expectedName + '; found ' + String(trace.name));
  }
  if (trace.model !== traceRequirements.model) {
    throw new Error('native journey trace model must be ' + traceRequirements.model + '; found ' + String(trace.model));
  }
  if (!trace.initialObservation || typeof trace.initialObservation !== 'object' || Array.isArray(trace.initialObservation)) {
    throw new Error('native journey trace must preserve the actual initial Builder observation');
  }
  if (!Array.isArray(trace.actions) || trace.actions.length === 0) {
    throw new Error('native journey trace must preserve actual transition observations');
  }
  const observedCounts = {};
  for (const action of trace.actions) {
    if (!action || typeof action.label !== 'string' || typeof action.event !== 'string') {
      throw new Error('native journey actions must preserve an exact label and observed event');
    }
    observedCounts[action.label] = (observedCounts[action.label] ?? 0) + 1;
  }
  const requiredActionCounts = traceRequirements.requiredActionCounts ?? {};
  for (const [action, expected] of Object.entries(requiredActionCounts)) {
    const actual = observedCounts[action] ?? 0;
    if (actual !== expected) {
      throw new Error('native journey action count for ' + action + ' must be ' + expected + '; found ' + actual);
    }
  }
  const minimumActionCounts = traceRequirements.minimumActionCounts ?? {};
  for (const [action, minimum] of Object.entries(minimumActionCounts)) {
    const actual = observedCounts[action] ?? 0;
    if (actual < minimum) {
      throw new Error('native journey action count for ' + action + ' must be at least ' + minimum + '; found ' + actual);
    }
  }
  for (const action of Object.keys(observedCounts)) {
    if (!Object.hasOwn(requiredActionCounts, action) && !Object.hasOwn(minimumActionCounts, action) &&
        !(traceRequirements.optionalActions ?? []).includes(action)) {
      throw new Error('native journey contains an action absent from its registered action contract: ' + action);
    }
  }
  const actionLabels = new Set(Object.keys(observedCounts));
  const requiredActions = traceRequirements.requiredActions ?? traceRequirements.requiredTraceActions ?? [];
  const missingActions = requiredActions.filter((action) => !actionLabels.has(action));
  if (missingActions.length > 0) {
    throw new Error('native journey trace is missing registered actions: ' + missingActions.join(', '));
  }
  if (!Array.isArray(trace.unmodeledEvents)) {
    throw new Error('native journey trace must retain its unmodeled-event inventory');
  }
  return {
    name: trace.name,
    model: trace.model,
    actions: trace.actions.map((action) => action.label),
    actionCounts: observedCounts,
    actionCount: trace.actions.length,
    result: 'captured',
    initialObservation: trace.initialObservation,
  };
};

const inspectJourney = (tracePath, minBackendCalls = 10, traceRequirements = {}) => {
  const payload = JSON.parse(readFileSync(resolve(ROOT, tracePath), 'utf8'));
  const trace = payload.traces?.[0];
  if (!trace) throw new Error('journey trace did not contain a trace');
  if (traceRequirements.traceKind === 'native-lifecycle') {
    return inspectNativeLifecycleJourney(trace, traceRequirements);
  }
  if (traceRequirements.name && trace.name !== traceRequirements.name) {
    throw new Error('journey trace name must be ' + traceRequirements.name + '; found ' + String(trace.name));
  }
  const actionLabels = new Set((trace.actions ?? []).map((action) => action.label));
  const missingActions = (traceRequirements.requiredActions ?? []).filter((action) => !actionLabels.has(action));
  if (missingActions.length > 0) {
    throw new Error('journey trace is missing registered native actions: ' + missingActions.join(', '));
  }
  if (!Array.isArray(trace.backendCalls) || trace.backendCalls.length < minBackendCalls) {
    throw new Error('journey trace must contain at least ' + minBackendCalls + ' authoring backend calls; found ' + (trace.backendCalls?.length ?? 0));
  }
  const outcome = trace.outcome;
  if (!outcome || !['ready', 'error'].includes(outcome.status)) {
    throw new Error('journey trace outcome must have status ready or error');
  }
  if (outcome.status === 'ready' && typeof outcome.rowCount !== 'number') {
    throw new Error('ready journey outcome must include a numeric rowCount');
  }
  if (outcome.status === 'error' && (typeof outcome.httpStatus !== 'number' || typeof outcome.errorClass !== 'string')) {
    throw new Error('error journey outcome must include httpStatus and errorClass');
  }
  return {
    name: trace.name,
    model: trace.model,
    actions: trace.actions.map((action) => action.label),
    backendCallCount: trace.backendCalls.length,
    result: outcome.status === 'ready' ? 'pass' : 'product-failure',
    outcome,
  };
};

const nativeEnvironmentTargetProblems = (summary, domainReport) => {
  const problems = [];
  const validation = summary?.targetValidation;
  if (summary?.targetIdentity !== null) {
    problems.push('environment-only bracket must not invent a registered targetIdentity');
  }
  if (validation?.scope !== 'environment-only' || validation?.registryBinding !== 'unbound' ||
      validation?.runtimeDatasetIdentity !== 'not-checked' || validation?.configPath !== null ||
      validation?.project !== null || validation?.generation !== null) {
    problems.push('environment-only bracket must retain its unbound target validation scope');
  }
  const target = domainReport?.target;
  if (!target || target.kind !== 'isolated' || typeof target.project !== 'string' || target.project.length === 0 ||
      typeof target.generation !== 'string' || target.generation.length === 0) {
    problems.push('domain report must identify its actual isolated fixture project and generation');
    return problems;
  }
  const isolation = target.qaIsolation;
  if (!isolation || isolation.project !== target.project || isolation.generation !== target.generation) {
    problems.push('domain report isolated target must match its retained QA isolation project/generation');
  }
  const fixtureOracle = isolation?.fixtureOracle;
  if (!fixtureOracle || fixtureOracle.project !== target.project || fixtureOracle.generation !== target.generation) {
    problems.push('domain report fixture oracle must bind the actual isolated project/generation');
  }
  return problems;
};

const validateNativeBracketSummary = (summary, selection, targetPath, tracePaths = null) => {
  const issues = [];
  const requireValue = (condition, message) => { if (!condition) issues.push(message); };
  requireValue(summary && typeof summary === 'object', 'native bracket summary is missing');
  if (!summary || typeof summary !== 'object') return { status: 'failed', issues };
  requireValue(summary.status === 'passed', 'native bracket status is ' + String(summary.status));
  requireValue(summary.nativeStatus === 'passed', 'native status is ' + String(summary.nativeStatus));
  requireValue(summary.scenario === selection.scenarioID && summary.case === selection.caseName,
    'native bracket identity does not match the registered case');
  requireValue(summary.integrity?.status === 'PASS', 'native bracket integrity status is not PASS');
  if (selection.targetMode === 'environment') {
    requireValue(targetPath === null, 'environment-only native case cannot use an owned target configuration');
  } else {
    requireValue(summary.targetValidation?.registryBinding === 'bound', 'native target is not bound to the owned target configuration');
    if (targetPath) {
      requireValue(resolve(summary.targetValidation?.configPath ?? '') === resolve(targetPath),
        'native bracket target config differs from the selected owned target config');
    }
    const expectedProject = selection.expectedIdentity?.project;
    requireValue(typeof expectedProject === 'string' && summary.targetIdentity?.project === expectedProject,
      'native bracket project identity differs from the registered target');
    requireValue(typeof summary.targetIdentity?.generation === 'string' && summary.targetIdentity.generation.length > 0,
      'native bracket has no validated generation identity');
  }

  const dimensions = summary.integrity?.dimensions ?? {};
  const source = dimensions.source;
  const docs = dimensions.docs;
  const api = dimensions.apiBuildIdentity;
  requireValue(source?.status === 'PASS' && source.before?.sha256 === source.after?.sha256 &&
    source.before?.files === source.after?.files && Array.isArray(source.changedPaths) && source.changedPaths.length === 0,
  'native source integrity did not prove identical before/after source');
  requireValue(docs?.status === 'PASS' && docs.before?.sha256 === docs.after?.sha256 &&
    docs.before?.files === docs.after?.files && Array.isArray(docs.changedPaths) && docs.changedPaths.length === 0,
  'native documentation integrity did not prove identical before/after documents');
  requireValue(api?.status === 'PASS' && typeof api.before === 'string' && api.before === api.after,
    'native API build identity did not remain unchanged');
  requireValue(dimensions.ownedMounts?.status === 'PASS' && dimensions.healthBefore?.status === 'PASS' &&
    dimensions.healthAfter?.status === 'PASS', 'native owned mounts or health checks did not pass');

  const lifecycle = summary.lifecycle ?? {};
  requireValue(lifecycle.reportStatus === 'passed' && lifecycle.reportIdentityMatchesSelection === true,
    'native domain report did not pass the exact selected case identity');
  requireValue(lifecycle.requiredCheckCount > 0 && lifecycle.passedCheckCount === lifecycle.requiredCheckCount &&
    lifecycle.failedCheckNames?.length === 0 && lifecycle.missingCheckNames?.length === 0,
  'native required checks did not all pass');
  requireValue(Array.isArray(selection.requiredChecks) && selection.requiredChecks.length > 0 &&
    lifecycle.requiredCheckCount === selection.requiredChecks.length,
  'native bracket required-check count differs from the registered case contract');
  requireValue(['usability', 'correctness', 'persistence', 'performance'].every((dimension) =>
    lifecycle.dimensions?.[dimension] === 'passed'), 'native lifecycle dimensions are incomplete or failed');

  let domainReport = null;
  let formalTraceProof = null;
  const domainReportPath = summary.evidence?.domainReport;
  if (typeof domainReportPath !== 'string' || !existsSync(domainReportPath)) {
    issues.push('native domain report artifact is missing');
  } else {
    try {
      domainReport = JSON.parse(readFileSync(domainReportPath, 'utf8'));
      requireValue(domainReport.status === 'passed' && domainReport.scenario === selection.scenarioID &&
        domainReport.case === selection.caseName, 'native domain report identity or status is invalid');
      if (selection.targetMode === 'environment') {
        issues.push(...nativeEnvironmentTargetProblems(summary, domainReport));
      } else {
        requireValue(domainReport.target?.project === summary.targetIdentity?.project &&
          domainReport.target?.generation === summary.targetIdentity?.generation,
        'native domain report project/generation differs from the bracket target identity');
      }
      requireValue(domainReport.target?.sourceFingerprint?.sha256 === source?.before?.sha256 &&
        domainReport.target?.sourceFingerprint?.files === source?.before?.files,
      'native domain report source fingerprint differs from the before/after bracket');
      requireValue(domainReport.target?.apiBuildIdentity === api?.before,
        'native domain report API identity differs from the before/after bracket');
      const requiredChecks = Array.isArray(selection.requiredChecks) ? selection.requiredChecks : [];
      requireValue(requiredChecks.length > 0 && new Set(requiredChecks).size === requiredChecks.length,
        'registered native required checks must be non-empty and uniquely named');
      const requiredAssertionProblems = requiredChecks.filter((name) => {
        const matches = (Array.isArray(domainReport.assertions) ? domainReport.assertions : [])
          .filter((assertion) => assertion?.name === name);
        return matches.length !== 1 || matches[0]?.status !== 'passed';
      });
      requireValue(requiredAssertionProblems.length === 0,
        'native domain report is missing exactly one passing assertion for required check(s): ' + requiredAssertionProblems.join(', '));
      requireValue(Array.isArray(domainReport.requiredChecks) && domainReport.requiredChecks.length === lifecycle.requiredCheckCount &&
        canonicalJson(domainReport.requiredChecks) === canonicalJson(selection.requiredChecks) &&
        Array.isArray(domainReport.missingRequiredChecks) && domainReport.missingRequiredChecks.length === 0 &&
        Array.isArray(domainReport.assertions) &&
        domainReport.assertions.every((assertion) => assertion.status === 'passed'),
      'native domain report assertions do not all pass');
      if (selection.traceKind === 'native-lifecycle') {
        const formalAssertionName = selection.formalProofAssertion;
        requireValue(typeof formalAssertionName === 'string' && formalAssertionName.length > 0 &&
          selection.requiredChecks.filter((name) => name === formalAssertionName).length === 1,
        'registered native journeyTrace must name exactly one required formal proof assertion');
        try {
          const receiptEvidence = nativeBuilderJourneyReceiptFromReport(domainReport, {
            binding: {
              name: selection.traceName,
              formalProofAssertion: selection.formalProofAssertion,
            },
            scenarioID: selection.scenarioID,
            caseName: selection.caseName,
            requiredChecks: selection.requiredChecks,
          });
          requireValue(receiptEvidence && typeof receiptEvidence === 'object',
            'native domain report is missing the registered formal proof assertion receipt');
          requireValue(tracePaths && typeof tracePaths === 'object',
            'native runner did not reserve the registered trace, replay, and self-test output paths');

          const boundPath = (field, label) => {
            const evidencePath = receiptEvidence?.[field];
            requireValue(typeof evidencePath === 'string' && evidencePath.length > 0,
              'native formal proof assertion is missing ' + label);
            const actualPath = resolveNativeBuilderJourneyArtifactPath(evidencePath, ROOT);
            const expectedPath = realpathSync(tracePaths[field]);
            requireValue(actualPath === expectedPath,
              'native formal proof assertion ' + label + ' differs from the selected output path');
            return actualPath;
          };
          const tracePath = boundPath('tracePath', 'tracePath');
          const replayReportPath = boundPath('replayReportPath', 'replayReportPath');
          const selfTestReportPath = boundPath('selfTestReportPath', 'selfTestReportPath');
          const traceBytes = readFileSync(tracePath);
          const actualTraceSha256 = createHash('sha256').update(traceBytes).digest('hex');
          const traceDocument = JSON.parse(traceBytes.toString('utf8'));
          const actionLabels = traceDocument.traces?.[0]?.actions?.map((action) => action.label);
          const replayReport = JSON.parse(readFileSync(replayReportPath, 'utf8'));
          const selfTestReceipt = JSON.parse(readFileSync(selfTestReportPath, 'utf8'));
          const nativeContract = JSON.parse(readFileSync(resolve(ROOT, selection.contract), 'utf8'));
          const verifiedProof = finalizeNativeBuilderJourneyReceipt(
            replayReport.builderJourneyTrace,
            selfTestReceipt,
            {
              traceName: selection.traceName,
              nativeCaseKey: selection.nativeCaseKey,
              actionLabels,
              tracePath,
              traceSha256: actualTraceSha256,
              selfTestReportPath,
              contract: nativeContract,
              traceDocument,
            },
          );
          const normalizedEvidence = {
            ...receiptEvidence,
            tracePath: resolveNativeBuilderJourneyArtifactPath(receiptEvidence.tracePath, ROOT),
            replayReportPath: resolveNativeBuilderJourneyArtifactPath(receiptEvidence.replayReportPath, ROOT),
            selfTestReportPath: resolveNativeBuilderJourneyArtifactPath(receiptEvidence.selfTestReportPath, ROOT),
          };
          requireValue(canonicalJson(normalizedEvidence) === canonicalJson(verifiedProof),
            'registered formal proof assertion differs from the retained replay and self-test receipts');
          const requestCoverage = verifiedProof.requestCoverage;
          requireValue(verifiedProof.status === 'pass' && verifiedProof.replayStatus === 'pass' &&
            verifiedProof.selfTestStatus === 'pass' && verifiedProof.traceName === selection.traceName &&
            verifiedProof.nativeCaseKey === selection.nativeCaseKey &&
            verifiedProof.traceSha256 === actualTraceSha256,
          'native domain report did not include a passing source-bound trace replay and mutation self-test');
          requireValue(Number.isInteger(requestCoverage?.observed) &&
            requestCoverage.modeled === requestCoverage.observed &&
            requestCoverage.unmapped === 0 && requestCoverage.duplicates === 0 &&
            Object.keys(verifiedProof.requiredActionCountsByTrace?.mismatches ?? {}).length === 0 &&
            Object.keys(verifiedProof.requiredActionMinimumsByTrace?.mismatches ?? {}).length === 0 &&
            verifiedProof.unmodeledEvents?.unclassified?.total === 0,
          'native formal receipt has incomplete request, action-count, or read-only support coverage');
          requireValue(Array.isArray(verifiedProof.mutationControls) && verifiedProof.mutationControls.length > 0 &&
            verifiedProof.mutationControls.every((control) => typeof control.name === 'string' &&
              control.name.length > 0 && control.status === 'pass'),
          'native formal receipt is missing passing mutation controls');
          formalTraceProof = Object.freeze(verifiedProof);
        } catch (error) {
          issues.push('native formal proof assertion or retained receipt is invalid: ' + String(error?.message ?? error));
        }
      }
    } catch (error) {
      issues.push('native domain report could not be validated: ' + String(error?.message ?? error));
    }
  }
  return {
    status: issues.length === 0 ? 'passed' : 'failed',
    issues,
    summaryPath: summary.evidence?.summary ?? null,
    domainReportPath: domainReportPath ?? null,
    targetIdentity: summary.targetIdentity,
    sourceFingerprint: source?.before ?? null,
    apiBuildIdentity: api?.before ?? null,
    requiredCheckCount: lifecycle.requiredCheckCount ?? null,
    passedCheckCount: lifecycle.passedCheckCount ?? null,
    lifecycleDimensions: lifecycle.dimensions ?? null,
    formalTraceProof,
  };
};

const validateNativeBracketEnvelope = (envelope, selection, targetPath, tracePaths = null) => {
  const issues = [];
  const requireValue = (condition, message) => { if (!condition) issues.push(message); };
  requireValue(envelope && typeof envelope === 'object', 'native bracket output envelope is missing');
  if (!envelope || typeof envelope !== 'object') return { status: 'failed', issues };
  requireValue(envelope.status === 'passed', 'native bracket output status is ' + String(envelope.status));
  requireValue(envelope.scenario === selection.scenarioID && envelope.case === selection.caseName,
    'native bracket output identity does not match the registered case');
  requireValue(typeof envelope.summary === 'string' && envelope.summary.trim().length > 0,
    'native bracket output has no retained full summary path');
  requireValue(typeof envelope.report === 'string' && envelope.report.trim().length > 0,
    'native bracket output has no retained domain report path');
  if (typeof envelope.summary !== 'string' || envelope.summary.trim().length === 0) {
    return { status: 'failed', issues };
  }
  const summaryPath = resolve(ROOT, envelope.summary);
  if (!existsSync(summaryPath)) {
    issues.push('native bracket full summary artifact is missing: ' + envelope.summary);
    return { status: 'failed', issues, summaryPath: envelope.summary };
  }
  let summary;
  try { summary = JSON.parse(readFileSync(summaryPath, 'utf8')); }
  catch (error) {
    issues.push('native bracket full summary artifact is not valid JSON: ' + String(error?.message ?? error));
    return { status: 'failed', issues, summaryPath: envelope.summary };
  }
  if (summary?.status !== envelope.status) {
    issues.push('native bracket output status differs from its retained full summary');
  }
  if (resolve(summary?.evidence?.summary ?? '') !== summaryPath) {
    issues.push('native bracket output summary path differs from the path retained in the full summary');
  }
  if (resolve(summary?.evidence?.domainReport ?? '') !== resolve(ROOT, envelope.report)) {
    issues.push('native bracket output domain-report path differs from the path retained in the full summary');
  }
  const validated = validateNativeBracketSummary(summary, selection, targetPath, tracePaths);
  issues.push(...validated.issues);
  return {
    ...validated,
    status: issues.length === 0 ? 'passed' : 'failed',
    issues,
    summaryPath: envelope.summary,
    envelopeScenario: envelope.scenario,
    envelopeCase: envelope.case,
  };
};

export const validateNativeJourneyTraceBinding = (nativeBracket, selection, tracePath, actionLabels) => {
  const issues = [];
  const proof = nativeBracket?.formalTraceProof;
  if (!proof || typeof proof.tracePath !== 'string') {
    issues.push('native report formal trace path is missing');
  } else {
    try {
      const retainedTracePath = resolveNativeBuilderJourneyArtifactPath(proof.tracePath, ROOT);
      const selectedTracePath = resolveNativeBuilderJourneyArtifactPath(tracePath, ROOT);
      if (retainedTracePath !== selectedTracePath) {
        issues.push('native report formal trace path differs from the selected registered trace output');
      }
    } catch (error) {
      issues.push('native report formal trace path is invalid: ' + String(error?.message ?? error));
    }
  }
  if (proof?.traceName !== selection.traceName) {
    issues.push('native report formal traceName differs from the registered trace binding');
  }
  if (proof?.nativeCaseKey !== selection.nativeCaseKey) {
    issues.push('native report formal nativeCaseKey differs from the selected native bracket case');
  }
  if (!Array.isArray(proof?.actionLabels) || canonicalJson(proof.actionLabels) !== canonicalJson(actionLabels)) {
    issues.push('native report formal action labels differ from the captured trace');
  }
  return { status: issues.length === 0 ? 'passed' : 'failed', issues };
};

const validateNativeTlaReceipt = ({ tracePath, tla, sourceCommit, sourceFingerprint, contractSourceCommit, selection }) => {
  const issues = [];
  const requireValue = (condition, message) => { if (!condition) issues.push(message); };
  let traceDocument = null;
  let actualTraceSha256 = null;
  try {
    const traceBytes = readFileSync(resolve(ROOT, tracePath));
    actualTraceSha256 = createHash('sha256').update(traceBytes).digest('hex');
    traceDocument = JSON.parse(traceBytes.toString('utf8'));
  }
  catch (error) { issues.push('native trace could not be read: ' + String(error?.message ?? error)); }
  requireValue(actualTraceSha256 !== null && tla?.traceSha256 === actualTraceSha256 &&
    tla?.builderJourneyTrace?.traceSha256 === actualTraceSha256,
  'native replay receipt traceSha256 does not bind the exact retained trace bytes');
  requireValue(traceDocument?.sourceCommit === sourceCommit && tla?.sourceCommit === sourceCommit,
    'native trace/TLA source commit differs from the tested commit');
  requireValue(traceDocument?.sourceFingerprint === sourceFingerprint && tla?.sourceFingerprint === sourceFingerprint,
    'native trace/TLA source fingerprint differs from the pinned Builder source set');
  requireValue(traceDocument?.contractSourceCommit === contractSourceCommit && tla?.contractSourceCommit === contractSourceCommit,
    'native trace/TLA contract source commit differs from the selected static contract');
  requireValue(traceDocument?.sourceProfile === 'journey' && tla?.sourceProfile === 'journey',
    'native trace/TLA source profile is not journey');
  requireValue(tla?.traceCount === 1 && tla?.uniqueTraceCount === 1 && tla?.validatedCount === 1 && tla?.invalidCount === 0,
    'TLA receipt does not contain exactly one validated trace');
  requireValue(tla?.coverage?.requiredModels?.missing?.length === 0 &&
    tla?.coverage?.requiredActions?.missing?.length === 0 &&
    Object.keys(tla?.coverage?.requiredActionsByTrace?.missing ?? {}).length === 0,
  'TLA receipt is missing required model or action coverage');
  requireValue(Array.isArray(tla?.coverage?.requiredEvents?.missing) && tla.coverage.requiredEvents.missing.length === 0,
    'TLA receipt is missing required events');
  requireValue(tla?.unmodeledEvents?.unclassified?.total === 0,
    'TLA receipt contains unclassified events');
  const trace = traceDocument?.traces?.[0];
  const receipt = tla?.traces?.[0];
  const journeyReceipt = tla?.builderJourneyTrace;
  requireValue(trace?.name === selection.traceName && receipt?.name === selection.traceName,
    'native trace/TLA journey name differs from the registered binding');
  requireValue(trace?.model === selection.model && receipt?.model === selection.model,
    'native trace/TLA model differs from the registered binding');
  requireValue(receipt?.status === 'pass' && receipt?.actionCount === trace?.actions?.length,
    'TLA receipt did not replay every observed action successfully');
  requireValue(journeyReceipt?.traceName === selection.traceName &&
    journeyReceipt?.nativeCaseKey === selection.nativeCaseKey &&
    journeyReceipt?.replayStatus === 'pass' && journeyReceipt?.status === 'pending-self-test' &&
    Number.isInteger(journeyReceipt?.requestCoverage?.observed) &&
    journeyReceipt.requestCoverage.modeled === journeyReceipt.requestCoverage.observed &&
    journeyReceipt.requestCoverage.unmapped === 0 && journeyReceipt.requestCoverage.duplicates === 0 &&
    Object.keys(journeyReceipt.requiredActionCountsByTrace?.mismatches ?? {}).length === 0 &&
    Object.keys(journeyReceipt.requiredActionMinimumsByTrace?.mismatches ?? {}).length === 0 &&
    journeyReceipt.unmodeledEvents?.unclassified?.total === 0,
  'TLA report typed receipt does not bind the selected native case or complete request/action coverage');
  if (selection.captureAdapter === 'policy-one-disagreement-v1') {
    let adapter;
    try {
      adapter = builderFormalAdapterForBinding({
        captureAdapter: selection.captureAdapter,
        captureKind: selection.captureKind,
        name: selection.traceName,
        model: selection.model,
      });
      const actualCoverage = adapter.requestCoverage(trace);
      requireValue(canonicalJson(journeyReceipt.requestCoverage) === canonicalJson(actualCoverage),
        'Policy One replay request coverage differs from the retained transport ledger');
      requireValue(journeyReceipt.requestCoverageBasis === selection.requestCoverageBasis &&
        typeof journeyReceipt.requestCoverageBasis === 'string' && journeyReceipt.requestCoverageBasis.length > 0,
      'Policy One replay coverage basis differs from the static contract');
      requireValue(canonicalJson(journeyReceipt.requiredMutationControlNames) ===
        canonicalJson(selection.requiredMutationControlNames),
      'Policy One replay control inventory differs from the static contract');
    } catch (error) {
      issues.push('Policy One replay adapter validation failed: ' + String(error?.message ?? error));
    }
  }
  return {
    status: issues.length === 0 ? 'passed' : 'failed',
    issues,
    traceName: trace?.name ?? null,
    model: trace?.model ?? null,
    actionCount: trace?.actions?.length ?? null,
    sourceCommit: tla?.sourceCommit ?? null,
    sourceFingerprint: tla?.sourceFingerprint ?? null,
    traceSha256: actualTraceSha256,
    contractSourceCommit: tla?.contractSourceCommit ?? null,
    tracePath,
  };
};

const requestJSON = async (url, init = {}) => {
  const response = await fetch(url, { ...init, signal: AbortSignal.timeout(30_000) });
  let body;
  try { body = await response.json(); } catch { body = undefined; }
  return { response, body };
};

const ensureDatasetTable = async ({ apiOrigin, project, explorerId }) => {
  const explorersURL = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const listed = await requestJSON(explorersURL);
  if (!listed.response.ok) throw new Error(`cannot list dataset Explorers: HTTP ${listed.response.status}`);
  const existingExplorers = new Set((Array.isArray(listed.body) ? listed.body : []).map((entry) => entry.explorerId));
  let explorer = explorerId ?? (Array.isArray(listed.body) ? listed.body[0]?.explorerId : undefined);
  if (!explorer || !existingExplorers.has(explorer)) {
    explorer = explorer ?? 'loom-journey-' + Date.now().toString(36);
    const created = await requestJSON(explorersURL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: explorer, title: 'Loom journey' }),
    });
    if (!created.response.ok) throw new Error(`cannot create journey Explorer: HTTP ${created.response.status} ${JSON.stringify(created.body).slice(0, 500)}`);
  }

  const base = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
  let builder = await requestJSON(`${base}/builder`);
  if (!builder.response.ok) throw new Error(`cannot read journey Builder: HTTP ${builder.response.status}`);

  if (!Array.isArray(builder.body.workspace?.documents) || builder.body.workspace.documents.length === 0) {
    const rootResource = String(process.env.LOOM_JOURNEY_ROOT_RESOURCE ?? 'Patient').trim() || 'Patient';
    const root = builder.body.catalog.nodes.find((node) => node.resourceType === rootResource && node.rowRootEligible)
      ?? builder.body.catalog.nodes.find((node) => node.rowRootEligible);
    if (!root) throw new Error('dataset catalog has no row-root-eligible resource type');
    const created = await requestJSON(`${base}/commands`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        commandId: `journey-${Date.now().toString(36)}-table`,
        semanticsVersion: builder.body.workspace?.semanticsVersion ?? 10,
        snapshotToken: builder.body.catalog.snapshotToken,
        expectedDraftVersion: builder.body.draftVersion,
        ...(builder.body.draftDigest ? { expectedDraftDigest: builder.body.draftDigest } : {}),
        commands: [{ type: 'CREATE_TABLE', title: `Journey ${root.resourceType}`, rootNodeId: root.nodeId }],
      }),
    });
    if (!created.response.ok) throw new Error(`cannot create journey table: HTTP ${created.response.status} ${JSON.stringify(created.body).slice(0, 500)}`);
    builder = await requestJSON(`${base}/builder`);
    if (!builder.response.ok) throw new Error(`cannot read journey Builder after table creation: HTTP ${builder.response.status}`);
  }

  const document = builder.body.workspace.documents[0];
  if (!Array.isArray(document.columns) || document.columns.length === 0) {
    const root = builder.body.catalog.nodes.find((node) => node.resourceType === document.rootResourceType && node.rowRootEligible);
    const scalar = builder.body.catalog.candidates.find((candidate) => candidate.nodeId === root?.nodeId
      && candidate.fieldPath === 'id'
      && candidate.constructionChoice?.options?.some((option) => option.form === 'VALUE'))
      ?? builder.body.catalog.candidates.find((candidate) => candidate.nodeId === root?.nodeId
        && candidate.constructionChoice?.options?.some((option) => option.form === 'VALUE'));
    if (scalar) {
      const added = await requestJSON(`${base}/commands`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          commandId: `journey-${Date.now().toString(36)}-column`,
          semanticsVersion: builder.body.workspace.semanticsVersion,
          snapshotToken: builder.body.catalog.snapshotToken,
          expectedDraftVersion: builder.body.draftVersion,
          ...(builder.body.draftDigest ? { expectedDraftDigest: builder.body.draftDigest } : {}),
          commands: [{
            type: 'ADD_COLUMN',
            outputId: document.output.id,
            occurrenceId: 'base',
            candidateId: scalar.candidateId,
            projectionMode: 'VALUE',
            initialPresentation: 'TABLE',
            title: scalar.label || scalar.fieldPath,
          }],
        }),
      });
      if (!added.response.ok) throw new Error(`cannot add journey column: HTTP ${added.response.status} ${JSON.stringify(added.body).slice(0, 500)}`);
    }
  }
  return explorer;
};

const main = async () => {
  const options = parseArgs();
  if (options.checkBinding) {
    const selected = registeredNativeJourney(options.nativeCase);
    const sourceManifest = JSON.parse(readFileSync(resolve(ROOT, SOURCE_MANIFEST), 'utf8'));
    const sourceFingerprint = fingerprintPinnedSources(sourceManifest.sources);
    console.log(JSON.stringify({
      status: 'binding-consistent',
      nativeCase: `${selected.nativeSelection.scenarioID}/${selected.nativeSelection.caseName}`,
      traceName: selected.nativeSelection.traceName,
      model: selected.nativeSelection.model,
      contract: selected.nativeSelection.contract,
      requiredActions: selected.nativeSelection.requiredTraceActions,
      requiredActionCounts: selected.nativeSelection.requiredActionCounts,
      minimumActionCounts: selected.nativeSelection.minimumActionCounts,
      sourceCommit: git(['rev-parse', 'HEAD']),
      sourceFingerprint,
      sourceFileCount: sourceManifest.sources.length,
      nativeLifecycleProofStatus: 'not-run',
    }, null, 2));
    return;
  }
  const selectedJourney = options.nativeCase
    ? registeredNativeJourney(options.nativeCase)
    : options.journey ? selectJourney(options.journey, options.manifest ?? MANIFEST) : null;
  const sourceCommit = git(['rev-parse', 'HEAD']);
  const sourceManifest = JSON.parse(readFileSync(resolve(ROOT, SOURCE_MANIFEST), 'utf8'));
  const sourceFingerprint = fingerprintPinnedSources(sourceManifest.sources);
  const actionContractPath = selectedJourney?.entry.contract === null
    ? CONTRACT
    : (selectedJourney?.entry.contract ?? CONTRACT);
  const actionContract = JSON.parse(readFileSync(resolve(ROOT, actionContractPath), 'utf8'));
  if (actionContract.sourceProfile !== 'journey') {
    throw new Error('journey contract ' + actionContractPath + ' must declare sourceProfile "journey"');
  }
  const contractSourceCommit = actionContract.sourceCommit;
  const nativeSelection = selectedJourney?.nativeSelection;
  if (selectedJourney?.entry.nativeLifecycle && selectedJourney.registeredCase.expectedIdentity && !options.target) {
    throw new Error('--native-case requires --target <owned-target-config> for this registered target-bound case');
  }
  if (nativeSelection?.targetMode === 'environment' && options.target) {
    throw new Error('--target cannot be combined with the registered target-from-environment formal case');
  }
  if (nativeSelection && (options.project || options.generation || options.explorer || options.uiUrl || options.apiUrl || options.bootstrap)) {
    throw new Error('journey ' + selectedJourney.entry.id + ' runs through the registered native fixture; configure its target environment instead of passing legacy dataset options');
  }
  let checked;
  let uiUrl;
  let apiUrl;
  let fixtureSource;
  if (nativeSelection) {
    fixtureSource = 'fresh-fixture-owned-by-native-runner';
  } else if (options.project) {
    if (!options.generation) throw new Error('--project requires --generation');
    uiUrl = options.uiUrl ?? process.env.LOOM_FAST_UI_URL ?? 'http://127.0.0.1:3180';
    apiUrl = options.apiUrl ?? process.env.LOOM_FAST_API_URL ?? 'http://127.0.0.1:8180';
    const explorerId = options.bootstrap
      ? await ensureDatasetTable({ apiOrigin: apiUrl, project: options.project, explorerId: options.explorer })
      : options.explorer;
    checked = await assertLoadedDataset({
      apiOrigin: apiUrl,
      project: options.project,
      generation: options.generation,
      explorerId,
    });
    fixtureSource = 'existing-dataset';
  } else {
    const runID = 'j' + Date.now().toString(36).slice(-8) + Math.random().toString(36).slice(2, 5);
    const session = createDevSession({
      ...process.env,
      LOOM_DEV_SOURCE_ROOT: process.env.LOOM_DEV_SOURCE_ROOT ?? ROOT,
      LOOM_DEV_API_PORT: process.env.LOOM_DEV_API_PORT ?? '8180',
      LOOM_DEV_UI_PORT: process.env.LOOM_DEV_UI_PORT ?? '3180',
    });
    const fixture = await createFreshVerificationFixture(session, runID, { populateBootstrap: true });
    checked = await assertLoadedDataset({
      apiOrigin: session.apiUrl,
      project: fixture.target.fixtureProject,
      generation: fixture.target.fixtureGeneration,
      explorerId: fixture.seed.bootstrapExplorerId,
    });
    uiUrl = session.uiUrl;
    apiUrl = session.apiUrl;
    fixtureSource = 'fresh-synthetic-fixture';
  }

  const nativeRunID = nativeSelection?.traceKind === 'native-lifecycle'
    ? Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7)
    : null;
  const nativeArtifactStem = nativeSelection?.traceKind === 'native-lifecycle'
    ? `${nativeSelection.scenarioID}-${nativeSelection.caseName}-${nativeRunID}`
    : null;
  const traceRelative = options.trace ?? (selectedJourney
    ? `.artifacts/builder-journey/${nativeArtifactStem ?? selectedJourney.entry.id}-trace.json`
    : DEFAULT_TRACE);
  const reportRelative = options.report ?? (selectedJourney
    ? `.artifacts/builder-journey/${nativeArtifactStem ?? selectedJourney.entry.id}-report.json`
    : DEFAULT_REPORT);
  const tracePath = resolve(ROOT, traceRelative);
  const reportPath = resolve(ROOT, reportRelative);
  const retainedNativeReportPath = nativeSelection?.traceKind === 'native-lifecycle'
    ? resolve(ROOT, `${reportPath.replace(/\.json$/, '')}-native-report.json`)
    : null;
  let nativeTlaReportPath = null;
  let nativeProofOutputPaths = null;
  let nativeEvidenceParent = null;
  if (nativeSelection?.traceKind === 'native-lifecycle') {
    const artifactRoot = resolve(ROOT, '.artifacts');
    if (!insideRepository(tracePath) || !insideRepository(reportPath) ||
        !isWithin(artifactRoot, tracePath) || !isWithin(artifactRoot, reportPath)) {
      throw new Error('native lifecycle trace and report paths must stay under repository .artifacts during the frozen browser bracket');
    }
    if (!insideRepository(retainedNativeReportPath) || !isWithin(artifactRoot, retainedNativeReportPath)) {
      throw new Error('native lifecycle retained report must use a fresh output under repository .artifacts');
    }
    nativeEvidenceParent = resolveNativeEvidenceParent(options.evidenceParent);
  }
  mkdirSync(dirname(tracePath), { recursive: true });
  mkdirSync(dirname(reportPath), { recursive: true });
  if (nativeSelection?.traceKind === 'native-lifecycle') {
    const binding = builderJourneyTraceBindingFor(nativeSelection.scenarioID, nativeSelection.caseName);
    nativeProofOutputPaths = builderJourneyReportPaths({
      captureKind: binding.captureKind,
      tracePath,
      evidenceDirectory: null,
      artifactStem: binding.name,
      repositoryRoot: ROOT,
      nativeReportPath: reportPath,
      retainedNativeReportPath,
    });
    nativeTlaReportPath = nativeProofOutputPaths.outerTlaReportPath;
    const repositoryRealPath = realpathSync(ROOT);
    const artifactRootRealPath = realpathSync(resolve(ROOT, '.artifacts'));
    const reservedNativeOutputPaths = [
      tracePath,
      nativeProofOutputPaths.replayReportPath,
      nativeProofOutputPaths.selfTestReportPath,
      reportPath,
      nativeTlaReportPath,
      retainedNativeReportPath,
    ];
    if (!isWithin(repositoryRealPath, artifactRootRealPath) || reservedNativeOutputPaths.some(path =>
      !isWithin(artifactRootRealPath, realpathSync(dirname(path))))) {
      throw new Error('native lifecycle trace and retained reports must resolve inside repository .artifacts');
    }
  }

  const env = {
    ...process.env,
    LOOM_TRACE_SOURCE_PROFILE: 'journey',
    LOOM_SOURCE_COMMIT: sourceCommit,
    LOOM_CONTRACT_SOURCE_COMMIT: contractSourceCommit,
    LOOM_SOURCE_FINGERPRINT: sourceFingerprint,
    ...(checked ? {
      LOOM_FAST_SKIP_GLOBAL_SETUP: '1',
      LOOM_FAST_PROJECT: checked.project,
      LOOM_FAST_GENERATION: checked.generation,
      LOOM_FAST_EXPLORER: checked.explorerId,
      LOOM_FAST_UI_URL: uiUrl,
      LOOM_FAST_API_URL: apiUrl,
    } : {}),
    LOOM_JOURNEY_TRACE: tracePath,
    LOOM_JOURNEY_CONTRACT: actionContractPath,
    ...(nativeSelection?.traceKind === 'native-lifecycle' ? {
      LOOM_JOURNEY_NAME: nativeSelection.traceName,
      LOOM_JOURNEY_MODEL: nativeSelection.model,
    } : {}),
    LOOM_JOURNEY_SOURCE_COMMIT: sourceCommit,
    LOOM_JOURNEY_CONTRACT_SOURCE_COMMIT: contractSourceCommit,
    LOOM_JOURNEY_SOURCE_FINGERPRINT: sourceFingerprint,
    LOOM_JOURNEY_MIN_BACKEND_CALLS: String(selectedJourney?.entry.minBackendCalls ?? 10),
  };

  let nativeRun = null;
  let nativeBracket = null;
  let retainedNativeReport = null;
  const playwrightStatus = nativeSelection
    ? (nativeRun = runRegisteredNativeCase(env, nativeSelection, {
      targetPath: options.target ? resolve(ROOT, options.target) : null,
      evidenceParent: nativeEvidenceParent,
    }), nativeRun.exitStatus)
    : runPlaywright(env, selectedJourney?.specPath);
  if (nativeSelection?.traceKind === 'native-lifecycle') {
    nativeBracket = validateNativeBracketEnvelope(nativeRun?.summaryEnvelope, nativeSelection,
      options.target ? resolve(ROOT, options.target) : null,
      {
        tracePath,
        replayReportPath: nativeProofOutputPaths?.replayReportPath,
        selfTestReportPath: nativeProofOutputPaths?.selfTestReportPath,
      });
    if (nativeBracket.summaryPath && nativeBracket.domainReportPath) {
      try {
        retainedNativeReport = retainNativeDomainReportArtifact({
          sourcePath: resolve(ROOT, nativeBracket.domainReportPath),
          destinationPath: retainedNativeReportPath,
          summaryPath: resolve(ROOT, nativeBracket.summaryPath),
        });
      } catch (error) {
        nativeBracket.issues.push('native domain report retention failed: ' + String(error?.message ?? error));
        nativeBracket.status = 'failed';
      }
    }
  }
  let sourceFingerprintAfter = sourceFingerprint;
  let sourceFingerprintAfterError = null;
  if (nativeSelection?.traceKind === 'native-lifecycle') {
    try { sourceFingerprintAfter = fingerprintPinnedSources(sourceManifest.sources); }
    catch (error) { sourceFingerprintAfterError = String(error?.message ?? error); }
  }
  const formalSourceFingerprintStable = sourceFingerprintAfterError === null && sourceFingerprintAfter === sourceFingerprint;
  const fixture = checked ? {
    project: checked.project,
    generation: checked.generation,
    explorerId: checked.explorerId,
    source: fixtureSource,
  } : { source: fixtureSource };

  if (!existsSync(tracePath)) {
    const report = {
      schemaVersion: 1,
      sourceProfile: 'journey',
      sourceCommit,
      contractSourceCommit,
      sourceFingerprint,
      fixture,
      journey: selectedJourney ? { id: selectedJourney.entry.id, spec: selectedJourney.entry.spec } : null,
      ...(nativeSelection ? { nativeCase: nativeSelection } : {}),
      ...(nativeBracket ? { nativeBracket } : {}),
      ...(retainedNativeReport ? { retainedNativeReport } : {}),
      result: 'harness-failure',
      playwrightStatus,
      ...(nativeSelection ? { nativeRunnerStatus: playwrightStatus } : {}),
      error: 'journey did not write a trace',
      tla: null,
    };
    writeFileSync(reportPath, JSON.stringify(report, null, 2) + '\n');
    console.error('JOURNEY_HARNESS_FAILURE ' + JSON.stringify({
      project: checked?.project ?? null,
      journey: selectedJourney?.entry.id ?? null,
      nativeCase: nativeSelection ? `${nativeSelection.scenarioID}/${nativeSelection.caseName}` : null,
      playwrightStatus,
      report: reportRelative,
    }));
    process.exitCode = playwrightStatus || 1;
    return;
  }

  const journey = inspectJourney(
    tracePath,
    selectedJourney?.entry.minBackendCalls ?? 10,
    selectedJourney?.nativeSelection ?? {},
  );
  if (nativeSelection?.traceKind === 'native-lifecycle') {
    const proofBinding = validateNativeJourneyTraceBinding(nativeBracket, nativeSelection, tracePath, journey.actions);
    nativeBracket?.issues.push(...proofBinding.issues);
    if (nativeBracket && nativeBracket.issues.length > 0) nativeBracket.status = 'failed';
  }
  const tlaReportPath = nativeTlaReportPath ?? reportPath.replace(/\.json$/, '.tla.json');
  const hasDeclaredModel = Boolean(selectedJourney?.entry.model);
  const unmodeled = Boolean(selectedJourney && selectedJourney.entry.contract === null && !hasDeclaredModel);
  const contractPath = unmodeled
    ? null
    : (selectedJourney?.entry.contract ?? CONTRACT);
  const allowPartial = Boolean(selectedJourney && selectedJourney.entry.contract === null && hasDeclaredModel);
  const validationStatus = unmodeled ? 0 : validateTrace(tracePath, tlaReportPath, contractPath, allowPartial);
  const tla = !unmodeled && existsSync(tlaReportPath) ? JSON.parse(readFileSync(tlaReportPath, 'utf8')) : null;
  let tlaStatus = unmodeled ? 'not-modeled'
    : allowPartial ? (validationStatus === 0 ? 'partial' : 'fail')
      : (validationStatus === 0 ? 'pass' : 'fail');
  const formalTla = nativeSelection?.traceKind === 'native-lifecycle'
    ? validateNativeTlaReceipt({ tracePath, tla, sourceCommit, sourceFingerprint, contractSourceCommit, selection: nativeSelection })
    : null;
  if (formalTla?.status !== undefined && formalTla.status !== 'passed') tlaStatus = 'fail';
  const nativeBracketFailed = nativeSelection?.traceKind === 'native-lifecycle' && nativeBracket?.status !== 'passed';
  const nativeFormalProofFailed = nativeSelection?.traceKind === 'native-lifecycle' &&
    (validationStatus !== 0 || formalTla?.status !== 'passed' || !formalSourceFingerprintStable);
  const result = nativeBracketFailed
    ? 'native-runner-failure'
    : nativeFormalProofFailed
      ? 'formal-proof-failure'
      : journey.result === 'product-failure'
    ? 'product-failure'
    : nativeSelection && playwrightStatus !== 0
      ? 'native-runner-failure'
      : playwrightStatus !== 0 ? 'product-failure' : 'pass';
  const report = {
    schemaVersion: 1,
    sourceProfile: 'journey',
    sourceCommit,
    contractSourceCommit,
    sourceFingerprint,
    fixture,
    journey,
    ...(nativeSelection ? { nativeCase: nativeSelection } : {}),
    ...(nativeBracket ? { nativeBracket } : {}),
    ...(retainedNativeReport ? { retainedNativeReport } : {}),
    result,
    playwrightStatus,
    ...(nativeSelection ? { nativeRunnerStatus: playwrightStatus } : {}),
    tlaStatus,
    tla,
    ...(formalTla ? { formalTla } : {}),
    ...(nativeSelection?.traceKind === 'native-lifecycle' ? {
      formalSourceFingerprintAfter: sourceFingerprintAfter,
      formalSourceFingerprintAfterError: sourceFingerprintAfterError,
      formalSourceFingerprintStable,
    } : {}),
  };
  writeFileSync(reportPath, JSON.stringify(report, null, 2) + '\n');

  if (result === 'product-failure') {
    console.log('JOURNEY_PRODUCT_FAILURE ' + JSON.stringify({
      project: checked?.project ?? null,
      journey: selectedJourney?.entry.id ?? journey.name,
      nativeCase: nativeSelection ? `${nativeSelection.scenarioID}/${nativeSelection.caseName}` : null,
      actions: journey.actions,
      backendCallCount: journey.backendCallCount,
      outcome: journey.outcome,
      trace: traceRelative,
      report: reportRelative,
      tlaStatus: report.tlaStatus,
    }));
    process.exitCode = 1;
    return;
  }

  if (result === 'native-runner-failure') {
    console.error('JOURNEY_NATIVE_RUNNER_FAILURE ' + JSON.stringify({
      journey: selectedJourney.entry.id,
      nativeCase: `${nativeSelection.scenarioID}/${nativeSelection.caseName}`,
      nativeRunnerStatus: playwrightStatus,
      trace: traceRelative,
      report: reportRelative,
      tlaStatus: report.tlaStatus,
    }));
    process.exitCode = playwrightStatus || 1;
    return;
  }

  if (result === 'formal-proof-failure') {
    console.error('JOURNEY_FORMAL_PROOF_FAILURE ' + JSON.stringify({
      journey: selectedJourney.entry.id,
      nativeCase: `${nativeSelection.scenarioID}/${nativeSelection.caseName}`,
      nativeBracket: nativeBracket?.status ?? null,
      nativeBracketIssues: nativeBracket?.issues ?? [],
      formalTla: formalTla?.status ?? null,
      formalTlaIssues: formalTla?.issues ?? [],
      trace: traceRelative,
      tlaReport: reportPath.replace(/\.json$/, '.tla.json'),
      report: reportRelative,
    }));
    process.exitCode = 1;
    return;
  }

  if (validationStatus !== 0) {
    process.exitCode = validationStatus;
    return;
  }

  console.log('JOURNEY_OK ' + JSON.stringify({
    project: checked?.project ?? null,
    journey: selectedJourney?.entry.id ?? journey.name,
    nativeCase: nativeSelection ? `${nativeSelection.scenarioID}/${nativeSelection.caseName}` : null,
    actions: journey.actions,
    ...(nativeSelection?.traceKind === 'native-lifecycle' ? {
      actionCounts: journey.actionCounts,
      actionCount: journey.actionCount,
      nativeBracket: nativeBracket.status,
      nativeBracketSummary: nativeBracket.summaryPath,
      formalTla: formalTla.status,
    } : { backendCallCount: journey.backendCallCount, outcome: journey.outcome }),
    tlaStatus: report.tlaStatus,
    trace: traceRelative,
    report: reportRelative,
  }));
};

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack ?? error.message : String(error));
    process.exitCode = 1;
  });
}

export function resolveNativeEvidenceParent(configuredPath) {
  if (configuredPath === undefined || configuredPath === null || configuredPath === '') return null;
  const evidenceParent = resolve(ROOT, configuredPath);
  if (insideRepository(evidenceParent)) {
    throw new Error('native lifecycle bracket evidence must stay outside the watched repository root');
  }
  return evidenceParent;
}

export function retainNativeDomainReportArtifact({
  sourcePath,
  destinationPath,
  summaryPath,
  repositoryRoot = ROOT,
  artifactRoot = resolve(repositoryRoot, '.artifacts'),
}) {
  const repository = realpathSync(repositoryRoot);
  const artifacts = realpathSync(artifactRoot);
  if (!isWithin(repository, artifacts)) {
    throw new Error('repository .artifacts must resolve inside the repository root');
  }
  const bracketSummary = realpathSync(summaryPath);
  const bracketRun = dirname(bracketSummary);
  const source = realpathSync(sourcePath);
  const destinationDirectory = realpathSync(dirname(destinationPath));
  if (isWithin(repository, source)) {
    throw new Error('native domain report source must remain in external bracket evidence');
  }
  if (!isWithin(bracketRun, source)) {
    throw new Error('native domain report source must come from the exact bracket run directory');
  }
  if (!isWithin(artifacts, destinationDirectory)) {
    throw new Error('retained native domain report must stay under repository .artifacts');
  }
  const bytes = readFileSync(source);
  writeFileSync(destinationPath, bytes, { flag: 'wx' });
  const retainedBytes = readFileSync(destinationPath);
  if (!retainedBytes.equals(bytes)) {
    throw new Error('retained native domain report differs from the exact bracket report bytes');
  }
  return {
    path: relative(repository, join(destinationDirectory, basename(destinationPath))),
    sha256: createHash('sha256').update(bytes).digest('hex'),
  };
}

export {
  nativeEnvironmentTargetProblems,
  validateNativeBracketEnvelope,
  validateNativeBracketSummary,
  validateNativeTlaReceipt,
};
