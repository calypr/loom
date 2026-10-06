import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const workflow = await readFile(new URL('../../workflows/verify-cda-collection-repair.mjs', import.meta.url), 'utf8');

function assertReloadCheckpoint({ name, start, open, savedState, coverage, duration, record }) {
  const positions = [start, open, savedState, coverage, duration, record].map(value => workflow.indexOf(value));
  assert(positions.every(position => position >= 0), `${name}: every latency-boundary marker must exist`);
  assert(positions.every((position, index) => index === 0 || positions[index - 1] < position),
    `${name}: start → navigation → saved state → coverage → budget → recorded case order changed`);
}

test('partial collection reload budgets include exact preview rows and persisted state', () => {
  assert.match(workflow, /if\(partialLongRoute\)await assertPreviewIDs\(previewName\)/,
    'the UI reload checkpoint must render and compare exact raw-oracle Observation IDs');
  assert.match(workflow, /rendered\.rows\.map\(row=>row\[0\]\)\.sort\(\),\[\.\.\.expectedObservationIDs\]\.sort\(\)/,
    'the preview checkpoint must retain exact identity and multiplicity comparison');
  const openStart = workflow.indexOf('const open = async');
  const openEnd = workflow.indexOf('const checkCoverage = async', openStart);
  const openBody = workflow.slice(openStart, openEnd);
  assert(openBody.includes('if(partialLongRoute)await assertPreviewIDs(previewName);'),
    'each timed reload must call the shared exact-row comparison before returning from navigation');

  assertReloadCheckpoint({
    name: 'repair reload',
    start: 'const repairReloadStartedAt=Date.now();',
    open: "await open(partialLongRoute?'partial-long-route-reload-raw-oracle':undefined);",
    savedState: "assert.deepEqual(reloaded.population.route,report.savedConnection,'Reload must preserve the exact saved Observation → Specimen → parent route');",
    coverage: "await checkCoverage('partial-collection-reload','2 selected · 2 produce rows · 0 needs attention');",
    duration: 'const repairReloadDuration=Date.now()-repairReloadStartedAt;',
    record: "recordCase({name:'partial-long-route-reload-exact-rows-and-state',durationMs:repairReloadDuration,observationIDs:expectedObservationIDs});",
  });
  assert.match(workflow, /assert\(repairReloadDuration<=5000,/,
    'the repair reload must fail the case when exact rows or saved state take more than five seconds');

  assertReloadCheckpoint({
    name: 'reattached reload',
    start: 'const reattachedReloadStartedAt=Date.now();',
    open: "await open(partialLongRoute?'reattached-partial-long-route-reload-raw-oracle':undefined);",
    savedState: "assert.deepEqual(reattachedReload.population,revised.population,'Reload after reattachment must preserve the exact two-member selection and route');",
    coverage: "await checkCoverage('reattached-partial-long-collection-reload','2 selected · 2 produce rows · 0 needs attention');",
    duration: 'const reattachedReloadDuration=Date.now()-reattachedReloadStartedAt;',
    record: "recordCase({name:'reattached-partial-long-route-reload-exact-rows-and-state',durationMs:reattachedReloadDuration,observationIDs:expectedObservationIDs});",
  });
  assert.match(workflow, /assert\(reattachedReloadDuration<=5000,/,
    'the reattached reload must fail the case when exact rows or saved state take more than five seconds');
  assert.match(workflow, /\.\.\.report\.cases\.map\(item => item\.durationMs\)[\s\S]*durationMs <= 5000/,
    'both reload checkpoints must flow into the existing required five-second performance check');
});
