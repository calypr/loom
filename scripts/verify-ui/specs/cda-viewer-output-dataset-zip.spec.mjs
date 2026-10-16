import { expect, test } from '../helpers/cda-fixtures.mjs';
import {
  CdaPatientDatasetZipWitnessUnavailable,
} from '../helpers/cda-viewer-output-dataset-zip-oracle.mjs';
import {
  cdaViewerObservationOutputDatasetZipWorkflow,
  cdaViewerOutputDatasetZipWorkflow,
} from '../workflows/cda-viewer-output-dataset-zip.mjs';

test.describe('CDA Viewer Patient dataset ZIP', () => {
  test.use({
    cdaScenarioID: 'cda-viewer-output-dataset-zip',
    cdaCaseName: 'published-patient-dataset-zip',
    cdaRequireClickhouse: true,
    cdaRequireSourceFixture: true,
  });

  test('downloads exact raw CDA Patient IDs from the published Viewer as an identity-bound ZIP', async ({ page, cda }, testInfo) => {
    test.setTimeout(300_000);
    try {
      await cdaViewerOutputDatasetZipWorkflow({ page, cda }, testInfo);
    } catch (error) {
      if (!(error instanceof CdaPatientDatasetZipWitnessUnavailable)) throw error;
      test.skip(true, error.message);
    }
    await expect(page).toHaveURL(/mode=viewer/);
  });
});

test.describe('CDA Viewer Observation owner-record dataset ZIP', () => {
  test.use({
    cdaScenarioID: 'cda-viewer-output-dataset-zip',
    cdaCaseName: 'published-observation-repeated-values-zip',
    cdaRequireClickhouse: true,
    cdaRequireSourceFixture: true,
  });

  test('downloads exact repeated CDA Observation values from the published Viewer as a typed JSONL ZIP', async ({ page, cda }, testInfo) => {
    test.setTimeout(300_000);
    await cdaViewerObservationOutputDatasetZipWorkflow({ page, cda }, testInfo);
    await expect(page).toHaveURL(/mode=viewer/);
  });
});
