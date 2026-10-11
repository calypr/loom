import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync } from 'node:fs';
import { previewHeaderIndex } from '../playwright-preview-rows.mjs';
import { readOptionalProposalID } from '../../workflows/basic-pivot-unpivot.mjs';
import {
  loadBasicPivotUnpivotFixtureOracle,
  readBasicPivotUnpivotSeedManifest,
  validateBasicPivotUnpivotRecords,
} from '../basic-pivot-unpivot-oracle.mjs';

test('visible Basic fixture ID lookup matches CSS-uppercased rendered headers', () => {
  assert.equal(previewHeaderIndex(['OBSERVATION ID'], 'Observation ID'), 0);
});

test('optional native proposal ID lookup returns absent immediately and reads an existing marker', async () => {
  const read = panels => readOptionalProposalID({
    evaluateAll: callback => Promise.resolve(callback(panels)),
  });
  assert.equal(await read([]), null);
  assert.equal(await read([{ getAttribute: name => name === 'data-proposal-id' ? 'proposal-17' : null }]), 'proposal-17');
});

test('Pivot→Unpivot Basic fixture gives literal source, Pivot, and unpivoted row multisets', () => {
  const oracle = loadBasicPivotUnpivotFixtureOracle();
  const sourceObservations = readFileSync(new URL('../../../../testdata/basic-pivot-unpivot/Observation.ndjson', import.meta.url), 'utf8')
    .trim().split(/\r?\n/).map(line => JSON.parse(line));
  assert.deepEqual(sourceObservations.map(({ id, code }) => [id, code]), [
    ['basic-pivot-unpivot-a-missing', { text: 'Basic fixture Observation' }],
    ['basic-pivot-unpivot-a-null', { text: 'Basic fixture Observation' }],
    ['basic-pivot-unpivot-a-d', { text: 'Basic fixture Observation' }],
    ['basic-pivot-unpivot-b-missing', { text: 'Basic fixture Observation' }],
    ['basic-pivot-unpivot-b-d', { text: 'Basic fixture Observation' }],
  ], 'Every FHIR Observation needs the literal required top-level code concept');
  assert.deepEqual(oracle.sourceRows, [
    { id: 'basic-pivot-unpivot-a-missing', subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'MISSING', value: 3 },
    { id: 'basic-pivot-unpivot-a-null', subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'NULL', value: 5 },
    { id: 'basic-pivot-unpivot-a-d', subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'd', value: 2 },
    { id: 'basic-pivot-unpivot-b-missing', subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'MISSING', value: 7 },
    { id: 'basic-pivot-unpivot-b-d', subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'd', value: 8 },
  ]);
  assert.deepEqual(oracle.categories.map(({ identity }) => identity), ['MISSING', 'NULL', 'd']);
  assert.deepEqual(oracle.pivotRows, [
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', MISSING: 3, NULL: 5, d: 2 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', MISSING: 7, NULL: null, d: 8 },
  ]);
  assert.deepEqual(oracle.preservingUnpivotRows, [
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'MISSING', value: 3 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'NULL', value: 5 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'd', value: 2 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'MISSING', value: 7 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'NULL', value: null },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'd', value: 8 },
  ]);
  assert.deepEqual(oracle.droppingUnpivotRows, [
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'MISSING', value: 3 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'NULL', value: 5 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'd', value: 2 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'MISSING', value: 7 },
    { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'd', value: 8 },
  ]);
  assert.deepEqual(oracle.absentCategoryRows, ['basic-pivot-unpivot-a-missing', 'basic-pivot-unpivot-b-missing']);
  assert.deepEqual(oracle.explicitNullCategoryRows, ['basic-pivot-unpivot-a-null']);
  assert.equal(oracle.preservingUnpivotRows.length, 6, 'Two group identities × three Pivot columns must preserve six rows');
  assert.equal(oracle.droppingUnpivotRows.length, 5, 'DROP must remove only the single generated null cell');
});

test('Pivot→Unpivot fixture manifest is closed over the two native FHIR source files', () => {
  const manifest = readBasicPivotUnpivotSeedManifest();
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 5 },
    { name: 'Patient.ndjson', recordCount: 2 },
  ]);
  assert(manifest.every(file => /^[a-f0-9]{64}$/.test(file.sha256)));
});

test('Pivot→Unpivot oracle rejects coalescing absent and explicit-null category states', () => {
  const oracle = loadBasicPivotUnpivotFixtureOracle();
  const observations = oracle.sourceRows.map(({ id, subjectReference, category, value }) => ({
    resourceType: 'Observation',
    id,
    status: 'final',
    subject: { reference: subjectReference },
    valueQuantity: category === 'MISSING' ? { value, unit: 'unit' }
      : { code: category === 'NULL' ? null : category, value, unit: 'unit' },
  }));
  const patients = [
    { resourceType: 'Patient', id: 'basic-pivot-unpivot-patient-a', gender: 'female' },
    { resourceType: 'Patient', id: 'basic-pivot-unpivot-patient-b', gender: 'male' },
  ];
  assert.throws(() => validateBasicPivotUnpivotRecords({
    patients,
    observations: observations.map(row => row.id === 'basic-pivot-unpivot-a-missing'
      ? { ...row, valueQuantity: { ...row.valueQuantity, code: null } }
      : row),
  }), /absent-versus-null category distinction/);
});
