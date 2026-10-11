import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { sanitizeReportPayload, sanitizeText } from '../helpers/playwright-browser.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { matchesNativeConstructionRemovalProposal } from '../helpers/cda-playwright-requests.mjs';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';
import { expectedRelatedSourceOneValidation, matchesExpectedRelatedSourceOneValidationCapture, relatedSourceProposalCandidate, selectedRelatedSourceProposal as matchSelectedRelatedSourceProposal } from '../helpers/related-source-capture.mjs';
import { fixtureUnavailableOutcome } from '../helpers/cda-fixture-outcomes.mjs';
import { hasSameQuantityValueMultiset, readBasicRelatedQuantityOneAllOracle } from '../helpers/basic-related-quantity-one-all-oracle.mjs';
import { matchesRootRebaseAssessment } from '../helpers/root-rebase-preservation.mjs';
import {
  choosePatientLinkedObservationOption,
  choosePatientObservationSubjectRestorationButton,
  choosePatientSubjectObservationRepairButton,
  directObservationRootPreservesPatientState,
  matchesPlainPatientObservationAssessment,
} from './verify-cda-plain-patient-root-selector.mjs';
import {
  statusOneAllLifecycleChecks,
  zeroObservationLifecycleChecks,
  zeroObservationSelectionRevisionMatches,
} from '../helpers/related-one-all-lifecycle-contract.mjs';

const ACTION_TIMEOUT_MS = 5_000;
export const relatedOneAllResponsePaths = /builder|population-routes|construction-capabilities|semantic-inventory|construction-choices|related-expand-choices|construction-proposals|commands|reconcile|preview|row-change/;

// Adapted from verify-cda-group-related-values-browser.mjs and
// verify-cda-coded-field-lifecycle-browser.mjs. The raw CDA oracle is kept
// separate from Explorer previews and bounds independent witnesses.
export async function relatedOneAllWorkflow({
  page: nativePage, cda, mode: requestedMode, fieldMode: requestedFieldMode, witnessMode: requestedWitnessMode,
  savedEmptyPolicyEdit: requestedSavedEmptyPolicyEdit, zeroStartingRootRoundtrip = false,
}) {
const mode = requestedMode ?? process.env.LOOM_RELATED_ONE_ALL_MODE ?? 'cda';
assert(['basic', 'cda'].includes(mode), 'LOOM_RELATED_ONE_ALL_MODE must be basic or cda');
const basicMode = mode === 'basic';
const project = cda.project ?? (basicMode ? process.env.LOOM_DEV_PROJECT : undefined);
let browserProject = project;
const generation = cda.generation ?? (basicMode ? process.env.LOOM_DEV_GENERATION : process.env.LOOM_CDA_GENERATION);
const fieldMode = requestedFieldMode ?? process.env.LOOM_RELATED_ONE_ALL_FIELD ?? 'id';
assert(['id', 'status', 'specimen-reference', 'quantity'].includes(fieldMode),
  'LOOM_RELATED_ONE_ALL_FIELD must be id, status, specimen-reference, or quantity');
const witnessMode = requestedWitnessMode ?? process.env.LOOM_RELATED_ONE_ALL_WITNESS ?? 'default';
assert(['default', 'zero'].includes(witnessMode), 'LOOM_RELATED_ONE_ALL_WITNESS must be default or zero');
const zeroObservationMode = witnessMode === 'zero';
const savedEmptyPolicyEdit = requestedSavedEmptyPolicyEdit ?? false;
assert.equal(typeof savedEmptyPolicyEdit, 'boolean', 'savedEmptyPolicyEdit must be a boolean');
assert(!zeroObservationMode || (mode === 'cda' && fieldMode === 'id'),
  'The zero Observation witness mode requires the CDA Observation.id workflow');
assert(!savedEmptyPolicyEdit || zeroObservationMode,
  'The saved RelatedExpand empty-policy edit requires the exact zero Observation witness workflow');
assert(!zeroStartingRootRoundtrip || (zeroObservationMode && !savedEmptyPolicyEdit),
  'The starting-root roundtrip requires the bounded zero Observation witness and standalone policy mode');
const statusFieldMode = fieldMode === 'status';
const referenceFieldMode = fieldMode === 'specimen-reference';
const quantityFieldMode = fieldMode === 'quantity';
assert(!basicMode || statusFieldMode || quantityFieldMode, 'The basic fixture mode is only defined for Observation.status or Observation.valueQuantity.value');
assert(!quantityFieldMode || basicMode,
  'Observation.valueQuantity.value ONE/ALL coverage is currently defined only for the Basic Patient fixture');
const relatedFieldPath = quantityFieldMode ? 'valueQuantity.value' : statusFieldMode ? 'status' : referenceFieldMode ? 'specimen.reference' : 'id';
const relatedOutputLabel = quantityFieldMode ? 'Observation valueQuantity.value' : statusFieldMode ? 'Observation status' : referenceFieldMode ? 'Observation specimen reference' : 'Observation ID';
const relatedChoiceLabel = quantityFieldMode ? 'Value Quantity Value' : statusFieldMode ? 'Status' : referenceFieldMode ? 'Specimen Reference' : relatedOutputLabel;
const logicalTypeExpected = quantityFieldMode ? 'decimal' : 'string';
const cardinalityExpected = 'optional_one';
const referenceWitness = {
  specimenKey: 'Specimen/g_00003b7dca775d32e82a19f3c3c1227d234ca8d3486df068194137e93b1f796b',
  specimenId: 'b7cad184-db67-5542-a975-10fffa3e89e7',
  patientId: 'afcfb15e-7617-5691-ae2c-ab675322fb33',
};
const rootResourceType = basicMode || zeroObservationMode ? 'Patient' : 'Specimen';
const expectedRelatedRouteTypes = basicMode || zeroObservationMode ? ['Patient', 'Observation'] : ['Specimen', 'Patient', 'Observation'];
const expectedPatientObservationRoute = [
  { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
];
const expectedReferenceRoute = [
  { fromResourceType: 'Specimen', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'OUTBOUND' },
  { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
];
const patientObservationRouteLabel = basicMode || zeroObservationMode
  ? `${relatedChoiceLabel}: Patient <-[subject]- Observation`
  : `${relatedChoiceLabel}: Specimen -[subject]-> Patient <-[subject]- Observation`;
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const requestedExplorerName = `related-one-all-${Date.now()}-${randomUUID()}`;
let explorer;
const evidence = cda.evidence;
const apiOrigin = process.env.LOOM_API_ORIGIN ?? cda.apiOrigin;
const uiOrigin = process.env.LOOM_UI_ORIGIN ?? cda.uiOrigin;
const apiBuildContainer = cda.target?.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER ?? process.env.LOOM_CDA_API_CONTAINER;
const arangoContainer = cda.target?.arangoContainer ?? cda.env?.LOOM_CDA_ARANGO_CONTAINER
  ?? process.env.LOOM_CDA_ARANGO_CONTAINER ?? process.env.LOOM_ARANGO_CONTAINER;
const arangoDatabase = cda.target?.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE
  ?? process.env.LOOM_ARANGO_DATABASE ?? (cda.target ? 'loom_dev' : undefined);
const composeProject = cda.target?.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT ?? process.env.LOOM_CDA_COMPOSE_PROJECT;
const explorerCollection = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
let base;
assert(project && generation && apiOrigin && uiOrigin, 'Set the explicit isolated project, generation, API origin, and UI origin.');
assert(apiBuildContainer && arangoContainer && arangoDatabase && composeProject,
  'Set explicit isolated CDA API/Arango containers, Arango database, and Compose project.');
const sourceRoot = cda.target.sourceRoot ?? fileURLToPath(new URL('../../..', import.meta.url));
const report = {
  requestedExplorer: requestedExplorerName, explorer: null, project, generation,
  protectedExplorerUntouched: true, relatedChoiceAssertions: [],
  mode: basicMode ? 'basic-fixture' : 'cda', fieldMode, witnessMode,
  ...(savedEmptyPolicyEdit ? { savedEmptyPolicyEdit: true } : {}),
  scenario: zeroObservationMode
    ? zeroStartingRootRoundtrip
      ? 'Exact selected zero-Observation Patient population changes to an Observation row root, cancels without saving, applies and reloads with zero rows and the Patient ID column, then restores and reloads the exact Patient row.'
      : savedEmptyPolicyEdit
      ? 'Bounded exact project/generation Patient root with an independently reread empty incoming typed Observation.subject_Patient edge set; native ONE/ALL and label lifecycle are followed by a saved RelatedExpand PRESERVE_PARENT-to-EXCLUDE edit, Cancel, Apply/reload, and upstream restoration.'
      : 'Bounded exact project/generation Patient root with an independently reread empty incoming typed Observation.subject_Patient edge set; native ONE/ALL is checked through the current PRESERVE_PARENT expansion and full saved lifecycle.'
    : referenceFieldMode
    ? 'Pinned current-generation Specimen→Patient→Observation witness with 29 distinct nonnull Observation.specimen.reference values and two null/missing references; native grouped-row ONE rejection and same-chooser ALL repair preserve one protocol value per terminal Observation identity.'
    : quantityFieldMode
      ? 'Basic Patient-root numeric Related Source using the exact Observation.valueQuantity.value scalar from the same-code Height family; ONE must reject distinct decimal values, then the same chooser repairs to ALL.'
    : statusFieldMode
      ? basicMode
        ? 'Basic Patient-root fixture with exact Patient→Observation edges, a direct optional_one Observation.status scalar source, and ONE/ALL preview lifecycle.'
        : 'Patient-grouped CDA rows with the direct related Observation.status scalar source; a bounded multi-status witness predicts ONE rejection and same-chooser ALL repair.'
      : 'Patient-grouped CDA rows from one selected Specimen per witness, with related Observation ID values; a many-Observation witness must reproduce grouped-row ONE rejection and same-chooser ALL repair. Zero/one Observation witnesses are additional coverage when available.',
  started: new Date().toISOString(), cases: [], apiCalls: [], errors: [], nativeRequests: [],
  ...(statusFieldMode ? { statusSourceContract: { path: 'Observation.status', logicalType: 'string', cardinality: 'optional_one',
    catalogProjectionMode: 'VALUE', relatedSourceForm: 'ALL',
    note: 'FHIR code primitive is represented as string per Observation; the related source retains all matching records.' } } : {}),
  ...(quantityFieldMode ? { quantitySourceContract: { path: 'Observation.valueQuantity.value', logicalType: 'decimal', cardinality: 'optional_one',
    operation: 'RELATED_SOURCE', codeFamily: { system: 'https://example.test/codes', code: 'height', display: 'Height' },
    onePolicyExpectedError: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', allValuesPreserveNumericType: true,
    note: 'This checks scalar numeric Related Source ONE/ALL behavior; it is not coded Pivot or coded Group coverage.' } } : {}),
  ...(referenceFieldMode ? { referenceSourceContract: { path: 'Observation.specimen.reference', logicalType: 'string', cardinality: 'optional_one',
    nullable: true, route: ['Specimen', 'Patient', 'Observation'], form: 'ALL', terminalIdentity: '_id',
    duplicateValuePolicy: 'Preserve one value per distinct terminal Observation identity; do not collapse by field value.',
    note: 'The pinned oracle has 31 distinct Observation records: 29 distinct nonnull references and two null/missing references, which the exact AQL projection normalizes to null. The native ALL protocol array must retain both normalized null entries.' } } : {}),
};
const fixtureUnavailableError = (reason) => Object.assign(new Error(reason), {
  fixtureUnavailableOutcome: fixtureUnavailableOutcome(reason, report.oracle),
});
await mkdir(evidence, { recursive: true });
report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(sourceRoot);
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };

let officialRequestCapture;
let builder;
let outputId;
let frozenApiBuild;
let apiBuildCheckStarted = false;
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-one-all-${randomUUID()}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.apiCalls.push({ path, status: response.status,
    body: body === undefined ? undefined : sanitizeReportPayload(body),
    response: sanitizeReportPayload(value) });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', arangoDatabase, '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1000)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};
