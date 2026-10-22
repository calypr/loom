#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createDevSession, createFreshVerificationFixture } from './loom-dev.mjs';
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
  const options = { trace: null, report: null, journey: null, manifest: null, project: null, generation: null, explorer: null, uiUrl: null, apiUrl: null, bootstrap: false };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--trace') options.trace = args[++index];
    else if (arg === '--report') options.report = args[++index];
    else if (arg === '--journey') options.journey = args[++index];
    else if (arg === '--manifest') options.manifest = args[++index];
    else if (arg === '--project') options.project = args[++index];
    else if (arg === '--generation') options.generation = args[++index];
    else if (arg === '--explorer') options.explorer = args[++index];
    else if (arg === '--ui-url') options.uiUrl = args[++index];
    else if (arg === '--api-url') options.apiUrl = args[++index];
    else if (arg === '--bootstrap') options.bootstrap = true;
    else if (arg === '--help' || arg === '-h') {
      console.log('usage: node scripts/run-builder-journey.mjs [--manifest <manifest.json>] [--journey <id>] [--trace <trace.json>] [--report <report.json>] [--project <dataset> --generation <generation> [--explorer <id>] [--ui-url <url>] [--api-url <url>] [--bootstrap]]');
      process.exit(0);
    } else {
      throw new Error('unknown argument ' + arg + '; use --help for usage');
    }
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
      playwrightTest: registeredCase.playwrightTest,
      playwrightGrep: registeredCase.playwrightGrep,
      traceName: registeredCase.journeyTrace?.name ?? null,
      requiredTraceActions: registeredCase.journeyTrace?.requiredActions ?? [],
    };
  }
  return { entry, specPath, contract: entry.contract ?? CONTRACT, nativeSelection };
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

const runRegisteredNativeCase = (env, selection) => {
  const result = spawnSync(process.execPath, [
    join(SCRIPT_DIR, 'run-native-verification-bracket.mjs'),
    '--scenario', selection.scenarioID,
    '--case', selection.caseName,
    '--target-from-environment',
    '--grep', selection.playwrightGrep,
  ], {
    cwd: ROOT,
    env,
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
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

const inspectJourney = (tracePath, minBackendCalls = 10, traceRequirements = {}) => {
  const payload = JSON.parse(readFileSync(resolve(ROOT, tracePath), 'utf8'));
  const trace = payload.traces?.[0];
  if (!trace) throw new Error('journey trace did not contain a trace');
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
  const selectedJourney = options.journey ? selectJourney(options.journey, options.manifest ?? MANIFEST) : null;
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

  const traceRelative = options.trace ?? (selectedJourney
    ? `.artifacts/builder-journey/${selectedJourney.entry.id}-trace.json`
    : DEFAULT_TRACE);
  const reportRelative = options.report ?? (selectedJourney
    ? `.artifacts/builder-journey/${selectedJourney.entry.id}-report.json`
    : DEFAULT_REPORT);
  const tracePath = resolve(ROOT, traceRelative);
  const reportPath = resolve(ROOT, reportRelative);
  mkdirSync(dirname(tracePath), { recursive: true });
  mkdirSync(dirname(reportPath), { recursive: true });

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
    LOOM_JOURNEY_SOURCE_COMMIT: sourceCommit,
    LOOM_JOURNEY_CONTRACT_SOURCE_COMMIT: contractSourceCommit,
    LOOM_JOURNEY_SOURCE_FINGERPRINT: sourceFingerprint,
    LOOM_JOURNEY_MIN_BACKEND_CALLS: String(selectedJourney?.entry.minBackendCalls ?? 10),
  };

  const playwrightStatus = nativeSelection
    ? runRegisteredNativeCase(env, nativeSelection)
    : runPlaywright(env, selectedJourney?.specPath);
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
  const tlaReportPath = reportPath.replace(/\.json$/, '.tla.json');
  const hasDeclaredModel = Boolean(selectedJourney?.entry.model);
  const unmodeled = Boolean(selectedJourney && selectedJourney.entry.contract === null && !hasDeclaredModel);
  const contractPath = unmodeled
    ? null
    : (selectedJourney?.entry.contract ?? CONTRACT);
  const allowPartial = Boolean(selectedJourney && selectedJourney.entry.contract === null && hasDeclaredModel);
  const validationStatus = unmodeled ? 0 : validateTrace(tracePath, tlaReportPath, contractPath, allowPartial);
  const tla = !unmodeled && existsSync(tlaReportPath) ? JSON.parse(readFileSync(tlaReportPath, 'utf8')) : null;
  const tlaStatus = unmodeled ? 'not-modeled'
    : allowPartial ? (validationStatus === 0 ? 'partial' : 'fail')
      : (validationStatus === 0 ? 'pass' : 'fail');
  const result = journey.result === 'product-failure'
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
    result,
    playwrightStatus,
    ...(nativeSelection ? { nativeRunnerStatus: playwrightStatus } : {}),
    tlaStatus,
    tla,
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

  if (validationStatus !== 0) {
    process.exitCode = validationStatus;
    return;
  }

  console.log('JOURNEY_OK ' + JSON.stringify({
    project: checked?.project ?? null,
    journey: selectedJourney?.entry.id ?? journey.name,
    nativeCase: nativeSelection ? `${nativeSelection.scenarioID}/${nativeSelection.caseName}` : null,
    actions: journey.actions,
    backendCallCount: journey.backendCallCount,
    outcome: journey.outcome,
    tlaStatus: report.tlaStatus,
    trace: traceRelative,
    report: reportRelative,
  }));
};

main().catch((error) => {
  console.error(error instanceof Error ? error.stack ?? error.message : String(error));
  process.exitCode = 1;
});
