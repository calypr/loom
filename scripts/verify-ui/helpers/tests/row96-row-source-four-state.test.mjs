import assert from 'node:assert/strict';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registry } from '../../registry.mjs';

const REPO = fileURLToPath(new URL('../../../..', import.meta.url));
const SCRIPT = 'scripts/verify-ui/workflows/verify-explorer-row-source-four-state.mjs';

const scenario = registry.find((entry) => entry.id === 'row96-row-source-four-state');

test('row96 four-state script passes its static self-validation via --dry-run', () => {
  const run = spawnSync(process.execPath, [join(REPO, SCRIPT), '--dry-run'], { encoding: 'utf8', cwd: REPO, timeout: 30000 });
  assert.equal(run.status, 0, `--dry-run exited ${run.status}: ${run.stdout}${run.stderr}`);
  assert.match(run.stdout, /DRY_RUN_OK artifact=row96-four-state checks=9/);
});

test('row96 registry requiredChecks are machine-identical to the script expectedChecks', () => {
  assert.ok(scenario, 'registry entry row96-row-source-four-state is missing');
  const source = readFileSync(join(REPO, SCRIPT), 'utf8');
  const body = source.match(/const expectedChecks = \[([\s\S]*?)\];/)[1];
  const names = [...body.matchAll(/'([^']+)'/g)].map((m) => m[1]);
  const keys = Object.keys(scenario.cases);
  assert.equal(keys.length, 1, `expected exactly one case, got ${JSON.stringify(keys)}`);
  const required = scenario.cases[keys[0]].requiredChecks;
  assert.equal(required.length, names.length, `count ${required.length} != ${names.length}`);
  names.forEach((name, index) => assert.equal(required[index], name, `name drift at ${index}`));
});

test('row96 script keeps its identity, closed-set, and class-disjointness guards', () => {
  const source = readFileSync(join(REPO, SCRIPT), 'utf8') + readFileSync(join(REPO, 'scripts/loom-dev.mjs'), 'utf8');
  for (const probe of [
    'loom-dev-build-stamp.sh',
    'serving build identity must be current before the journey runs',
    'post-run: serving binary moved during run',
    'post-run: live source moved during run',
    'skipped checks:',
    'Refusing to overwrite browser evidence directory',
    '_SOURCE_INVENTORY_UNAVAILABLE',
    'NO_OBSERVED_VALUES_',
    'NO_ELIGIBLE_POPULATED_',
  ]) {
    assert.ok(source.includes(probe), `structural guard missing from artifact: ${probe}`);
  }
  const emptyFamily = source.match(/const emptyDataFamily = (\/[^/]+\/)/)[1];
  // eslint-disable-next-line no-new-func
  const re = new Function(`return ${emptyFamily}`)();
  assert.ok(!re.test('PIVOT_ROW_SOURCE_SOURCE_INVENTORY_UNAVAILABLE'), 'inventory class must never match the empty-data family regex');
  assert.ok(re.test('NO_OBSERVED_VALUES_GENDER_SOURCE_FIELDS'));
});

test('row96 refusal and reasonCode assertions never fall back to vacuous passes', () => {
  const source = readFileSync(join(REPO, SCRIPT), 'utf8');
  assert.ok(source.includes('would pass vacuously'), 'lane capabilities must fail-loud when empty');
  assert.ok(source.includes('the label assertion would be vacuous'), 'label read must fail-loud when no titled option exists');
  assert.ok(!source.includes('assert.ok(true'), 'no unconditional assertions allowed in the artifact');
  assert.ok(!/\|\|\s*true\b/.test(source), 'no tautological || true tokens allowed');
  assert.match(source, /row96-ui-gap-reason-control/, 'the UI-gap side-Finding channel must exist for the reason consumer');
});
