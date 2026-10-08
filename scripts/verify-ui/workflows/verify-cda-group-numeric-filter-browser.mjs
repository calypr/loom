import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  selectAcceptedChoicePreviewBinding,
  selectCanceledAcceptedChoicePreviewBinding,
  selectCanceledSavedPreviewRequest,
  selectSavedPreviewRequest,
} from '../helpers/saved-preview-binding.mjs';
import { waitForSourceCapabilities } from './verify-cda-authored-expand-browser.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import {
  buildCdaGroupNumericFilterInitialQuery,
  buildCdaGroupNumericFilterExactReadQuery,
  buildCdaGroupNumericFilterTargetedQuery,
  matchesExactGroupSourceOptionsRequest,
  hasCancellableGroupSourceOptionsResponseState,
  isExactGroupSourceOptionsCancellation,
  authoredColumnIds,
  hasExactAuthoredColumnRestoration,
  prepareCdaGroupNumericFilterOracle,
  validateCdaGroupCandidate,
} from '../helpers/cda-group-numeric-filter-oracle.mjs';
import {
  chooseCdaGroupPivotJoinWitness,
  selectCdaGroupPivotJoinSubjects,
  verifyCdaGroupPivotJoinReread,
} from '../helpers/cda-group-pivot-join-oracle.mjs';

