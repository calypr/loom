import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';

export async function mixedSiblingCountWorkflow({ page, cda }) {
let fatal;
const project = cda.project;
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `mixed-sibling-count-${Date.now()}-${randomUUID().slice(0, 8)}`;
const evidence = cda.evidenceDirectory;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = cda.target.arangoContainer;
const apiContainer = cda.target.apiContainer;
const composeProject = cda.target.composeProject;
assert.equal(project, 'loom_dev_cda_fhir', 'This verifier is bound to the CDA-FHIR project');
const patientType = 'Patient';
const targetTypes = ['Condition', 'Observation', 'Specimen'];
const seedLimit = 2000;
const witnessIDLimit = 25;
const protectedRoot = `/api/v1/projects/${project}/explorers/${protectedExplorer}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = `${root}/${explorer}/selections`;

assert.notEqual(explorer, protectedExplorer, 'The verifier must own a fresh QA Explorer');

const report = Object.assign(cda.report, {
  started: new Date().toISOString(),
  explorer,
  protectedExplorer,
  protectedExplorerUntouched: true,
  target: { ...cda.report.target, apiOrigin, uiOrigin, project, generation, arangoContainer },
  localScopeAssertion: {
    mode: 'localhost unrestricted, no-auth local API',
    apiOrigin,
    authorizationHeadersSent: false,
    catalogScopeDigest: null,
  },
  behavior: 'Patient-root table with authored sibling Condition, Observation, and Specimen related COUNT outputs sharing subject_Patient',
  bounds: { sortedScopedPatientSeeds: seedLimit, returnedChildWitnessIDsPerType: witnessIDLimit },
  coverageBoundary: { group: 'deferred', pivot: 'deferred' },
  cases: [],
  apiRequests: [],
  nativeRequests: cda.nativeRequests,
  errors: [],
});

let builder;
let outputId;
let rootColumnId;
let originalWorkspace;
let originalDocument;
let nativeCapture;

const browserEval = (_page, callback, args = []) => cda.inspect(callback, args);
const click = (_page, selector, identity, timeout) => cda.click(selector, identity, timeout);
const navigate = (_page, url) => cda.navigate(url);
const waitForBrowser = (_page, predicate, argsOrTimeout = [], argsOrTimeout2) => Array.isArray(argsOrTimeout)
  ? cda.wait(predicate, argsOrTimeout, argsOrTimeout2 ?? 5000)
  : cda.wait(predicate, [], argsOrTimeout);
const pause = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const pathOf = entry => entry.path.split('?')[0];
const doc = (state = builder) => state.workspace?.documents?.find(document => document.output.id === outputId);
const columnID = column => column?.column ?? column?.id;

class BoundedAbsenceError extends Error {
  constructor(message) { super(message); this.name = 'BoundedAbsenceError'; }
}

const record = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms; required maximum is 5000 ms`);
  report.cases.push({ name, durationMs, ...details });
};

const stableLegacySourceColumnID = (outputID, sourceColumn) => {
  const digest = createHash('sha256').update(`${outputID}\0${sourceColumn}\0`).digest('hex').slice(0, 32);
  return `source_${digest}`;
};

const expectedCanonicalDocumentAfterConstructionRemoval = legacyDocument => {
  assert.equal(legacyDocument.construction, undefined, 'The captured baseline must be the legacy source-only document');
  assert.equal(legacyDocument.tableShape, undefined, 'The captured baseline must have no legacy post-source shape');
  const expected = structuredClone(legacyDocument);
  expected.columns = expected.columns.map(column => ({
    ...column,
    ...(column.columnId ? {} : { columnId: stableLegacySourceColumnID(expected.output.id, column.column) }),
  }));
  expected.construction = { version: 1, steps: [] };
  return expected;
};

