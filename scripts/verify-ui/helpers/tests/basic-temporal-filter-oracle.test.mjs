import assert from 'node:assert/strict';
import { test } from 'node:test';
import { resolve } from 'node:path';
import {
  BASIC_TEMPORAL_FILTER_CASES,
  matchesBasicTemporalChoiceProposal,
  matchesBasicTemporalFilterCandidate,
  readBasicTemporalFilterOracle,
} from '../basic-temporal-filter-oracle.mjs';

const fixtureDir = resolve('testdata/basic-temporal-filter');

test('Basic DATE oracle keeps literal Patient IDs and boundary expectations', () => {
  const oracle = readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date' });
  assert.equal(oracle.inputType, 'date');
  assert.equal(oracle.filterValueKind, 'DATE');
  assert.deepEqual(oracle.rows, [
    { id: 'dev-patient-001', value: '1980-01-02' },
    { id: 'dev-patient-002', value: '1985-03-04' },
  ]);
  assert.deepEqual(oracle.initial.ids, ['dev-patient-002']);
  assert.deepEqual(oracle.edited.ids, ['dev-patient-001', 'dev-patient-002']);
});

test('Basic DATE_TIME oracle keeps literal Observation identities at the UTC boundary', () => {
  const oracle = readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date-time' });
  assert.equal(oracle.inputType, 'datetime-local');
  assert.equal(oracle.filterValueKind, 'DATE_TIME');
  assert.deepEqual(oracle.rows, [
    { id: 'dev-observation-001', value: '2026-01-02T00:00:00Z' },
    { id: 'dev-observation-002', value: '2026-01-02T00:00:00Z' },
    { id: 'dev-observation-003', value: '2026-01-02T00:00:00Z' },
  ]);
  assert.deepEqual(oracle.initial.ids, []);
  assert.deepEqual(oracle.edited.ids, ['dev-observation-001', 'dev-observation-002', 'dev-observation-003']);
});

test('DATE_TIME choice waiter matches the retained exact field-choice label and route', () => {
  const retained = {
    method: 'POST',
    origin: 'http://127.0.0.1:30000',
    path: '/api/v1/projects/loom_dev_verify_mvofxlvb%252F77c51e0/explorers/verify-vb-77c51e0-observation-effectivedatetime-filter/authoring/v2/construction-choice-proposals',
    body: {
      outputId: 'out_b006ea2c73719a765236acd3',
      constructionChoices: [{ form: 'VALUE', title: 'Effective Date Time' }],
    },
  };
  const contract = BASIC_TEMPORAL_FILTER_CASES['date-time'];
  const expected = {
    origin: retained.origin,
    path: retained.path,
    outputId: retained.body.outputId,
    fieldLabel: contract.fieldLabel,
  };
  assert.equal(contract.fieldLabel, 'Effective Date Time');
  assert.equal(matchesBasicTemporalChoiceProposal(retained, expected), true);
  assert.equal(matchesBasicTemporalChoiceProposal(retained, { ...expected, fieldLabel: 'Effective DateTime' }), false,
    'The old concatenated spelling must not claim the retained native response.');
  assert.equal(matchesBasicTemporalChoiceProposal(retained, { ...expected, path: '/foreign/authoring/v2/construction-choice-proposals' }), false,
    'A response from a different route must not satisfy the choice waiter.');
  assert.equal(matchesBasicTemporalChoiceProposal(retained, { ...expected, outputId: 'other-output' }), false,
    'A response for another output must not satisfy the choice waiter.');
});

test('typed temporal request and saved values retain their exact stage-specific literals', () => {
  assert.deepEqual(BASIC_TEMPORAL_FILTER_CASES.date.initial.savedValue, { kind: 'DATE', date: '1980-01-02' });
  const dateTime = BASIC_TEMPORAL_FILTER_CASES['date-time'];
  assert.deepEqual(dateTime.initial.requestValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' });
  assert.deepEqual(dateTime.initial.savedValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  assert.deepEqual(dateTime.edited.requestValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  assert.deepEqual(dateTime.edited.savedValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
});

test('candidate matching rejects a ready proposal for an older temporal predicate', () => {
  const expected = {
    outputId: 'patients', columnId: 'birth-date', operator: 'GTE',
    savedValue: { kind: 'DATE', date: '1980-01-02' },
  };
  const candidateConstruction = filter => ({ steps: [{ operation: { kind: 'FILTER', filter } }] });
  const current = { outputId: 'patients', candidateConstruction: candidateConstruction({
    columnId: 'birth-date', operator: 'GTE', values: [{ kind: 'DATE', date: '1980-01-02' }],
  }) };
  assert.equal(matchesBasicTemporalFilterCandidate(current, expected), true);
  assert.equal(matchesBasicTemporalFilterCandidate({ ...current, outputId: 'observations' }, expected), false);
  assert.equal(matchesBasicTemporalFilterCandidate({ outputId: 'patients', candidateConstruction: candidateConstruction({
    columnId: 'birth-date', operator: 'GT', values: [{ kind: 'DATE', date: '1980-01-02' }],
  }) }, expected), false);
  assert.equal(matchesBasicTemporalFilterCandidate({ outputId: 'patients', candidateConstruction: candidateConstruction({
    columnId: 'birth-date', operator: 'GTE', values: [{ kind: 'DATE', date: '1985-03-04' }],
  }) }, expected), false);

  const dateTimeExpected = {
    outputId: 'observations', columnId: 'effective-at', operator: 'GT',
    savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' },
  };
  const dateTimeResponse = { outputId: 'observations', candidateConstruction: candidateConstruction({
    columnId: 'effective-at', operator: 'GT',
    values: [{ kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }],
  }) };
  assert.equal(matchesBasicTemporalFilterCandidate(dateTimeResponse, dateTimeExpected), true);
  const dateTimeRequestExpected = { ...dateTimeExpected,
    savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' },
  };
  const dateTimeRequest = { outputId: 'observations', candidateConstruction: candidateConstruction({
    columnId: 'effective-at', operator: 'GT',
    values: [{ kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' }],
  }) };
  assert.equal(matchesBasicTemporalFilterCandidate(dateTimeRequest, dateTimeRequestExpected), true);
  assert.equal(matchesBasicTemporalFilterCandidate(dateTimeRequest, dateTimeExpected), false);
  const editedExpected = { ...dateTimeExpected, operator: 'GTE' };
  const editedRequest = { outputId: 'observations', candidateConstruction: candidateConstruction({
    columnId: 'effective-at', operator: 'GTE', values: [{ kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }],
  }) };
  assert.equal(matchesBasicTemporalFilterCandidate(editedRequest, editedExpected), true);
  assert.equal(matchesBasicTemporalFilterCandidate({ ...editedRequest, candidateConstruction: candidateConstruction({
    columnId: 'effective-at', operator: 'GTE', values: [{ kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' }],
  }) }, editedExpected), false);
  assert.equal(matchesBasicTemporalFilterCandidate({ ...dateTimeResponse, candidateConstruction: candidateConstruction({
    columnId: 'effective-at', operator: 'GT',
    values: [{ kind: 'DATE_TIME', dateTime: '2026-01-01T16:00:00.000Z' }],
  }) }, dateTimeExpected), false);
  assert.equal(matchesBasicTemporalFilterCandidate({ ...dateTimeResponse, candidateConstruction: candidateConstruction({
    columnId: 'effective-at', operator: 'GT',
    values: [{ kind: 'DATE', date: '2026-01-02' }],
  }) }, dateTimeExpected), false);
});
