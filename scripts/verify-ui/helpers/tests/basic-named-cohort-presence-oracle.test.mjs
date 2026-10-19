import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { fixtureProjectExplorerCollectionPath } from '../fixture-context.mjs';
import { canonicalProjectID } from '../../../loom-dev.mjs';
import {
  assertBasicNamedCohortPresenceRows,
  buildBasicNamedCohortPresenceOracle,
} from '../basic-named-cohort-presence-oracle.mjs';

const fixtureDir = new URL('../../../../testdata/basic-named-cohort-presence-zero-match/', import.meta.url);
const workflowSource = readFileSync(new URL('../../workflows/basic-named-cohort-presence.mjs', import.meta.url), 'utf8');
const read = filename => readFileSync(new URL(filename, fixtureDir), 'utf8').trim().split(/\r?\n/).map(line => JSON.parse(line));
const fixture = {
  patients: read('Patient.ndjson'),
  specimens: read('Specimen.ndjson'),
  observations: read('Observation.ndjson'),
};

test('literal Basic fixture proves nonempty zero-match Group false and positive comparator true', () => {
  const oracle = buildBasicNamedCohortPresenceOracle(fixture);
  assert.deepEqual(oracle.sourceCounts, { patients: 2, specimens: 2, observations: 1 });
  assert.deepEqual(oracle.groups.map(({ memberIDs, observationIDs, presence }) => ({ memberIDs, observationIDs, presence })), [
    { memberIDs: ['basic-presence-zero-specimen-001'], observationIDs: [], presence: false },
    { memberIDs: ['basic-presence-positive-specimen-001'], observationIDs: ['basic-presence-positive-observation-001'], presence: true },
  ]);
});

test('literal oracle rejects an absent explicit Group member or a mismatched related Observation', () => {
  assert.throws(() => buildBasicNamedCohortPresenceOracle({
    ...fixture,
    specimens: fixture.specimens.map(specimen => specimen.id === 'basic-presence-zero-specimen-001'
      ? { ...specimen, subject: { reference: 'Patient/basic-presence-positive-patient-001' } }
      : specimen),
  }), /exact explicit Group membership/);
  assert.throws(() => buildBasicNamedCohortPresenceOracle({
    ...fixture,
    observations: [{ ...fixture.observations[0], subject: { reference: 'Patient/basic-presence-zero-patient-001' } }],
  }), /exact related Observation IDs/);
});

test('row assertion requires exact group membership and Boolean results', () => {
  const source = buildBasicNamedCohortPresenceOracle(fixture);
  const oracle = { ...source, labelColumn: 'Group label' };
  const rows = [
    { 'Group label': 'Nonempty member with no related Observation', Members: ['basic-presence-zero-specimen-001'], Presence: false },
    { 'Group label': 'Nonempty member with one related Observation', Members: ['basic-presence-positive-specimen-001'], Presence: true },
  ];
  const accepted = assertBasicNamedCohortPresenceRows(rows, {
    sourceIDColumn: undefined, memberColumn: 'Members', presenceColumn: 'Presence', oracle, phase: 'literal test',
  });
  assert.deepEqual(accepted.map(row => row.presence), [false, true]);
  assert.throws(() => assertBasicNamedCohortPresenceRows([
    { ...rows[0], Members: ['basic-presence-positive-specimen-001'] }, rows[1],
  ], { memberColumn: 'Members', presenceColumn: 'Presence', oracle, phase: 'mutated test' }), /lost exact member|gained foreign member/);
  assert.throws(() => assertBasicNamedCohortPresenceRows([
    { ...rows[0], Presence: true }, rows[1],
  ], { memberColumn: 'Members', presenceColumn: 'Presence', oracle, phase: 'mutated presence' }), /exact Boolean false/);
});

test('browser Explorer creation uses the canonical collection path while direct API reads keep their route path', () => {
  const project = 'loom_dev_verify_example/cda-fhir-v1';
  assert.equal(fixtureProjectExplorerCollectionPath(project), '/api/v1/projects/loom_dev_verify_example%252Fcda-fhir-v1/explorers');
  assert.match(workflowSource, /const browserOrigin = new URL\(target\.uiUrl\)\.origin;/);
  assert.match(workflowSource, /const browserExplorerCollectionPath = fixtureProjectExplorerCollectionPath\(project\);/);
  assert.match(workflowSource, /entry\.origin === browserOrigin && entry\.method === 'POST' &&\s*entry\.path === browserExplorerCollectionPath && entry\.request\?\.title === explorerTitle/);
  assert.match(workflowSource, /const directAPIExplorerCollectionPath = `\/api\/v1\/projects\/\$\{encodeURIComponent\(project\)\}\/explorers`;/);
  assert.match(workflowSource, /const rootPath = `\$\{directAPIExplorerCollectionPath\}\/\$\{encodeURIComponent\(explorer\)\}\/authoring\/v2`;/);
});

test('explicit-group selection refs use canonical project identity and report storage alias separately', () => {
  const storageProject = 'loom_dev_verify_mv48uh79-0a5e8d8';
  const selectionProject = canonicalProjectID(storageProject);
  assert.equal(selectionProject, 'loom_dev_verify_mv48uh79/0a5e8d8');
  assert.match(workflowSource, /const selectionProject = canonicalProjectID\(project\);/);
  assert.match(workflowSource, /project: selectionProject, generation: target\.fixtureGeneration, resourceType: 'Specimen', id,/);
  assert.match(workflowSource, /assert\.equal\(selection\.project, selectionProject\);/);
  assert.match(workflowSource, /assert\.equal\(selectionPage\.revision\?\.project, selectionProject\);/);
  assert.match(workflowSource, /member\.ref\.project === selectionProject/);
  assert.match(workflowSource, /sourceIdentity: \{ project: selectionProject, storageProject: project,/);
});

test('API-seeded table reloads the exact saved output and draft before opening row settings', () => {
  const populationCommand = workflowSource.indexOf("await sendCommand([{ type: 'SET_TABLE_POPULATION'");
  const reload = workflowSource.indexOf("await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });", populationCommand);
  const readinessWait = workflowSource.indexOf('await page.waitForFunction(({ outputId, expectedDraftVersion, expectedDraftDigest, expectedRows }) =>', reload);
  const rowSettings = workflowSource.indexOf("const rowSettings = page.getByTestId('construction-rows-settings-trigger');", readinessWait);
  assert(populationCommand >= 0 && reload > populationCommand && readinessWait > reload && rowSettings > readinessWait,
    'The current UI must reload and settle the exact saved draft after API seeding before row settings open.');
  assert.match(workflowSource, /workspace\?\.dataset\.draftVersion === expectedDraftVersion && workspace\.dataset\.draftDigest === expectedDraftDigest/);
  assert.match(workflowSource, /selected\[0\]\.getAttribute\('data-testid'\) === `construction-table-\$\{outputId\}`/);
  assert.match(workflowSource, /preview\.dataset\.currentDraftVersion === expectedDraftVersion && preview\.dataset\.currentDraftDigest === expectedDraftDigest/);
  assert.match(workflowSource, /table\?\.getAttribute\('aria-rowcount'\) === String\(expectedRows \+ 1\)/);
});
