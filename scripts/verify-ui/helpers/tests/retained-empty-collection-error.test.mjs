import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  matchesExpectedEmptyCollectionValidation,
  matchesExpectedEmptyCollectionValidationConsole,
} from '../cda-playwright-requests.mjs';

const retained = JSON.parse(await readFile(
  new URL('./fixtures/retained-empty-collection-error.json', import.meta.url),
  'utf8',
));

const expected = {
  path: '/api/v1/projects/loom_dev_cda_fhir/explorers/cda-repeated-empty-1791173546717/authoring/v2/row-definition-proposals',
  snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7',
  expectedDraftVersion: 8,
  expectedDraftDigest: 'sha256:187463b3b469fede9a09a24b18c3ee3df691953aacc4656aa44a4772aeac5990',
  outputId: 'out_eda2f30cfcf7fde4bf5a783f',
  rowChoiceId: 'rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJFWFBBTkRFRF9TQ09QRSIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6N2U5YTAyNWVjMjU1NTAzM2ZkMTM4YTNjYmY5ZWFlY2ViYjI0Zjg3MGE2YjM3Y2Y1ZTg2ZTAzNTVjODRiNDBmNyIsInNjaGVtYURpZ2VzdCI6IjllOTYwOTM2MzQ3MmIzZGJlODI2MzFjN2VhZjc2YzQzZTNiOWQ3MDkzMGNmNzJiMjA1MmY2ZTVmMzYwYmQ0ZWUiLCJvY2N1cnJlbmNlIjp7Ik9jY3VycmVuY2VJRCI6ImJhc2UiLCJOb2RlSUQiOiJuX2YzOTZjOGNhNzI4ZjQzNDlmYThkMmUzYyIsIlJlc291cmNlVHlwZSI6Ik9ic2VydmF0aW9uIiwiUm91dGUiOltdfSwicGF0aCI6ImNvbXBvbmVudFtdIiwiZmhpclR5cGUiOiJPYnNlcnZhdGlvbkNvbXBvbmVudCIsImNhcmRpbmFsaXR5IjoiTUFOWSIsInNoYXBlIjoiQVJSQVkiLCJyZWZlcmVuY2UiOmZhbHNlfQ.35ceb463a7439df31007447a653125107abce2208530b433aaf1ec3d432a5dcc',
  code: 'EMPTY_COLLECTION_ERROR',
  stage: 'row-definition-proposal',
};

test('retained empty-collection validation matches only its exact native request and response', () => {
  assert.equal(matchesExpectedEmptyCollectionValidation(retained, expected), true);

  const consoleError = {
    kind: 'console',
    location: `${retained.origin}${retained.path}`,
    message: 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)',
  };
  assert.equal(matchesExpectedEmptyCollectionValidationConsole(consoleError, retained, [retained], expected), true);

  const competingDraft = structuredClone(retained);
  competingDraft.body.expectedDraftVersion -= 1;
  assert.equal(matchesExpectedEmptyCollectionValidation(competingDraft, expected), false);
  assert.equal(matchesExpectedEmptyCollectionValidationConsole(
    consoleError, retained, [retained, competingDraft], expected,
  ), false, 'same-path/status console output is ambiguous with a second draft request');

  const mutations = [
    ['draft version', entry => { entry.body.expectedDraftVersion += 1; }],
    ['draft digest', entry => { entry.body.expectedDraftDigest = 'sha256:stale'; }],
    ['empty-collection policy', entry => { entry.body.selection.expanded.emptyCollectionPolicy = 'PRESERVE_PARENT'; }],
    ['output', entry => { entry.body.outputId = 'out-other'; }],
    ['row choice', entry => { entry.body.selection.expanded.rowChoiceId = 'choice-other'; }],
    ['response code', entry => { entry.response.error.code = 'INTERNAL_ERROR'; }],
    ['diagnostic code', entry => { entry.response.error.diagnostic.code = 'OTHER_VALIDATION'; }],
    ['status', entry => { entry.status = 400; }],
  ];

  for (const [field, mutate] of mutations) {
    const unrelated = structuredClone(retained);
    mutate(unrelated);
    assert.equal(matchesExpectedEmptyCollectionValidation(unrelated, expected), false, field);
  }
});
