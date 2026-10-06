import { isDeepStrictEqual } from 'node:util';

export const workspaceOutputRef = (outputId) => ({ kind: 'WORKSPACE_OUTPUT', outputId });

export const workspaceOutputOption = (outputId) => JSON.stringify(['WORKSPACE_OUTPUT', outputId]);


const normalizeConstructionOutputNullability = (construction) => {
  if (!construction || typeof construction !== 'object' || Array.isArray(construction) || !Array.isArray(construction.steps)) {
    return construction;
  }
  return {
    ...construction,
    steps: construction.steps.map((step) => {
      if (!step || typeof step !== 'object' || Array.isArray(step) || !Array.isArray(step.outputs)) return step;
      return {
        ...step,
        outputs: step.outputs.map((output) => {
          if (!output || typeof output !== 'object' || Array.isArray(output) || output.nullable !== false) return output;
          const { nullable: _nullable, ...withoutExplicitDefaultFalse } = output;
          return withoutExplicitDefaultFalse;
        }),
      };
    }),
  };
};

// Go omits false StageColumn.Nullable values on the response wire. Compare the
// complete construction after only that one default-value normalization.
export const constructionCandidateWireEquivalent = (request, response) =>
  isDeepStrictEqual(
    normalizeConstructionOutputNullability(request),
    normalizeConstructionOutputNullability(response),
  );

export const builderDraftStateEvidence = (builder, expectedState = 'draft') => {
  if (!['empty', 'draft'].includes(expectedState)) throw new TypeError('expected Builder draft state must be empty or draft');
  const empty = builder?.workspace === null && builder?.draftVersion === 0 && builder?.draftDigest === '';
  const draft = Boolean(builder?.workspace && Array.isArray(builder.workspace.documents) && builder.workspace.documents.length > 0 &&
    Number.isInteger(builder.draftVersion) && builder.draftVersion > 0 && typeof builder.draftDigest === 'string' && builder.draftDigest.length > 0);
  return {
    ok: expectedState === 'empty' ? empty : draft,
    expectedState,
    actualState: empty ? 'empty' : draft ? 'draft' : 'invalid',
    empty,
    draft,
    workspaceDocumentCount: Array.isArray(builder?.workspace?.documents) ? builder.workspace.documents.length : null,
    draftVersion: builder?.draftVersion ?? null,
    draftDigest: builder?.draftDigest ?? null,
  };
};

export const groupCounts = (rows, key) => {
  const counts = new Map();
  for (const row of rows) {
    const value = row?.[key];
    const identity = value === null || value === undefined ? null : String(value);
    counts.set(identity, (counts.get(identity) ?? 0) + 1);
  }
  return [...counts.entries()]
    .sort(([left], [right]) => String(left).localeCompare(String(right)))
    .map(([value, count]) => [value, count]);
};

export const joinGroupedCounts = (leftRows, rightRows, joinType = 'INNER') => {
  const rightByKey = new Map(rightRows.map(([key, value]) => [String(key), value]));
  return leftRows.flatMap(([key, leftValue]) => {
    if (rightByKey.has(String(key))) return [[String(key), Number(leftValue), String(key), Number(rightByKey.get(String(key)))]];
    return joinType === 'LEFT' ? [[String(key), Number(leftValue), '—', '—']] : [];
  });
};

export const appendGroupedCounts = (groups) => groups.flatMap(({ keyRows, countRows }) =>
  keyRows.map(([key], index) => [String(key), Number(countRows[index]?.[1] ?? 0)]));

export const groupedPivotRows = (rawRows, rowID, category, categories) => {
  const output = new Map();
  for (const row of rawRows) {
    const id = String(row[rowID]);
    const key = String(row[category]);
    const value = output.get(id) ?? new Map();
    value.set(key, (value.get(key) ?? 0) + 1);
    output.set(id, value);
  }
  return [...output.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([id, values]) => [id, ...categories.map((value) => values.get(value) ?? null)]);
};

export const joinPivotRows = (leftRows, rightRows) => {
  const right = new Map(rightRows.map((row) => [row[0], row.slice(1)]));
  return leftRows.flatMap((row) => {
    const match = right.get(row[0]);
    return match ? [[row[0], ...row.slice(1), row[0], ...match]] : [];
  });
};

