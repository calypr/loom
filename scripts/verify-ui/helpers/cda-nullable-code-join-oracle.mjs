import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';

const nonempty = value => typeof value === 'string' && value.trim().length > 0;

export const frozenCdaNullableValueQuantityCodeWitness = Object.freeze({
  artifact: {
    path: '/private/tmp/loom-native-nullable-cda-valuequantity-code-join-witness-20261006-final.json',
    sha256: 'ae9a395ed38e6ae601bb9ad36d3668574a89e4fdb2aa9c93139aadccb178380b',
  },
  catalogArtifact: {
    path: '/private/tmp/loom-native-nullable-cda-valuequantity-code-catalog-20261006.json',
    sha256: '3c0bda1cb9b4473d7da6b915bbad4e176cddeaf9f12593e29c6d3dd67125c1ba',
  },
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  left: [
    { _id: 'Observation/g_0001ca9610a40e30f2d300f59812a5c60b0005b48cb536963f34078387467c0b', id: 'd9143e11-8d1e-5b43-8957-d433ba2ef6c2', code: 'd' },
    { _id: 'Observation/g_000221e2bab99dc6e54d4ff0f4074fe15b251804fcbc91bb796689d44aa6e166', id: '7928b626-b6ad-54d2-8f61-0fd201d2a48d', code: 'd' },
    { _id: 'Observation/g_00002bde584b7d0d363ce5595bd1cc7f2abfbe104957c328c2ffc5d7eb15b4c7', id: '52f5e622-0181-5feb-936c-e9992ab26616', code: null },
  ],
  right: [
    { _id: 'Observation/g_0004ddc5df5561883f930a243497df837faca00531497bb66e78681e9c1c471f', id: '2103771b-4af2-5eb3-b1b9-6f74d766da86', code: 'd' },
    { _id: 'Observation/g_000524939539d74b230e4969865cff29a0b6ddea9fd6c45593585ed06e6f80ed', id: 'bfe79f40-5134-53d0-bcc7-9ec6d0843646', code: 'd' },
    { _id: 'Observation/g_00005e873383a29c3283f8b4e768ceb746474dd19041546de3553e97e52764be', id: '485e2567-b566-56f3-b5bd-5f025f37cd95', code: null },
  ],
});

export const cdaNullableValueQuantityCodeCandidateEvidence = (builder, generation) => {
  const catalog = builder?.catalog;
  const roots = (catalog?.nodes ?? []).filter(node =>
    node.resourceType === 'Observation' && node.rowRootEligible === true);
  const root = roots.length === 1 ? roots[0] : null;
  const candidates = (catalog?.candidates ?? []).filter(candidate =>
    candidate.nodeId === root?.nodeId && candidate.fieldPath === 'valueQuantity.code');
  const candidate = candidates.length === 1 ? candidates[0] : null;
  const checks = {
    exactGeneration: catalog?.generation === generation,
    uniqueEligibleObservationRoot: roots.length === 1 && nonempty(root?.nodeId),
    uniqueCodeCandidate: candidates.length === 1 && nonempty(candidate?.candidateId),
    logicalTypeString: candidate?.logicalType === 'string',
    cardinalityOptionalOne: candidate?.cardinality === 'optional_one',
    notRepeated: candidate?.repeated === false,
  };
  return { ok: Object.values(checks).every(Boolean), checks, generation: catalog?.generation ?? null,
    rootNodeId: root?.nodeId ?? null, candidate: candidate ?? null };
};

export const cdaNullableNewExplorerIdentityEvidence = ({ created, builder, project, explorer, title,
  builderURL, expectedAPIOrigin, expectedBuilderPath, generation }) => {
  const checks = {
    createdProject: created?.project === project,
    createdExplorerID: created?.explorerId === explorer,
    createdTitle: created?.title === title,
    exactProjectRoute: (() => {
      try {
        const actual = new URL(builderURL);
        return actual.origin === new URL(expectedAPIOrigin).origin && actual.pathname === expectedBuilderPath &&
          actual.search === '' && actual.hash === '';
      } catch {
        return false;
      }
    })(),
    newLifecycle: builder?.lifecycleState === 'NEW',
    nullInitialWorkspace: builder?.workspace === null,
    emptyInitialDraftCAS: builder?.draftVersion === 0 && builder?.draftDigest === '',
    exactCatalogGeneration: builder?.catalog?.generation === generation,
    catalogSnapshotBound: nonempty(builder?.catalog?.snapshotToken),
    authorizationScopeBound: nonempty(builder?.catalog?.authorizationScopeDigest),
  };
  return {
    ok: Object.values(checks).every(Boolean),
    checks,
    expected: { project, explorerId: explorer, title, generation, builderURL: builderURL ?? null },
    actual: {
      createdProject: created?.project ?? null,
      createdExplorerId: created?.explorerId ?? null,
      createdTitle: created?.title ?? null,
      builderURL: builderURL ?? null,
      lifecycleState: builder?.lifecycleState ?? null,
      workspaceIsNull: builder?.workspace === null,
      draftVersion: builder?.draftVersion ?? null,
      draftDigest: builder?.draftDigest ?? null,
      generation: builder?.catalog?.generation ?? null,
      snapshotToken: builder?.catalog?.snapshotToken ?? null,
      authorizationScopeDigest: builder?.catalog?.authorizationScopeDigest ?? null,
    },
  };
};

