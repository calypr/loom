import { isDeepStrictEqual } from 'node:util';

const rootOccurrenceId = 'base';
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);

export function matchesRootRebaseAssessment(entry, expected) {
  const request = entry?.body;
  const response = entry?.response;
  const proposal = response?.proposal;
  if (!isRecord(expected) || !expected.outputId || !expected.snapshotToken ||
    !Number.isInteger(expected.draftVersion) || !expected.draftDigest ||
    !expected.currentRootResourceType || !expected.candidateRootResourceType || !expected.rootOccurrenceId) return false;
  return entry?.path?.endsWith('/row-change') === true && entry.method === 'POST' && entry.status === 200 &&
    request?.outputId === expected.outputId && request?.snapshotToken === expected.snapshotToken &&
    request?.draftVersion === expected.draftVersion && request?.draftDigest === expected.draftDigest &&
    response?.snapshotToken === expected.snapshotToken && response?.draftVersion === expected.draftVersion &&
    response?.draftDigest === expected.draftDigest &&
    response?.currentRootResourceType === expected.currentRootResourceType &&
    response?.candidateRootResourceType === expected.candidateRootResourceType &&
    typeof request.rootNodeId === 'string' && request.rootNodeId.length > 0 && request.rootOccurrenceId === expected.rootOccurrenceId &&
    (response.status === 'BLOCKED' ? proposal === undefined :
      response.status === 'READY' && proposal?.outputId === expected.outputId &&
      proposal.rootNodeId === request.rootNodeId && proposal.rootOccurrenceId === expected.rootOccurrenceId);
}

function directRouteNodes(route) {
  if (!isRecord(route) || !Array.isArray(route.children) || route.children.length !== 1 ||
    !isRecord(route.children[0]) ||
    (route.children[0].children !== undefined &&
      (!Array.isArray(route.children[0].children) || route.children[0].children.length !== 0))) return null;
  return [route, route.children[0]];
}

function expectedColumns(columns, selectedOccurrenceId) {
  if (!Array.isArray(columns)) return null;
  return columns.map(column => {
    if (!isRecord(column)) return null;
    const occurrenceId = column.occurrenceId === rootOccurrenceId
      ? selectedOccurrenceId
      : column.occurrenceId === selectedOccurrenceId ? rootOccurrenceId : column.occurrenceId;
    return { ...structuredClone(column), occurrenceId };
  });
}

function expectedPopulation(beforePopulation, path, choicesByOccurrence, edgesById) {
  if (beforePopulation == null) return beforePopulation;
  if (!isRecord(beforePopulation) || !Array.isArray(beforePopulation.route) ||
    typeof beforePopulation.selectionRevisionId !== 'string' || !beforePopulation.selectionRevisionId) return undefined;

  const prefix = [];
  for (let index = path.length - 2; index >= 0; index -= 1) {
    const choice = choicesByOccurrence.get(path[index].occurrenceId);
    const edge = choice && edgesById.get(choice.edgeId);
    if (!edge) return undefined;
    const step = {
      resourceType: path[index].resourceType,
      relationship: edge.label,
      catalogEdgeId: edge.edgeId,
    };
    if (typeof edge.storageDirection === 'string' && edge.storageDirection) {
      step.storageDirection = edge.storageDirection;
    }
    prefix.push(step);
  }
  return {
    ...structuredClone(beforePopulation),
    route: [...prefix, ...structuredClone(beforePopulation.route)],
  };
}

function validRebasedRoute(beforePath, afterRoute, selectedOccurrenceId, afterRoot, beforeRoot, choicesByOccurrence, edgesById) {
  const afterPath = directRouteNodes(afterRoute);
  if (!afterPath || beforePath.length !== 2 ||
    afterRoute?.occurrenceId !== rootOccurrenceId || afterRoute?.resourceType !== afterRoot ||
    afterPath[0].occurrenceId !== rootOccurrenceId || afterPath[0].resourceType !== afterRoot ||
    afterPath[1].occurrenceId !== selectedOccurrenceId || afterPath[1].resourceType !== beforeRoot) return false;

  const choice = choicesByOccurrence.get(beforePath[0].occurrenceId);
  const edge = choice && edgesById.get(choice.edgeId);
  return Boolean(edge && afterPath[1].catalogEdgeId === edge.edgeId &&
    afterPath[1].relationship === edge.label && afterPath[1].matchMode === beforePath[1].matchMode);
}

function isFHIRIDColumn(column, occurrenceId, label) {
  return column?.occurrenceId === occurrenceId && column?.label === label &&
    column?.source?.kind === 'field' && column?.source?.field?.path === 'id';
}

