import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { builderDraftStateEvidence } from '../helpers/builder-combine-draft-helpers.mjs';
import { assertCdaNoAuthRuntime } from '../helpers/cda-no-auth-runtime.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import {
  assertCdaPatientDatasetZipReread,
  bindCdaPatientDatasetZipPublication,
  cdaPatientDatasetZipRereadQuery,
  cdaPatientDatasetZipScanQuery,
  CdaPatientDatasetZipWitnessUnavailable,
  inspectCdaPatientDatasetZip,
  prepareCdaPatientDatasetZipOracle,
} from '../helpers/cda-viewer-output-dataset-zip-oracle.mjs';

const ACTION_BUDGET_MS = 5_000;
const encode = value => encodeURIComponent(value);
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();

function runArango(container, query, label) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const invocation = buildArangoShellInvocation({ container, database: 'loom_dev', script: javascript });
  const result = spawnSync(invocation.command, invocation.args,
    { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `${label} failed: ${normalize(result.stderr || result.stdout).slice(0, 1_800)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `${label} returned no JSON array: ${normalize(result.stdout).slice(-900)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows), `${label} did not return an array`);
  return rows;
}

const readBuilderGrid = page => page.evaluate(() => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return { ready: false, headers: [], rows: [], rowCount: null };
  const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  return {
    ready: document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready',
    headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent)),
    rows: [...table.querySelectorAll('[role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.textContent))),
    rowCount: table.getAttribute('aria-rowcount'),
  };
});

const readDownloadModal = dialog => dialog.evaluate(element => {
  const value = label => [...element.querySelectorAll('dt')]
    .find(term => term.textContent.trim() === label)?.nextElementSibling?.textContent.trim() ?? '';
  return {
    text: element.innerText,
    sourceGeneration: value('Source generation'),
    schemaDigest: value('Schema digest'),
    types: element.querySelector('[aria-label="Declared output types"]')?.innerText ?? '',
  };
});

