const maxSourceOptionsDiagnosticBytes = 512 * 1024;
import { isDeepStrictEqual } from 'node:util';

const maxSourceOptionsDiagnosticEntries = 50;
const nonempty = value => typeof value === 'string' && value.trim().length > 0;
const normalizedSourceLabel = value => String(value ?? '').replace(/\s+/g, ' ').trim();

export const codedPivotProposalRequestMatches = (request, {
  outputId, snapshotToken, sourceChoiceId, missingCellPolicy, categories, stepId,
}) => {
  if (![outputId, snapshotToken, sourceChoiceId, missingCellPolicy].every(nonempty) || !Array.isArray(categories) || categories.length === 0) return false;
  const candidate = request?.candidateConstruction;
  if (request?.outputId !== outputId || request?.snapshotToken !== snapshotToken ||
      !candidate || candidate.version !== 1 || !Array.isArray(candidate.steps) || candidate.steps.length !== 1) return false;
  const step = candidate.steps[0];
  const codedPivot = step?.operation?.kind === 'CODED_PIVOT' ? step.operation.codedPivot : undefined;
  if (!codedPivot || !nonempty(step.id) || request.changedStepId !== step.id ||
      (stepId !== undefined && step.id !== stepId) || codedPivot.constructionId !== step.id ||
      !Array.isArray(step.inputs) || !isDeepStrictEqual(step.inputs, [{ kind: 'SOURCE_PROJECTION' }]) ||
      codedPivot.sourceChoiceId !== sourceChoiceId || codedPivot.missingCellPolicy !== missingCellPolicy ||
      codedPivot.duplicatePolicy !== 'ERROR') return false;

  const actualCategories = codedPivot.categories;
  const outputs = step.outputs;
  if (!Array.isArray(actualCategories) || actualCategories.length !== categories.length ||
      !Array.isArray(outputs) || outputs.length !== categories.length) return false;
  const choiceForm = actualCategories.every(category => nonempty(category?.choiceId));
  const canonicalForm = actualCategories.every(category => !category?.choiceId && nonempty(category?.system) && nonempty(category?.code));
  if (!choiceForm && !canonicalForm) return false;

  const matched = new Set();
  const outputIds = new Set();
  for (const category of actualCategories) {
    const matches = categories.filter(expected => choiceForm
      ? expected.choiceId === category.choiceId
      : expected.system === category.system && expected.code === category.code);
    if (matches.length !== 1) return false;
    const expected = matches[0];
    if (matched.has(expected) || (category.system !== undefined && category.system !== expected.system) ||
        (category.code !== undefined && category.code !== expected.code) ||
        !nonempty(category.outputColumnId) || outputIds.has(category.outputColumnId) ||
        (expected.outputColumnId !== undefined && category.outputColumnId !== expected.outputColumnId)) return false;
    const outputMatches = outputs.filter(output => output?.id === category.outputColumnId);
    if (outputMatches.length !== 1 || outputMatches[0].name !== expected.code || outputMatches[0].label !== expected.label) return false;
    matched.add(expected);
    outputIds.add(category.outputColumnId);
  }
  return matched.size === categories.length && outputIds.size === outputs.length;
};

export const codedPivotRemovalRequestMatches = (request, {
  outputId, snapshotToken, removedStepId, candidateConstruction,
}) => Boolean(
  nonempty(outputId) && nonempty(snapshotToken) && nonempty(removedStepId) &&
  request?.outputId === outputId && request?.snapshotToken === snapshotToken &&
  !Object.hasOwn(request, 'changedStepId') &&
  isDeepStrictEqual(request?.removeStepIds, [removedStepId]) &&
  isDeepStrictEqual(request?.candidateConstruction, candidateConstruction) &&
  Array.isArray(candidateConstruction?.steps) &&
  candidateConstruction.steps.every(step => step?.operation?.kind !== 'CODED_PIVOT')
);

export const codedPivotPersistedSourceBindingsFor = step => {
  const source = step?.operation?.codedPivot?.source;
  return source && typeof source === 'object' && source.family && typeof source.family === 'object' ? source : null;
};

export const codedPivotPersistedSourceBindingsEqual = (leftStep, rightStep) => {
  const left = codedPivotPersistedSourceBindingsFor(leftStep);
  const right = codedPivotPersistedSourceBindingsFor(rightStep);
  return Boolean(left && right && isDeepStrictEqual(left, right));
};

export const codedPivotPersistedSourceMatchesOption = (step, option) => {
  const source = codedPivotPersistedSourceBindingsFor(step);
  const family = source?.family;
  if (!source || !family || !option || !Array.isArray(option.route)) return false;
  const selectedFields = ['bindingId', 'resourceType', 'sourcePath', 'sourceCanonical', 'owningScope', 'keyPath', 'valuePath', 'logicalType'];
  if (selectedFields.some(field => !nonempty(option[field]) || family[field] !== option[field])) return false;
  return nonempty(source.candidateId) && nonempty(source.nodeId) &&
    source.fieldPath === `${option.sourcePath}.${option.valuePath}` &&
    Array.isArray(family.choiceArms) && family.choiceArms.length === 1 && family.choiceArms[0] === option.valuePath &&
    JSON.stringify(source.route) === JSON.stringify(option.route);
};