function constructionOutputsForColumns(columns) {
  if (!Array.isArray(columns)) return null;
  return columns.map(column => {
    if (!isRecord(column) || !column.columnId || !column.column || !column.label || !column.logicalType) return null;
    return { id: column.columnId, name: column.column, label: column.label, type: column.logicalType };
  });
}

export function rootRebaseColumnChoiceIdentity(requests, {
  fromIndex = 0,
  outputId,
  snapshotToken,
  draftVersion,
  draftDigest,
} = {}) {
  if (!Array.isArray(requests) || !Number.isInteger(fromIndex) || fromIndex < 0 ||
    typeof outputId !== 'string' || !outputId || typeof snapshotToken !== 'string' || !snapshotToken ||
    !Number.isInteger(draftVersion) || typeof draftDigest !== 'string' || !draftDigest) {
    throw new TypeError('Root-rebase column Apply requires its request window, output, snapshot, and draft identity.');
  }
  const proposals = requests.slice(fromIndex).filter(entry =>
    entry?.path?.endsWith('/construction-choice-proposals') && entry.method === 'POST' &&
    entry.body?.outputId === outputId && entry.body?.snapshotToken === snapshotToken &&
    entry.body?.expectedDraftVersion === draftVersion && entry.body?.expectedDraftDigest === draftDigest);
  if (proposals.length !== 1) {
    throw new Error(`Expected one current Observation column-choice proposal, found ${proposals.length}.`);
  }
  const [proposal] = proposals;
  if (proposal.status !== 200 || !Number.isFinite(proposal.completedAt) || proposal.failure ||
    typeof proposal.body.commandId !== 'string' || !proposal.body.commandId ||
    !Number.isInteger(proposal.body.expectedDraftVersion) ||
    typeof proposal.body.expectedDraftDigest !== 'string' || !proposal.body.expectedDraftDigest) {
    throw new Error('Observation column-choice proposal did not complete successfully with its current draft identity.');
  }
  const choices = proposal.body.constructionChoices;
  if (!Array.isArray(choices) || choices.length !== 1 || !isRecord(choices[0]) ||
    typeof choices[0].choiceId !== 'string' || !choices[0].choiceId ||
    typeof choices[0].form !== 'string' || !choices[0].form) {
    throw new Error('Observation column-choice proposal must contain exactly one native choice.');
  }
  const selected = choices[0];
  const constructionChoice = Object.fromEntries(
    ['choiceId', 'form', 'frameId', 'rowValuePolicy']
      .filter(key => Object.hasOwn(selected, key))
      .map(key => [key, selected[key]]),
  );
  return {
    commandId: proposal.body.commandId,
    outputId,
    snapshotToken,
    expectedDraftVersion: proposal.body.expectedDraftVersion,
    expectedDraftDigest: proposal.body.expectedDraftDigest,
    command: {
      type: 'APPLY_CONSTRUCTION_CHOICE',
      outputId,
      constructionChoice,
      ...(typeof selected.title === 'string' ? { title: selected.title } : {}),
    },
  };
}

function matchesRootRebaseColumnApply(entry, expected) {
  return entry?.path?.endsWith('/commands') === true && entry.method === 'POST' &&
    entry.body?.commandId === expected.commandId && entry.body?.snapshotToken === expected.snapshotToken &&
    entry.body?.expectedDraftVersion === expected.expectedDraftVersion &&
    entry.body?.expectedDraftDigest === expected.expectedDraftDigest &&
    isDeepStrictEqual(entry.body?.commands, [expected.command]);
}

function assertCompletedRootRebaseRequest(entry, label, errors) {
  if (!entry || !Number.isFinite(entry.completedAt) || entry.status !== 200 || entry.failure ||
    errors.some(error => error?.browserRequestId === entry.browserRequestId ||
      (entry.requestId && error?.requestId === entry.requestId))) {
    throw new Error(`${label} did not complete successfully: ${JSON.stringify({
      path: entry?.path,
      status: entry?.status,
      failure: entry?.failure,
      completedAt: entry?.completedAt,
    })}`);
  }
}

