import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const parseNDJSON = path => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map(line => JSON.parse(line));

const compareText = (left, right) => left.localeCompare(right);
const requiredObservationCodeText = 'Basic fixture Observation';

export const readScope001RepeatedPivotOracle = fixtureDir => {
  const patients = parseNDJSON(join(fixtureDir, 'Patient.ndjson'));
  const observations = parseNDJSON(join(fixtureDir, 'Observation.ndjson'));
  const errors = [];
  const expectedIDs = [
    'scope-001-observation-a',
    'scope-001-observation-b',
    'scope-001-observation-c',
  ];
  if (patients.length !== 1 || patients[0]?.id !== 'scope-001-patient-1') {
    errors.push('fixture must contain the one named Patient');
  }
  if (observations.length !== expectedIDs.length ||
      JSON.stringify(observations.map(row => row.id).sort(compareText)) !== JSON.stringify(expectedIDs)) {
    errors.push('fixture must contain exactly the three named Observation roots');
  }
  if (observations.some(row => row.resourceType !== 'Observation' ||
      row.subject?.reference !== 'Patient/scope-001-patient-1')) {
    errors.push('each fixture Observation must link to the named Patient');
  }
  if (observations.some(row => row.code?.text !== requiredObservationCodeText)) {
    errors.push('each fixture Observation must contain the required named top-level code text');
  }

  const expandedRows = observations.flatMap(observation => {
    const components = observation.component;
    if (!Array.isArray(components) || components.length === 0) {
      errors.push(`${observation.id} must have a populated component array`);
      return [];
    }
    return components.map((component, ordinal) => {
      const category = component?.code?.text;
      const value = component?.valueInteger;
      if (typeof category !== 'string' || !category.trim()) {
        errors.push(`${observation.id} component ${ordinal} must have a nonempty category`);
      }
      if (!Number.isInteger(value)) errors.push(`${observation.id} component ${ordinal} must have an integer value`);
      return { id: observation.id, ordinal, category, value };
    });
  }).sort((left, right) => compareText(left.id, right.id) || left.ordinal - right.ordinal);

  const categories = [...new Set(expandedRows.map(row => row.category))].sort(compareText);
  const grouped = new Map();
  for (const row of expandedRows) {
    const key = JSON.stringify([row.id, row.category]);
    grouped.set(key, (grouped.get(key) ?? 0) + row.value);
  }
  const pivotRows = observations.map(observation => {
    const row = { id: observation.id };
    for (const category of categories) {
      row[category] = grouped.get(JSON.stringify([observation.id, category])) ?? null;
    }
    return row;
  }).sort((left, right) => compareText(left.id, right.id));

  assert.deepEqual(errors, [], 'SCOPE-001 fixture shape must be complete and unambiguous');
  return {
    patientIDs: patients.map(patient => patient.id),
    observationIDs: observations.map(observation => observation.id).sort(compareText),
    observationCodeTexts: observations.map(observation => observation.code?.text),
    expandedRows,
    categories,
    pivotRows,
  };
};
