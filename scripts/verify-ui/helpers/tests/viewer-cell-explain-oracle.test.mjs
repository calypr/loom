import test from 'node:test';
import assert from 'node:assert/strict';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readBasicPatientCellExplainOracle } from '../viewer-cell-explain-oracle.mjs';
import { assertCellTraceBinding, authorizationScopeDigestFromBuilder } from '../../workflows/viewer-cell-explain.mjs';

const fixtureDir = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/devloop-fixture');

test('Basic Viewer cell explanation oracle distinguishes literal female from a missing Gender field', () => {
  const oracle = readBasicPatientCellExplainOracle(fixtureDir);

  assert.deepEqual(oracle.patients, [
    { id: 'dev-patient-001', genderPresent: true, gender: 'female' },
    { id: 'dev-patient-002', genderPresent: false },
  ]);
  assert.deepEqual(oracle.explainedPatient, { id: 'dev-patient-001', value: 'female' });
  assert.equal(oracle.patientIDColumn, 'patient_id');
  assert.equal(oracle.patientIDSourcePath, 'id');
  assert.equal(oracle.genderColumn, 'gender');
  assert.equal(oracle.genderSourcePath, 'gender');
  assert.match(oracle.patientSHA256, /^[a-f0-9]{64}$/);
});

test('cell trace binds the receipt authorization scope separately from publication quality scope', () => {
  const canonicalProject = 'loom_dev_verify_mv1mtemg/08fdf9d';
  const explorer = 'default';
  const generation = 'cda-fhir-v1';
  const receiptId = 'receipt_4708fe4fcdd00b875fb6da6c03372d96a13a83887b32565dd7250965f546e167';
  const authorizationScopeDigest = 'ec9d970710fa72b2edaa12c9080c3861e1c191935535582b23f0a29b50f042d6';
  const publicationScopeDigest = '9d2f4d3f1421bf5d27b56c4eeb24a8111e14131f73bfcd159ce37794192bb6c7';
  const outputId = 'patients';
  const rowId = 'd3ea32601ce1c310cec81340c8ede920e18ec2adbcedcbf1660bdb4336d53308';
  const qualityReport = { receiptId, scopeDigest: publicationScopeDigest };
  const builder = { catalog: { generation, authorizationScopeDigest, snapshotToken: 'snapshot-retained' } };
  const actualAuthorizationScopeDigest = authorizationScopeDigestFromBuilder(builder, generation);
  const entry = {
    status: 200,
    request: { receiptId, outputId, rowId, column: 'gender', limit: 25 },
    response: {
      binding: { receiptId, outputId, project: canonicalProject, explorerId: explorer, generation,
        scopeDigest: authorizationScopeDigest },
      feature: { outputId, column: 'gender', authoredColumn: 'gender', occurrenceId: 'base', label: 'Gender',
        logicalType: 'string', sourceResourceType: 'Patient', sourcePath: 'gender', projectionMode: 'VALUE',
        lossless: true, lossReasons: [] },
      trace: { status: 'VALUE', rowId, column: 'gender', value: 'female', complete: true, hasMore: false,
        contributions: [{ resourceType: 'Patient', resourceId: 'dev-patient-001', value: 'female' }] },
    },
  };
  assert.notEqual(publicationScopeDigest, authorizationScopeDigest);
  assert.equal(assertCellTraceBinding({ entry, canonicalProject, explorer, generation, qualityReport,
    authorizationScopeDigest: actualAuthorizationScopeDigest, outputIdForTrace: outputId, rowId }), entry.response);
  assert.throws(() => assertCellTraceBinding({ entry, canonicalProject, explorer, generation, qualityReport,
    authorizationScopeDigest: publicationScopeDigest, outputIdForTrace: outputId, rowId }),
  /cell trace response must bind evidence to the current receipt authorization scope/);
  assert.throws(() => authorizationScopeDigestFromBuilder({ catalog: { generation, snapshotToken: 'snapshot-retained' } }, generation),
    /Builder catalog must expose its authorization scope digest/);
});
