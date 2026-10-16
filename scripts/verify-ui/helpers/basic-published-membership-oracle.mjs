import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const EXPECTED = Object.freeze({
  observations: ['combine-observation-final-1', 'combine-observation-final-2', 'combine-observation-preliminary', 'combine-observation-unmatched'],
  reports: ['combine-observation-final-1', 'combine-observation-final-2', 'combine-observation-preliminary'],
});

const readResource = (fixtureDir, resourceType) => {
  const path = join(fixtureDir, resourceType + '.ndjson');
  const source = readFileSync(path);
  const rows = source.toString('utf8').split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  if (rows.some((row) => row.resourceType !== resourceType || typeof row.id !== 'string' || row.id.length === 0)) {
    throw new Error('Basic published Membership fixture contains an invalid ' + resourceType + ' identity.');
  }
  const ids = rows.map((row) => row.id);
  if (new Set(ids).size !== ids.length) throw new Error('Basic published Membership fixture repeats a ' + resourceType + ' id.');
  return {
    path: 'testdata/verify-combine/' + resourceType + '.ndjson',
    sha256: createHash('sha256').update(source).digest('hex'),
    rows,
    ids: [...ids].sort(),
  };
};

export const loadBasicPublishedMembershipOracle = (fixtureDir) => {
  const observations = readResource(fixtureDir, 'Observation');
  const reports = readResource(fixtureDir, 'DiagnosticReport');
  const reportIDs = new Set(reports.ids);
  const includeIDs = observations.ids.filter((id) => reportIDs.has(id));
  const excludeIDs = observations.ids.filter((id) => !reportIDs.has(id));
  const exactSources = JSON.stringify(observations.ids) === JSON.stringify([...EXPECTED.observations].sort()) &&
    JSON.stringify(reports.ids) === JSON.stringify([...EXPECTED.reports].sort());
  const expectedIncludeIDs = [...EXPECTED.reports].sort();
  const expectedExcludeIDs = ['combine-observation-unmatched'];
  const ok = exactSources && JSON.stringify(includeIDs) === JSON.stringify(expectedIncludeIDs) &&
    JSON.stringify(excludeIDs) === JSON.stringify(expectedExcludeIDs);
  return {
    ok,
    exactSources,
    observations,
    reports,
    includeIDs,
    excludeIDs,
    includeRows: includeIDs.map((id) => [id]),
    excludeRows: excludeIDs.map((id) => [id]),
  };
};