export const codedPivotSourceOptionsDiagnosticFor = (entry, request, response, {
  origin, project, generation, explorerId, outputId, snapshotToken, mode, domSnapshot,
}) => {
  if (![origin, project, generation, explorerId, outputId, snapshotToken].every(nonempty) || !['integer', 'string'].includes(mode)) {
    throw new TypeError('Coded Pivot source-options evidence needs the exact project, generation, Explorer, output, snapshot, and mode.');
  }
  const endpointPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/frame-source-options`;
  if (entry?.origin !== new URL(origin).origin || entry?.path !== endpointPath || entry?.method !== 'POST' ||
      !Number.isInteger(entry?.status) || entry.status < 200 || entry.status >= 300 || !Number.isFinite(entry?.completedAt)) {
    throw new Error('Coded Pivot source-options evidence must be a completed successful POST from the exact owned endpoint.');
  }
  const expectedRequestKeys = ['limit', 'outputId', 'resourceType', 'snapshotToken'];
  if (!request || typeof request !== 'object' || Array.isArray(request) ||
      JSON.stringify(Object.keys(request).sort()) !== JSON.stringify(expectedRequestKeys) ||
      request.resourceType !== 'Observation' || request.snapshotToken !== snapshotToken || request.outputId !== outputId ||
      request.limit !== 50 || JSON.stringify(entry.body) !== JSON.stringify(request)) {
    throw new Error('Coded Pivot source-options request must bind the exact owned output and catalog snapshot.');
  }
  if (response?.snapshotToken !== snapshotToken || response?.outputId !== outputId ||
      typeof response.complete !== 'boolean' || typeof response.truncated !== 'boolean' || !Array.isArray(response.sources)) {
    throw new Error('Coded Pivot source-options response must echo the exact output and snapshot with its page envelope.');
  }
  if (domSnapshot && (domSnapshot.mode !== mode || !Array.isArray(domSnapshot.sourceControls))) {
    throw new Error('Coded Pivot failure DOM must retain its mode and rendered source controls.');
  }

  const expectedValuePath = mode === 'integer' ? /valueInteger$/i : /valueString$/i;
  const sourceControls = domSnapshot?.sourceControls ?? [];
  const modeLabels = sourceControls.filter(control => control.name === 'coded-pivot-source' &&
    normalizedSourceLabel(control.labelText).toLowerCase().includes('component') &&
    normalizedSourceLabel(control.labelText).toLowerCase().includes(mode));
  const sourceOptions = response.sources;
  const candidateIndexes = sourceOptions.flatMap((option, index) =>
    Array.isArray(option.route) && option.route.length === 0 && option.resourceType === 'Observation' &&
      expectedValuePath.test(option.valuePath ?? '') &&
      normalizedSourceLabel(`${option.title} ${option.description}`).toLowerCase().includes('component') &&
      normalizedSourceLabel(`${option.title} ${option.description}`).toLowerCase().includes(mode) ? [index] : []);
  const matchedIndexes = candidateIndexes.filter(index => modeLabels.some(control =>
    normalizedSourceLabel(control.labelText) === normalizedSourceLabel(`${sourceOptions[index].title} ${sourceOptions[index].description}`)));
  const priorityCandidateIndexes = [...new Set([...matchedIndexes, ...candidateIndexes])].slice(0, maxSourceOptionsDiagnosticEntries);
  const candidateChoices = priorityCandidateIndexes.map(index => sourceOptions[index]);
  const sourceBytes = Buffer.byteLength(JSON.stringify(response), 'utf8');
  const diagnostic = {
    target: {
      project, generation, explorerId, outputId, snapshotToken, mode,
      identitySource: 'created Explorer scope and BuilderV2 catalog generation/snapshot',
      outputSource: 'saved table document output id before coded Pivot chooser',
    },
    endpoint: { origin: new URL(origin).origin, path: endpointPath, method: entry.method, status: entry.status,
      browserRequestId: entry.browserRequestId },
    request: { resourceType: request.resourceType, outputId: request.outputId, snapshotToken: request.snapshotToken, limit: request.limit },
    envelope: {
      snapshotToken: response.snapshotToken,
      outputId: response.outputId,
      complete: response.complete,
      truncated: response.truncated,
      nextCursor: response.nextCursor ?? null,
      requestedLimit: request.limit,
      sourceCount: sourceOptions.length,
      sourceCountExceedsLimit: sourceOptions.length > request.limit,
      sourceCountExceedsDiagnosticEntryCap: sourceOptions.length > maxSourceOptionsDiagnosticEntries,
      responseBytes: sourceBytes,
    },
    domSnapshotCaptured: Boolean(domSnapshot),
    frameOwnership: 'direct Observation frame: empty route, Observation resourceType, mode-specific FHIR valuePath',
    domSourceControlCount: domSnapshot?.sourceControlCount ?? sourceControls.length,
    domSourceControlsTruncated: domSnapshot?.sourceControlsTruncated ?? false,
    domSourceLabels: modeLabels.map(control => normalizedSourceLabel(control.labelText)),
    candidateChoiceCount: candidateIndexes.length,
    matchedChoiceCount: matchedIndexes.length,
    matchedChoiceIndexes: matchedIndexes.flatMap(index => priorityCandidateIndexes.indexOf(index) >= 0 ? [priorityCandidateIndexes.indexOf(index)] : []),
    candidateChoices,
    sourcePreview: [],
    diagnosticTruncated: sourceOptions.length > priorityCandidateIndexes.length,
    diagnosticEntryCap: maxSourceOptionsDiagnosticEntries,
    diagnosticByteCap: maxSourceOptionsDiagnosticBytes,
  };

  const candidateSet = new Set(priorityCandidateIndexes);
  const previewCandidates = sourceOptions.flatMap((option, index) => candidateSet.has(index) ? [] : [{ index, option }]);
  const savedIndexes = new Set(priorityCandidateIndexes);
  for (const { index, option } of previewCandidates) {
    if (savedIndexes.size >= maxSourceOptionsDiagnosticEntries) break;
    diagnostic.sourcePreview.push(option);
    savedIndexes.add(index);
    if (Buffer.byteLength(JSON.stringify(diagnostic), 'utf8') > maxSourceOptionsDiagnosticBytes) {
      diagnostic.sourcePreview.pop();
      savedIndexes.delete(index);
      diagnostic.diagnosticTruncated = true;
      break;
    }
  }
  if (savedIndexes.size < sourceOptions.length) diagnostic.diagnosticTruncated = true;
  diagnostic.diagnosticBytes = 0;
  for (;;) {
    const serializedBytes = Buffer.byteLength(JSON.stringify(diagnostic), 'utf8');
    if (serializedBytes === diagnostic.diagnosticBytes) break;
    diagnostic.diagnosticBytes = serializedBytes;
  }
  const diagnosticBytes = diagnostic.diagnosticBytes;
  if (diagnosticBytes > maxSourceOptionsDiagnosticBytes) {
    throw new RangeError(`Coded Pivot matched source-options evidence alone exceeds ${maxSourceOptionsDiagnosticBytes} bytes.`);
  }
  return diagnostic;
};

export async function codedPivotFirstFailureEvidenceFor({ mode, action, captureDom, captureSourceOptions }) {
  const evidence = { capturedAt: new Date().toISOString(), action };
  if (captureDom) {
    try {
      evidence.dom = await captureDom();
    } catch (error) {
      evidence.domCaptureError = String(error?.stack ?? error);
    }
  } else {
    evidence.domCaptureStatus = 'unavailable';
  }
  if (captureSourceOptions) {
    try {
      evidence.sourceOptions = await captureSourceOptions(evidence.dom);
    } catch (error) {
      evidence.sourceOptionsCaptureError = String(error?.stack ?? error);
    }
  } else {
    evidence.sourceOptionsCaptureStatus = 'unavailable';
  }
  if (evidence.dom && evidence.dom.mode !== mode) evidence.domModeMismatch = { expected: mode, actual: evidence.dom.mode };
  return evidence;
}

export const summarizeCodedPivotNativeRequests = requests => {
  if (!Array.isArray(requests)) throw new TypeError('Coded Pivot request evidence must be an array.');
  const details = requests.map(request => {
    const explicitFailure = typeof request.failure === 'string' && request.failure.length > 0
      ? request.failure
      : typeof request.responseReadError === 'string' && request.responseReadError.length > 0
        ? request.responseReadError
        : undefined;
    const terminal = Number.isFinite(request.completedAt) || Boolean(explicitFailure);
    return {
      browserRequestId: request.browserRequestId,
      requestId: request.requestId,
      path: request.path,
      method: request.method,
      status: request.status,
      terminal,
      ...(explicitFailure ? { failure: explicitFailure } : {}),
      ...(request.expectedCancellation ? { expectedCancellation: request.expectedCancellation } : {}),
    };
  });
  const pending = details.filter(request => !request.terminal);
  const terminalFailures = details.filter(request => request.failure);
  const invalidStatuses = details.filter(request => !Number.isFinite(request.status) || request.status >= 400);
  return {
    total: details.length,
    pending,
    terminalFailures,
    invalidStatuses,
    passed: details.length > 0 && pending.length === 0 && terminalFailures.length === 0 && invalidStatuses.length === 0,
  };
};