export async function waitForRootRebaseColumnApply({
  requests,
  errors = [],
  fromIndex,
  schemaFromIndex,
  expected,
  schemaIdentity,
  deadlineAt,
  waitForRequest,
  closeCatalog,
  waitForPairedRows,
  now = Date.now,
} = {}) {
  if (!Array.isArray(requests) || !Array.isArray(errors) ||
    !Number.isInteger(fromIndex) || fromIndex < 0 ||
    !Number.isInteger(schemaFromIndex) || schemaFromIndex < 0 ||
    !isRecord(expected) || !isRecord(schemaIdentity) ||
    typeof schemaIdentity.snapshotToken !== 'string' || !schemaIdentity.snapshotToken ||
    typeof schemaIdentity.nodeId !== 'string' || !schemaIdentity.nodeId ||
    !Number.isFinite(deadlineAt) || typeof waitForRequest !== 'function' ||
    typeof closeCatalog !== 'function' || typeof waitForPairedRows !== 'function' ||
    typeof now !== 'function') {
    throw new TypeError('Root-rebase column Apply requires exact request identities and one shared deadline.');
  }
  const remaining = () => {
    const value = deadlineAt - now();
    if (value <= 0) throw new Error('Timed out waiting for Observation column Apply and field discovery within the 5s action deadline.');
    return value;
  };
  const command = await waitForRequest(
    entry => matchesRootRebaseColumnApply(entry, expected),
    { fromIndex, timeoutMs: remaining() },
  );
  assertCompletedRootRebaseRequest(command, 'Observation column Apply', errors);

  const matchingSchemaRequests = () => requests.slice(schemaFromIndex).filter(entry =>
    entry?.path?.endsWith('/schema-fields') && entry.method === 'POST' &&
    entry.body?.snapshotToken === schemaIdentity.snapshotToken &&
    entry.body?.nodeId === schemaIdentity.nodeId);
  for (const request of matchingSchemaRequests()) {
    if (Number.isFinite(request.completedAt)) {
      assertCompletedRootRebaseRequest(request, 'Observation schema-fields request', errors);
    }
  }

  await closeCatalog(remaining());
  const schemaRequestsAtClose = matchingSchemaRequests();
  const pendingAtClose = schemaRequestsAtClose.filter(entry => !Number.isFinite(entry.completedAt));
  await Promise.all(pendingAtClose.map(async request => {
    const completed = await waitForRequest(
      entry => entry.browserRequestId === request.browserRequestId && Number.isFinite(entry.completedAt),
      { fromIndex: schemaFromIndex, timeoutMs: remaining() },
    );
    assertCompletedRootRebaseRequest(completed, 'Observation schema-fields request', errors);
  }));
  for (const request of schemaRequestsAtClose) {
    assertCompletedRootRebaseRequest(request, 'Observation schema-fields request', errors);
  }

  await waitForPairedRows(remaining());
  for (const request of matchingSchemaRequests()) {
    assertCompletedRootRebaseRequest(request, 'Observation schema-fields request', errors);
  }
  return { command, schemaRequests: schemaRequestsAtClose };
}

export function rootRebasePreservesFilterWhenAddingColumn(beforeDocument, afterDocument) {
  if (!isRecord(beforeDocument) || !isRecord(afterDocument) ||
    beforeDocument.output?.id !== afterDocument.output?.id ||
    !isRecord(beforeDocument.construction) || !isRecord(afterDocument.construction) ||
    beforeDocument.construction.version !== afterDocument.construction.version ||
    !Array.isArray(beforeDocument.construction.steps) || beforeDocument.construction.steps.length !== 1 ||
    !Array.isArray(afterDocument.construction.steps) || afterDocument.construction.steps.length !== 1 ||
    !Array.isArray(beforeDocument.columns) || !Array.isArray(afterDocument.columns) ||
    afterDocument.columns.length !== beforeDocument.columns.length + 1) return false;

  const beforeStep = beforeDocument.construction.steps[0];
  const afterStep = afterDocument.construction.steps[0];
  if (beforeStep?.operation?.kind !== 'FILTER' || afterStep?.operation?.kind !== 'FILTER' ||
    !Array.isArray(beforeStep.outputs) || !Array.isArray(afterStep.outputs)) return false;

  const beforeOutputs = constructionOutputsForColumns(beforeDocument.columns);
  const afterOutputs = constructionOutputsForColumns(afterDocument.columns);
  return beforeOutputs?.every(Boolean) === true && afterOutputs?.every(Boolean) === true &&
    isDeepStrictEqual(beforeStep.id, afterStep.id) &&
    isDeepStrictEqual(beforeStep.inputs, afterStep.inputs) &&
    isDeepStrictEqual(beforeStep.operation, afterStep.operation) &&
    isDeepStrictEqual(beforeStep.outputs, beforeOutputs) &&
    isDeepStrictEqual(afterStep.outputs, [...beforeStep.outputs, afterOutputs.at(-1)]) &&
    isDeepStrictEqual(afterStep.outputs, afterOutputs);
}