const generationExpected = 'cda-fhir-v1';
const scanLimit = 2_000;
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const encode = value => encodeURIComponent(value);
const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const basePath = (project, explorer) => `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;

function runArango(arangoContainer, query) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `Bounded owned Arango query failed: ${tidy(result.stderr || result.stdout).slice(0, 1600)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango query returned no JSON array: ${tidy(result.stdout).slice(-800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
}

export async function cdaGroupNumericFilterWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(cda.target);
  assert.equal(project, cda.target.fixtureProject, 'The numeric Group Filter oracle must use the owned CDA fixture project.');
  assert.equal(generation, generationExpected, 'Numeric Group Filter requires the pinned CDA FHIR generation.');
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  report.phase = 'CDA numeric COUNT_ROWS Filter after Group';
  report.cases = [];
  report.numericGroupFilter = { project, generation, initialSampleRowLimit: scanLimit, targetedSubjectRowLimit: scanLimit };
  const checkpoints = report.workflowCheckpoints = [];
  const checkpoint = (name, startedAt, evidence = {}) => {
    const durationMs = Date.now() - startedAt;
    assert(durationMs <= 5_000, `${name} took ${durationMs}ms; CDA native transitions must stay within five seconds.`);
    const result = { name, durationMs, ...evidence };
    checkpoints.push({ name, durationMs });
    report.cases.push(result);
    return result;
  };
  const check = (dimension, name, passed, evidence = {}) => {
    cda.check(dimension, name, Boolean(passed), evidence);
    assert(passed, name);
  };
  const apiRequests = report.apiRequests = [];
  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-group-numeric-filter-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${tidy(text).slice(0, 1200)}`); }
    apiRequests.push({ path, method: body === undefined ? 'GET' : 'POST', status: response.status(), requestId: headers['X-Request-ID'] });
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1600)}`);
    return value;
  };

  const initialQuery = buildCdaGroupNumericFilterInitialQuery({ project, generation });
  const initialRows = runArango(arangoContainer, initialQuery);
  const selectedSubjects = selectCdaGroupPivotJoinSubjects(initialRows, { project, generation, limit: 10 });
  const targetedQuery = buildCdaGroupNumericFilterTargetedQuery({ project, generation, selectedSubjects });
  const targetedRows = runArango(arangoContainer, targetedQuery);
  const witness = chooseCdaGroupPivotJoinWitness(targetedRows, { project, generation, limit: scanLimit });
  assert(witness, `The bounded ${scanLimit}-row CDA sample did not contain the exact shared-subject 3/1 COUNT_ROWS witness.`);
  const exactIds = witness.left.map(row => row._id);
  const exactReadQuery = buildCdaGroupNumericFilterExactReadQuery({ project, generation, exactIds });
  const exactRows = runArango(arangoContainer, exactReadQuery);
  const exactRead = verifyCdaGroupPivotJoinReread(witness, exactRows, { project, generation });
  const oracle = prepareCdaGroupNumericFilterOracle(witness, { project, generation, operator: 'GT', threshold: 1 });
  const emptyEditOracle = prepareCdaGroupNumericFilterOracle(witness, {
    project, generation, operator: 'GT', threshold: 3, requireStrictSubset: false,
  });
  assert.deepEqual(oracle.groupRows.map(row => Number(row[1])).sort((left, right) => left - right), [1, 3]);
  assert.deepEqual(oracle.filteredRows, [[witness.sharedSubject, '3']]);
  assert.deepEqual(emptyEditOracle.filteredRows, []);
  report.oracle = {
    initialQuery, targetedQuery, exactReadQuery,
    scanLimits: { initial: scanLimit, targeted: scanLimit, selectedSubjects: 10, selectedDocuments: 4 },
    initialRowsReturned: initialRows.length, targetedRowsReturned: targetedRows.length,
    selectedSubjects, exactRead, witness, expectedGroupRows: oracle.groupRows,
    expectedGtOneRows: oracle.filteredRows, expectedGtThreeRows: emptyEditOracle.filteredRows,
  };
  check('correctness', 'Bounded raw Observation oracle selects exact 3/1 COUNT_ROWS groups from four scoped IDs',
    witness.left.length === 4 && exactRead.exact && isDeepStrictEqual(oracle.groupRows.map(row => Number(row[1])).sort((a, b) => a - b), [1, 3]) &&
    isDeepStrictEqual(oracle.filteredRows, [[witness.sharedSubject, '3']]), {
      project, generation, leftIDs: witness.left.map(row => row.id), documentIDs: exactRead.leftIDs,
      groupRows: oracle.groupRows, gtOneRows: oracle.filteredRows, selectedSubjects,
      initialRowsReturned: initialRows.length, targetedRowsReturned: targetedRows.length,
    });

  const explorer = `cda-group-numeric-filter-${randomUUID()}`;
  const rootPath = `/api/v1/projects/${encode(project)}/explorers`;
  const base = `${basePath(project, explorer)}/authoring/v2`;
  const uiURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`;
  const created = await api(rootPath, { name: explorer, title: 'CDA numeric Group Filter QA' });
  assert.equal(created.explorerId ?? created.id ?? created.explorer?.id, explorer);
  report.explorer = explorer;

  let builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation);
  const initialScope = {
    snapshotToken: builder.catalog.snapshotToken,
    authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
    generation: builder.catalog.generation,
  };
  assert(initialScope.snapshotToken && initialScope.authorizationScopeDigest);
  const rootNodes = builder.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible);
  assert.equal(rootNodes.length, 1, 'The owned catalog must expose one eligible Observation root.');
  const command = async commands => {
    await api(`${base}/commands`, {
      commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, commands,
    });
    builder = await api(`${base}/builder`);
  };
  await command([{ type: 'CREATE_TABLE', title: 'Numeric COUNT_ROWS source', rootNodeId: rootNodes[0].nodeId }]);
  const documentBefore = builder.workspace.documents[0];
  const outputId = documentBefore.output.id;
  const idCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === rootNodes[0].nodeId && candidate.fieldPath === 'id');
  assert.equal(idCandidates.length, 1, 'The exact Observation FHIR ID column must be available.');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidates[0].candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);
  const selection = await api(`${basePath(project, explorer)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: `numeric-group-filter-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: witness.left.map(row => ({
      project, generation, resourceType: 'Observation', id: row.id,
    })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Observation');
  assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
  assert.equal(selection.memberCount, 4);
  const routes = await api(`${base}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
  });
  const direct = routes.choices.find(choice => choice.route.length === 0);
  assert(direct, 'The exact four-row Observation source must use a direct table population.');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);

  const capture = cda.captureRequests(`${rootPath}/${encode(explorer)}`, {
    responsePaths: /commands|construction-capabilities|construction-choice-proposals|construction-proposals|preview|reconcile/,
  });
  const nativeRequests = report.nativeRequests;
  const requestTerminalEvents = new WeakMap();
  const requestTerminalWaiters = new WeakMap();
  const recordRequestTerminal = (request, kind) => {
    const event = { kind, at: Date.now() };
    requestTerminalEvents.set(request, event);
    for (const resolve of requestTerminalWaiters.get(request) ?? []) resolve(event);
    requestTerminalWaiters.delete(request);
  };
  const onNativeRequestFinished = request => recordRequestTerminal(request, 'finished');
  const onNativeRequestFailed = request => recordRequestTerminal(request, 'failed');
  const removeNativeRequestTerminalListeners = () => {
    page.off('requestfinished', onNativeRequestFinished);
    page.off('requestfailed', onNativeRequestFailed);
    page.off('close', removeNativeRequestTerminalListeners);
  };
  page.on('requestfinished', onNativeRequestFinished);
  page.on('requestfailed', onNativeRequestFailed);
  page.once('close', removeNativeRequestTerminalListeners);
  const waitForRequestTerminal = (request, deadlineAt) => {
    const current = requestTerminalEvents.get(request);
    if (current) return Promise.resolve(current);
    return new Promise((resolve, reject) => {
      const timeoutMs = Math.max(1, deadlineAt - Date.now());
      const waiters = requestTerminalWaiters.get(request) ?? new Set();
      const timer = setTimeout(() => {
        waiters.delete(onTerminal);
        reject(new Error('The captured Group source-options request did not reach a terminal browser event within Cancel’s five-second action deadline.'));
      }, timeoutMs);
      const onTerminal = event => {
        clearTimeout(timer);
        waiters.delete(onTerminal);
        resolve(event);
      };
      waiters.add(onTerminal);
      requestTerminalWaiters.set(request, waiters);
      const afterRegistration = requestTerminalEvents.get(request);
      if (afterRegistration) onTerminal(afterRegistration);
    });
  };
  const documentFor = state => state.workspace.documents.find(document => document.output.id === outputId);
  const apiBuilder = async () => api(`${base}/builder`);
  const browserRows = async () => cda.inspect(() => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
    .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  const visibleRows = async expectedRows => {
    await cda.wait(({ rowCount }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table?.getAttribute('aria-rowcount') === String(rowCount + 1) && !document.body.innerText.includes('Loading your table…');
    }, { rowCount: expectedRows.length }, 5_000);
    const rows = await browserRows();
    assert.deepEqual(sortRows(rows), sortRows(expectedRows), 'Visible saved cells must match every exact independent raw tuple.');
    return rows;
  };
  const previewDom = async () => cda.inspect(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return {
      status: preview?.dataset.previewStatus ?? null,
      receiptId: preview?.dataset.previewReceiptId ?? null,
      outputId: preview?.dataset.previewOutputId ?? null,
      draftVersion: preview?.dataset.currentDraftVersion ?? null,
      draftDigest: preview?.dataset.currentDraftDigest ?? null,
    };
  });
  const readPreviewRows = async ({ receiptId, expectedRows, phase }) => {
    const saved = await api(`${base}/preview`, { receiptId, outputId, limit: 100 });
    assert.equal(saved.receiptId, receiptId, `${phase} must reread the exact saved preview receipt.`);
    assert.equal(saved.outputId, outputId, `${phase} saved preview must use the exact output.`);
    assert.equal(saved.rowCount, expectedRows.length, `${phase} full saved row count must match the raw oracle.`);
    assert.equal(saved.rows.length, expectedRows.length, `${phase} must return every saved row for this bounded witness.`);
    const values = saved.rows.map(row => saved.columns.map(column => row[column.column] == null ? '—' : String(row[column.column])));
    assert.deepEqual(sortRows(values), sortRows(expectedRows), `${phase} receipt-bound saved rows must equal the exact raw tuples.`);
    return { receiptId, outputId, rowCount: saved.rowCount, rows: values, columns: saved.columns };
  };
  const savedView = async (expectedRows, phase, { acceptedChoiceWindow } = {}) => {
    const current = await apiBuilder();
    const document = documentFor(current);
    assert(document, `${phase} requires the exact current output document.`);
    assert.equal(document.output.id, outputId);
    const preview = await previewDom();
    assert.equal(preview.status, 'ready', `${phase} must restore an automatically rendered saved preview.`);
    assert.equal(preview.outputId, outputId);
    assert.equal(preview.draftVersion, String(current.draftVersion));
    assert.equal(preview.draftDigest, current.draftDigest);
    let previewBinding;
    if (acceptedChoiceWindow) {
      const accepted = selectAcceptedChoicePreviewBinding(nativeRequests, {
        startIndex: acceptedChoiceWindow.startIndex,
        proposalPath: `${base}/construction-choice-proposals`,
        commandPath: `${base}/commands`,
        reconcilePath: `${base}/reconcile`,
        outputId,
        before: acceptedChoiceWindow.before,
        after: {
          snapshotToken: current.catalog.snapshotToken, draftVersion: current.draftVersion,
          draftDigest: current.draftDigest, outputId, workspace: current.workspace, preview,
        },
      });
      assert(accepted, `${phase} current preview must be the exact captured choice preview accepted by Apply and current reconciliation.`);
      assert(Array.isArray(accepted.preview.columns), `${phase} accepted choice receipt must include its exact saved columns.`);
      const proposalRows = accepted.preview.rows.map(row => accepted.preview.columns
        .map(column => row[column.column] == null ? '—' : String(row[column.column])));
      assert.deepEqual(sortRows(proposalRows), sortRows(expectedRows), `${phase} accepted choice preview rows must equal the independent raw tuples.`);
      previewBinding = {
        kind: 'accepted-choice', receiptId: accepted.receiptId, outputId, commandId: accepted.commandId,
        snapshotToken: current.catalog.snapshotToken, draftVersion: current.draftVersion, draftDigest: current.draftDigest,
      };
    } else {
      const request = selectSavedPreviewRequest(nativeRequests, {
        startIndex: 0, path: `${base}/preview`, receiptId: preview.receiptId, outputId,
      });
      assert(request, `${phase} receipt must be backed by a successful completed native saved Preview request.`);
      const raw = capture.rawResponseBody(request);
      assert.equal(raw?.receiptId, preview.receiptId);
      assert.equal(raw?.outputId, outputId);
      previewBinding = { kind: 'saved-preview', receiptId: preview.receiptId, outputId };
    }
    const rows = await readPreviewRows({ receiptId: preview.receiptId, expectedRows, phase });
    return {
      outputId, snapshotToken: current.catalog.snapshotToken, draftVersion: current.draftVersion,
      draftDigest: current.draftDigest, construction: document.construction ?? { version: 1, steps: [] },
      workspace: current.workspace, preview, previewBinding, rows,
      ...(acceptedChoiceWindow ? { acceptedChoiceWindow } : {}),
    };
  };
  const canceledSavedView = async (before, expectedRows, phase) => {
    const after = await savedView(expectedRows, phase, {
      ...(before.acceptedChoiceWindow ? { acceptedChoiceWindow: before.acceptedChoiceWindow } : {}),
    });
    assert.equal(after.snapshotToken, before.snapshotToken);
    assert.equal(after.draftVersion, before.draftVersion);
    assert.equal(after.draftDigest, before.draftDigest);
    assert(isDeepStrictEqual(after.construction, before.construction), `${phase} must preserve the exact saved construction.`);
    assert(isDeepStrictEqual(after.workspace, before.workspace), `${phase} must preserve the exact saved workspace.`);
    const savedPreviewProof = selectCanceledSavedPreviewRequest(nativeRequests, {
      startIndex: 0, path: `${base}/preview`, outputId, before, after,
    });
    const acceptedChoiceProof = selectCanceledAcceptedChoicePreviewBinding(before.previewBinding, after.previewBinding);
    assert(savedPreviewProof || acceptedChoiceProof,
      `${phase} must restore a native saved receipt or the exact unchanged accepted-choice receipt.`);
    if (savedPreviewProof) assert.equal(capture.rawResponseBody(savedPreviewProof.request)?.receiptId, after.preview.receiptId);
    return { ...after, cancelReceiptSource: savedPreviewProof?.source ?? acceptedChoiceProof.source };
  };
  const openTable = async (expectedRows, phase, { expectedCapabilities } = {}) => {
    const started = Date.now();
    const deadlineAt = started + 5_000;
    const fromIndex = nativeRequests.length;
    await cda.navigate(uiURL);
    await cda.wait(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` }, Math.max(1, deadlineAt - Date.now()));
    await cda.click(`[data-testid="construction-table-${outputId}"]`);
    await cda.wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false,
      [], Math.max(1, deadlineAt - Date.now()));
    let sourceCapabilities;
    if (expectedCapabilities) {
      sourceCapabilities = await waitForSourceCapabilities(capture, {
        fromIndex, deadlineAt, path: `${base}/construction-capabilities`, expected: expectedCapabilities,
      });
      report.finalSourceCapabilities = {
        requestId: sourceCapabilities.requestId, browserRequestId: sourceCapabilities.browserRequestId,
        status: sourceCapabilities.status, completedAt: sourceCapabilities.completedAt,
        outputId: expectedCapabilities.outputId, stageId: expectedCapabilities.stageId,
        draftVersion: expectedCapabilities.draftVersion, draftDigestMatched: true, snapshotMatched: true,
      };
    }
    const rows = await visibleRows(expectedRows);
    builder = await apiBuilder();
    const saved = await savedView(expectedRows, phase);
    checkpoint(phase, started, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
      receiptId: saved.preview.receiptId, rows: rows.length,
      ...(sourceCapabilities ? { capabilitiesRequestId: sourceCapabilities.requestId, capabilityStageId: expectedCapabilities.stageId } : {}) });
    return saved;
  };
  const proposal = async ({ name, started, fromIndex, expectedRows, verifyCandidate }) => {
    const remaining = Math.max(1, 5_000 - (Date.now() - started));
    const entry = await capture.waitFor(candidate => candidate.path === `${base}/construction-proposals` &&
      candidate.method === 'POST' && candidate.status === 200 && candidate.completedAt && candidate.body?.outputId === outputId,
    { fromIndex, timeoutMs: remaining });
    const request = capture.rawRequestBody(entry);
    const response = capture.rawResponseBody(entry);
    assert(request && response, `${name} needs exact native proposal request and response bodies.`);
    assert.equal(response.previewStatus, 'READY', `${name} proposal must be ready.`);
    assert.equal(response.outputId, outputId);
    await cda.wait(({ proposalId }) => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === proposalId,
      { proposalId: response.proposalId }, 5_000);
    await cda.wait(() => ['ready', 'error', 'needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus), [], 5_000);
    const rendered = await cda.inspect(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return {
        status: panel?.dataset.proposalStatus ?? null,
        proposalId: panel?.dataset.proposalId ?? null,
        rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
          .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
      };
    });
    assert.equal(rendered.status, 'ready', `${name} proposal must render successfully.`);
    assert.equal(rendered.proposalId, response.proposalId);
    assert.deepEqual(sortRows(rendered.rows), sortRows(expectedRows), `${name} proposal values must match independent numeric rows.`);
    verifyCandidate?.(request);
    const timing = checkpoint(name, started, { outputId, proposalId: response.proposalId, rows: rendered.rows });
    return { entry, request, response, timing, rows: rendered.rows };
  };
  const applyProposal = async ({ name, expectedRows, expectedConstruction, expectedDigest }) => {
    const started = Date.now();
    const deadlineAt = started + 5_000;
    const fromIndex = nativeRequests.length;
    const remaining = () => Math.max(1, deadlineAt - Date.now());
    const beforeApply = builder;
    await cda.click('[data-testid="construction-apply-proposal"]');
    await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), [], remaining());
    const applyRequest = await capture.waitFor(entry => {
      const body = capture.rawRequestBody(entry) ?? entry.body;
      return entry.path === `${base}/commands` && entry.method === 'POST' && entry.status === 200 &&
        entry.completedAt && entry.failure === undefined && entry.responseReadError === undefined &&
        body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' && command.outputId === outputId);
    }, { fromIndex, timeoutMs: remaining() });
    const applyBody = capture.rawRequestBody(applyRequest);
    const applyResponse = capture.rawResponseBody(applyRequest);
    assert(applyBody && applyResponse, `${name} must capture the exact Apply command and response.`);
    assert.equal(applyBody.snapshotToken, beforeApply.catalog.snapshotToken);
    assert.equal(applyBody.expectedDraftVersion, beforeApply.draftVersion);
    assert.equal(applyBody.expectedDraftDigest, beforeApply.draftDigest);
    assert(Number.isSafeInteger(applyResponse.draftVersion));
    assert.equal(typeof applyResponse.draftDigest, 'string');
    await cda.wait(({ outputId: expectedOutput, draftVersion, draftDigest }) => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput &&
        preview.dataset.currentDraftVersion === String(draftVersion) && preview.dataset.currentDraftDigest === draftDigest &&
        Boolean(preview.dataset.previewReceiptId);
    }, { outputId, draftVersion: applyResponse.draftVersion, draftDigest: applyResponse.draftDigest }, remaining());
    const activePreview = await previewDom();
    const previewRequest = await capture.waitFor(entry => {
      const request = capture.rawRequestBody(entry) ?? entry.body;
      const response = capture.rawResponseBody(entry) ?? entry.response;
      return entry.path === `${base}/preview` && entry.method === 'POST' && entry.status === 200 &&
        entry.completedAt && entry.failure === undefined && entry.responseReadError === undefined &&
        request?.receiptId === activePreview.receiptId && request?.outputId === outputId &&
        response?.receiptId === activePreview.receiptId && response?.outputId === outputId;
    }, { fromIndex, timeoutMs: remaining() });
    await visibleRows(expectedRows);
    builder = await apiBuilder();
    assert.equal(builder.draftVersion, applyResponse.draftVersion, `${name} builder must retain the exact Apply successor version.`);
    assert.equal(builder.draftDigest, applyResponse.draftDigest, `${name} builder must retain the exact Apply successor digest.`);
    const document = documentFor(builder);
    assert(document);
    assert(isDeepStrictEqual(document.construction, expectedConstruction), `${name} must persist the exact accepted construction.`);
    if (expectedDigest) assert.equal(builder.draftDigest, expectedDigest, `${name} draft digest must match the accepted proposal.`);
    const saved = await savedView(expectedRows, name);
    assert.equal(saved.preview.receiptId, activePreview.receiptId, `${name} saved Preview receipt must match the visible current-draft receipt.`);
    assert.equal(saved.preview.draftVersion, String(applyResponse.draftVersion));
    assert.equal(saved.preview.draftDigest, applyResponse.draftDigest);
    const exactPreviewRequest = selectSavedPreviewRequest(nativeRequests, {
      startIndex: fromIndex, path: `${base}/preview`, receiptId: activePreview.receiptId, outputId,
    });
    assert.equal(exactPreviewRequest, previewRequest, `${name} must wait for the exact receipt-bound Preview request it later verifies.`);
    checkpoint(name, started, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
      receiptId: saved.preview.receiptId, rows: expectedRows.length });
    return { builder, document, saved, applyRequest, applyBody, applyResponse, previewRequest };
  };
  const cancelProposal = async ({ name, before, expectedRows, groupSourceOptionsCancel = false }) => {
    const started = Date.now();
    const deadlineAt = started + 5_000;
    const remaining = () => Math.max(1, deadlineAt - Date.now());
    let pendingGroupOptions;
    if (groupSourceOptionsCancel) {
      const candidates = [...capture.byRequest.entries()].filter(([request, entry]) =>
        matchesExactGroupSourceOptionsRequest(request, {
          uiOrigin, project, explorer, outputId, snapshotToken: before.snapshotToken,
        }) && entry.startedAt <= started && !requestTerminalEvents.has(request));
      assert(candidates.length <= 1, 'Group Cancel may retire at most one exact pending Observation source-options request.');
      pendingGroupOptions = candidates[0];
    }
    const expectedCancellationStart = report.expectedCancellations?.length ?? 0;
    const cancelStartedAt = Date.now();
    await cda.click('[data-testid="construction-cancel-proposal"]', {}, remaining());
    await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), [], remaining());
    if (pendingGroupOptions) {
      const [request, entry] = pendingGroupOptions;
      const terminal = await waitForRequestTerminal(request, deadlineAt);
      assert(terminal.at <= deadlineAt, 'The exact Group source-options request must terminate within the Cancel action deadline.');
      if (terminal.kind === 'failed' && terminal.at >= cancelStartedAt) {
        assert.equal(entry.failure, 'net::ERR_ABORTED', 'Only the exact observed native AbortController cancellation may be classified.');
        assert(hasCancellableGroupSourceOptionsResponseState(entry),
          'The exact Group cancellation must be terminal ERR_ABORTED either before response headers or after HTTP 200; other response states remain fatal.');
        assert(matchesExactGroupSourceOptionsRequest(request, {
          uiOrigin, project, explorer, outputId, snapshotToken: before.snapshotToken,
        }), 'The terminal request must still match its captured project, Explorer, output, snapshot, resource type, and limit.');
        const diagnostic = (report.network ?? []).find(item =>
          item.browserRequestId === entry.browserRequestId && item.errorText === 'net::ERR_ABORTED');
        assert(diagnostic, 'The exact failed request must have one retained native failure diagnostic.');
        assert.equal(diagnostic.requestAction?.label, entry.triggerAction,
          'The source-options request must retain the exact action that opened its Group owner.');
        assert.match(diagnostic.requestAction?.label ?? '', /^Combine rows into groups\b/,
          'The request must originate in the native Group editor.');
        assert.equal(diagnostic.failureAction?.label, 'Cancel',
          'The exact request failure must occur during the explicit Group proposal Cancel action.');
        cda.expectCapturedCancellation(entry,
          'Group Cancel retires its exact pending GroupCodedValuePicker source-options request.', {
            action: 'Group Cancel', project, explorer, outputId, snapshotToken: before.snapshotToken,
            request: { method: 'POST', resourceType: 'Observation', limit: 50 },
            owner: 'ConstructionReshapeEditor GroupCodedValuePicker AbortController on Cancel unmount',
            requestAction: diagnostic.requestAction, failureAction: diagnostic.failureAction,
            responseStatus: entry.status ?? null, responseReceivedAt: entry.responseReceivedAt ?? null,
          });
      } else {
        assert.equal(terminal.kind, 'finished',
          'A pre-Cancel exact request may finish normally; failures outside the Cancel action remain unexpected.');
        assert.equal(entry.status, 200);
      }
    }
    assert.equal((report.expectedCancellations ?? []).length - expectedCancellationStart,
      pendingGroupOptions && requestTerminalEvents.get(pendingGroupOptions[0])?.kind === 'failed' ? 1 : 0,
      'Group Cancel may classify only the exact pre-captured Group source-options request.');
    if (groupSourceOptionsCancel) removeNativeRequestTerminalListeners();
    const rows = await visibleRows(expectedRows);
    const after = await canceledSavedView(before, expectedRows, name);
    checkpoint(name, started, { draftDigest: after.draftDigest, cancelReceiptSource: after.cancelReceiptSource, rows: rows.length });
    return after;
  };
  const rawIdRows = witness.left.map(row => [row.id]);
  const initialSourceView = await openTable(rawIdRows, 'initial exact Observation rows render');
  const rawFieldsStart = Date.now();
  const rawFieldsRequestStartIndex = nativeRequests.length;
  const rawFieldsBase = {
    snapshotToken: initialSourceView.snapshotToken, draftVersion: initialSourceView.draftVersion,
    draftDigest: initialSourceView.draftDigest, outputId,
  };
  await cda.click('[data-testid="construction-action-add-columns"]');
  await cda.wait(() => Boolean(document.querySelector('[aria-label="Add columns editor"]')), [], 5_000);
  await cda.click('button', { name: 'Fields and related data', exact: true });
  await cda.click('summary', { name: 'Raw FHIR fields (advanced)', exact: true });
  const subjectSelector = 'input[type="checkbox"][aria-label="Select Observation.subject.reference"]';
  await cda.wait(({ selector }) => Boolean(document.querySelector(selector)), { selector: subjectSelector }, 5_000);
  await cda.click(subjectSelector);
  await cda.click('button', { name: 'Add 1 selected feature', exact: true });
  await cda.wait(() => [...document.querySelectorAll('button')]
    .some(button => button.innerText.trim() === 'Apply columns' && !button.disabled), [], 5_000);
  await cda.click('button', { name: 'Apply columns', exact: true });
  await cda.click('button', { name: 'Close operation editor', exact: true });
  await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')), [], 5_000);
  const sourceBuilder = await apiBuilder();
  builder = sourceBuilder;
  const sourceDocument = documentFor(sourceBuilder);
  const sourceSubjectColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'subject.reference' && column.source.field.projectionMode === 'VALUE');
  const sourceIdColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'id' && column.source.field.projectionMode === 'VALUE');
  const sourceColumnIds = authoredColumnIds(sourceDocument.columns);
  assert(sourceSubjectColumn && sourceIdColumn && sourceColumnIds?.length === 2,
    'The exact Observation subject.reference and ID projections must have distinct saved V2 columnId identities.');
  const sourceRows = witness.left.map(row => sourceDocument.columns.map(column => {
    const path = column.source?.field?.path;
    if (path === 'id') return row.id;
    if (path === 'subject.reference') return row.subjectReference;
    throw new Error(`Unexpected source column in numeric Group fixture: ${path}`);
  }));
  const renderedSourceRows = await visibleRows(sourceRows);
  const sourceChoiceWindow = { startIndex: rawFieldsRequestStartIndex, before: rawFieldsBase };
  const sourceSaved = await savedView(sourceRows, 'source fields saved before Group', { acceptedChoiceWindow: sourceChoiceWindow });
  checkpoint('add exact subject.reference Group key', rawFieldsStart, { sourceRows: sourceRows.length,
    sourceColumnIds: [sourceIdColumn.columnId, sourceSubjectColumn.columnId], receiptId: sourceSaved.preview.receiptId });
  check('correctness', 'Native Observation source rows match the exact selected raw records',
    isDeepStrictEqual(sortRows(renderedSourceRows), sortRows(sourceRows)), {
      selectionId: selection.id, selectionScopeDigest: selection.scopeDigest,
      selectedIds: witness.left.map(row => row.id), columns: sourceDocument.columns.map(column => ({ columnId: column.columnId, path: column.source?.field?.path })),
      expectedRows: sourceRows, actualRows: renderedSourceRows,
    });

  const configureGroup = async () => {
    const started = Date.now();
    const fromIndex = nativeRequests.length;
    await cda.click('[data-testid="construction-rows-settings-trigger"]');
    await cda.wait(() => document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled === false, [], 5_000);
    await cda.click('[data-testid="construction-action-group-rows"]');
    await cda.wait(() => document.querySelector('select[aria-label="Summary 1"]')?.disabled === false, [], 5_000);
    const summary = await cda.inspect(() => document.querySelector('select[aria-label="Summary 1"]')?.value ?? null);
    assert.equal(summary, 'COUNT_ROWS', 'Numeric Group fixture must use the native COUNT_ROWS aggregate.');
    const selector = `input[type="checkbox"][aria-label=${JSON.stringify(`Group by ${sourceSubjectColumn.label}`)}]`;
    await cda.wait(({ selector: target }) => Boolean(document.querySelector(target) && !document.querySelector(target).disabled), { selector }, 5_000);
    await cda.click(selector);
    return { started, fromIndex };
  };
  const beforeGroup = await savedView(sourceRows, 'before Group proposal', { acceptedChoiceWindow: sourceChoiceWindow });
  let groupWindow = await configureGroup();
  const groupProposal = await proposal({
    name: 'native Group COUNT_ROWS preview matches exact 3/1 raw numeric tuples',
    started: groupWindow.started, fromIndex: groupWindow.fromIndex, expectedRows: oracle.groupRows,
    verifyCandidate: request => {
      report.groupCandidate = validateCdaGroupCandidate({
        candidateConstruction: request.candidateConstruction, sourceSubjectColumn,
      });
    },
  });
  check('correctness', 'Native Group by subject.reference previews exact COUNT_ROWS values 3 and 1', true, {
    proposalId: groupProposal.response.proposalId, expectedRows: oracle.groupRows,
    candidate: report.groupCandidate,
  });
  await cancelProposal({ name: 'Group Cancel restores exact four Observation rows and empty construction', before: beforeGroup,
    expectedRows: sourceRows, groupSourceOptionsCancel: true });
  const canceledGroupBuilder = await apiBuilder();
  check('persistence', 'Group Cancel preserves the exact four-record source draft and rows',
    isDeepStrictEqual(documentFor(canceledGroupBuilder).construction?.steps ?? [], []) &&
      isDeepStrictEqual(canceledGroupBuilder.workspace, beforeGroup.workspace), {
      draftVersion: canceledGroupBuilder.draftVersion, draftDigest: canceledGroupBuilder.draftDigest,
      selectionId: selection.id, receiptId: beforeGroup.preview.receiptId,
    });

  groupWindow = await configureGroup();
  const confirmedGroupProposal = await proposal({
    name: 'confirmed Group COUNT_ROWS preview matches exact 3/1 raw numeric tuples',
    started: groupWindow.started, fromIndex: groupWindow.fromIndex, expectedRows: oracle.groupRows,
  });
  let groupStep;
  let countOutput;
  const groupApplied = await applyProposal({
    name: 'Apply Group persists exact 3/1 COUNT_ROWS tuples', expectedRows: oracle.groupRows,
    expectedConstruction: confirmedGroupProposal.request.candidateConstruction,
    expectedDigest: confirmedGroupProposal.response.candidateWorkspaceDigest,
  });
  const appliedGroupDocument = documentFor(groupApplied.builder);
  groupStep = appliedGroupDocument.construction.steps.at(-1);
  assert.equal(groupStep.operation.kind, 'GROUP');
  const aggregate = groupStep.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
  assert(aggregate);
  countOutput = groupStep.outputs.find(output => output.id === aggregate.outputColumnId);
  assert(countOutput && countOutput.type === 'integer');
  assert.deepEqual(appliedGroupDocument.construction.steps.map(step => step.operation.kind), ['GROUP']);
  check('persistence', 'Applied Group persists the exact 3/1 integer COUNT_ROWS binding', true, {
    outputId, groupStepId: groupStep.id, countColumnId: countOutput.id, countColumnType: countOutput.type,
    countColumnLabel: countOutput.label, expectedRows: oracle.groupRows,
    draftVersion: groupApplied.builder.draftVersion, draftDigest: groupApplied.builder.draftDigest,
  });
  await openTable(oracle.groupRows, 'Group reload restores exact count values 3 and 1');
  check('persistence', 'Group COUNT_ROWS rows survive reload with exact values and stable output identity', true, {
    outputId, groupStepId: groupStep.id, countColumnId: countOutput.id, expectedRows: oracle.groupRows,
    draftDigest: builder.draftDigest,
  });

  const filterColumnCapability = async deadlineAt => {
    const expected = {
      snapshotToken: builder.catalog.snapshotToken,
      draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest,
      outputId,
      stageId: groupStep.id,
    };
    const entry = await waitForSourceCapabilities(capture, {
      fromIndex: 0, deadlineAt, path: `${base}/construction-capabilities`, expected,
    });
    const body = capture.rawRequestBody(entry);
    const response = capture.rawResponseBody(entry);
    assert.equal(body.outputId, outputId);
    assert.equal(body.stageId, groupStep.id, 'Filter capability discovery must use the Group stage.');
    assert.equal(body.snapshotToken, expected.snapshotToken);
    assert.equal(body.expectedDraftVersion, expected.draftVersion);
    assert.equal(body.expectedDraftDigest, expected.draftDigest);
    assert.equal(response.selectedStage?.id, groupStep.id);
    const column = response.selectedStage.columns.find(item => item.id === countOutput.id);
    assert(column, 'The exact COUNT_ROWS output must be available to native Filter capabilities.');
    assert.equal(column.type, 'integer');
    const option = await cda.inspect(({ selector, value }) => {
      const select = document.querySelector(selector);
      const matches = [...(select?.options ?? [])].filter(item => item.value === value);
      return {
        count: matches.length,
        option: matches.map(item => ({ value: item.value, label: item.textContent.trim(), disabled: item.disabled })),
      };
    }, {
      selector: '[data-testid="construction-filter-editor"] select[aria-label="Column"]',
      value: countOutput.id,
    });
    assert.deepEqual(option, {
      count: 1,
      option: [{ value: countOutput.id, label: `${countOutput.label} (${countOutput.type})`, disabled: false }],
    }, 'Native Filter must render the exact integer COUNT_ROWS option from the bound Group capabilities.');
    return { entry, response, column, option: option.option[0] };
  };
  const configureFilter = async ({ name, threshold, editingStepId, expectedSavedThreshold }) => {
    const started = Date.now();
    const deadlineAt = started + 5_000;
    const fromIndex = nativeRequests.length;
    if (editingStepId) {
      await cda.click(`[data-testid="construction-history-step-${editingStepId}"]`, {}, Math.max(1, deadlineAt - Date.now()));
      await cda.click(`[data-testid="construction-edit-step-${editingStepId}"]`, {}, Math.max(1, deadlineAt - Date.now()));
    } else await cda.click('[data-testid="construction-action-keep-rows"]', {}, Math.max(1, deadlineAt - Date.now()));
    await cda.wait(() => document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]')?.disabled === false,
      [], Math.max(1, deadlineAt - Date.now()));
    if (editingStepId) {
      const reopened = await cda.inspect(() => ({
        columnId: document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]')?.value ?? null,
        operator: document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]')?.value ?? null,
        value: document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]')?.value ?? null,
      }));
      assert.equal(reopened.columnId, countOutput.id, 'Saved numeric Filter edit must reopen the same COUNT_ROWS output identity.');
      assert.equal(reopened.operator, 'GT', 'Saved numeric Filter edit must reopen the exact comparison.');
      assert.equal(reopened.value, String(expectedSavedThreshold), 'Saved numeric Filter edit must reopen its exact integer threshold.');
      report.reopenedFilter = { ...reopened, stepId: editingStepId, groupStepId: groupStep.id };
    }
    const capabilities = await filterColumnCapability(deadlineAt);
    await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Column"]', countOutput.id,
      { timeout: Math.max(1, deadlineAt - Date.now()) });
    await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'GT',
      { timeout: Math.max(1, deadlineAt - Date.now()) });
    const valueSelector = '[data-testid="construction-filter-editor"] input[aria-label="Value"]';
    await cda.wait(({ selector }) => Boolean(document.querySelector(selector) && !document.querySelector(selector).disabled),
      { selector: valueSelector }, Math.max(1, deadlineAt - Date.now()));
    await cda.fill(valueSelector, String(threshold), {}, Math.max(1, deadlineAt - Date.now()));
    const expected = prepareCdaGroupNumericFilterOracle(witness, {
      project, generation, operator: 'GT', threshold, requireStrictSubset: threshold !== 3,
    });
    const result = await proposal({
      name, started, fromIndex, expectedRows: expected.filteredRows,
      verifyCandidate: request => {
        const construction = request.candidateConstruction;
        const steps = construction?.steps ?? [];
        assert.equal(steps.length, 2);
        assert.deepEqual(steps.map(step => step.operation.kind), ['GROUP', 'FILTER']);
        const savedGroup = steps[0];
        const filter = steps[1];
        assert.equal(savedGroup.id, groupStep.id, 'Filter proposal must retain the exact saved Group step.');
        assert.deepEqual(filter.inputs, [{ kind: 'STEP_OUTPUT', stepId: groupStep.id }],
          'Numeric Filter must consume the current Group output as a STEP_OUTPUT.');
        assert.equal(filter.operation.kind, 'FILTER');
        assert.equal(filter.operation.filter.columnId, countOutput.id);
        assert.equal(filter.operation.filter.operator, 'GT');
        assert.deepEqual(filter.operation.filter.values, [{ kind: 'INTEGER', integer: threshold }]);
        assert.deepEqual(filter.outputs, savedGroup.outputs, 'Row filtering must preserve the exact Group output columns.');
        if (editingStepId) assert.equal(request.changedStepId, editingStepId);
        report.filterCandidate = { groupStepId: savedGroup.id, filterStepId: filter.id,
          input: filter.inputs[0], countColumnId: filter.operation.filter.columnId,
          operator: filter.operation.filter.operator, value: filter.operation.filter.values[0], outputs: filter.outputs };
      },
    });
    return { ...result, expected, capabilities };
  };

  const groupSavedView = await savedView(oracle.groupRows, 'before numeric Filter');
  const firstFilter = await configureFilter({ name: 'native numeric GT 1 previews exactly the count-3 Group row', threshold: 1 });
  check('usability', 'Native Filter selects the Group integer COUNT_ROWS output', true, {
    groupStepId: groupStep.id, countColumnId: countOutput.id, countColumnType: countOutput.type,
    capabilityStageId: firstFilter.capabilities.response.selectedStage.id,
    capabilityColumnType: firstFilter.capabilities.column.type,
  });
  check('correctness', 'Numeric GT 1 proposal returns exactly the count-3 Group row', true, {
    expectedRows: oracle.filteredRows, actualRows: firstFilter.rows, filter: report.filterCandidate,
  });
  const afterFilterCancel = await cancelProposal({
    name: 'Filter Cancel restores exact 3/1 Group rows and saved Group construction',
    before: groupSavedView, expectedRows: oracle.groupRows,
  });
  check('persistence', 'Filter Cancel preserves the exact Group construction and 3/1 rows', true, {
    groupStepId: groupStep.id, countColumnId: countOutput.id, receiptId: afterFilterCancel.preview.receiptId,
    cancelReceiptSource: afterFilterCancel.cancelReceiptSource, expectedRows: oracle.groupRows,
  });

  const secondFilter = await configureFilter({ name: 'confirmed numeric GT 1 applies exact count-3 subset', threshold: 1 });
  const filterApplied = await applyProposal({
    name: 'Apply numeric GT 1 persists exact count-3 Group row', expectedRows: oracle.filteredRows,
    expectedConstruction: secondFilter.request.candidateConstruction,
    expectedDigest: secondFilter.response.candidateWorkspaceDigest,
  });
  const filteredDocument = documentFor(filterApplied.builder);
  const filterStep = filteredDocument.construction.steps.at(-1);
  assert.equal(filterStep.operation.kind, 'FILTER');
  assert.deepEqual(filterStep.inputs, [{ kind: 'STEP_OUTPUT', stepId: groupStep.id }]);
  assert.equal(filterStep.operation.filter.columnId, countOutput.id);
  assert.deepEqual(filterStep.operation.filter.values, [{ kind: 'INTEGER', integer: 1 }]);
  check('persistence', 'Applied numeric GT 1 Filter retains exactly the count-3 Group row', true, {
    groupStepId: groupStep.id, filterStepId: filterStep.id, countColumnId: countOutput.id,
    input: filterStep.inputs, operator: filterStep.operation.filter.operator,
    value: filterStep.operation.filter.values[0], expectedRows: oracle.filteredRows,
  });
  await openTable(oracle.filteredRows, 'numeric Filter reload preserves the count-3 row');
  check('persistence', 'Filtered Group row survives reload with exact count and stable bindings', true, {
    groupStepId: groupStep.id, filterStepId: filterStep.id, countColumnId: countOutput.id,
    filterInput: filterStep.inputs[0], expectedRows: oracle.filteredRows, draftDigest: builder.draftDigest,
  });

  const filteredSavedView = await savedView(oracle.filteredRows, 'before saved Filter edit');
  const editToEmpty = await configureFilter({
    name: 'saved Filter GT 3 edit previews the exact empty result', threshold: 3,
    editingStepId: filterStep.id, expectedSavedThreshold: 1,
  });
  check('persistence', 'Saved Filter edit preserves Group STEP_OUTPUT and integer COUNT_ROWS column identity', true, {
    groupStepId: groupStep.id, filterStepId: filterStep.id, countColumnId: countOutput.id,
    reopened: report.reopenedFilter, editCandidate: report.filterCandidate,
  });
  check('correctness', 'Numeric GT 3 edit preview returns no Group rows', editToEmpty.rows.length === 0, {
    groupRows: oracle.groupRows, operator: 'GT', threshold: 3, expectedRows: [], actualRows: editToEmpty.rows,
  });
  const afterEditCancel = await cancelProposal({
    name: 'GT 3 edit Cancel preserves the saved GT 1 Filter and count-3 row',
    before: filteredSavedView, expectedRows: oracle.filteredRows,
  });
  check('persistence', 'GT 3 edit Cancel preserves saved GT 1 Filter and exact filtered row', true, {
    filterStepId: filterStep.id, savedOperator: 'GT', savedValue: 1,
    receiptId: afterEditCancel.preview.receiptId, cancelReceiptSource: afterEditCancel.cancelReceiptSource,
    expectedRows: oracle.filteredRows,
  });
  const appliedThresholdEdit = await configureFilter({
    name: 'saved Filter GT 2 edit applies exact count-3 row', threshold: 2,
    editingStepId: filterStep.id, expectedSavedThreshold: 1,
  });
  const editedApply = await applyProposal({
    name: 'Apply numeric GT 2 Filter preserves exact count-3 row', expectedRows: oracle.filteredRows,
    expectedConstruction: appliedThresholdEdit.request.candidateConstruction,
    expectedDigest: appliedThresholdEdit.response.candidateWorkspaceDigest,
  });
  const editedFilter = documentFor(editedApply.builder).construction.steps.at(-1);
  assert.equal(editedFilter.id, filterStep.id);
  assert.deepEqual(editedFilter.inputs, [{ kind: 'STEP_OUTPUT', stepId: groupStep.id }]);
  assert.deepEqual(editedFilter.operation.filter.values, [{ kind: 'INTEGER', integer: 2 }]);
  await openTable(oracle.filteredRows, 'saved numeric GT 2 Filter reload preserves count-3 row');
  check('persistence', 'Edited numeric GT 2 Filter applies and reloads the exact count-3 row', true, {
    groupStepId: groupStep.id, filterStepId: editedFilter.id, countColumnId: countOutput.id,
    operator: editedFilter.operation.filter.operator, value: editedFilter.operation.filter.values[0],
    expectedRows: oracle.filteredRows, draftDigest: builder.draftDigest,
  });

  const removeFilterStarted = Date.now();
  const removeFilterFromIndex = nativeRequests.length;
  await cda.click(`[data-testid="construction-history-step-${filterStep.id}"]`);
  await cda.click(`[data-testid="construction-remove-step-${filterStep.id}"]`);
  const removeFilterProposal = await proposal({
    name: 'Filter removal previews the exact 3/1 Group rows', started: removeFilterStarted,
    fromIndex: removeFilterFromIndex, expectedRows: oracle.groupRows,
    verifyCandidate: request => {
      const steps = request.candidateConstruction?.steps ?? [];
      assert.deepEqual(steps.map(step => step.operation.kind), ['GROUP']);
      assert.equal(steps[0].id, groupStep.id);
    },
  });
  const filterRemoved = await applyProposal({
    name: 'Filter removal restores exact 3/1 Group rows', expectedRows: oracle.groupRows,
    expectedConstruction: removeFilterProposal.request.candidateConstruction,
    expectedDigest: removeFilterProposal.response.candidateWorkspaceDigest,
  });
  await openTable(oracle.groupRows, 'Filter removal reload restores exact 3/1 Group rows');
  check('persistence', 'Filter removal restores exact 3/1 Group construction and rows after reload',
    documentFor(builder).construction.steps.length === 1 && documentFor(builder).construction.steps[0].id === groupStep.id,
    { groupStepId: groupStep.id, filterStepId: filterStep.id, expectedRows: oracle.groupRows,
      actualConstruction: documentFor(builder).construction, draftDigest: builder.draftDigest,
      removalDigest: filterRemoved.builder.draftDigest });

  const removeGroupStarted = Date.now();
  const removeGroupFromIndex = nativeRequests.length;
  await cda.click(`[data-testid="construction-history-step-${groupStep.id}"]`);
  await cda.click(`[data-testid="construction-remove-step-${groupStep.id}"]`);
  const removeGroupProposal = await proposal({
    name: 'Group removal previews exact four raw Observation records', started: removeGroupStarted,
    fromIndex: removeGroupFromIndex, expectedRows: sourceRows,
    verifyCandidate: request => assert.deepEqual(request.candidateConstruction?.steps ?? [], []),
  });
  const groupRemoved = await applyProposal({
    name: 'Group removal restores exact raw Observation rows', expectedRows: sourceRows,
    expectedConstruction: removeGroupProposal.request.candidateConstruction,
    expectedDigest: removeGroupProposal.response.candidateWorkspaceDigest,
  });
  const finalSourceIdentity = {
    snapshotToken: groupRemoved.builder.catalog.snapshotToken,
    draftVersion: groupRemoved.builder.draftVersion,
    draftDigest: groupRemoved.builder.draftDigest,
    outputId, stageId: 'source_projection',
  };
  assert(finalSourceIdentity.snapshotToken && Number.isSafeInteger(finalSourceIdentity.draftVersion) && finalSourceIdentity.draftDigest);
  await openTable(sourceRows, 'Group removal reload restores exact four raw Observation records', {
    expectedCapabilities: finalSourceIdentity,
  });
  const restored = documentFor(builder);
  const restoredColumnIds = authoredColumnIds(restored.columns);
  check('persistence', 'Group removal restores exact source construction, columns, and four raw rows after reload',
    restored.construction.steps.length === 0 && hasExactAuthoredColumnRestoration(sourceDocument.columns, restored.columns), {
      outputId, removedGroupStepId: groupStep.id, expectedRows: sourceRows,
      actualColumns: restored.columns.map(column => ({ columnId: column.columnId, column: column.column, path: column.source?.field?.path })),
      actualColumnIds: restoredColumnIds, expectedColumnIds: sourceColumnIds,
      draftDigest: builder.draftDigest, removalDigest: groupRemoved.builder.draftDigest,
    });

  const unfinished = nativeRequests.flatMap((entry, index) =>
    Number.isFinite(entry.completedAt) || entry.failure ? [] : [{ index, path: entry.path, requestId: entry.requestId }]);
  const nonSuccess = nativeRequests.flatMap((entry, index) =>
    (entry.status === 200 && Number.isFinite(entry.completedAt) && entry.failure === undefined) || isExactGroupSourceOptionsCancellation(entry, {
      uiOrigin, project, explorer, outputId, snapshotToken: beforeGroup.snapshotToken,
    })
    ? [] : [{ index, path: entry.path, status: entry.status ?? null, failure: entry.failure ?? null, completedAt: entry.completedAt ?? null }]);
  const unexpectedErrors = [...(report.errors ?? []), ...(report.network ?? [])].filter(entry =>
    !entry.expectedHttpFailure && !entry.expected && !entry.expectedCancellation && !entry.expectedInjectedFault &&
    (entry.kind === 'console' || entry.kind === 'runtime' || entry.kind === 'network' || entry.kind === 'http' || entry.status >= 400 || entry.errorText));
  check('correctness', 'No unexpected native, browser, transport, or HTTP errors occur',
    unfinished.length === 0 && nonSuccess.length === 0 && unexpectedErrors.length === 0, {
      nativeRequestCount: nativeRequests.length, unfinished, nonSuccess,
      unexpectedErrors: unexpectedErrors.map(entry => ({ kind: entry.kind, path: entry.path, status: entry.status, message: entry.message, error: entry.error })),
    });
  const actions = report.actions ?? [];
  const badActions = actions.filter(action => action.status !== 'passed' || !Number.isFinite(action.elapsedMs) || action.elapsedMs < 0 || action.elapsedMs > 5_000);
  const checkpointDurations = checkpoints.map(item => item.durationMs);
  const performanceEvidence = {
    workflowCheckpoints: checkpoints,
    workflowCheckpointCount: checkpoints.length,
    maximumWorkflowCheckpointMs: checkpointDurations.length ? Math.max(...checkpointDurations) : null,
    nativeActionCount: actions.length,
    maximumNativeActionMs: actions.length ? Math.max(...actions.map(action => action.elapsedMs ?? 0)) : null,
    badActions, budgetMs: 5_000,
  };
  check('performance', 'All native Group and Filter actions and render transitions complete within five seconds',
    checkpoints.length > 0 && checkpointDurations.every(duration => Number.isFinite(duration) && duration >= 0 && duration <= 5_000) &&
      actions.length > 0 && badActions.length === 0, performanceEvidence);
  report.terminalRequestCensus = { total: nativeRequests.length, complete: nativeRequests.length - unfinished.length,
    status200Completed: nativeRequests.filter(entry => entry.status === 200 && Number.isFinite(entry.completedAt) && entry.failure === undefined).length,
    expectedCancellations: nativeRequests.filter(entry => isExactGroupSourceOptionsCancellation(entry, {
      uiOrigin, project, explorer, outputId, snapshotToken: beforeGroup.snapshotToken,
    })).length,
    nonSuccess };
  report.finish = { outputId, sourceSelectionId: selection.id, groupStepId: groupStep.id,
    countColumnId: countOutput.id, filterStepId: filterStep.id, expectedGroupRows: oracle.groupRows,
    expectedGtOneRows: oracle.filteredRows, restoredRawRows: sourceRows };
  await cda.attachReport('cda-group-numeric-filter-workflow', report);
}
