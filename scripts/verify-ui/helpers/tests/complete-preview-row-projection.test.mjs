import assert from 'node:assert/strict';
import test from 'node:test';
import retainedWave120 from './fixtures/wave120-upstream-proposal-preview.json' with { type: 'json' };
import { assertCompletePreviewRows, expectedPreviewColumns } from '../complete-preview-row-projection.mjs';

const construction = { steps: [{ outputs: [
  { name: 'root_id', label: 'FHIR resource ID' },
  { name: 'related_patient_id', label: 'Patient FHIR resource ID' },
] }] };
const columns = expectedPreviewColumns(construction);
const rows = [
  ['Specimen/1', 'Patient/7'],
  ['Specimen/1', 'Patient/7'],
  ['Specimen/2', 'Patient/8'],
];
const preview = () => ({
  columns: columns.map(column => ({ column: column.column, label: column.label })),
  rows: rows.map((values, index) => ({
    __loom_row_id: `row-${index}`,
    root_id: values[0],
    related_patient_id: values[1],
  })),
});

test('projects API row objects through the exact saved output schema and preserves duplicate values', () => {
  const result = assertCompletePreviewRows(preview(), columns, rows, 'focused edit');
  assert.deepEqual(result.projectedRows, rows);
  assert.deepEqual(result.rowIDs, ['row-0', 'row-1', 'row-2']);
  assert.deepEqual(expectedPreviewColumns({ steps: [] }, [
    { column: 'root_id', label: 'FHIR resource ID' },
  ]), [{ column: 'root_id', label: 'FHIR resource ID' }], 'zero-step schema comes from authored source columns');
  assert.throws(() => expectedPreviewColumns({ steps: [{ outputs: [] }] }, [
    { column: 'root_id', label: 'FHIR resource ID' },
  ]), /non-empty construction must define its final output schema/,
  'a malformed non-empty construction must not fall back to the source schema');
});

test('rejects any API column order, label, omission, or addition that differs from the authored output schema', () => {
  const reordered = preview();
  reordered.columns.reverse();
  assert.throws(() => assertCompletePreviewRows(reordered, columns, rows, 'reordered'), /exact authored output key order/);

  const renamed = preview();
  renamed.columns[1].label = 'Wrong label';
  assert.throws(() => assertCompletePreviewRows(renamed, columns, rows, 'renamed'), /exact authored output key order/);

  const missing = preview();
  missing.columns.pop();
  assert.throws(() => assertCompletePreviewRows(missing, columns, rows, 'missing'), /exact authored output key order/);

  const extra = preview();
  extra.columns.push({ column: 'unexpected', label: 'Unexpected visible field' });
  assert.throws(() => assertCompletePreviewRows(extra, columns, rows, 'extra'), /exact authored output key order/);
});

test('rejects missing or extra API row keys, duplicate identities, and value multiplicity drift', () => {
  const missing = preview();
  delete missing.rows[0].related_patient_id;
  assert.throws(() => assertCompletePreviewRows(missing, columns, rows, 'missing row field'), /missing authored key/);

  const extra = preview();
  extra.rows[0].unexpected = 'must fail';
  assert.throws(() => assertCompletePreviewRows(extra, columns, rows, 'extra row field'), /only the internal row identity/);

  const duplicateIdentity = preview();
  duplicateIdentity.rows[1].__loom_row_id = duplicateIdentity.rows[0].__loom_row_id;
  assert.throws(() => assertCompletePreviewRows(duplicateIdentity, columns, rows, 'duplicate identity'), /identities must be unique/);

  const wrongMultiplicity = preview();
  wrongMultiplicity.rows[2].related_patient_id = 'Patient/7';
  assert.throws(() => assertCompletePreviewRows(wrongMultiplicity, columns, rows, 'wrong multiset'), /duplicate-sensitive independent oracle/);
});

test('replays retained wave120 proposal objects against its independent raw-oracle rows', () => {
  assert.equal(retainedWave120.evidence.runReportSha256, '8810e4ab21401b90ee507a857bf59427f8e8f33d62caf02fbd3c526637d027aa');
  assert.equal(retainedWave120.evidence.errorContextSha256, '1aa8b15ee5b911dd6135679cf01c709a9666b4fcb154b10d03227476944e0697');
  const expectedColumns = expectedPreviewColumns(retainedWave120.candidateConstruction);
  const result = assertCompletePreviewRows(retainedWave120.preview, expectedColumns,
    retainedWave120.expectedRows, 'retained wave120 upstream proposal');
  assert.equal(retainedWave120.preview.rowCount, 10);
  assert.equal(result.projectedRows.length, 10);
  assert.equal(result.rowIDs.length, 10);
});
