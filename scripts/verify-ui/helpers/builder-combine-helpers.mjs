import { isDeepStrictEqual } from 'node:util';

export const isOwnedConstructionCapabilitiesRequest = ({ requestURL, method, uiUrl, project, explorer }) => {
  if (method !== 'POST') return false;
  try {
    const request = new URL(requestURL);
    const ui = new URL(uiUrl);
    const expectedPath = '/api/v1/projects/' + encodeURIComponent(project) +
      '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2/construction-capabilities';
    return request.origin === ui.origin && request.pathname === expectedPath;
  } catch {
    return false;
  }
};

export const rootedEmptyTargetAppliedExpression = (outputId) => {
  const selected = '[data-testid="construction-table-' + outputId + '"]';
  return '(()=>{const selected=document.querySelector(' + JSON.stringify(selected) + ');' +
    'const preview=document.querySelector("[data-testid=\\\"preview-table-scroll\\\"]");' +
    'return Boolean(selected?.getAttribute("aria-current")==="page"&&' +
    '!document.querySelector("[data-testid=\\\"construction-proposal-panel\\\"]")&&' +
    '!document.querySelector("[data-testid=\\\"construction-history\\\"]")&&' +
    '!document.querySelector("[data-testid=\\\"construction-combine-editor\\\"]")&&' +
    'preview?.textContent?.trim()==="Add a column to see your table.")})()';
};

export const rootedEmptyTargetRestorationEvidence = (restored, baseline, target) => {
  const emptyRoot = restored?.output?.id === target?.outputId &&
    restored?.rootResourceType === 'Observation' &&
    restored?.columns?.length === 0 &&
    (restored?.construction?.steps?.length ?? 0) === 0;
  const isExactEmptyConstruction = (construction) => Boolean(
    construction && typeof construction === 'object' &&
    construction.version === 1 && Array.isArray(construction.steps) && construction.steps.length === 0 &&
    Object.keys(construction).length === 2 && Object.hasOwn(construction, 'version') && Object.hasOwn(construction, 'steps')
  );
  const normalizeEmptyConstruction = (document) => {
    const normalized = structuredClone(document);
    if (isExactEmptyConstruction(normalized?.construction)) delete normalized.construction;
    return normalized;
  };
  const normalizedRestored = normalizeEmptyConstruction(restored);
  const normalizedBaseline = normalizeEmptyConstruction(baseline);
  const unchanged = isDeepStrictEqual(normalizedRestored, normalizedBaseline);
  const emptyConstructionNormalized =
    (!Object.hasOwn(restored ?? {}, 'construction') && isExactEmptyConstruction(baseline?.construction)) ||
    (!Object.hasOwn(baseline ?? {}, 'construction') && isExactEmptyConstruction(restored?.construction));
  return { ok: emptyRoot && unchanged, emptyRoot, unchanged, emptyConstructionNormalized };
};

export const findColumn = (revision, resourceType, path) => {
  const canonical = (value) => String(value ?? '').toLowerCase().replace(/^root\./, '').replace(/[^a-z0-9]/g, '');
  const wanted = canonical(path);
  const semanticPath = canonical(resourceType + '.' + path);
  const semanticExact = revision.columns.filter((column) => {
    const semantic = canonical(column.semanticPath);
    return semantic === wanted || semantic === semanticPath;
  });
  const nameExact = revision.columns.filter((column) => canonical(column.name) === wanted);
  const suffixMatch = revision.columns.filter((column) =>
    canonical(column.semanticPath).endsWith(wanted) || canonical(column.name).endsWith(wanted));
  const byLabel = revision.columns.filter((column) => canonical(column.label).includes(wanted));
  const candidates = semanticExact.length ? semanticExact : nameExact.length ? nameExact : suffixMatch.length ? suffixMatch : byLabel;
  if (candidates.length !== 1) {
    throw new Error('Could not identify one published ' + resourceType + '.' + path + ' column: ' +
      JSON.stringify({ outputId: revision.outputId, columns: revision.columns }).slice(0, 1200));
  }
  return candidates[0];
};

