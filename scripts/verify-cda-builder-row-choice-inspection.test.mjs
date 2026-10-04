import assert from 'node:assert/strict';
import { test } from 'node:test';
import { rowChoiceCases, runRowChoiceInspection, summarizeRowChoiceDialog } from './verify-cda-builder-row-choice-inspection.mjs';

test('bounded row choice inspection only accepts the three migrated cases', () => {
  assert.deepEqual(rowChoiceCases, {
    'Inspect bounded Patient row choices': 'Patient',
    'Inspect bounded BodyStructure row choices': 'BodyStructure',
    'Inspect bounded Observation row choices': 'Observation',
  });
  assert.throws(() => summarizeRowChoiceDialog('', [], 'Patient'), /no choices/);
  assert.throws(() => summarizeRowChoiceDialog('Choose', [{ label: 'One row', value: 'records', selected: false }], 'Patient'), /no selected choice/);
  assert.throws(() => summarizeRowChoiceDialog('Choose', [{ label: '', value: '', selected: true }], 'Patient'), /visible label and a value/);
});

test('row choice inspection fails closed without an explicit isolated CDA target', async () => {
  await assert.rejects(runRowChoiceInspection({
    action: 'Inspect bounded Patient row choices',
    explorerId: 'example-explorer',
    env: {},
  }), /Set an explicit isolated CDA target: LOOM_CDA_SOURCE_ROOT/);
});

test('row choice inspection rejects cases outside its migrated subset before target discovery', async () => {
  await assert.rejects(runRowChoiceInspection({
    action: 'Verify bounded Observation row definition',
    explorerId: 'example-explorer',
    env: {},
  }), /Unsupported bounded row-choice case/);
});