export const joinGroupPivotRows = (leftRows, rightRows, joinType = 'INNER') => {
  const right = new Map(rightRows.map((row) => [String(row[0]), row]));
  return leftRows.flatMap((row) => {
    const match = right.get(String(row[0]));
    if (match) return [[...row, ...match]];
    return joinType === 'LEFT' ? [[...row, ...Array(rightRows[0]?.length ?? 1).fill(null)]] : [];
  });
};

export const currentDraftSourceEvidence = ({
  inputs,
  expectedOutputIDs,
  sourceDocuments,
  publishedOutputIDs = [],
}) => {
  const actual = (inputs ?? []).map((input) => ({
    kind: input?.kind,
    outputId: input?.outputId,
    tableId: input?.tableId,
    revisionId: input?.revisionId,
  }));
  const expected = expectedOutputIDs.map((outputId) => ({ kind: 'WORKSPACE_OUTPUT', outputId }));
  const docs = new Map((sourceDocuments ?? []).map((document) => [document?.output?.id, document]));
  const stepsAreSaved = expectedOutputIDs.every((outputId) => {
    const document = docs.get(outputId);
    const kinds = document?.construction?.steps?.map((step) => step?.operation?.kind) ?? [];
    return kinds.includes('GROUP') && (!kinds.includes('PIVOT') || kinds.indexOf('GROUP') < kinds.indexOf('PIVOT'));
  });
  const uniqueInputs = new Set(actual.map((input) => `${input.kind}:${input.outputId}`)).size === actual.length;
  const noPublishedSource = expectedOutputIDs.every((outputId) => !publishedOutputIDs.includes(outputId));
  const refsExact = actual.length === expected.length && actual.every((input, index) =>
    input.kind === expected[index]?.kind && input.outputId === expected[index]?.outputId &&
    input.tableId === undefined && input.revisionId === undefined);
  return {
    ok: refsExact && uniqueInputs && stepsAreSaved && noPublishedSource,
    actual,
    expected,
    refsExact,
    uniqueInputs,
    stepsAreSaved,
    noPublishedSource,
  };
};

export const canceledDraftEvidence = (before, after) => {
  const sameWorkspace = isDeepStrictEqual(before?.workspace, after?.workspace);
  const sameCAS = before?.draftVersion === after?.draftVersion && before?.draftDigest === after?.draftDigest;
  const sameScope = before?.catalog?.generation === after?.catalog?.generation &&
    before?.catalog?.authorizationScopeDigest === after?.catalog?.authorizationScopeDigest;
  return {
    ok: sameWorkspace && sameCAS && sameScope,
    sameWorkspace,
    sameCAS,
    sameScope,
    beforeDraftVersion: before?.draftVersion ?? null,
    afterDraftVersion: after?.draftVersion ?? null,
    beforeDraftDigest: before?.draftDigest ?? null,
    afterDraftDigest: after?.draftDigest ?? null,
  };
};

export const sourceRecompileEvidence = ({ before, after, targetOutputId, sourceOutputId, oldReceipt, newReceipt }) => {
  const target = (after?.workspace?.documents ?? []).find((document) => document?.output?.id === targetOutputId);
  const step = target?.construction?.steps?.at(-1);
  const sourceStillBound = step?.inputs?.some((input) => input.kind === 'WORKSPACE_OUTPUT' && input.outputId === sourceOutputId) ?? false;
  const draftAdvanced = after?.draftVersion > before?.draftVersion && after?.draftDigest !== before?.draftDigest;
  const receiptChanged = Boolean(oldReceipt && newReceipt && oldReceipt !== newReceipt);
  return {
    ok: sourceStillBound && draftAdvanced && receiptChanged,
    sourceStillBound,
    draftAdvanced,
    receiptChanged,
    beforeDraftVersion: before?.draftVersion ?? null,
    afterDraftVersion: after?.draftVersion ?? null,
    oldReceipt: oldReceipt ?? null,
    newReceipt: newReceipt ?? null,
  };
};
