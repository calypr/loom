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
const MANIFEST_RELATIVE = 'verification/builder-interactions/builder-preview-contract.json';
const MODEL_DIR = 'verification/builder-interactions';
const MAP_RELATIVE = 'verification/builder-interactions/affordance-model-map.json';
const INVENTORY_RELATIVE = 'docs/ui-contract-inventory/ui-supported-affordances.json';
const REPORT_RELATIVE = 'verification/builder-interactions/report.json';
const REPORT_MD_RELATIVE = 'verification/builder-interactions/report.md';
const TLC_VERSION = '1.7.4';
const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';
const TLC_URL = 'https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar';
const FORMAL_TEST = 'src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx';
const GO_LIFECYCLE_TESTS = [
  'TestBuilderFullUIRegisteredGroupFilterDeriveFixtureCompiles',
  'TestBuilderFullUIScalarFilterAndDeriveRequestsReplayThroughRealService',
  'TestBuilderFullUIRowDefinitionProposalDispatchUsesRealService',
  'TestBuilderFullUIEmptyRootCreationDispatchUsesRealService',
  'TestBuilderFullUIGroupFilterDeriveAggregateEditReplaysLifecycle',
];
const GO_SERVER_TESTS = [
  'TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly',
  'TestConstructionProposalSemanticGroupChoiceReconcilesCompiledOutputMetadata',
  'TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer',
];
const MODEL_RUNS = [
  { name: 'builder-operations', module: 'BuilderOperations', config: 'BuilderOperations.cfg' },
  { name: 'builder-preview-safety', module: 'BuilderPreview', config: 'BuilderPreviewSafety.cfg' },
  { name: 'builder-workspace-lifecycle', module: 'WorkspaceLifecycle', config: 'WorkspaceLifecycle.cfg' },
  { name: 'builder-row-definition-lifecycle', module: 'RowDefinitionLifecycle', config: 'RowDefinitionLifecycle.cfg' },
  { name: 'builder-draft-restore', module: 'DraftRestore', config: 'DraftRestore.cfg' },
  { name: 'builder-root-change', module: 'RootChange', config: 'RootChange.cfg' },
  { name: 'builder-column-presentation', module: 'ColumnPresentation', config: 'ColumnPresentation.cfg' },
  { name: 'builder-population', module: 'Population', config: 'Population.cfg' },
  { name: 'builder-interpretation', module: 'Interpretation', config: 'Interpretation.cfg' },
  { name: 'builder-supporting-surfaces', module: 'SupportingSurfaces', config: 'SupportingSurfaces.cfg' },
  { name: 'builder-lifecycle-entry', module: 'LifecycleEntry', config: 'LifecycleEntry.cfg' },
  { name: 'builder-reconcile-lifecycle', module: 'ReconcileLifecycle', config: 'ReconcileLifecycle.cfg' },
];

const FINDINGS = [];

// Retired on 2026-10-18 after the compiled and real-AQL presence audits found
// no valid UI-offered field reaching the refusal condition
// (missing category value AND untracked presence). Materialized outputs report
// present=true, so they collapse absent to null instead of refusing discovery.
// That missing/null distinction is a separate, slower correctness follow-up.
const RETIRED_FINDINGS = [
  {
    id: 'pivot-missing-category-unsupported',
    title: 'The mounted Pivot editor offered category fields whose absent values the backend refused to discover.',
    reason: 'Not reproduced in the current compiler. Source fields with absent values are presence-tracked; materialized Derive, Filter, Expand, Group, and Unpivot outputs report present=true. No audited valid path produced missing && !PresenceTracked.',
    evidence: [
      'internal/dataframe/compiler/pivot_presence_audit_test.go',
      'internal/dataframe/compiler/pivot_presence_arango_audit_test.go',
      'internal/explorer/lifecycle/construction_category_discovery.go:426-437',
    ],
  },
];

const RESOLVED_FINDINGS = [
  {
    id: 'apply-ack-targets-current-owner-success',
    title: 'Successful Apply acknowledgment now targets the owner that began Apply.',
    regressionTest: 'regression: old Apply success after replacement must not cancel the replacement owner',
    evidence: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts:beginApply/finishApply',
    ],
  },
  {
    id: 'apply-ack-targets-current-owner-failure',
    title: 'Failed Apply acknowledgment now targets the owner that began Apply.',
    regressionTest: 'regression: old Apply failure after replacement must not mark the replacement owner as an error',
    evidence: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts:beginApply/finishApply',
    ],
  },
];

