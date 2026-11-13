import assert from 'node:assert/strict';
import test from 'node:test';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { assertHistoryStressRows, assertHistoryStressPlan, historyStressStepIDs, nextStressStep, seedThroughProductionProposals } from '../../workflows/graph-rows-history-stress.mjs';

const ids = ['obs-one', 'obs-many-A', 'obs-many-B'];
const preview = () => ({ sampled: false, rows: ids.map(id => Object.fromEntries([
  ['subject_id', id], ...Array.from({ length: 100 }, (_, index) => [`stress_value_${String(index).padStart(3, '0')}`, 3]),
])) });
const plan = () => ({ construction: { steps: historyStressStepIDs.map((id, index) => ({ id,
  inputs: index === 0 ? [{ kind: 'SOURCE_PROJECTION' }] : [{ kind: 'STEP_OUTPUT', stepId: historyStressStepIDs[index - 1] }],
  operation: { kind: 'DERIVE', derive: { constructionId: id, outputColumnId: `stress_column_${String(index).padStart(3, '0')}`, operation: 'ADD', left: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } }, right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } }, missingInputPolicy: 'ERROR' } },
})) } });

test('100-operation result oracle rejects changed identity, multiplicity, missing values and arbitrary values', () => {
  assertHistoryStressRows(preview(), 'subject_id');
  let changed = preview(); changed.rows.push(structuredClone(changed.rows[0]));
  assert.throws(() => assertHistoryStressRows(changed, 'subject_id'));
  changed = preview(); changed.rows.pop();
  assert.throws(() => assertHistoryStressRows(changed, 'subject_id'));
  changed = preview(); changed.rows[2].stress_value_099 = 42;
  assert.throws(() => assertHistoryStressRows(changed, 'subject_id'), /Independent 1 \+ 2 oracle/);
  changed = preview(); delete changed.rows[1].stress_value_099;
  assert.throws(() => assertHistoryStressRows(changed, 'subject_id'), /Missing calculated value/);
  changed = preview(); changed.sampled = true;
  assert.throws(() => assertHistoryStressRows(changed, 'subject_id'), /complete result/);
});

test('history oracle rejects lost IDs, visual reordering and changed stage input anchors', () => {
  assertHistoryStressPlan(plan());
  let changed = plan(); changed.construction.steps.splice(50, 1);
  assert.throws(() => assertHistoryStressPlan(changed), /execution order/);
  changed = plan(); [changed.construction.steps[0], changed.construction.steps[1]] = [changed.construction.steps[1], changed.construction.steps[0]];
  assert.throws(() => assertHistoryStressPlan(changed), /execution order/);
  changed = plan(); changed.construction.steps[99].inputs[0].stepId = historyStressStepIDs[0];
  assert.throws(() => assertHistoryStressPlan(changed));
});


test('the actual public construction decoder accepts the complete 100-step fixture', async () => {
  const uiPackage = new URL('../../../../ui/package.json', import.meta.url);
  const requireUI = createRequire(uiPackage);
  const { build } = requireUI('esbuild');
  const root = resolve(fileURLToPath(new URL('../../../..', import.meta.url)));
  const bundled = await build({ stdin: { contents: 'export { constructionSchema } from "./ui/packages/loom-ui/src/types.ts";',
    resolveDir: root, sourcefile: 'graph-rows-runtime-schema-check.mjs' }, bundle: true, write: false, format: 'esm', platform: 'node' });
  const { constructionSchema } = await import(`data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString('base64')}`);
  const construction = { version: 1, steps: [] };
  let columns = [{ id: 'observation_id', name: 'observation_id', label: 'Observation ID', type: 'string' }, { id: 'score', name: 'score', label: 'Fixture score', type: 'integer' }];
  for (let index = 0; index < 100; index += 1) {
    const step = nextStressStep(index, columns);
    construction.steps.push(step);
    assert.equal(step.outputs.length, index + 3, 'Every authored stage retains both source fields and every prior calculated output');
    columns = step.outputs;
  }
  assert.equal(constructionSchema.parse(construction).steps.length, 100);
  assertHistoryStressPlan({ construction });
});


test('the30s fixture bootstrap allowance cannot enter a native lifecycle context', async () => {
  const marker = new Error('request reached');
  let observedTimeout;
  const request = { post: async (_url, options) => { observedTimeout = options.timeout; throw marker; } };
  const state = { catalog: { snapshotToken: 'fixture-snapshot' }, draftVersion: 1, draftDigest: 'fixture-digest' };
  await assert.rejects(seedThroughProductionProposals({ report: { phase: 'native', requestBudgetMs: 30000 } }, request, state, 'output', { fixtureRequestTimeoutMs: 30000 }), /cannot be used by a native lifecycle/);
  assert.equal(observedTimeout, undefined, 'Native-context rejection must precede every request');
  await assert.rejects(seedThroughProductionProposals({ report: { phase: 'native' } }, request, state, 'output'), error => error === marker);
  assert.equal(observedTimeout, 5000, 'The default helper preserves its native request budget');
  await assert.rejects(seedThroughProductionProposals({ report: { phase: 'production-fixture-preparation', requestBudgetMs: 30000 } }, request, state, 'output', { fixtureRequestTimeoutMs: 30000 }), error => error === marker);
  assert.equal(observedTimeout, 30000, 'Only the declared separate fixture context receives its recorded setup budget');
});
