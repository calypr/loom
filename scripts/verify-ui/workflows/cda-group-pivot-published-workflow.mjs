import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { builderDraftStateEvidence, constructionCandidateWireEquivalent } from '../helpers/builder-combine-draft-helpers.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { proposalPreviewReadinessExpression } from '../helpers/proposal-preview-readiness.mjs';
import {
  chooseCdaGroupPivotJoinWitness,
  prepareCdaGroupPivotJoinOracle,
  selectCdaGroupPivotJoinSubjects,
} from '../helpers/cda-group-pivot-join-oracle.mjs';
import {
  prepareCdaGroupPivotPublishedViewerOracle,
  verifyCdaGroupPivotPublicationReread,
} from '../helpers/cda-group-pivot-publication-oracle.mjs';
import { decodeCohortGroupPivotIntegerValue } from './cohort-group-pivot-publication.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const SCAN_LIMIT = 2_000;
const encode = value => encodeURIComponent(value);
const basePath = (project, explorer) => `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const VALID_EXPLORER_ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;

export function resolveCdaBootstrapExplorer(summaries, { project, preferredExplorer = null } = {}) {
  assert(Array.isArray(summaries), 'The exact CDA project Explorer list must be an array.');
  assert.equal(typeof project, 'string');
  assert(project.trim(), 'The owned CDA project is required to select a bootstrap Explorer.');
  assert(summaries.every(summary => summary && summary.project === project),
    'Every bootstrap candidate must belong to the exact owned CDA project.');
  const ids = summaries.map(summary => summary.explorerId);
  assert(ids.every(id => typeof id === 'string' && VALID_EXPLORER_ID.test(id)),
    'The exact CDA project Explorer list must contain valid Explorer identities.');
  assert.equal(new Set(ids).size, ids.length, 'The exact CDA project Explorer list must not duplicate identities.');
  assert(ids.length > 0, 'The exact owned CDA project must have an existing Explorer for native creation navigation.');
  if (preferredExplorer !== null) {
    assert(ids.includes(preferredExplorer), 'Configured CDA Explorer must be present in the exact owned project list.');
    return preferredExplorer;
  }
  return [...ids].sort()[0];
}

export function cdaPublishedFirstTableReady() {
  const headings = [...document.querySelectorAll('h2')]
    .filter(heading => heading.textContent?.trim() === 'Build your first table');
  const tableName = document.querySelector('#first-table-name');
  const observationChoices = [...document.querySelectorAll('button[aria-label="Choose Observation rows"]')];
  return headings.length === 1 &&
    tableName instanceof HTMLInputElement &&
    tableName.getClientRects().length > 0 &&
    observationChoices.length === 1 &&
    observationChoices[0].getClientRects().length > 0 &&
    !observationChoices[0].disabled;
}

export function readCdaSelectOptions(select) {
  return [...select.options].map(option => ({
    value: option.value,
    text: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(),
    disabled: option.disabled,
  }));
}

export function readCdaHeaderLabels(headers) {
  return [...headers].map(header => String(header.textContent ?? '').replace(/\s+/g, ' ').trim());
}

const awaitBeforeDeadline = async (work, deadlineAt, label) => {
  const remaining = deadlineAt - Date.now();
  assert(remaining > 0, `${label} started after the five-second deadline.`);
  let timer;
  try {
    return await Promise.race([
      Promise.resolve().then(work),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${label} exceeded the five-second budget.`)), remaining);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
};

function runArango(arangoContainer, query) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `Bounded owned Arango query failed: ${tidy(result.stderr || result.stdout).slice(0, 1_600)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango query returned no JSON array: ${tidy(result.stdout).slice(-800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
}

const readGrid = async (page, kind = 'saved') => page.evaluate(({ kind }) => {
  const proposal = kind === 'proposal'
    ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]')
    : null;
  const table = proposal?.querySelector('table') ?? document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return { ready: false, headers: [], rows: [] };
  const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const headers = proposal
    ? [...table.querySelectorAll('thead th')].map(cell => clean(cell.querySelector('span')?.textContent ?? cell.textContent))
    : [...table.querySelectorAll('[role="columnheader"]')].map(cell => clean(cell.textContent));
  const rows = proposal
    ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => clean(cell.innerText)))
    : [...table.querySelectorAll('[role="row"]')].slice(1)
      .filter(row => row.querySelector('[role="cell"]'))
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => clean(cell.innerText)));
  return { ready: true, headers, rows };
}, { kind });

const normalizeExpectedRows = (columns, rows) => rows.map(row => columns.map(column => String(row[column])));

function assertGrid(grid, expectedColumns, expectedRows, phase) {
  assert(grid.ready, `${phase} did not render a ready Builder table.`);
  assert.deepEqual(grid.headers, expectedColumns, `${phase} columns differ from the independent raw oracle.`);
  assert.deepEqual(sortRows(grid.rows), sortRows(expectedRows), `${phase} values or row multiplicity differ from the independent raw oracle.`);
  return grid;
}

const makeGridWait = (outputId, rowCount) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');` +
  `const t=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');` +
  `return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${JSON.stringify(outputId)}&&` +
  `t?.getAttribute('aria-rowcount')===${JSON.stringify(String(rowCount + 1))}&&` +
  `!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;

const getDocument = (state, outputId) => {
  const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Expected exactly one Builder document for output ${outputId}.`);
  return matches[0];
};

function runArangoQuery(project, generation, predicate, { ids } = {}) {
  const codeCodingCodes = '(IS_ARRAY(r.payload.code.coding) ? r.payload.code.coding[*].code : [])';
  const exactIDs = ids ? `AND r._id IN ${JSON.stringify(ids)}` : '';
  return `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0
      AND IS_STRING(r.payload.status) AND LENGTH(TRIM(r.payload.status)) > 0
      AND IS_ARRAY(r.payload.code.coding) AND LENGTH(r.payload.code.coding) > 0
      AND IS_STRING(r.payload.code.coding[0].code) AND LENGTH(TRIM(r.payload.code.coding[0].code)) > 0
      ${predicate ?? ''} ${exactIDs}
    SORT r._id LIMIT ${ids ? 3 : SCAN_LIMIT}
    RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,status:r.payload.status,
      codeCodingCodes:${codeCodingCodes}}`;
}

export async function cdaGroupPivotPublishedWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(cda.target);
  assert.equal(project, cda.target.fixtureProject, 'Raw oracle must use the owned CDA fixture project.');
  assert.equal(generation, 'cda-fhir-v1', 'Published CDA Group→Pivot requires the pinned FHIR generation.');
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  report.phase = 'CDA native Group→Pivot publication and Viewer reload';
  report.scope = { project, generation, initialSampleRowLimit: SCAN_LIMIT, targetedSubjectRowLimit: SCAN_LIMIT,
    maxSelectedSubjects: 10, maxReturnedRows: SCAN_LIMIT * 2, selectedPublicationRows: 3 };

  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-group-pivot-published-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${tidy(text).slice(0, 1_200)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_600)}`);
    return { value, status: response.status() };
  };

  const eligibleCodeFilter = `IS_STRING(r.payload.status) AND LENGTH(TRIM(r.payload.status)) > 0
      AND IS_ARRAY(r.payload.code.coding) AND LENGTH(r.payload.code.coding) > 0
      AND IS_STRING(r.payload.code.coding[0].code) AND LENGTH(TRIM(r.payload.code.coding[0].code)) > 0`;
  const codeCodingCodes = '(IS_ARRAY(r.payload.code.coding) ? r.payload.code.coding[*].code : [])';
  const observationProjection = `RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,status:r.payload.status,
      codeCodingCodes:${codeCodingCodes}}`;
  const initialQuery = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0
      AND ${eligibleCodeFilter}
    SORT r._id LIMIT ${SCAN_LIMIT}
    ${observationProjection}`;
  const initialRows = runArango(arangoContainer, initialQuery);
  const selectedSubjects = selectCdaGroupPivotJoinSubjects(initialRows, { project, generation, limit: 10 });
  const targetedQuery = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND r.payload.subject.reference IN ${JSON.stringify(selectedSubjects)}
      AND ${eligibleCodeFilter}
    SORT r._id LIMIT ${SCAN_LIMIT}
    ${observationProjection}`;
  const targetedRows = runArango(arangoContainer, targetedQuery);
  const witness = chooseCdaGroupPivotJoinWitness(targetedRows, { project, generation, limit: SCAN_LIMIT });
  if (!witness) {
    const error = new Error(`Bounded ${SCAN_LIMIT}-row CDA scans did not produce the retained shared-subject A,A,B witness; no lifecycle result is claimed.`);
    error.name = 'RawOracleUnavailableError';
    error.rawOracleFailure = true;
    report.status = 'unverified';
    report.unverifiedReason = error.message;
    report.finished = new Date().toISOString();
    await cda.attachReport('group-pivot-published', report);
    throw error;
  }
  assert.equal(witness.right.length, 3, 'Only the bounded right three Observation IDs may enter publication.');
  const oracle = prepareCdaGroupPivotPublishedViewerOracle(witness, { project, generation });
  const groupOracle = prepareCdaGroupPivotJoinOracle(witness, { project, generation });
  assert.deepEqual(oracle.columns, ['Subject Reference', '1222593009', '81247-9']);
  assert.deepEqual(oracle.rows, [{ 'Subject Reference': witness.sharedSubject, '1222593009': 2, '81247-9': 1 }]);
  const rightIDs = witness.right.map(row => row._id);
  const exactReadQuery = runArangoQuery(project, generation, '', { ids: rightIDs });
  const rereadRows = runArango(arangoContainer, exactReadQuery);
  const rereadIDs = verifyCdaGroupPivotPublicationReread(witness, rereadRows, { project, generation });
  assert.deepEqual(rereadRows.map(row => row.id).sort(), oracle.sourceObservationIDs);
  cda.check('correctness', 'Bounded raw CDA oracle and exact three-Observation reread pin the published Pivot rows', true, {
    project, generation, initialSampleRows: initialRows.length, targetedRows: targetedRows.length,
    selectedSubjects, selectedDocumentIDs: rereadIDs, selectedFHIRIDs: oracle.sourceObservationIDs,
    sharedSubject: witness.sharedSubject, categories: oracle.categories,
    expectedColumns: oracle.columns, expectedRows: oracle.rows, initialQuery, targetedQuery, exactReadQuery,
  });
  report.oracle = { scope: report.scope, initialQuery, targetedQuery, exactReadQuery, witness: {
    rightObservationIDs: oracle.sourceObservationIDs, rightDocumentIDs: rereadIDs,
    subject: witness.sharedSubject, categories: oracle.categories,
  }, groupRows: groupOracle.rightGroups, pivotRows: oracle.groupPivotRows, columns: oracle.columns, rows: oracle.rows };

  const action = (label, locator, perform, after, editable = false) => cda.action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    ...(editable ? { editable: true } : {}),
    ...(after ? { after, requiredCheck: 'native Builder lifecycle actions complete within five seconds' } : {}),
  });
  const waitSelector = selector => page.locator(selector).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const reportCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const explorerSummaries = (await api(`/api/v1/projects/${encode(project)}/explorers`)).value;
  const bootstrapExplorer = resolveCdaBootstrapExplorer(explorerSummaries, {
    project,
    preferredExplorer: cda.target.explorer,
  });
  cda.target.explorer = bootstrapExplorer;
  cda.report.target.explorer = bootstrapExplorer;
  cda.report.bootstrapExplorerId = bootstrapExplorer;
  await cda.navigate(`${uiOrigin}/?project=${encode(project)}&explorer=${encode(bootstrapExplorer)}&mode=builder`);
  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await action('Open native fresh CDA Explorer creation', newExplorer,
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }));
  const explorerTitle = `CDA Group Pivot publication ${randomUUID().slice(0, 8)}`;
  const explorerName = page.locator('#new-explorer-name');
  await action('Name fresh CDA publication Explorer', explorerName,
    locator => locator.fill(explorerTitle, { timeout: ACTION_TIMEOUT_MS }), undefined, true);
  await action('Create blank CDA publication Explorer through UI', page.getByRole('button', { name: 'Create blank', exact: true }),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => page.waitForFunction(title =>
      document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent.trim() === title,
    explorerTitle, { timeout: ACTION_TIMEOUT_MS }));
  const explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorer && explorer !== bootstrapExplorer, 'Native Explorer creation must select a fresh Explorer.');
  cda.target.explorer = explorer;
  cda.report.explorer = explorer;
  cda.report.target.explorer = explorer;
  await page.getByText('Build your first table', { exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const base = basePath(project, explorer);
  const authoring = `${base}/authoring/v2`;
  const builderPath = `${authoring}/builder`;
  const capture = cda.captureRequests(authoring, { responsePaths: /commands|construction-proposals|publish/ });
  let builder = (await api(builderPath)).value;
  const empty = builderDraftStateEvidence(builder, 'empty');
  assert(empty.ok, `Fresh CDA Explorer must begin with an empty draft: ${JSON.stringify(empty)}`);
  assert.equal(builder.catalog?.generation, generation);
  const initialScope = {
    snapshotToken: builder.catalog.snapshotToken,
    authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
    generation: builder.catalog.generation,
  };
  assert(initialScope.snapshotToken && initialScope.authorizationScopeDigest);
  const checkScope = state => {
    assert.equal(state.catalog.generation, initialScope.generation);
    assert.equal(state.catalog.snapshotToken, initialScope.snapshotToken);
    assert.equal(state.catalog.authorizationScopeDigest, initialScope.authorizationScopeDigest);
  };
  const readBuilder = async () => (await api(builderPath)).value;

  const selectionCreated = await api(`${base}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `cda-group-pivot-published-selection-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: oracle.sourceObservationIDs.map(id => ({
      project, generation, resourceType: 'Observation', id,
    })) } },
  });
  const selection = selectionCreated.value;
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Observation');
  assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
  assert.equal(selection.memberCount, 3);
  const selectionPage = (await api(`${base}/selections/${encode(selection.id)}?limit=100`)).value;
  assert.equal(selectionPage.revision?.id, selection.id);
  assert.equal(selectionPage.revision?.project, project);
  assert.equal(selectionPage.revision?.generation, generation);
  assert.equal(selectionPage.revision?.resourceType, 'Observation');
  assert.equal(selectionPage.revision?.scopeDigest, initialScope.authorizationScopeDigest);
  assert.equal(selectionPage.revision?.memberCount, 3);
  const selectionRefs = (selectionPage.members ?? []).map(member =>
    `${member.ref.project}/${member.ref.generation}/${member.ref.resourceType}/${member.ref.id}`).sort();
  assert.deepEqual(selectionRefs, oracle.sourceObservationIDs.map(id => `${project}/${generation}/Observation/${id}`).sort());
  const selectionURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder&selection=${encode(selection.id)}`;
  const firstTableNavigationStarted = performance.now();
  const firstTableNavigationDeadline = Date.now() + ACTION_TIMEOUT_MS;
  await awaitBeforeDeadline(
    () => cda.navigate(selectionURL),
    firstTableNavigationDeadline,
    'Navigate to the native CDA first-table setup',
  );
  await page.waitForFunction(cdaPublishedFirstTableReady, undefined, {
    timeout: Math.max(1, firstTableNavigationDeadline - Date.now()),
  });
  assert(performance.now() - firstTableNavigationStarted <= ACTION_TIMEOUT_MS,
    'Native CDA first-table setup must become actionable within five seconds.');
  const tableTitle = 'CDA selected Group Pivot source';
  await action('Name native CDA Observation source table', page.locator('#first-table-name'),
    locator => locator.fill(tableTitle, { timeout: ACTION_TIMEOUT_MS }), undefined, true);
  await action('Create native CDA Observation root table', page.getByRole('button', { name: 'Choose Observation rows', exact: true }),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => page.waitForFunction(title =>
      document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(title), tableTitle,
    { timeout: ACTION_TIMEOUT_MS }));
  await action('Open native CDA starting collection settings', page.getByTestId('construction-rows-settings-trigger'),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => waitSelector('[role="dialog"][aria-label="Row definition settings"]'));
  const useSelected = page.locator('[aria-label="Starting collection"] button').filter({ hasText: 'Use selected resources' });
  await useSelected.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await action('Bind exact CDA Selection as starting collection', useSelected,
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => page.waitForFunction(() =>
      document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('3 Observation resources attached'),
    undefined, { timeout: ACTION_TIMEOUT_MS }));
  await action('Return to native CDA Observation table', page.getByRole('button', { name: 'Back to table', exact: true }),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => waitSelector('[data-testid="construction-action-add-columns"]'));
  builder = await readBuilder();
  checkScope(builder);
  const newDocuments = builder.workspace?.documents ?? [];
  const sourceDocumentSeed = newDocuments.find(document => document.rootResourceType === 'Observation');
  assert.equal(newDocuments.length, 1, 'Fresh native CDA Explorer must contain exactly the newly created Observation table.');
  const outputId = sourceDocumentSeed?.output?.id;
  assert(outputId, 'Native CDA Observation root table must expose a stable output identity.');
  let sourceDocument = getDocument(builder, outputId);
  assert.equal(sourceDocument.population?.selectionRevisionId, selection.id);
  assert.equal(sourceDocument.population?.route?.length ?? 0, 0);
  const initialIdColumns = sourceDocument.columns.filter(column => column.source?.field?.path === 'id' &&
    column.source.field.projectionMode === 'VALUE');
  assert.equal(initialIdColumns.length, 1,
    'Native Observation root creation must add exactly one direct id VALUE binding.');
  const initialIdColumn = initialIdColumns[0];
  assert(initialIdColumn.columnId && initialIdColumn.column,
    'Native Observation root ID must retain its stable source and physical column identities.');
  reportCheck('persistence', 'Native CDA starting collection binds the exact three-member immutable Selection', true, {
    outputId, selectionRevisionId: selection.id, selectionRefs,
  });
  const outputTab = page.getByTestId(`construction-table-${outputId}`);
  await outputTab.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await page.waitForFunction(makeGridWait(outputId, 3), undefined, { timeout: ACTION_TIMEOUT_MS });
  const savedReady = (rows, timeout = ACTION_TIMEOUT_MS) => page.waitForFunction(makeGridWait(outputId, rows), undefined, { timeout });
  const waitProposal = rowCount => page.waitForFunction(proposalPreviewReadinessExpression(outputId, rowCount), undefined,
    { timeout: ACTION_TIMEOUT_MS });
  const proposalEvent = async (fromIndex, matchCandidate, baseState) => {
    const event = await capture.waitForResponse(entry => entry.path === `${authoring}/construction-proposals` &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.request?.outputId === outputId &&
      matchCandidate(entry.request?.candidateConstruction), { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    const requestBody = capture.requestBody(event);
    const responseBody = capture.responseBody(event);
    assert(requestBody && responseBody, 'Native Builder proposal request and response bodies must be retained.');
    assert.equal(requestBody.outputId, outputId);
    assert.equal(responseBody.outputId, outputId);
    assert.equal(responseBody.previewStatus, 'READY');
    assert.equal(requestBody.snapshotToken, baseState.catalog.snapshotToken);
    assert.equal(requestBody.expectedDraftVersion, baseState.draftVersion);
    assert.equal(requestBody.expectedDraftDigest, baseState.draftDigest);
    assert.equal(responseBody.snapshotToken, requestBody.snapshotToken);
    assert.equal(responseBody.draftVersion, requestBody.expectedDraftVersion);
    assert.equal(responseBody.draftDigest, requestBody.expectedDraftDigest);
    assert(responseBody.proposalId && responseBody.preview?.receiptId);
    assert.equal(responseBody.preview.receiptId, responseBody.proposalId);
    const dom = await page.evaluate(() => ({
      proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id') ?? null,
      receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
      outputId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-output-id') ?? null,
    }));
    assert.equal(dom.proposalId, responseBody.proposalId);
    assert.equal(dom.receiptId, responseBody.preview.receiptId);
    assert.equal(dom.outputId, outputId);
    assert(constructionCandidateWireEquivalent(requestBody.candidateConstruction, responseBody.candidateConstruction));
    return { event, requestBody, responseBody };
  };

  const addFieldCheck = async (selector, label) => {
    const control = page.locator(selector);
    assert.equal(await control.count(), 1, `Expected one exact ${label} Builder control.`);
    await action(`Select ${label}`, control, locator => locator.check({ timeout: ACTION_TIMEOUT_MS }));
  };
  await cda.action('Open native raw Observation fields', page.getByTestId('construction-action-add-columns'),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), { after: () => waitSelector('[aria-label="Add columns editor"]') });
  await cda.click('button', { name: 'Fields and related data' });
  await cda.click('summary', { name: 'Raw FHIR fields (advanced)' });
  await addFieldCheck('input[type="checkbox"][aria-label="Select Observation.subject.reference"]', 'Observation.subject.reference');
  await addFieldCheck('input[type="checkbox"][aria-label="Select Observation.code.coding[].code"]', 'Observation.code.coding[].code');
  const addSelected = page.getByRole('button', { name: 'Add 2 selected features', exact: true });
  const chooser = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
  await action('Add the exact Observation subject and first-code fields', addSelected,
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => chooser.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  const subjectValue = chooser.getByRole('radio', { name: 'Subject Reference: Use the matching value', exact: true });
  await action('Use direct Observation.subject.reference values', subjectValue,
    locator => locator.check({ timeout: ACTION_TIMEOUT_MS }), async () => assert(await subjectValue.isChecked()));
  const firstCode = chooser.getByRole('radio', { name: 'Code Coding Code: Use the first value', exact: true });
  await action('Use the first coding value for Pivot categories', firstCode,
    locator => locator.check({ timeout: ACTION_TIMEOUT_MS }), async () => assert(await firstCode.isChecked()));
  await action('Confirm exact Observation subject and first-code columns', chooser.getByRole('button', { name: 'Add 2 columns', exact: true }),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => page.waitForFunction(
      () => [...document.querySelectorAll('button')].some(button => button.innerText.trim() === 'Apply columns' && !button.disabled),
      undefined, { timeout: ACTION_TIMEOUT_MS }));
  await action('Apply raw Observation subject and first-code columns', page.getByRole('button', { name: 'Apply columns', exact: true }),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => savedReady(3));
  const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
  if (await closeEditor.count() && await closeEditor.isVisible()) {
    await action('Close raw-field editor', closeEditor, locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => savedReady(3));
  }
  builder = await readBuilder();
  checkScope(builder);
  sourceDocument = getDocument(builder, outputId);
  const idColumns = sourceDocument.columns.filter(column => column.source?.field?.path === 'id' &&
    column.source.field.projectionMode === 'VALUE');
  const subjectColumns = sourceDocument.columns.filter(column => column.source?.field?.path === 'subject.reference' &&
    column.source.field.projectionMode === 'VALUE');
  const codeColumns = sourceDocument.columns.filter(column => column.source?.field?.path === 'code.coding[].code' &&
    column.source.field.projectionMode === 'FIRST');
  assert.equal(idColumns.length, 1, 'Native Add columns must retain exactly one direct Observation.id VALUE binding.');
  assert.equal(idColumns[0].columnId, initialIdColumn.columnId,
    'Adding raw fields must preserve the exact Observation root ID source binding.');
  assert.equal(idColumns[0].column, initialIdColumn.column,
    'Adding raw fields must preserve the exact Observation root ID physical column.');
  assert.equal(subjectColumns.length, 1);
  assert.equal(codeColumns.length, 1);
  const idColumn = idColumns[0];
  const subjectColumn = subjectColumns[0];
  const codeColumn = codeColumns[0];
  const sourceGrid = await readGrid(page);
  const idIndex = sourceGrid.headers.indexOf(idColumn.label);
  const subjectIndex = sourceGrid.headers.indexOf(subjectColumn.label);
  const codeIndex = sourceGrid.headers.indexOf(codeColumn.label);
  assert(idIndex >= 0 && subjectIndex >= 0 && codeIndex >= 0);
  const displayedSource = sourceGrid.rows.map(row => ({ id: row[idIndex], subject: row[subjectIndex], code: row[codeIndex] }))
    .sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(displayedSource, witness.right.map(row => ({ id: row.id, subject: row.subjectReference,
    code: row.codeCodingCodes[0] })).sort((left, right) => left.id.localeCompare(right.id)),
  'Native Builder source rows must match all and only the independently reread three Observations.');
  cda.check('correctness', 'Native Builder source rows retain exact selected FHIR IDs, subject, and first coding values', true, {
    outputId, selectionRevisionId: selection.id, displayedSource,
  });

  const groupKeys = [subjectColumn, codeColumn];
  const groupBase = structuredClone(builder);
  await cda.click('[data-testid="construction-rows-settings-trigger"]');
  await page.waitForFunction("document.querySelector('[data-testid=construction-action-group-rows]')?.disabled===false",
    undefined, { timeout: ACTION_TIMEOUT_MS });
  await action('Open native two-key Observation Group', page.getByTestId('construction-action-group-rows'),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => waitSelector('select[aria-label="Summary 1"]'));
  assert.equal(await page.locator('select[aria-label="Summary 1"]').inputValue(), 'COUNT_ROWS');
  let groupFromIndex;
  for (const [index, column] of groupKeys.entries()) {
    if (index === groupKeys.length - 1) groupFromIndex = capture.requests.length;
    await action(`Group by exact ${column.label}`, page.locator(`input[type="checkbox"][aria-label=${JSON.stringify(`Group by ${column.label}`)}]`),
      locator => locator.check({ timeout: ACTION_TIMEOUT_MS }), index === groupKeys.length - 1 ? () => waitProposal(2) : undefined);
  }
  const groupProposal = await proposalEvent(groupFromIndex, candidate => {
    const step = candidate?.steps?.at(-1);
    return step?.operation?.kind === 'GROUP' &&
      isDeepStrictEqual(step.operation.group.keys?.map(key => key.inputColumnId), groupKeys.map(key => key.columnId)) &&
      step.operation.group.aggregates?.length === 1 && step.operation.group.aggregates[0].operation === 'COUNT_ROWS';
  }, groupBase);
  const groupGrid = await readGrid(page, 'proposal');
  const countLabels = groupGrid.headers.filter(header => /^row count$/i.test(header));
  assert.equal(countLabels.length, 1, `COUNT_ROWS preview must expose one exact row-count column: ${JSON.stringify(groupGrid.headers)}`);
  const expectedGroupRows = groupOracle.rightGroups.map(row => row.map(String));
  const groupPreview = assertGrid(groupGrid, [subjectColumn.label, codeColumn.label, countLabels[0]],
    expectedGroupRows, 'Native CDA Group proposal');
  const groupCandidate = groupProposal.requestBody.candidateConstruction.steps.at(-1);
  assert.deepEqual(groupCandidate.operation.group.keys.map(key => key.inputColumnId), groupKeys.map(key => key.columnId));
  assert.equal(groupCandidate.operation.group.aggregates[0].operation, 'COUNT_ROWS');
  reportCheck('correctness', 'Native Group preserves exact first-code multiplicity as two COUNT_ROWS groups', true, {
    outputId, proposalId: groupProposal.responseBody.proposalId, rows: groupPreview.rows, expectedRows: expectedGroupRows,
  });
  await action('Apply native Observation Group', page.getByTestId('construction-apply-proposal'),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => savedReady(2));
  builder = await readBuilder();
  let document = getDocument(builder, outputId);
  let steps = document.construction?.steps ?? [];
  assert.deepEqual(steps.map(step => step.operation.kind), ['GROUP']);
  const groupStep = steps[0];
  assert.deepEqual(groupStep.operation.group.keys.map(key => key.inputColumnId), groupKeys.map(key => key.columnId));
  assert.equal(groupStep.operation.group.aggregates[0].operation, 'COUNT_ROWS');

  if (await closeEditor.count() && await closeEditor.isVisible()) {
    await action('Close Group editor before Pivot', closeEditor, locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => savedReady(2));
  }
  const groupOutputColumns = groupStep.outputs;
  const savedGroupKeys = groupStep.operation.group.keys;
  assert.deepEqual(savedGroupKeys.map(key => key.inputColumnId), groupKeys.map(key => key.columnId));
  const subjectGroupOutput = groupOutputColumns.find(output => output.id === savedGroupKeys[0].outputColumnId);
  const codeGroupOutput = groupOutputColumns.find(output => output.id === savedGroupKeys[1].outputColumnId);
  const countGroupOutput = groupOutputColumns.find(output => groupStep.operation.group.aggregates.some(aggregate => aggregate.outputColumnId === output.id));
  assert(subjectGroupOutput && codeGroupOutput && countGroupOutput, 'Saved Group must expose exact subject, first-code, and COUNT_ROWS output identities.');
  const pivotBase = structuredClone(builder);
  const pivotFromIndex = capture.requests.length;
  await cda.click('[data-testid="construction-rows-settings-trigger"]');
  const explicitPivot = page.getByTestId('construction-action-table-pivot-rows');
  const useExplicitPivot = (await explicitPivot.count()) === 1 && await explicitPivot.isVisible();
  const pivotEntry = useExplicitPivot ? explicitPivot : page.getByTestId('construction-action-pivot-rows');
  await page.waitForFunction(`document.querySelector('[data-testid="${useExplicitPivot ? 'construction-action-table-pivot-rows' : 'construction-action-pivot-rows'}"]')?.disabled===false`,
    undefined, { timeout: ACTION_TIMEOUT_MS });
  await action('Open native Pivot editor for the saved Group output', pivotEntry,
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => waitSelector('select[aria-label="Pivot category field"]'));
  const groupBySubject = page.locator(`input[type="checkbox"][aria-label=${JSON.stringify(`Pivot group ${subjectGroupOutput.label}`)}]`);
  await action('Set Pivot row key to exact Subject Reference', groupBySubject,
    locator => locator.check({ timeout: ACTION_TIMEOUT_MS }));
  const selectOutputById = async (selector, expectedId) => {
    const options = await page.locator(selector).evaluate(readCdaSelectOptions);
    const matches = options.filter(option => option.value === expectedId && !option.disabled);
    assert.equal(matches.length, 1, `Expected one exact output ${expectedId} in ${selector}: ${JSON.stringify(options)}`);
    await action(`Bind Pivot field to ${expectedId}`, page.locator(selector),
      locator => locator.selectOption(expectedId, { timeout: ACTION_TIMEOUT_MS }));
  };
  await selectOutputById('select[aria-label="Pivot category field"]', codeGroupOutput.id);
  await selectOutputById('select[aria-label="Pivot values field"]', countGroupOutput.id);
  const categorySummary = page.locator('summary').filter({ hasText: 'Change selected categories' });
  await categorySummary.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  if (!await categorySummary.evaluate(element => element.parentElement.open)) {
    await action('Open exact Pivot category choices', categorySummary,
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }));
  }
  const categoryContainer = page.getByTestId('construction-reshape-pivot-categories');
  await categoryContainer.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const categoryInputs = await categoryContainer.locator('input[type="checkbox"][aria-label^="Include category "]').evaluateAll(inputs =>
    inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked })));
  assert.deepEqual(categoryInputs.map(item => item.label).sort(), oracle.categories.map(category => `Include category ${category}`).sort());
  for (const category of oracle.categories) {
    const input = categoryContainer.locator(`input[aria-label=${JSON.stringify(`Include category ${category}`)}]`);
    assert.equal(await input.count(), 1);
    if (!await input.isChecked()) await action(`Include exact Pivot category ${category}`, input,
      locator => locator.check({ timeout: ACTION_TIMEOUT_MS }));
  }
  await waitProposal(1);
  const pivotProposal = await proposalEvent(pivotFromIndex, candidate => {
    const step = candidate?.steps?.at(-1);
    const pivot = step?.operation?.pivot;
    return step?.operation?.kind === 'PIVOT' &&
      isDeepStrictEqual(pivot?.groupKeyIds, [subjectGroupOutput.id]) &&
      pivot?.categoryColumnId === codeGroupOutput.id && pivot?.valueColumnId === countGroupOutput.id &&
      isDeepStrictEqual(pivot?.categories?.map(category => category.key?.string), oracle.categories);
  }, pivotBase);
  const expectedViewerRows = normalizeExpectedRows(oracle.columns, oracle.rows);
  const pivotPreview = assertGrid(await readGrid(page, 'proposal'), oracle.columns, expectedViewerRows, 'Native CDA Group→Pivot proposal');
  const pivotCandidate = pivotProposal.requestBody.candidateConstruction.steps.at(-1).operation.pivot;
  assert.deepEqual(pivotCandidate.groupKeyIds, [subjectGroupOutput.id]);
  assert.equal(pivotCandidate.categoryColumnId, codeGroupOutput.id);
  assert.equal(pivotCandidate.valueColumnId, countGroupOutput.id);
  assert.deepEqual(pivotCandidate.categories.map(category => category.key.string), oracle.categories);
  reportCheck('correctness', 'Native Pivot preserves exact CDA category columns and 2/1 multiplicity', true, {
    outputId, proposalId: pivotProposal.responseBody.proposalId, columns: pivotPreview.headers,
    rows: pivotPreview.rows, expectedRows: expectedViewerRows,
  });
  await action('Apply native CDA Group→Pivot composition', page.getByTestId('construction-apply-proposal'),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => savedReady(1));
  builder = await readBuilder();
  document = getDocument(builder, outputId);
  steps = document.construction?.steps ?? [];
  assert.deepEqual(steps.map(step => step.operation.kind), ['GROUP', 'PIVOT']);
  const savedGroup = steps[0];
  const pivotStep = steps[1];
  assert.equal(savedGroup.id, groupStep.id);
  assert.deepEqual(savedGroup.operation.group.keys.map(key => key.inputColumnId), groupKeys.map(key => key.columnId));
  assert.equal(savedGroup.operation.group.aggregates[0].operation, 'COUNT_ROWS');
  assert.deepEqual(pivotStep.operation.pivot.groupKeyIds, [subjectGroupOutput.id]);
  assert.equal(pivotStep.operation.pivot.categoryColumnId, codeGroupOutput.id);
  assert.equal(pivotStep.operation.pivot.valueColumnId, countGroupOutput.id);
  assert.deepEqual(pivotStep.operation.pivot.categories.map(category => category.key.string), oracle.categories);
  assert.deepEqual(document.population?.selectionRevisionId, selection.id);
  assertGrid(await readGrid(page), oracle.columns, expectedViewerRows, 'Applied CDA Group→Pivot rows');
  reportCheck('persistence', 'Saved CDA output contains the exact GROUP then PIVOT bindings and immutable source selection', true, {
    outputId, selectionRevisionId: selection.id, stepIds: steps.map(step => step.id),
    groupKeys: savedGroup.operation.group.keys, pivot: pivotStep.operation.pivot,
  });

  if (await closeEditor.count() && await closeEditor.isVisible()) {
    await action('Close Pivot editor before Builder reload', closeEditor,
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), () => savedReady(1));
  }
  const builderReloadStarted = performance.now();
  await action('Reload saved CDA Group→Pivot Builder output', outputTab,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }),
    async () => {
      await outputTab.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      if (await outputTab.getAttribute('aria-current') !== 'page') {
        await outputTab.click({ timeout: ACTION_TIMEOUT_MS });
      }
      await savedReady(1);
    });
  const builderReloadMs = performance.now() - builderReloadStarted;
  assert(builderReloadMs <= ACTION_TIMEOUT_MS);
  builder = await readBuilder();
  document = getDocument(builder, outputId);
  steps = document.construction?.steps ?? [];
  assert.deepEqual(steps.map(step => step.operation.kind), ['GROUP', 'PIVOT']);
  assert.equal(steps[0].id, groupStep.id);
  assert.equal(steps[1].id, pivotStep.id);
  assert.equal(document.population?.selectionRevisionId, selection.id);
  assertGrid(await readGrid(page), oracle.columns, expectedViewerRows, 'Reloaded saved CDA Group→Pivot rows');
  reportCheck('persistence', 'Exact CDA GROUP/PIVOT bindings and Viewer-bound rows survive Builder reload', true, {
    outputId, selectionRevisionId: selection.id, stepIds: steps.map(step => step.id),
    elapsedMs: Math.round(builderReloadMs), budgetMs: ACTION_TIMEOUT_MS,
  });
  reportCheck('performance', 'Saved CDA Group→Pivot Builder reload completes within five seconds', builderReloadMs <= ACTION_TIMEOUT_MS,
    { elapsedMs: Math.round(builderReloadMs), budgetMs: ACTION_TIMEOUT_MS });

  const publishPath = `${authoring}/publish`;
  const publishFromIndex = capture.requests.length;
  let publishResponse;
  await action('Publish exact saved CDA Group→Pivot output through Builder UI', page.getByRole('button', { name: 'Publish', exact: true }),
    locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
      const event = await capture.waitForResponse(entry => entry.path === publishPath && entry.method === 'POST' &&
        entry.status >= 200 && entry.status < 300 && entry.completedAt,
      { fromIndex: publishFromIndex, timeoutMs: ACTION_TIMEOUT_MS });
      const requestBody = capture.requestBody(event);
      const responseBody = capture.responseBody(event);
      assert(requestBody && responseBody, 'Native Publish request and response bodies must be retained.');
      assert.equal(responseBody.kind, 'ExplorerBuilderPublication');
      assert(responseBody.apiVersion && requestBody.receiptId && responseBody.receiptId && responseBody.revisionId);
      assert.equal(responseBody.receiptId, requestBody.receiptId, 'Publish response must bind the submitted exact receipt.');
      assert.equal(responseBody.state, 'ACTIVE');
      assert.deepEqual(responseBody.diagnostics, []);
      assert.equal(responseBody.outputs?.length, 1, 'Fresh Explorer publication must return exactly one output.');
      assert.deepEqual(responseBody.outputs.map(output => ({ outputId: output.outputId, state: output.state })),
        [{ outputId, state: 'READY' }]);
      publishResponse = { event, requestBody, responseBody };
      await page.waitForFunction(() => [...document.querySelectorAll('button')]
        .some(button => button.innerText.trim() === 'Publish' && button.disabled), undefined, { timeout: ACTION_TIMEOUT_MS });
    });
  const { responseBody: publishPayload } = publishResponse;
  reportCheck('correctness', 'Native Publish binds the submitted receipt, active revision, and exact CDA output', true, {
    status: publishResponse.event.status, receiptId: publishPayload.receiptId,
    revisionId: publishPayload.revisionId, state: publishPayload.state, outputId,
    outputState: publishPayload.outputs[0].state,
  });

  const runtimePath = base;
  const graphPath = '/graphql/graph';
  const matchesRuntime = response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === new URL(uiOrigin).origin && new URL(response.url()).pathname === runtimePath;
  const matchesOutputQuery = response => {
    try {
      const input = response.request().postDataJSON()?.variables?.input;
      return response.request().method() === 'POST' && new URL(response.url()).origin === new URL(uiOrigin).origin &&
        new URL(response.url()).pathname === graphPath && input?.projectId === project && input?.selector?.output === outputId;
    } catch { return false; }
  };
  const viewerURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=viewer`;
  const verifyViewerCycle = async ({ reload, label }) => {
    const started = performance.now();
    const deadlineAt = Date.now() + ACTION_TIMEOUT_MS;
    const remaining = () => {
      const value = deadlineAt - Date.now();
      assert(value > 0, `${label} exhausted the five-second navigation and render budget.`);
      return value;
    };
    const runtimePromise = page.waitForResponse(matchesRuntime, { timeout: remaining() });
    const queryPromise = page.waitForResponse(matchesOutputQuery, { timeout: remaining() });
    if (reload) await page.reload({ waitUntil: 'domcontentloaded', timeout: remaining() });
    else await awaitBeforeDeadline(() => cda.navigate(viewerURL), deadlineAt, `${label} CDA Viewer navigation`);
    const [runtimeResponse, queryResponse] = await awaitBeforeDeadline(
      () => Promise.all([runtimePromise, queryPromise]), deadlineAt, `${label} runtime and GraphQL response capture`);
    assert(runtimeResponse.ok(), `${label} runtime returned HTTP ${runtimeResponse.status()}.`);
    assert(queryResponse.status() >= 200 && queryResponse.status() < 300, `${label} GraphQL returned HTTP ${queryResponse.status()}.`);
    const [state, queryPayload] = await awaitBeforeDeadline(
      () => Promise.all([runtimeResponse.json(), queryResponse.json()]), deadlineAt, `${label} runtime and GraphQL JSON bodies`);
    const runtime = state.runtime;
    assert.equal(state.project, project);
    assert.equal(state.explorerId, explorer);
    assert.equal(state.active?.revisionId, publishPayload.revisionId, `${label} active state must bind the exact Publish revision.`);
    assert(runtime && Array.isArray(runtime.outputs));
    assert.equal(runtime.status, 'ACTIVE');
    assert.equal(runtime.generation ?? runtime.publication?.generation, generation);
    assert.equal(runtime.publication?.state, 'ACTIVE');
    assert.equal(runtime.publication?.revisionId, publishPayload.revisionId,
      `${label} runtime metadata must bind the exact Publish revision.`);
    const outputs = runtime.outputs.filter(output => output.outputId === outputId);
    assert.equal(outputs.length, 1, `${label} runtime must expose the exact published output once.`);
    assert.equal(runtime.outputs.length, 1, `${label} fresh Explorer runtime must contain one output.`);
    const output = outputs[0];
    const queryInput = queryResponse.request().postDataJSON()?.variables?.input;
    assert.equal(queryInput.projectId, project);
    assert.equal(queryInput.selector?.output, outputId);
    assert.equal(queryInput.selector?.recipe, output.selector?.recipe);
    assert.equal(queryInput.selector?.translationVersion, output.selector?.translationVersion);
    assert(!Array.isArray(queryPayload.errors) || queryPayload.errors.length === 0,
      `${label} GraphQL query must have no errors.`);
    const connection = queryPayload.data?.dataframeRows;
    assert(connection && Array.isArray(connection.columns) && Array.isArray(connection.rows));
    const visibleBindings = output.table.columns.filter(binding => binding.visible &&
      output.columns.some(column => column.column === binding.column && column.visible));
    assert.equal(visibleBindings.length, oracle.columns.length);
    const selectedColumns = oracle.columns.map(label => {
      const matches = output.columns.filter(column => column.label === label);
      assert.equal(matches.length, 1, `${label} must map to exactly one runtime output column.`);
      assert(visibleBindings.some(binding => binding.column === matches[0].column), `${label} must be visible in Viewer runtime metadata.`);
      return matches[0];
    });
    assert.deepEqual(visibleBindings.map(binding => output.columns.find(column => column.column === binding.column)?.label), oracle.columns,
      `${label} runtime visible column order must match the independent CDA oracle.`);
    const materializationColumns = output.materialization?.columns;
    assert(Array.isArray(materializationColumns), `${label} runtime output must expose compiled materialization columns.`);
    const selectedMaterializations = selectedColumns.map((column, index) => {
      const matches = materializationColumns.filter(item => item.name === column.column);
      assert.equal(matches.length, 1, `${label} ${column.label} must map to one compiled materialization column.`);
      assert.equal(matches[0].logicalType, column.logicalType,
        `${label} runtime and compiled materialization logical types must agree.`);
      const expectedType = typeof oracle.rows[0][oracle.columns[index]] === 'number' ? 'integer' : 'string';
      assert.equal(column.logicalType, expectedType, `${label} ${column.label} type must match the raw oracle.`);
      return matches[0];
    });
    const indices = selectedColumns.map(column => connection.columns.indexOf(column.column));
    assert(indices.every(index => index >= 0), `${label} GraphQL response must contain every exact published column.`);
    const wireRows = connection.rows.map(row => Object.fromEntries(selectedColumns.map((column, index) => [
      oracle.columns[index], Array.isArray(row) ? row[indices[index]] : row?.[column.column],
    ])));
    let responseRows;
    try {
      responseRows = wireRows.map(row => Object.fromEntries(selectedColumns.map((column, index) => {
        const value = row[oracle.columns[index]];
        return [oracle.columns[index], column.logicalType === 'integer'
          ? decodeCohortGroupPivotIntegerValue(value, column.logicalType) : value];
      })));
    } catch (error) {
      assert.fail(`${label} GraphQL integer decoding failed: ${error.message}; wire=${JSON.stringify(wireRows)}; expected=${JSON.stringify(oracle.rows)}`);
    }
    assert.deepEqual(responseRows, oracle.rows, `${label} exact GraphQL rows must equal the independent raw CDA oracle.`);
    const table = page.locator('table[aria-label$=" results"]');
    await table.waitFor({ state: 'visible', timeout: remaining() });
    await page.waitForFunction(expectedCount => document.querySelector('table[aria-label$=" results"] tbody')?.querySelectorAll('tr').length === expectedCount,
      oracle.rows.length, { timeout: remaining() });
    const visible = {
      headers: await table.locator('thead th').evaluateAll(readCdaHeaderLabels),
      rows: await table.locator('tbody tr').evaluateAll(nodes => nodes.map(row =>
        [...row.querySelectorAll('td')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim()))),
    };
    assert.deepEqual(visible.headers, oracle.columns, `${label} visible Viewer headers must match the independent CDA oracle.`);
    assert.deepEqual(sortRows(visible.rows), sortRows(expectedViewerRows),
      `${label} visible Viewer values or row multiplicity must match the independent CDA oracle.`);
    const elapsedMs = performance.now() - started;
    assert(elapsedMs <= ACTION_TIMEOUT_MS, `${label} took ${Math.round(elapsedMs)} ms, over five seconds.`);
    reportCheck('performance', `${label} runtime, GraphQL, and exact visible Viewer rows complete within five seconds`, true, {
      elapsedMs: Math.round(elapsedMs), budgetMs: ACTION_TIMEOUT_MS, outputId, revisionId: publishPayload.revisionId,
    });
    reportCheck('correctness', `${label} runtime metadata binds the exact published output and revision`, true, {
      receiptId: publishPayload.receiptId, revisionId: runtime.publication.revisionId, outputId,
      generation: runtime.generation, selector: queryInput.selector,
    });
    reportCheck('correctness', `${label} GraphQL response matches exact raw CDA Group→Pivot rows and compiled types`, true, {
      outputId, columns: connection.columns, logicalTypes: selectedColumns.map((column, index) => ({
        column: column.column, logicalType: column.logicalType, clickhouseType: selectedMaterializations[index].clickhouseType,
      })), wireRows, rows: responseRows, expectedRows: oracle.rows,
    });
    reportCheck('correctness', `${label} visible Viewer renders the exact published CDA Group→Pivot rows`, true, {
      outputId, headers: visible.headers, rows: visible.rows, expectedRows: expectedViewerRows,
    });
    return { state, runtime, output, queryInput, wireRows, rows: responseRows, visible, elapsedMs };
  };

  const initialViewer = await verifyViewerCycle({ reload: false, label: 'Published CDA Viewer first navigation' });
  report.target.publication = {
    receiptId: publishPayload.receiptId, revisionId: publishPayload.revisionId,
    outputId, state: publishPayload.state, generation,
  };
  report.target.viewer = { revisionId: initialViewer.runtime.publication.revisionId,
    receiptId: publishPayload.receiptId, outputId, columns: oracle.columns, rows: initialViewer.rows };
  const reloadedViewer = await verifyViewerCycle({ reload: true, label: 'Published CDA Viewer full reload' });
  reportCheck('persistence', 'Published CDA receipt, revision, exact output, and rows survive full Viewer reload',
    reloadedViewer.runtime.publication.revisionId === publishPayload.revisionId &&
      isDeepStrictEqual(reloadedViewer.rows, oracle.rows), {
      receiptId: publishPayload.receiptId, revisionId: reloadedViewer.runtime.publication.revisionId,
      outputId, rows: reloadedViewer.rows, expectedRows: oracle.rows,
    });
}