export const unwrapNullableClickHouseType = (value) => {
  const type = String(value ?? '');
  const match = /^Nullable\((.*)\)$/.exec(type);
  return match ? match[1] : type;
};

export const isJoinableStringColumn = (column) =>
  ['String', 'Nullable(String)'].includes(column?.clickhouseType) &&
  column.nullable === (column.clickhouseType === 'Nullable(String)') && column.repeated === false;

export const isMembershipStringKeyColumn = (column) =>
  ['String', 'Nullable(String)'].includes(column?.clickhouseType) &&
  column.nullable === (column.clickhouseType === 'Nullable(String)') && column.repeated === false;

export const isMembershipWorkspaceKeyColumn = (column) =>
  column?.logicalType === 'string' &&
  ['required_one', 'optional_one'].includes(column.cardinality) &&
  typeof column.nullable === 'boolean' &&
  column.joinCompatibilityKey === 'String';

export const membershipGroupCapabilityKeyEvidence = (columns, groupKeyOutputColumnId) => {
  const validOutputColumnId = typeof groupKeyOutputColumnId === 'string' && groupKeyOutputColumnId.length > 0;
  const matchingColumns = (columns ?? []).filter((column) => column?.id === groupKeyOutputColumnId);
  const column = matchingColumns.length === 1 ? matchingColumns[0] : undefined;
  return {
    ok: validOutputColumnId && matchingColumns.length === 1 && isMembershipWorkspaceKeyColumn(column),
    exactOutputColumnId: validOutputColumnId && matchingColumns.length === 1,
    matchingColumns,
    column,
    compiledNullable: typeof column?.nullable === 'boolean' ? column.nullable : null,
  };
};

export const membershipOutputNullabilityEvidence = (outputColumn, sourceCompiledNullable) => {
  const hasOutputColumn = Boolean(outputColumn && typeof outputColumn === 'object' && !Array.isArray(outputColumn));
  const outputNullable = hasOutputColumn
    ? Object.hasOwn(outputColumn, 'nullable') ? outputColumn.nullable : false
    : undefined;
  const ok = hasOutputColumn && typeof sourceCompiledNullable === 'boolean' && typeof outputNullable === 'boolean' &&
    outputNullable === sourceCompiledNullable;
  return {
    ok,
    sourceCompiledNullable: typeof sourceCompiledNullable === 'boolean' ? sourceCompiledNullable : null,
    outputNullable: typeof outputNullable === 'boolean' ? outputNullable : null,
  };
};

export const isScalarStringColumn = (column) =>
  ['String', 'Nullable(String)'].includes(column?.clickhouseType) &&
  column.nullable === (column.clickhouseType === 'Nullable(String)') && column.repeated === false;

export const isCombineInputIDColumn = (column, operation) => {
  if (operation === 'APPEND') return isScalarStringColumn(column);
  if (operation === 'KEY_JOIN') return isJoinableStringColumn(column);
  if (operation === 'MEMBERSHIP') return isMembershipStringKeyColumn(column);
  return false;
};

export const isNumericClickHouseType = (value) =>
  /^(?:U?Int|Float|Decimal)/.test(unwrapNullableClickHouseType(value));

export const builderRequestURL = (apiBase, project, explorer) => new URL(
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/builder`,
  apiBase,
);

export const builderResponseIdentity = (builder, apiBase, project, explorer, workspaceTitle, expectedSources) => {
  const requestURL = builderRequestURL(apiBase, project, explorer);
  const expectedPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/builder`;
  const documents = builder?.workspace?.documents ?? [];
  const sourceOutputs = expectedSources.map((expected) => {
    const matches = documents.filter((document) =>
      document.rootResourceType === expected.rootResourceType &&
      document.output?.title === expected.title &&
      (!expected.outputId || document.output?.id === expected.outputId));
    return matches.length === 1 ? matches[0].output.id : null;
  });
  const outputIDs = sourceOutputs.filter(Boolean);
  const bound = requestURL.origin === new URL(apiBase).origin &&
    requestURL.pathname === expectedPath &&
    builder?.workspace?.explorer?.title === workspaceTitle &&
    sourceOutputs.length === expectedSources.length &&
    outputIDs.length === expectedSources.length &&
    new Set(outputIDs).size === expectedSources.length;
  return {
    bound,
    requestURL: requestURL.toString(),
    routePath: requestURL.pathname,
    workspaceTitle: builder?.workspace?.explorer?.title ?? null,
    outputIDs,
  };
};