const api = async (path, body) => {
  const method = body === undefined ? 'GET' : 'POST';
  const requestID = `mixed-sibling-count-${randomUUID()}`;
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': requestID };
  assert.equal(Object.keys(headers).some(name => name.toLowerCase() === 'authorization'), false);
  const response = await fetch(apiOrigin + path, {
    method, headers,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : undefined; } catch { value = text; }
  report.apiRequests.push({ requestID, method, path, body, status: response.status,
    response: path.endsWith('/builder') ? {
      draftVersion: value?.draftVersion,
      draftDigest: value?.draftDigest,
      catalog: { generation: value?.catalog?.generation, authorizationScopeDigest: value?.catalog?.authorizationScopeDigest },
      documentCount: value?.workspace?.documents?.length ?? 0,
    } : value });
  assert(response.ok, `${response.status} ${method} ${path}: ${JSON.stringify(value)}`);
  return value;
};

const command = async commands => {
  const before = builder;
  const response = await api(`${base}/commands`, {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, before.catalog.generation);
  assert.equal(builder.catalog.authorizationScopeDigest, before.catalog.authorizationScopeDigest);
  return response;
};

const rawQuery = query => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const opening = result.stdout.indexOf('[');
  assert(opening >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1200)}`);
  return JSON.parse(result.stdout.slice(opening));
};

const childCountSubquery = `
  LET distinctChildIDs = (
    FOR edge IN fhir_edge
      FILTER edge._to == patient._id
        AND edge.label == "subject_Patient"
        AND edge.project == ${JSON.stringify(project)}
        AND edge.dataset_generation == ${JSON.stringify(generation)}
        AND STARTS_WITH(edge._from, CONCAT(targetType, "/"))
      LET child = DOCUMENT(edge._from)
      FILTER child != null
        AND child.project == ${JSON.stringify(project)}
        AND child.dataset_generation == ${JSON.stringify(generation)}
        AND child.resourceType == targetType
      COLLECT childID = child._id
      SORT childID
      RETURN childID
  )
  RETURN { resourceType: targetType, count: LENGTH(distinctChildIDs),
    witnessOverflow: LENGTH(distinctChildIDs) > ${witnessIDLimit},
    witnessIDs: SLICE(distinctChildIDs, 0, ${witnessIDLimit}) }
`;

const seedQuery = `
LET patientSeeds = (
  FOR patient IN Patient
    FILTER patient.project == ${JSON.stringify(project)}
      AND patient.dataset_generation == ${JSON.stringify(generation)}
      AND patient.resourceType == ${JSON.stringify(patientType)}
    SORT patient._key
    LIMIT ${seedLimit}
    RETURN patient
)
FOR patient IN patientSeeds
  LET siblingCounts = (
    FOR targetType IN ${JSON.stringify(targetTypes)}
      ${childCountSubquery}
  )
  FILTER LENGTH(siblingCounts) == ${targetTypes.length}
    AND siblingCounts[0].count > 0
    AND siblingCounts[1].count > 0
    AND siblingCounts[2].count > 0
  SORT patient._key
  LIMIT 1
  RETURN {
    patient: { id: patient.id, _id: patient._id, resourceType: patient.resourceType,
      project: patient.project, generation: patient.dataset_generation },
    siblingCounts
  }
`;

const exactOracleQuery = patientID => `
FOR patient IN Patient
  FILTER patient.id == ${JSON.stringify(patientID)}
    AND patient.project == ${JSON.stringify(project)}
    AND patient.dataset_generation == ${JSON.stringify(generation)}
    AND patient.resourceType == ${JSON.stringify(patientType)}
  LET siblingCounts = (
    FOR targetType IN ${JSON.stringify(targetTypes)}
      ${childCountSubquery}
  )
  RETURN {
    patient: { id: patient.id, _id: patient._id, resourceType: patient.resourceType,
      project: patient.project, generation: patient.dataset_generation },
    siblingCounts
  }
`;

const trackBrowser = () => {
  nativeCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base, report, responsePaths: /builder|commands|preview|construction-proposals|population-routes|selections/ });
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.pathname.includes(protectedExplorer)) {
      report.protectedExplorerUntouched = false;
      report.errors.push({ kind: 'protected-explorer-request', path: url.pathname });
    }
    const entry = nativeCapture.byRequest.get(request);
    if (entry?.authorizationHeaderPresent) report.errors.push({ kind: 'unexpected-auth-header', path: entry.path });
  });
};

const waitNative = async (predicate, fromIndex, timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const entry = report.nativeRequests.slice(fromIndex).find(candidate => candidate.completedAt && !candidate.failure && predicate(candidate));
    if (entry) {
      assert.equal(entry.status, 200, `Native ${entry.path} returned ${entry.status}: ${JSON.stringify(entry.response).slice(0, 1500)}`);
      return entry;
    }
    await pause(25);
  }
  assert.fail(`Timed out waiting for native request: ${JSON.stringify(report.nativeRequests.slice(fromIndex).map(({ path, status, complete, failure }) => ({ path, status, complete, failure })))}`);
};

const nativeIndex = () => report.nativeRequests.length;
const previewBody = response => response?.preview?.rows !== undefined ? response.preview : response;
const assertPreviewValues = (preview, expectedTypes, phase) => {
  assert(preview && Array.isArray(preview.rows) && Array.isArray(preview.columns), `${phase} omitted typed Preview rows/columns`);
  assert.equal(preview.outputId, outputId, `${phase} Preview belongs to another output`);
  assert(preview.receiptId, `${phase} Preview has no receipt`);
  assert.equal(preview.rowCount, 1, `${phase} must retain one selected Patient row`);
  assert.equal(preview.rows.length, 1, `${phase} must return exactly one row`);
  const row = preview.rows[0];
  assert.equal(row[rootColumnId], report.oracle.patient.id, `${phase} changed the exact selected Patient identity`);
  for (const targetType of expectedTypes) {
    const column = report.savedColumns[targetType];
    assert(column, `${phase} has no saved ${targetType} output identity`);
    assert(preview.columns.some(candidate => candidate.column === column.name), `${phase} Preview omitted ${targetType} output column`);
    assert.equal(row[column.name], report.oracle.counts[targetType],
      `${phase} ${targetType} COUNT differs from independent raw scoped Arango oracle`);
  }
  assert.equal(preview.columns.length, 1 + expectedTypes.length, `${phase} has unexpected Preview outputs`);
  return { receiptId: preview.receiptId, rowCount: preview.rowCount, values: Object.fromEntries(expectedTypes.map(type => [type, row[report.savedColumns[type].name]])) };
};

const renderTable = async (expectedTypes, phase) => {
  const expectedColumns = 1 + expectedTypes.length;
  await waitForBrowser(page, (args) => { return Boolean(((() => {
    const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return Boolean(table && table.getAttribute('aria-rowcount')==='2' &&
      table.getAttribute('aria-colcount')===args[0] &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:'));
  })())); }, [String(expectedColumns)]);
  const view = await browserEval(page, (args) => { const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return {
    rowCount:table?.getAttribute('aria-rowcount'),columnCount:table?.getAttribute('aria-colcount'),
    headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),
    rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length)
  }; });
  assert.equal(view.rowCount, '2', `${phase} rendered the wrong data row count`);
  assert.equal(view.columnCount, String(expectedColumns), `${phase} rendered the wrong column count`);
  assert.equal(view.rows.length, 1, `${phase} must render the one selected Patient row`);
  const row = view.rows[0];
  const outputKeys = [rootColumnId, ...expectedTypes.map(type => report.savedColumns[type].name)];
  assert.equal(row.length, outputKeys.length, `${phase} rendered the wrong number of bound outputs`);
  const byColumn = new Map(outputKeys.map((column, index) => [column, row[index]]));
  assert.equal(byColumn.get(rootColumnId), report.oracle.patient.id, `${phase} visible row has the wrong Patient ID`);
  for (const targetType of expectedTypes) {
    const saved = report.savedColumns[targetType];
    assert.equal(byColumn.get(saved.name), String(report.oracle.counts[targetType]), `${phase} visible ${targetType} count differs from raw oracle`);
  }
  return view;
};

const openTable = async (expectedTypes, phase) => {
  const startedAt = Date.now();
  const fromIndex = nativeIndex();
  await navigate(page, `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
  await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector(args[0])))); }, [`[data-testid="construction-table-${outputId}"]`]);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false)); });
  const view = await renderTable(expectedTypes, phase);
  const request = await waitNative(entry => pathOf(entry).endsWith('/preview') && entry.body?.outputId === outputId, fromIndex);
  const preview = previewBody(request.response);
  const values = assertPreviewValues(preview, expectedTypes, phase);
  record(phase, startedAt, { rowCount: preview.rowCount, columnCount: preview.columns.length, receiptId: preview.receiptId });
  return { view, request, preview, values };
};