export function rootRebasePreservesAuthoredDocument(
  beforeDocument,
  afterDocument,
  { selectedOccurrenceId, routeRebase, catalogEdges } = {},
) {
  if (!isRecord(beforeDocument) || !isRecord(afterDocument) ||
    typeof selectedOccurrenceId !== 'string' || !selectedOccurrenceId || selectedOccurrenceId === rootOccurrenceId ||
    !Array.isArray(routeRebase) || !Array.isArray(catalogEdges) ||
    !beforeDocument.output?.id || !isRecord(beforeDocument.construction) ||
    !Array.isArray(beforeDocument.construction.steps) ||
    !beforeDocument.construction.steps.some(step => step?.operation?.kind === 'FILTER' && isRecord(step.operation.filter)) ||
    !Array.isArray(beforeDocument.columns) || beforeDocument.columns.length === 0 ||
    !Array.isArray(afterDocument.columns) || beforeDocument.columns.length !== afterDocument.columns.length) return false;

  const beforeRoot = beforeDocument.rootResourceType;
  const afterRoot = afterDocument.rootResourceType;
  const beforePath = directRouteNodes(beforeDocument.route);
  if (!beforePath || beforePath[0].occurrenceId !== rootOccurrenceId ||
    beforePath[0].resourceType !== beforeRoot || beforePath.at(-1).resourceType !== afterRoot || beforeRoot === afterRoot) return false;

  const choicesByOccurrence = new Map();
  for (const choice of routeRebase) {
    if (!isRecord(choice) || typeof choice.occurrenceId !== 'string' || typeof choice.edgeId !== 'string' ||
      !choice.edgeId || choicesByOccurrence.has(choice.occurrenceId)) return false;
    choicesByOccurrence.set(choice.occurrenceId, choice);
  }
  if (choicesByOccurrence.size !== beforePath.length - 1 ||
    beforePath.slice(0, -1).some(node => !choicesByOccurrence.has(node.occurrenceId))) return false;

  const edgesById = new Map();
  for (const edge of catalogEdges) {
    if (!isRecord(edge) || typeof edge.edgeId !== 'string' || !edge.edgeId ||
      typeof edge.label !== 'string' || !edge.label || edgesById.has(edge.edgeId)) return false;
    edgesById.set(edge.edgeId, edge);
  }
  if ([...choicesByOccurrence.values()].some(choice => !edgesById.has(choice.edgeId))) return false;

  if (!validRebasedRoute(beforePath, afterDocument.route, selectedOccurrenceId, afterRoot, beforeRoot, choicesByOccurrence, edgesById)) return false;

  const columns = expectedColumns(beforeDocument.columns, selectedOccurrenceId);
  if (!columns || columns.some(column => column === null)) return false;
  if (beforeRoot === 'Patient' && afterRoot === 'Observation') {
    const patientIDColumns = beforeDocument.columns.filter(column => isFHIRIDColumn(column, rootOccurrenceId, 'Patient ID'));
    if (patientIDColumns.length !== 1 || columns[beforeDocument.columns.indexOf(patientIDColumns[0])]?.occurrenceId !== selectedOccurrenceId) return false;
  }
  if (beforeRoot === 'Observation' && afterRoot === 'Patient') {
    const patientIDColumns = beforeDocument.columns.filter(column => isFHIRIDColumn(column, selectedOccurrenceId, 'Patient ID'));
    if (patientIDColumns.length !== 1 || columns[beforeDocument.columns.indexOf(patientIDColumns[0])]?.occurrenceId !== rootOccurrenceId) return false;
  }

  const expectedDocument = structuredClone(beforeDocument);
  expectedDocument.rootResourceType = afterRoot;
  expectedDocument.columns = columns;
  if (Object.hasOwn(beforeDocument, 'population')) {
    const population = expectedPopulation(beforeDocument.population, beforePath, choicesByOccurrence, edgesById);
    if (beforeDocument.population != null && population === undefined) return false;
    expectedDocument.population = population;
  }
  return isDeepStrictEqual(expectedDocument.output, afterDocument.output) &&
    isDeepStrictEqual(expectedDocument.construction, afterDocument.construction) &&
    isDeepStrictEqual(expectedDocument.rows, afterDocument.rows) &&
    isDeepStrictEqual(expectedDocument.columns, afterDocument.columns) &&
    isDeepStrictEqual(expectedDocument.population, afterDocument.population) &&
    isDeepStrictEqual(afterDocument, { ...expectedDocument, route: afterDocument.route });
}