export const appendEditorConfigurationEvidence = (actualRows, expectedRows) => {
  const actual = Array.isArray(actualRows) ? actualRows : [];
  const expected = Array.isArray(expectedRows) ? expectedRows : [];
  return {
    ok: actual.length === 3 && expected.length === 3 && isDeepStrictEqual(actual, expected),
    rowCount: actual.length,
    expectedRowCount: 3,
    actualRows: actual,
    expectedRows: expected,
  };
};

export const nativeCombineTargetBindingEvidence = ({
  requestBody,
  responseStatus,
  response,
  expectedRootNodeIds,
  expectedRootResourceType,
  previousOutputIds,
  mountedOutputId,
}) => {
  const createCommands = (requestBody?.commands ?? []).filter((command) => command?.type === 'CREATE_TABLE');
  const createdResults = (response?.results ?? []).filter((result) =>
    result?.type === 'TABLE_CREATED' && typeof result.outputId === 'string' && result.outputId.length > 0);
  const outputId = createdResults.length === 1 ? createdResults[0].outputId : null;
  const workspaceDocuments = response?.workspace?.documents ?? [];
  const targetDocuments = outputId
    ? workspaceDocuments.filter((document) => document?.output?.id === outputId)
    : [];
  const target = targetDocuments.length === 1 ? targetDocuments[0] : null;
  const rootNodeId = createCommands.length === 1 ? createCommands[0].rootNodeId : null;
  const commandId = requestBody?.commandId;
  const expectedRoots = new Set(expectedRootNodeIds ?? []);
  const previousOutputs = new Set(previousOutputIds ?? []);
  const commandBound = responseStatus >= 200 && responseStatus < 300 &&
    createCommands.length === 1 && typeof commandId === 'string' && commandId.length > 0 &&
    response?.commandId === commandId && typeof rootNodeId === 'string' && rootNodeId.length > 0 &&
    expectedRoots.has(rootNodeId);
  const resultBound = createdResults.length === 1 && Boolean(outputId) && !previousOutputs.has(outputId);
  const workspaceBound = Boolean(target) && target.rootResourceType === expectedRootResourceType &&
    target.columns?.length === 0 && (target.construction?.steps?.length ?? 0) === 0;
  const editorBound = Boolean(outputId) && mountedOutputId === outputId;
  return {
    ok: commandBound && resultBound && workspaceBound && editorBound,
    commandBound,
    resultBound,
    workspaceBound,
    editorBound,
    responseStatus,
    commandId: commandId ?? null,
    rootNodeId,
    expectedRootNodeIds: [...expectedRoots],
    outputId,
    previousOutputIDs: [...previousOutputs],
    mountedOutputId: mountedOutputId ?? null,
    workspaceTargetCount: targetDocuments.length,
    targetRootResourceType: target?.rootResourceType ?? null,
    targetColumnCount: target?.columns?.length ?? null,
    targetConstructionSteps: target?.construction?.steps?.length ?? 0,
  };
};

export const joinOracleRows = (observations, reports, joinType, observationKey = 'id', reportKey = 'id') => observations.flatMap((observation) => {
  const matches = reports.filter((report) => report[reportKey] === observation[observationKey]);
  const rows = matches.length ? matches : joinType === 'LEFT' ? [null] : [];
  return rows.map((report) => [observation.id, observation.status, report?.id ?? '—', report?.status ?? '—']);
});

