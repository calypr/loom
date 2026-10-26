import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export const BASIC_TEMPORAL_FILTER_CASES = Object.freeze({
  date: Object.freeze({
    resourceType: 'Patient', fieldPath: 'birthDate', inputType: 'date', filterValueKind: 'DATE', fieldLabel: 'Birth Date',
    initial: Object.freeze({ operator: 'GT', inputValue: '1980-01-02', savedValue: { kind: 'DATE', date: '1980-01-02' }, ids: ['dev-patient-002'] }),
    edited: Object.freeze({ operator: 'GTE', inputValue: '1980-01-02', savedValue: { kind: 'DATE', date: '1980-01-02' }, ids: ['dev-patient-001', 'dev-patient-002'] }),
    rows: Object.freeze([
      Object.freeze({ id: 'dev-patient-001', value: '1980-01-02' }),
      Object.freeze({ id: 'dev-patient-002', value: '1985-03-04' }),
    ]),
  }),
  'date-time': Object.freeze({
    resourceType: 'Observation', fieldPath: 'effectiveDateTime', inputType: 'datetime-local', filterValueKind: 'DATE_TIME', fieldLabel: 'Effective Date Time',
    initial: Object.freeze({ operator: 'GT', boundaryInstant: '2026-01-02T00:00:00.000Z', requestValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' }, savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }, ids: [] }),
    edited: Object.freeze({ operator: 'GTE', boundaryInstant: '2026-01-02T00:00:00.000Z', requestValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }, savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }, ids: ['dev-observation-001', 'dev-observation-002', 'dev-observation-003'] }),
    rows: Object.freeze([
      Object.freeze({ id: 'dev-observation-001', value: '2026-01-02T00:00:00Z' }),
      Object.freeze({ id: 'dev-observation-002', value: '2026-01-02T00:00:00Z' }),
      Object.freeze({ id: 'dev-observation-003', value: '2026-01-02T00:00:00Z' }),
    ]),
  }),
});

export const matchesBasicTemporalChoiceProposal = (observed, expected) =>
  observed?.method === 'POST' && observed.origin === expected.origin && observed.path === expected.path &&
  observed.body?.outputId === expected.outputId &&
  Array.isArray(observed.body.constructionChoices) &&
  observed.body.constructionChoices.some(choice => choice.form === 'VALUE' && choice.title === expected.fieldLabel);

export const matchesBasicTemporalFilterCandidate = (candidate, expected) => {
  if (candidate?.outputId !== expected.outputId) return false;
  const sameValue = (value) => value?.kind === expected.savedValue.kind &&
    (value.kind === 'DATE'
      ? value.date === expected.savedValue.date
      : value.kind === 'DATE_TIME' && value.dateTime === expected.savedValue.dateTime);
  return candidate.candidateConstruction?.steps?.some(step => {
    const filter = step.operation?.kind === 'FILTER' ? step.operation.filter : undefined;
    return filter?.columnId === expected.columnId && filter.operator === expected.operator &&
      Array.isArray(filter.values) && filter.values.length === 1 && sameValue(filter.values[0]);
  }) ?? false;
};

export const readBasicTemporalFilterOracle = ({ fixtureDir, caseName }) => {
  const contract = BASIC_TEMPORAL_FILTER_CASES[caseName];
  assert(contract, `Unsupported Basic temporal filter case ${JSON.stringify(caseName)}.`);
  const path = join(fixtureDir, `${contract.resourceType}.ndjson`);
  const bytes = readFileSync(path);
  const records = bytes.toString('utf8').split(/\r?\n/).filter(line => line.trim()).map((line, index) => {
    try { return JSON.parse(line); }
    catch (error) { throw new Error(`Invalid ${contract.resourceType}.ndjson JSON at line ${index + 1}: ${error.message}`); }
  });
  const rows = records.filter(record => record?.resourceType === contract.resourceType)
    .map(record => ({ id: record.id, value: record[contract.fieldPath] }))
    .sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(rows, contract.rows, `${contract.resourceType}.${contract.fieldPath} must match the independent literal fixture oracle.`);
  return { ...contract, rows, ids: rows.map(row => row.id), sha256: createHash('sha256').update(bytes).digest('hex'), path };
};