const command = async (commands) => {
  await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(`${base}/builder`);
  return builder;
};
const doc = (state = builder) => state.workspace.documents.find((document) => document.output.id === outputId);
const expectedRelatedSource = () => ({
  outputId,
  candidateId: report.relatedFieldCandidate?.candidateId,
  nodeId: report.relatedFieldCandidate?.nodeId,
  choiceId: report.currentRelatedChoiceId,
  snapshotToken: builder?.catalog?.snapshotToken,
  resourceType: 'Observation',
  path: relatedFieldPath,
  routeTypes: expectedRelatedRouteTypes,
});
const assertStatusCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'status');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.status candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, logicalTypeExpected, 'Observation.status must retain the current compiler-proved string logical type');
    assert.equal(candidate.cardinality, cardinalityExpected, 'Observation.status must retain its compiler-proved scalar cardinality');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.status must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The native catalog must advertise the compiler-proved scalar VALUE projection');
    const choice = candidate.constructionChoice;
    assert(choice, 'Observation.status must carry its nested compiler-proved field choice');
    assert.equal(choice.source?.kind, 'FIELD');
    assert.equal(choice.source?.nodeId, candidate.nodeId);
    assert.equal(choice.source?.resourceType, 'Observation');
    assert.equal(choice.source?.path, 'status');
    assert.equal(choice.source?.cardinality, cardinalityExpected);
    assert(choice.options?.some((option) => option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'),
      'The nested field choice must expose a supported scalar VALUE form');
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      scalarOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'VALUE' && option.shape === 'SCALAR'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const assertQuantityCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'valueQuantity.value');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.valueQuantity.value candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, 'decimal', 'Observation.valueQuantity.value must retain its compiler-proved decimal logical type');
    assert.equal(candidate.cardinality, 'optional_one', 'Observation.valueQuantity.value must retain its scalar source cardinality');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.valueQuantity.value must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The current catalog must expose scalar VALUE projection for Observation.valueQuantity.value');
    const choice = candidate.constructionChoice;
    assert(choice, 'Observation.valueQuantity.value must carry its nested compiler-proved field choice');
    assert.equal(choice.source?.kind, 'FIELD');
    assert.equal(choice.source?.nodeId, candidate.nodeId);
    assert.equal(choice.source?.resourceType, 'Observation');
    assert.equal(choice.source?.path, 'valueQuantity.value');
    assert.equal(choice.source?.cardinality, 'optional_one');
    assert(choice.options?.some((option) => option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'),
      'The nested field choice must expose a supported scalar decimal VALUE form');
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      scalarOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'VALUE' && option.shape === 'SCALAR'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const assertReferenceCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'specimen.reference');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.specimen.reference candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, 'string', 'Observation.specimen.reference must retain its compiler-proved string logical type');
    assert.equal(candidate.cardinality, 'optional_one', 'Observation.specimen.reference must remain a nullable scalar');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.specimen.reference must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The current catalog must expose scalar VALUE projection for Observation.specimen.reference');
    const choice = candidate.constructionChoice;
    assert(choice, 'Observation.specimen.reference must carry its nested compiler-proved field choice');
    assert.equal(choice.source?.kind, 'FIELD');
    assert.equal(choice.source?.nodeId, candidate.nodeId);
    assert.equal(choice.source?.resourceType, 'Observation');
    assert.equal(choice.source?.path, 'specimen.reference');
    assert.equal(choice.source?.cardinality, 'optional_one');
    assert(choice.options?.some((option) => option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'),
      'The nested field choice must expose a supported scalar VALUE form');
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      allOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'ALL'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const assertRelatedIdCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'id');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.id candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, 'string', 'Observation.id must retain its compiler-proved string logical type');
    assert.equal(candidate.cardinality, 'optional_one', 'Observation.id must retain its compiler-proved scalar cardinality');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.id must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The native catalog must advertise Observation.id VALUE projection');
    assert.equal(candidate.constructionChoice?.source?.kind, 'FIELD');
    assert.equal(candidate.constructionChoice?.source?.nodeId, candidate.nodeId);
    assert.equal(candidate.constructionChoice?.source?.resourceType, 'Observation');
    assert.equal(candidate.constructionChoice?.source?.path, 'id');
    assert.equal(candidate.constructionChoice?.source?.cardinality, 'optional_one');
    assert(candidate.constructionChoice?.options?.some((option) =>
      option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'));
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      scalarOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'VALUE' && option.shape === 'SCALAR'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const relatedValuesFor = (witness) => quantityFieldMode ? witness.quantityValues : statusFieldMode ? witness.observationStatuses : referenceFieldMode ? witness.observationReferences : witness.observationIds;
const relatedDisplayValuesFor = (witness) => {
  const values = relatedValuesFor(witness).filter((value) => value !== null && value !== undefined);
  return zeroObservationMode && values.length === 0 ? '—' : values.join('; ');
};
const readFixtureNDJSON = async (relativePath) => (await readFile(fileURLToPath(new URL(relativePath, import.meta.url)), 'utf8'))
  .split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
let nativeCapture;
let fatalError;
let fixtureUnavailable;
let browserAuthoringBase;
const startNativeCapture = async () => {
  nativeCapture = captureCDARequests(nativePage, {
    apiOrigin,
    browserRequestOrigin: uiOrigin,
    appOrigins: [uiOrigin],
    ownedPathPrefix: browserAuthoringBase,
    report,
    responsePaths: relatedOneAllResponsePaths,
  });
  nativePage.on('request', request => {
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (url.pathname.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
  });
  nativePage.on('pageerror', error => report.errors.push({ kind: 'runtime', message: sanitizeText(error.message) }));
};
const responseOf = entry => entry?.response;
const waitNative = (predicate, fromIndex = 0, timeoutMs = 5000) => nativeCapture.waitForResponse(
  entry => predicate(entry, entry.response), { fromIndex, timeoutMs });
const nativeRequestValue = (entry, key) => entry.request?.[key] ?? entry.query?.[key];
const inspectPage = (page, inspect, argument) => page.evaluate(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = Math.min(5000, typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument);
  return page.waitForFunction(predicate, argument, { timeout }).then(() => undefined);
};
const waitForVisible = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'visible', timeout: Math.min(5000, timeout) });
const waitForHidden = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'hidden', timeout: Math.min(5000, timeout) });
const gotoPage = (_page, url) => cda.navigate(url);
const clickNative = (page, selector, identity = {}) => {
  const locator = identity.name !== undefined
    ? page.getByRole('button', { name: identity.name, exact: true })
    : identity.includes !== undefined
      ? page.getByRole('button', { name: new RegExp(identity.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') })
      : page.locator(selector);
  return cda.action(`click ${identity.name ?? identity.includes ?? selector}`, locator,
    target => target.click({ timeout: 5000 }), { timeout: 5000 });
};
const selectNative = async (page, selector, value) => {
  const locator = page.locator(selector);
  await cda.action(`select ${selector}`, locator, target => target.selectOption(value, { timeout: 5000 }), { timeout: 5000 });
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
const record = (name, started, details = {}) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  report.cases.push({ name, durationMs, ...details });
};
let activeQuantityValuesByPatient = new Map();
const parseQuantityDisplayCell = display => {
  if (typeof display !== 'string' || display.trim() === '') return null;
  const tokens = display.split(';').map(token => token.trim());
  if (tokens.some(token => !/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(token))) return null;
  const values = tokens.map(Number);
  return values.every(Number.isFinite) ? values : null;
};
const quantityDisplayMatches = (display, expectedValues) => {
  const values = parseQuantityDisplayCell(display);
  return values !== null && hasSameQuantityValueMultiset(values, expectedValues);
};
const assertQuantityOutputRowsMatch = (actualRows, expectedRows, name) => {
  assert.equal(actualRows.length, expectedRows.length, `${name} must retain every Quantity output row`);
  const pending = expectedRows.map(row => {
    const expectedValues = activeQuantityValuesByPatient.get(row[0]);
    assert(Array.isArray(expectedValues), `${name} has no raw Quantity oracle for Patient ${row[0]}`);
    return { row, expectedValues };
  });
  for (const actual of actualRows) {
    const index = pending.findIndex(({ row, expectedValues }) => actual.length === row.length &&
      row.slice(0, -1).every((cell, cellIndex) => actual[cellIndex] === cell) &&
      quantityDisplayMatches(actual.at(-1), expectedValues));
    assert.notEqual(index, -1, `${name} contains an unexpected Quantity row or numeric multiset: ${JSON.stringify(actual)}`);
    pending.splice(index, 1);
  }
  assert.equal(pending.length, 0, `${name} omitted a Quantity row from the independent raw oracle`);
};
const rendered = async (expectedRows) => {
  const columnCount = expectedRows[0]?.length ?? 2;
  const quantityExpectedRows = quantityFieldMode && columnCount === 3
    ? expectedRows.map(row => {
      const values = activeQuantityValuesByPatient.get(row[0]);
      assert(Array.isArray(values), `No raw Quantity oracle is available for Patient ${row[0]}`);
      return { prefix: row.slice(0, -1), values };
    })
    : null;
  const exactExpectedRows = !quantityExpectedRows && (referenceFieldMode || quantityFieldMode) && expectedRows.length <= 25
    ? expectedRows.map(row => JSON.stringify(row)).sort()
    : null;
  await waitForObservable(nativePage, ({ columnCount, exactExpectedRows, quantityExpectedRows }) => {
    const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const settled = table?.getAttribute('aria-colcount')===String(columnCount)&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:');
    if (!settled) return false;
    const rows = [...table.querySelectorAll('[role="row"]')].slice(1)
      .map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length);
    if (quantityExpectedRows !== null) {
      if (rows.length !== quantityExpectedRows.length) return false;
      const parse = display => {
        if (typeof display !== 'string' || display.trim() === '') return null;
        const tokens = display.split(';').map(token => token.trim());
        if (tokens.some(token => !/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(token))) return null;
        const values = tokens.map(Number);
        return values.every(Number.isFinite) ? values.sort((left, right) => left - right) : null;
      };
      const pending = [...quantityExpectedRows];
      for (const row of rows) {
        const actualValues = parse(row.at(-1));
        const index = pending.findIndex(expected => row.length === expected.prefix.length + 1 &&
          expected.prefix.every((cell, cellIndex) => row[cellIndex] === cell) && actualValues !== null &&
          actualValues.length === expected.values.length &&
          actualValues.every((value, valueIndex) => value === [...expected.values].sort((left, right) => left - right)[valueIndex]));
        if (index < 0) return false;
        pending.splice(index, 1);
      }
      return pending.length === 0;
    }
    if (exactExpectedRows === null) return true;
    const exactRows = rows.map(row => JSON.stringify(row)).sort();
    return JSON.stringify(exactRows) === JSON.stringify(exactExpectedRows);
  }, { columnCount, exactExpectedRows, quantityExpectedRows }, 5000);
  const rows = await inspectPage(nativePage, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
    .slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length));
  assert(rows.length > 0 || expectedRows.length === 0);
  if (quantityExpectedRows !== null) {
    assertQuantityOutputRowsMatch(rows, expectedRows, 'Visible saved Quantity table');
  } else {
    for (const row of rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `Visible row is not in the independent CDA witness: ${JSON.stringify(row)}`);
    if (exactExpectedRows !== null) {
      const ordered = values => values.map(row => JSON.stringify(row)).sort();
      assert.deepEqual(ordered(rows), exactExpectedRows, 'Every settled grouped table row must match the exact independent CDA oracle');
    }
  }
  return rows;
};
const openTable = async (expectedRows, name, assertSettled = async () => {}) => {
  const started = Date.now();
  const fromIndex = nativeCapture.requests.length;
  await gotoPage(nativePage, `${uiOrigin}/?project=${encodeURIComponent(browserProject)}&explorer=${explorer}&mode=builder`);
  await waitForVisible(nativePage, `[data-testid="construction-table-${outputId}"]`, 5000);
  await clickNative(nativePage, `[data-testid="construction-table-${outputId}"]`);
  await rendered(expectedRows);
  const preview = await waitNative((entry, response) => entry.path.endsWith('/preview') &&
    entry.request?.outputId === outputId && response !== undefined, fromIndex);
  assert.equal(preview.status, 200, `${name} native Preview must complete successfully: ${JSON.stringify(preview.response)}`);
  const response = responseOf(preview);
  assert(response?.receiptId, `${name} native Preview has no current receipt`);
  await assertSettled(response);
  record(name, started, { receiptId: response.receiptId, rowCount: response.rowCount });
  return response;
};
const proposal = async (name, started, expectedRows, fromIndex, matchesRequest = () => true) => {
  const adopted = await waitNative((entry, response) => entry.startedAt >= started && entry.path.endsWith('/construction-proposals') &&
    response?.proposalId && matchesRequest(entry, response), fromIndex, 5000);
  const response = responseOf(adopted);
  await waitForObservable(nativePage, ({ proposalId }) => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready','error','needs-repair'].includes(panel?.dataset.proposalStatus) && panel?.dataset.proposalId === proposalId;
  }, { proposalId: response.proposalId }, Math.max(1, started + 5000 - Date.now()));
  const value = await inspectPage(nativePage, () => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    return {proposalId:panel?.dataset.proposalId,status:panel?.dataset.proposalStatus,text:panel?.innerText,
      rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};
  });
  assert.equal(value.status, 'ready', `${name}: ${value.text}`);
  assert.equal(value.rows.length, Math.min(25, expectedRows.length));
  if (quantityFieldMode && expectedRows[0]?.length === 3) {
    assertQuantityOutputRowsMatch(value.rows, expectedRows, name);
  } else {
    for (const row of value.rows) assert(expectedRows.some((expected) => JSON.stringify(expected) === JSON.stringify(row)), `${name} differs from the raw CDA witness: ${JSON.stringify(row)}`);
  }
  record(name, started, { proposalId: response.proposalId, rows: value.rows });
  return value;
};
const applyProposal = async (expectedRows, name) => {
  const started = Date.now();
  await clickNative(nativePage, '[data-testid="construction-apply-proposal"]');
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  await rendered(expectedRows);
  record(name, started);
  builder = await api(`${base}/builder`);
};
const waitRelatedProposal = async (name, started, expectedRequestPolicy, fromIndex, assertSettled) => {
  const entry = await waitNative((candidate, response) => candidate.startedAt >= started && response !== undefined &&
    matchSelectedRelatedSourceProposal({ ...candidate, response }, expectedRelatedSource()), fromIndex);
  const response = entry.response;
  const match = matchSelectedRelatedSourceProposal({ ...entry, response }, expectedRelatedSource());
  assert(match, 'The native candidate must contain the exact selected Observation related source');
  assert.equal(match.related.form, 'ALL', 'RELATED_SOURCE must retain the all-matching Observation result form');
  assert.equal(match.related.contributorRule?.policy, 'ALL_MATCHES', 'RELATED_SOURCE must retain all matching records on the selected route');
  assert.equal(match.related.choiceId, report.currentRelatedChoiceId,
    'The related proposal must retain the exact signed candidate and route inspected from the authorized catalog');
  assert.equal(match.related.source.nodeId, report.relatedFieldCandidate.nodeId,
    'The related proposal must retain the exact selected Observation node');
  assert.equal(match.rowValuePolicy, expectedRequestPolicy,
    `The grouped-row selector requested ${expectedRequestPolicy}, but the native RELATED_SOURCE proposal encoded ${match.rowValuePolicy}`);
  assert.equal(match.related.source.logicalType, logicalTypeExpected);
  if (cardinalityExpected) assert.equal(match.related.source.cardinality, cardinalityExpected);
  assert.equal(match.related.route.length, expectedRelatedRouteTypes.length - 1);
  assert(match.related.route.every((hop, index) => hop.fromResourceType === expectedRelatedRouteTypes[index] &&
    hop.toResourceType === expectedRelatedRouteTypes[index + 1]),
  `The related source proposal lost exact route ${expectedRelatedRouteTypes.join(' → ')}`);
  if (zeroObservationMode) assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedPatientObservationRoute,
  'The zero-match proposal must use the exact inbound Patient <-[subject_Patient]- Observation route');
  if (referenceFieldMode) assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The nullable-reference repair must retain the exact Specimen→Patient→Observation subject route');
  await assertSettled?.(entry, match);
  record(name, started, { status: entry.status, responseStatus: response?.previewStatus,
    previewDurationMs: response?.previewDurationMs, relatedSourceForm: match.related.form,
    requestedRowValuePolicy: expectedRequestPolicy, relatedSourceRowValuePolicy: match.related.rowValuePolicy ?? 'ALL',
    proposalId: response?.proposalId });
  return entry;
};
const waitAdoptedChoicePreview = async (entry, name) => {
  const proposalId = entry.response?.proposalId;
  assert(proposalId, `${name} response has no construction proposal ID`);
  await waitForObservable(nativePage, ({ proposalId }) => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.dataset.proposalStatus === 'ready' && panel?.dataset.proposalId === proposalId;
  }, { proposalId }, 5000);
  const active = await inspectPage(nativePage, () => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return { status: panel?.dataset.proposalStatus, proposalId: panel?.dataset.proposalId };
  });
  assert.deepEqual(active, { status: 'ready', proposalId }, `${name} must be the active native construction proposal`);
  return proposalId;
};
const expand = async (hop, witnesses, expectedRows, sourcePopulationBaseline) => {
  await clickNative(nativePage, '[data-testid="construction-rows-settings-trigger"]');
  await waitForObservable(nativePage, () => document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false, 5000);
  await clickNative(nativePage, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForObservable(nativePage, () => document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]')?.disabled === false, 5000);
  const started = Date.now();
  await selectNative(nativePage, `${panel} select[aria-label="Related record type"]`, hop.to);
  const label = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
  const relationshipChoice = nativePage.locator(`${panel} input[aria-label=${JSON.stringify(label)}]`);
  await relationshipChoice.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await relationshipChoice.count(), 1, 'The relationship choice must be unique');
  const proposalFromIndex = nativeCapture.requests.length;
  const proposalStarted = Date.now();
  await clickNative(nativePage, `${panel} input[aria-label="${label}"]`);
  let rowsFromProposal = expectedRows;
  let zeroExpandEvidence;
  let zeroExpansionStepID;
  if (zeroObservationMode) {
    const expansionEntry = await waitNative((entry, response) => entry.startedAt >= proposalStarted &&
      entry.path.endsWith('/construction-proposals') && response?.proposalId, proposalFromIndex);
    const expansionResponse = responseOf(expansionEntry);
    const expansionStep = expansionEntry.request?.candidateConstruction?.steps?.find((step) =>
      step.operation?.kind === 'RELATED_EXPAND' && step.operation.relatedExpand?.targetResourceType === 'Observation');
    assert(expansionStep, 'The direct Patient-root proposal must contain the selected Observation expansion');
    zeroExpansionStepID = expansionStep.id;
    assert.equal(expansionStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT',
      'The zero-match lifecycle must use the current native RelatedExpand empty policy');
    const expansionOutput = expansionStep.outputs?.find((output) =>
      output.id === expansionStep.operation.relatedExpand.relatedRecordColumnId);
    assert(expansionOutput, 'The PRESERVE_PARENT proposal must expose its nullable Observation identity output');
    const normalizedExpansionStep = expansionResponse?.candidateConstruction?.steps?.find((step) =>
      step.id === expansionStep.id);
    assert(normalizedExpansionStep,
      'The PRESERVE_PARENT proposal response must retain the selected expansion step');
    const normalizedExpansionOutput = normalizedExpansionStep.outputs?.find((output) =>
      output.id === expansionOutput.id);
    assert(normalizedExpansionOutput,
      'The PRESERVE_PARENT proposal response must retain the related Observation identity output');
    const preview = expansionResponse?.preview;
    const previewExpansionColumn = preview?.columns?.find((column) => column.column === expansionOutput.name);
    assert.equal(normalizedExpansionOutput.nullable, true,
      'The normalized PRESERVE_PARENT related Observation identity output must be nullable');
    assert.equal(previewExpansionColumn?.nullable, true,
      'The compiled PRESERVE_PARENT preview schema must mark the related Observation identity nullable');
    assert.equal(preview?.rowCount, 1, 'The native PRESERVE_PARENT proposal must retain the exact zero-match Patient row');
    assert.equal(preview?.rows?.length, 1, 'The zero-match proposal must return the retained parent row');
    const parentColumn = preview.columns.find((column) => column.label === 'Patient ID');
    assert(parentColumn, 'The proposal must retain the native Patient identity column');
    assert.equal(preview.rows[0][parentColumn.column], witnesses[0].patient.id,
      'The native proposal must preserve the exact selected Patient under its current empty policy');
    assert.equal(preview.rows[0][expansionOutput.name], null,
      'PRESERVE_PARENT must represent the unmatched related Observation identity as a null value');
    rowsFromProposal = preview.rows.map((row) => preview.columns.map((column) =>
      row[column.column] === null || row[column.column] === undefined ? '—' : String(row[column.column])));
    assert.deepEqual(rowsFromProposal, expectedRows,
      'The native proposal rows must agree with the independently reread Patient root and zero-edge oracle');
    zeroExpandEvidence = { stepId: expansionStep.id,
      patientId: witnesses[0].patient.id,
      routeChoiceId: expansionStep.operation.relatedExpand.choiceId,
      targetNodeId: expansionStep.operation.relatedExpand.targetNodeId,
      targetResourceType: expansionStep.operation.relatedExpand.targetResourceType,
      route: expansionStep.operation.relatedExpand.route,
      relatedRecordColumnId: expansionStep.operation.relatedExpand.relatedRecordColumnId,
      outputNullable: normalizedExpansionOutput.nullable,
      previewSchemaNullable: previewExpansionColumn?.nullable,
      emptyPolicy: expansionStep.operation.relatedExpand.emptyPolicy,
      previewRowCount: preview.rowCount,
      unmatchedObservationOutputIsNull: preview.rows[0][expansionOutput.name] === null };
  }
  const value = await proposal(`expand-${hop.from}-${hop.to}-preview`, proposalStarted, rowsFromProposal, proposalFromIndex);
  assert(nativeCapture.requests.slice(proposalFromIndex).some((entry) => entry.response?.proposalId === value.proposalId), 'The displayed expansion preview must match its captured native proposal');
  report.cases.at(-1).witnessCount = witnesses.length;
  await applyProposal(rowsFromProposal, `expand-${hop.from}-${hop.to}-apply-to-render`);
  if (zeroObservationMode) {
    const savedExpansion = doc().construction.steps.find((step) => step.id === zeroExpansionStepID);
    assert.equal(savedExpansion?.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT',
      'Applying the zero-match expansion must save the proposal’s PRESERVE_PARENT policy');
    assert.deepEqual(doc().population, sourcePopulationBaseline,
      'Applying the zero-match expansion must preserve the exact one-Patient source population');
    report.zeroObservationExpansion = savedEmptyPolicyEdit
      ? { ...zeroExpandEvidence,
        savedPolicy: savedExpansion.operation.relatedExpand.emptyPolicy,
        savedStepId: savedExpansion.id,
        savedOutputNullable: savedExpansion.outputs?.find((output) =>
          output.id === savedExpansion.operation.relatedExpand.relatedRecordColumnId)?.nullable }
      : { ...zeroExpandEvidence, savedPolicy: savedExpansion.operation.relatedExpand.emptyPolicy };
  }
};
const openRelatedFieldChooser = async () => {
  const alreadyOpen = await nativePage.locator('[aria-label="Add columns editor"]').count() === 1;
  if (!alreadyOpen) {
    await clickNative(nativePage, '[data-testid="construction-action-add-columns"]');
    await clickNative(nativePage, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  }
  await waitForVisible(nativePage, '[data-testid="construction-add-columns-source"]', 5000);
  const relatedResourcesOpen = await nativePage.locator('[aria-label="Related resources"]').evaluate(node => node.open);
  if (!relatedResourcesOpen) {
    await clickNative(nativePage, '[aria-label="Related resources"] summary');
    await waitForObservable(nativePage, () => document.querySelector('[aria-label="Related resources"]')?.open === true, 5000);
  }
  await clickNative(nativePage, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  const rawFieldsOpen = await nativePage.locator('[data-testid="feature-catalog-raw-fields"]').evaluate(node => node.open);
  if (!rawFieldsOpen) {
    await clickNative(nativePage, '[data-testid="feature-catalog-raw-fields"] > summary');
    await waitForObservable(nativePage, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true, 5000);
  }
  const choiceSearchFrom = nativeCapture.requests.length;
  const fieldSelector = `input[aria-label=${JSON.stringify(`Select Observation.${relatedFieldPath}`)}]`;
  const fieldControl = nativePage.locator(fieldSelector);
  await fieldControl.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await fieldControl.count(), 1, 'The related field option must be unique');
  await fieldControl.waitFor({ state: 'attached', timeout: 5000 });
  assert.equal(await fieldControl.isEnabled(), true, 'The related field option must be enabled');
  await clickNative(nativePage, fieldSelector);
  await clickNative(nativePage, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForVisible(nativePage, '[role="dialog"]', 5000);
  const otherPathDisclosure = nativePage.locator('[role="dialog"] summary').filter({ hasText: 'Other relationship paths' });
  if (await otherPathDisclosure.count() > 0) {
    await cda.action('click Other relationship paths', otherPathDisclosure,
      target => target.click({ timeout: 5000 }), { timeout: 5000 });
  }
  const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
  await waitForVisible(nativePage, routeSelector, 5000);
  await clickNative(nativePage, routeSelector);
  await nativePage.locator(routeSelector).waitFor({ state: 'visible', timeout: 5000 });
  await waitForObservable(nativePage, ({ selector }) => document.querySelector(selector)?.checked === true, { selector: routeSelector }, 5000);
  const formLabel = `${relatedChoiceLabel}: Keep all matching values`;
  const formSelector = `[role="dialog"] input[aria-label=${JSON.stringify(formLabel)}]`;
  await waitForVisible(nativePage, formSelector, 5000);
  await clickNative(nativePage, formSelector);
  const control = await inspectPage(nativePage, () => {
    const dialog = document.querySelector('[role="dialog"]');
    const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
    return { dialog: Boolean(dialog), policyOptions: policy ? [...policy.options].map(option => ({ value: option.value, label: option.textContent })) : [], policy: policy?.value };
  });
  assert(control.dialog, 'Related field ONE/ALL chooser is not open');
  assert.deepEqual(control.policyOptions.map((option) => option.value), ['ALL', 'ONE'], 'The current Add columns chooser does not expose grouped-row ONE and ALL');
  const candidateIds = new Set(report.relatedFieldCandidates?.map((candidate) => candidate.candidateId));
  assert(candidateIds.size > 0, 'The related field candidate identity must be captured before opening related-choice search');
  const matchesSelectedRoute = (choice) => {
    const source = choice?.source;
    const route = choice?.route;
    const candidate = report.relatedFieldCandidates.find((item) => item.candidateId === source?.candidateId);
    return source?.kind === 'FIELD' && candidateIds.has(source.candidateId) && candidate?.nodeId === source.nodeId &&
      source.resourceType === 'Observation' && source.path === relatedFieldPath &&
      Array.isArray(route) && route.length === expectedRelatedRouteTypes.length - 1 &&
      route.every((hop, index) => hop.fromResourceType === expectedRelatedRouteTypes[index] &&
        hop.toResourceType === expectedRelatedRouteTypes[index + 1]) &&
      (!zeroObservationMode || JSON.stringify(route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
        ({ fromResourceType, toResourceType, relationship, storageDirection }))) === JSON.stringify(expectedPatientObservationRoute)) &&
      (!referenceFieldMode || JSON.stringify(route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
        ({ fromResourceType, toResourceType, relationship, storageDirection }))) === JSON.stringify(expectedReferenceRoute));
  };
  const catalogEntry = await waitNative((entry, response) => entry.path.endsWith('/construction-choices') &&
    candidateIds.has(entry.request?.source?.candidateId) &&
    response?.choices?.some(matchesSelectedRoute), choiceSearchFrom);
  const catalogResponse = responseOf(catalogEntry);
  assert.equal(catalogEntry.status, 200, JSON.stringify(catalogEntry.response));
  assert.equal(catalogEntry.request.outputId, outputId, 'The selected route must come from this owned output’s current catalog request');
  assert.equal(catalogEntry.request.snapshotToken, builder.catalog.snapshotToken, 'The selected route must come from the current pinned catalog snapshot');
  const selectedChoice = catalogResponse.choices.find(matchesSelectedRoute);
  assert(selectedChoice?.choiceId, 'The current authorized field catalog must contain the exact selected relationship route');
  assert(selectedChoice.options?.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED'),
    'The exact signed route choice must support preserving all matching records');
  const candidate = report.relatedFieldCandidates.find((item) => item.candidateId === selectedChoice.source.candidateId);
  assert(candidate, 'The selected related source must be one of the current catalog candidates');
  assert.equal(selectedChoice.source.nodeId, candidate.nodeId);
  assert.equal(selectedChoice.source.resourceType, 'Observation');
  assert.equal(selectedChoice.source.path, relatedFieldPath);
  if (zeroObservationMode) assert.deepEqual(selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedPatientObservationRoute,
  'The authorized catalog choice must represent the exact incoming typed Observation edge');
  else if (!basicMode) assert.deepEqual(selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The selected catalog choice must preserve the exact Specimen→Patient→Observation subject route');
  report.relatedFieldCandidate = candidate;
  report.relatedChoiceAssertions.push({ candidateId: candidate.candidateId, choiceId: selectedChoice.choiceId,
    resourceType: selectedChoice.source.resourceType, path: selectedChoice.source.path,
    logicalType: candidate.logicalType, cardinality: candidate.cardinality, form: 'ALL',
    supportedForms: selectedChoice.options.filter((option) => option.support === 'SUPPORTED').map((option) => option.form),
    route: selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
      ({ fromResourceType, toResourceType, relationship, storageDirection })),
    choiceRequest: { status: catalogEntry.status, outputId: catalogEntry.request.outputId,
      snapshotToken: catalogEntry.request.snapshotToken, truncated: catalogResponse.truncated,
      responseState: catalogResponse.state } });
  report.currentRelatedChoiceId = selectedChoice.choiceId;
  assert(report.relatedFieldCandidate?.candidateId && report.currentRelatedChoiceId,
    'The selected related field must be bound to its authorized candidate and signed route choice');
  report.groupedRowPolicyControl = control;
};
const selectRelatedPolicy = async (policy) => {
  await selectNative(nativePage, '[role="dialog"] select[aria-label="Values per grouped row"]', policy);
  const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
  const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
  const state = await inspectPage(nativePage, ({ routeSelector, formSelector }) => {
    const dialog = document.querySelector('[role="dialog"]');
    return { policy: dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value,
      routeChecked: dialog?.querySelector(routeSelector)?.checked, formChecked: dialog?.querySelector(formSelector)?.checked };
  }, { routeSelector, formSelector });
  assert.equal(state.policy, policy);
  assert.equal(state.routeChecked, true, 'The exact selected Observation relationship path was lost');
  assert.equal(state.formChecked, true, 'Per-record matching Observation values must remain ALL');
  return state;
};
const clickAddRelated = async () => {
  await clickNative(nativePage, '[role="dialog"] button', { name: 'Add 1 column' });
};
const cancelColumnProposal = async () => {
  await clickNative(nativePage, '[data-testid="construction-cancel-proposal"]');
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
};
const readPreviewTable = async (expectedRows, expectedColumns, name) => {
  const started = Date.now();
  const nativeFrom = nativeCapture.requests.length;
  await gotoPage(nativePage, `${uiOrigin}/?project=${encodeURIComponent(browserProject)}&explorer=${explorer}&mode=builder`);
  await waitForVisible(nativePage, `[data-testid="construction-table-${outputId}"]`, 5000);
  await clickNative(nativePage, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(nativePage, ({ expectedColumns }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-colcount') === String(expectedColumns) &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:');
  }, { expectedColumns }, 5000);
  const dom = await inspectPage(nativePage, () => {
    const area = document.querySelector('[data-testid="preview-table-scroll"]');
    const table = area?.querySelector('[role="table"]');
    return { headers: [...(area?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: [...(area?.querySelectorAll('[role="row"]') ?? [])].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
      columnCount: table?.getAttribute('aria-colcount') };
  });
  assert(dom.rows.length > 0 || expectedRows.length === 0, `${name} did not render a witness row: ${JSON.stringify(dom)}`);
  for (const row of dom.rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `${name} visible rows differ from the raw witnesses: ${JSON.stringify(row)}`);
  const preview = await waitNative((entry, response) => entry.path.endsWith('/preview') &&
    entry.request?.outputId === outputId && response !== undefined, nativeFrom);
  assert.equal(preview.status, 200, `${name} native Preview must complete successfully: ${JSON.stringify(preview.response)}`);
  const response = responseOf(preview);
  assert(response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: response.receiptId, headers: dom.headers, nativeRowCount: response.rowCount, mountedRowCount: dom.rows.length });
  return response;
};

try {
  assert.notEqual(requestedExplorerName, protectedExplorer, 'Only a fresh QA Explorer may be requested');
  apiBuildCheckStarted = true;
  report.apiBuildFreeze = { target: 'running local API build stamp', container: apiBuildContainer, invalidatesRun: true, productFailure: false };
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiBuildContainer));
  report.apiBuildFreeze.initial = frozenApiBuild.initial;
  let witnesses;
  let selectedMembers;
  let manyWitness;
  let rereadZeroObservationWitness;
  if (zeroObservationMode) {
    const patientCandidateLimit = 2000;
    const zeroWitnessQuery = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)}
      AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
      AND scopedPatient.resourceType == "Patient"
      AND scopedPatient.payload.resourceType == "Patient"
    SORT scopedPatient.id
    LIMIT ${patientCandidateLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET matchingObservations = (
    FOR edge IN fhir_edge
      FILTER edge._to == p._id AND edge.from_type == "Observation" AND edge.to_type == "Patient"
        AND STARTS_WITH(edge._from, "Observation/") AND edge.label == "subject_Patient"
        AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
      LET observation = DOCUMENT(edge._from)
      FILTER observation != null AND observation.project == ${JSON.stringify(project)}
        AND observation.dataset_generation == ${JSON.stringify(generation)}
        AND observation.resourceType == "Observation"
        AND observation.payload.resourceType == "Observation"
      COLLECT observationKey = observation._id
      RETURN observationKey
  )
  FILTER LENGTH(matchingObservations) == 0
  SORT p.id
  LIMIT 1
  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) } }
`;
    report.oracle = {
      kind: 'bounded scoped Patient finder for no incoming typed Observation.subject_Patient edge, followed by an exact selected-Patient reread',
      searchBounds: {
        patientCandidates: { project, generation, sortedBy: 'id', limit: patientCandidateLimit },
        edge: { fromResourceType: 'Observation', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
        targetDocumentScope: { project, generation },
        authorizationScope: 'local unrestricted project/generation reads; no restricted-auth claim',
        missingWitnessMeaning: 'No witness in this bounded candidate scan is unavailability, not proof of global absence.',
      },
      fixtureAvailability: { zeroIncomingObservationWitnessAvailable: false, patientCandidateLimit },
      missingWitnessCategories: [],
    };
    const [zeroSeed] = rawQuery(zeroWitnessQuery);
    if (!zeroSeed) {
      const meaning = `No Patient with zero scoped incoming Observation.subject_Patient edges was found among the first ${patientCandidateLimit} current-generation Patient candidates. This is bounded witness unavailability, not proof of global absence.`;
      report.oracle.fixtureAvailability.meaning = meaning;
      report.oracle.missingWitnessCategories.push({ category: 'zero-observation', patientCandidateLimit, meaning });
      throw fixtureUnavailableError(meaning);
    }

    const exactZeroMembershipQuery = `
LET patientKey = ${JSON.stringify(zeroSeed.patient._id)}
LET patient = DOCUMENT(patientKey)
FILTER patient != null AND patient._id == patientKey
  AND patient.id == ${JSON.stringify(zeroSeed.patient.id)}
  AND patient.project == ${JSON.stringify(project)}
  AND patient.dataset_generation == ${JSON.stringify(generation)}
  AND patient.resourceType == "Patient"
  AND patient.payload.resourceType == "Patient"
