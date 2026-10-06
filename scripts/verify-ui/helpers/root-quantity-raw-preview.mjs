import assert from 'node:assert/strict';

export const assertBoundedPreviewCount = (preview, rawSourceRows, limit = 25) => {
  assert(Number.isInteger(rawSourceRows) && rawSourceRows > 0, 'Raw source row count must be a positive integer');
  assert(Number.isInteger(limit) && limit > 0, 'Preview limit must be a positive integer');
  const expectedPreviewRows = Math.min(limit, rawSourceRows);
  assert.equal(preview?.rowCount, expectedPreviewRows, 'Preview rowCount must describe bounded rows consumed, not full source cardinality');
  assert.equal(preview?.rows?.length, expectedPreviewRows, 'Preview rows must match the bounded row count');
  return { rawSourceRows, boundedPreviewRows: expectedPreviewRows, limit };
};

export const assertReloadPreviewContext = ({ requests, requestOffset, basePath, outputId, generation, snapshotToken, draftVersion, draftDigest }) => {
  assert(Array.isArray(requests), 'Captured browser authoring requests must be an array');
  assert(Number.isInteger(requestOffset) && requestOffset >= 0 && requestOffset <= requests.length, 'Reload preview request offset must address the captured request list');
  const currentRequests = requests.slice(requestOffset);
  const previewRequest = currentRequests.findLast(entry => entry.endpoint === 'preview'
    && entry.pathname === `${basePath}/preview`
    && entry.body?.outputId === outputId);
  assert(previewRequest, 'The final reload must issue an automatic preview for the exact restored output');
  assert.equal(previewRequest.status, 200, 'The final reload automatic preview must succeed');
  assert.equal(previewRequest.body?.limit, 25, 'The final reload automatic preview must use the bounded 25-row window');
  const preview = previewRequest.response;
  assert(preview && Array.isArray(preview.rows) && Array.isArray(preview.columns), 'The final reload automatic preview must return typed rows and columns');
  assert.equal(previewRequest.body?.receiptId, preview.receiptId, 'The preview response must match the request receipt');
  assert.equal(preview.outputId, outputId, 'The automatic preview must return the exact restored output');
  assert.equal(preview.rowCount, preview.rows.length, 'The automatic preview row count must describe its returned bounded rows');
  assert(preview.rowCount > 0 && preview.rowCount <= previewRequest.body.limit, 'The automatic preview must stay within its bounded request limit');

  const reconciliation = currentRequests.findLast(entry => entry.endpoint === 'reconcile'
    && entry.pathname === `${basePath}/reconcile`
    && entry.response?.receiptId === preview.receiptId);
  assert(reconciliation, 'The automatic preview receipt must come from a reconciliation captured during this reload');
  assert.equal(reconciliation.status, 200, 'The reload reconciliation for the automatic preview must succeed');
  assert.equal(reconciliation.body?.snapshotToken, snapshotToken, 'The reload reconciliation must use the exact current source snapshot');
  assert.equal(reconciliation.body?.draftVersion, draftVersion, 'The reload reconciliation must use the exact current draft version');
  assert.equal(reconciliation.body?.draftDigest, draftDigest, 'The reload reconciliation must use the exact current draft digest');
  assert.equal(reconciliation.response?.snapshotToken, snapshotToken, 'The reload reconciliation response must retain the exact current source snapshot');
  assert.equal(reconciliation.response?.generation, generation, 'The reload reconciliation must retain the exact source generation');
  assert.equal(reconciliation.response?.receiptId, previewRequest.body.receiptId, 'The preview must use the receipt issued for the exact reload reconciliation');
  assert(reconciliation.response?.outputs?.some(output => output.outputId === outputId), 'The reload reconciliation must authorize the exact preview output');
  return { request: previewRequest, reconciliation, preview };
};

const categoryIdentity = row => !row.codePresent
  ? 'MISSING'
  : row.codeValue === null
    ? 'NULL'
    : JSON.stringify({ kind: 'STRING', string: row.codeValue });