export const cdaNullableCodeJoinDirectSourceEvidence = ({ inputs, expectedOutputIDs, expectedSelectionRevisionIDs, sourceDocuments }) => {
  const expectedIDs = Array.isArray(expectedOutputIDs) ? expectedOutputIDs : [];
  const expectedSelections = Array.isArray(expectedSelectionRevisionIDs) ? expectedSelectionRevisionIDs : [];
  const sourceRows = Array.isArray(sourceDocuments) ? sourceDocuments : [];
  const expectedInputs = expectedIDs.map(outputId => ({ kind: 'WORKSPACE_OUTPUT', outputId }));
  const distinctExpectedOutputs = expectedIDs.length === 2 && expectedIDs.every(nonempty) && new Set(expectedIDs).size === 2;
  const exactExpectedSelections = expectedSelections.length === expectedIDs.length && expectedSelections.every(nonempty) &&
    new Set(expectedSelections).size === expectedSelections.length;
  const exactInputRefs = distinctExpectedOutputs && isDeepStrictEqual(inputs, expectedInputs);
  const currentDraftOnlyInputs = Array.isArray(inputs) && inputs.length === expectedInputs.length && inputs.every(input =>
    input?.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId);
  const sources = expectedIDs.map((outputId, index) => {
    const matches = sourceRows.filter(document => document?.output?.id === outputId);
    const document = matches.length === 1 ? matches[0] : null;
    const steps = document?.construction?.steps ?? [];
    return {
      outputId,
      documentMatchCount: matches.length,
      rootResourceType: document?.rootResourceType ?? null,
      directObservationRoot: document?.rootResourceType === 'Observation',
      selectionRevisionId: document?.population?.selectionRevisionId ?? null,
      expectedSelectionRevisionId: expectedSelections[index] ?? null,
      exactSelectionRevision: nonempty(expectedSelections[index]) &&
        document?.population?.selectionRevisionId === expectedSelections[index],
      directPopulation: nonempty(document?.population?.selectionRevisionId) &&
        (document?.population?.route?.length ?? 0) === 0,
      constructionSteps: steps,
      noGroupOrPivotConstruction: steps.length === 0,
    };
  });
  const documentsUnique = sources.every(source => source.documentMatchCount === 1);
  const directObservationSources = sources.every(source => source.directObservationRoot && source.directPopulation &&
    source.noGroupOrPivotConstruction && source.exactSelectionRevision);
  const ok = distinctExpectedOutputs && exactExpectedSelections && exactInputRefs && currentDraftOnlyInputs &&
    documentsUnique && directObservationSources;
  return {
    ok,
    checks: { distinctExpectedOutputs, exactExpectedSelections, exactInputRefs, currentDraftOnlyInputs,
      documentsUnique, directObservationSources },
    expectedInputs,
    expectedSelectionRevisionIDs: expectedSelections,
    observedInputs: Array.isArray(inputs) ? inputs : null,
    sources,
  };
};