LET matchingObservations = (
  FOR edge IN fhir_edge
    FILTER edge._to == patient._id AND edge.from_type == "Observation" AND edge.to_type == "Patient"
      AND STARTS_WITH(edge._from, "Observation/") AND edge.label == "subject_Patient"
      AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
    LET observation = DOCUMENT(edge._from)
    FILTER observation != null AND observation.project == ${JSON.stringify(project)}
      AND observation.dataset_generation == ${JSON.stringify(generation)}
      AND observation.resourceType == "Observation"
      AND observation.payload.resourceType == "Observation"
    COLLECT observationKey = observation._id
    RETURN observationKey
)
RETURN {
  patient: { id: patient.id, _id: patient._id, reference: CONCAT("Patient/", patient.id) },
  matchingObservationCount: LENGTH(matchingObservations)
}
`;
    const [exactZero] = rawQuery(exactZeroMembershipQuery);
    assert(exactZero, 'The exact selected Patient and its project/generation scope must survive the independent reread');
    assert.equal(exactZero.matchingObservationCount, 0,
      'The independent exact Patient reread found a scoped incoming Observation.subject_Patient match');
    assert.equal(exactZero.patient.id, zeroSeed.patient.id, 'The bounded finder and exact Patient reread disagree');
    assert.equal(exactZero.patient._id, zeroSeed.patient._id, 'The exact Patient identity changed between finder and reread');
    assert.equal(exactZero.patient.reference, zeroSeed.patient.reference, 'The exact Patient reference changed between finder and reread');

    const witness = {
      category: 'zero-observation', patient: exactZero.patient,
      members: [{ id: exactZero.patient.id, _id: exactZero.patient._id,
        patientReference: exactZero.patient.reference, patient: exactZero.patient }],
      observationIds: [], observationKeys: [], observationCount: 0,
      observationStatuses: [], distinctStatusValues: [], expectedContributorRows: 1,
    };
    witnesses = [witness];
    selectedMembers = witness.members;
    rereadZeroObservationWitness = () => rawQuery(exactZeroMembershipQuery);
    Object.assign(report.oracle, {
      exactMembershipScope: { project, generation, rootResourceType: 'Patient', selectedRootCount: 1,
        incomingObservationEdgeCount: 0, exactRootRereadPerformed: true,
        authorizationScope: 'unrestricted local Arango scope' },
      fixtureAvailability: { zeroIncomingObservationWitnessAvailable: true, patientCandidateLimit },
      witnessSummary: { category: witness.category, rootResourceType: 'Patient', rootCount: 1,
        patientId: witness.patient.id, patientKey: witness.patient._id,
        distinctIncomingObservationCount: 0 },
    });
  } else if (basicMode && quantityFieldMode) {
    const patientCandidateLimit = 2000;
    const expectedCode = { system: 'https://example.test/codes', code: 'height', display: 'Height' };
    const basicQuantityWitnessQuery = [
      'FOR p IN (',
      '  FOR scopedPatient IN Patient',
      '    FILTER scopedPatient.project == ' + JSON.stringify(project) + ' AND scopedPatient.dataset_generation == ' + JSON.stringify(generation),
      '      AND scopedPatient.resourceType == "Patient" AND scopedPatient.payload.resourceType == "Patient"',
      '    SORT scopedPatient.id',
      '    LIMIT ' + patientCandidateLimit,
      '    RETURN { id: scopedPatient.id, _id: scopedPatient._id }',
      ')',
      '  LET observations = (',
      '    FOR e IN fhir_edge',
      '      FILTER e._to == p._id AND e.from_type == "Observation" AND e.to_type == "Patient"',
      '        AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"',
      '        AND e.project == ' + JSON.stringify(project) + ' AND e.dataset_generation == ' + JSON.stringify(generation),
      '      COLLECT observationKey = e._from',
      '      LET o = DOCUMENT(observationKey)',
      '      FILTER o != null AND o.project == ' + JSON.stringify(project) + ' AND o.dataset_generation == ' + JSON.stringify(generation),
      '        AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"',
      '        AND o.payload.subject.reference == CONCAT("Patient/", p.id)',
      '      SORT o.id',
      '      RETURN { id: o.id, _id: o._id, code: o.payload.code.coding, value: o.payload.valueQuantity.value, quantity: o.payload.valueQuantity }',
      '  )',
      '  LET heightObservations = (',
      '    FOR observation IN observations',
      '      FILTER IS_NUMBER(observation.value) AND IS_ARRAY(observation.code) AND LENGTH(observation.code) == 1',
      '        AND FIRST(observation.code).system == ' + JSON.stringify(expectedCode.system),
      '        AND FIRST(observation.code).code == ' + JSON.stringify(expectedCode.code),
      '        AND FIRST(observation.code).display == ' + JSON.stringify(expectedCode.display),
      '      RETURN observation',
      '  )',
      '  LET distinctValues = (',
      '    FOR observation IN heightObservations',
      '      COLLECT value = observation.value',
      '      SORT value',
      '      RETURN value',
      '  )',
      '  FILTER LENGTH(observations) == 2 AND LENGTH(heightObservations) == 2 AND LENGTH(distinctValues) == 2',
      '  SORT p.id',
      '  LIMIT 1',
      '  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) }, observations }',
    ].join('\n');
    const [basicSeed] = rawQuery(basicQuantityWitnessQuery);
    report.oracle = {
      kind: 'bounded Patient→Observation edge witness for exact scalar Quantity values, followed by an exact selected-Patient reread',
      searchBounds: { patientCandidates: { project, generation, sortedBy: 'id', limit: patientCandidateLimit },
        selectedObservationCount: 2, codeFamily: expectedCode,
        missingWitnessMeaning: 'No witness in this bounded scan is unavailability, not global absence.' },
      requiredRelatedField: { resourceType: 'Observation', path: 'valueQuantity.value', logicalType: 'decimal', cardinality: 'optional_one' },
      witnessQuery: basicQuantityWitnessQuery,
    };
    if (!basicSeed) {
      report.oracle.fixtureAvailability = { requiredSameCodeTwoDistinctQuantityValuesAvailable: false, patientCandidateLimit };
      throw fixtureUnavailableError('No scoped Patient with exactly two linked same-code Observations carrying distinct scalar Quantity values was found among the first ' + patientCandidateLimit + ' current-generation Patients.');
    }
    const patientKey = basicSeed.patient._id;
    const exactBasicQuantityMembershipQuery = [
      'FOR p IN Patient',
      '  FILTER p._id == ' + JSON.stringify(patientKey) + ' AND p.project == ' + JSON.stringify(project),
      '    AND p.dataset_generation == ' + JSON.stringify(generation) + ' AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"',
      '  LET observations = (',
      '    FOR e IN fhir_edge',
      '      FILTER e._to == p._id AND e.from_type == "Observation" AND e.to_type == "Patient"',
      '        AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"',
      '        AND e.project == ' + JSON.stringify(project) + ' AND e.dataset_generation == ' + JSON.stringify(generation),
      '      COLLECT observationKey = e._from',
      '      LET o = DOCUMENT(observationKey)',
      '      FILTER o != null AND o.project == ' + JSON.stringify(project) + ' AND o.dataset_generation == ' + JSON.stringify(generation),
      '        AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"',
      '        AND o.payload.subject.reference == CONCAT("Patient/", p.id)',
      '      SORT o.id',
      '      RETURN { id: o.id, _id: o._id, code: o.payload.code.coding, value: o.payload.valueQuantity.value, quantity: o.payload.valueQuantity }',
      '  )',
      '  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) }, observations }',
    ].join('\n');
    const [exactBasic] = rawQuery(exactBasicQuantityMembershipQuery);
    assert(exactBasic, 'The selected Basic Patient is no longer visible in the requested project/generation');
    assert.equal(exactBasic.patient.id, basicSeed.patient.id);
    assert.equal(exactBasic.patient._id, patientKey);
    assert.equal(exactBasic.observations.length, 2, 'The exact Basic witness must retain both linked Observation identities');
    assert.deepEqual(exactBasic.observations.map(({ id, _id }) => ({ id, _id })),
      basicSeed.observations.map(({ id, _id }) => ({ id, _id })),
      'The bounded finder and exact Patient→Observation reread disagree on source identities');
    assert(exactBasic.observations.every((observation) => typeof observation.value === 'number' && Number.isFinite(observation.value)),
      'Every exact Quantity scalar must remain a finite JSON number');
    assert.deepEqual(exactBasic.observations.map(({ id, value, quantity }) => ({ observationID: id, value, quantity })),
      basicSeed.observations.map(({ id, value, quantity }) => ({ observationID: id, value, quantity })),
      'The exact Patient reread changed the raw decimal Quantity values or units');
    const quantityOracle = await readBasicRelatedQuantityOneAllOracle(
      fileURLToPath(new URL('../../../testdata/devloop-fixture', import.meta.url)));
    assert.equal(exactBasic.patient.id, quantityOracle.patientID,
      'The selected raw Patient differs from the literal Basic fixture oracle');
    const exactFixtureValues = exactBasic.observations.map(({ id, code, value, quantity }) =>
      ({ observationID: id, code: code[0], value, quantity }));
    const expectedFixtureValues = quantityOracle.all.map(({ observationID, value, quantity }) => ({
      observationID, code: quantityOracle.codeFamily, value, quantity,
    }));
    assert.deepEqual(exactFixtureValues, expectedFixtureValues,
      'The scoped Arango witness differs from the exact Patient-linked fixture Quantity values');
    assert.deepEqual(exactBasic.observations.map(({ code }) => code), [[expectedCode], [expectedCode]],
      'Every Observation on the selected Patient route must belong to the exact single-coding Height family');
    assert.deepEqual(exactBasic.observations.map(({ value }) => value), [172.5, 180],
      'The complete selected Patient route must independently predict exactly two distinct decimal values');

    const quantityWitness = {
      category: 'same-code-family-distinct-decimals', patient: exactBasic.patient,
      members: [{ id: exactBasic.patient.id, _id: exactBasic.patient._id, patientReference: exactBasic.patient.reference }],
      observationIds: exactBasic.observations.map(({ id }) => id),
      observationKeys: exactBasic.observations.map(({ _id }) => _id),
      observationCount: exactBasic.observations.length,
      quantityValues: exactBasic.observations.map(({ value }) => value),
      observationQuantities: exactBasic.observations.map(({ quantity }) => quantity),
      codeFamily: quantityOracle.codeFamily,
      distinctQuantityValues: [...new Set(exactBasic.observations.map(({ value }) => value))],
      expectedContributorRows: 1,
    };
    witnesses = [quantityWitness];
    activeQuantityValuesByPatient = new Map([[quantityWitness.patient.id, quantityWitness.quantityValues]]);
    selectedMembers = quantityWitness.members;
    manyWitness = quantityWitness;
    report.oracle.exactMembershipQuery = exactBasicQuantityMembershipQuery;
    report.oracle.exactMembershipScope = { project, generation, rootResourceType: 'Patient',
      patientKey, patientId: exactBasic.patient.id, observationKeys: quantityWitness.observationKeys,
      observationCount: 2, codeFamily: quantityOracle.codeFamily,
      completeRouteMembership: true, allRowsMatchCodeFamily: true };
    report.oracle.exactScope = report.oracle.exactMembershipScope;
    report.oracle.witness = quantityWitness;
    report.oracle.witnesses = [quantityWitness];
    report.oracle.fixtureAvailability = { requiredSameCodeTwoDistinctQuantityValuesAvailable: true, patientCandidateLimit };
    report.oracle.literalFixtureCrossCheck = {
      fixture: 'testdata/devloop-fixture/Patient.ndjson + Observation.ndjson',
      files: quantityOracle.files,
      patientId: exactBasic.patient.id,
      codeFamily: quantityOracle.codeFamily,
      observationValues: exactFixtureValues,
    };
    report.oracle.finalScopedRereadMatched = false;
  } else if (basicMode && statusFieldMode) {
    const patientCandidateLimit = 2000;
    const basicWitnessQuery = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)} AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
      AND scopedPatient.resourceType == "Patient" AND scopedPatient.payload.resourceType == "Patient"
    SORT scopedPatient.id
    LIMIT ${patientCandidateLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
        AND IS_STRING(o.payload.status)
      SORT o.id
      RETURN { id: o.id, _id: o._id, status: o.payload.status }
  )
  FILTER LENGTH(observations) == 1
  SORT p.id
  LIMIT 1
  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) }, observations }
`;
    const [basicSeed] = rawQuery(basicWitnessQuery);
    report.oracle = {
      kind: 'bounded exact project/generation Patient→Observation fhir_edge witness plus an exact selected-Patient edge reread',
      searchBounds: { patientCandidates: { project, generation, sortedBy: 'id', limit: patientCandidateLimit },
        selectedObservationCount: 1, missingWitnessMeaning: 'No fixture witness in this bounded scan is unavailability, not global absence.' },
      requiredRelatedField: { resourceType: 'Observation', path: 'status', logicalType: 'code', cardinality: 'optional_one' },
      witnessQuery: basicWitnessQuery,
    };
    if (!basicSeed) {
      report.oracle.fixtureAvailability = { requiredSingleObservationWitnessAvailable: false, patientCandidateLimit };
      throw fixtureUnavailableError(`No scoped Patient with exactly one linked Observation carrying a scalar status was found among the first ${patientCandidateLimit} current-generation Patients.`);
    }
    const patientKey = basicSeed.patient._id;
    const exactBasicMembershipQuery = `
FOR p IN Patient
  FILTER p._id == ${JSON.stringify(patientKey)} AND p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      SORT o.id
      RETURN { id: o.id, _id: o._id, status: o.payload.status }
  )
  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) }, observations }
`;
    const [exactBasic] = rawQuery(exactBasicMembershipQuery);
    assert(exactBasic, 'The selected basic Patient is no longer visible in the requested project/generation');
    assert.deepEqual(exactBasic.observations.map(({ _id }) => _id), basicSeed.observations.map(({ _id }) => _id),
      'The bounded finder and exact Patient→Observation edge reread disagree');
    assert.equal(exactBasic.observations.length, 1, 'The basic status case requires exactly one linked Observation');
    assert.equal(exactBasic.observations[0].status, basicSeed.observations[0].status,
      'The exact raw Observation.status value differs from the bounded finder');
    if (project === 'loom_dev_c89a69d7e137' && generation === 'fixture-v1') {
      const fixturePatients = await readFixtureNDJSON('../../../testdata/devloop-fixture/Patient.ndjson');
      const fixtureObservations = await readFixtureNDJSON('../../../testdata/devloop-fixture/Observation.ndjson');
      assert(fixturePatients.some((patient) => patient.id === exactBasic.patient.id),
        'The raw-selected Patient is absent from the checked-in devloop fixture');
      const fixtureStatuses = fixtureObservations
        .filter((observation) => observation.subject?.reference === exactBasic.patient.reference)
        .map(({ id, status }) => ({ id, status })).sort((left, right) => left.id.localeCompare(right.id));
      const databaseStatuses = exactBasic.observations.map(({ id, status }) => ({ id, status }))
        .sort((left, right) => left.id.localeCompare(right.id));
      assert.deepEqual(databaseStatuses, fixtureStatuses,
        'The exact raw Patient→Observation witness/status differs from the checked-in NDJSON fixture');
      report.oracle.literalFixtureCrossCheck = {
        fixture: 'testdata/devloop-fixture/Patient.ndjson + Observation.ndjson',
        patientId: exactBasic.patient.id, observations: fixtureStatuses,
      };
    } else {
      report.oracle.literalFixtureCrossCheck = { available: false, reason: 'The selected project/generation is not the default devloop fixture.' };
    }
    witnesses = [{
      category: 'one-observation', patient: exactBasic.patient,
      members: [{ id: exactBasic.patient.id, _id: exactBasic.patient._id, patientReference: exactBasic.patient.reference }],
      observationIds: exactBasic.observations.map((observation) => observation.id),
      observationStatuses: exactBasic.observations.map((observation) => observation.status),
      distinctStatusValues: [...new Set(exactBasic.observations.map((observation) => observation.status))],
      expectedContributorRows: 1,
    }];
    selectedMembers = witnesses.flatMap((witness) => witness.members);
    report.oracle.exactMembershipQuery = exactBasicMembershipQuery;
    report.oracle.witness = witnesses[0];
    report.oracle.fixtureAvailability = { requiredSingleObservationWitnessAvailable: true, patientCandidateLimit };
    report.oracle.exactScope = { project, generation, patientKey, observationKeys: exactBasic.observations.map(({ _id }) => _id) };
  } else if (referenceFieldMode) {
    const exactReferenceMembershipQuery = `
LET specimen = DOCUMENT(${JSON.stringify(referenceWitness.specimenKey)})
FILTER specimen != null
  AND specimen.id == ${JSON.stringify(referenceWitness.specimenId)}
  AND specimen.project == ${JSON.stringify(project)}
  AND specimen.dataset_generation == ${JSON.stringify(generation)}
LET patientKeys = (
  FOR edge IN fhir_edge
    FILTER edge._from == specimen._id AND edge.label == "subject_Patient"
      AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT patientKey = edge._to
    RETURN patientKey
)
LET patients = (
  FOR patientKey IN patientKeys
    LET patient = DOCUMENT(patientKey)
    FILTER patient != null AND patient.project == ${JSON.stringify(project)}
      AND patient.dataset_generation == ${JSON.stringify(generation)}
      AND specimen.payload.subject.reference == CONCAT("Patient/", patient.id)
    RETURN { id: patient.id, _id: patient._id, reference: CONCAT("Patient/", patient.id) }
)
FILTER LENGTH(patients) == 1 AND FIRST(patients).id == ${JSON.stringify(referenceWitness.patientId)}
LET patient = FIRST(patients)
LET observations = (
  FOR edge IN fhir_edge
    FILTER edge._to == patient._id AND STARTS_WITH(edge._from, "Observation/")
      AND edge.label == "subject_Patient" AND edge.project == ${JSON.stringify(project)}
      AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT observationKey = edge._from
    LET observation = DOCUMENT(observationKey)
    FILTER observation != null AND observation.project == ${JSON.stringify(project)}
      AND observation.dataset_generation == ${JSON.stringify(generation)}
      AND observation.payload.subject.reference == patient.reference
    SORT observation._id
    LIMIT 32
    RETURN {
      id: observation.id,
      _id: observation._id,
      specimenReference: observation.payload.specimen.reference == null
        ? null
        : observation.payload.specimen.reference
    }
)
RETURN {
  specimen: { id: specimen.id, _id: specimen._id, patientReference: specimen.payload.subject.reference },
  patient,
  observations
}
`;
    const [exactReference] = rawQuery(exactReferenceMembershipQuery);
    assert(exactReference, 'The pinned Specimen→Patient route is no longer visible in the exact CDA project/generation');
    assert.equal(exactReference.specimen._id, referenceWitness.specimenKey);
    assert.equal(exactReference.specimen.id, referenceWitness.specimenId);
    assert.equal(exactReference.patient.id, referenceWitness.patientId);
    assert.equal(exactReference.specimen.patientReference, exactReference.patient.reference);
    const observations = exactReference.observations;
    assert.equal(observations.length, 31, 'Pinned nullable-reference witness must contain exactly 31 distinct linked Observation documents (32 is the overflow sentinel)');
    assert.equal(new Set(observations.map((observation) => observation._id)).size, 31,
      'The bounded raw oracle must deduplicate repeated source edges by terminal Observation identity');
    const observationReferences = observations.map((observation) => observation.specimenReference);
    assert(observationReferences.every((value) => value === null || (typeof value === 'string' && value.length > 0)),
      'Pinned Observation.specimen.reference values must be nonempty strings or null/missing references normalized to null by the raw oracle');
    const presentReferences = observationReferences.filter((value) => value !== null);
    assert.equal(presentReferences.length, 29, 'Pinned witness must retain 29 nonnull Observation.specimen.reference values');
    assert.equal(new Set(presentReferences).size, 29, 'Pinned witness must retain 29 distinct nonnull Observation.specimen.reference values');
    assert.equal(observationReferences.filter((value) => value === null).length, 2,
      'Pinned witness must retain two null/missing Observation.specimen.reference values');
    assert.equal(new Set(observationReferences).size, 30,
      'The two null observations are the fixture duplicate and must remain separate terminal-identity values in ALL');
    const witness = {
      category: 'many-distinct-nullable-references',
      patient: exactReference.patient,
      members: [{ id: exactReference.specimen.id, _id: exactReference.specimen._id,
        patientReference: exactReference.specimen.patientReference, patient: exactReference.patient }],
      observationIds: observations.map((observation) => observation.id),
      observationKeys: observations.map((observation) => observation._id),
      observationReferences,
      observationCount: observations.length,
      distinctObservationReferenceValues: [...new Set(presentReferences)].sort(),
      nullObservationReferenceCount: observationReferences.filter((value) => value === null).length,
      expectedContributorRows: observations.length,
    };
    witnesses = [witness];
    selectedMembers = witness.members;
    manyWitness = witness;
    report.oracle = {
      kind: 'pinned exact raw current-generation Specimen→Patient→Observation edge witness; no global or first-N search',
      searchBounds: {
        project, generation, pinnedSpecimenKey: referenceWitness.specimenKey,
        pinnedSpecimenId: referenceWitness.specimenId, pinnedPatientId: referenceWitness.patientId,
        maximumDistinctObservationDocuments: 32, overflowSentinel: '32 documents fails the exact 31-document witness assertion',
        edgeAndResourceProjectGenerationFilters: true,
      },
      requiredRelatedField: { resourceType: 'Observation', path: 'specimen.reference', logicalType: 'string', cardinality: 'optional_one' },
      exactMembershipQuery: exactReferenceMembershipQuery,
      exactScope: { project, generation, specimenKey: exactReference.specimen._id,
        specimenId: exactReference.specimen.id, patientKey: exactReference.patient._id,
        patientId: exactReference.patient.id, observationKeys: witness.observationKeys },
      fixtureAvailability: { requiredManyWitnessAvailable: true, observationCount: 31,
        presentDistinctValueCount: 29, nullOrMissingValueCount: 2, broadPatientScanPerformed: false },
      witnessSeeds: [witness], missingWitnessCategories: [],
      witnesses: [{ ...witness, members: witness.members.map((member) => ({ ...member })) }],
      selectedSpecimenIds: [exactReference.specimen.id],
      allProtocolSemantics: {
        terminalIdentity: '_id', perTerminalIdentityValues: true, deduplicatesByFieldValue: false,
        rawNullEntriesPreserved: true, expectedProtocolValues: observationReferences,
        sourceObservationKeysInExpectedOrder: witness.observationKeys,
        distinctProjectedValueCountIncludingNull: new Set(observationReferences).size,
        duplicateProjectedValueOccurrenceCount: observationReferences.length - new Set(observationReferences).size,
        duplicateNullValueOccurrenceCount: observationReferences.filter((value) => value === null).length - 1,
        note: 'The compiler sorts and distincts ALL by terminal _id, not by projected value. This fixture has 29 distinct nonnull strings plus two null/missing references normalized to null: its 31 identity rows have 30 distinct projected values, so preserving both nulls proves ALL does not collapse by field value.'
      },
    };
    report.referenceOracleAssertions = {
      sourceProject: project, sourceGeneration: generation,
      specimenId: exactReference.specimen.id, patientId: exactReference.patient.id,
      observationCount: observations.length, distinctObservationIdentityCount: new Set(observations.map(({ _id }) => _id)).size,
      nonnullReferenceCount: presentReferences.length, distinctNonnullReferenceCount: new Set(presentReferences).size,
      nullOrMissingReferenceCount: observationReferences.filter((value) => value === null).length,
      distinctProtocolValueCountIncludingNull: new Set(observationReferences).size,
      duplicateNullValueOccurrencesBeyondFirst: observationReferences.filter((value) => value === null).length - 1,
      rawProtocolExpectedValues: observationReferences,
      rawProtocolExpectedObservationKeys: witness.observationKeys,
      rawExpectedRelatedExpansionRows: observations.map((observation) =>
        [exactReference.specimen.id, exactReference.patient.id, observation.id]),
    };
  } else {
  const patientWitnessLimit = 2000;
  const specimensPerPatient = 1;
  const observationWitnessLimit = 11;
  const categories = statusFieldMode
    ? [{ name: 'many-distinct-statuses', predicate: 'LENGTH(observations) >= 2 AND LENGTH(observations) <= 10 AND LENGTH(distinctStatuses) >= 2' }]
    : [
      { name: 'zero', predicate: 'LENGTH(observations) == 0' },
      { name: 'one', predicate: 'LENGTH(observations) == 1' },
      { name: 'many', predicate: 'LENGTH(observations) >= 2 AND LENGTH(observations) <= 10' },
    ];
  const witnessQueries = {};
  const witnessSeeds = [];
  report.oracle = {
    kind: statusFieldMode
      ? 'bounded current-generation raw Patient→Specimen and Patient→Observation fhir_edge witness finder with a separate exact-membership reread of selected Specimens and primitive Observation.status values'
      : 'bounded current-generation raw fhir_edge witness finder plus a separate exact-membership join over selected Specimen keys',
    searchBounds: {
      patientCandidates: { project, generation, sortedBy: 'id', limit: patientWitnessLimit },
      selectedSpecimensPerPatient: specimensPerPatient,
      distinctObservationsPerCandidate: { deduplicateBy: '_id', cap: observationWitnessLimit, manyMaximum: 10, capSemantics: '11 distinct documents is a sentinel for more than 10 and is excluded; selected zero/one/many witnesses therefore have at most 10 and are reread exactly.' },
      requiredRelatedField: statusFieldMode ? { resourceType: 'Observation', path: 'status', logicalType: 'code', cardinality: 'optional_one' } : undefined,
      missingWitnessMeaning: 'A missing category is bounded fixture unavailability, not proof that no such witness exists elsewhere in the project or generation.',
    },
    witnessQueries, witnessSeeds: [], missingWitnessCategories: [],
  };
  for (const category of categories) {
    const query = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)} AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
    SORT scopedPatient.id
    LIMIT ${patientWitnessLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET specimens = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND e.from_type == "Specimen" AND e.to_type == "Patient"
        AND STARTS_WITH(e._from, "Specimen/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT specimenKey = e._from
      LET s = DOCUMENT(specimenKey)
      FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
        AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
        AND s.payload.subject.reference == CONCAT("Patient/", p.id)
      SORT s.id
      LIMIT ${specimensPerPatient}
      RETURN { id: s.id, _id: s._id, patientReference: s.payload.subject.reference }
  )
  FILTER LENGTH(specimens) == ${specimensPerPatient}
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND e.from_type == "Observation" AND e.to_type == "Patient"
        AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
        AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"
        ${statusFieldMode ? 'AND IS_STRING(o.payload.status)' : ''}
      SORT o.id
      LIMIT ${observationWitnessLimit}
      RETURN { id: o.id, _id: o._id${statusFieldMode ? ', status: o.payload.status' : ''} }
  )
  LET distinctStatuses = (
    FOR observation IN observations
      FILTER IS_STRING(observation.status)
      COLLECT status = observation.status
      SORT status
      RETURN status
  )
  FILTER ${category.predicate}
  SORT p.id
  LIMIT 1
  RETURN {
    patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
    specimens, observations: (FOR o IN observations SORT o.id RETURN o)
  }
`;
    witnessQueries[category.name] = query;
    const [seed] = rawQuery(query);
    if (!seed) {
      const boundedAbsence = statusFieldMode
        ? `No Patient with a linked Specimen and at least two distinct direct Observation.status values was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates. This is bounded fixture absence, not proof of global absence.`
        : `No ${category.name}-Observation witness with at least one linked Specimen was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates. The query selects one Specimen per Patient for the exact membership reread. This is bounded fixture absence, not proof that no such witness exists elsewhere in the project or generation.`;
      report.oracle.boundedAbsence = [...(report.oracle.boundedAbsence ?? []), {
        category: category.name, patientCandidateLimit: patientWitnessLimit,
        selectedSpecimensPerPatient: specimensPerPatient, meaning: boundedAbsence,
      }];
      report.oracle.missingWitnessCategories.push({ category: category.name, patientCandidateLimit: patientWitnessLimit,
        selectedSpecimensPerPatient: specimensPerPatient, meaning: boundedAbsence });
      continue;
    }
    const witnessSeed = { category: category.name, ...seed };
    witnessSeeds.push(witnessSeed);
    report.oracle.witnessSeeds.push(witnessSeed);
  }
  const manySeed = witnessSeeds.find((seed) => seed.category === (statusFieldMode ? 'many-distinct-statuses' : 'many'));
  manyWitness = manySeed;
  report.oracle.fixtureAvailability = {
    requiredForRepair: [statusFieldMode ? 'many-distinct-statuses' : 'many'], requiredManyWitnessAvailable: Boolean(manySeed),
    availableCategories: witnessSeeds.map((seed) => seed.category),
    missingCategories: report.oracle.missingWitnessCategories.map(({ category }) => category),
    completeZeroOneManyCoverage: !statusFieldMode && report.oracle.missingWitnessCategories.length === 0,
  };
  if (!manySeed) {
    throw fixtureUnavailableError(statusFieldMode
      ? `No bounded Patient→Specimen→Patient→Observation witness with at least two distinct Observation.status values was found among the first ${patientWitnessLimit} current-generation Patient candidates; this status ONE-to-ALL case is unavailable in this bounded fixture scan.`
      : `No many-Observation witness with at least one linked Specimen was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates; the ONE-to-ALL repair cannot be exercised. The witness query selects one Specimen per Patient.`);
  }
  assert.equal(new Set(witnessSeeds.map((seed) => seed.patient.id)).size, witnessSeeds.length,
    'Every available zero/one/many category must use an independent Patient witness');

  const selectedKeys = witnessSeeds.flatMap((seed) => seed.specimens.map((specimen) => specimen._id)).sort();
  const exactMembershipQuery = `
LET selectedKeys = ${JSON.stringify(selectedKeys)}
FOR s IN Specimen
  FILTER s._id IN selectedKeys AND s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
    AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
  LET patientEdge = FIRST(
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e.from_type == "Specimen" AND e.to_type == "Patient"
        AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)}
        AND e.dataset_generation == ${JSON.stringify(generation)}
      RETURN e
  )
  FILTER patientEdge != null
  LET p = DOCUMENT(patientEdge._to)
  FILTER p != null AND p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
    AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND e.from_type == "Observation" AND e.to_type == "Patient"
        AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      LET o = DOCUMENT(e._from)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
        AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"
        ${statusFieldMode ? 'AND IS_STRING(o.payload.status)' : ''}
      RETURN DISTINCT { id: o.id, _id: o._id${statusFieldMode ? ', status: o.payload.status' : ''} }
  )
  RETURN {
    id: s.id, _id: s._id, patientReference: s.payload.subject.reference,
    patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
    observationIds: (FOR o IN observations SORT o.id RETURN o.id),
    observationValues: (FOR o IN observations SORT o._id RETURN o),
    observationKeys: (FOR o IN observations SORT o._id RETURN o._id)
  }