export const assertPreviewRowsMatchRawObservations = ({ previewRows, rawRows, idColumn, categoryColumn, valueColumn, rawAggregateGroups = [] }) => {
  assert(Array.isArray(previewRows) && Array.isArray(rawRows), 'Preview and raw Observation rows must be arrays');
  assert.equal(previewRows.length, rawRows.length, 'The bounded preview and raw tuple query must contain the same number of rows');
  const previewIDs = previewRows.map(row => row[idColumn]);
  assert(previewIDs.every(id => typeof id === 'string' && id.length > 0), 'Every bounded preview row must expose its exact Observation ID');
  assert.equal(new Set(previewIDs).size, previewIDs.length, 'The bounded preview must not duplicate Observation IDs');
  const rawByID = new Map();
  for (const row of rawRows) {
    assert.equal(typeof row.id, 'string', `Raw Observation identity must be a string: ${JSON.stringify(row)}`);
    assert.equal(typeof row.status, 'string', `Raw Observation.status must be a string: ${JSON.stringify(row)}`);
    assert.equal(typeof row.codePresent, 'boolean', `Raw quantity.code presence must be boolean: ${JSON.stringify(row)}`);
    assert(!row.codePresent || row.codeValue === null || typeof row.codeValue === 'string', `Raw quantity.code must be NULL or a string: ${JSON.stringify(row)}`);
    if (!row.codePresent) assert.equal(row.codeValue, null, `Missing raw quantity.code must project to null with presence=false: ${JSON.stringify(row)}`);
    assert.equal(typeof row.valuePresent, 'boolean', `Raw quantity.value presence must be boolean: ${JSON.stringify(row)}`);
    assert(!row.valuePresent || Number.isFinite(row.value), `Raw quantity.value must be numeric when present: ${JSON.stringify(row)}`);
    if (!row.valuePresent) assert.equal(row.value, null, `Missing raw quantity.value must project to null with presence=false: ${JSON.stringify(row)}`);
    assert(!rawByID.has(row.id), `Raw tuple query returned duplicate Observation ${row.id}`);
    rawByID.set(row.id, row);
  }
  assert.deepEqual([...rawByID.keys()].sort(), [...previewIDs].sort(), 'Raw tuple query must return the exact bounded preview ID set');
  const sampleCounts = new Map();
  for (const previewRow of previewRows) {
    const id = previewRow[idColumn];
    const raw = rawByID.get(id);
    assert(raw, `Bounded preview Observation ${id} is missing from the independent raw tuple query`);
    assert.equal(previewRow[categoryColumn] ?? null, raw.codePresent ? raw.codeValue : null,
      `Preview quantity.code value differs from raw Observation ${id}`);
    assert.equal(previewRow[valueColumn] ?? null, raw.valuePresent ? raw.value : null,
      `Preview quantity.value differs from raw Observation ${id}`);
    const identity = categoryIdentity(raw);
    const aggregate = rawAggregateGroups.find(group => group.status === raw.status && group.present === raw.codePresent
      && (group.present ? group.value === raw.codeValue : group.value === null));
    if (rawAggregateGroups.length) assert(aggregate, `Raw Observation ${id} is outside the full status/category oracle`);
    const key = `${raw.status}\u0000${identity}`;
    sampleCounts.set(key, (sampleCounts.get(key) ?? 0) + 1);
  }
  if (rawAggregateGroups.length) {
    for (const [key, count] of sampleCounts) {
      const [status, identity] = key.split('\u0000');
      const aggregate = rawAggregateGroups.find(group => group.status === status && categoryIdentity({ codePresent: group.present, codeValue: group.value }) === identity);
      assert(aggregate, `Bounded raw sample bucket ${key} is missing from the full aggregate oracle`);
      assert(count <= aggregate.rowCount, `Bounded raw sample exceeds full source membership for ${key}`);
    }
  }
  return rawRows.map(row => ({ id: row.id, status: row.status, category: categoryIdentity(row), valuePresent: row.valuePresent, value: row.value }));
};