export const appendNullPaddingRows = ({ observations, diagnosticReports, patients }) => [
  ...observations.map(({ id, status }) => [id, status, null]),
  ...diagnosticReports.map(({ id, status }) => [id, status, null]),
  ...patients.map(({ id, gender }) => [id, null, gender]),
];

export const displayAppendNullPaddingRows = (rows) => rows.map((row) =>
  row.map((value) => value === null || value === undefined ? '—' : String(value)));

export const constructionProposalPreviewEvidence = ({
  responseStatus,
  response,
  requestBody,
  expectedOutputId,
  expectedColumns,
  expectedRows,
  domProposalId,
  domReceiptId,
}) => {
  const preview = response?.preview;
  const columns = Array.isArray(preview?.columns) ? preview.columns.map((column) => column.column) : [];
  const rawRows = Array.isArray(preview?.rows)
    ? preview.rows.map((row) => columns.map((column) => row?.[column]))
    : null;
  const requestBound = Boolean(requestBody?.candidateConstruction) &&
    response?.outputId === expectedOutputId && requestBody?.outputId === expectedOutputId &&
    response?.snapshotToken === requestBody?.snapshotToken &&
    response?.draftVersion === requestBody?.expectedDraftVersion &&
    response?.draftDigest === requestBody?.expectedDraftDigest &&
    isDeepStrictEqual(response?.candidateConstruction, requestBody.candidateConstruction);
  const proposalID = response?.proposalId;
  const receiptBound = Boolean(proposalID) && preview?.receiptId === proposalID &&
    domProposalId === proposalID && domReceiptId === proposalID;
  const unmatchedRows = [...expectedRows];
  const rowsMatch = Array.isArray(rawRows) && rawRows.length === expectedRows.length &&
    rawRows.every((row) => {
      const index = unmatchedRows.findIndex((expected) => isDeepStrictEqual(row, expected));
      if (index < 0) return false;
      unmatchedRows.splice(index, 1);
      return true;
    });
  const ok = responseStatus === 200 && requestBound && receiptBound &&
    response?.previewStatus === 'READY' && preview?.outputId === expectedOutputId &&
    isDeepStrictEqual(columns, expectedColumns) && preview?.rowCount === expectedRows.length &&
    rawRows?.length === expectedRows.length && rowsMatch;
  return {
    ok,
    responseStatus,
    requestBound,
    receiptBound,
    previewStatus: response?.previewStatus ?? null,
    outputId: preview?.outputId ?? null,
    columns,
    expectedColumns,
    rowCount: preview?.rowCount ?? null,
    rawRows,
    expectedRows,
    nullPositions: rawRows?.flatMap((row, rowIndex) => row.flatMap((value, columnIndex) =>
      value === null ? [[rowIndex, columnIndex]] : [])) ?? null,
  };
};

export const summarizePublishedInputEntries = (entries, expectedOutputIDs) => {
  const expected = new Set(expectedOutputIDs);
  const sourceEntries = entries.filter((entry) => expected.has(entry.outputId));
  return {
    expectedOutputIDs: [...expectedOutputIDs],
    entryCount: entries.length,
    matchingEntries: sourceEntries.map((entry) => ({
      tableId: entry.tableId,
      revisionId: entry.revisionId,
      outputId: entry.outputId,
      isCurrent: entry.isCurrent,
      tableTitle: entry.tableTitle,
      outputTitle: entry.outputTitle,
    })),
    catalogOutputIDs: [...new Set(entries.map((entry) => entry.outputId).filter(Boolean))].slice(0, 50),
  };
};

export const currentPublishedRevisionForOutput = (entries, outputId) => {
  const matches = entries.filter((entry) => entry.outputId === outputId && entry.isCurrent === true);
  if (matches.length !== 1) {
    throw new Error('Expected one current published revision for output ' + outputId + '; found ' + matches.length +
      '; catalog=' + JSON.stringify(summarizePublishedInputEntries(entries, [outputId])));
  }
  return matches[0];
};

export const snapshotSourceDocument = (document) => structuredClone(document);

export const sameSourceDocuments = (left, right) => isDeepStrictEqual(left, right);
