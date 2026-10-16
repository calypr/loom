import assert from 'node:assert/strict';
import { readNDJSONResourceIdentityOracle } from './ndjson-resource-oracle.mjs';

const expectedPatientIDs = ['dev-patient-001', 'dev-patient-002'];

export async function readBasicPatientPopulationOracle({ path, project, generation }) {
  const source = await readNDJSONResourceIdentityOracle({ path, project, generation, resourceType: 'Patient' });
  assert.equal(source.count, expectedPatientIDs.length, 'Basic Patient fixture must contain exactly two Patients');
  assert.deepEqual([...source.ids].sort(), expectedPatientIDs,
    'Basic Patient fixture must contain the exact independent Patient identities');
  return {
    scope: { project, generation },
    resourceType: 'Patient',
    count: source.count,
    ids: source.ids,
    sha256: source.sha256,
  };
}
