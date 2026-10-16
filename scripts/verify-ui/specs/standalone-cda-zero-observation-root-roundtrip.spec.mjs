import { expect, test } from '../helpers/cda-fixtures.mjs';
import { relatedOneAllWorkflow } from '../workflows/verify-cda-related-one-all-browser.mjs';
import { fixtureUnavailableSkipReason } from '../helpers/cda-fixture-outcomes.mjs';

test.describe('CDA zero-Observation selected-Patient root roundtrip', () => {
  test.use({
    cdaScenarioID: 'cda-related-one-all-zero-observation',
    cdaCaseName: 'zero-patient-observation-starting-root-roundtrip',
  });

  test('rebases the exact zero-match Patient population through Observation and restores it after reload', async ({ page, cda }) => {
    const result = await relatedOneAllWorkflow({
      page,
      cda,
      mode: 'cda',
      fieldMode: 'id',
      witnessMode: 'zero',
      zeroStartingRootRoundtrip: true,
    });
    const skipReason = fixtureUnavailableSkipReason(result);
    if (skipReason) test.skip(true, skipReason);

    const patientId = result.oracle?.witnessSummary?.patientId;
    const roundtrip = result.zeroObservationStartingRootRoundtrip;
    expect(result).toMatchObject({ project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
    expect(result.oracle?.exactMembershipScope).toMatchObject({
      project: 'loom_dev_cda_fhir',
      generation: 'cda-fhir-v1',
      rootResourceType: 'Patient',
      selectedRootCount: 1,
      incomingObservationEdgeCount: 0,
    });
    expect(patientId).toEqual(expect.any(String));
    expect(roundtrip?.selectionRevisionId).toEqual(expect.any(String));
    expect(roundtrip?.initial).toMatchObject({
      rootResourceType: 'Patient',
      selectionRevisionId: roundtrip?.selectionRevisionId,
      populationRoute: [],
      headers: ['PATIENT ID'],
      previewRowCount: 1,
      rows: [[patientId]],
    });
    expect(roundtrip?.observationRoot).toMatchObject({
      rootResourceType: 'Observation',
      selectionRevisionId: roundtrip?.selectionRevisionId,
      populationRoute: [{ resourceType: 'Patient', relationship: 'subject_Patient' }],
      headers: ['PATIENT ID'],
      previewRowCount: 0,
      rows: [],
    });
    expect(roundtrip?.observationReload).toMatchObject({
      rootResourceType: 'Observation',
      selectionRevisionId: roundtrip?.selectionRevisionId,
      populationRoute: [{ resourceType: 'Patient', relationship: 'subject_Patient' }],
      headers: ['PATIENT ID'],
      previewRowCount: 0,
      rows: [],
    });
    expect(roundtrip?.restoredPatient).toMatchObject({
      rootResourceType: 'Patient',
      selectionRevisionId: roundtrip?.selectionRevisionId,
      populationRoute: [],
      headers: ['PATIENT ID'],
      previewRowCount: 1,
      rows: [[patientId]],
    });
    expect(roundtrip?.restoredReload).toMatchObject({
      rootResourceType: 'Patient',
      selectionRevisionId: roundtrip?.selectionRevisionId,
      populationRoute: [],
      headers: ['PATIENT ID'],
      previewRowCount: 1,
      rows: [[patientId]],
    });
  });
});
