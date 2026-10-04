import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runRelatedSourceChooser } from './verify-cda-builder-related-source-chooser.mjs';
import { runPatientRelatedInspection } from './verify-cda-builder-related-patient-inspection.mjs';

test('related source chooser requires an explicitly named isolated target before launch', async () => {
  await assert.rejects(
    runRelatedSourceChooser({ explorerId: 'example-explorer', env: {} }),
    error => {
      assert.match(error.message, /LOOM_CDA_SOURCE_ROOT/);
      assert.match(error.message, /LOOM_CDA_COMPOSE_PROJECT/);
      assert.match(error.message, /LOOM_CDA_API_CONTAINER/);
      assert.match(error.message, /LOOM_CDA_PROJECT/);
      return true;
    },
  );
});

test('Patient related inspection also refuses an implicit target before browser launch', async () => {
  await assert.rejects(
    runPatientRelatedInspection({ action: 'Inspect Patient field choice', explorerId: 'example-explorer', env: {} }),
    /Set an explicit isolated CDA target: LOOM_CDA_SOURCE_ROOT/,
  );
});
