import assert from 'node:assert/strict';
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import {tmpdir} from 'node:os';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {validatePlan} from './validate_ml_dataframer_plan.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const planPath = resolve(root, 'docs/product/ml-dataframer/execution.json');
const plan = JSON.parse(readFileSync(planPath, 'utf8'));
const packageById = (candidate, id) => candidate.workPackages.find((workPackage) => workPackage.id === id);
const gates = ['focused_tests', 'live_dom', 'literal_output', 'negative_cases', 'performance', 'review'];
const evidencePath = 'scripts/validate_ml_dataframer_plan.mjs';

function acceptPackage(candidate, id) {
  const workPackage = packageById(candidate, id);
  workPackage.status = 'accepted';
  for (const task of workPackage.tasks) task.status = 'accepted';
  for (const kpi of workPackage.kpis) {
    kpi.result = 'pass';
    kpi.observation = 'Observed the required output on the fixture.';
    kpi.evidence = [evidencePath];
  }
  workPackage.evidence = [evidencePath];
  workPackage.verification = {
    sha: 'a'.repeat(40),
    gates: Object.fromEntries(gates.map((gate) => [gate, 'pass'])),
  };
}

test('revision 4 plan and archive are structurally valid', () => {
  assert.deepEqual(validatePlan(plan), []);
});

test('malformed JSON values return diagnostics', () => {
  assert.ok(validatePlan(null).some((error) => error.includes('JSON object')));
  const candidate = structuredClone(plan);
  candidate.workPackages = [null];
  assert.doesNotThrow(() => validatePlan(candidate));
  assert.ok(validatePlan(candidate).some((error) => error.includes('malformed package')));
});

const cases = [
  ['obsolete schema', (candidate) => { candidate.schemaVersion = 1; }, 'schemaVersion must be 2'],
  ['obsolete revision', (candidate) => { candidate.planRevision = 3; }, 'planRevision must be 4'],
  ['inherited top-level status', (candidate) => { candidate.status = 'accepted'; }, 'status must be planned'],
  ['wrong baseline branch', (candidate) => { candidate.baseline.branch = 'main'; }, 'baseline branch'],
  ['missing baseline checkpoint', (candidate) => { candidate.baseline.requiresCheckpoint = false; }, 'requiresCheckpoint'],
  ['unsafe specification path', (candidate) => { candidate.specification = '../outside.md'; }, 'specification must be'],
  ['obsolete execution order', (candidate) => { candidate.executionOrder.reverse(); }, 'executionOrder must be'],
  ['extra package', (candidate) => { candidate.workPackages.push({...structuredClone(candidate.workPackages[4]), id: 'S06'}); }, 'exactly five packages'],
  ['unknown task', (candidate) => { packageById(candidate, 'S01').tasks[0].id = 'S01-05'; }, 'unknown task ID'],
  ['extra task', (candidate) => { packageById(candidate, 'S01').tasks.push({id: 'S01-05', status: 'planned'}); }, 'expected four implementation tasks'],
  ['missing task status', (candidate) => { delete packageById(candidate, 'S01').tasks[0].status; }, 'S01-01: invalid status'],
  ['unknown KPI', (candidate) => { packageById(candidate, 'S01').kpis[0].id = 'K-S01-d'; }, 'unknown KPI ID'],
  ['extra KPI', (candidate) => { packageById(candidate, 'S01').kpis.push({id: 'K-S01-d', result: 'unmeasured', evidence: []}); }, 'expected three KPIs'],
  ['dependency mismatch', (candidate) => { packageById(candidate, 'S04').dependsOn = ['S01']; }, 'ledger dependencies differ'],
  ['dependency before prerequisite', (candidate) => { packageById(candidate, 'S02').dependsOn = ['S04']; }, 'execution order precedes dependency S04'],
  ['dependency cycle', (candidate) => { packageById(candidate, 'S01').dependsOn = ['S02']; }, 'dependency cycle'],
  ['missing source file', (candidate) => { packageById(candidate, 'S01').sourcePaths = ['internal/not-a-source.go']; }, 'missing or unsafe source'],
  ['traversing evidence path', (candidate) => { packageById(candidate, 'S01').evidence = ['../outside.txt']; }, 'missing or unsafe evidence'],
  ['measured KPI without observation', (candidate) => { packageById(candidate, 'S01').kpis[0].result = 'pass'; }, 'measured result needs an observation'],
  ['measured KPI without evidence', (candidate) => { const kpi = packageById(candidate, 'S01').kpis[0]; kpi.result = 'fail'; kpi.observation = 'The output differed.'; }, 'measured result needs evidence'],
  ['journey row missing', (candidate) => { packageById(candidate, 'S01').journeys = ['J09']; }, 'acceptance journey rows must match'],
];

for (const [name, mutate, expected] of cases) {
  test(name, () => {
    const candidate = structuredClone(plan);
    mutate(candidate);
    assert.ok(validatePlan(candidate).some((error) => error.includes(expected)), `expected diagnostic: ${expected}`);
  });
}

test('acceptance requires all tasks, measured KPIs, evidence, SHA, gates, and accepted dependencies', () => {
  const candidate = structuredClone(plan);
  const first = packageById(candidate, 'S01');
  first.status = 'accepted';
  const errors = validatePlan(candidate);
  for (const expected of [
    'S01: unfinished implementation tasks',
    'S01: unproven KPI',
    'S01: accepted without evidence',
    'S01: verification SHA missing',
    ...gates.map((gate) => `S01: gate ${gate} not passed`),
  ]) assert.ok(errors.includes(expected), `expected diagnostic: ${expected}`);

  const dependencyCandidate = structuredClone(plan);
  acceptPackage(dependencyCandidate, 'S02');
  assert.ok(validatePlan(dependencyCandidate).includes('S02: unaccepted dependency S01'));
});

test('a package can be accepted only after every gate and dependency passes', () => {
  const candidate = structuredClone(plan);
  acceptPackage(candidate, 'S01');
  assert.deepEqual(validatePlan(candidate), []);
});

test('archive manifest checks archived bytes against recorded SHA-256', () => {
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'loom-plan-validator-'));
  try {
    const manifest = JSON.parse(readFileSync(resolve(root, plan.archiveManifest), 'utf8'));
    const files = new Set([
      plan.specification,
      plan.acceptance,
      plan.archiveManifest,
      ...plan.workPackages.flatMap((workPackage) => [
        ...workPackage.sourcePaths,
        ...workPackage.evidence,
        ...workPackage.kpis.flatMap((kpi) => kpi.evidence ?? []),
      ]),
      ...manifest.files.map((entry) => entry.archived),
    ]);
    for (const path of files) {
      const destination = resolve(temporaryRoot, path);
      mkdirSync(dirname(destination), {recursive: true});
      copyFileSync(resolve(root, path), destination);
    }
    const manifestPath = resolve(temporaryRoot, plan.archiveManifest);
    manifest.files[0].sha256 = '0'.repeat(64);
    writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
    assert.ok(validatePlan(plan, temporaryRoot).some((error) => error.includes('SHA-256 does not match')));
    writeFileSync(manifestPath, 'null\n');
    assert.ok(validatePlan(plan, temporaryRoot).some((error) => error.includes('must be a JSON object')));
  } finally {
    rmSync(temporaryRoot, {recursive: true, force: true});
  }
});