function fail(message) {
  throw new Error(message);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function bytes(relativePath, root = ROOT) {
  return readFileSync(resolve(root, relativePath));
}

function readJson(relativePath, root = ROOT) {
  return JSON.parse(bytes(relativePath, root).toString('utf8'));
}

function canonicalJson(value) {
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  if (value && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

function requirePinnedSources() {
  const manifest = readJson(MANIFEST_RELATIVE);
  if (manifest.schemaVersion !== 1 || !Array.isArray(manifest.sources) || manifest.sources.length === 0) {
    fail('builder-preview-contract.json has an unsupported shape');
  }
  const drift = manifest.sources.map((source) => {
    const actual = sha256(bytes(source.path));
    return { ...source, actual };
  }).filter((source) => source.sha256 !== source.actual);
  if (drift.length > 0) {
    fail(
      'modeled Builder sources drifted. Review the source-to-model mapping, update the TLA+ model and implementation checks, then deliberately update builder-preview-contract.json:\n' +
      drift.map((source) =>
        '  ' + source.path + '\n    expected ' + source.sha256 + '\n    found    ' + source.actual,
      ).join('\n'),
    );
  }
  return manifest;
}

function verifyJava() {
  const result = spawnSync('java', ['-version'], { encoding: 'utf8' });
  if (result.error) fail('Java is required to run TLC ' + TLC_VERSION + ': ' + result.error.message);
  if (result.status !== 0) fail('Java is required to run TLC ' + TLC_VERSION + ': ' + (result.stderr || result.stdout || '').trim());
  return (result.stderr || result.stdout || '').trim().split('\n')[0];
}

function verifyJar(path) {
  const absolute = realpathSync(path);
  if (!existsSync(absolute)) fail('TLC jar does not exist at ' + absolute);
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

function runCommand(label, command, args, cwd, environment = process.env) {
  console.log('\n' + label);
  console.log(cwd + '$ ' + [command, ...args].join(' '));
  const result = spawnSync(command, args, { cwd, env: environment, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  return {
    status: result.status,
    error: result.error,
    output: (result.stdout || '') + (result.stderr || ''),
  };
}

function parseTlc(text) {
  const statesMatch = text.match(/([0-9,]+) states generated, ([0-9,]+) distinct states found/);
  const depthMatch = text.match(/The depth of the complete state graph search is ([0-9]+)/);
  const violationIndexes = [];
  const regex = /Error: Invariant ([A-Za-z0-9_]+) is violated\./g;
  let match;
  while ((match = regex.exec(text)) !== null) {
    violationIndexes.push({ invariant: match[1], index: match.index });
  }
  const violations = [];
  const seen = new Set();
  for (let index = 0; index < violationIndexes.length; index += 1) {
    const current = violationIndexes[index];
    const end = index + 1 < violationIndexes.length ? violationIndexes[index + 1].index : text.length;
    const segment = text.slice(current.index, end);
    const actions = [...segment.matchAll(/State \d+: <([A-Za-z0-9_]+)/g)].map((entry) => entry[1]);
    const lastActions = [...segment.matchAll(/lastAction = "([^"]+)"/g)].map((entry) => entry[1]);
    const signature = current.invariant + '\u0000' + actions.join('>') + '\u0000' + lastActions.join('>');
    if (seen.has(signature)) continue;
    seen.add(signature);
    violations.push({ invariant: current.invariant, actions, lastActions });
  }
  return {
    stateCount: statesMatch ? Number(statesMatch[1].replaceAll(',', '')) : null,
    distinctStateCount: statesMatch ? Number(statesMatch[2].replaceAll(',', '')) : null,
    depth: depthMatch ? Number(depthMatch[1]) : null,
    passed: text.includes('Model checking completed. No error has been found.'),
    violations,
    output: text,
  };
}

function runTlc(jarPath, modelModule, configFile, options = {}) {
  const runDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-formal-tlc-'));
  const modelDirectory = join(runDirectory, 'model');
  const metadataDirectory = join(runDirectory, 'states');
  mkdirSync(modelDirectory);
  mkdirSync(metadataDirectory);
  copyFileSync(resolve(ROOT, MODEL_DIR, modelModule + '.tla'), join(modelDirectory, modelModule + '.tla'));
  copyFileSync(resolve(ROOT, MODEL_DIR, configFile), join(modelDirectory, configFile));
  const args = [
    '-XX:+UseParallelGC', '-cp', jarPath, 'tlc2.TLC',
    '-workers', options.workers ?? process.env.LOOM_TLC_WORKERS ?? '4',
    ...(options.continue ? ['-continue'] : []),
    '-metadir', metadataDirectory,
    '-config', configFile,
    modelModule + '.tla',
  ];
  const result = runCommand('TLC ' + configFile, 'java', args, modelDirectory);
  const parsed = parseTlc(result.output);
  return { ...parsed, status: result.status, runDirectory, configFile, modelModule };
}

function vitestBinary() {
  const candidates = [
    'ui/node_modules/vitest/vitest.mjs',
    'ui/packages/loom-ui/node_modules/vitest/vitest.mjs',
  ];
  for (const relative of candidates) {
    const absolute = resolve(ROOT, relative);
    if (existsSync(absolute)) return absolute;
  }
  fail('UI dependencies are missing. Install the existing workspace dependencies with npm install in ui/.');
}

function traceContractCounts() {
  const contract = readJson(MODEL_DIR + '/builder-trace-contract.json');
  return {
    traceCount: contract.phase1?.expectedTraceCount,
    scheduleCount: contract.phase1?.expectedScheduleCount,
  };
}

function runFormalSchedules() {
  const expected = traceContractCounts().traceCount;
  const reportDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-formal-vitest-'));
  const reportPath = join(reportDirectory, 'vitest.json');
  const tracePath = join(reportDirectory, 'traces.json');
  const result = runCommand(
    'Real controller schedules and retained findings',
    process.execPath,
    [vitestBinary(), 'run', '--reporter=json', '--outputFile=' + reportPath, FORMAL_TEST],
    resolve(ROOT, 'ui/packages/loom-ui'),
    { ...process.env, LOOM_BUILDER_TRACE_OUTPUT: tracePath },
  );
  let report;
  try {
    report = JSON.parse(readFileSync(reportPath, 'utf8'));
  } catch {
    fail('Vitest did not produce its JSON report at ' + reportPath);
  }
  const assertions = report.testResults.flatMap((suite) => suite.assertionResults);
  const regressionTests = assertions
    .filter((assertion) => assertion.fullName.includes('regression:'))
    .map((assertion) => assertion.fullName);
  const passed = result.status === 0 &&
    report.success === true &&
    report.numTotalTests === expected &&
    report.numPassedTests === expected &&
    report.numFailedTests === 0 &&
    report.numPendingTests === 0 &&
    report.numTodoTests === 0;
  if (!passed) {
    fail('the real-controller schedule suite failed. Expected ' + expected + '/' + expected + ' passing tests including retained expected-failure cases. See ' + reportPath);
  }
  if (!existsSync(tracePath)) fail('the formal suite did not write its trace corpus to ' + tracePath);
  return {
    name: 'real-controller-schedules',
    status: 'pass',
    tests: report.numTotalTests,
    regressionTests,
    reportPath,
    tracePath,
  };
}

function runTraceValidation(tracePath) {
  const expected = traceContractCounts().traceCount;
  const reportPath = tracePath.replace(/\.json$/, '-report.json');
  const result = runCommand(
    'TLA trace validation',
    process.execPath,
    ['scripts/verify-builder-traces.mjs', '--traces', tracePath, '--output', reportPath, '--quiet'],
    ROOT,
  );
  if (!existsSync(reportPath)) fail('trace validation did not produce a report at ' + reportPath);
  let report;
  try {
    report = JSON.parse(readFileSync(reportPath, 'utf8'));
  } catch {
    fail('trace validation report is not valid JSON: ' + reportPath);
  }
  if (result.status !== 0 || report.invalidCount !== 0) {
    fail('trace validation failed; inspect ' + reportPath);
  }
  if (report.traceCount !== expected || report.validatedCount !== expected) {
    fail('trace validation did not cover all ' + expected + ' formal traces: ' + JSON.stringify({
      traceCount: report.traceCount,
      validatedCount: report.validatedCount,
    }));
  }
  return {
    name: 'builder-trace-validation',
    status: 'pass',
    traceCount: report.traceCount,
    uniqueTraceCount: report.uniqueTraceCount,
    validatedCount: report.validatedCount,
    invalidCount: report.invalidCount,
    modelCounts: report.modelCounts,
    coverage: report.coverage,
    unmodeledEvents: report.unmodeledEvents,
    reportPath,
  };
}

function runTraceValidatorSelfTest(tracePath) {
  const result = runCommand(
    'TLA trace validator mutation self-test',
    process.execPath,
    [
      'scripts/self-test-builder-traces.mjs',
      '--traces',
      tracePath,
      '--contract',
      MODEL_DIR + '/builder-trace-contract.json',
    ],
    ROOT,
  );
  return {
    name: 'builder-trace-validator-self-test',
    status: result.status === 0 ? 'pass' : 'fail',
    outputTail: result.output.split('\n').slice(-30).join('\n'),
  };
}

function goTest(target, tests) {
  const pattern = '^(' + tests.map((name) => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') + ')$';
  const result = runCommand('Go ' + target, 'go', ['test', target, '-run', pattern, '-count=1'], ROOT);
  return {
    name: 'go-' + target.replace(/^\.\//, '').replaceAll('/', '-'),
    target,
    tests,
    status: result.status === 0 ? 'pass' : 'fail',
    outputTail: result.output.split('\n').slice(-20).join('\n'),
  };
}

function runInventorySourceCheck() {
  const result = runCommand(
    'UI contract inventory source check',
    'python3',
    ['scripts/verify_ui_contract_inventory.py', '--source-only'],
    ROOT,
  );
  return {
    name: 'ui-contract-inventory-source',
    status: result.status === 0 ? 'pass' : 'fail',
    outputTail: result.output.split('\n').slice(-12).join('\n'),
  };
}

function validateCoverageMap(inventory, map, modelText) {
  if (map.sourceCommit !== inventory.sourceCommit) {
    fail('affordance-model-map.json sourceCommit does not match the inventory');
  }
  const inventoryIds = new Set(inventory.affordances.map((entry) => entry.id));
  const mapIds = new Set(map.affordances.map((entry) => entry.id));
  const missing = [...inventoryIds].filter((id) => !mapIds.has(id));
  const extra = [...mapIds].filter((id) => !inventoryIds.has(id));
  if (missing.length > 0) fail('affordance-model-map.json is missing inventory IDs: ' + missing.join(', '));
  if (extra.length > 0) fail('affordance-model-map.json has unknown IDs: ' + extra.join(', '));
  const missingActions = new Set();
  for (const entry of map.affordances) {
    for (const action of entry.modelActions ?? []) {
      const symbol = String(action).split('(')[0];
      if (symbol && !new RegExp('\\b' + symbol + '\\b').test(modelText)) missingActions.add(symbol);
    }
  }
  if (missingActions.size > 0) fail('coverage map refers to missing model actions: ' + [...missingActions].join(', '));
}

function buildFindings(tlcRuns) {
  const findings = [];
  for (const finding of FINDINGS) {
    const run = tlcRuns.find((entry) => entry.violations.some((violation) => violation.invariant === finding.invariant));
    if (!run) continue;
    const violation = run.violations.find((entry) => entry.invariant === finding.invariant);
    findings.push({
      id: finding.id,
      kind: finding.kind,
      title: finding.title,
      harm: finding.harm,
      evidence: finding.evidence,
      model: {
        module: run.modelModule,
        config: run.configFile,
        invariant: finding.invariant,
        shortestWitness: violation,
      },
    });
  }
  return findings;
}

function writeReports({ sourceCommit, jarPath, javaVersion, checks, findings, retiredFindings, resolvedFindings, map, traceValidation }) {
  const routeLedgerCheck = checks.find((check) => check.name === 'builder-route-ledger');
  const report = {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    sourceCommit,
    tool: {
      name: 'TLC',
      version: TLC_VERSION,
      sha256: TLC_SHA256,
      java: javaVersion,
      jar: 'tla2tools-' + TLC_VERSION + '.jar',
    },
    checks: checks.map((check) => ({
      name: check.name,
      status: check.status,
      ...(check.stateCount === undefined ? {} : { stateCount: check.stateCount, distinctStateCount: check.distinctStateCount, depth: check.depth }),
      ...(check.tests === undefined ? {} : { tests: check.tests }),
      ...(check.traceCount === undefined ? {} : {
        traceCount: check.traceCount,
        uniqueTraceCount: check.uniqueTraceCount,
        validatedCount: check.validatedCount,
        modelCounts: check.modelCounts,
      }),
      ...(check.outputTail ? { outputTail: check.outputTail } : {}),
    })),
    routeLedger: routeLedgerCheck?.counts ?? null,
    traceValidation,
    findings,
    retiredFindings,
    resolvedFindings,
    coverage: {
      affordances: map.affordances.length,
      modeled: map.affordances.filter((entry) => entry.modelStatus === 'modeled').length,
      partiallyModeled: map.affordances.filter((entry) => entry.modelStatus === 'partially-modeled').length,
      unmodeled: map.affordances.filter((entry) => entry.modelStatus === 'unmodeled').length,
      tested: map.affordances.filter((entry) => entry.implementationStatus === 'tested').length,
      fixtureGap: map.affordances.filter((entry) => entry.implementationStatus === 'fixture-gap').length,
      untested: map.affordances.filter((entry) => entry.implementationStatus === 'untested').length,
    },
    unmodeled: map.affordances
      .filter((entry) => entry.modelStatus !== 'modeled' || entry.implementationStatus !== 'tested')
      .map((entry) => ({
        id: entry.id,
        modelStatus: entry.modelStatus,
        implementationStatus: entry.implementationStatus,
        reason: entry.reason,
      })),
  };
  writeFileSync(resolve(ROOT, REPORT_RELATIVE), JSON.stringify(report, null, 2) + '\n');

  const lines = [];
  lines.push('# Builder formal verification report');
  lines.push('');
  lines.push('Source commit: `' + sourceCommit + '`');
  lines.push('');
  lines.push('Tool: TLC ' + TLC_VERSION + ' (`' + TLC_SHA256 + '`), ' + javaVersion + '.');
  lines.push('');
  lines.push('The model is bounded to the state spaces in the checked-in `.cfg` files. A TLC pass covers those bounds, not an unbounded refinement proof of the React or Go applications.');
  lines.push('');
  lines.push('## Checks');
  lines.push('');
  for (const check of report.checks) {
    const counts = check.stateCount === undefined ? '' : ` (${check.stateCount} generated / ${check.distinctStateCount} distinct, depth ${check.depth})`;
    lines.push('- `' + check.name + '`: **' + check.status + '**' + counts);
  }
  lines.push('');
  if (report.routeLedger) {
    const ledger = report.routeLedger;
    lines.push('## Route ledger');
    lines.push('');
    lines.push('- Builder routes: ' + ledger.builder + ' (' + ledger.tier1 + ' Tier 1, ' + ledger.tier2 + ' Tier 2).');
    lines.push('- Proof: ' + ledger.modeledAndTraced + ' modeled-and-traced, ' + ledger.modeledOnly + ' modeled-only, ' +
      ledger.contractTested + ' contract-tested, ' + ledger.unproven + ' unproven.');
    lines.push('- Excluded authoring routes: ' + ledger.excluded + '.');
    lines.push('- Builder evidence: ' + ledger.withBuilderEvidence + '/' + ledger.builder + ' routes.');
    const countLabel = (count, singular, plural) => count + ' ' + (count === 1 ? singular : plural);
    lines.push('- Route-level evidence: ' + countLabel(ledger.withTrace, 'trace', 'traces') + ', ' +
      countLabel(ledger.withTest, 'test', 'tests') + ', ' + countLabel(ledger.withBrowserCase, 'browser case', 'browser cases') + '.');
    lines.push('');
  }
  lines.push('## Trace conformance');
  lines.push('');
  lines.push('- ' + traceValidation.traceCount + ' formal traces, ' + traceValidation.uniqueTraceCount + ' unique after deduplication.');
  lines.push('- ' + traceValidation.validatedCount + ' validated against TLC model paths; ' + traceValidation.invalidCount + ' invalid.');
  lines.push('- Models: ' + Object.entries(traceValidation.modelCounts).map(([model, count]) => model + '=' + count).join(', ') + '.');
  const missingActions = traceValidation.coverage?.requiredActions?.missing ?? [];
  const missingEvents = traceValidation.coverage?.requiredEvents?.missing ?? [];
  lines.push('- Required actions: ' + (missingActions.length === 0 ? 'all covered' : 'missing ' + missingActions.join(', ')) + '.');
  lines.push('- Required events: ' + (missingEvents.length === 0 ? 'all covered' : 'missing ' + missingEvents.join(', ')) + '.');
  const unmodeledTotal = traceValidation.unmodeledEvents?.total ?? 0;
  const unclassifiedEvents = traceValidation.unmodeledEvents?.unclassified?.total ?? 0;
  const unmodeledKinds = Object.entries(traceValidation.unmodeledEvents?.byKind ?? {});
  lines.push('- Implementation events outside the trace vocabulary: ' + unmodeledTotal + ', ' + unclassifiedEvents + ' unclassified.');
  if (unmodeledKinds.length > 0) {
    lines.push('- Classified event kinds: ' + unmodeledKinds.map(([kind, count]) => kind + '=' + count).join(', ') + '.');
  }
  lines.push('');
  lines.push('## Resolved regressions');
  lines.push('');
  for (const resolved of resolvedFindings) {
    lines.push('- `' + resolved.id + '`: ' + resolved.title + ' Regression: `' + resolved.regressionTest + '`.');
  }
  lines.push('');
  lines.push('## Retired findings');
  lines.push('');
  if (retiredFindings.length === 0) lines.push('No findings are retired.');
  for (const retired of retiredFindings) {
    lines.push('### `' + retired.id + '`');
    lines.push('');
    lines.push('- ' + retired.title);
    lines.push('- Retired because: ' + retired.reason);
    lines.push('- Evidence: ' + retired.evidence.map((entry) => '`' + entry + '`').join(', '));
    lines.push('');
  }
  lines.push('## Findings');
  lines.push('');
  if (findings.length === 0) lines.push('No model or implementation findings were reproduced in this run.');
  for (const finding of findings) {
    lines.push('### ' + finding.title);
    lines.push('');
    lines.push('- Kind: `' + finding.kind + '`');
    lines.push('- Harm: ' + finding.harm);
    lines.push('- TLC witness actions: `' + (finding.model.shortestWitness.actions.join(' → ') || '(initial state)') + '`');
    lines.push('- TLC witness labels: `' + (finding.model.shortestWitness.lastActions.join(' → ') || '(none)') + '`');
    if (finding.implementationEvidence) {
      lines.push('- Real-controller test: `' + finding.implementationEvidence + '` (retained expected failure).');
    }
    lines.push('- Source evidence: ' + finding.evidence.map((entry) => '`' + entry + '`').join(', '));
    lines.push('');
  }
  lines.push('');
  lines.push('## Coverage gaps');
  lines.push('');
  for (const entry of report.unmodeled) {
    lines.push('- `' + entry.id + '`: model `' + entry.modelStatus + '`, implementation `' + entry.implementationStatus + '`. ' + entry.reason);
  }
  lines.push('');
  writeFileSync(resolve(ROOT, REPORT_MD_RELATIVE), lines.join('\n').trimEnd() + '\n');
  return report;
}

function runRouteLedger() {
  const freshness = runCommand(
    'Builder route ledger freshness',
    process.execPath,
    ['scripts/generate-builder-route-ledger.mjs', '--check'],
    ROOT,
  );
  if (freshness.status !== 0) {
    return {
      name: 'builder-route-ledger',
      status: 'fail',
      counts: null,
      output: freshness.output,
    };
  }
  const result = runCommand(
    'Builder route ledger',
    process.execPath,
    ['scripts/verify-builder-route-ledger.mjs', '--report-only'],
    ROOT,
  );
  let counts = null;
  try {
    counts = JSON.parse(result.output.slice(result.output.indexOf('{'), result.output.lastIndexOf('}') + 1));
  } catch {
    counts = null;
  }
  return {
    name: 'builder-route-ledger',
    status: result.status === 0 ? 'pass' : 'fail',
    counts,
    output: result.output,
  };
}

function runReadiness() {
  const obligations = runCommand(
    'Builder obligation report freshness',
    process.execPath,
    ['scripts/build-test-obligations.mjs', '--check'],
    ROOT,
  );
  if (obligations.status !== 0) {
    return { name: 'builder-readiness', status: 'fail', output: obligations.output };
  }
  const readiness = runCommand(
    'Builder readiness report',
    process.execPath,
    ['scripts/build-builder-readiness.mjs', '--check'],
    ROOT,
  );
  return { name: 'builder-readiness', status: readiness.status === 0 ? 'pass' : 'fail', output: readiness.output };
}

function parseArgs() {
  const args = process.argv.slice(2);
  const options = { strict: false, skipGo: false, model: null, listModels: false };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--strict') options.strict = true;
    else if (arg === '--skip-go') options.skipGo = true;
    else if (arg === '--model') options.model = args[++index];
    else if (arg === '--list-models') options.listModels = true;
    else fail('usage: node scripts/verify-builder-formal.mjs [--strict] [--skip-go] [--model <name>] [--list-models]');
  }
  return options;
}

async function main() {
  const options = parseArgs();
  if (realpathSync(process.cwd()) !== ROOT) {
    fail('run this command from the repository root: ' + ROOT);
  }
  const manifest = requirePinnedSources();
  process.env.LOOM_SOURCE_COMMIT = manifest.sourceCommit;
  const inventory = readJson(INVENTORY_RELATIVE);
  if (manifest.sourceCommit !== inventory.sourceCommit) {
    fail('builder-preview-contract.json sourceCommit does not match the inventory');
  }
  const map = readJson(MAP_RELATIVE);
  const modelText = [
    bytes(MODEL_DIR + '/BuilderOperations.tla').toString('utf8'),
    bytes(MODEL_DIR + '/BuilderPreview.tla').toString('utf8'),
    bytes(MODEL_DIR + '/WorkspaceLifecycle.tla').toString('utf8'),
    bytes(MODEL_DIR + '/RowDefinitionLifecycle.tla').toString('utf8'),
    bytes(MODEL_DIR + '/DraftRestore.tla').toString('utf8'),
    bytes(MODEL_DIR + '/RootChange.tla').toString('utf8'),
    bytes(MODEL_DIR + '/ColumnPresentation.tla').toString('utf8'),
    bytes(MODEL_DIR + '/Population.tla').toString('utf8'),
    bytes(MODEL_DIR + '/Interpretation.tla').toString('utf8'),
    bytes(MODEL_DIR + '/SupportingSurfaces.tla').toString('utf8'),
    bytes(MODEL_DIR + '/LifecycleEntry.tla').toString('utf8'),
    bytes(MODEL_DIR + '/ReconcileLifecycle.tla').toString('utf8'),
  ].join('\n');
  validateCoverageMap(inventory, map, modelText);

  const javaVersion = verifyJava();
  const jarPath = await acquireTlcJar();
  if (options.listModels) {
    for (const model of MODEL_RUNS) console.log(model.name + ' (' + model.module + ')');
    return;
  }
  if (options.model) {
    const model = MODEL_RUNS.find((candidate) => candidate.name === options.model || candidate.module === options.model);
    if (!model) fail('unknown model ' + options.model + '; use --list-models');
    const result = runTlc(jarPath, model.module, model.config);
    if (!result.passed) fail(model.config + ' did not complete without an error.');
    console.log('TLC ' + model.name + ': ' + result.stateCount + ' generated / ' + result.distinctStateCount + ' distinct, depth ' + result.depth);
    return;
  }
  process.env.GOCACHE = process.env.LOOM_GO_CACHE || join(tmpdir(), 'loom-go-build-cache');

  const operations = runTlc(jarPath, 'BuilderOperations', 'BuilderOperations.cfg');
  const safety = runTlc(jarPath, 'BuilderPreview', 'BuilderPreviewSafety.cfg');
  const workspace = runTlc(jarPath, 'WorkspaceLifecycle', 'WorkspaceLifecycle.cfg');
  const rowDefinition = runTlc(jarPath, 'RowDefinitionLifecycle', 'RowDefinitionLifecycle.cfg');
  const draftRestore = runTlc(jarPath, 'DraftRestore', 'DraftRestore.cfg');
  const rootChange = runTlc(jarPath, 'RootChange', 'RootChange.cfg');
  const columnPresentation = runTlc(jarPath, 'ColumnPresentation', 'ColumnPresentation.cfg');
  const population = runTlc(jarPath, 'Population', 'Population.cfg');
  const interpretation = runTlc(jarPath, 'Interpretation', 'Interpretation.cfg');
  const supportingSurfaces = runTlc(jarPath, 'SupportingSurfaces', 'SupportingSurfaces.cfg');
  const lifecycleEntry = runTlc(jarPath, 'LifecycleEntry', 'LifecycleEntry.cfg');
  const reconcileLifecycle = runTlc(jarPath, 'ReconcileLifecycle', 'ReconcileLifecycle.cfg');

  if (!operations.passed) fail('BuilderOperations.cfg did not complete without an error.');
  if (!safety.passed) fail('BuilderPreviewSafety.cfg did not complete without an error.');
  if (!workspace.passed) fail('WorkspaceLifecycle.cfg did not complete without an error.');
  if (!rowDefinition.passed) fail('RowDefinitionLifecycle.cfg did not complete without an error.');
  if (!draftRestore.passed) fail('DraftRestore.cfg did not complete without an error.');
  if (!rootChange.passed) fail('RootChange.cfg did not complete without an error.');
  if (!columnPresentation.passed) fail('ColumnPresentation.cfg did not complete without an error.');
  if (!population.passed) fail('Population.cfg did not complete without an error.');
  if (!interpretation.passed) fail('Interpretation.cfg did not complete without an error.');
  if (!supportingSurfaces.passed) fail('SupportingSurfaces.cfg did not complete without an error.');
  if (!lifecycleEntry.passed) fail('LifecycleEntry.cfg did not complete without an error.');
  if (!reconcileLifecycle.passed) fail('ReconcileLifecycle.cfg did not complete without an error.');

  const scheduleRun = runFormalSchedules();
  const traceValidation = runTraceValidation(scheduleRun.tracePath);
  const traceSelfTest = runTraceValidatorSelfTest(scheduleRun.tracePath);
  const checks = [
    { name: 'builder-operations', status: 'pass', stateCount: operations.stateCount, distinctStateCount: operations.distinctStateCount, depth: operations.depth },
    { name: 'builder-preview-safety', status: 'pass', stateCount: safety.stateCount, distinctStateCount: safety.distinctStateCount, depth: safety.depth },
    { name: 'builder-workspace-lifecycle', status: 'pass', stateCount: workspace.stateCount, distinctStateCount: workspace.distinctStateCount, depth: workspace.depth },
    { name: 'builder-row-definition-lifecycle', status: 'pass', stateCount: rowDefinition.stateCount, distinctStateCount: rowDefinition.distinctStateCount, depth: rowDefinition.depth },
    { name: 'builder-draft-restore', status: 'pass', stateCount: draftRestore.stateCount, distinctStateCount: draftRestore.distinctStateCount, depth: draftRestore.depth },
    { name: 'builder-root-change', status: 'pass', stateCount: rootChange.stateCount, distinctStateCount: rootChange.distinctStateCount, depth: rootChange.depth },
    { name: 'builder-column-presentation', status: 'pass', stateCount: columnPresentation.stateCount, distinctStateCount: columnPresentation.distinctStateCount, depth: columnPresentation.depth },
    { name: 'builder-population', status: 'pass', stateCount: population.stateCount, distinctStateCount: population.distinctStateCount, depth: population.depth },
    { name: 'builder-interpretation', status: 'pass', stateCount: interpretation.stateCount, distinctStateCount: interpretation.distinctStateCount, depth: interpretation.depth },
    { name: 'builder-supporting-surfaces', status: 'pass', stateCount: supportingSurfaces.stateCount, distinctStateCount: supportingSurfaces.distinctStateCount, depth: supportingSurfaces.depth },
    { name: 'builder-lifecycle-entry', status: 'pass', stateCount: lifecycleEntry.stateCount, distinctStateCount: lifecycleEntry.distinctStateCount, depth: lifecycleEntry.depth },
    { name: 'builder-reconcile-lifecycle', status: 'pass', stateCount: reconcileLifecycle.stateCount, distinctStateCount: reconcileLifecycle.distinctStateCount, depth: reconcileLifecycle.depth },
    scheduleRun,
    traceValidation,
    traceSelfTest,
  ];

  if (!options.skipGo) {
    checks.push(goTest('./internal/explorer/lifecycle', GO_LIFECYCLE_TESTS));
    checks.push(goTest('./internal/server', GO_SERVER_TESTS));
  } else {
    checks.push({ name: 'go-lifecycle-replay', status: 'skipped' });
    checks.push({ name: 'go-server-boundary', status: 'skipped' });
  }
  checks.push(runInventorySourceCheck());
  checks.push(runRouteLedger());
  checks.push(runReadiness());

  const findings = buildFindings([]);
  const resolvedFindings = RESOLVED_FINDINGS.filter((resolved) =>
    scheduleRun.regressionTests.some((name) => name.includes(resolved.regressionTest)));
  if (resolvedFindings.length !== RESOLVED_FINDINGS.length) {
    fail('expected regression coverage for every resolved finding, found ' + resolvedFindings.length);
  }
  const failedChecks = checks.filter((check) => check.status === 'fail');
  const report = writeReports({
    sourceCommit: manifest.sourceCommit,
    jarPath,
    javaVersion,
    checks,
    findings,
    retiredFindings: RETIRED_FINDINGS,
    resolvedFindings,
    map,
    traceValidation,
  });

  console.log('\nBuilder formal verification report: ' + resolve(ROOT, REPORT_RELATIVE));
  if (report.routeLedger) console.log('Route ledger: ' + JSON.stringify(report.routeLedger));
  console.log('Coverage: ' + JSON.stringify(report.coverage));
  console.log('Trace conformance: ' + report.traceValidation.validatedCount + '/' + report.traceValidation.traceCount + ' validated; ' +
    report.traceValidation.uniqueTraceCount + ' unique; unclassified events=' + (report.traceValidation.unmodeledEvents?.unclassified?.total ?? 0));
  for (const finding of findings) {
    console.log('FINDING [' + finding.kind + '] ' + finding.id + ': ' + finding.title);
  }
  for (const entry of report.unmodeled) {
    console.log('COVERAGE GAP ' + entry.id + ': model=' + entry.modelStatus + ', implementation=' + entry.implementationStatus);
  }
  if (failedChecks.length > 0) {
    fail('verification checks failed: ' + failedChecks.map((check) => check.name).join(', '));
  }
  if (options.strict) {
    const blockers = [];
    if (findings.length > 0) blockers.push(findings.length + ' findings');
    if (report.unmodeled.length > 0) blockers.push(report.unmodeled.length + ' coverage gaps');
    if (blockers.length > 0) fail('strict mode: ' + blockers.join(', ') + ' remain');
  }
  console.log('\nHARNESS PASS Builder formal verification; ' + findings.length + ' findings, ' + report.unmodeled.length + ' coverage gaps reported.');
}

main().catch((error) => {
  console.error('\nFAIL Builder formal verification: ' + error.message);
  process.exitCode = 1;
});