const selectRelatedCount = async targetType => {
  const start = Date.now();
  const fromIndex = nativeIndex();
  await click(page, '[data-testid="construction-action-add-columns"]');
  await click(page, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector('[data-testid="construction-add-columns-source"]')))); });
  const resourcesOpen = await browserEval(page, (args) => { return document.querySelector('[aria-label="Related resources"]')?.open===true; });
  if (!resourcesOpen) await click(page, '[aria-label="Related resources"] summary');
  const sourceOption = `[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(`${targetType}, Related resource`)}]`;
  await click(page, sourceOption);
  const rawFieldsOpen = await browserEval(page, (args) => { return document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open===true; });
  if (!rawFieldsOpen) await click(page, '[data-testid="feature-catalog-raw-fields"] summary');
  const fieldSelector = `input[aria-label=${JSON.stringify(`Select ${targetType}.id`)}]`;
  await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector(args[0])))); }, [fieldSelector + ':not(:disabled)']);
  await click(page, fieldSelector);
  await click(page, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector('[role="dialog"]')))); });

  const routeLabel = `${targetType} ID: Patient <-[subject]- ${targetType}`;
  const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(routeLabel)}]`;
  let routePresent = await browserEval(page, (args) => { return Boolean(document.querySelector(args[0])); }, [routeSelector]);
  if (!routePresent) {
    const alternativesOpen = await browserEval(page, (args) => { return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open===true; });
    if (!alternativesOpen) await click(page, '[role="dialog"] summary', { includes: 'Other relationship paths' });
    await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector(args[0])))); }, [routeSelector]);
    routePresent = true;
  }
  assert(routePresent, `Native chooser omitted one-hop Patient-to-${targetType} path`);
  await click(page, routeSelector);
  const formLabel = `${targetType} ID: Count matching records`;
  const formSelector = `[role="dialog"] input[aria-label=${JSON.stringify(formLabel)}]`;
  await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector(args[0])))); }, [formSelector]);
  const formChoice = await browserEval(page, (args) => { const input=document.querySelector(args[0]);return input?{disabled:input.disabled,checked:input.checked}:null; }, [formSelector]);
  assert(formChoice && !formChoice.disabled, `Native chooser cannot author a ${targetType} related COUNT: ${JSON.stringify(formChoice)}`);
  await click(page, formSelector);
  const chosen = await browserEval(page, (args) => { return {
    route:document.querySelector(args[0])?.checked,
    form:document.querySelector(args[1])?.checked
  }; }, [routeSelector, formSelector]);
  assert.deepEqual(chosen, { route: true, form: true }, `Chooser must retain the selected ${targetType} route and COUNT form`);
  await click(page, '[role="dialog"] button', { name: 'Add 1 column' });
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready')); });
  const adoptedProposal = await browserEval(page, (args) => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {proposalId:panel?.dataset.proposalId,status:panel?.dataset.proposalStatus}; });
  assert(adoptedProposal.proposalId, `The native ${targetType} proposal preview has no proposal identity`);
  const proposalRequest = await waitNative(entry => pathOf(entry).endsWith('/construction-proposals') &&
    entry.body?.candidateConstruction?.steps?.some(step => step.operation?.kind === 'RELATED_SOURCE' &&
      step.operation.relatedSource?.source?.resourceType === targetType && step.operation.relatedSource?.form === 'COUNT') &&
    entry.response?.proposalId === adoptedProposal.proposalId && entry.response?.previewStatus === 'READY', fromIndex);
  assert.equal(proposalRequest.status, 200, JSON.stringify(proposalRequest.response));
  const proposal = proposalRequest.response;
  assert.equal(proposal.outputId, outputId);
  assert.equal(proposal.preview?.rows?.length, 1, 'Native candidate preview must retain the exact selected Patient row');
  const candidateStep = proposal.candidateConstruction?.steps?.findLast(step => step.operation?.kind === 'RELATED_SOURCE' &&
    step.operation.relatedSource?.source?.resourceType === targetType);
  assert(candidateStep, `The ${targetType} proposal omitted its native RELATED_SOURCE step`);
  const related = candidateStep.operation.relatedSource;
  const candidateColumn = related.outputColumnId;
  const candidateOutput = candidateStep.outputs?.find(output => output.id === candidateColumn);
  assert(candidateOutput, `${targetType} proposal omitted its related output descriptor`);
  assert.equal(proposal.preview.rows[0]?.[rootColumnId], report.oracle.patient.id, `${targetType} candidate preview changed the root identity`);
  assert.equal(proposal.preview.rows[0]?.[candidateOutput.name], report.oracle.counts[targetType],
    `Native ${targetType} candidate preview differs from independent raw oracle`);
  assert.equal(candidateStep?.operation?.kind, 'RELATED_SOURCE', 'Native related chooser must author a RELATED_SOURCE construction step');
  assert.equal(related.form, 'COUNT');
  assert.equal(related.source?.kind, 'FIELD');
  assert.equal(related.source?.resourceType, targetType);
  assert.equal(related.source?.path, 'id');
  assert.deepEqual(related.route?.map(edge => ({
    from: edge.fromResourceType, to: edge.toResourceType,
    relationship: edge.relationship, direction: edge.storageDirection,
  })), [{ from: patientType, to: targetType, relationship: 'subject_Patient', direction: 'INBOUND' }]);
  const proposalPanel = await browserEval(page, (args) => { return document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText; });
  assert(proposalPanel, `The native ${targetType} count proposal is not visible in the Builder`);
  record(`add-${targetType}-count-chooser-to-preview`, start, {
    candidateColumnId: candidateColumn,
    candidateColumnName: candidateOutput.name,
    proposalId: proposal.proposalId,
    receiptId: proposal.preview?.receiptId,
    value: proposal.preview.rows[0][candidateOutput.name],
    path: related.route,
  });
  return { proposalRequest, proposal, candidateColumn, candidateOutput, related };
};

const applyRelatedCount = async (targetType, candidate) => {
  const start = Date.now();
  const fromIndex = nativeIndex();
  await click(page, '[data-testid="construction-apply-proposal"]');
  const savedCommand = await waitNative(entry => pathOf(entry).endsWith('/commands') &&
    entry.body?.commands?.some(item => item.type === 'APPLY_CONSTRUCTION_PROPOSAL'), fromIndex);
  const appliedProposal = savedCommand.body.commands.find(item => item.type === 'APPLY_CONSTRUCTION_PROPOSAL');
  assert.equal(appliedProposal.outputId, outputId);
  assert.equal(appliedProposal.proposalId, candidate.proposal.proposalId, 'Apply must consume the exact native preview proposal');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  builder = await api(`${base}/builder`);
  const savedStep = doc().construction.steps.find(step => step.operation?.relatedSource?.outputColumnId === candidate.candidateColumn);
  assert(savedStep, `Saved Builder omitted ${targetType} related COUNT step`);
  assert.equal(savedStep.operation.kind, 'RELATED_SOURCE');
  assert.equal(savedStep.operation.relatedSource.form, 'COUNT');
  assert.equal(savedStep.operation.relatedSource.source.resourceType, targetType);
  const savedOutput = savedStep.outputs.find(output => output.id === candidate.candidateColumn);
  assert(savedOutput, `Saved ${targetType} step/output binding was lost`);
  assert.equal(savedOutput.name, candidate.candidateOutput.name, `Apply changed the ${targetType} output key`);
  report.savedColumns[targetType] = {
    id: candidate.candidateColumn,
    name: savedOutput.name,
    label: savedOutput.label,
    stepId: savedStep.id,
  };
  assert.equal(doc().construction.steps.filter(step => step.operation?.kind === 'RELATED_SOURCE').length,
    Object.keys(report.savedColumns).length, 'All sibling related count operations must coexist in the same saved output');
  assert.equal(doc().columns.length, originalDocument.columns.length,
    'RELATED_SOURCE outputs must remain construction outputs rather than rewriting source-column bindings');
  assert.equal(doc().rootResourceType, patientType);
  assert.equal(doc().population.selectionRevisionId, report.oracle.selectionRevisionId);
  assert.equal(doc().population.route?.length ?? 0, 0, 'Sibling count outputs must preserve direct Patient population');

  const view = await renderTable(targetTypes.filter(type => Object.hasOwn(report.savedColumns, type)), `apply-${targetType}-count`);
  const savedPreview = await waitNative(entry => pathOf(entry).endsWith('/preview') && entry.body?.outputId === outputId, fromIndex);
  const preview = previewBody(savedPreview.response);
  const values = assertPreviewValues(preview, targetTypes.filter(type => Object.hasOwn(report.savedColumns, type)), `apply-${targetType}-count`);
  record(`apply-${targetType}-count-to-render`, start, { commandStatus: savedCommand.status, receiptId: preview.receiptId, values: values.values });
  return { view, preview, savedCommand };
};

const removeRelatedCount = async targetType => {
  const saved = report.savedColumns[targetType];
  assert(saved, `No saved ${targetType} count exists to remove`);
  const currentTypes = targetTypes.filter(type => Object.hasOwn(report.savedColumns, type));
  const remainingTypes = currentTypes.filter(type => type !== targetType);
  const start = Date.now();
  const fromIndex = nativeIndex();
  await click(page, `[data-testid="construction-history-step-${saved.stepId}"]`);
  await waitForBrowser(page, (args) => { return Boolean((Boolean(document.querySelector(args[0])))); }, [`[data-testid="construction-remove-step-${saved.stepId}"]:not(:disabled)`]);
  await click(page, `[data-testid="construction-remove-step-${saved.stepId}"]`);
  const removalProposal = await waitNative(entry => pathOf(entry).endsWith('/construction-proposals') &&
    entry.body?.removeStepIds?.includes(saved.stepId) && entry.response?.previewStatus === 'READY', fromIndex);
  assert(!removalProposal.response.candidateConstruction.steps.some(step => step.id === saved.stepId),
    `The native ${targetType} removal proposal retained the authored source step`);
  assertPreviewValues(removalProposal.response.preview, remainingTypes, `remove-${targetType}-count-proposal`);
  await click(page, '[data-testid="construction-apply-proposal"]');
  const removal = await waitNative(entry => pathOf(entry).endsWith('/commands') &&
    entry.body?.commands?.some(item => item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === removalProposal.response.proposalId), fromIndex);
  delete report.savedColumns[targetType];
  builder = await api(`${base}/builder`);
  assert(!doc().construction.steps.some(step => step.operation?.relatedSource?.outputColumnId === saved.id), `Removing ${targetType} left its authored step in Builder state`);
  assert.equal(doc().columns.length, originalDocument.columns.length);
  assert.equal(doc().population.selectionRevisionId, report.oracle.selectionRevisionId);
  const view = await renderTable(remainingTypes, `remove-${targetType}-count`);
  const previewRequest = await waitNative(entry => pathOf(entry).endsWith('/preview') && entry.body?.outputId === outputId, fromIndex);
  const preview = previewBody(previewRequest.response);
  const values = assertPreviewValues(preview, remainingTypes, `remove-${targetType}-count`);
  record(`remove-${targetType}-count-to-render`, start, { commandStatus: removal.status, receiptId: preview.receiptId, values: values.values });
  return { view, preview };
};

try {
  const [seed] = rawQuery(seedQuery);
  if (!seed) {
    report.status = 'unverified';
    report.productFailure = false;
    report.unverifiedReason = `No Patient with all three nonzero sibling counts was found among the first ${seedLimit} scoped Patient records; this bounded scan does not establish absence elsewhere.`;
    report.oracle = { status: 'bounded-absence', seedQuery, seedLimit, targetTypes };
    throw new BoundedAbsenceError(report.unverifiedReason);
  }
  assert.equal(seed.patient.project, project);
  assert.equal(seed.patient.generation, generation);
  assert.equal(seed.patient.resourceType, patientType);
  const exactQuery = exactOracleQuery(seed.patient.id);
  const [exact] = rawQuery(exactQuery);
  assert(exact, 'Exact raw oracle could not reread the bounded Patient witness');
  assert.deepEqual(exact.patient, seed.patient, 'Bounded finder and exact Patient oracle disagree');
  assert.deepEqual(exact.siblingCounts, seed.siblingCounts, 'Bounded finder and exact sibling-count oracle disagree');
  const counts = Object.fromEntries(exact.siblingCounts.map(entry => [entry.resourceType, entry.count]));
  assert.deepEqual(Object.keys(counts).sort(), [...targetTypes].sort());
  for (const type of targetTypes) assert(Number.isInteger(counts[type]) && counts[type] > 0, `Witness must have at least one ${type}`);
  report.oracle = {
    status: 'selected',
    seedQuery,
    exactQuery,
    seedLimit,
    patient: exact.patient,
    counts,
    childWitnesses: exact.siblingCounts.map(({ resourceType, count, witnessOverflow, witnessIDs }) => ({ resourceType, count, witnessOverflow, witnessIDs })),
    project,
    generation,
    deduplicationIdentity: 'distinct scoped FHIR document _id',
  };

  await api(root, { name: explorer, title: 'Mixed target sibling related COUNT QA' });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation, 'Fresh QA Explorer must use the requested CDA generation');
  const scopeDigest = builder.catalog.authorizationScopeDigest;
  assert(scopeDigest, 'The active local API catalog must expose its explicit authorization scope digest');
  report.localScopeAssertion.catalogScopeDigest = scopeDigest;
  const patientNode = builder.catalog.nodes.find(node => node.resourceType === patientType);
  assert(patientNode, 'CDA catalog has no Patient root');
  for (const type of targetTypes) assert(builder.catalog.nodes.some(node => node.resourceType === type), `CDA catalog has no ${type} node`);

  await command([{ type: 'CREATE_TABLE', title: 'Mixed target sibling counts', rootNodeId: patientNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const patientIDCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id');
  assert(patientIDCandidate, 'Patient.id is unavailable as the stable baseline field');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: patientIDCandidate.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient ID' }]);
  rootColumnId = columnID(doc().columns[0]);
  assert(rootColumnId, 'Baseline Patient identity output has no stable column ID');
  const selection = await api(selections, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `mixed-sibling-count-${explorer}`,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: patientType, id: exact.patient.id }] } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, patientType);
  assert.equal(selection.scopeDigest, scopeDigest);
  assert.equal(selection.memberCount, 1);
  const routes = await api(`${base}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
  });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'Exact selected Patient must expose a direct table population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const selectionPage = await api(`${selections}/${selection.id}?limit=10`);
  assert.equal(selectionPage.revision.project, project);
  assert.equal(selectionPage.revision.generation, generation);
  assert.equal(selectionPage.revision.resourceType, patientType);
  assert.equal(selectionPage.revision.scopeDigest, scopeDigest);
  assert.equal(selectionPage.revision.memberCount, 1);
  assert.deepEqual(selectionPage.members.map(member => member.ref.id), [exact.patient.id]);
  assert.equal(doc().rootResourceType, patientType);
  assert.equal(doc().population.selectionRevisionId, selection.id);
  assert.equal(doc().population.route?.length ?? 0, 0);
  assert.equal(doc().rows.kind, 'RECORDS');
  assert.equal(doc().columns.length, 1);
  originalWorkspace = structuredClone(builder.workspace);
  originalDocument = structuredClone(doc());
  const expectedRestoredDocument = expectedCanonicalDocumentAfterConstructionRemoval(originalDocument);
  const baselineDocumentIndex = originalWorkspace.documents.findIndex(document => document.output?.id === outputId);
  assert.notEqual(baselineDocumentIndex, -1, 'The captured baseline workspace must contain the Patient output document');
  const expectedRestoredWorkspace = structuredClone(originalWorkspace);
  expectedRestoredWorkspace.documents[baselineDocumentIndex] = expectedRestoredDocument;
  const baselineDraftVersion = builder.draftVersion;
  const baselineDraftDigest = builder.draftDigest;
  report.oracle.selectionRevisionId = selection.id;
  report.seed = { outputId, rootColumnId, patientNodeId: patientNode.nodeId, patientCandidateId: patientIDCandidate.candidateId,
    selectionRevisionId: selection.id, scopeDigest, population: originalDocument.population,
    draftVersion: baselineDraftVersion, draftDigest: baselineDraftDigest };
  report.savedColumns = {};
  assert.deepEqual(report.oracle.patient, { id: exact.patient.id, _id: exact.patient._id, resourceType: patientType, project, generation });

  trackBrowser();
  const initial = await openTable([], 'load-base-Patient-table');
  assertPreviewValues(initial.preview, [], 'base Patient table');
  assert.deepEqual(initial.view.rows[0], [exact.patient.id]);
  const originalRenderedRow = initial.view.rows[0];

  const candidates = {};
  const firstTargetType = targetTypes[0];
  const cancelledCandidate = await selectRelatedCount(firstTargetType);
  const beforeCancel = await api(`${base}/builder`);
  assert.deepEqual(beforeCancel.workspace, originalWorkspace,
    'A ready, unapplied related COUNT proposal must not mutate the saved Builder workspace');
  assert.equal(beforeCancel.draftVersion, baselineDraftVersion,
    'A ready, unapplied related COUNT proposal must not advance the saved draft version');
  assert.equal(beforeCancel.draftDigest, baselineDraftDigest,
    'A ready, unapplied related COUNT proposal must not change the saved draft digest');
  const cancelStart = Date.now();
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  builder = await api(`${base}/builder`);
  assert.deepEqual(builder.workspace, originalWorkspace,
    'Cancel must leave the complete source-only Patient workspace unchanged');
  assert.equal(builder.draftVersion, baselineDraftVersion,
    'Cancel must leave the saved draft version unchanged');
  assert.equal(builder.draftDigest, baselineDraftDigest,
    'Cancel must leave the saved draft digest unchanged');
  const afterCancel = await openTable([], 'reload-after-cancel-first-related-count');
  assert.deepEqual(afterCancel.preview.columns, initial.preview.columns,
    'Cancel plus reload must restore the exact baseline published column set');
  assert.deepEqual(afterCancel.preview.rows, initial.preview.rows,
    'Cancel plus reload must preserve the exact baseline Patient row and value');
  assert.equal(afterCancel.preview.rowCount, initial.preview.rowCount);
  record('cancel-ready-first-related-count-proposal', cancelStart, {
    cancelledProposalId: cancelledCandidate.proposal.proposalId,
    unchangedDraftVersion: builder.draftVersion,
    unchangedDraftDigest: builder.draftDigest,
    baselineRow: afterCancel.preview.rows[0],
  });

  const candidate = await selectRelatedCount(firstTargetType);
  const relatedIntent = related => {
    const { outputColumnId, ...intent } = related;
    assert(outputColumnId, 'A related COUNT proposal must provide its candidate output identity');
    return intent;
  };
  assert.deepEqual(relatedIntent(candidate.related), relatedIntent(cancelledCandidate.related),
    'Reselecting COUNT after Cancel must preserve the exact resource, route, form, and contributor intent');
  assert.equal(candidate.candidateOutput.name, cancelledCandidate.candidateOutput.name,
    'Reselecting COUNT after Cancel must preserve its output field name');
  candidates[firstTargetType] = candidate;
  const firstApplied = await applyRelatedCount(firstTargetType, candidate);
  report.savedColumns[firstTargetType].expectedCount = counts[firstTargetType];
  assert.deepEqual(firstApplied.view.rows[0][0], exact.patient.id);

  for (const targetType of targetTypes.slice(1)) {
    const nextCandidate = await selectRelatedCount(targetType);
    candidates[targetType] = nextCandidate;
    const applied = await applyRelatedCount(targetType, nextCandidate);
    report.savedColumns[targetType].expectedCount = counts[targetType];
    assert.deepEqual(applied.view.rows[0][0], exact.patient.id);
  }
  report.authoredSiblingOperations = doc().construction.steps.map(step => ({
    stepId: step.id,
    kind: step.operation.kind,
    targetType: step.operation.relatedSource?.source?.resourceType,
    form: step.operation.relatedSource?.form,
    route: step.operation.relatedSource?.route,
  }));
  assert.deepEqual(report.authoredSiblingOperations.map(step => step.targetType), targetTypes,
    'The same saved Patient-root output must retain all three typed sibling related sources');
  assert(report.authoredSiblingOperations.every(step => step.kind === 'RELATED_SOURCE' && step.form === 'COUNT'));

  const reloaded = await openTable(targetTypes, 'reload-all-three-mixed-sibling-counts');
  assert.deepEqual(reloaded.view.rows[0][0], exact.patient.id);
  for (const targetType of targetTypes) {
    const saved = doc().construction.steps.find(step => step.operation?.relatedSource?.source?.resourceType === targetType);
    assert(saved, `Reload omitted saved ${targetType} related source`);
    assert.equal(saved.operation.relatedSource.form, 'COUNT');
    assert.equal(saved.operation.relatedSource.outputColumnId, report.savedColumns[targetType].id);
  }

  for (const targetType of [...targetTypes].reverse()) await removeRelatedCount(targetType);
  builder = await api(`${base}/builder`);
  assert.deepEqual(builder.workspace, expectedRestoredWorkspace,
    'Removing all three sibling outputs must restore the exact baseline workspace, allowing only deterministic source-ID and empty-construction migration');
  assert.deepEqual(doc(), expectedRestoredDocument,
    'Final removal must restore the original Patient table semantics and exact authored source bindings');
  const restored = await openTable([], 'reload-after-removing-all-sibling-counts');
  assert.deepEqual(restored.view.rows[0], originalRenderedRow, 'Final reload must restore the original Patient row');
  assert.deepEqual(doc().columns.map(columnID), [rootColumnId]);
  assert.deepEqual(doc().construction?.steps ?? [], []);
  assert.equal(doc().population.selectionRevisionId, selection.id);
  assert.equal(doc().population.route?.length ?? 0, 0);
  assert.deepEqual(report.errors, [], `Unexpected native browser errors: ${JSON.stringify(report.errors)}`);
  assert.equal(report.protectedExplorerUntouched, true, 'Protected full-QA Explorer must remain untouched');
  assert.equal(doc().rows.kind, 'RECORDS', 'This unit must keep Group/Pivot out of the mixed-sibling population');
  assert(report.authoredSiblingOperations.every(step => step.kind === 'RELATED_SOURCE'),
    'This unit must keep Group/Pivot operations out of the mixed-sibling construction chain');
  report.persistedWorkspaceDigest = builder.draftDigest;
  report.status = 'passed';
} catch (error) {
  fatal = error;
  const invalidated = Boolean(error.invalidatesRun);
  report.status = invalidated ? 'invalidated' : error instanceof BoundedAbsenceError ? 'unverified' : 'failed';
  report.error = String(error.stack ?? error);
  if (invalidated) {
    report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true };
  }
  report.failureUI = await browserEval(page, () => { return {
    tail:document.body.innerText.slice(-8000),
    headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),
    proposal:(()=>{const p=document.querySelector('[data-testid="construction-proposal-panel"]');return p?{proposalId:p.dataset.proposalId,status:p.dataset.proposalStatus,text:p.innerText}:null})(),
    dialog:(()=>{const d=document.querySelector('[role="dialog"]');return d?{text:d.innerText,radios:[...d.querySelectorAll('input[type="radio"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled}))}:null})()
  }; }).catch(String);
} finally {
  await nativeCapture?.flush();
  report.finished = new Date().toISOString();
  report.nativeRequestSummary = report.nativeRequests.map(({ path, method, status, body, response, failure }) => ({ path, method, status, body, response, failure }));
  await cda.attachReport('mixedSiblingCountWorkflow-domain-report.json', report);
}

if (fatal || report.status !== 'passed') throw fatal ?? new Error(report.unverifiedReason ?? report.error ?? 'CDA sibling COUNT workflow did not pass');
return report;
}
