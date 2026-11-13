import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export const DEVLOOP_OBSERVATION_PATH = fileURLToPath(
  new URL('../../../testdata/devloop-fixture/Observation.ndjson', import.meta.url),
);

export const PIVOT_KEYWORD_NAMES = Object.freeze(['null', 'RETURN', 'Filter']);

const PINNED_OBSERVATION_COUNT = 6;
const PINNED_CATEGORY_SUMS = Object.freeze({
  'Patient/dev-patient-001': 352.5,
  'Patient/dev-patient-002': 68,
  __missing__: 0,
});

const round = (value) => Number(value.toFixed(6));

export function buildPivotKeywordOracle(observationPath = DEVLOOP_OBSERVATION_PATH) {
  const lines = readFileSync(observationPath, 'utf8').trim().split('\n').filter(Boolean);
  const observations = lines.map((line) => JSON.parse(line));
  assert.equal(observations.length, PINNED_OBSERVATION_COUNT,
    'dev-loop Observation fixture row count drifted from the audited oracle');
  const statuses = new Set();
  const categorySums = new Map();
  for (const observation of observations) {
    const status = observation?.status?.value ?? null;
    statuses.add(status);
    const category = observation?.subject?.reference ?? '__missing__';
    const raw = observation?.valueQuantity?.value;
    const value = typeof raw === 'number' ? raw : 0;
    categorySums.set(category, round((categorySums.get(category) ?? 0) + value));
  }
  assert.equal(statuses.size, 1, 'expected exactly one distinct status group in the fixture');
  const sums = Object.fromEntries([...categorySums.entries()].sort(([a], [b]) => (a < b ? -1 : 1)));
  assert.deepEqual(sums, { ...PINNED_CATEGORY_SUMS },
    'fixture per-category sums drifted from the audited oracle');
  return Object.freeze({
    statuses: Object.freeze([...statuses]),
    groupRowCount: 1,
    categorySums: Object.freeze({ ...PINNED_CATEGORY_SUMS }),
    keywordNames: PIVOT_KEYWORD_NAMES,
  });
}
