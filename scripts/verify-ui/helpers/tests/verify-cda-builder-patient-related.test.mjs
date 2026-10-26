import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { assertPersistedPatientRelated, assertPreviewOracle, assertProposalOracle, patientRelatedStepInspectionCases, readPatientRelatedOracle, runPatientRelatedStepInspection } from '../../workflows/verify-cda-builder-patient-related.mjs';

test('saved related-step inspection only exposes its two declared cases', async () => {
  assert.deepEqual(patientRelatedStepInspectionCases, ['Inspect saved related step', 'Inspect related edit']);
  await assert.rejects(() => runPatientRelatedStepInspection({ action: 'unexpected', explorerId: '', env: {} }), /Unsupported saved Patient related inspection/);
});

test('Patient related oracle keeps nulls and independently checks source membership', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-patient-oracle-'));
  try {
    await mkdir(join(directory, 'META'));
    await writeFile(join(directory, 'META/Specimen.ndjson'), [
      JSON.stringify({ resourceType: 'Specimen', id: 'specimen-a', subject: { reference: 'Patient/patient-a' }, collection: { bodySite: { reference: { reference: 'BodyStructure/site-a' } } } }),
      JSON.stringify({ resourceType: 'Specimen', id: 'specimen-b', subject: { reference: 'Patient/missing' } }),
    ].join('\n') + '\n');
    await writeFile(join(directory, 'META/Patient.ndjson'), `${JSON.stringify({ resourceType: 'Patient', id: 'patient-a' })}\n`);
    const oracle = await readPatientRelatedOracle({ datasetDir: directory, project: 'loom_dev_test', generation: 'cda-test' });
    assert.equal(oracle.specimenCount, 2);
    assert.equal(oracle.patientCount, 1);
    assert.equal(oracle.rows.length, 2);
    const byId = new Map(oracle.rows.map(row => [row.id, row]));
    assert.deepEqual(byId.get('specimen-a'), { id: 'specimen-a', subject: 'Patient/patient-a', bodySite: 'BodyStructure/site-a', patientId: 'patient-a' });
    assert.deepEqual(byId.get('specimen-b'), { id: 'specimen-b', subject: 'Patient/missing', bodySite: null, patientId: null });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('preview oracle rejects missing ordinals and incorrect visible values', () => {
  const expected = [
    { id: 's1', subject: null, bodySite: 'site-1', patientId: null },
    { id: 's2', subject: 'Patient/p2', bodySite: null, patientId: 'p2' },
  ];
  const headers = ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'];
  const rows = [
    { ordinal: 1, cells: ['s1', '—', 'site-1'] },
    { ordinal: 2, cells: ['s2', 'Patient/p2', '—'] },
  ];
  assertPreviewOracle({ rows, expected, ariaRowCount: 3, headers, applied: false });
  assert.throws(() => assertPreviewOracle({ rows: rows.slice(0, 1), expected, ariaRowCount: 3, headers, applied: false }), /deep-equal|Visible Preview/);
  assert.throws(() => assertPreviewOracle({ rows: [{ ...rows[0], cells: ['wrong', '—', 'site-1'] }, rows[1]], expected, ariaRowCount: 3, headers, applied: false }), /deep-equal|Visible Preview/);
});

test('proposal and reload checks reject incorrect values or missing persisted history', () => {
  const expected = [{ id: 's1', subject: 'Patient/p1', bodySite: null, patientId: 'p1' }];
  const headers = ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE', 'PATIENT ID'];
  const proposalRows = [['s1', 'Patient/p1', '—', 'p1']];
  assertProposalOracle({ rows: proposalRows, expected, headers });
  assert.throws(() => assertProposalOracle({ rows: [['s1', 'Patient/p1', '—', 'wrong']], expected, headers }), /deep-equal|Proposal rows/);
  const persistedRows = [{ ordinal: 1, cells: proposalRows[0] }];
  assertPersistedPatientRelated({ expectedHistory: 'Add Patient ID', actualHistory: 'Add Patient ID', rows: persistedRows,
    expected, headers, ariaRowCount: 2 });
  assert.throws(() => assertPersistedPatientRelated({ expectedHistory: 'Add Patient ID', actualHistory: '', rows: persistedRows,
    expected, headers, ariaRowCount: 2 }), /Reload must restore/);
  assert.throws(() => assertPersistedPatientRelated({ expectedHistory: 'Add Patient ID', actualHistory: 'Add Patient ID', rows: [],
    expected, headers, ariaRowCount: 2 }), /deep-equal|Visible Preview/);
});