`;
  const exactMembers = rawQuery(exactMembershipQuery);
  assert.deepEqual(exactMembers.map((member) => member._id).sort(), selectedKeys,
    `The independent exact-membership oracle did not resolve exactly the ${selectedKeys.length} selected Specimens`);
  witnesses = witnessSeeds.map((seed) => {
    const members = exactMembers.filter((member) => member.patient.id === seed.patient.id).sort((a, b) => a.id.localeCompare(b.id));
    assert.equal(members.length, specimensPerPatient);
    assert(members.every((member) => member.patientReference === seed.patient.reference));
    const observationValues = [...new Map(members.flatMap((member) => member.observationValues)
      .map((observation) => [observation._id, observation])).values()].sort((a, b) => a._id.localeCompare(b._id));
    const observationIds = observationValues.map((observation) => observation.id);
    assert.deepEqual([...observationIds].sort((a, b) => a.localeCompare(b)),
      seed.observations.map((observation) => observation.id).sort((a, b) => a.localeCompare(b)),
      `${seed.category} finder and exact-membership joins disagree`);
    if (statusFieldMode) {
      assert(observationValues.every((observation) => typeof observation.status === 'string' && observation.status.length > 0),
        `${seed.category} witness contains an Observation without a scalar status code`);
      assert.deepEqual(observationValues.map(({ id, status }) => ({ id, status })).sort((a, b) => a.id.localeCompare(b.id)),
        seed.observations.map(({ id, status }) => ({ id, status })).sort((a, b) => a.id.localeCompare(b.id)),
        `${seed.category} finder and exact-membership status reads disagree`);
    }
    const expectedCount = seed.category === 'zero' ? 0 : seed.category === 'one' ? 1 : observationIds.length;
    assert.equal(observationIds.length, expectedCount, `${seed.category} witness cardinality changed`);
    const observationStatuses = observationValues.map((observation) => observation.status);
    return {
      category: seed.category, patient: seed.patient, members,
      observationIds, observationKeys: observationValues.map((observation) => observation._id), observationCount: observationIds.length,
      observationStatuses,
      distinctStatusValues: [...new Set(observationStatuses)].sort(),
      expectedContributorRows: members.length * Math.max(1, observationIds.length),
    };
  });
  manyWitness = witnesses.find((witness) => witness.category === (statusFieldMode ? 'many-distinct-statuses' : 'many'));
  assert(manyWitness && manyWitness.observationIds.length > 1, 'The independent many witness must predict an actual ONE disagreement');
  selectedMembers = witnesses.flatMap((witness) => witness.members);
  Object.assign(report.oracle, {
    exactMembershipQuery, selectedSpecimenKeys: selectedKeys,
    exactMembershipScope: { project, generation, rootResourceType: 'Specimen', selectedSpecimenCount: selectedKeys.length,
      selectedSpecimensPerPatient: specimensPerPatient,
      maximumExpectedDistinctObservationsPerPatient: 10, observationSetsReadCompletelyForSelectedAtMostTenWitnesses: true,
      patientIds: witnesses.map((witness) => witness.patient.id) },
    witnesses: witnesses.map(({ category, patient, members, observationIds, observationKeys, observationCount, observationStatuses, distinctStatusValues, expectedContributorRows }) => ({
      category, patient, observationCount, observationIds, observationKeys,
      ...(statusFieldMode ? { observationStatuses, distinctStatusValues } : {}),
      members: members.map(({ id, _id, patientReference }) => ({ id, _id, patientReference })), expectedContributorRows,
    })),
    selectedSpecimenIds: selectedMembers.map((member) => member.id),
  });
  }

  const rootTitle = `${rootResourceType} ID`;
  const tableTitle = zeroObservationMode ? 'Zero Observation related ID ONE to ALL QA' : quantityFieldMode ? 'Related Observation Quantity ONE to ALL QA' : referenceFieldMode ? 'Related Observation specimen reference QA' : statusFieldMode ? 'Related Observation status code QA' : 'Related ID ONE to ALL QA';
  const createdExplorer = await api(explorerCollection, { name: requestedExplorerName, title: tableTitle });
  const createdScope = createdExplorerScope(project, createdExplorer);
  if (quantityFieldMode) browserProject = createdScope.project;
  explorer = createdScope.explorerId;
  base = createdScope.authoringBase;
  browserAuthoringBase = createdScope.browserAuthoringBase;
  report.explorer = explorer;
  report.explorerRoot = createdScope.explorerRoot;
  officialRequestCapture = cda.captureRequests(createdScope.browserExplorerRoot);
  assert.notEqual(explorer, protectedExplorer, 'The server-assigned Explorer must be a fresh QA Explorer');
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation);
  if (quantityFieldMode) assertQuantityCandidate(builder);
  else if (statusFieldMode) assertStatusCandidate(builder);
  else if (referenceFieldMode) assertReferenceCandidate(builder);
  else assertRelatedIdCandidate(builder);
  const rootNode = builder.catalog.nodes.find((node) => node.resourceType === rootResourceType && node.rowRootEligible);
  assert(rootNode, `Current catalog has no authorized ${rootResourceType} row root`);
  const created = await command([{ type: 'CREATE_TABLE', title: tableTitle, rootNodeId: rootNode.nodeId }]);
  assert.equal(created.workspace.documents.length, 1);
  outputId = created.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find((candidate) => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
  assert(idField, `The current ${rootResourceType} catalog must expose its id field`);
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: rootTitle }]);
  const selection = await api(`${base.replace('/authoring/v2', '')}/selections`, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: selectedMembers.map((member) => ({ project, generation, resourceType: rootResourceType, id: member.id })) } },
  });
  assert.equal(selection.memberCount, selectedMembers.length, 'Population selection must preserve the exact independent witness membership');
  const routes = await api(`${base}/population-routes`, { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'Exact selected Specimen members have no direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  report.ownedWorkspace = { explorer, outputId, selectionRevisionId: selection.id, memberCount: selection.memberCount };
  const sourceRows = selectedMembers.map((member) => [member.id]);
  const groupedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows)]).sort((a, b) => a[0].localeCompare(b[0]));
  const relatedRows = basicMode ? sourceRows : witnesses.flatMap((witness) => witness.members.flatMap((member) => witness.observationIds.length
    ? witness.observationIds.map((observationId) => zeroObservationMode
      ? [member.id, observationId]
      : [member.id, member.patient.id, observationId])
    : [zeroObservationMode ? [member.id, '—'] : [member.id, member.patient.id, '—']])).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  if (referenceFieldMode) {
    const actualRawRelatedRows = relatedRows.map((row) => JSON.stringify(row)).sort();
    const expectedRawRelatedRows = report.referenceOracleAssertions.rawExpectedRelatedExpansionRows
      .map((row) => JSON.stringify(row)).sort();
    assert.deepEqual(actualRawRelatedRows, expectedRawRelatedRows,
      'The shared related-row oracle must preserve the exact pinned Specimen, Patient, and Observation identities from the independent raw membership query');
    assert.equal(relatedRows.length, report.referenceOracleAssertions.observationCount,
      'The shared related-row oracle must produce one row for each distinct raw Observation identity');
    report.referenceOracleAssertions.sharedRelatedRowsMatchRawRoute = true;
  }

  await startNativeCapture();
  const sourcePreview = await openTable(sourceRows, 'reload-exact-available-cardinality-source-members');
  const sourceBaseline = await api(`${base}/builder`);
  assert.equal(doc(sourceBaseline).population.selectionRevisionId, selection.id);
  assert.deepEqual(doc(sourceBaseline).columns.map((column) => column.label), [rootTitle]);

  if (zeroStartingRootRoundtrip) {
    const patientId = witnesses[0].patient.id;
    const initialDocument = structuredClone(doc(sourceBaseline));
    const initialWorkspace = structuredClone(sourceBaseline.workspace);
    const initialDraftVersion = sourceBaseline.draftVersion;
    const initialDraftDigest = sourceBaseline.draftDigest;
    const visibleHeaders = ['PATIENT ID'];
    assert.equal(initialDocument.rootResourceType, 'Patient');
    assert.deepEqual(initialDocument.population.route, []);
    assert.equal(initialDocument.population.selectionRevisionId, selection.id);
    assert.equal(sourcePreview.rowCount, 1);

    const selectionCollection = base.replace('/authoring/v2', '').replace(/\/$/, '') + '/selections';
    const exactSelection = async () => {
      const saved = await api(`${selectionCollection}/${encodeURIComponent(selection.id)}?limit=100`);
      assert.equal(saved.revision?.id, selection.id);
      assert.equal(saved.revision?.project, project);
      assert.equal(saved.revision?.generation, generation);
      assert.equal(saved.revision?.resourceType, 'Patient');
      assert.equal(saved.revision?.scopeDigest, selection.scopeDigest);
      assert.equal(saved.revision?.memberCount, 1);
      const members = (saved.members ?? []).map(member => member.ref).map(ref => ({
        project: ref?.project, generation: ref?.generation, resourceType: ref?.resourceType, id: ref?.id,
      })).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
      assert.deepEqual(members, [{ project, generation, resourceType: 'Patient', id: patientId }],
        'The immutable selected Patient collection must contain exactly the raw zero-Observation witness');
      return saved;
    };
    const selectionAtStart = await exactSelection();
    const rowHeadersAndRows = async () => inspectPage(nativePage, () => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return {
        ready: Boolean(table) && !document.body.innerText.includes('Loading your table…')
          && !document.body.innerText.includes('Preview failed:'),
        headers: [...(table?.querySelectorAll('[role="columnheader"]') ?? [])]
          .map(cell => cell.innerText.trim().toUpperCase()),
        rows: [...(table?.querySelectorAll('[role="row"]') ?? [])].slice(1)
          .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
        columnCount: table?.getAttribute('aria-colcount') ?? null,
      };
    });
    const remaining = deadline => {
      const value = deadline - Date.now();
      assert(value > 0, 'The starting-root action exceeded its five-second action-to-render deadline');
      return value;
    };
    const waitGrid = async (expectedRows, deadline) => {
      await waitForObservable(nativePage, ({ headers, rows }) => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        if (!table || document.body.innerText.includes('Loading your table…')
          || document.body.innerText.includes('Preview failed:')
          || table.getAttribute('aria-colcount') !== String(headers.length)) return false;
        const actualHeaders = [...table.querySelectorAll('[role="columnheader"]')]
          .map(cell => cell.innerText.trim().toUpperCase());
        const actualRows = [...table.querySelectorAll('[role="row"]')].slice(1)
          .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
        return JSON.stringify(actualHeaders) === JSON.stringify(headers)
          && JSON.stringify(actualRows) === JSON.stringify(rows);
      }, { headers: visibleHeaders, rows: expectedRows }, remaining(deadline));
      const grid = await rowHeadersAndRows();
      assert.deepEqual(grid.headers, visibleHeaders);
      assert.deepEqual(grid.rows, expectedRows);
      return grid;
    };
    const waitProposalPreview = async (assessmentEntry, expectedRows, deadline) => {
      const receiptId = assessmentEntry.response?.candidateReceiptId;
      assert.equal(typeof receiptId, 'string', 'A READY row assessment must name its exact candidate preview receipt');
      await waitForObservable(nativePage, ({ receiptId, outputId, headers, rows, expectEmpty }) => {
        const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
        const preview = panel?.querySelector('[data-testid="construction-proposal-preview"]');
        const table = preview?.querySelector('table');
        if (!panel || preview?.dataset.previewStatus !== 'ready'
          || preview.dataset.previewReceiptId !== receiptId
          || preview.dataset.previewOutputId !== outputId || !table) return false;
        const actualHeaders = [...table.querySelectorAll('thead th')]
          .map(cell => (cell.querySelector('span')?.textContent ?? cell.textContent ?? '').trim().toUpperCase());
        const actualRows = [...panel.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
          .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
        const emptyMessage = panel.innerText.includes('No rows matched this proposal.');
        const apply = [...panel.querySelectorAll('button')]
          .find(button => button.textContent?.trim() === 'Apply row change');
        return Boolean(apply && !apply.disabled)
          && JSON.stringify(actualHeaders) === JSON.stringify(headers)
          && JSON.stringify(actualRows) === JSON.stringify(rows)
          && emptyMessage === expectEmpty;
      }, { receiptId, outputId, headers: visibleHeaders, rows: expectedRows, expectEmpty: expectedRows.length === 0 }, remaining(deadline));
      const snapshot = await inspectPage(nativePage, () => {
        const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
        const preview = panel?.querySelector('[data-testid="construction-proposal-preview"]');
        const table = preview?.querySelector('table');
        return {
          receiptId: preview?.dataset.previewReceiptId ?? null,
          outputId: preview?.dataset.previewOutputId ?? null,
          headers: [...(table?.querySelectorAll('thead th') ?? [])]
            .map(cell => (cell.querySelector('span')?.textContent ?? cell.textContent ?? '').trim().toUpperCase()),
          rows: [...(panel?.querySelectorAll('[data-testid="construction-proposal-preview-row"]') ?? [])]
            .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
          emptyMessage: panel?.innerText.includes('No rows matched this proposal.') ?? false,
        };
      });
      assert.deepEqual(snapshot, {
        receiptId, outputId, headers: visibleHeaders, rows: expectedRows,
        emptyMessage: expectedRows.length === 0,
      });
      return snapshot;
    };
    const assessObservationRoot = async (currentDraft, label) => {
      await cda.action(`Open row settings for ${label}`, nativePage.locator('[data-testid="construction-rows-settings-trigger"]'),
        target => target.click({ timeout: ACTION_TIMEOUT_MS }), {
          timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
          after: () => waitForObservable(nativePage,
            () => Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]:not(:disabled)')),
            ACTION_TIMEOUT_MS),
        });
      const select = nativePage.locator('[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]');
      const options = await select.locator('option').evaluateAll(items => items.map(option => ({
        value: option.value, label: option.textContent?.trim() ?? '', disabled: option.disabled,
      })));
      const observationChoice = choosePatientLinkedObservationOption(options);
      const observationNodes = currentDraft.catalog.nodes.filter(node => node.resourceType === 'Observation'
        && node.rowRootEligible && node.populated);
      assert.equal(observationNodes.length, 1, 'The scoped catalog must expose exactly one populated eligible Observation row root');
      const expected = {
        outputId, snapshotToken: currentDraft.catalog.snapshotToken, draftVersion: currentDraft.draftVersion,
        draftDigest: currentDraft.draftDigest, rootNodeId: observationNodes[0].nodeId,
      };
      const fromIndex = nativeCapture.requests.length;
      let assessmentEntry;
      const started = Date.now();
      const deadline = started + ACTION_TIMEOUT_MS;
      await cda.action(`Select Observation rows for ${label}`, select,
        target => target.selectOption(observationChoice.value, { timeout: remaining(deadline) }), {
          timeout: remaining(deadline), budget: ACTION_TIMEOUT_MS,
          after: async () => {
            assessmentEntry = await waitNative(entry =>
              matchesPlainPatientObservationAssessment(entry, expected)
                || matchesPlainPatientObservationAssessment(entry, expected, { status: 'BLOCKED' }),
            fromIndex, remaining(deadline));
            if (assessmentEntry.response.status === 'BLOCKED') {
              await waitForObservable(nativePage,
                () => Boolean(document.getElementById('row-change-repair-title')?.closest('section')),
                remaining(deadline));
            } else {
              await waitProposalPreview(assessmentEntry, [], deadline);
            }
          },
        });
      record(`select-observation-root-${label}`, started, {
        status: assessmentEntry.response.status, rootNodeId: assessmentEntry.request.rootNodeId,
      });
      assert.equal(assessmentEntry.request.rootNodeId, observationNodes[0].nodeId);
      if (assessmentEntry.response.status === 'BLOCKED') {
        const repairPanel = await inspectPage(nativePage, () => {
          const panel = document.getElementById('row-change-repair-title')?.closest('section');
          return { buttons: [...(panel?.querySelectorAll('button') ?? [])]
            .map(button => ({ text: button.innerText.trim(), disabled: button.disabled })) };
        });
        const repair = choosePatientSubjectObservationRepairButton(repairPanel.buttons);
        const repairFromIndex = nativeCapture.requests.length;
        const repairStarted = Date.now();
        const repairDeadline = repairStarted + ACTION_TIMEOUT_MS;
        await cda.action(`Preserve the selected Patient route for ${label}`,
          nativePage.getByRole('button', { name: repair.text, exact: true }),
          target => target.click({ timeout: remaining(repairDeadline) }), {
            timeout: remaining(repairDeadline), budget: ACTION_TIMEOUT_MS,
            after: async () => {
              assessmentEntry = await waitNative(entry => matchesPlainPatientObservationAssessment(entry, expected),
                repairFromIndex, remaining(repairDeadline));
              await waitProposalPreview(assessmentEntry, [], repairDeadline);
            },
          });
        record(`repair-observation-root-${label}`, repairStarted, {
          status: assessmentEntry.response.status, rootNodeId: assessmentEntry.request.rootNodeId,
        });
      }
      assert.equal(assessmentEntry.response.status, 'READY');
      assert.equal(assessmentEntry.request.rootNodeId, observationNodes[0].nodeId);
      assert(!nativeCapture.requests.slice(fromIndex).some(entry => entry.path.endsWith('/commands')),
        'Selecting Observation and resolving its route must not change the saved table before Apply');
      return assessmentEntry;
    };
    const applyRootAssessment = async (assessmentEntry, expectedRows, label) => {
      const fromIndex = nativeCapture.requests.length;
      const started = Date.now();
      const deadline = started + ACTION_TIMEOUT_MS;
      let commandEntry;
      let previewEntry;
      await cda.action(`Apply ${label}`, nativePage.getByRole('button', { name: 'Apply row change', exact: true }),
        target => target.click({ timeout: remaining(deadline) }), {
          timeout: remaining(deadline), budget: ACTION_TIMEOUT_MS,
          after: async () => {
            commandEntry = await waitNative(entry => entry.path.endsWith('/commands')
              && entry.request?.commands?.some(command => command.type === 'APPLY_TABLE_ROOT_REBASE'),
            fromIndex, remaining(deadline));
            assert.equal(commandEntry.status, 200, `${label} root change command failed: ${JSON.stringify(commandEntry.response)}`);
            previewEntry = await waitNative(entry => entry.path.endsWith('/preview')
              && entry.request?.outputId === outputId && entry.response !== undefined,
            fromIndex, remaining(deadline));
            assert.equal(previewEntry.status, 200, `${label} table preview failed: ${JSON.stringify(previewEntry.response)}`);
            assert.equal(previewEntry.response.rowCount, expectedRows.length);
            assert.equal(previewEntry.response.rows.length, expectedRows.length);
            await waitGrid(expectedRows, deadline);
          },
        });
      record(`apply-${label}`, started, {
        commandId: commandEntry.request.commandId, responseStatus: commandEntry.status,
        receiptId: previewEntry.response.receiptId, rowCount: previewEntry.response.rowCount,
      });
      return previewEntry.response;
    };
    const openRoundtripTable = async (name, expectedRows) => {
      const fromIndex = nativeCapture.requests.length;
      const tableStarted = Date.now();
      const deadline = tableStarted + ACTION_TIMEOUT_MS;
      let preview;
      await gotoPage(nativePage, `${uiOrigin}/?project=${encodeURIComponent(browserProject)}&explorer=${explorer}&mode=builder`);
      await waitForVisible(nativePage, `[data-testid="construction-table-${outputId}"]`, remaining(deadline));
      await cda.action(`Select table for ${name}`, nativePage.locator(`[data-testid="construction-table-${outputId}"]`),
        target => target.click({ timeout: remaining(deadline) }), {
          timeout: remaining(deadline), budget: ACTION_TIMEOUT_MS,
          after: async () => {
            preview = await waitNative(entry => entry.path.endsWith('/preview')
              && entry.request?.outputId === outputId && entry.response !== undefined,
            fromIndex, remaining(deadline));
            assert.equal(preview.status, 200, `${name} Preview failed: ${JSON.stringify(preview.response)}`);
            assert.equal(preview.response.rowCount, expectedRows.length);
            assert.equal(preview.response.rows.length, expectedRows.length);
            await waitGrid(expectedRows, deadline);
          },
        });
      record(name, tableStarted, { receiptId: preview.response.receiptId, rowCount: preview.response.rowCount });
      return preview.response;
    };
    const assertPopulationAndSelection = async (document, expectedRoute) => {
      assert.equal(document.population?.selectionRevisionId, selection.id);
      assert.deepEqual(document.population?.route, expectedRoute);
      const saved = await exactSelection();
      assert.equal(saved.revision.id, selectionAtStart.revision.id);
      assert.equal(saved.revision.scopeDigest, selectionAtStart.revision.scopeDigest);
      return saved;
    };

    assert.equal(initialDocument.rootResourceType, 'Patient');
    assert.equal(sourcePreview.rowCount, 1);
    const initialGrid = await rowHeadersAndRows();
    assert.deepEqual(initialGrid, { ready: true, headers: visibleHeaders, rows: sourceRows, columnCount: '1' });
    assert.deepEqual(initialGrid.rows, [[patientId]], 'The initial Patient root must render the exact selected raw witness');
    const observationAssessment = await assessObservationRoot(sourceBaseline, 'cancel-proposal');
    assert.equal(observationAssessment.response.status, 'READY');
    const previewPanel = await inspectPage(nativePage, () => ({
      receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewReceiptId,
      headers: [...(document.querySelector('[data-testid="construction-proposal-preview"] table')?.querySelectorAll('thead th') ?? [])]
        .map(cell => (cell.querySelector('span')?.textContent ?? cell.textContent ?? '').trim().toUpperCase()),
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].length,
      emptyMessage: document.querySelector('[data-testid="row-change-preview-panel"]')?.innerText.includes('No rows matched this proposal.') ?? false,
    }));
    const cancelFromIndex = nativeCapture.requests.length;
    const cancelStarted = Date.now();
    const cancelDeadline = cancelStarted + ACTION_TIMEOUT_MS;
    await cda.action('Cancel Observation root preview', nativePage.getByRole('button', { name: 'Keep current rows', exact: true }),
      target => target.click({ timeout: remaining(cancelDeadline) }), {
        timeout: remaining(cancelDeadline), budget: ACTION_TIMEOUT_MS,
        after: async () => {
          await waitForObservable(nativePage, ({ headers, rows }) => {
            const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
            const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
            if (panel || !table || document.body.innerText.includes('Loading your table…')) return false;
            const actualHeaders = [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim().toUpperCase());
            const actualRows = [...table.querySelectorAll('[role="row"]')].slice(1)
              .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
            return JSON.stringify(actualHeaders) === JSON.stringify(headers) && JSON.stringify(actualRows) === JSON.stringify(rows);
          }, { headers: visibleHeaders, rows: sourceRows }, remaining(cancelDeadline));
        },
      });
    record('cancel-zero-observation-root-proposal', cancelStarted, { candidateReceiptId: previewPanel.receiptId,
      candidateHeaders: previewPanel.headers, candidateRowCount: previewPanel.rows, emptyMessage: previewPanel.emptyMessage });
    assert.deepEqual(previewPanel, { receiptId: observationAssessment.response.candidateReceiptId,
      headers: visibleHeaders, rows: 0, emptyMessage: true });
    assert(!nativeCapture.requests.slice(cancelFromIndex).some(entry => entry.path.endsWith('/commands')),
      'Canceling the root proposal must not save a Builder command');
    builder = await api(`${base}/builder`);
    assert.equal(builder.draftVersion, initialDraftVersion);
    assert.equal(builder.draftDigest, initialDraftDigest);
    assert.deepEqual(builder.workspace, initialWorkspace,
      'Canceling the Observation root preview must leave the complete selected Patient workspace unchanged');
    assert.deepEqual(doc().population.route, []);
    await assertPopulationAndSelection(doc(), []);
    const cancelEvidence = { workspaceUnchanged: true, draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest, populationRoute: doc().population.route, selectionRevisionId: selection.id };

    const cancelledReloadPreview = await openRoundtripTable('reload-patient-after-root-cancel', sourceRows);
    builder = await api(`${base}/builder`);
    assert.deepEqual(doc().construction, initialDocument.construction);
    assert.deepEqual(doc().columns, initialDocument.columns);
    assert.deepEqual(doc().population, initialDocument.population);
    assert.equal(doc().rootResourceType, 'Patient');
    assert.equal(cancelledReloadPreview.rows[0][initialDocument.columns[0].column], patientId);
    await assertPopulationAndSelection(doc(), []);

    const applyAssessment = await assessObservationRoot(builder, 'apply-observation-root');
    const observationPreview = await applyRootAssessment(applyAssessment, [], 'observation-root-zero-rows');
    builder = await api(`${base}/builder`);
    const observationDocument = structuredClone(doc());
    assert.equal(observationDocument.rootResourceType, 'Observation');
    assert.equal(directObservationRootPreservesPatientState(initialDocument, observationDocument), true,
      'Changing to Observation rows must retain the exact Patient ID column, construction, and selected collection');
    assert.equal(observationDocument.columns.length, 1);
    assert.equal(observationDocument.columns[0].label, 'Patient ID');
    assert.equal(observationDocument.population.selectionRevisionId, selection.id);
    assert.equal(observationDocument.population.route.length, 1);
    const patientOccurrence = observationDocument.route?.children?.find(node => node.resourceType === 'Patient');
    assert(patientOccurrence, 'Observation root must retain the Patient route occurrence used by its collection');
    const patientEdge = sourceBaseline.catalog.edges.find(edge => edge.edgeId === patientOccurrence.catalogEdgeId);
    assert(patientEdge && patientEdge.label === 'subject_Patient',
      'Observation root must use the exact Patient subject edge from the scoped catalog');
    const observationPopulationRoute = [{ resourceType: 'Patient', relationship: patientEdge.label,
      catalogEdgeId: patientEdge.edgeId,
      ...(typeof patientEdge.storageDirection === 'string' && patientEdge.storageDirection
        ? { storageDirection: patientEdge.storageDirection } : {}) }];
    assert.deepEqual(observationDocument.population.route, observationPopulationRoute);
    assert.deepEqual(observationPreview.rows, []);
    const observationRootEvidence = {
      rootResourceType: observationDocument.rootResourceType,
      selectionRevisionId: observationDocument.population.selectionRevisionId,
      populationRoute: observationDocument.population.route.map(({ resourceType, relationship }) => ({ resourceType, relationship })),
      headers: visibleHeaders, previewRowCount: observationPreview.rowCount, rows: [],
    };
    const observationReloadPreview = await openRoundtripTable('reload-observation-root-zero-rows', []);
    builder = await api(`${base}/builder`);
    const observationReloadDocument = doc();
    assert.equal(observationReloadDocument.rootResourceType, 'Observation');
    assert.deepEqual(observationReloadDocument.columns, observationDocument.columns);
    assert.deepEqual(observationReloadDocument.construction, observationDocument.construction);
    assert.deepEqual(observationReloadDocument.population, observationDocument.population);
    assert.deepEqual(observationReloadPreview.rows, []);
    await assertPopulationAndSelection(observationReloadDocument, observationPopulationRoute);
    const observationReloadEvidence = {
      rootResourceType: observationReloadDocument.rootResourceType,
      selectionRevisionId: observationReloadDocument.population.selectionRevisionId,
      populationRoute: observationReloadDocument.population.route.map(({ resourceType, relationship }) => ({ resourceType, relationship })),
      headers: visibleHeaders, previewRowCount: observationReloadPreview.rowCount, rows: [],
    };

    await cda.action('Open row settings to restore Patient rows', nativePage.locator('[data-testid="construction-rows-settings-trigger"]'),
      target => target.click({ timeout: ACTION_TIMEOUT_MS }), {
        timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
        after: () => waitForObservable(nativePage,
          () => Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]:not(:disabled)')),
          ACTION_TIMEOUT_MS),
      });
    const patientSelect = nativePage.locator('[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]');
    const patientOptions = await patientSelect.locator('option').evaluateAll(items => items.map(option => ({
      value: option.value, label: option.textContent?.trim() ?? '', disabled: option.disabled,
    })));
    const patientChoices = patientOptions.filter(option => !option.disabled && /^Patient\b/i.test(option.label));
    assert.equal(patientChoices.length, 1, 'The current Observation table must expose exactly one enabled Patient root choice');
    const patientRouteOccurrence = observationReloadDocument.route?.children?.find(node => node.resourceType === 'Patient');
    assert(patientRouteOccurrence?.occurrenceId, 'Observation root must contain a Patient occurrence for restoration assessment');
    const restorationExpected = {
      outputId, snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest, currentRootResourceType: 'Observation',
      candidateRootResourceType: 'Patient', rootOccurrenceId: patientRouteOccurrence.occurrenceId,
    };
    const restoreFromIndex = nativeCapture.requests.length;
    let restorationEntry;
    const restoreStarted = Date.now();
    const restoreDeadline = restoreStarted + ACTION_TIMEOUT_MS;
    await cda.action('Select Patient root for restoration', patientSelect,
      target => target.selectOption(patientChoices[0].value, { timeout: remaining(restoreDeadline) }), {
        timeout: remaining(restoreDeadline), budget: ACTION_TIMEOUT_MS,
        after: async () => {
          restorationEntry = await waitNative(entry => matchesRootRebaseAssessment(entry, restorationExpected),
          restoreFromIndex, remaining(restoreDeadline));
          if (restorationEntry.response.status === 'BLOCKED') {
            await waitForObservable(nativePage,
              () => Boolean(document.getElementById('row-change-repair-title')?.closest('section')),
              remaining(restoreDeadline));
          } else {
            await waitProposalPreview(restorationEntry, [[patientId]], restoreDeadline);
          }
        },
      });
    record('select-patient-root-for-restoration', restoreStarted, { status: restorationEntry.response.status });
    if (restorationEntry.response.status === 'BLOCKED') {
      const panel = await inspectPage(nativePage, () => {
        const rootPanel = document.getElementById('row-change-repair-title')?.closest('section');
        return { buttons: [...(rootPanel?.querySelectorAll('button') ?? [])]
          .map(button => ({ text: button.innerText.trim(), disabled: button.disabled })) };
      });
      const repair = choosePatientObservationSubjectRestorationButton(panel.buttons);
      const repairStart = nativeCapture.requests.length;
      const repairStarted = Date.now();
      const repairDeadline = repairStarted + ACTION_TIMEOUT_MS;
      await cda.action('Restore Patient Subject population route', nativePage.getByRole('button', { name: repair.text, exact: true }),
        target => target.click({ timeout: remaining(repairDeadline) }), {
          timeout: remaining(repairDeadline), budget: ACTION_TIMEOUT_MS,
          after: async () => {
            restorationEntry = await waitNative(entry => matchesRootRebaseAssessment(entry, restorationExpected)
              && entry.response.status === 'READY', repairStart, remaining(repairDeadline));
            await waitProposalPreview(restorationEntry, [[patientId]], repairDeadline);
          },
        });
      record('repair-patient-restoration-route', repairStarted, { status: restorationEntry.response.status });
    }
    assert.equal(restorationEntry.response.status, 'READY');
    await applyRootAssessment(restorationEntry, [[patientId]], 'restore-patient-root-row');
    builder = await api(`${base}/builder`);
    const restoredDocument = structuredClone(doc());
    assert.equal(restoredDocument.rootResourceType, 'Patient');
    assert.deepEqual(restoredDocument.construction, initialDocument.construction);
    assert.deepEqual(restoredDocument.columns, initialDocument.columns);
    assert.deepEqual(restoredDocument.population, initialDocument.population);
    assert.equal(restoredDocument.route?.occurrenceId, initialDocument.route?.occurrenceId);
    assert.equal(restoredDocument.route?.resourceType, initialDocument.route?.resourceType);
    await assertPopulationAndSelection(restoredDocument, []);
    const restoredPatientEvidence = {
      rootResourceType: restoredDocument.rootResourceType,
      selectionRevisionId: restoredDocument.population.selectionRevisionId,
      populationRoute: restoredDocument.population.route,
      headers: visibleHeaders, previewRowCount: 1, rows: [[patientId]],
    };
    const restoredReloadPreview = await openRoundtripTable('reload-restored-patient-root-row', [[patientId]]);
    builder = await api(`${base}/builder`);
    const restoredReloadDocument = doc();
    assert.equal(restoredReloadDocument.rootResourceType, 'Patient');
    assert.deepEqual(restoredReloadDocument.construction, initialDocument.construction);
    assert.deepEqual(restoredReloadDocument.columns, initialDocument.columns);
    assert.deepEqual(restoredReloadDocument.population, initialDocument.population);
    assert.equal(restoredReloadDocument.route?.occurrenceId, initialDocument.route?.occurrenceId);
    assert.equal(restoredReloadDocument.route?.resourceType, initialDocument.route?.resourceType);
    assert.equal(restoredReloadPreview.rows[0][initialDocument.columns[0].column], patientId);
    await assertPopulationAndSelection(restoredReloadDocument, []);
    const restoredReloadEvidence = {
      rootResourceType: restoredReloadDocument.rootResourceType,
      selectionRevisionId: restoredReloadDocument.population.selectionRevisionId,
      populationRoute: restoredReloadDocument.population.route,
      headers: visibleHeaders, previewRowCount: restoredReloadPreview.rowCount, rows: [[patientId]],
    };
    const [finalWitness] = rereadZeroObservationWitness();
    assert(finalWitness, 'The bounded Patient witness must still exist after the starting-root roundtrip');
    assert.equal(finalWitness.matchingObservationCount, 0);
    assert.equal(finalWitness.patient.id, witnesses[0].patient.id);
    assert.equal(finalWitness.patient._id, witnesses[0].patient._id);
    report.oracle.finalScopedRereadMatched = true;
    report.zeroObservationStartingRootRoundtrip = {
      selectionRevisionId: selection.id,
      initial: { rootResourceType: initialDocument.rootResourceType,
        selectionRevisionId: initialDocument.population.selectionRevisionId,
        populationRoute: initialDocument.population.route,
        headers: initialGrid.headers, previewRowCount: sourcePreview.rowCount, rows: sourceRows },
      cancel: cancelEvidence,
      observationRoot: observationRootEvidence,
      observationReload: observationReloadEvidence,
      restoredPatient: restoredPatientEvidence,
      restoredReload: restoredReloadEvidence,
      selectionSeed: { project, generation, resourceType: 'Patient', patientId,
        selectionRevisionId: selectionAtStart.revision.id, scopeDigest: selection.scopeDigest,
        memberCount: selectionAtStart.revision.memberCount },
      finalRawWitness: { patient: finalWitness.patient, matchingObservationCount: finalWitness.matchingObservationCount },
    };
    assert.deepEqual(nativeCapture.requests.filter(entry => entry.status >= 400), [],
      'The zero-Observation root roundtrip must not produce unexpected native HTTP errors');
    assert.deepEqual(report.errors.filter(error => error.expectedValidation !== true), [],
      'The zero-Observation root roundtrip must not produce unexpected browser errors');
    report.browserDiagnostics = cda.diagnostics;
    assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
    report.relatedFieldLifecycle = 'passed';
    report.status = 'passed';
  } else {
  let pipelineRows = sourceRows;
  const chain = basicMode ? [] : zeroObservationMode
    ? [{ from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' }]
    : [
      { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
      { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' },
  ];
  for (let index = 0; index < chain.length; index += 1) {
    const hop = chain[index];
    const firstSpecimenPatientHop = index === 0 && !zeroObservationMode;
    const expectedRows = firstSpecimenPatientHop
      ? selectedMembers.map((member) => [member.id, member.patient.id])
      : relatedRows;
    const witnessesForHop = firstSpecimenPatientHop ? selectedMembers : witnesses;
    await expand(hop, witnessesForHop, expectedRows, zeroObservationMode ? doc(sourceBaseline).population : undefined);
    pipelineRows = expectedRows;
  }
  assert.equal(pipelineRows.length, relatedRows.length);
  const expandedDoc = doc();
  const populationBeforeGroup = structuredClone(expandedDoc.population);
  const patientExpansion = basicMode ? undefined : expandedDoc.construction?.steps.find((step) => step.operation.kind === 'RELATED_EXPAND' && step.operation.relatedExpand.targetResourceType === 'Patient');
  const patientGroupOutput = basicMode || zeroObservationMode
    ? expandedDoc.columns.find((column) => column.label === rootTitle)
    : patientExpansion?.outputs.find((output) => output.label === 'Patient FHIR resource ID');
  assert(patientGroupOutput, basicMode ? 'The Patient root ID source must remain available as the Group key' : 'The first related expansion must retain the exact Patient ID group key');
  const groupKeyLabel = patientGroupOutput.label;
  const configureGroup = async () => {
    await clickNative(nativePage, '[data-testid="construction-rows-settings-trigger"]');
    await waitForObservable(nativePage, () => document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled === false, 5000);
    await clickNative(nativePage, '[data-testid="construction-action-group-rows"]');
    const groupKeyControl = nativePage.locator(`input[aria-label=${JSON.stringify(`Group by ${groupKeyLabel}`)}]`);
    await groupKeyControl.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await groupKeyControl.count(), 1, 'The group key control must be unique');
    await groupKeyControl.waitFor({ state: 'attached', timeout: 5000 });
    assert.equal(await groupKeyControl.isEnabled(), true, 'The group key control must be enabled');
    const fromIndex = nativeCapture.requests.length;
    const started = Date.now();
    await clickNative(nativePage, `input[aria-label=${JSON.stringify(`Group by ${groupKeyLabel}`)}]`);
    return { started, fromIndex };
  };
  let groupAttempt = await configureGroup();
  await proposal('group-available-patient-witnesses-preview-cancel-target', groupAttempt.started, groupedRows, groupAttempt.fromIndex);
  const beforeGroupCancel = await api(`${base}/builder`);
  const cancelGroupStarted = Date.now();
  await clickNative(nativePage, '[data-testid="construction-cancel-proposal"]');
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  assert.deepEqual((await api(`${base}/builder`)).workspace, beforeGroupCancel.workspace, 'Cancel must preserve the exact expanded source workspace');
  record('cancel-group-proposal-preserves-source-bindings', cancelGroupStarted);
  await openTable(relatedRows, 'reload-expanded-source-after-group-cancel');

  groupAttempt = await configureGroup();
  await proposal('group-available-patient-witnesses-preview', groupAttempt.started, groupedRows, groupAttempt.fromIndex);
  await applyProposal(groupedRows, 'group-available-patient-witnesses-apply-to-render');
  const groupedWorkspace = structuredClone(builder.workspace);
  const groupedBaseline = doc();
  assert.deepEqual(groupedBaseline.population, populationBeforeGroup, 'Grouping must preserve exact selected member scope');
  const groupStepBefore = groupedBaseline.construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert(groupStepBefore, 'The Group operation was not saved');
  const savedGroupKeyOutput = groupStepBefore.outputs.find((output) => output.label === groupKeyLabel);
  assert(savedGroupKeyOutput, 'The saved Group output lost its Patient resource key');
  const groupKeyName = savedGroupKeyOutput.name;
  await openTable(groupedRows, 'reload-independent-grouped-witnesses');

  await openRelatedFieldChooser();
  const beforeOne = await api(`${base}/builder`);
  assert.deepEqual(doc(beforeOne).construction, groupedBaseline.construction);
  assert.deepEqual(doc(beforeOne).population, groupedBaseline.population);
  const assertReferenceAllProposalSettled = async (entry, name) => {
    const response = entry.response;
    assert.equal(entry.status, 200, `${name} must complete successfully: ${JSON.stringify(response)}`);
    assert.equal(response?.previewStatus, 'READY', `${name} must render a ready proposal: ${JSON.stringify(response)}`);
    const relatedProposal = relatedSourceProposalCandidate({ ...entry, response }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(relatedProposal, `${name} must retain the exact raw-oracle Observation source`);
    const output = relatedProposal.step.outputs.find((candidate) => candidate.id === relatedProposal.related.outputColumnId);
    assert(output, `${name} must expose its related Observation output`);
    assert.equal(response.preview?.rowCount, witnesses.length, `${name} must settle every grouped witness row`);
    assert.equal(response.preview?.rows?.length, witnesses.length, `${name} must return every grouped witness row`);
    const patientIDs = response.preview.rows.map((row) => row[groupKeyName]);
    assert.equal(new Set(patientIDs).size, witnesses.length, `${name} must return distinct exact grouped identities`);
    for (const witness of witnesses) {
      const row = response.preview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
      assert(row, `${name} omitted ${witness.category} from the independent raw oracle`);
      const values = row[output.name];
      assert.equal(values.length, witness.observationKeys.length, `${name} must preserve one value per Observation identity`);
      assert.equal(values.filter((value) => value === null).length, 2, `${name} must preserve both null/missing values`);
      assert.equal(new Set(values).size, 30, `${name} must retain duplicate null values from distinct Observation identities`);
      assert.deepEqual(values, relatedValuesFor(witness), `${name} values differ from the independent raw oracle`);
    }
    await waitAdoptedChoicePreview(entry, name);
  };
  const assertReferenceOneRejectionSettled = async (entry) => {
    const response = entry.response;
    const errorCode = response?.error?.code ?? response?.code ??
      response?.diagnostics?.find((diagnostic) => diagnostic.severity === 'ERROR')?.code;
    assert.equal(entry.status, 422);
    assert.equal(errorCode, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
    assert.notEqual(response?.previewStatus, 'READY');
    assert(manyWitness.observationIds.length > 1);
    assert(manyWitness.distinctObservationReferenceValues.length > 1);
    assert.equal(manyWitness.nullObservationReferenceCount, 2);
    assert.deepEqual((await api(`${base}/builder`)).workspace, beforeOne.workspace,
      'The ONE rejection must leave the saved Group workspace unchanged before its deadline');
    const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
    const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
    await waitForObservable(nativePage, ({ routeSelector, formSelector }) => {
      const dialog = document.querySelector('[role="dialog"]');
      const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
      return Boolean(dialog && policy?.value === 'ONE' && dialog.querySelector(routeSelector)?.checked && dialog.querySelector(formSelector)?.checked);
    }, { routeSelector, formSelector }, 5000);
    const retainedChooser = await inspectPage(nativePage, ({ routeSelector, formSelector }) => {
      const dialog = document.querySelector('[role="dialog"]');
      const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
      return { open: Boolean(dialog), policy: policy?.value, routeChecked: dialog?.querySelector(routeSelector)?.checked,
        formChecked: dialog?.querySelector(formSelector)?.checked,
        addEnabled: [...(dialog?.querySelectorAll('button') ?? [])].some(button => button.textContent.trim() === 'Add 1 column' && !button.disabled) };
    }, { routeSelector, formSelector });
    assert.deepEqual(retainedChooser, { open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true },
      'The rejected ONE proposal must settle with the exact route and ALL form retained for repair');
    report.oneChooserAfterRejection = { ...retainedChooser, savedGroupUnchanged: true };
  };
  if (zeroObservationMode) {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = nativeCapture.requests.length;
    await clickAddRelated();
    const oneProposal = await waitRelatedProposal('zero-observation-one-preview', oneStarted, 'ONE', oneFromIndex);
    const oneResponse = oneProposal.response;
    assert.equal(oneProposal.status, 200, JSON.stringify(oneProposal.response));
    assert.equal(oneResponse.previewStatus, 'READY', JSON.stringify(oneProposal.response));
    await waitAdoptedChoicePreview(oneProposal, 'Zero Observation ONE proposal');
    const oneRelatedStep = relatedSourceProposalCandidate({ ...oneProposal, response: oneResponse }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: 'id',
    })?.step;
    assert(oneRelatedStep, 'The zero-match ONE proposal must retain the selected Observation.id RELATED_SOURCE step');
    const oneOutput = oneRelatedStep.outputs.find((candidate) => candidate.id === oneRelatedStep.operation.relatedSource.outputColumnId);
    assert(oneOutput, 'The zero-match ONE proposal must expose its related ID output');
    assert.equal(oneResponse.preview?.rowCount, 1, 'ONE must preserve the grouped Patient row when the exact raw route has no Observation matches');
    const oneRow = oneResponse.preview.rows.find((row) => row[groupKeyName] === witnesses[0].patient.id);
    assert(oneRow, 'The zero-match ONE preview omitted the exact scoped Patient root');
    assert.equal(oneRow[oneOutput.name], null,
      'The typed nullable ONE source must return null for zero matching Observation values');
    report.zeroObservationOne = { status: oneProposal.status, previewStatus: oneResponse.previewStatus,
      rowCount: oneResponse.preview.rowCount, patientId: witnesses[0].patient.id,
      parentValue: oneRow[groupKeyName], relatedValue: oneRow[oneOutput.name], value: oneRow[oneOutput.name],
      nullableValueIsNull: true };

    const cancelOneStarted = Date.now();
    await cancelColumnProposal();
    await rendered(groupedRows);
    const canceledOneBuilder = await api(`${base}/builder`);
    const canceledOneDocument = doc(canceledOneBuilder);
    assert.deepEqual(canceledOneBuilder.workspace, groupedWorkspace,
      'Canceling zero-match ONE must preserve the exact grouped source workspace');
    assert.deepEqual(canceledOneDocument.construction, groupedBaseline.construction,
      'Canceling zero-match ONE must preserve the exact grouped construction');
    assert.deepEqual(canceledOneDocument.population, groupedBaseline.population,
      'Canceling zero-match ONE must preserve the exact grouped population');
    report.zeroObservationOneCancel = { workspaceUnchanged: true, constructionUnchanged: true,
      populationUnchanged: true, renderedRowsMatch: true };
    record('cancel-zero-observation-one-preserves-group', cancelOneStarted, report.zeroObservationOneCancel);
    await openRelatedFieldChooser();
    await selectRelatedPolicy('ALL');
  } else if (basicMode && statusFieldMode) {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = nativeCapture.requests.length;
    await clickAddRelated();
    const oneProposal = await waitRelatedProposal('basic-status-one-preview', oneStarted, 'ONE', oneFromIndex);
    const oneProposalResponse = oneProposal.response;
    assert.equal(oneProposal.status, 200, JSON.stringify(oneProposal.response));
    assert.equal(oneProposalResponse.previewStatus, 'READY', JSON.stringify(oneProposal.response));
    const adoptedOneProposalId = await waitAdoptedChoicePreview(oneProposal, 'Basic status ONE proposal');
    const oneRelatedStep = relatedSourceProposalCandidate({ ...oneProposal, response: oneProposal.response }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: 'status',
    })?.step;
    assert(oneRelatedStep, 'The basic ONE preview must contain the selected status RELATED_SOURCE step');
    const oneOutput = oneRelatedStep.outputs.find((candidate) => candidate.id === oneRelatedStep.operation.relatedSource.outputColumnId);
    assert(oneOutput, 'The basic ONE preview must expose its candidate status output');
    const oneRow = oneProposalResponse.preview.rows.find((row) => row[groupKeyName] === witnesses[0].patient.id);
    assert(oneRow, 'The basic ONE preview omitted the independently selected Patient');
    assert.equal(oneRow[oneOutput.name], witnesses[0].observationStatuses[0], 'The basic ONE value differs from the exact raw Observation.status');
    report.basicOneStatus = { status: oneProposal.status, proposalId: adoptedOneProposalId,
      source: report.relatedFieldCandidate, rawValue: witnesses[0].observationStatuses[0], candidateValue: oneRow[oneOutput.name] };
    const cancelOneStarted = Date.now();
    await cancelColumnProposal();
    await rendered(groupedRows);
    assert.deepEqual((await api(`${base}/builder`)).workspace, groupedWorkspace, 'Canceling basic ONE must preserve the exact Group workspace');
    record('cancel-basic-status-one-proposal-preserves-group', cancelOneStarted);
    await openRelatedFieldChooser();
    await selectRelatedPolicy('ALL');
  } else {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = nativeCapture.requests.length;
    await clickAddRelated();
    const oneFailure = await waitRelatedProposal('related-observation-one-disagreement', oneStarted, 'ONE', oneFromIndex,
      referenceFieldMode ? assertReferenceOneRejectionSettled : undefined);
    const oneFailureResponse = oneFailure.response;
    const oneError = oneFailureResponse?.error?.code ?? oneFailureResponse?.code ??
      oneFailureResponse?.diagnostics?.find((diagnostic) => diagnostic.severity === 'ERROR')?.code;
    assert.equal(oneFailure.status, 422, JSON.stringify(oneFailure));
    assert.equal(oneError, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', JSON.stringify(oneFailure.response));
    assert.notEqual(oneFailure.response?.previewStatus, 'READY', 'Raw multiple-value witness must not pass grouped-row ONE');
    assert(manyWitness.observationIds.length > 1, 'The raw many witness must independently predict the ONE conflict');
    if (statusFieldMode) assert(manyWitness.distinctStatusValues.length > 1,
      'The status witness must independently prove distinct ONE values');
    if (quantityFieldMode) {
      assert.deepEqual(manyWitness.distinctQuantityValues, [172.5, 180],
        'The raw same-code fixture must independently prove two exact decimal values');
      assert(manyWitness.quantityValues.every((value) => typeof value === 'number' && Number.isFinite(value)),
        'The raw Quantity witness must preserve numeric JSON values');
    }
    if (referenceFieldMode) {
      assert(manyWitness.distinctObservationReferenceValues.length > 1,
        'The raw nullable-reference witness must independently prove more than one distinct present ONE value');
      assert.equal(manyWitness.nullObservationReferenceCount, 2,
        'The source oracle must retain two null values while predicting ONE rejection from the 29 distinct present references');
    }
    if (!referenceFieldMode) {
      const afterOne = await api(`${base}/builder`);
      assert.deepEqual(afterOne.workspace, beforeOne.workspace, 'Rejected ONE preflight must not mutate source membership, route, or Group');
    }
    report.oneRejection = { status: oneFailure.status, errorCode: oneError,
      manyObservationIds: manyWitness.observationIds,
      ...(statusFieldMode ? { distinctStatusValues: manyWitness.distinctStatusValues } : {}),
      ...(quantityFieldMode ? { distinctQuantityValues: manyWitness.distinctQuantityValues } : {}),
      ...(referenceFieldMode ? { distinctObservationReferenceValues: manyWitness.distinctObservationReferenceValues,
        nullObservationReferenceCount: manyWitness.nullObservationReferenceCount } : {}),
      proposalId: oneFailure.response?.proposalId };
    if (!referenceFieldMode) {
      const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
      const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
      await waitForObservable(nativePage, ({ routeSelector, formSelector }) => {
        const dialog = document.querySelector('[role="dialog"]');
        const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
        return Boolean(dialog && policy?.value === 'ONE' && dialog.querySelector(routeSelector)?.checked && dialog.querySelector(formSelector)?.checked);
      }, { routeSelector, formSelector }, 5000);
      const retainedChooser = await inspectPage(nativePage, ({ routeSelector, formSelector }) => {
        const dialog = document.querySelector('[role="dialog"]');
        const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
        return { open: Boolean(dialog), policy: policy?.value, routeChecked: dialog?.querySelector(routeSelector)?.checked,
          formChecked: dialog?.querySelector(formSelector)?.checked,
          addEnabled: [...(dialog?.querySelectorAll('button') ?? [])].some(button => button.textContent.trim() === 'Add 1 column' && !button.disabled) };
      }, { routeSelector, formSelector });
      assert.deepEqual(retainedChooser, { open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true },
        'ONE rejection must retain the same related-source chooser, exact route, form, and source selection for direct repair');
      report.oneChooserAfterRejection = { ...retainedChooser, savedGroupUnchanged: true };
    }
    await selectRelatedPolicy('ALL');
  }
  const allRepairStarted = Date.now();
  const allRepairFromIndex = nativeCapture.requests.length;
  await clickAddRelated();
  const firstAll = await waitRelatedProposal('same-chooser-all-repair-preview', allRepairStarted, 'ALL', allRepairFromIndex,
    referenceFieldMode ? entry => assertReferenceAllProposalSettled(entry, 'same-chooser-all-repair-preview') : undefined);
  const firstAllResponse = firstAll.response;
  assert.equal(firstAll.status, 200, JSON.stringify(firstAll.response));
  assert.equal(firstAllResponse.previewStatus, 'READY', JSON.stringify(firstAll.response));
  assert(firstAllResponse.previewDurationMs <= 5000, `ALL preview took ${firstAllResponse.previewDurationMs} ms`);
  const firstAllReceiptId = await waitAdoptedChoicePreview(firstAll, 'Same-chooser ALL repair');
  let previewColumnId;
  {
    const relatedProposal = relatedSourceProposalCandidate({ ...firstAll, response: firstAll.response }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(relatedProposal, 'Native ALL preview must propose the exact related Observation field');
    const output = relatedProposal.step.outputs.find((candidate) => candidate.id === relatedProposal.related.outputColumnId);
    assert(output, `Native ALL preview omitted the related ${relatedFieldPath} output`);
    previewColumnId = output.name;
    report.directAllRepair = { policy: relatedProposal.rowValuePolicy, form: relatedProposal.related.form,
      contributorPolicy: relatedProposal.related.contributorRule.policy, receiptId: firstAllReceiptId };
  }
  assert(previewColumnId, `Native ALL preview did not propose the related ${relatedFieldPath} column`);
  const proposedValues = firstAllResponse.preview.rows.map((row) => ({ patientReference: row[groupKeyName], values: row[previewColumnId] }));
  assert.equal(proposedValues.length, witnesses.length);
  for (const witness of witnesses) {
    const proposed = proposedValues.find((row) => row.patientReference === witness.patient.id);
    assert(proposed, `Native preview omitted ${witness.category} witness ${witness.patient.reference}`);
    if (zeroObservationMode) {
      assert(Array.isArray(proposed.values), 'The typed ALL output must remain an array when its exact raw route has zero terminals');
      assert.equal(proposed.values.length, 0,
        'The typed ALL output must contain no fabricated value when the exact raw route has zero terminals');
      report.zeroObservationAll = { previewStatus: firstAllResponse.previewStatus,
        rowCount: firstAllResponse.preview.rowCount, patientId: witness.patient.id,
        values: proposed.values, previewValueIsEmptyArray: true,
        sourceContract: 'RELATED_SOURCE ALL returns a typed array subplan with empty-on-null behavior when no terminal record matches' };
    }
    if (referenceFieldMode) {
      assert.equal(proposed.values.length, witness.observationKeys.length,
        'ALL must return one protocol value per distinct terminal Observation identity');
      assert.equal(proposed.values.filter((value) => value === null).length, 2,
        'ALL protocol output must preserve both null/missing Observation.specimen.reference entries normalized by the raw oracle');
      assert.equal(new Set(proposed.values).size, 30,
        'ALL must preserve the two null values from distinct Observation identities instead of deduplicating by field value');
    }
    if (quantityFieldMode) assert(hasSameQuantityValueMultiset(proposed.values, relatedValuesFor(witness)), `${witness.category} ALL numeric multiset differs from the independent raw oracle`);
    else assert.deepEqual(proposed.values, relatedValuesFor(witness), `${witness.category} ALL output differs from the independent raw oracle`);
  }
  if (quantityFieldMode) {
    const proposed = proposedValues.find(({ patientReference }) => patientReference === manyWitness.patient.id);
    assert(Array.isArray(proposed?.values), 'ALL Quantity output must be an array');
    assert(proposed.values.every((value) => typeof value === 'number' && Number.isFinite(value)),
      'ALL Quantity values must remain finite JSON numbers');
    report.quantityAllOutputEvidence = { expectedValues: relatedValuesFor(manyWitness), previewValues: proposed.values,
      identitiesMatch: hasSameQuantityValueMultiset(proposed.values, relatedValuesFor(manyWitness)), logicalType: logicalTypeExpected };
  }
  if (statusFieldMode && !basicMode) {
    const proposed = proposedValues.find(({ patientReference }) => patientReference === manyWitness.patient.id);
    report.relatedAllOutputEvidence = { ...report.directAllRepair,
      patientId: manyWitness.patient.id,
      expectedValues: relatedValuesFor(manyWitness), previewValues: proposed?.values,
      identitiesMatch: JSON.stringify(proposed?.values) === JSON.stringify(relatedValuesFor(manyWitness)),
    };
  }
  const cancelAllStarted = Date.now();
  await cancelColumnProposal();
  await rendered(groupedRows);
  const canceledAllBuilder = await api(`${base}/builder`);
  const canceledAllDocument = doc(canceledAllBuilder);
  assert.deepEqual(canceledAllBuilder.workspace, groupedWorkspace, 'Canceling the successful ALL preview must preserve the exact grouped workspace');
  assert.deepEqual(canceledAllDocument.construction, groupedBaseline.construction,
    'Canceling the successful ALL preview must preserve the exact Group construction');
  assert.deepEqual(canceledAllDocument.population, groupedBaseline.population,
    'Canceling the successful ALL preview must preserve the exact source population');
  const allCancelEvidence = { workspaceUnchanged: true, constructionUnchanged: true,
    populationUnchanged: true, renderedRowsMatch: true };
  if (zeroObservationMode) report.zeroObservationAllCancel = allCancelEvidence;
  if (quantityFieldMode) report.quantityAllCancelEvidence = allCancelEvidence;
  if (statusFieldMode && !basicMode) report.relatedAllCancelEvidence = allCancelEvidence;
  record('cancel-related-all-proposal-preserves-group', cancelAllStarted, allCancelEvidence);

  await openRelatedFieldChooser();
  await selectRelatedPolicy('ALL');
  const allApplyStarted = Date.now();
  const allApplyFromIndex = nativeCapture.requests.length;
  await clickAddRelated();
  const allProposal = await waitRelatedProposal('reopened-all-preview-before-apply', allApplyStarted, 'ALL', allApplyFromIndex,
    referenceFieldMode ? entry => assertReferenceAllProposalSettled(entry, 'reopened-all-preview-before-apply') : undefined);
  assert.equal(allProposal.status, 200);
  const allProposalResponse = allProposal.response;
  assert.equal(allProposalResponse.previewStatus, 'READY');
  {
    const reopenedRelated = relatedSourceProposalCandidate({ ...allProposal, response: allProposal.response }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(reopenedRelated, 'Reopened status ALL must retain the exact typed source and relationship route');
    assert.equal(reopenedRelated.rowValuePolicy, 'ALL');
  }
  const allProposalReceiptId = await waitAdoptedChoicePreview(allProposal, 'Reopened ALL proposal');
  report.reopenedAllProposalReceiptId = allProposalReceiptId;
  const applyStarted = Date.now();
  await clickNative(nativePage, '[data-testid="construction-apply-proposal"]');
  const allCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) =>
    item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === allProposalResponse.proposalId), allApplyFromIndex);
  assert.equal(allCommand.status, 200, JSON.stringify(allCommand.response));
  assert.equal(allCommand.request.commands.length, 1);
  assert.equal(allCommand.request.commands[0].proposalId, allProposalResponse.proposalId);
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  const addedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows), relatedDisplayValuesFor(witness)]).sort((a, b) => a[0].localeCompare(b[0]));
  await rendered(addedRows);
  builder = await api(`${base}/builder`);
  const addedDocument = doc();
  assert.deepEqual(addedDocument.population, groupedBaseline.population, 'ALL apply must preserve the exact source selection');
  const savedRelatedStep = addedDocument.construction.steps.find((step) => step.operation?.kind === 'RELATED_SOURCE' &&
    step.operation.relatedSource?.source?.candidateId === report.relatedFieldCandidate.candidateId &&
    step.operation.relatedSource?.source?.nodeId === report.relatedFieldCandidate.nodeId &&
    step.operation.relatedSource?.source?.resourceType === 'Observation' &&
    step.operation.relatedSource?.source?.path === relatedFieldPath &&
    step.operation.relatedSource?.choiceId === report.currentRelatedChoiceId);
  assert(savedRelatedStep, `The saved construction must retain the selected RELATED_SOURCE Observation.${relatedFieldPath} candidate and route choice`);
  const related = savedRelatedStep.operation.relatedSource;
  assert.equal(related.form, 'ALL');
  assert.equal(related.contributorRule.policy, 'ALL_MATCHES');
  assert.equal(related.rowValuePolicy ?? 'ALL', 'ALL', 'The saved related source must retain the selected grouped-row ALL policy');
  assert.deepEqual(related.route.map((hop) => [hop.fromResourceType, hop.toResourceType]),
    expectedRelatedRouteTypes.slice(0, -1).map((resourceType, index) => [resourceType, expectedRelatedRouteTypes[index + 1]]));
  if (zeroObservationMode) assert.deepEqual(related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedPatientObservationRoute,
  'The saved RELATED_SOURCE must retain the exact inbound typed Observation edge');
  else if (!basicMode) assert.deepEqual(related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The saved RELATED_SOURCE must retain the exact Specimen→Patient→Observation subject route');
  const output = savedRelatedStep.outputs.find((candidate) => candidate.id === related.outputColumnId);
  assert(output, `The saved related step omitted output ${related.outputColumnId}`);
  const relatedColumn = { column: output.name, columnId: output.id, label: output.label, logicalType: output.type };
  const assertRelatedPreviewMatchesOracle = (preview, name) => {
    assert.equal(preview.rowCount, witnesses.length, `${name} must preserve every grouped witness row`);
    assert.equal(preview.rows.length, witnesses.length, `${name} must return every grouped witness row`);
    const patientIDs = preview.rows.map((row) => row[groupKeyName]);
    assert.deepEqual(patientIDs.slice().sort(), witnesses.map((witness) => witness.patient.id).sort(),
      `${name} must return the exact independently selected Patient identities`);
    for (const witness of witnesses) {
      const row = preview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
      assert(row, `${name} omitted ${witness.category} from the independent raw oracle`);
      if (quantityFieldMode) {
        assert(Array.isArray(row[relatedColumn.column]), name + ' must return numeric ALL values as an array');
        assert(row[relatedColumn.column].every((value) => typeof value === 'number' && Number.isFinite(value)),
          name + ' must preserve the decimal output as finite JSON numbers');
      }
      if (referenceFieldMode) {
        assert.equal(row[relatedColumn.column].length, witness.observationKeys.length,
          `${name} must contain one value per distinct Observation identity`);
        assert.equal(row[relatedColumn.column].filter((value) => value === null).length, 2,
          `${name} must preserve both null array entries`);
        assert.equal(new Set(row[relatedColumn.column]).size, 30,
          `${name} must retain the duplicate null from distinct Observation identities`);
      }
      if (quantityFieldMode) assert(hasSameQuantityValueMultiset(row[relatedColumn.column], relatedValuesFor(witness)), `${name} numeric multiset differs from the independent raw oracle`);
      else assert.deepEqual(row[relatedColumn.column], relatedValuesFor(witness), `${name} values differ from the independent raw oracle`);
    }
  };
  const proposalOutput = allProposalResponse.preview?.columns.find((column) => column.column === relatedColumn.column);
  assert(proposalOutput, 'The accepted ALL proposal preview must contain the exact saved related output');
  assert.equal(relatedColumn.label, proposalOutput.label,
    'Applying the related source must retain the output label shown by its accepted proposal preview');
  if (statusFieldMode || quantityFieldMode) assert.equal(relatedColumn.label, relatedOutputLabel,
    quantityFieldMode
      ? 'The Quantity source must retain its accessible Observation value output label'
      : 'Observation.status is titled Status in the chooser, while the generated related output defaults to Observation status');
  assert.equal(relatedColumn.logicalType, logicalTypeExpected,
    `The persisted related column must match the compiler-proved Observation.${relatedFieldPath} logical type`);
  const savedGroup = addedDocument.construction.steps.find((step) => step.id === groupStepBefore.id);
  assert(savedGroup, 'The original Group step identity must remain stable');
  assert.deepEqual(savedGroup.operation.group.keys, groupStepBefore.operation.group.keys, 'ALL apply changed the authored group key binding');
  assert.deepEqual(savedGroup.operation.group.aggregates, groupStepBefore.operation.group.aggregates, 'ALL apply changed the authored Group aggregate');
  assert.deepEqual(savedGroup.rowValues, groupStepBefore.rowValues,
    'Adding a RELATED_SOURCE must preserve existing GROUP row-value bindings; policy belongs to the related source');
  const rowValueOutput = savedRelatedStep.outputs.find((candidate) => candidate.id === related.outputColumnId);
  assert.equal(rowValueOutput?.name, relatedColumn.column);
  for (const sourceColumn of groupedBaseline.columns) {
    assert.deepEqual(addedDocument.columns.find((column) => column.columnId === sourceColumn.columnId), sourceColumn, `ALL apply changed source binding ${sourceColumn.label}`);
  }
  const applyPreviewFrom = nativeCapture.requests.indexOf(allCommand);
  const savedPreviewStarted = Date.now();
  const proposalPreview = allProposalResponse.preview;
  assert(proposalPreview, 'The accepted ALL proposal did not retain its candidate preview');
  assert.equal(allProposalResponse.candidateWorkspaceDigest, builder.draftDigest, 'The saved builder digest must equal the applied proposal digest');
  assert.equal(allProposalResponse.outputId, outputId);
  const allCommandResponse = allCommand.response;
  assert.equal(allCommandResponse?.draftVersion, builder.draftVersion);
  assert.equal(allCommandResponse?.draftDigest, builder.draftDigest);
  const acceptedReconcile = await waitNative((entry) => entry.path.endsWith('/reconcile') &&
    entry.request?.draftVersion === builder.draftVersion && entry.request?.draftDigest === builder.draftDigest, applyPreviewFrom);
  assert.equal(acceptedReconcile.status, 200, JSON.stringify(acceptedReconcile.response));
  const acceptedReconcileResponse = acceptedReconcile.response;
  assert.equal(acceptedReconcileResponse?.snapshotToken, allProposalResponse.snapshotToken);
  assert.equal(acceptedReconcileResponse?.intentDigest, builder.draftDigest);
  assert(acceptedReconcileResponse?.outputs?.some((output) => output.outputId === outputId), 'The saved reconcile receipt does not include the edited output');
  assert.equal(proposalPreview.outputId, outputId);
  const expectedActivePreview = { receiptId: acceptedReconcileResponse.receiptId, outputId,
    draftVersion: String(builder.draftVersion), draftDigest: builder.draftDigest };
  await waitForObservable(nativePage, expected => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && preview?.dataset.previewReceiptId === expected.receiptId &&
      preview?.dataset.previewOutputId === expected.outputId && preview?.dataset.currentDraftVersion === expected.draftVersion &&
      preview?.dataset.currentDraftDigest === expected.draftDigest;
  }, expectedActivePreview, 5000);
  const activePreview = await inspectPage(nativePage, () => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return { status: preview?.dataset.previewStatus, receiptId: preview?.dataset.previewReceiptId,
      outputId: preview?.dataset.previewOutputId, draftVersion: preview?.dataset.currentDraftVersion,
      draftDigest: preview?.dataset.currentDraftDigest };
  });
  assert.deepEqual(activePreview, {
    status: 'ready',
    receiptId: acceptedReconcileResponse.receiptId,
    outputId,
    draftVersion: String(builder.draftVersion),
    draftDigest: builder.draftDigest,
  }, 'The rendered table must be the active preview for the exact saved draft and accepted receipt');
  const applyPreviewRequests = nativeCapture.requests.slice(applyPreviewFrom).filter((entry) => entry.path.endsWith('/preview'));
  for (const entry of applyPreviewRequests) {
    assert(nativeRequestValue(entry, 'outputId'), `Captured ${entry.method} ${entry.path} without outputId body/query: ${JSON.stringify(entry)}`);
    await nativeCapture.waitForResponse(candidate => candidate === entry, { fromIndex: 0, timeoutMs: 5000 });
  }
  const targetPreviewRequests = applyPreviewRequests.filter((entry) => nativeRequestValue(entry, 'outputId') === outputId);
  for (const entry of targetPreviewRequests) {
    assert.equal(nativeRequestValue(entry, 'receiptId'), acceptedReconcileResponse.receiptId, 'A post-Apply native preview must use the accepted saved receipt');
    assert.equal(entry.status, 200, JSON.stringify(entry.response));
    const response = entry.response;
    assert.equal(response?.receiptId, acceptedReconcileResponse.receiptId);
    assert.equal(response?.outputId, outputId);
  }
  const previewSource = targetPreviewRequests.length === 0 ? 'direct-preview-for-accepted-saved-receipt' : 'post-apply-native-preview';
  const savedPreview = targetPreviewRequests.at(-1)?.response ?? await api(`${base}/preview`, {
    receiptId: acceptedReconcileResponse.receiptId, outputId, limit: 25,
  });
  assert.equal(savedPreview.receiptId, acceptedReconcileResponse.receiptId, 'Saved values must be read from the exact accepted receipt');
  assert.equal(savedPreview.outputId, outputId);
  report.savedPreviewVerification = {
    source: previewSource,
    outputId,
    receiptId: acceptedReconcileResponse.receiptId,
    savedDraftVersion: builder.draftVersion,
    savedDraftDigest: builder.draftDigest,
    candidateWorkspaceDigest: allProposalResponse.candidateWorkspaceDigest,
    proposalRequestId: allProposal.requestId,
    reconcileRequestId: acceptedReconcile.requestId,
    activePreview,
    postApplyPreviewRequests: applyPreviewRequests.map((entry) => ({
      requestId: entry.requestId,
      method: entry.method,
      url: entry.url,
      path: entry.path,
      query: entry.query,
      request: entry.request,
      status: entry.status,
      responseReceiptId: entry.response?.receiptId,
      responseOutputId: entry.response?.outputId,
      rowCount: entry.response?.rowCount,
      status: entry.status,
    })),
  };
  assert.equal(savedPreview.rowCount, witnesses.length);
  assertRelatedPreviewMatchesOracle(savedPreview, 'Saved native Preview');
  record('apply-related-all-to-native-table-render', applyStarted, { commandStatus: allCommand.status });
  record('native-preview-matches-exact-related-field-oracle', savedPreviewStarted, {
    receiptId: savedPreview.receiptId,
    source: previewSource,
    postApplyPreviewRequestCount: targetPreviewRequests.length,
  });

  const reloadedPreview = await openTable(addedRows, 'reload-related-all-available-cardinality-values',
    preview => assertRelatedPreviewMatchesOracle(preview, 'Reloaded native Preview'));
  const reloadedBuilder = await api(`${base}/builder`);
  const reloadedDocument = doc(reloadedBuilder);
  const reloadedRelatedStep = reloadedDocument.construction.steps.find((step) => step.id === savedRelatedStep.id);
  assert(reloadedRelatedStep, 'Reload must retain the exact saved related-source step identity');
  assert.deepEqual(reloadedRelatedStep.operation.relatedSource, related,
    'Reload must retain the exact related source candidate, signed route, and ALL policy');
  const savedWitnessRow = savedPreview.rows.find((candidate) => candidate[groupKeyName] === witnesses[0].patient.id);
  const reloadedWitnessRow = reloadedPreview.rows.find((candidate) => candidate[groupKeyName] === witnesses[0].patient.id);
  const savedOutputValues = savedWitnessRow?.[relatedColumn.column];
  const reloadedOutputValues = reloadedWitnessRow?.[relatedColumn.column];
  const expectedSavedRoute = basicMode || zeroObservationMode ? expectedPatientObservationRoute : expectedReferenceRoute;
  const savedRouteShape = related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection }));
  const savedSourceMatches = savedRelatedStep.operation.relatedSource.source?.candidateId === report.relatedFieldCandidate.candidateId &&
    savedRelatedStep.operation.relatedSource.source?.nodeId === report.relatedFieldCandidate.nodeId &&
    savedRelatedStep.operation.relatedSource.source?.path === relatedFieldPath &&
    savedRelatedStep.operation.relatedSource.choiceId === report.currentRelatedChoiceId &&
    JSON.stringify(savedRouteShape) === JSON.stringify(expectedSavedRoute) &&
    savedRelatedStep.operation.relatedSource.form === 'ALL' &&
    (savedRelatedStep.operation.relatedSource.rowValuePolicy ?? 'ALL') === 'ALL';
  if (quantityFieldMode) {
    assert(hasSameQuantityValueMultiset(savedOutputValues, relatedValuesFor(manyWitness)), 'Saved Quantity output must preserve the exact raw numeric multiset');
    assert(hasSameQuantityValueMultiset(reloadedOutputValues, relatedValuesFor(manyWitness)), 'Reloaded Quantity output must preserve the exact raw numeric multiset');
    assert(savedOutputValues.every((value) => typeof value === 'number' && Number.isFinite(value)));
    assert(reloadedOutputValues.every((value) => typeof value === 'number' && Number.isFinite(value)));
    report.quantityAllSavedEvidence = { logicalType: relatedColumn.logicalType, expectedValues: relatedValuesFor(manyWitness),
      previewValues: savedOutputValues, reloadValues: reloadedOutputValues, sourceMatches: savedSourceMatches };
  }
  if (zeroObservationMode) {
    report.zeroObservationSavedAll = {
      candidateSourceMatches: savedSourceMatches,
      form: related.form,
      rowValuePolicy: related.rowValuePolicy ?? 'ALL',
      savedParentCount: savedPreview.rowCount,
      previewValues: savedOutputValues,
      reloadParentCount: reloadedPreview.rowCount,
      reloadValues: reloadedOutputValues,
      reloadSourceMatches: savedSourceMatches && JSON.stringify(reloadedRelatedStep.operation.relatedSource) === JSON.stringify(related),
      receiptId: savedPreview.receiptId,
    };
  }
  if (statusFieldMode && !basicMode) {
    report.relatedAllSavedEvidence = {
      candidateSourceMatches: savedSourceMatches,
      form: related.form,
      rowValuePolicy: related.rowValuePolicy ?? 'ALL',
      expectedValues: relatedValuesFor(manyWitness),
      previewValues: savedOutputValues,
      reloadValues: reloadedOutputValues,
      reloadSourceMatches: savedSourceMatches && JSON.stringify(reloadedRelatedStep.operation.relatedSource) === JSON.stringify(related),
      receiptId: savedPreview.receiptId,
    };
  }

  let activeRelatedColumnLabel = relatedColumn.label;
  {
    const renamedLabel = quantityFieldMode ? 'Verified Observation value' : `Verified ${relatedOutputLabel}`;
    const editStarted = Date.now();
    const editFrom = nativeCapture.requests.length;
    await clickNative(nativePage, 'button', { name: 'Columns' });
    const labelSelector = `[aria-label=${JSON.stringify(`Column name for ${relatedColumn.label}`)}]`;
    const renameInput = nativePage.locator(labelSelector);
    await renameInput.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await renameInput.count(), 1, 'The output label editor must be unique');
    const renameState = await renameInput.evaluate(input => ({ value: input.value, disabled: input.disabled, readOnly: input.readOnly }));
    assert.equal(renameState.disabled, false, 'The applied related-field output must remain editable');
    assert.equal(renameState.readOnly, false, 'The applied related-field output must not be read-only');
    await cda.action(`fill output label ${relatedColumn.label}`, renameInput,
      target => target.fill(renamedLabel, { timeout: 5000 }), { timeout: 5000, editable: true });
    await cda.action(`commit output label ${relatedColumn.label}`, renameInput,
      target => target.press('Enter', { timeout: 5000 }), { timeout: 5000, editable: true });
    const editableStepId = savedRelatedStep.id;
    const rename = await waitNative((entry) => entry.path.endsWith('/commands') &&
      entry.request?.commands?.some((item) => item.type === 'UPDATE_CONSTRUCTION_OUTPUT' &&
        item.constructionOutput?.stepId === editableStepId && item.constructionOutput?.columnId === rowValueOutput.id), editFrom);
    assert.equal(rename.status, 200, JSON.stringify(rename.response));
    assert.equal(rename.request.commands[0].constructionOutput.label, renamedLabel);
    builder = await api(`${base}/builder`);
    const editedStep = doc().construction.steps.find((step) => step.id === editableStepId);
    const editedOutput = editedStep?.outputs.find((output) => output.id === rowValueOutput.id);
    assert.equal(editedOutput?.name, rowValueOutput.name, 'Editing a display label must preserve the stable output name');
    assert.equal(editedOutput?.label, renamedLabel, 'The saved construction output must retain its edited label');
    assert.deepEqual(editedStep?.operation.relatedSource?.source, related.source,
      'Editing the label must preserve the exact RELATED_SOURCE candidate and field binding');
    assert.equal(editedStep?.operation.relatedSource?.choiceId, report.currentRelatedChoiceId,
      'Editing the label must preserve the selected signed route choice');
    assert.deepEqual(editedStep?.operation.relatedSource?.route, related.route,
      'Editing the label must preserve the exact related source route');
    assert.equal(editedStep?.operation.relatedSource?.rowValuePolicy ?? 'ALL', 'ALL',
      'Editing the label must preserve the related source ONE/ALL policy');
    await clickNative(nativePage, 'button', { name: 'Columns' });
    await waitForObservable(nativePage, ({ columnCount, label, rowCount }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const headers = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
        .map(cell => cell.textContent.trim());
      const rows = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
        .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length);
      return table?.getAttribute('aria-colcount') === String(columnCount) && headers.includes(label) &&
        rows.length === rowCount && !document.body.innerText.includes('Loading your table…') &&
        !document.body.innerText.includes('Preview failed:');
    }, { columnCount: addedRows[0]?.length ?? 2, label: renamedLabel, rowCount: addedRows.length });
    const editedCurrentTable = await inspectPage(nativePage, () => ({
      headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
        .map(cell => cell.textContent.trim()),
      rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
        .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
    }));
    assert(editedCurrentTable.headers.includes(renamedLabel),
      `Current saved table header did not render the edited output label: ${JSON.stringify(editedCurrentTable.headers)}`);
    assert.equal(editedCurrentTable.rows.length, addedRows.length,
      'Current saved table must render the exact expected number of rows after the label edit');
    if (quantityFieldMode) assertQuantityOutputRowsMatch(editedCurrentTable.rows, addedRows,
      'Current saved table after the Quantity output label edit');
    else {
      const orderedRows = rows => rows.map(row => JSON.stringify(row)).sort();
      assert.deepEqual(orderedRows(editedCurrentTable.rows), orderedRows(addedRows),
        'Current saved table rows and cell values must exactly match the raw CDA oracle after the label edit');
    }
    record('edit-related-field-output-label', editStarted, { priorLabel: relatedColumn.label, newLabel: renamedLabel, path: relatedFieldPath, rowCount: editedCurrentTable.rows.length });
    let editedHeaders;
    const editedPreview = await openTable(addedRows, 'reload-edited-related-field-output-label', async preview => {
      editedHeaders = await inspectPage(nativePage, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
        .map(cell => cell.textContent.trim()));
      assert(editedHeaders.includes(renamedLabel),
        `Reloaded table header text did not retain the exact edited output label: ${JSON.stringify(editedHeaders)}`);
      assertRelatedPreviewMatchesOracle(preview, 'Edited and reloaded native Preview');
    });
    const editedWitnessRow = editedPreview.rows.find((candidate) => candidate[groupKeyName] === witnesses[0].patient.id);
    report.relatedOutputEditEvidence = {
      labelChanged: editedOutput.label === renamedLabel && editedHeaders.includes(renamedLabel),
      candidateSourceMatches: JSON.stringify(editedStep.operation.relatedSource) === JSON.stringify(related),
      policy: editedStep.operation.relatedSource.rowValuePolicy ?? 'ALL',
      expectedValues: relatedValuesFor(witnesses[0]),
      reloadValues: editedWitnessRow?.[relatedColumn.column],
      label: renamedLabel,
    };
    activeRelatedColumnLabel = renamedLabel;
  }

  if (savedEmptyPolicyEdit) {
    const beforeCancelEdit = await api(`${base}/builder`);
    const beforeCancelDocument = doc(beforeCancelEdit);
    const preserveSteps = beforeCancelDocument.construction.steps.filter((step) =>
      step.operation?.kind === 'RELATED_EXPAND' &&
      step.operation.relatedExpand?.targetResourceType === 'Observation');
    assert.equal(preserveSteps.length, 1, 'The saved Patient→Observation expansion must be unique before its policy edit');
    const preserveStep = preserveSteps[0];
    const preserveExpand = preserveStep.operation.relatedExpand;
    const preserveOutput = preserveStep.outputs.find((output) => output.id === preserveExpand.relatedRecordColumnId);
    assert(preserveOutput, 'The saved PRESERVE_PARENT expansion must retain its Observation identity output');
    assert.equal(preserveExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.equal(preserveOutput.nullable, true);
    assert.deepEqual(beforeCancelDocument.population, doc(sourceBaseline).population,
      'The policy edit must start from the exact selected Patient population');
    const beforeCancelConstruction = structuredClone(beforeCancelDocument.construction);

    const editStarted = Date.now();
    const editFromIndex = nativeCapture.requests.length;
    await clickNative(nativePage, `[data-testid="construction-history-step-${preserveStep.id}"]`);
    await clickNative(nativePage, `[data-testid="construction-edit-step-${preserveStep.id}"]`);
    const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
    await waitForObservable(nativePage, ({ selector }) => {
      const policy = document.querySelector(selector);
      return Boolean(policy && !policy.disabled && policy.value === 'PRESERVE_PARENT');
    }, { selector: policySelector }, 5000);
    await selectNative(nativePage, policySelector, 'EXCLUDE');
    const excludePanel = await proposal('edit-saved-related-expand-exclude-cancel-preview', editStarted, [], editFromIndex);
    const cancelProposal = nativeCapture.requests.findLast((entry) => entry.startedAt >= editStarted && Number.isInteger(entry.status) &&
      entry.path.endsWith('/construction-proposals') && entry.response?.proposalId === excludePanel.proposalId);
    assert(cancelProposal, 'The saved RelatedExpand EXCLUDE preview must match a captured native proposal request');
    assert.equal(cancelProposal.status, 200, JSON.stringify(cancelProposal.response));
    const cancelProposalResponse = cancelProposal.response;
    assert.equal(cancelProposal.request?.outputId, outputId);
    assert.equal(cancelProposalResponse?.previewStatus, 'READY', JSON.stringify(cancelProposalResponse));
    assert.equal(cancelProposalResponse?.preview?.rowCount, 0,
      'The zero-match EXCLUDE candidate must preview a literal zero-row result');
    assert.deepEqual(cancelProposalResponse?.preview?.rows, [],
      'The zero-match EXCLUDE candidate must return no fabricated parent or related rows');
    const expectedExcludeConstruction = structuredClone(beforeCancelConstruction);
    const expectedExcludeStep = expectedExcludeConstruction.steps.find((step) => step.id === preserveStep.id);
    assert(expectedExcludeStep, 'The candidate construction must retain the saved RelatedExpand step identity');
    expectedExcludeStep.operation.relatedExpand.emptyPolicy = 'EXCLUDE';
    const expectedExcludeOutput = expectedExcludeStep.outputs.find((output) =>
      output.id === preserveExpand.relatedRecordColumnId);
    assert(expectedExcludeOutput, 'The EXCLUDE candidate must retain the related Observation output identity');
    expectedExcludeOutput.nullable = false;
    assert.deepEqual(cancelProposal.request?.candidateConstruction?.steps, expectedExcludeConstruction.steps,
      'The actual candidate wire must preserve every saved step and binding except the EXCLUDE policy and required output nullability');
    const cancelCandidateStep = cancelProposal.request.candidateConstruction.steps.find((step) => step.id === preserveStep.id);
    const cancelCandidateExpand = cancelCandidateStep.operation.relatedExpand;
    const cancelResponseStep = cancelProposalResponse.candidateConstruction?.steps?.find((step) => step.id === preserveStep.id);
    const cancelResponseOutput = cancelResponseStep?.outputs?.find((output) => output.id === preserveOutput.id);
    assert(cancelResponseOutput, 'The normalized EXCLUDE proposal must retain the related Observation output');
    assert.equal(Object.hasOwn(cancelResponseOutput, 'nullable'), false,
      'The normalized EXCLUDE output must serialize typed false nullable as omitted');
    assert.equal(cancelCandidateStep.id, preserveStep.id);
    assert.equal(cancelCandidateExpand.choiceId, preserveExpand.choiceId);
    assert.equal(cancelCandidateExpand.targetNodeId, preserveExpand.targetNodeId);
    assert.equal(cancelCandidateExpand.targetResourceType, preserveExpand.targetResourceType);
    assert.deepEqual(cancelCandidateExpand.route, preserveExpand.route);
    assert.equal(cancelCandidateExpand.relatedRecordColumnId, preserveExpand.relatedRecordColumnId);
    assert.equal(cancelCandidateExpand.emptyPolicy, 'EXCLUDE');
    assert.equal(cancelCandidateStep.outputs.find((output) => output.id === preserveOutput.id)?.nullable, false);

    const cancelStarted = Date.now();
    await clickNative(nativePage, '[data-testid="construction-cancel-proposal"]');
    await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
    await rendered(addedRows);
    builder = await api(`${base}/builder`);
    assert.deepEqual(doc().construction, beforeCancelConstruction,
      'Cancel must preserve the exact saved PRESERVE_PARENT and related ALL construction');
    assert.deepEqual(doc().population, beforeCancelDocument.population,
      'Cancel must preserve the exact selected Patient population');
    const afterCancelExpansion = doc().construction.steps.find((step) => step.id === preserveStep.id);
    assert.equal(afterCancelExpansion?.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.equal(afterCancelExpansion?.outputs.find((output) => output.id === preserveOutput.id)?.nullable, true);
    const afterCancelRelated = doc().construction.steps.find((step) => step.id === savedRelatedStep.id);
    assert.equal(afterCancelRelated?.operation.relatedSource?.form, 'ALL',
      'Cancel must preserve the previously applied related-source ALL state');
    assert.equal(afterCancelRelated?.operation.relatedSource?.outputColumnId, relatedColumn.columnId);
    const canceledReload = await openTable(addedRows, 'reload-canceled-exclude-preserves-related-all');
    assert.equal(canceledReload.rowCount, 1, 'Cancel/reload must retain the exact unmatched Patient parent row');
    assert.equal(canceledReload.rows.length, 1);
    assert.equal(canceledReload.rows[0][groupKeyName], witnesses[0].patient.id);
    assert.deepEqual(canceledReload.rows[0][relatedColumn.column], [],
      'Cancel/reload must preserve the applied related ALL empty array for the exact zero-match Patient');
    record('cancel-saved-related-expand-exclude-preserves-parent-and-all-after-reload', cancelStarted, {
      rowCount: canceledReload.rowCount, parentId: witnesses[0].patient.id,
      preserveParentPolicy: afterCancelExpansion.operation.relatedExpand.emptyPolicy,
      relatedAllValues: canceledReload.rows[0][relatedColumn.column],
    });
    report.savedEmptyPolicyEvidence = {
      cancelCandidate: { status: cancelProposal.status, previewStatus: cancelProposalResponse.previewStatus,
        rowCount: cancelProposalResponse.preview.rowCount, rows: cancelProposalResponse.preview.rows,
        candidateWireExact: true, stepId: preserveStep.id, routeChoiceId: cancelCandidateExpand.choiceId,
        route: cancelCandidateExpand.route,
        targetNodeId: cancelCandidateExpand.targetNodeId, targetResourceType: cancelCandidateExpand.targetResourceType,
        relatedRecordColumnId: cancelCandidateExpand.relatedRecordColumnId, outputNullable: false,
        responseNullableFieldOmitted: !Object.hasOwn(cancelResponseOutput, 'nullable') },
      cancel: { constructionUnchanged: true, populationUnchanged: true,
        parentRowCount: canceledReload.rowCount, parentId: witnesses[0].patient.id,
        savedPolicy: afterCancelExpansion.operation.relatedExpand.emptyPolicy,
        relatedOutputNullable: afterCancelExpansion.outputs.find((output) => output.id === preserveOutput.id)?.nullable,
        relatedAllValues: canceledReload.rows[0][relatedColumn.column] },
    };
  }

  const removeStarted = Date.now();
  const removeFromIndex = nativeCapture.requests.length;
  let removeCommand;
  let removeApplyStarted = removeStarted;
  const stepId = savedRelatedStep.id;
  assert.equal(savedRelatedStep.operation.relatedSource.source.candidateId, report.relatedFieldCandidate.candidateId);
  assert.equal(savedRelatedStep.operation.relatedSource.choiceId, report.currentRelatedChoiceId);
  await clickNative(nativePage, `[data-testid="construction-history-step-${stepId}"]`);
  const removeStepButton = nativePage.locator(`[data-testid="construction-remove-step-${stepId}"]`);
  await removeStepButton.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await removeStepButton.count(), 1, 'The construction step removal control must be unique');
  assert.equal(await removeStepButton.isEnabled(), true, 'The construction step removal control must be enabled');
  const removalProposalFromIndex = nativeCapture.requests.length;
  await clickNative(nativePage, `[data-testid="construction-remove-step-${stepId}"]`);
  const removalPreview = await proposal('remove-related-source-step-preview', removeStarted, groupedRows, removalProposalFromIndex);
  const removalProposal = nativeCapture.requests.findLast((entry) => entry.startedAt >= removeStarted && Number.isInteger(entry.status) &&
    entry.path.endsWith('/construction-proposals') && entry.response?.proposalId === removalPreview.proposalId);
  assert(removalProposal, 'The visible related-step removal must match a captured native construction proposal');
  assert.equal(removalProposal.status, 200, JSON.stringify(removalProposal.response));
  assert.deepEqual(removalProposal.request?.removeStepIds, [stepId], 'The removal proposal must target only the saved RELATED_SOURCE step');
  assert.deepEqual(removalProposal.request?.candidateConstruction?.steps, groupedBaseline.construction.steps,
    'The removal proposal must restore the exact original Group construction');
  assert.equal(removalProposal.response?.previewStatus, 'READY', JSON.stringify(removalProposal.response));
  removeApplyStarted = Date.now();
  await clickNative(nativePage, '[data-testid="construction-apply-proposal"]');
  removeCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) =>
    item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === removalPreview.proposalId), removeFromIndex);
  assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
  assert.equal(removeCommand.request.commands.length, 1);
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
  builder = await api(`${base}/builder`);
  assert.deepEqual(doc().columns, groupedBaseline.columns, `Removing the related Observation.${relatedFieldPath} column must restore the original source columns`);
  assert.deepEqual(doc().construction, groupedBaseline.construction, 'Removing the related field column must restore the original Group construction');
  assert.deepEqual(doc().population, groupedBaseline.population, 'Removing the related field column must restore the original exact source membership');
  const restoredRows = groupedRows;
  await rendered(restoredRows);
  record('apply-remove-related-step-restores-native-group-table', removeApplyStarted, { commandStatus: removeCommand.status });
  const restoredPreview = await openTable(restoredRows, 'reload-restored-available-witness-group-table', preview => {
    assert.equal(preview.rowCount, witnesses.length);
    assert.equal(preview.rows.length, witnesses.length);
    assert(!preview.columns.some((column) => column.column === relatedColumn.column),
      'Reloaded Group preview still contains the removed related value');
    const patientIDs = preview.rows.map((row) => row[groupKeyName]);
    assert.deepEqual(patientIDs.slice().sort(), witnesses.map((witness) => witness.patient.id).sort(),
      'Reload after related-source removal must restore the exact grouped Patient identities');
    for (const witness of witnesses) {
      const row = preview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
      assert(row, `Reloaded Group preview omitted ${witness.category} witness`);
      assert.equal(row.row_count, witness.expectedContributorRows, `${witness.category} grouped count differs from the raw CDA oracle`);
      assert.equal(Object.hasOwn(row, relatedColumn.column), false, `${witness.category} restored row still contains the removed related value`);
    }
  });
  report.relatedSourceRestoration = {
    constructionMatchesBaseline: true,
    populationMatchesBaseline: true,
    columnsMatchBaseline: true,
    previewRowsMatchBaseline: true,
    relatedOutputAbsent: !restoredPreview.columns.some((column) => column.column === relatedColumn.column) &&
      restoredPreview.rows.every((row) => !Object.hasOwn(row, relatedColumn.column)),
    reloadedRowCount: restoredPreview.rowCount,
    expectedRowCount: witnesses.length,
    patientIds: witnesses.map((witness) => witness.patient.id),
  };

  if (savedEmptyPolicyEdit) {
    const beforeApplyEdit = await api(`${base}/builder`);
    const beforeApplyDocument = doc(beforeApplyEdit);
    const preserveSteps = beforeApplyDocument.construction.steps.filter((step) =>
      step.operation?.kind === 'RELATED_EXPAND' &&
      step.operation.relatedExpand?.targetResourceType === 'Observation');
    assert.equal(preserveSteps.length, 1, 'The saved Patient→Observation expansion must be unique before Apply');
    const preserveStep = preserveSteps[0];
    const preserveExpand = preserveStep.operation.relatedExpand;
    const preserveOutput = preserveStep.outputs.find((output) => output.id === preserveExpand.relatedRecordColumnId);
    assert(preserveOutput, 'The saved expansion must retain its related Observation identity output before Apply');
    assert.equal(preserveExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.equal(preserveOutput.nullable, true);
    const beforeApplyConstruction = structuredClone(beforeApplyDocument.construction);

    const editStarted = Date.now();
    const editFromIndex = nativeCapture.requests.length;
    await clickNative(nativePage, `[data-testid="construction-history-step-${preserveStep.id}"]`);
    await clickNative(nativePage, `[data-testid="construction-edit-step-${preserveStep.id}"]`);
    const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
    await waitForObservable(nativePage, ({ selector }) => {
      const policy = document.querySelector(selector);
      return Boolean(policy && !policy.disabled && policy.value === 'PRESERVE_PARENT');
    }, { selector: policySelector }, 5000);
    await selectNative(nativePage, policySelector, 'EXCLUDE');
    const excludePanel = await proposal('edit-saved-related-expand-exclude-apply-preview', editStarted, [], editFromIndex);
    const applyProposalEntry = nativeCapture.requests.findLast((entry) => entry.startedAt >= editStarted && Number.isInteger(entry.status) &&
      entry.path.endsWith('/construction-proposals') && entry.response?.proposalId === excludePanel.proposalId);
    assert(applyProposalEntry, 'The saved RelatedExpand EXCLUDE Apply preview must match a captured native proposal request');
    assert.equal(applyProposalEntry.status, 200, JSON.stringify(applyProposalEntry.response));
    const applyProposalResponse = applyProposalEntry.response;
    assert.equal(applyProposalEntry.request?.outputId, outputId);
    assert.equal(applyProposalResponse?.previewStatus, 'READY', JSON.stringify(applyProposalResponse));
    assert.equal(applyProposalResponse?.preview?.rowCount, 0);
    assert.deepEqual(applyProposalResponse?.preview?.rows, []);
    const expectedExcludeConstruction = structuredClone(beforeApplyConstruction);
    const expectedExcludeStep = expectedExcludeConstruction.steps.find((step) => step.id === preserveStep.id);
    assert(expectedExcludeStep, 'The Apply candidate wire must retain the saved RelatedExpand step identity');
    expectedExcludeStep.operation.relatedExpand.emptyPolicy = 'EXCLUDE';
    const expectedExcludeOutput = expectedExcludeStep.outputs.find((output) =>
      output.id === preserveExpand.relatedRecordColumnId);
    assert(expectedExcludeOutput, 'The Apply candidate wire must retain the Observation output identity');
    expectedExcludeOutput.nullable = false;
    assert.deepEqual(applyProposalEntry.request?.candidateConstruction?.steps, expectedExcludeConstruction.steps,
      'The Apply candidate wire must preserve saved steps and bindings except the EXCLUDE policy and required output nullability');
    const applyCandidateStep = applyProposalEntry.request.candidateConstruction.steps.find((step) => step.id === preserveStep.id);
    const applyCandidateExpand = applyCandidateStep.operation.relatedExpand;
    const applyResponseStep = applyProposalResponse.candidateConstruction?.steps?.find((step) => step.id === preserveStep.id);
    const applyResponseOutput = applyResponseStep?.outputs?.find((output) => output.id === preserveOutput.id);
    assert(applyResponseOutput, 'The normalized EXCLUDE proposal must retain the related Observation output');
    assert.equal(Object.hasOwn(applyResponseOutput, 'nullable'), false,
      'The normalized EXCLUDE output must serialize typed false nullable as omitted');
    assert.equal(applyCandidateStep.id, preserveStep.id);
    assert.equal(applyCandidateExpand.choiceId, preserveExpand.choiceId);
    assert.equal(applyCandidateExpand.targetNodeId, preserveExpand.targetNodeId);
    assert.equal(applyCandidateExpand.targetResourceType, preserveExpand.targetResourceType);
    assert.deepEqual(applyCandidateExpand.route, preserveExpand.route);
    assert.equal(applyCandidateExpand.relatedRecordColumnId, preserveExpand.relatedRecordColumnId);
    assert.equal(applyCandidateExpand.emptyPolicy, 'EXCLUDE');
    assert.equal(applyCandidateStep.outputs.find((output) => output.id === preserveOutput.id)?.nullable, false);

    const applyReloadStarted = Date.now();
    await applyProposal([], 'apply-saved-related-expand-exclude-to-zero-rows');
    const appliedDomRows = await inspectPage(nativePage, () =>
      [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
        .slice(1).map((row) => [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.innerText.trim()))
        .filter((row) => row.length));
    assert.deepEqual(appliedDomRows, [], 'Applying EXCLUDE must render exactly zero table rows');
    builder = await api(`${base}/builder`);
    const appliedExpansion = doc().construction.steps.find((step) => step.id === preserveStep.id);
    assert.equal(appliedExpansion?.operation.relatedExpand.emptyPolicy, 'EXCLUDE',
      'Applying the saved edit must persist EXCLUDE on the same RelatedExpand step');
    const appliedOutput = appliedExpansion?.outputs.find((output) => output.id === preserveOutput.id);
    assert(appliedOutput, 'The persisted EXCLUDE step must retain its related Observation output');
    assert.equal(Object.hasOwn(appliedOutput, 'nullable'), false,
      'The persisted EXCLUDE output must serialize typed false nullable as omitted');
    assert.deepEqual(doc().population, beforeApplyDocument.population,
      'Applying EXCLUDE must preserve the exact selected Patient population');
    const appliedPolicyEvidence = {
      savedPolicy: appliedExpansion?.operation.relatedExpand.emptyPolicy,
      outputNullable: false,
      outputNullableFieldOmitted: !Object.hasOwn(appliedOutput, 'nullable'),
    };
    const excludedPreview = await openTable([], 'reload-saved-related-expand-exclude-zero-rows');
    assert.equal(excludedPreview.rowCount, 0, 'Reload must retain the exact zero-row EXCLUDE result');
    assert.deepEqual(excludedPreview.rows, [], 'Reload must not fabricate a parent row or related value');
    const reloadedDomRows = await inspectPage(nativePage, () =>
      [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
        .slice(1).map((row) => [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.innerText.trim()))
        .filter((row) => row.length));
    assert.deepEqual(reloadedDomRows, [], 'Reload must render exactly zero table rows for the saved EXCLUDE expansion');
    builder = await api(`${base}/builder`);
    const reloadedExpansion = doc().construction.steps.find((step) => step.id === preserveStep.id);
    assert.equal(reloadedExpansion?.operation.relatedExpand.emptyPolicy, 'EXCLUDE',
      'Reload must retain the saved EXCLUDE policy');
    const reloadedOutput = reloadedExpansion?.outputs.find((output) => output.id === preserveOutput.id);
    assert(reloadedOutput, 'Reload must retain the EXCLUDE related Observation output');
    assert.equal(Object.hasOwn(reloadedOutput, 'nullable'), false,
      'Reloaded EXCLUDE output must serialize typed false nullable as omitted');
    record('apply-and-reload-saved-related-expand-exclude-zero-rows', applyReloadStarted, {
      rowCount: excludedPreview.rowCount, rows: excludedPreview.rows,
      domRowsAfterApply: appliedDomRows, domRowsAfterReload: reloadedDomRows,
      savedPolicy: reloadedExpansion.operation.relatedExpand.emptyPolicy,
      outputNullable: false, outputNullableFieldOmitted: !Object.hasOwn(reloadedOutput, 'nullable'),
    });

    const beforeRemoval = await api(`${base}/builder`);
    const beforeRemovalDocument = doc(beforeRemoval);
    const excludedExpansion = beforeRemovalDocument.construction.steps.find((step) => step.id === preserveStep.id);
    assert.equal(excludedExpansion?.operation.relatedExpand.emptyPolicy, 'EXCLUDE');
    const removeStarted = Date.now();
    await clickNative(nativePage, `[data-testid="construction-history-step-${preserveStep.id}"]`);
    const removeButton = nativePage.locator(`[data-testid="construction-remove-step-${preserveStep.id}"]`);
    await removeButton.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await removeButton.count(), 1, 'The saved RelatedExpand removal control must be unique');
    assert.equal(await removeButton.isEnabled(), true);
    const removalFromIndex = nativeCapture.requests.length;
    await clickNative(nativePage, `[data-testid="construction-remove-step-${preserveStep.id}"]`);
    const removalIdentity = {
      stepId: preserveStep.id,
      outputId,
      snapshotToken: beforeRemoval.catalog.snapshotToken,
      draftVersion: beforeRemoval.draftVersion,
      draftDigest: beforeRemoval.draftDigest,
    };
    const removalPanel = await proposal('remove-saved-exclude-related-expand-restores-patient-preview', removeStarted, groupedRows,
      removalFromIndex, (entry, response) => matchesNativeConstructionRemovalProposal(entry, response, removalIdentity));
    const removalProposalEntry = nativeCapture.requests.findLast((entry) => entry.startedAt >= removeStarted && Number.isInteger(entry.status) &&
      matchesNativeConstructionRemovalProposal(entry, entry.response, removalIdentity) &&
      entry.response?.proposalId === removalPanel.proposalId);
    assert(removalProposalEntry, 'Removing the saved EXCLUDE expansion must match a captured native proposal');
    assert.deepEqual(removalProposalEntry.request?.removeStepIds, [preserveStep.id],
      'The restoration proposal must target the exact saved RelatedExpand step');
    const removalResponse = removalProposalEntry.response;
    assert.equal(removalResponse?.previewStatus, 'READY', JSON.stringify(removalResponse));
    assert.equal(removalResponse?.preview?.rowCount, 1,
      'Removing the upstream expansion must restore the exact selected Patient row in its proposal');
    assert.equal(removalResponse?.preview?.rows?.length, 1);
    const expectedRestoredConstruction = structuredClone(beforeRemovalDocument.construction);
    expectedRestoredConstruction.steps = expectedRestoredConstruction.steps.filter((step) => step.id !== preserveStep.id);
    for (const step of expectedRestoredConstruction.steps) {
      for (const input of step.inputs) {
        if (input.kind === 'STEP_OUTPUT' && input.stepId === preserveStep.id) {
          input.kind = 'SOURCE_PROJECTION';
          delete input.stepId;
        }
      }
    }
    const restoredGroupStep = expectedRestoredConstruction.steps.find((step) => step.operation.kind === 'GROUP');
    assert(restoredGroupStep, 'Removing the expansion must preserve the independent Patient Group step');
    assert.deepEqual(restoredGroupStep.inputs, [{ kind: 'SOURCE_PROJECTION' }],
      'The surviving Group step must read directly from the Patient source projection');
    const restoredGroupCount = restoredGroupStep.outputs.find((output) => output.id ===
      restoredGroupStep.operation.group.aggregates.find((aggregate) => aggregate.operation === 'COUNT_ROWS')?.outputColumnId);
    assert(restoredGroupCount, 'The surviving Patient Group step must retain its COUNT_ROWS output');
    const rootOutput = doc(sourceBaseline).columns.find((column) => column.label === rootTitle);
    assert(rootOutput, 'The original Patient source ID column must remain available as the restoration oracle');
    assert.equal(removalResponse.preview.rows[0][rootOutput.column], witnesses[0].patient.id,
      'The removal preview must restore the exact independently selected Patient identity');
    assert.deepEqual(removalResponse.candidateConstruction?.steps, expectedRestoredConstruction.steps,
      'Removing the expansion must preserve the valid Patient Group and rebind it to the original source projection');

    const restoreStarted = Date.now();
    await applyProposal(groupedRows, 'apply-remove-saved-related-expand-restores-patient');
    builder = await api(`${base}/builder`);
    assert.deepEqual(doc().construction, expectedRestoredConstruction,
      'Removing the saved expansion must retain only the source-backed Patient Group construction');
    assert.deepEqual(doc().columns, doc(sourceBaseline).columns,
      'Removing the saved expansion must restore the original Patient source columns');
    assert.deepEqual(doc().population, doc(sourceBaseline).population,
      'Removing the saved expansion must restore the exact original Patient population');
    const restoredPatientPreview = await openTable(groupedRows, 'reload-restored-exact-patient-source-row');
    assert.equal(restoredPatientPreview.rowCount, 1);
    assert.equal(restoredPatientPreview.rows.length, 1);
    assert.equal(restoredPatientPreview.rows[0][rootOutput.column], witnesses[0].patient.id,
      'Reload after expansion removal must render the exact selected Patient source identity');
    assert.equal(restoredPatientPreview.rows[0][restoredGroupCount.name], witnesses[0].expectedContributorRows,
      'Reload after expansion removal must retain the exact independently counted Patient Group result');
    record('remove-saved-related-expand-restores-exact-patient-after-reload', removeStarted, {
      proposalRowCount: removalResponse.preview.rowCount,
      restoredRowCount: restoredPatientPreview.rowCount,
      parentId: restoredPatientPreview.rows[0][rootOutput.column],
    });
    record('apply-and-reload-remove-expansion-restoration', restoreStarted, {
      restoredRowCount: restoredPatientPreview.rowCount,
      parentId: restoredPatientPreview.rows[0][rootOutput.name],
    });
    report.savedEmptyPolicyEvidence.apply = {
      candidateWireExact: true, previewRowCount: applyProposalResponse.preview.rowCount,
      previewRows: applyProposalResponse.preview.rows,
      responseNullableFieldOmitted: !Object.hasOwn(applyResponseOutput, 'nullable'),
      stepId: applyCandidateStep.id, routeChoiceId: applyCandidateExpand.choiceId,
      route: applyCandidateExpand.route, targetNodeId: applyCandidateExpand.targetNodeId,
      targetResourceType: applyCandidateExpand.targetResourceType,
      relatedRecordColumnId: applyCandidateExpand.relatedRecordColumnId,
      savedPolicyAfterApply: appliedPolicyEvidence.savedPolicy,
      outputNullableAfterApply: appliedPolicyEvidence.outputNullable,
      outputNullableFieldOmittedAfterApply: appliedPolicyEvidence.outputNullableFieldOmitted,
      savedPolicyAfterReload: reloadedExpansion.operation.relatedExpand.emptyPolicy,
      outputNullableAfterReload: false,
      outputNullableFieldOmittedAfterReload: !Object.hasOwn(reloadedOutput, 'nullable'),
      reloadRowCount: excludedPreview.rowCount, reloadRows: excludedPreview.rows,
      domRowsAfterApply: appliedDomRows, domRowsAfterReload: reloadedDomRows,
    };
    report.savedEmptyPolicyEvidence.restoration = {
      removeStepIds: removalProposalEntry.request.removeStepIds,
      restoredConstruction: true, retainedGroupStepId: restoredGroupStep.id,
      restoredGroupCountOutput: restoredGroupCount.name,
      restoredColumns: true, restoredPopulation: true,
      rowCount: restoredPatientPreview.rowCount, parentId: restoredPatientPreview.rows[0][rootOutput.column],
      groupCount: restoredPatientPreview.rows[0][restoredGroupCount.name],
    };
  }

  if (zeroObservationMode) {
    const [finalWitness] = rereadZeroObservationWitness();
    assert(finalWitness, 'The selected Patient disappeared during the native lifecycle');
    assert.equal(finalWitness.matchingObservationCount, 0,
      'The independent exact Patient reread found an incoming Observation edge after the native lifecycle');
    assert.equal(finalWitness.patient.id, witnesses[0].patient.id,
      'The exact scoped Patient identity changed during the native lifecycle');
    report.oracle.finalScopedRereadMatched = true;
  }
  if (referenceFieldMode) {
    const [finalReference] = rawQuery(report.oracle.exactMembershipQuery);
    assert(finalReference, 'The pinned source witness disappeared during the native lifecycle');
    assert.deepEqual(finalReference.observations.map(({ _id, specimenReference }) => ({ _id, specimenReference })),
      manyWitness.observationKeys.map((_id, index) => ({ _id, specimenReference: manyWitness.observationReferences[index] })),
      'The independent raw Observation reference membership changed during the native Explorer lifecycle');
    report.referenceOracleAssertions.finalRawRereadMatched = true;
  }
  if (quantityFieldMode) {
    const [finalQuantityMember] = rawQuery(report.oracle.exactMembershipQuery);
    assert(finalQuantityMember, 'The exact Basic Patient disappeared during the native numeric Related Source lifecycle');
    assert.equal(finalQuantityMember.patient._id, report.oracle.exactMembershipScope.patientKey);
    assert.deepEqual(finalQuantityMember.observations.map(({ id, _id, value, quantity }) => ({ id, _id, value, quantity })),
      manyWitness.observationIds.map((id, index) => ({ id, _id: manyWitness.observationKeys[index],
        value: manyWitness.quantityValues[index], quantity: manyWitness.observationQuantities[index] })),
      'The final raw reread must preserve exact Observation identities and decimal Quantity values');
    report.oracle.finalScopedRereadMatched = true;
  }
  if (statusFieldMode && !basicMode) {
    const finalStatusMembers = rawQuery(report.oracle.exactMembershipQuery);
    assert.deepEqual(finalStatusMembers.map(({ _id }) => _id).sort(), report.oracle.selectedSpecimenKeys,
      'The exact status-witness reread must preserve the selected project/generation-scoped Specimen roots');
    const finalStatusByIdentity = new Map(finalStatusMembers.flatMap(({ observationValues }) => observationValues)
      .map((observation) => [observation._id, { id: observation.id, _id: observation._id, status: observation.status }]));
    const finalStatusPairs = [...finalStatusByIdentity.values()].sort((left, right) => left._id.localeCompare(right._id));
    const expectedStatusPairs = manyWitness.observationKeys.map((key, index) => ({
      id: manyWitness.observationIds[index], _id: key, status: manyWitness.observationStatuses[index],
    })).sort((left, right) => left._id.localeCompare(right._id));
    assert.deepEqual(finalStatusPairs, expectedStatusPairs,
      'The final raw reread must preserve every exact Observation identity/status pair used by the ONE-to-ALL witness');
    report.oracle.finalStatusMembership = { project, generation,
      selectedSpecimenKeys: report.oracle.selectedSpecimenKeys,
      selectedPatientIds: report.oracle.exactMembershipScope.patientIds,
      observationPairs: finalStatusPairs };
    report.oracle.finalScopedRereadMatched = true;
  }
  const expectedHttpFailures = nativeCapture.requests.filter(entry =>
    expectedRelatedSourceOneValidation(entry, expectedRelatedSource()));
  for (const expectedFailure of expectedHttpFailures) {
    const expectedFailureIndex = nativeCapture.requests.indexOf(expectedFailure);
    assert(expectedFailureIndex >= 0, 'Expected ONE validation must be present in the scoped native request capture');
    const capturedFailure = await nativeCapture.waitForResponse(entry => entry === expectedFailure && entry.status === 422,
      { fromIndex: expectedFailureIndex, timeoutMs: 5000 });
    nativeCapture.markExpectedValidation(capturedFailure, {
      status: 422,
      reason: 'Independent raw CDA witness predicts grouped-row ONE has multiple values.',
      proof: { outputId, fieldMode, witnessObservationIds: manyWitness.observationIds, errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' },
    });
    assert.equal(typeof expectedFailure.requestId, 'string',
      'The expected ONE validation must expose the canonical request ID from its X-Request-ID header');
    assert(expectedFailure.requestId.trim(), 'The expected ONE validation request ID must not be empty');
    const fixtureEntry = await officialRequestCapture.waitForResponse(entry =>
      matchesExpectedRelatedSourceOneValidationCapture(entry, expectedFailure), { timeoutMs: 5000 });
    assert(fixtureEntry, 'The official CDA fixture must capture the same exact expected ONE validation response');
    cda.expectHttpFailure(fixtureEntry, 'The independent raw CDA witness predicts multiple related values for grouped-row ONE', {
      outputPath: expectedFailure.path,
      method: expectedFailure.method,
      status: expectedFailure.status,
      requestId: expectedFailure.requestId,
      errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
      fieldMode,
      witnessObservationIds: manyWitness.observationIds,
    });
  }
  const unexpectedHttp = nativeCapture.requests.filter((entry) => entry.status >= 400 && !expectedHttpFailures.includes(entry));
  const expectedOneFailureCount = (basicMode && !quantityFieldMode) || zeroObservationMode ? 0 : 1;
  assert.equal(expectedHttpFailures.length, expectedOneFailureCount, basicMode && !quantityFieldMode
    ? 'The exact single-Observation basic status witness must permit ONE'
    : quantityFieldMode
      ? 'The exact two-value Quantity witness must reject ONE once before same-chooser ALL repair'
    : zeroObservationMode
      ? 'The exact zero-Observation root must permit ONE with the nullable result defined by the source contract'
      : 'Exactly one raw-oracle-predicted ONE disagreement is expected');
  assert.deepEqual(unexpectedHttp, [], 'No unexpected browser HTTP errors are allowed');
  assert.deepEqual(report.errors.filter(error => error.expectedValidation !== true), [],
    'No unexpected browser runtime, console, network, or module errors are allowed');
  report.browserDiagnostics = cda.diagnostics;
  assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
  report.relatedFieldLifecycle = 'passed';
  if ((quantityFieldMode || !basicMode) && !zeroObservationMode) report.repairStatus = 'passed';
  if ((report.oracle.missingWitnessCategories ?? []).length > 0) {
    report.optionalFixtureGaps = report.oracle.missingWitnessCategories;
  }
  report.status = 'passed';
  }
} catch (error) {
  const apiBuildInvalidation = error instanceof ApiBuildFreezeError || error?.invalidatesRun === true;
  fixtureUnavailable = error?.fixtureUnavailableOutcome;
  fatalError = error;
  report.status = apiBuildInvalidation ? 'invalidated' : fixtureUnavailable ? 'unverified' : 'failed';
  if (apiBuildInvalidation) {
    report.productFailure = false;
    report.apiBuildFreeze = { ...report.apiBuildFreeze, invalidatesRun: true, productFailure: false,
      error: String(error), reason: error.reason, before: error.before, after: error.after };
  }
  if (fixtureUnavailable) {
    report.productFailure = false;
    report.fixtureUnavailable = fixtureUnavailable;
    report.skipReason = fixtureUnavailable.reason;
  }
  if (!fixtureUnavailable) {
    report.error = String(error.stack ?? error);
    report.failureUI = nativePage ? await inspectPage(nativePage, () => {
      const dialog = document.querySelector('[role="dialog"]');
      const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
      const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
      return { body: document.body.innerText.slice(0, 5000), chooser: { open: Boolean(dialog), policy: policy?.value },
        proposal: { status: proposal?.dataset.proposalStatus, text: proposal?.innerText } };
    }).catch(captureError => sanitizeText(captureError.message)) : undefined;
    report.savedBuilderAtFailure = builder ? await api(`${base}/builder`).catch((readError) => ({ readError: sanitizeText(readError.message) })) : undefined;
    if (!apiBuildInvalidation) report.status = 'failed';
  }
} finally {
  if (apiBuildCheckStarted && frozenApiBuild) {
    try {
      report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()) };
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true,
        productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after };
      report.status = 'invalidated';
    }
  }
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    report.status = 'invalidated';
  }
  const sourceFingerprintAfter = sourceFingerprint(sourceRoot);
  report.sourceFingerprint.after = sourceFingerprintAfter;
  report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === sourceFingerprintAfter.sha256
    && report.sourceFingerprint.before.files === sourceFingerprintAfter.files;
  report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
  report.sourceFingerprint.productFailure = false;
  if (!report.sourceFingerprint.unchanged) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFingerprint.changed = true;
    report.status = 'invalidated';
  }
  report.finished = new Date().toISOString();
  cda.report.standaloneCdaRows = report;
  await nativeCapture?.stop();
  await cda.attachReport('standalone-cda-related-one-all.json', report);
  if (!fatalError || (fixtureUnavailable && report.relatedFieldLifecycle === 'passed')) {
    if (zeroStartingRootRoundtrip) {
      const roundtrip = report.zeroObservationStartingRootRoundtrip;
      const timingNames = [
        'select-observation-root-cancel-proposal', 'cancel-zero-observation-root-proposal',
        'reload-patient-after-root-cancel', 'select-observation-root-apply-observation-root',
        'apply-observation-root-zero-rows', 'reload-observation-root-zero-rows',
        'select-patient-root-for-restoration', 'apply-restore-patient-root-row',
        'reload-restored-patient-root-row',
      ];
      const durationByName = new Map(report.cases.map(({ name, durationMs }) => [name, durationMs]));
      const timings = timingNames.map(name => ({ name, durationMs: durationByName.get(name), limitMs: ACTION_TIMEOUT_MS }));
      const allWithinBudget = timings.every(({ durationMs }) => Number.isFinite(durationMs) && durationMs <= ACTION_TIMEOUT_MS);
      cda.check('correctness',
        'the exact scoped selected Patient witness has zero typed Observation subject edges and remains the immutable collection',
        report.oracle?.finalScopedRereadMatched === true
          && zeroObservationSelectionRevisionMatches(roundtrip)
          && roundtrip?.selectionSeed?.patientId === report.oracle?.witnessSummary?.patientId
          && roundtrip?.selectionSeed?.memberCount === 1,
        { witness: roundtrip?.finalRawWitness, selection: roundtrip?.selectionSeed });
      cda.check('correctness',
        'the Patient root starts with its exact ID row and Cancel leaves the saved workspace unchanged',
        roundtrip?.initial?.rootResourceType === 'Patient' && roundtrip?.initial?.rows?.length === 1
          && roundtrip?.initial?.rows[0]?.[0] === report.oracle?.witnessSummary?.patientId
          && roundtrip?.cancel?.workspaceUnchanged === true,
        { initial: roundtrip?.initial, cancel: roundtrip?.cancel });
      cda.check('correctness',
        'Observation root preview and Apply retain the Patient ID column with exactly zero rows',
        roundtrip?.observationRoot?.rootResourceType === 'Observation'
          && roundtrip?.observationRoot?.headers?.includes('PATIENT ID')
          && roundtrip?.observationRoot?.previewRowCount === 0 && roundtrip?.observationRoot?.rows?.length === 0,
        { observationRoot: roundtrip?.observationRoot });
      cda.check('persistence',
        'reloading the Observation root preserves the exact selected Patient population route and zero rows',
        roundtrip?.observationReload?.rootResourceType === 'Observation'
          && roundtrip?.observationReload?.selectionRevisionId === roundtrip?.selectionRevisionId
          && roundtrip?.observationReload?.populationRoute?.length === 1
          && roundtrip?.observationReload?.rows?.length === 0,
        { observationReload: roundtrip?.observationReload });
      cda.check('correctness',
        'restoring Patient returns to the exact original ID column, direct collection route, and Patient row',
        roundtrip?.restoredPatient?.rootResourceType === 'Patient'
          && roundtrip?.restoredPatient?.selectionRevisionId === roundtrip?.selectionRevisionId
          && JSON.stringify(roundtrip?.restoredPatient?.populationRoute) === JSON.stringify([])
          && JSON.stringify(roundtrip?.restoredPatient?.rows) === JSON.stringify([[report.oracle?.witnessSummary?.patientId]]),
        { restoredPatient: roundtrip?.restoredPatient });
      cda.check('persistence',
        'reload preserves the restored Patient root and exact raw Patient row after the full roundtrip',
        roundtrip?.restoredReload?.rootResourceType === 'Patient'
          && roundtrip?.restoredReload?.selectionRevisionId === roundtrip?.selectionRevisionId
          && JSON.stringify(roundtrip?.restoredReload?.rows) === JSON.stringify([[report.oracle?.witnessSummary?.patientId]])
          && report.oracle?.finalScopedRereadMatched === true,
        { restoredReload: roundtrip?.restoredReload, finalRawWitness: roundtrip?.finalRawWitness });
      cda.check('performance', 'each root choice, Cancel, Apply, and reload reaches its exact rendered state within five seconds',
        timings.length === timingNames.length && allWithinBudget,
        { timings, expectedTimingCount: timingNames.length, limitMs: ACTION_TIMEOUT_MS });
      cda.check('correctness', 'CDA watched source and API build stayed unchanged',
        report.sourceFreeze?.unchanged === true && report.sourceFingerprint?.unchanged === true
          && report.apiBuildFreeze?.unchanged === true,
        { sourceFreezeUnchanged: report.sourceFreeze?.unchanged,
          sourceFingerprintUnchanged: report.sourceFingerprint?.unchanged, apiBuildUnchanged: report.apiBuildFreeze?.unchanged });
    } else if (savedEmptyPolicyEdit) {
      const timingNames = [
        'edit-saved-related-expand-exclude-cancel-preview',
        'cancel-saved-related-expand-exclude-preserves-parent-and-all-after-reload',
        'edit-saved-related-expand-exclude-apply-preview',
        'apply-and-reload-saved-related-expand-exclude-zero-rows',
        'remove-saved-exclude-related-expand-restores-patient-preview',
        'remove-saved-related-expand-restores-exact-patient-after-reload',
        'apply-and-reload-remove-expansion-restoration',
      ];
      const policyTimings = report.cases.filter(({ name }) => timingNames.includes(name))
        .map(({ name, durationMs }) => ({ name, durationMs, limitMs: 5000 }));
      const allTimingsWithinBudget = policyTimings.length === timingNames.length &&
        policyTimings.every(({ durationMs }) => Number.isFinite(durationMs) && durationMs <= 5000);
      const initialExpansion = report.zeroObservationExpansion;
      const policyEvidence = report.savedEmptyPolicyEvidence;
      cda.check('correctness',
        'bounded zero-match Patient stays in the exact authorized project/generation with the signed Patient→Observation route',
        report.oracle.finalScopedRereadMatched === true && report.oracle.witnessSummary?.rootCount === 1 &&
          report.oracle.exactMembershipScope?.project === project && report.oracle.exactMembershipScope?.generation === generation &&
          report.currentRelatedChoiceId && report.relatedFieldCandidate?.nodeId &&
          JSON.stringify(report.relatedChoiceAssertions.at(-1)?.route) === JSON.stringify(expectedPatientObservationRoute),
        { project, generation, exactPatient: report.oracle.witnessSummary, exactScope: report.oracle.exactMembershipScope,
          relatedChoiceId: report.currentRelatedChoiceId, relatedCandidate: report.relatedFieldCandidate,
          signedRoute: report.relatedChoiceAssertions.at(-1)?.route,
          expansionRoute: initialExpansion?.route, finalRawRereadMatched: report.oracle.finalScopedRereadMatched });
      cda.check('correctness',
        'initial PRESERVE_PARENT expansion saves one exact Patient row with a null related Observation ID',
        initialExpansion?.savedPolicy === 'PRESERVE_PARENT' && initialExpansion?.savedOutputNullable === true &&
          initialExpansion?.outputNullable === true && initialExpansion?.previewSchemaNullable === true &&
          initialExpansion?.previewRowCount === 1 && initialExpansion?.unmatchedObservationOutputIsNull === true &&
          initialExpansion?.patientId === report.oracle.witnessSummary?.patientId,
        { ...initialExpansion,
          rowCount: report.oracle.witnessSummary?.rootCount });
      cda.check('correctness',
        'saved EXCLUDE edit preserves step/route/target/output IDs, clears output nullability, and previews exactly zero rows',
        policyEvidence?.cancelCandidate?.candidateWireExact === true && policyEvidence?.cancelCandidate?.stepId === initialExpansion?.savedStepId &&
          policyEvidence?.cancelCandidate?.routeChoiceId === initialExpansion?.routeChoiceId &&
          policyEvidence?.cancelCandidate?.targetNodeId === initialExpansion?.targetNodeId &&
          policyEvidence?.cancelCandidate?.targetResourceType === initialExpansion?.targetResourceType &&
          policyEvidence?.cancelCandidate?.relatedRecordColumnId === initialExpansion?.relatedRecordColumnId &&
          JSON.stringify(policyEvidence?.cancelCandidate?.route) === JSON.stringify(initialExpansion?.route) &&
          policyEvidence?.cancelCandidate?.outputNullable === false && policyEvidence?.cancelCandidate?.responseNullableFieldOmitted === true &&
          policyEvidence?.cancelCandidate?.rowCount === 0 &&
          Array.isArray(policyEvidence?.cancelCandidate?.rows) && policyEvidence.cancelCandidate.rows.length === 0 &&
          policyEvidence?.apply?.candidateWireExact === true && policyEvidence?.apply?.stepId === initialExpansion?.savedStepId &&
          policyEvidence?.apply?.routeChoiceId === initialExpansion?.routeChoiceId &&
          policyEvidence?.apply?.targetNodeId === initialExpansion?.targetNodeId &&
          policyEvidence?.apply?.targetResourceType === initialExpansion?.targetResourceType &&
          policyEvidence?.apply?.relatedRecordColumnId === initialExpansion?.relatedRecordColumnId &&
          policyEvidence?.apply?.responseNullableFieldOmitted === true &&
          JSON.stringify(policyEvidence?.apply?.route) === JSON.stringify(initialExpansion?.route) &&
          policyEvidence?.apply?.previewRowCount === 0 &&
          Array.isArray(policyEvidence?.apply?.previewRows) && policyEvidence.apply.previewRows.length === 0,
        { cancelCandidate: policyEvidence?.cancelCandidate, applyCandidate: policyEvidence?.apply,
          preserveParentExpansion: initialExpansion });
      cda.check('correctness',
        'Cancel restores the saved PRESERVE_PARENT construction, population, and exact rendered rows',
        policyEvidence?.cancel?.constructionUnchanged === true && policyEvidence?.cancel?.populationUnchanged === true &&
          policyEvidence?.cancel?.parentRowCount === 1 && policyEvidence?.cancel?.parentId === report.oracle.witnessSummary?.patientId &&
          policyEvidence?.cancel?.savedPolicy === 'PRESERVE_PARENT' && policyEvidence?.cancel?.relatedOutputNullable === true &&
          Array.isArray(policyEvidence?.cancel?.relatedAllValues) && policyEvidence.cancel.relatedAllValues.length === 0,
        { cancel: policyEvidence?.cancel });
      cda.check('correctness', 'Apply saves EXCLUDE and renders exactly zero rows',
        policyEvidence?.apply?.savedPolicyAfterApply === 'EXCLUDE' && policyEvidence?.apply?.outputNullableAfterApply === false &&
          policyEvidence?.apply?.outputNullableFieldOmittedAfterApply === true &&
          policyEvidence?.apply?.previewRowCount === 0 && Array.isArray(policyEvidence?.apply?.previewRows) &&
          policyEvidence.apply.previewRows.length === 0 && Array.isArray(policyEvidence?.apply?.domRowsAfterApply) &&
          policyEvidence.apply.domRowsAfterApply.length === 0,
        { apply: policyEvidence?.apply });
      cda.check('persistence', 'reload retains the saved EXCLUDE policy and zero-row result',
        policyEvidence?.apply?.savedPolicyAfterReload === 'EXCLUDE' &&
          policyEvidence?.apply?.outputNullableAfterReload === false &&
          policyEvidence?.apply?.outputNullableFieldOmittedAfterReload === true && policyEvidence?.apply?.reloadRowCount === 0 &&
          Array.isArray(policyEvidence?.apply?.reloadRows) && policyEvidence.apply.reloadRows.length === 0 &&
          Array.isArray(policyEvidence?.apply?.domRowsAfterReload) && policyEvidence.apply.domRowsAfterReload.length === 0,
        { apply: policyEvidence?.apply });
      cda.check('persistence',
        'removing the saved expansion restores the exact Patient source row after reload and final raw reread',
        policyEvidence?.restoration?.restoredConstruction === true && policyEvidence?.restoration?.restoredColumns === true &&
          policyEvidence?.restoration?.restoredPopulation === true && policyEvidence?.restoration?.rowCount === 1 &&
          policyEvidence?.restoration?.parentId === report.oracle.witnessSummary?.patientId &&
          report.oracle.finalScopedRereadMatched === true,
        { restoration: policyEvidence?.restoration, finalRawRereadMatched: report.oracle.finalScopedRereadMatched });
      cda.check('performance',
        'policy edit, Apply/reload, and removal/restoration checkpoints each finish within five seconds',
        allTimingsWithinBudget,
        { timings: policyTimings, expectedTimingCount: timingNames.length, limitMs: 5000 });
      cda.check('correctness', 'CDA watched source and API build stayed unchanged',
        report.sourceFreeze?.unchanged === true && report.sourceFingerprint?.unchanged === true &&
          report.apiBuildFreeze?.unchanged === true,
        { sourceFreezeUnchanged: report.sourceFreeze?.unchanged,
          sourceFingerprintUnchanged: report.sourceFingerprint?.unchanged,
          apiBuildUnchanged: report.apiBuildFreeze?.unchanged,
          sourceFingerprint: report.sourceFingerprint, apiBuildFreeze: report.apiBuildFreeze });
    } else {
      const checkName = zeroObservationMode
        ? 'zero Patient Observation ONE/ALL preserves the exact scoped parent through the native lifecycle'
        : 'related ONE/ALL lifecycle preserves exact raw-source values and row identities';
      cda.check('correctness', checkName, true, {
        mode, fieldMode, witnessMode, cases: report.cases.map(({ name }) => name),
        ...(zeroObservationMode ? {
          scopedZeroMatch: report.oracle.finalScopedRereadMatched === true,
          emptyPolicy: report.zeroObservationExpansion?.savedPolicy,
          oneValueIsNull: report.zeroObservationOne?.nullableValueIsNull === true,
          allValueIsEmptyArray: report.zeroObservationAll?.previewValueIsEmptyArray === true,
          parentRowCount: report.oracle.witnessSummary?.rootCount,
          expectedOneValidationCount: 0,
        } : {}),
        exactRawOracle: true, oneValidationCount: nativeCapture.requests.filter(entry => entry.status === 422).length,
      });
      if (zeroObservationMode) {
        for (const check of zeroObservationLifecycleChecks(report)) {
          cda.check(check.dimension, check.name, check.passed, check.evidence);
        }
      }
      if (statusFieldMode && !basicMode) {
        for (const check of statusOneAllLifecycleChecks(report)) {
          cda.check(check.dimension, check.name, check.passed, check.evidence);
        }
      }
      if (quantityFieldMode) {
        const timingNames = [
          'related-observation-one-disagreement',
          'same-chooser-all-repair-preview',
          'cancel-related-all-proposal-preserves-group',
          'reopened-all-preview-before-apply',
          'apply-related-all-to-native-table-render',
          'native-preview-matches-exact-related-field-oracle',
          'reload-related-all-available-cardinality-values',
          'edit-related-field-output-label',
          'reload-edited-related-field-output-label',
          'remove-related-source-step-preview',
          'apply-remove-related-step-restores-native-group-table',
          'reload-restored-available-witness-group-table',
        ];
        const durationByName = new Map(report.cases.map(({ name, durationMs }) => [name, durationMs]));
        const timings = timingNames.map((name) => ({ name, durationMs: durationByName.get(name), limitMs: ACTION_TIMEOUT_MS }));
        const allWithinBudget = timings.length === timingNames.length &&
          timings.every(({ durationMs }) => Number.isFinite(durationMs) && durationMs <= ACTION_TIMEOUT_MS);
        cda.check('correctness',
          'Basic Quantity ONE rejects the two exact same-code decimal values and ALL renders the full numeric array',
          report.oneRejection?.status === 422 && report.oneRejection?.errorCode === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' &&
            JSON.stringify(report.oneRejection?.distinctQuantityValues) === JSON.stringify([172.5, 180]) &&
            report.quantityAllOutputEvidence?.logicalType === 'decimal' &&
            hasSameQuantityValueMultiset(report.quantityAllOutputEvidence?.previewValues, [172.5, 180]) &&
            report.quantityAllOutputEvidence?.identitiesMatch === true,
          { sourceContract: report.quantitySourceContract, oneRejection: report.oneRejection,
            allOutput: report.quantityAllOutputEvidence });
        cda.check('correctness',
          'Cancel preserves the Group and Apply/edit retain the exact Quantity source binding',
          report.quantityAllCancelEvidence?.workspaceUnchanged === true &&
            report.quantityAllCancelEvidence?.constructionUnchanged === true &&
            report.quantityAllCancelEvidence?.populationUnchanged === true &&
            report.quantityAllCancelEvidence?.renderedRowsMatch === true &&
            report.quantityAllSavedEvidence?.sourceMatches === true &&
            report.relatedOutputEditEvidence?.candidateSourceMatches === true &&
            report.relatedOutputEditEvidence?.policy === 'ALL',
          { cancel: report.quantityAllCancelEvidence, saved: report.quantityAllSavedEvidence,
            edit: report.relatedOutputEditEvidence });
        cda.check('persistence',
          'Reload preserves the exact decimal array and removal restores the original Patient Group rows',
          report.quantityAllSavedEvidence?.logicalType === 'decimal' &&
            hasSameQuantityValueMultiset(report.quantityAllSavedEvidence?.previewValues, [172.5, 180]) &&
            hasSameQuantityValueMultiset(report.quantityAllSavedEvidence?.reloadValues, [172.5, 180]) &&
            report.relatedOutputEditEvidence?.labelChanged === true &&
            hasSameQuantityValueMultiset(report.relatedOutputEditEvidence?.reloadValues, [172.5, 180]) &&
            report.relatedSourceRestoration?.constructionMatchesBaseline === true &&
            report.relatedSourceRestoration?.populationMatchesBaseline === true &&
            report.relatedSourceRestoration?.columnsMatchBaseline === true &&
            report.relatedSourceRestoration?.previewRowsMatchBaseline === true &&
            report.relatedSourceRestoration?.relatedOutputAbsent === true &&
            report.oracle?.finalScopedRereadMatched === true,
          { saved: report.quantityAllSavedEvidence, edit: report.relatedOutputEditEvidence,
            restoration: report.relatedSourceRestoration, finalRawRereadMatched: report.oracle?.finalScopedRereadMatched });
        cda.check('performance',
          'Basic Quantity ONE-to-ALL lifecycle action-to-render checkpoints finish within five seconds',
          allWithinBudget, { timings, expectedTimingCount: timingNames.length, limitMs: ACTION_TIMEOUT_MS });
      }
    }
  }
  if (fatalError && !fixtureUnavailable) throw fatalError;
  assert.notEqual(report.apiBuildFreeze?.invalidatesRun, true, `API build changed during Related ONE/ALL: ${JSON.stringify(report.apiBuildFreeze)}`);
  assert.notEqual(report.sourceFreeze?.invalidatesRun, true, `Watched source changed during Related ONE/ALL: ${JSON.stringify(report.sourceFreeze)}`);
  assert.notEqual(report.sourceFingerprint?.invalidatesRun, true, `Watched source fingerprint changed during Related ONE/ALL: ${JSON.stringify(report.sourceFingerprint)}`);
}
return report;
}
