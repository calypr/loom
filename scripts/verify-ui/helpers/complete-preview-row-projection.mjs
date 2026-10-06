import assert from 'node:assert/strict';

export function expectedPreviewColumns(construction, sourceColumns = []) {
  const steps = construction?.steps ?? [];
  const finalOutputs = steps.at(-1)?.outputs ?? [];
  if (steps.length > 0) {
    assert(finalOutputs.length > 0, 'A non-empty construction must define its final output schema');
  }
  const columns = steps.length > 0
    ? finalOutputs.map(output => ({ column: output.name, label: output.label }))
    : sourceColumns.map(column => ({ column: column.column, label: column.label }));
  assert(columns.length > 0, 'A complete preview must have an authored output schema');
  for (const [index, column] of columns.entries()) {
    assert.equal(typeof column.column, 'string', `Expected preview column ${index} needs a stable physical key`);
    assert(column.column.length > 0, `Expected preview column ${index} needs a non-empty physical key`);
    assert.equal(typeof column.label, 'string', `Expected preview column ${index} needs an authored label`);
    assert(column.label.length > 0, `Expected preview column ${index} needs a non-empty authored label`);
  }
  assert.equal(new Set(columns.map(column => column.column)).size, columns.length,
    'The authored preview schema must not repeat a physical column key');
  return columns;
}

export function assertCompletePreviewRows(preview, expectedColumns, expectedRows, label) {
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
    `${label} requires a complete API preview with columns and rows`);
  const actualColumns = preview.columns.map(column => ({ column: column.column, label: column.label }));
  assert.deepEqual(actualColumns, expectedColumns,
    `${label} API preview must preserve the exact authored output key order and labels`);

  const expectedKeys = expectedColumns.map(column => column.column);
  const allowedKeys = new Set(['__loom_row_id', ...expectedKeys]);
  const rowIDs = [];
  const projectedRows = preview.rows.map((row, rowIndex) => {
    assert(row && typeof row === 'object' && !Array.isArray(row), `${label} API row ${rowIndex} must be an object`);
    for (const key of allowedKeys) assert(Object.hasOwn(row, key), `${label} API row ${rowIndex} is missing authored key ${key}`);
    assert.deepEqual(Object.keys(row).sort(), [...allowedKeys].sort(),
      `${label} API row ${rowIndex} must contain only the internal row identity and exact authored columns`);
    assert.equal(typeof row.__loom_row_id, 'string', `${label} API row ${rowIndex} must retain its internal identity`);
    assert(row.__loom_row_id.length > 0, `${label} API row ${rowIndex} must retain a non-empty internal identity`);
    rowIDs.push(row.__loom_row_id);
    return expectedKeys.map(key => row[key]);
  });
  assert.equal(new Set(rowIDs).size, rowIDs.length, `${label} API row identities must be unique`);
  const multiset = rows => rows.map(row => JSON.stringify(row)).sort();
  assert.deepEqual(multiset(projectedRows), multiset(expectedRows),
    `${label} API row values must match the duplicate-sensitive independent oracle`);
  return { columns: actualColumns, projectedRows, rowIDs };
}
