import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';

const evidencePath = process.argv[2];
assert(evidencePath, 'Pivot browser evidence path is required');
const evidence = JSON.parse(await readFile(evidencePath, 'utf8'));
const preview = evidence.proposalBody?.preview;
assert.equal(preview?.partialValidation, true);
assert.equal(preview.rows.length, 25);
const idColumn = preview.columns.find(column => column.label === 'Specimen ID')?.column;
assert(idColumn, 'Specimen ID column is missing');
const labels = new Map(preview.columns.map(column => [column.column, column.label]));
const ids = preview.rows.map(row => row[idColumn]);
assert.equal(new Set(ids).size, ids.length, 'preview contains repeated Specimen IDs');

const builder = await fetch('http://127.0.0.1:30002/api/v1/projects/loom_dev_cda_fhir/explorers/cda-builder-full-qa-1790440983382/authoring/v2/builder').then(response => response.json());
const query = `FOR specimen IN Specimen
  FILTER specimen.project == ${JSON.stringify('loom_dev_cda_fhir')}
  FILTER specimen.dataset_generation == ${JSON.stringify(builder.catalog.generation)}
  FILTER specimen.id IN ${JSON.stringify(ids)}
  RETURN {id: specimen.id, category: specimen.payload.collection.bodySite.reference.reference, value: specimen.payload.subject.reference}`;
const code = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`;
const output = execFileSync('rtk', [
  'docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh',
  '--server.database', 'loom_dev', '--javascript.execute-string', code,
], { encoding: 'utf8' });
const source = new Map(JSON.parse(output.slice(output.indexOf('\n') + 1)).map(row => [row.id, row]));
assert.equal(source.size, ids.length, 'source records missing for preview IDs');

for (const row of preview.rows) {
  const original = source.get(row[idColumn]);
  assert(original, `source Specimen ${row[idColumn]} is missing`);
  const values = Object.entries(row).filter(([key, value]) => key !== idColumn && value !== null);
  assert.equal(values.length, 1, `expected one populated Pivot cell for ${row[idColumn]}`);
  const [column, value] = values[0];
  assert.equal(labels.get(column), original.category ?? 'Null', `category differs for ${row[idColumn]}`);
  assert.equal(value, original.value, `subject differs for ${row[idColumn]}`);
}
console.log(JSON.stringify({ evidencePath, checkedRows: ids.length, checkedCells: ids.length, sourceGeneration: builder.catalog.generation }));
