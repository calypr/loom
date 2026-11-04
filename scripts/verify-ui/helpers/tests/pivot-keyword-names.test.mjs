import assert from 'node:assert/strict';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registry } from '../../registry.mjs';

const REPO = fileURLToPath(new URL('../../../..', import.meta.url));
const SCRIPT = 'scripts/verify-ui/workflows/verify-explorer-pivot-keyword-names.mjs';

const scenario = registry.find((entry) => entry.id === 'pivot-keyword-names');

test('pivot keyword-names script passes its static self-validation via --dry-run', () => {
  const run = spawnSync(process.execPath, [join(REPO, SCRIPT), '--dry-run'], { encoding: 'utf8', cwd: REPO, timeout: 30000 });
  assert.equal(run.status, 0, `--dry-run exited ${run.status}: ${run.stdout}${run.stderr}`);
  assert.match(run.stdout, /DRY_RUN_OK artifact=pivot-keyword-names checks=14/);
});

test('pivot keyword-names registry requiredChecks are machine-identical to the script expectedChecks', () => {
  assert.ok(scenario, 'registry entry pivot-keyword-names is missing');
  const source = readFileSync(join(REPO, SCRIPT), 'utf8');
  const body = source.match(/const expectedChecks = \[([\s\S]*?)\];/)[1];
  const names = [...body.matchAll(/'([^']+)'/g)].map((m) => m[1]);
  const keys = Object.keys(scenario.cases);
  assert.equal(keys.length, 1, `expected exactly one case, got ${JSON.stringify(keys)}`);
  const required = scenario.cases[keys[0]].requiredChecks;
  assert.equal(required.length, names.length, `count ${required.length} != ${names.length}`);
  names.forEach((name, index) => assert.equal(required[index], name, `name drift at ${index}`));
});

test('pivot keyword-names script keeps identity, 5xx-closed-set, anti-vacuity, and keyword guards', () => {
  const source = readFileSync(join(REPO, SCRIPT), 'utf8') + readFileSync(join(REPO, 'scripts/loom-dev.mjs'), 'utf8');
  for (const probe of [
    'loom-dev-build-stamp.sh',
    'serving build identity must be current',
    'live source moved during run',
    'skipped checks:',
    'Refusing to overwrite browser evidence directory',
    'server 5xx class is NEVER legitimate',
    'Cancel must not change the draft digest (byte stability)',
    'duplicate names were silently accepted into the document',
    'the duplicate-ID class needs a second category',
    'switching intent is the real close path',
    'Pivot output name',
    'construction-apply-proposal',
    'construction-row-remove-',
  ]) {
    assert.ok(source.includes(probe), `structural guard missing from artifact: ${probe}`);
  }
  const codeList = source.match(/const legit4xx = (\/[^/]+\/)/)[1];
  // eslint-disable-next-line no-new-func
  const re = new Function(`return ${codeList}`)();
  assert.ok(re.test('TABLE_PIVOT_CELL_CARDINALITY'), 'cardinality family must be admissible');
  assert.ok(!re.test('INTERNAL_SERVER_ERROR'), '5xx internals must never enter the admissible set');
  const oracle = readFileSync(join(REPO, 'scripts/verify-ui/helpers/pivot-keyword-fixture-oracle.mjs'), 'utf8');
  assert.ok(oracle.includes("'null'") && oracle.includes("'RETURN'"), 'oracle keyword names drifted from the AQL reserved-word pair');
});