export const cdaNullableEmptyRemovalPreviewEvidence = ({ proposal, outputId, dom }) => {
  const response = proposal?.responseBody;
  const preview = response?.preview;
  const proposalId = response?.proposalId;
  const readyResponse = response?.previewStatus === 'READY';
  const receiptBound = nonempty(proposalId) && preview?.receiptId === proposalId &&
    dom?.proposalId === proposalId && dom?.previewReceiptId === proposalId &&
    dom?.resultReceiptId === proposalId && dom?.resultProposalId === proposalId;
  const outputBound = nonempty(outputId) && response?.outputId === outputId &&
    preview?.outputId === outputId && dom?.previewOutputId === outputId && dom?.resultOutputId === outputId;
  const emptyResponse = preview?.rowCount === 0 && Array.isArray(preview?.columns) && preview.columns.length === 0 &&
    Array.isArray(preview?.rows) && preview.rows.length === 0;
  const readyDOM = dom?.proposalPanelCount === 1 && dom?.resultSectionCount === 1 && dom?.proposalPreviewCount === 1 &&
    dom?.proposalStatus === 'ready' && dom?.previewStatus === 'ready' && dom?.resultStatus === 'ready';
  const emptyStatus = dom?.statusText === 'This table has no visible columns.';
  const tableAbsent = dom?.tableCount === 0;
  const checks = { readyResponse, receiptBound, outputBound, emptyResponse, readyDOM, emptyStatus, tableAbsent };
  return {
    ok: Object.values(checks).every(Boolean),
    checks,
    proposalId: proposalId ?? null,
    outputId: outputId ?? null,
    responsePreview: preview ? { columns: preview.columns, rows: preview.rows, rowCount: preview.rowCount,
      receiptId: preview.receiptId, outputId: preview.outputId } : null,
    dom: dom ?? null,
  };
};

export const cdaNullableValueQuantityCodeJoinOracle = ({ leftRows, rightRows, project, generation }) => {
  assert(nonempty(project) && nonempty(generation), 'Exact project and generation are required.');
  assert(Array.isArray(leftRows) && leftRows.length === 3, 'Exactly three left Observation rows are required.');
  assert(Array.isArray(rightRows) && rightRows.length === 3, 'Exactly three right Observation rows are required.');
  const validateSide = (rows, label) => {
    const ids = new Set();
    for (const row of rows) {
      assert.equal(row.project, project, `${label} project`);
      assert.equal(row.generation, generation, `${label} generation`);
      assert.equal(row.resourceType, 'Observation', `${label} resource type`);
      assert(nonempty(row._id), `${label} Arango key`);
      assert(nonempty(row.id), `${label} FHIR id`);
      assert(!ids.has(row.id), `${label} repeats FHIR id ${row.id}.`);
      ids.add(row.id);
      assert.equal(typeof row.valueQuantityCodePresent, 'boolean', `${label} code presence`);
      if (row.valueQuantityCodePresent) assert.equal(typeof row.valueQuantityCode, 'string');
      else assert.equal(row.valueQuantityCode, null, `${label} absent code is represented as no value.`);
    }
  };
  validateSide(leftRows, 'Left');
  validateSide(rightRows, 'Right');
  const leftIDs = new Set(leftRows.map(row => row.id));
  assert(rightRows.every(row => !leftIDs.has(row.id)), 'Left and right selections must be disjoint.');
  const leftMatches = leftRows.filter(row => row.valueQuantityCodePresent && row.valueQuantityCode === 'd');
  const rightMatches = rightRows.filter(row => row.valueQuantityCodePresent && row.valueQuantityCode === 'd');
  const leftMissing = leftRows.filter(row => !row.valueQuantityCodePresent);
  const rightMissing = rightRows.filter(row => !row.valueQuantityCodePresent);
  assert.equal(leftMatches.length, 2, 'The left selection must contain two d keys.');
  assert.equal(rightMatches.length, 2, 'The right selection must contain two d keys.');
  assert.equal(leftMissing.length, 1, 'The left selection must contain one missing code.');
  assert.equal(rightMissing.length, 1, 'The right selection must contain one missing code.');
  assert.equal(leftRows.length, leftMatches.length + leftMissing.length);
  assert.equal(rightRows.length, rightMatches.length + rightMissing.length);

  const innerRows = leftMatches.flatMap(left => rightMatches.map(right =>
    [left.id, left.valueQuantityCode, right.id, right.valueQuantityCode]));
  const leftRowsWithNullPadding = [
    ...innerRows,
    [leftMissing[0].id, null, null, null],
  ];
  return {
    leftIDs: leftRows.map(row => row.id), rightIDs: rightRows.map(row => row.id),
    leftMissingIDs: leftMissing.map(row => row.id), rightMissingIDs: rightMissing.map(row => row.id),
    innerRows, leftRows: leftRowsWithNullPadding,
    evidence: { matchedKey: 'd', leftMultiplicity: leftMatches.length, rightMultiplicity: rightMatches.length,
      innerCartesianPairs: innerRows.length, leftMissingCodeRows: leftMissing.length,
      rightMissingCodeRows: rightMissing.length, missingCodeEqualityMatches: 0,
      explicitNullSemanticsClaimed: false },
  };
};