export async function cdaViewerOutputDatasetZipWorkflow({ page, cda }, testInfo) {
  const { target, report } = cda;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  assert(project && generation && apiOrigin && uiOrigin,
    'Owned CDA fixture must provide project, generation, API, and UI origins');
  assert.equal(project, target.fixtureProject, 'CDA ZIP witnesses must use the exact owned project');
  assert.equal(generation, 'cda-fhir-v1', 'CDA Viewer ZIP requires the pinned FHIR generation');
  report.phase = 'published exact raw CDA Patient IDs exported as an identity-bound ZIP';
  report.authorization.apiRuntime = assertCdaNoAuthRuntime({ apiContainer: target.apiContainer });

  const check = (name, passed, evidence = {}, dimension = 'correctness') => {
    cda.check(dimension, name, passed, evidence);
    assert(passed, name);
  };
  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-patient-zip-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${normalize(text).slice(0, 1_000)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_600)}`);
    return value;
  };
  const arangoContainer = validatedArangoContainer(target);
  const rawRows = runArango(arangoContainer, cdaPatientDatasetZipScanQuery({ project, generation }),
    'Bounded raw CDA Patient ZIP witness scan');
  const oracle = prepareCdaPatientDatasetZipOracle(rawRows, { project, generation });
  report.target.rawCdaPatientZipOracle = {
    project, generation, scanLimitPerResource: oracle.scanLimitPerResource,
    scanLimitBoundsReturnedRowsNotDatabaseScanCost: oracle.scanLimitBoundsReturnedRowsNotDatabaseScanCost,
    returnedRows: oracle.returnedRows, selected: oracle.selected, patientIDs: oracle.patientIDs,
    expectedRows: oracle.rows,
  };
  check('bounded raw CDA Patient scan returns two exact payload ID witnesses',
    oracle.selected.length === 2 && oracle.patientIDs.length === 2,
    report.target.rawCdaPatientZipOracle);

  const explorer = `cda-patient-zip-${randomUUID()}`;
  const explorerTitle = `CDA Patient ZIP ${randomUUID().slice(0, 8)}`;
  const apiRoot = `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
  const created = await api(`/api/v1/projects/${encode(project)}/explorers`, { name: explorer, title: explorerTitle });
  const createdID = created.explorerId ?? created.id ?? created.explorer?.id ?? created.name;
  assert.equal(createdID, explorer, 'Fresh CDA ZIP Explorer must retain its unique requested identity');
  const explorerList = await api(`/api/v1/projects/${encode(project)}/explorers`);
  const summaries = Array.isArray(explorerList) ? explorerList : explorerList.explorers ?? explorerList.value ?? [];
  const matchingExplorers = summaries.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
  assert.equal(matchingExplorers.length, 1, 'Fresh CDA ZIP Explorer must appear exactly once');
  assert.equal(matchingExplorers[0].project, project);
  assert.equal(matchingExplorers[0].title, explorerTitle);
  report.target.explorer = explorer;
  report.target.explorerTitle = explorerTitle;

  const builderPath = `${apiRoot}/authoring/v2/builder`;
  let builder = await api(builderPath);
  const emptyDraft = builderDraftStateEvidence(builder, 'empty');
  assert.equal(builder.catalog?.generation, generation);
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest);
  const scope = { generation, snapshotToken: builder.catalog.snapshotToken,
    authorizationScopeDigest: builder.catalog.authorizationScopeDigest };
  check('fresh CDA ZIP Explorer is empty and bound to the exact project, generation, and authorization scope',
    emptyDraft.ok, { explorer, scope, emptyDraft });

  const rootMatches = builder.catalog.nodes.filter(node => node.resourceType === 'Patient' && node.rowRootEligible);
  assert.equal(rootMatches.length, 1, 'CDA catalog must expose one Patient row root');
  const rootNode = rootMatches[0];
  const idCandidates = builder.catalog.candidates.filter(candidate =>
    candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
  assert.equal(idCandidates.length, 1, 'CDA Patient root must expose one Patient.id field candidate');
  const candidate = idCandidates[0];
  assert.equal(candidate.logicalType, 'string', 'CDA Patient.id must be scalar string');
  assert.equal(candidate.repeated, false, 'CDA Patient.id must not repeat');
  assert(['required_one', 'optional_one'].includes(candidate.cardinality),
    'CDA Patient.id must have one-valued cardinality');

  const readBuilder = async () => api(builderPath);
  const postCommand = async commands => {
    const body = {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    };
    await api(`${apiRoot}/authoring/v2/commands`, body);
    builder = await readBuilder();
    assert.equal(builder.catalog?.generation, generation, 'CDA ZIP Builder left its pinned generation');
    assert.equal(builder.catalog?.snapshotToken, scope.snapshotToken, 'CDA ZIP Builder snapshot changed');
    assert.equal(builder.catalog?.authorizationScopeDigest, scope.authorizationScopeDigest,
      'CDA ZIP Builder authorization scope changed');
    return builder;
  };
  const priorOutputIDs = new Set((builder.workspace?.documents ?? []).map(document => document.output.id));
  await postCommand([{ type: 'CREATE_TABLE', title: 'CDA Patient IDs', rootNodeId: rootNode.nodeId }]);
  const createdDocuments = (builder.workspace?.documents ?? []).filter(document => !priorOutputIDs.has(document.output.id));
  assert.equal(createdDocuments.length, 1, 'CDA ZIP source must create exactly one Patient output');
  const outputId = createdDocuments[0].output.id;
  await postCommand([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient ID' }]);
  const authoredDocument = builder.workspace.documents.find(document => document.output.id === outputId);
  assert(authoredDocument, 'CDA ZIP Patient output must remain in the Builder workspace');
  const authoredPatientColumns = authoredDocument.columns.filter(column =>
    column.source?.kind === 'field' && column.source.field?.path === 'id');
  assert.equal(authoredPatientColumns.length, 1, 'CDA ZIP output must author exactly one Patient.id column');
  const authoredColumn = authoredPatientColumns[0];
  const rereadRows = runArango(arangoContainer, cdaPatientDatasetZipRereadQuery({
    project, generation, documentIDs: oracle.selected.map(row => row._id),
  }), 'Exact raw CDA Patient ZIP witness reread');
  const reread = assertCdaPatientDatasetZipReread(rereadRows, oracle);

  const selection = await api(`${apiRoot}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `cda-patient-zip-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: oracle.references } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Patient');
  assert.equal(selection.scopeDigest, scope.authorizationScopeDigest);
  assert.equal(selection.memberCount, oracle.references.length);
  const selectionRead = await api(`${apiRoot}/selections/${encode(selection.id)}?limit=100`);
  const actualRefs = (selectionRead.members ?? []).map(member => {
    const ref = member.ref ?? {};
    return `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
  }).sort();
  const expectedRefs = oracle.references.map(ref =>
    `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  assert.equal(selectionRead.revision?.id, selection.id);
  assert.equal(selectionRead.revision?.project, project);
  assert.equal(selectionRead.revision?.generation, generation);
  assert.equal(selectionRead.revision?.resourceType, 'Patient');
  assert.equal(selectionRead.revision?.scopeDigest, scope.authorizationScopeDigest);
  assert.equal(selectionRead.revision?.memberCount, 2);
  assert.equal((selectionRead.members ?? []).length, 2);
  assert.deepEqual(actualRefs, expectedRefs, 'CDA ZIP immutable selection must pin the exact raw FHIR Patient IDs');
  assert.equal(new Set((selectionRead.members ?? []).map(member => member.memberKey)).size, 2);
  const routes = await api(`${apiRoot}/authoring/v2/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
  });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'CDA ZIP Patient selection must offer a direct root population route');
  await postCommand([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id,
    routeChoiceId: directRoute.routeChoiceId }]);
  const sourceDocument = builder.workspace.documents.find(document => document.output.id === outputId);
  const sourceBound = sourceDocument.rootResourceType === 'Patient' &&
    sourceDocument.population?.selectionRevisionId === selection.id &&
    (sourceDocument.population.route?.length ?? 0) === 0 && sourceDocument.columns.length === 1 &&
    sourceDocument.columns[0].source?.field?.path === 'id';
  check('CDA Patient output binds the exact immutable raw FHIR ID selection', sourceBound,
    { outputId, selectionId: selection.id, actualRefs, expectedRefs, rawReread: reread,
      rootResourceType: sourceDocument.rootResourceType,
      column: { column: authoredColumn.column, label: authoredColumn.label, source: authoredColumn.source } });

  const responsePaths = path => path === `${apiRoot}/authoring/v2/publish` ||
    path === `${apiRoot}/authoring/v2/preview` || path === `${apiRoot}/authoring/v2/artifacts` ||
    path === runtimePath(project, explorer) ||
    path === `${apiRoot}/authoring/v2/commands` || path === `${apiRoot}/authoring/v2/builder`;
  const requests = cda.captureRequests(apiRoot, { responsePaths });
  const previewPath = `${apiRoot}/authoring/v2/preview`;
  const previewStart = requests.requests.length;
  await cda.navigate(`${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`);
  const tableButton = page.getByTestId(`construction-table-${outputId}`);
  await tableButton.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const previewReady = () => page.waitForFunction(({ outputId, count }) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === outputId &&
      table?.getAttribute('aria-rowcount') === String(count + 1);
  }, { outputId, count: oracle.rows.length }, { timeout: ACTION_BUDGET_MS });
  let compiledPreviewEntry;
  let compiledPreviewColumn;
  const captureCompiledPreview = async () => {
    const previewReceiptId = await page.locator('[data-testid="construction-preview"]')
      .getAttribute('data-preview-receipt-id');
    assert(previewReceiptId, 'Ready CDA Patient preview must expose its receipt identity');
    compiledPreviewEntry = await requests.waitForResponse(entry =>
      entry.method === 'POST' && entry.path === previewPath && entry.status >= 200 && entry.status < 300 &&
      entry.request?.outputId === outputId && entry.request?.receiptId === previewReceiptId &&
      entry.response?.outputId === outputId && entry.response?.receiptId === previewReceiptId,
    { fromIndex: previewStart, timeoutMs: ACTION_BUDGET_MS });
    const previewResponse = requests.responseBody(compiledPreviewEntry);
    const matchingColumns = (previewResponse?.columns ?? []).filter(column =>
      column?.column === authoredColumn.column);
    assert.equal(matchingColumns.length, 1,
      'CDA compiled Preview must contain the exact authored Patient ID output column once');
    compiledPreviewColumn = matchingColumns[0];
    const exactCompiledColumn = compiledPreviewEntry.networkComplete === true &&
      compiledPreviewEntry.responseCaptureComplete === true &&
      previewResponse?.outputId === outputId && previewResponse?.receiptId === previewReceiptId &&
      previewResponse?.rowCount === oracle.rows.length &&
      previewResponse.columns.length === 1 && compiledPreviewColumn.label === 'Patient ID' &&
      compiledPreviewColumn.logicalType === 'string' && compiledPreviewColumn.shape === 'scalar' &&
      typeof compiledPreviewColumn.nullable === 'boolean';
    check('CDA compiled Preview binds the exact Patient ID output column, scalar type, and nullable schema',
      exactCompiledColumn,
      { receiptId: previewReceiptId, outputId: previewResponse?.outputId, rowCount: previewResponse?.rowCount,
        expectedRowCount: oracle.rows.length, columns: previewResponse?.columns,
        expectedColumn: authoredColumn.column });
    return true;
  };
  if (await tableButton.getAttribute('aria-current') !== 'page') {
    await cda.action('Open exact raw CDA Patient source preview', tableButton,
      locator => locator.click({ timeout: ACTION_BUDGET_MS }), {
        timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
        requiredCheck: 'CDA Patient source preview renders exact raw IDs within five seconds',
        after: async () => { await previewReady(); await captureCompiledPreview(); },
      });
  } else {
    await previewReady();
    await captureCompiledPreview();
  }
  const previewGrid = await readBuilderGrid(page);
  const previewIDs = previewGrid.rows.map(row => row[0]).sort();
  const expectedPatientIDs = [...oracle.patientIDs].sort();
  const previewExact = previewGrid.ready && isDeepStrictEqual(previewGrid.headers, ['Patient ID']) &&
    isDeepStrictEqual(previewIDs, expectedPatientIDs);
  check('CDA Builder preview shows exactly the selected raw Patient IDs', previewExact,
    { headers: previewGrid.headers, expectedHeaders: ['Patient ID'], rows: previewGrid.rows,
      expectedPatientIDs, rowCount: previewGrid.rowCount });

  const publishPath = `${apiRoot}/authoring/v2/publish`;
  const publishStart = requests.requests.length;
  const publishResponse = requests.waitForResponse(entry => entry.method === 'POST' && entry.path === publishPath,
    { fromIndex: publishStart, timeoutMs: ACTION_BUDGET_MS });
  const publishButton = page.getByRole('button', { name: 'Publish', exact: true });
  let publishedEntry;
  await cda.action('Publish exact raw CDA Patient ZIP source through Builder', publishButton,
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), {
      timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
      requiredCheck: 'native CDA Patient ZIP Publish completes within five seconds',
      after: async () => {
        publishedEntry = await publishResponse;
        assert(publishedEntry.status >= 200 && publishedEntry.status < 300,
          `Native CDA ZIP Publish returned HTTP ${publishedEntry.status}`);
      },
    });
  check('native Builder Publish succeeds for the exact raw CDA Patient source',
    publishedEntry.status >= 200 && publishedEntry.status < 300,
    { path: publishPath, status: publishedEntry.status, outputId, selectedPatientIDs: oracle.patientIDs });

  const viewerURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=viewer`;
  const runtimeURL = runtimePath(project, explorer);
  const waitRuntime = async fromIndex => requests.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === runtimeURL && entry.status >= 200 && entry.status < 300,
  { fromIndex, timeoutMs: ACTION_BUDGET_MS });
  const runtimeStart = requests.requests.length;
  await cda.navigate(viewerURL);
  let runtimeEntry = await waitRuntime(runtimeStart);
  const runtimeEnvelope = requests.responseBody(runtimeEntry);
  const runtime = runtimeEnvelope?.runtime;
  const output = runtime?.outputs?.find(item => item.outputId === outputId);
  const revisionId = runtime?.publication?.revisionId;
  check('CDA Viewer runtime binds the exact published project, Explorer, generation, revision, and Patient output',
    runtimeEnvelope?.project === project && runtimeEnvelope?.explorerId === explorer &&
      (runtime?.generation ?? runtime?.publication?.generation) === generation &&
      typeof revisionId === 'string' && revisionId.length > 0 && output?.outputId === outputId,
    { project: runtimeEnvelope?.project, explorer: runtimeEnvelope?.explorerId,
      generation: runtime?.generation ?? runtime?.publication?.generation, expectedGeneration: generation,
      revisionId, outputId: output?.outputId, expectedOutputId: outputId });
  assert(output, 'Published CDA Viewer runtime must expose the exact Patient output');
  const runtimeColumns = output.columns ?? [];
  check('published CDA Viewer output exposes the exact authored Patient ID column',
    runtimeColumns.length === 1 && runtimeColumns[0].column === authoredColumn.column &&
      runtimeColumns[0].label === 'Patient ID' && runtimeColumns[0].logicalType === 'string',
    { runtimeColumns, expectedColumn: authoredColumn.column });

  const outputTable = page.locator('table[aria-label$=" results"]');
  await outputTable.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const waitPatientIDs = ids => page.waitForFunction(expected => {
    const table = document.querySelector('table[aria-label$=" results"]');
    if (!table) return false;
    const rendered = [...table.querySelectorAll('tbody tr')].map(row => row.innerText);
    return rendered.length === expected.length && expected.every(id => rendered.some(text => text.includes(id)));
  }, ids, { timeout: ACTION_BUDGET_MS });
  await waitPatientIDs(expectedPatientIDs);
  const viewerRows = await outputTable.locator('tbody tr').allInnerTexts();
  const viewerIDs = viewerRows.map(text => expectedPatientIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`).sort();
  const viewerExact = isDeepStrictEqual(viewerIDs, expectedPatientIDs);
  check('CDA Viewer renders the exact independent raw Patient IDs before download', viewerExact,
    { expectedPatientIDs, viewerRows, actualPatientIDs: viewerIDs });

  const dialog = page.getByRole('dialog').filter({ hasText: 'Download dataset' });
  const artifactPath = `${apiRoot}/authoring/v2/artifacts`;
  const openModal = async phase => {
    const fromIndex = requests.requests.length;
    const preparationPromise = requests.waitForResponse(entry => entry.method === 'POST' &&
      entry.path === artifactPath && entry.status >= 200 && entry.status < 300 &&
      entry.response?.outputId === outputId,
    { fromIndex, timeoutMs: ACTION_BUDGET_MS });
    let preparation;
    await cda.action(`Open CDA Patient dataset ZIP modal ${phase}`,
      page.getByRole('button', { name: 'Download dataset', exact: true }),
      locator => locator.click({ timeout: ACTION_BUDGET_MS }), {
        timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
        requiredCheck: `${phase} CDA dataset ZIP modal opens within five seconds`,
        after: async () => {
          await dialog.getByRole('link', { name: 'Download ZIP', exact: true })
            .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
          preparation = await preparationPromise;
        },
      });
    const modal = await readDownloadModal(dialog);
    const descriptor = preparation.response;
    const modalBound = modal.sourceGeneration === generation &&
      /^[a-f0-9]{64}$/i.test(modal.schemaDigest) && modal.types.includes('Patient ID') &&
      modal.types.includes('string') && modal.text.includes('1 declared output columns');
    check(`${phase} modal declares the CDA source generation and exact Patient ID schema`, modalBound,
      { sourceGeneration: modal.sourceGeneration, expectedGeneration: generation,
        schemaDigest: modal.schemaDigest, declaredTypes: modal.types, modalText: modal.text });
    const href = await dialog.getByRole('link', { name: 'Download ZIP', exact: true }).getAttribute('href');
    const filename = await dialog.getByRole('link', { name: 'Download ZIP', exact: true }).getAttribute('download');
    assert.equal(typeof href, 'string', 'Visible CDA Download ZIP link must have a native artifact URL');
    const downloadURL = new URL(href, uiOrigin);
    const expectedArtifactPath = new RegExp(
      `^/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}/authoring/v2/artifacts/artifact_[0-9a-f]{64}$`);
    const artifactID = decodeURIComponent(downloadURL.pathname.split('/').at(-1));
    const descriptorMatches = preparation.status === 200 && preparation.networkComplete &&
      preparation.responseCaptureComplete && descriptor?.state === 'COMPLETE' && descriptor.id === artifactID &&
      descriptor.project === project && descriptor.explorerId === explorer &&
      descriptor.datasetGeneration === generation && descriptor.outputId === outputId &&
      descriptor.revisionId === revisionId && descriptor.schemaDigest === modal.schemaDigest &&
      descriptor.rows === oracle.rows.length && descriptor.features === 1 &&
      descriptor.mediaType === 'application/zip' && descriptor.filename === filename &&
      Number.isSafeInteger(descriptor.bytes) && descriptor.bytes > 0 &&
      /^[a-f0-9]{64}$/i.test(descriptor.archiveSha256 ?? '');
    check(`${phase} ZIP link binds the exact COMPLETE CDA preparation descriptor`,
      downloadURL.origin === new URL(uiOrigin).origin && expectedArtifactPath.test(downloadURL.pathname) &&
        filename === 'loom-dataset-artifact-v2.zip' && descriptorMatches,
      { path: downloadURL.pathname, artifactID, filename, status: preparation.status,
        descriptor: { id: descriptor?.id, state: descriptor?.state, project: descriptor?.project,
          explorerId: descriptor?.explorerId, datasetGeneration: descriptor?.datasetGeneration,
          outputId: descriptor?.outputId, revisionId: descriptor?.revisionId, schemaDigest: descriptor?.schemaDigest,
          rows: descriptor?.rows, features: descriptor?.features, mediaType: descriptor?.mediaType,
          filename: descriptor?.filename, bytes: descriptor?.bytes, archiveSha256: descriptor?.archiveSha256 },
        expectedRevisionId: revisionId, expectedSchemaDigest: modal.schemaDigest });
    const boundOracle = bindCdaPatientDatasetZipPublication(oracle, {
      explorerId: explorer, outputId, revisionId, schemaDigest: modal.schemaDigest,
      columnName: authoredColumn.column, candidate, compiledPreviewColumn,
    });
    return { modal, href: downloadURL.href, filename, descriptor, boundOracle };
  };

  const firstModal = await openModal('before cancel');
  await cda.action('Cancel CDA Patient dataset ZIP preview',
    dialog.getByRole('button', { name: 'Cancel', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), {
      timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
      requiredCheck: 'CDA dataset ZIP preview cancel closes within five seconds',
      after: () => dialog.waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS }),
    });
  check('Cancel closes the CDA dataset ZIP modal without leaving its download link active',
    await dialog.isHidden(),
    { dialogVisible: await dialog.isVisible(), firstArtifactId: firstModal.descriptor.id });

  const reloadStart = requests.requests.length;
  const reloadRuntimePromise = waitRuntime(reloadStart);
  await cda.action('Reload the published CDA Patient Viewer and restore the exact raw IDs',
    page.getByRole('button', { name: 'Download dataset', exact: true }),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), {
      timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
      requiredCheck: 'CDA Patient Viewer reload restores exact IDs within five seconds',
      after: async () => {
        [runtimeEntry] = await Promise.all([reloadRuntimePromise, waitPatientIDs(expectedPatientIDs)]);
      },
    });
  const reloadedEnvelope = requests.responseBody(runtimeEntry);
  const reloadedRuntime = reloadedEnvelope?.runtime;
  const reloadedOutput = reloadedRuntime?.outputs?.find(item => item.outputId === outputId);
  const reloadedRows = await outputTable.locator('tbody tr').allInnerTexts();
  const reloadedIDs = reloadedRows.map(text => expectedPatientIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`).sort();
  const reloadExact = reloadedEnvelope?.project === project && reloadedEnvelope?.explorerId === explorer &&
    (reloadedRuntime?.generation ?? reloadedRuntime?.publication?.generation) === generation &&
    reloadedRuntime?.publication?.revisionId === revisionId && reloadedOutput?.outputId === outputId &&
    isDeepStrictEqual(reloadedIDs, expectedPatientIDs);
  check('reload retains the exact CDA Patient publication revision, output, generation, and raw IDs', reloadExact,
    { project: reloadedEnvelope?.project, expectedProject: project,
      explorer: reloadedEnvelope?.explorerId, expectedExplorer: explorer,
      generation: reloadedRuntime?.generation ?? reloadedRuntime?.publication?.generation, expectedGeneration: generation,
      revisionId: reloadedRuntime?.publication?.revisionId, expectedRevisionId: revisionId,
      outputId: reloadedOutput?.outputId, expectedOutputId: outputId,
      expectedPatientIDs, reloadedRows, actualPatientIDs: reloadedIDs }, 'persistence');

  const finalModal = await openModal('after reload');
  assert.equal(finalModal.descriptor.revisionId, revisionId);
  assert.equal(finalModal.descriptor.outputId, outputId);
  const downloadPromise = page.waitForEvent('download', { timeout: ACTION_BUDGET_MS });
  const archivePath = testInfo.outputPath('cda-patient-dataset.zip');
  let download;
  await cda.action('Download the complete CDA Patient ZIP after reload',
    dialog.getByRole('link', { name: 'Download ZIP', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), {
      timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
      requiredCheck: 'after-reload native CDA ZIP download completes and matches the preparation bytes within five seconds',
      after: async () => {
        download = await downloadPromise;
        await download.saveAs(archivePath);
      },
    });
  await testInfo.attach('cda-patient-dataset.zip', { path: archivePath, contentType: 'application/zip' });
  const archiveBytes = await readFile(archivePath);
  const archiveSHA256 = createHash('sha256').update(archiveBytes).digest('hex');
  const downloadFailure = await download.failure();
  const byteBinding = downloadFailure === null && download.url() === finalModal.href &&
    download.suggestedFilename() === finalModal.filename && archiveBytes.byteLength === finalModal.descriptor.bytes &&
    archiveSHA256 === finalModal.descriptor.archiveSha256;
  check('after-reload native CDA ZIP download completes and matches the exact preparation bytes', byteBinding,
    { downloadURL: download.url(), expectedURL: finalModal.href, downloadFailure,
      suggestedFilename: download.suggestedFilename(), expectedFilename: finalModal.filename,
      archiveBytes: archiveBytes.byteLength, expectedBytes: finalModal.descriptor.bytes,
      archiveSHA256, expectedArchiveSHA256: finalModal.descriptor.archiveSha256 });

  const inspected = inspectCdaPatientDatasetZip(archivePath, finalModal.boundOracle, {
    expectedArchiveSHA256: finalModal.descriptor.archiveSha256,
  });
  check('CDA ZIP archive contains the exact typed members with verified checksums',
    inspected.memberNames.length === 6 && inspected.artifact.manifest.members.length === 5,
    { memberNames: inspected.memberNames, members: inspected.artifact.manifest.members });
  check('CDA ZIP archive identity and provenance match the published Explorer revision',
    inspected.artifact.manifest.identity.project === project &&
      inspected.artifact.manifest.identity.datasetGeneration === generation &&
      inspected.artifact.manifest.identity.outputId === outputId &&
      inspected.artifact.manifest.identity.revisionId === revisionId &&
      inspected.artifact.provenance?.explorerId === explorer,
    { identity: inspected.artifact.manifest.identity, provenance: inspected.artifact.provenance,
      expected: finalModal.boundOracle.identity });
  report.target.cdaPatientDatasetZip = {
    archivePath, archiveSHA256, archiveBytes: archiveBytes.byteLength,
    memberNames: inspected.memberNames, patientIDs: oracle.patientIDs,
    project, generation, explorer, outputId, revisionId, schemaDigest: finalModal.modal.schemaDigest,
  };
}
