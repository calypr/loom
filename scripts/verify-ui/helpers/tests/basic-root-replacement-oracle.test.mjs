import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync, mkdirSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { readBasicRootReplacementOracle } from '../basic-root-replacement-oracle.mjs';

const fixtureDir = new URL('../../../../testdata/basic-root-replacement/', import.meta.url).pathname;

test('Basic replacement fixture provides disjoint Patient rows and exact related replacement rows', () => {
  const oracle = readBasicRootReplacementOracle({ fixtureDir });
  assert.deepEqual(oracle.patients, [
    { id: 'replacement-patient-001', birthDate: '1980-01-02' },
    { id: 'replacement-patient-002', birthDate: '1990-05-06' },
    { id: 'replacement-patient-003', birthDate: '2000-12-31' },
  ]);
  assert.deepEqual(oracle.patientPopulationIDs, [
    'replacement-patient-001',
    'replacement-patient-002',
  ]);
  assert.deepEqual(oracle.patientFilteredPopulationIDs, ['replacement-patient-002']);
  assert.deepEqual(oracle.observations, [
    { id: 'replacement-observation-001', patientId: 'replacement-patient-001' },
    { id: 'replacement-observation-002', patientId: 'replacement-patient-002' },
  ]);
  assert.deepEqual(oracle.researchStudyRows, [
    { id: 'replacement-study-001', title: 'Replacement study alpha' },
    { id: 'replacement-study-002', title: 'Replacement study beta' },
  ]);
  assert.deepEqual(oracle.diagnosticReportIDs, [
    'replacement-report-001',
    'replacement-report-002',
  ]);
  assert.deepEqual(oracle.disconnectedResourceTypes, ['ResearchStudy']);
  assert.deepEqual(oracle.connectedPreservationRelationship, {
    fromResourceType: 'Patient',
    relationship: 'subject_Patient',
    toResourceType: 'Observation',
  });
  assert.deepEqual(oracle.connectedReplacementRelationship, {
    fromResourceType: 'ResearchStudy',
    relationship: 'result_DiagnosticReport',
    toResourceType: 'DiagnosticReport',
  });
  assert.equal(oracle.source.files.length, 4);
  assert(oracle.source.files.every(file => /^[a-f0-9]{64}$/.test(file.sha256)));
});

test('Basic replacement oracle rejects duplicate IDs and Patient links that invalidate root isolation', () => {
  const temp = mkdtempSync(join(tmpdir(), 'basic-root-replacement-oracle-'));
  try {
    mkdirSync(temp, { recursive: true });
    for (const name of ['Patient.ndjson', 'Observation.ndjson', 'ResearchStudy.ndjson', 'DiagnosticReport.ndjson']) {
      writeFileSync(join(temp, name), readFileSync(join(fixtureDir, name)));
    }
    writeFileSync(join(temp, 'ResearchStudy.ndjson'), [
      '{"resourceType":"ResearchStudy","id":"same","status":"active","title":"A"}',
      '{"resourceType":"ResearchStudy","id":"same","status":"completed","title":"B"}',
    ].join('\n'));
    assert.throws(() => readBasicRootReplacementOracle({ fixtureDir: temp }), /unique IDs/);

    const linkedStudies = readFileSync(join(fixtureDir, 'ResearchStudy.ndjson'), 'utf8').trim().split(/\r?\n/)
      .map(line => ({ ...JSON.parse(line), subject: { reference: 'Patient/replacement-patient-001' } }));
    writeFileSync(join(temp, 'ResearchStudy.ndjson'), `${linkedStudies.map(JSON.stringify).join('\n')}\n`);
    assert.throws(() => readBasicRootReplacementOracle({ fixtureDir: temp }), /disconnected/);
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
});
