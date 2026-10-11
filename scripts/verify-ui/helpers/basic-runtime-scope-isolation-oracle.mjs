import assert from 'node:assert/strict';
import { join } from 'node:path';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readNDJSONResourceIdentityOracle } from './ndjson-resource-oracle.mjs';

export function inspectRuntimeEnvelopeIdentity(envelope, { project, explorer }) {
  const expectedProject = canonicalProjectID(project);
  const projectMatches = envelope?.project === expectedProject;
  const explorerMatches = envelope?.explorerId === explorer;
  return {
    expectedProject,
    projectMatches,
    explorerMatches,
    matches: projectMatches && explorerMatches,
  };
}

export async function readBasicRuntimeScopeIsolationOracle({
  primaryFixtureDir,
  activeGenerationFixtureDir,
  foreignProjectFixtureDir,
  primaryProject,
  primaryGeneration,
  activeGeneration,
  foreignProject,
}) {
  for (const [name, value] of Object.entries({ primaryFixtureDir, activeGenerationFixtureDir,
    foreignProjectFixtureDir, primaryProject, primaryGeneration, activeGeneration, foreignProject })) {
    assert.equal(typeof value, 'string');
    assert(value.length > 0, `${name} is required`);
  }
  assert.notEqual(primaryProject, foreignProject, 'the foreign scope must use another project');
  assert.notEqual(primaryGeneration, activeGeneration, 'the active output scope must use another generation');

  const [primary, selected, foreign] = await Promise.all([
    readNDJSONResourceIdentityOracle({
      path: join(primaryFixtureDir, 'Patient.ndjson'), project: primaryProject,
      generation: primaryGeneration, resourceType: 'Patient',
    }),
    readNDJSONResourceIdentityOracle({
      path: join(activeGenerationFixtureDir, 'Patient.ndjson'), project: primaryProject,
      generation: activeGeneration, resourceType: 'Patient',
    }),
    readNDJSONResourceIdentityOracle({
      path: join(foreignProjectFixtureDir, 'Patient.ndjson'), project: foreignProject,
      generation: activeGeneration, resourceType: 'Patient',
    }),
  ]);

  assert.deepEqual([...primary.ids].sort(), ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(selected.ids, ['scope-active-patient-001']);
  assert.deepEqual(foreign.ids, ['scope-foreign-patient-001']);
  const allIDs = [...primary.ids, ...selected.ids, ...foreign.ids];
  assert.equal(new Set(allIDs).size, allIDs.length, 'all scope witness identities must be distinct');
  return {
    primary: { project: primaryProject, generation: primaryGeneration, ...primary },
    selected: { project: primaryProject, generation: activeGeneration, ...selected },
    foreign: { project: foreignProject, generation: activeGeneration, ...foreign },
  };
}

export function assertVisibleRowsMatchRuntimeScope({ rows, selectedIDs, excludedIDs }) {
  assert(Array.isArray(rows), 'Visible result rows must be an array');
  assert(Array.isArray(selectedIDs) && selectedIDs.length > 0, 'Selected-scope source identities are required');
  assert(Array.isArray(excludedIDs) && excludedIDs.length > 0, 'Foreign-scope identities are required');
  assert.equal(new Set([...selectedIDs, ...excludedIDs]).size, selectedIDs.length + excludedIDs.length,
    'selected and excluded scope identities must be disjoint');
  assert.equal(rows.length, selectedIDs.length, 'Viewer must render exactly the selected scope row count');

  const scopeIDs = [...selectedIDs, ...excludedIDs];
  const actualIDs = rows.map((row, index) => {
    const text = String(row);
    const matchingIDs = scopeIDs.filter(id => text.includes(id));
    assert.equal(matchingIDs.length, 1, `Visible row ${index + 1} must identify exactly one scoped Patient`);
    assert(selectedIDs.includes(matchingIDs[0]), `Visible row ${index + 1} leaked a foreign project or generation identity`);
    return matchingIDs[0];
  });
  assert.deepEqual(actualIDs, [...selectedIDs], 'Viewer rows must equal the exact independent selected-generation identity order');
  return actualIDs;
}
